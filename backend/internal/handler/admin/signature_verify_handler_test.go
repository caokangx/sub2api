//go:build unit

package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type signatureVerifyFakeUpstream struct {
	req        *http.Request
	reqBody    []byte
	status     int
	respBody   string
	doErr      error
	callsCount int
}

func (f *signatureVerifyFakeUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	f.callsCount++
	f.req = req
	if req.Body != nil {
		f.reqBody, _ = io.ReadAll(req.Body)
	}
	if f.doErr != nil {
		return nil, f.doErr
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader(f.respBody)),
		Header:     http.Header{},
	}, nil
}

func (f *signatureVerifyFakeUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(req, proxyURL, accountID, concurrency)
}

var _ service.HTTPUpstream = (*signatureVerifyFakeUpstream)(nil)

type signatureVerifyEnvelope struct {
	Code    int                             `json:"code"`
	Message string                          `json:"message"`
	Data    SignatureVerifyMessagesResponse `json:"data"`
}

func runSignatureVerify(t *testing.T, cfg *config.Config, up *signatureVerifyFakeUpstream, payload any) (*httptest.ResponseRecorder, signatureVerifyEnvelope) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/verify", NewSignatureVerifyHandler(cfg, up).Messages)

	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	var env signatureVerifyEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w, env
}

func TestSignatureVerifyForwardsRequest(t *testing.T) {
	up := &signatureVerifyFakeUpstream{status: http.StatusOK, respBody: `{"content":[{"type":"text","text":"Done."}]}`}
	w, env := runSignatureVerify(t, &config.Config{}, up, map[string]any{
		"base_url": "https://gateway.example.com/",
		"api_key":  "  sk-test  ",
		"body":     map[string]any{"model": "claude-sonnet-4-6", "max_tokens": 16},
	})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 0, env.Code)
	require.Equal(t, http.StatusOK, env.Data.Status)
	require.JSONEq(t, up.respBody, string(env.Data.Body))

	require.Equal(t, "https://gateway.example.com/v1/messages", up.req.URL.String())
	require.Equal(t, "sk-test", up.req.Header.Get("x-api-key"))
	require.Equal(t, "2023-06-01", up.req.Header.Get("anthropic-version"))
	require.JSONEq(t, `{"model":"claude-sonnet-4-6","max_tokens":16}`, string(up.reqBody))
	require.True(t, service.HTTPUpstreamRedirectsDisabled(up.req.Context()))
	require.True(t, service.HTTPUpstreamPublicHostsOnly(up.req.Context()))
}

func TestSignatureVerifyWrapsUpstreamErrorStatus(t *testing.T) {
	// An upstream 401 must not surface as an HTTP 401 (that would look like an expired admin session).
	up := &signatureVerifyFakeUpstream{status: http.StatusUnauthorized, respBody: `{"error":{"message":"invalid x-api-key"}}`}
	w, env := runSignatureVerify(t, &config.Config{}, up, map[string]any{
		"base_url": "https://gateway.example.com",
		"api_key":  "bad",
		"body":     map[string]any{"model": "m"},
	})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, http.StatusUnauthorized, env.Data.Status)
	require.Contains(t, string(env.Data.Body), "invalid x-api-key")
}

func TestSignatureVerifyNonJSONUpstream(t *testing.T) {
	up := &signatureVerifyFakeUpstream{status: http.StatusBadGateway, respBody: "<html>bad gateway</html>"}
	_, env := runSignatureVerify(t, &config.Config{}, up, map[string]any{
		"base_url": "https://gateway.example.com",
		"api_key":  "k",
		"body":     map[string]any{"model": "m"},
	})

	require.Equal(t, http.StatusBadGateway, env.Data.Status)
	require.Empty(t, env.Data.Body)
	require.Equal(t, "<html>bad gateway</html>", env.Data.Text)
}

func TestSignatureVerifyRejectsBadInput(t *testing.T) {
	cases := map[string]map[string]any{
		"private host":    {"base_url": "https://127.0.0.1:8080", "api_key": "k", "body": map[string]any{"model": "m"}},
		"localhost":       {"base_url": "https://localhost", "api_key": "k", "body": map[string]any{"model": "m"}},
		"insecure http":   {"base_url": "http://gateway.example.com", "api_key": "k", "body": map[string]any{"model": "m"}},
		"bad scheme":      {"base_url": "ftp://gateway.example.com", "api_key": "k", "body": map[string]any{"model": "m"}},
		"blank key":       {"base_url": "https://gateway.example.com", "api_key": "   ", "body": map[string]any{"model": "m"}},
		"body not object": {"base_url": "https://gateway.example.com", "api_key": "k", "body": []any{1, 2}},
		"missing body":    {"base_url": "https://gateway.example.com", "api_key": "k"},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			up := &signatureVerifyFakeUpstream{status: http.StatusOK, respBody: `{}`}
			w, _ := runSignatureVerify(t, &config.Config{}, up, payload)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Zero(t, up.callsCount)
		})
	}
}

func TestSignatureVerifyAllowsPrivateHostsWhenConfigured(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	up := &signatureVerifyFakeUpstream{status: http.StatusOK, respBody: `{}`}
	w, _ := runSignatureVerify(t, cfg, up, map[string]any{
		"base_url": "http://127.0.0.1:8080",
		"api_key":  "k",
		"body":     map[string]any{"model": "m"},
	})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, up.callsCount)
	require.False(t, service.HTTPUpstreamPublicHostsOnly(up.req.Context()))
}

func TestSignatureVerifyUpstreamFailure(t *testing.T) {
	up := &signatureVerifyFakeUpstream{doErr: io.ErrUnexpectedEOF}
	w, _ := runSignatureVerify(t, &config.Config{}, up, map[string]any{
		"base_url": "https://gateway.example.com",
		"api_key":  "k",
		"body":     map[string]any{"model": "m"},
	})
	require.Equal(t, http.StatusBadGateway, w.Code)
}

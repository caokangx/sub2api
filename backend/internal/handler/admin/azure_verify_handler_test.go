//go:build unit

package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type azureVerifyFakeUpstream struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	respond  func(req *http.Request, body string) (int, http.Header, string)
}

func (f *azureVerifyFakeUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var body string
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()
	status, header, resp := f.respond(req, body)
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(resp))}, nil
}

func (f *azureVerifyFakeUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(req, proxyURL, accountID, concurrency)
}

var _ service.HTTPUpstream = (*azureVerifyFakeUpstream)(nil)

func runAzureVerify(t *testing.T, path string, up *azureVerifyFakeUpstream, payload any) (*httptest.ResponseRecorder, json.RawMessage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewAzureVerifyHandler(&config.Config{}, up)
	r := gin.New()
	r.POST("/models", h.Models)
	r.POST("/probe", h.Probe)

	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w, env.Data
}

func azureLikeUpstream() *azureVerifyFakeUpstream {
	azureHeaders := func() http.Header {
		h := http.Header{}
		h.Set("apim-request-id", "24895689-0741-4b93-b9e0-6b1a90a4f62b")
		h.Set("x-ms-region", "East US 2")
		h.Set("x-ms-served-model", "gpt-6-sol-2026-09-22")
		h.Set("Content-Type", "application/json")
		return h
	}
	return &azureVerifyFakeUpstream{respond: func(req *http.Request, body string) (int, http.Header, string) {
		switch {
		case req.URL.Path == "/v1/chat/completions" && strings.Contains(body, `"stream":true`):
			return http.StatusOK, azureHeaders(), "data: {\"choices\":[],\"created\":0,\"id\":\"\",\"model\":\"\",\"object\":\"\",\"prompt_filter_results\":[{\"prompt_index\":0}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}],\"id\":\"chatcmpl-1\"}\n\n"
		case req.URL.Path == "/v1/chat/completions":
			return http.StatusOK, azureHeaders(), `{"id":"chatcmpl-1","model":"gpt-6-sol-2026-09-22","choices":[{"finish_reason":"length","message":{"content":""},"content_filter_results":{}}],"prompt_filter_results":[],"usage":{"prompt_tokens":17,"completion_tokens":64}}`
		case req.URL.Path == "/v1/responses" && strings.Contains(body, "previous_response_id"):
			return http.StatusOK, azureHeaders(), `{"id":"resp_2","output":[{"type":"message","content":[{"type":"output_text","text":"4729"}]}]}`
		case req.URL.Path == "/v1/responses":
			return http.StatusOK, azureHeaders(), `{"id":"resp_1","status":"completed"}`
		}
		return http.StatusNotFound, nil, `{"error":{"message":"not found"}}`
	}}
}

func TestAzureVerifyProbeDetectsAzure(t *testing.T) {
	up := azureLikeUpstream()
	w, data := runAzureVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com/v1/",
		"api_key":  "  sk-test  ",
		"model":    "gpt-6-sol",
	})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.AzureVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.AzureVerifyStatusOK, res.Status)
	require.True(t, res.IsAzure)
	require.False(t, res.Suspicious)
	require.Equal(t, "gpt-6-sol-2026-09-22", res.ServedModel)
	require.Len(t, res.Steps, 3)
	for _, step := range res.Steps {
		require.True(t, step.OK, step.Name)
	}
	require.Contains(t, res.Steps[0].Headers, "apim-request-id")
	require.NotContains(t, res.Steps[0].Headers, "content-type")

	require.Len(t, up.requests, 4)
	for _, req := range up.requests {
		require.Equal(t, "relay.example.com", req.URL.Host)
		require.Equal(t, "Bearer sk-test", req.Header.Get("Authorization"))
		require.True(t, service.HTTPUpstreamRedirectsDisabled(req.Context()))
		require.True(t, service.HTTPUpstreamPublicHostsOnly(req.Context()))
	}
	require.Contains(t, up.bodies[3], `"previous_response_id":"resp_1"`)
}

func TestAzureVerifyProbeReportsUnavailableModel(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(*http.Request, string) (int, http.Header, string) {
		return http.StatusServiceUnavailable, nil, `{"error":{"code":"model_not_found","message":"No available channel for model gpt-6.1-terra under group az-gpt (distributor)"}}`
	}}
	w, data := runAzureVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com",
		"api_key":  "k",
		"model":    "gpt-6.1-terra",
	})
	// Upstream failures are reported in the payload, never as the admin API status.
	require.Equal(t, http.StatusOK, w.Code)

	var res service.AzureVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.AzureVerifyStatusFailed, res.Status)
	require.False(t, res.IsAzure)
	require.True(t, res.ModelUnavailable)
	require.Contains(t, res.Error, "No available channel")
}

func TestAzureVerifyModels(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(req *http.Request, _ string) (int, http.Header, string) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/v1/models", req.URL.Path)
		return http.StatusOK, nil, `{"data":[{"id":"gpt-6-sol"},{"id":"gpt-6-astra"},{"id":""}]}`
	}}
	w, data := runAzureVerify(t, "/models", up, map[string]any{"base_url": "https://relay.example.com", "api_key": "k"})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.AzureVerifyModelsResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, http.StatusOK, res.Status)
	require.Equal(t, []string{"gpt-6-sol", "gpt-6-astra"}, res.Models)
	require.Empty(t, res.Error)
}

func TestAzureVerifyModelsWrapsUpstreamAuthError(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(*http.Request, string) (int, http.Header, string) {
		return http.StatusUnauthorized, nil, `{"error":{"message":"invalid api key"}}`
	}}
	w, data := runAzureVerify(t, "/models", up, map[string]any{"base_url": "https://relay.example.com", "api_key": "bad"})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.AzureVerifyModelsResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, http.StatusUnauthorized, res.Status)
	require.Contains(t, res.Error, "invalid api key")
}

func TestAzureVerifyRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		path    string
		payload map[string]any
	}{
		"private host":  {"/models", map[string]any{"base_url": "https://127.0.0.1:8080", "api_key": "k"}},
		"localhost":     {"/probe", map[string]any{"base_url": "https://localhost", "api_key": "k", "model": "m"}},
		"insecure http": {"/models", map[string]any{"base_url": "http://relay.example.com", "api_key": "k"}},
		"blank key":     {"/models", map[string]any{"base_url": "https://relay.example.com", "api_key": "   "}},
		"missing model": {"/probe", map[string]any{"base_url": "https://relay.example.com", "api_key": "k"}},
		"blank model":   {"/probe", map[string]any{"base_url": "https://relay.example.com", "api_key": "k", "model": "  "}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			up := &azureVerifyFakeUpstream{respond: func(*http.Request, string) (int, http.Header, string) {
				return http.StatusOK, nil, `{}`
			}}
			w, _ := runAzureVerify(t, tc.path, up, tc.payload)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Empty(t, up.requests)
		})
	}
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/claudetelemetry"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type claudeTelemetryUpstreamCall struct {
	URL         string
	Header      http.Header
	ProxyURL    string
	AccountID   int64
	Concurrency int
}

type claudeTelemetryUpstreamRecorder struct {
	mu    sync.Mutex
	calls []claudeTelemetryUpstreamCall
}

func (u *claudeTelemetryUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, concurrency, nil)
}

func (u *claudeTelemetryUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	u.calls = append(u.calls, claudeTelemetryUpstreamCall{
		URL: req.URL.String(), Header: req.Header.Clone(), ProxyURL: proxyURL,
		AccountID: accountID, Concurrency: concurrency,
	})
	u.mu.Unlock()
	if req.URL.Path == "/first" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"accepted_count":1000}`)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}, nil
}

func (u *claudeTelemetryUpstreamRecorder) snapshot() []claudeTelemetryUpstreamCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]claudeTelemetryUpstreamCall(nil), u.calls...)
}

func claudeTelemetryTestAccount() *Account {
	return &Account{
		ID: 47, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Concurrency: 7,
		Extra: map[string]any{
			"account_uuid":      "account-from-extra",
			"subscription_type": "pro",
		},
		Credentials: map[string]any{
			"organization_uuid": "org-from-credentials",
		},
	}
}

func claudeTelemetryTestWireBody(t *testing.T, stream bool) []byte {
	t.Helper()
	metadata, err := json.Marshal(map[string]string{
		"device_id": "device-from-wire", "account_uuid": "account-from-wire",
		"session_id": "session-from-wire",
	})
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"model": "claude-test", "stream": stream,
		"metadata": map[string]any{"user_id": string(metadata)},
		"messages": []map[string]any{{"role": "user", "content": "PRIVATE_PROMPT"}},
	})
	require.NoError(t, err)
	return body
}

func claudeTelemetryTestWireRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer OAUTH_TOKEN_MUST_NOT_BE_RECORDED")
	req.Header.Set("User-Agent", "claude-cli/2.1.284 (external, cli)")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("x-stainless-os", "Linux")
	req.Header.Set("x-stainless-arch", "arm64")
	req.Header.Set("x-stainless-runtime", "node")
	req.Header.Set("x-stainless-runtime-version", "v24.3.0")
	req.Header.Set("X-Claude-Code-Session-Id", "session-from-wire")
	return req
}

func newClaudeTelemetryGinContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c
}

func currentClaudeTelemetryExporter(manager *ClaudeTelemetryManager) *claudetelemetry.TelemetryExporter {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.current.exporter
}

func TestGatewayClaudeTelemetryBeginUsesFinalIdentityAndNeverRecordsAuthorization(t *testing.T) {
	_, manager, runtime := newClaudeTelemetryManagerForTest(t, nil)
	upstream := &claudeTelemetryUpstreamRecorder{}
	gateway := &GatewayService{claudeTelemetry: manager, httpUpstream: upstream}
	body := claudeTelemetryTestWireBody(t, false)
	request := claudeTelemetryTestWireRequest(t, body)
	c := newClaudeTelemetryGinContext(t)

	attempt := gateway.beginClaudeTelemetryAttempt(
		context.Background(), c, claudeTelemetryTestAccount(), body, request,
		"OAUTH_TOKEN_MUST_NOT_BE_RECORDED", "http://account-proxy.test:8080", nil,
		false, true,
	)
	require.NotNil(t, attempt)
	observeClaudeTelemetryJSON(c, []byte(`{"type":"message","id":"msg-json","model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2},"content":[]}`))
	finishClaudeTelemetryAttempt(context.Background(), attempt, "request-json")
	require.NoError(t, currentClaudeTelemetryExporter(manager).Flush(context.Background()))

	recorded, err := os.ReadFile(runtime.RecordFile)
	require.NoError(t, err)
	payload := string(recorded)
	require.Contains(t, payload, "account-from-extra")
	require.Contains(t, payload, "org-from-credentials")
	require.Contains(t, payload, "device-from-wire")
	require.Contains(t, payload, "session-from-wire")
	require.Contains(t, payload, `"platform":"Linux"`)
	require.NotContains(t, payload, "account-from-wire")
	require.NotContains(t, payload, "OAUTH_TOKEN_MUST_NOT_BE_RECORDED")
	require.NotContains(t, payload, request.Header.Get("Authorization"))
}

func TestGatewayClaudeTelemetryObservesSSEAndJSONSuccess(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
		feed   func(*gin.Context)
	}{
		{
			name: "json", stream: false,
			feed: func(c *gin.Context) {
				observeClaudeTelemetryJSON(c, []byte(`{"type":"message","id":"msg-json","model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":4},"content":[{"type":"text","text":"private output"}]}`))
			},
		},
		{
			name: "sse", stream: true,
			feed: func(c *gin.Context) {
				for _, raw := range []string{
					`{"type":"message_start","message":{"id":"msg-sse","model":"claude-test","usage":{"input_tokens":5}}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"private output"}}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":6}}`,
					`{"type":"message_stop"}`,
				} {
					observeClaudeTelemetrySSEJSON(c, []byte(raw))
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, manager, runtime := newClaudeTelemetryManagerForTest(t, nil)
			gateway := &GatewayService{claudeTelemetry: manager, httpUpstream: &claudeTelemetryUpstreamRecorder{}}
			body := claudeTelemetryTestWireBody(t, test.stream)
			c := newClaudeTelemetryGinContext(t)
			attempt := gateway.beginClaudeTelemetryAttempt(
				context.Background(), c, claudeTelemetryTestAccount(), body,
				claudeTelemetryTestWireRequest(t, body), "oauth-token",
				"http://account-proxy.test:8080", nil, test.stream, true,
			)
			require.NotNil(t, attempt)
			test.feed(c)
			finishClaudeTelemetryAttempt(context.Background(), attempt, "request-"+test.name)
			require.NoError(t, currentClaudeTelemetryExporter(manager).Flush(context.Background()))
			recorded, err := os.ReadFile(runtime.RecordFile)
			require.NoError(t, err)
			require.Contains(t, string(recorded), "tengu_api_success")
		})
	}
}

func TestGatewayClaudeTelemetryFailoverAbortsAndTerminalErrorHasNoSuccess(t *testing.T) {
	_, manager, runtime := newClaudeTelemetryManagerForTest(t, nil)
	gateway := &GatewayService{claudeTelemetry: manager, httpUpstream: &claudeTelemetryUpstreamRecorder{}}
	body := claudeTelemetryTestWireBody(t, false)

	begin := func() *ClaudeTelemetryAttempt {
		return gateway.beginClaudeTelemetryAttempt(
			context.Background(), newClaudeTelemetryGinContext(t), claudeTelemetryTestAccount(),
			body, claudeTelemetryTestWireRequest(t, body), "oauth-token",
			"http://account-proxy.test:8080", nil, false, true,
		)
	}
	failover := begin()
	require.NotNil(t, failover)
	failoverErr := &UpstreamFailoverError{StatusCode: 529}
	failoverErr.attachClaudeTelemetryAttempt(failover, "")
	failoverErr.AbortClaudeTelemetry()
	require.Zero(t, currentClaudeTelemetryExporter(manager).Status().QueuedEvents)

	terminal := begin()
	require.NotNil(t, terminal)
	terminalErr := &UpstreamFailoverError{StatusCode: 529}
	terminalErr.attachClaudeTelemetryAttempt(terminal, "")
	terminalErr.CompleteClaudeTelemetry(context.Background())
	require.Positive(t, currentClaudeTelemetryExporter(manager).Status().QueuedEvents)
	require.NoError(t, currentClaudeTelemetryExporter(manager).Flush(context.Background()))
	recorded, err := os.ReadFile(runtime.RecordFile)
	require.NoError(t, err)
	require.NotContains(t, string(recorded), "tengu_api_success")
}

func TestGatewayClaudeTelemetryDeliveryRouteUsesAccountProxyForBothTargets(t *testing.T) {
	_, manager, _ := newClaudeTelemetryManagerForTest(t, func(runtime *ClaudeTelemetryRuntimeConfig) {
		runtime.Mode = ClaudeTelemetryModeForward
		runtime.FirstParty.Enabled = true
		runtime.FirstParty.URL = "https://telemetry.test/first"
		runtime.Datadog.Enabled = true
		runtime.Datadog.URL = "https://telemetry.test/datadog"
		runtime.Datadog.APIKey = "datadog-key"
		runtime.Retry.MaxAttempts = 1
	})
	upstream := &claudeTelemetryUpstreamRecorder{}
	gateway := &GatewayService{claudeTelemetry: manager, httpUpstream: upstream}
	account := claudeTelemetryTestAccount()
	body := claudeTelemetryTestWireBody(t, false)
	c := newClaudeTelemetryGinContext(t)
	attempt := gateway.beginClaudeTelemetryAttempt(
		context.Background(), c, account, body, claudeTelemetryTestWireRequest(t, body),
		"final-oauth-token", "http://account-proxy.test:8080", nil, false, true,
	)
	require.NotNil(t, attempt)
	observeClaudeTelemetryJSON(c, []byte(`{"type":"message","id":"msg-route","model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[]}`))
	finishClaudeTelemetryAttempt(context.Background(), attempt, "request-route")
	require.NoError(t, currentClaudeTelemetryExporter(manager).Flush(context.Background()))

	calls := upstream.snapshot()
	require.NotEmpty(t, calls)
	byPath := make(map[string]claudeTelemetryUpstreamCall, len(calls))
	pathCounts := make(map[string]int)
	for _, call := range calls {
		path, err := http.NewRequest(http.MethodGet, call.URL, nil)
		require.NoError(t, err)
		byPath[path.URL.Path] = call
		pathCounts[path.URL.Path]++
		require.Equal(t, "http://account-proxy.test:8080", call.ProxyURL)
		require.Equal(t, account.ID, call.AccountID)
		require.Equal(t, account.Concurrency, call.Concurrency)
	}
	require.Positive(t, pathCounts["/first"])
	require.Positive(t, pathCounts["/datadog"])
	require.Empty(t, byPath["/first"].Header.Get("Authorization"), "custom first-party targets must never receive OAuth credentials")
	require.Equal(t, "datadog-key", byPath["/datadog"].Header.Get("dd-api-key"))
}

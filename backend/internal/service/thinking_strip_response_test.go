package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestThinkingSignatureTrackerJoinsSegmentedSignatures(t *testing.T) {
	tracker := newThinkingSignatureTracker()
	events := []map[string]any{
		{"type": "message_start"},
		{"type": "content_block_start", "index": float64(0), "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}},
		{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "thinking_delta", "thinking": "hmm"}},
		{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "signature_delta", "signature": "AAA"}},
		{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "signature_delta", "signature": "BBB"}},
		{"type": "content_block_start", "index": float64(1), "content_block": map[string]any{"type": "redacted_thinking", "data": "RED"}},
		{"type": "content_block_stop", "index": float64(1)},
		{"type": "content_block_start", "index": float64(2), "content_block": map[string]any{"type": "text", "text": ""}},
		{"type": "content_block_stop", "index": float64(2)},
	}
	var got []string
	for _, ev := range events {
		got = append(got, tracker.observe(ev["type"].(string), ev)...)
	}
	require.Equal(t, []string{"RED"}, got, "thinking signature is only complete at its own content_block_stop")

	got = tracker.observe("content_block_stop", map[string]any{"type": "content_block_stop", "index": float64(0)})
	require.Equal(t, []string{"AAABBB"}, got)
	require.Empty(t, tracker.observe("content_block_stop", map[string]any{"type": "content_block_stop", "index": float64(0)}))
}

func TestHandleStreamingResponseRegistersThinkingSignatures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{}, 0)
	svc := &GatewayService{cfg: &config.Config{}, rateLimitService: &RateLimitService{}, thinkingStrip: policy}

	sse := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m","model":"claude-sonnet-4-6","usage":{"input_tokens":1,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"PART1"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"PART2"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n") + "\n"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse))}

	_, err := svc.handleStreamingResponse(context.Background(), resp, c, oauthAccount(11), time.Now(), "claude-sonnet-4-6", "claude-sonnet-4-6", false)
	require.NoError(t, err)
	require.Equal(t, int64(11), cache.owners[xxhash.Sum64String("PART1PART2")])
	require.Contains(t, rec.Body.String(), "PART2")
}

func TestHandleStreamingResponseSkipsRegistrationForAPIKeyAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{}, 0)
	svc := &GatewayService{cfg: &config.Config{}, rateLimitService: &RateLimitService{}, thinkingStrip: policy}
	sse := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"RED\"}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse))}

	account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	_, _ = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "m", "m", false)
	require.Empty(t, cache.owners)
}

func newThinkingStripForwardService(t *testing.T, cfg ClaudeThinkingStripSettings, response string) (*GatewayService, *anthropicHTTPUpstreamRecorder, *fakeThinkingSignatureCache) {
	t.Helper()
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response)),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	settings := DefaultClaudeAntiBanSettings()
	settings.ThinkingStrip = cfg
	svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, svc.cfg)
	svc.settingService.claudeAntiBanRuntime.Store(settings)
	cache := newFakeThinkingSignatureCache()
	svc.SetThinkingStripPolicy(NewThinkingStripPolicy(cache, svc.settingService))
	return svc, upstream, cache
}

func thinkingStripForwardRequest(t *testing.T, endpoint string) (*gin.Context, *ParsedRequest) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, endpoint, nil)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":100,"thinking":{"type":"adaptive"},` +
		`"metadata":{"user_id":"{\"device_id\":\"d\",\"account_uuid\":\"\",\"session_id\":\"` + thinkingStripTestSession + `\"}"},` +
		`"messages":[{"role":"user","content":"hello"},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"FOREIGN_SIG"},{"type":"text","text":"Done."}]},` +
		`{"role":"user","content":"again"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	parsed.SessionContext = &SessionContext{APIKeyID: 9}
	return c, parsed
}

func TestForwardStripsUnknownThinkingAndPoisonsSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetGatewayForwardingSettingsCacheForTest(t)
	response := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"thinking","thinking":"","signature":"NEW_SIG"},{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`
	svc, upstream, cache := newThinkingStripForwardService(t, ClaudeThinkingStripSettings{Enabled: true}, response)
	account := newAnthropicOAuthAccountForPartialUsageTest()
	c, parsed := thinkingStripForwardRequest(t, "/v1/messages")

	_, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotContains(t, string(upstream.lastBody), "FOREIGN_SIG")
	require.NotContains(t, string(upstream.lastBody), `"type":"thinking"`)
	require.Equal(t, thinkingSessionStateStripInvalid, cache.sessions[thinkingStripSessionKey(nil, parsed)])
	require.Equal(t, account.ID, cache.owners[xxhash.Sum64String("NEW_SIG")])
}

func TestForwardKeepsThinkingWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetGatewayForwardingSettingsCacheForTest(t)
	response := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`
	svc, upstream, cache := newThinkingStripForwardService(t, ClaudeThinkingStripSettings{}, response)
	c, parsed := thinkingStripForwardRequest(t, "/v1/messages")

	_, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
	require.NoError(t, err)
	require.Contains(t, string(upstream.lastBody), "FOREIGN_SIG")
	require.Empty(t, cache.sessions)
}

func TestForwardCountTokensStripsWithoutPoisoningSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetGatewayForwardingSettingsCacheForTest(t)
	svc, upstream, cache := newThinkingStripForwardService(t, ClaudeThinkingStripSettings{Enabled: true}, `{"input_tokens":10}`)
	c, parsed := thinkingStripForwardRequest(t, "/v1/messages/count_tokens")

	require.NoError(t, svc.ForwardCountTokens(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed))
	require.NotContains(t, string(upstream.lastBody), "FOREIGN_SIG")
	require.Empty(t, cache.sessions)
}

func TestHandleNonStreamingResponseRegistersThinkingSignatures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{}, 0)
	svc := &GatewayService{cfg: &config.Config{}, rateLimitService: &RateLimitService{}, thinkingStrip: policy}
	body := `{"id":"m","type":"message","model":"claude-sonnet-4-6","content":[{"type":"thinking","thinking":"","signature":"NS1"},{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}

	_, err := svc.handleNonStreamingResponse(context.Background(), resp, c, oauthAccount(13), "claude-sonnet-4-6", "claude-sonnet-4-6")
	require.NoError(t, err)
	require.Equal(t, int64(13), cache.owners[xxhash.Sum64String("NS1")])
}

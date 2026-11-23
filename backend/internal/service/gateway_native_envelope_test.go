package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeNativeClaudeEnvelopeFoldsTopLevelCacheControl(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		path     string
		expected string
	}{
		{
			name:     "string content becomes a text block",
			body:     `{"messages":[{"role":"user","content":"hello"}],"cache_control":{"type":"ephemeral"}}`,
			path:     "messages.0.content.0",
			expected: `{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}`,
		},
		{
			name:     "last block of last message",
			body:     `{"messages":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}],"cache_control":{"type":"ephemeral","ttl":"1h"}}`,
			path:     "messages.0.content.1.cache_control",
			expected: `{"type":"ephemeral","ttl":"1h"}`,
		},
		{
			name:     "skips thinking and empty text",
			body:     `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"x","input":{}},{"type":"text","text":""},{"type":"thinking","thinking":"","signature":"s"}]}],"cache_control":{"type":"ephemeral"}}`,
			path:     "messages.0.content.0.cache_control",
			expected: `{"type":"ephemeral"}`,
		},
		{
			name:     "falls back to an earlier message",
			body:     `{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":[{"type":"redacted_thinking","data":"x"}]}],"cache_control":{"type":"ephemeral"}}`,
			path:     "messages.0.content.0.cache_control",
			expected: `{"type":"ephemeral"}`,
		},
		{
			name:     "keeps an existing block breakpoint",
			body:     `{"messages":[{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral","ttl":"5m"}}]}],"cache_control":{"type":"ephemeral","ttl":"1h"}}`,
			path:     "messages.0.content.0.cache_control",
			expected: `{"type":"ephemeral","ttl":"5m"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := normalizeNativeClaudeEnvelope([]byte(tc.body))
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(out, "cache_control").Exists())
			require.JSONEq(t, tc.expected, gjson.GetBytes(out, tc.path).Raw)
			again, err := normalizeNativeClaudeEnvelope(out)
			require.NoError(t, err)
			require.Equal(t, string(out), string(again), "must be idempotent")
		})
	}
}

func TestNormalizeNativeClaudeEnvelopeDropsCacheControlWithoutTarget(t *testing.T) {
	out, err := normalizeNativeClaudeEnvelope([]byte(`{"messages":[],"cache_control":{"type":"ephemeral"}}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"messages":[]}`, string(out))
}

func TestNormalizeNativeClaudeEnvelopeRejectsNonObjectCacheControl(t *testing.T) {
	_, err := normalizeNativeClaudeEnvelope([]byte(`{"messages":[],"cache_control":"ephemeral"}`))
	require.ErrorIs(t, err, errNativeClaudeCacheControlType)
}

func TestNormalizeNativeClaudeEnvelopeThread(t *testing.T) {
	out, err := normalizeNativeClaudeEnvelope([]byte(`{"messages":[],"thread":{"type":"create"}}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"messages":[]}`, string(out))

	for _, thread := range []string{`{"type":"continue","previous_message_id":"msg_1"}`, `{}`, `"create"`} {
		_, err := normalizeNativeClaudeEnvelope([]byte(`{"messages":[],"thread":` + thread + `}`))
		require.ErrorIs(t, err, errNativeClaudeThreadContinue, thread)
	}
}

func TestNormalizeNativeClaudeEnvelopeLeavesOtherBodiesUntouched(t *testing.T) {
	for _, body := range []string{`{"messages":[],"stream_options":{}}`, `not json`, `[]`} {
		out, err := normalizeNativeClaudeEnvelope([]byte(body))
		require.NoError(t, err)
		require.Equal(t, body, string(out))
	}
}

func TestPreprocessNativeClaudeMessagesAcceptsOfficialEnvelopeFields(t *testing.T) {
	result, err := PreprocessNativeClaudeMessages(
		[]byte(`{"messages":[{"role":"user","content":"hello"}],"cache_control":{"type":"ephemeral"},"thread":{"type":"create"}}`),
		DefaultClaudeAntiBanSettings(),
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(result.Body, "cache_control").Exists())
	require.False(t, gjson.GetBytes(result.Body, "thread").Exists())
	require.Equal(t, "ephemeral", gjson.GetBytes(result.Body, "messages.0.content.0.cache_control.type").String())
}

func TestPreprocessNativeClaudeMessagesEnvelopeErrors(t *testing.T) {
	cases := map[string]string{
		`{"messages":[],"thread":{"type":"continue"}}`:                      "unsupported_thread",
		`{"messages":[],"cache_control":true}`:                              "unsupported_cache_control",
		`{"messages":[],"stream_options":{"include_usage":true}}`:           "unsupported_body_fields",
		`{"messages":[],"cache_control":{"type":"ephemeral"},"store":true}`: "unsupported_body_fields",
	}
	for body, code := range cases {
		_, err := PreprocessNativeClaudeMessages([]byte(body), DefaultClaudeAntiBanSettings())
		var requestErr *ClaudeMessagesPreprocessError
		require.ErrorAs(t, err, &requestErr, body)
		require.Equal(t, 400, requestErr.Status, body)
		require.Equal(t, code, requestErr.Code, body)
	}
}

func forwardOAuthBodyForEnvelopeTest(t *testing.T, body string) (*httptest.ResponseRecorder, *anthropicHTTPUpstreamRecorder, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{err: context.Canceled}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	_, err = svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
	return rec, upstream, err
}

func TestForwardOAuthFoldsTopLevelCacheControlWithinBreakpointLimit(t *testing.T) {
	body := `{"model":"claude-sonnet-4-5","max_tokens":1024,"cache_control":{"type":"ephemeral"},
		"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"s2","cache_control":{"type":"ephemeral"}}],
		"tools":[{"name":"lookup","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`
	_, upstream, _ := forwardOAuthBodyForEnvelopeTest(t, body)
	require.NotNil(t, upstream.lastReq, "the request must reach the upstream")
	require.False(t, gjson.GetBytes(upstream.lastBody, "cache_control").Exists())
	require.LessOrEqual(t, strings.Count(string(upstream.lastBody), `"cache_control"`), maxCacheControlBlocks)
}

func TestForwardOAuthPlacesTopLevelCacheControlOnLastMessage(t *testing.T) {
	body := `{"model":"claude-sonnet-4-5","max_tokens":1024,"cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":"hello"}]}`
	_, upstream, _ := forwardOAuthBodyForEnvelopeTest(t, body)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, "cache_control").Exists())
	require.Equal(t, "ephemeral", gjson.GetBytes(upstream.lastBody, "messages.0.content.0.cache_control.type").String())
}

func TestForwardOAuthRejectsThreadContinue(t *testing.T) {
	body := `{"model":"claude-sonnet-4-5","max_tokens":1024,"thread":{"type":"continue","previous_message_id":"msg_1"},"messages":[{"role":"user","content":"hello"}]}`
	rec, upstream, err := forwardOAuthBodyForEnvelopeTest(t, body)
	require.ErrorIs(t, err, errNativeClaudeThreadContinue)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAgentSDKInlineToolsBetaFollowsClientHeaderAndBody(t *testing.T) {
	withBlock := `[{"role":"user","content":"hi"},{"role":"user","content":[{"type":"tool_addition","tools":[{"name":"x","input_schema":{"type":"object"}}]},{"type":"text","text":"go"}]}]`
	withoutBlock := `[{"role":"user","content":"hi"}]`
	cases := []struct {
		name     string
		header   string
		messages string
		want     bool
	}{
		{"header and block", "foo," + claude.BetaInlineTools, withBlock, true},
		{"header only", claude.BetaInlineTools, withoutBlock, false},
		{"block only", "", withBlock, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := DefaultClaudeAntiBanSettings()
			settings.AgentSDK.ConversationMode = ClaudeAgentSDKConversationStateless
			svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
			svc.settingService.claudeAntiBanRuntime.Store(settings)
			account := &Account{ID: 17, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
			ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			if tc.header != "" {
				c.Request.Header.Set("anthropic-beta", tc.header)
			}
			input := []byte(`{"model":"claude-sonnet-5-5","metadata":{"user_id":"{\"device_id\":\"device\",\"account_uuid\":\"\",\"session_id\":\"33333333-3333-4333-8333-333333333333\"}"},"messages":` + tc.messages + `}`)
			ctx, _, err := svc.prepareClaudeAgentSDK(ctx, c, account, input, "claude-sonnet-5-5")
			require.NoError(t, err)
			header := claudeAgentSDKBetaHeader(ctx)
			require.Equal(t, tc.want, containsBetaToken(header, claude.BetaInlineTools), header)
			require.True(t, strings.HasPrefix(header, agentSDKBetas("claude-sonnet-5-5", false)), "recorded betas keep their order")
		})
	}
}

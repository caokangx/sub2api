package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestProfileDefaultBudgetIgnoresClientMaxTokens(t *testing.T) {
	for _, raw := range []string{"null", "1", "-1", "999999999999999999999999999999999999999", "\"bad\"", "{}", "[]", "false", "1.5"} {
		body := []byte(`{"max_tokens":` + raw + `,"messages":[]}`)
		require.NoError(t, validateNativeClaudeRequest(body, "claude-sonnet-4-6", true))
		got, err := applyClaudeCodeProfileOutputBudget(body, "claude-sonnet-4-6")
		require.NoError(t, err)
		require.EqualValues(t, 32000, gjson.GetBytes(got, "max_tokens").Int())
	}
	for _, tt := range []struct {
		budget int
		want   int
		bad    bool
	}{{1, 0, true}, {1024, 32000, false}, {32000, 32001, false}, {127999, 128000, false}, {128000, 0, true}, {2147483647, 0, true}} {
		body := []byte(fmt.Sprintf(`{"thinking":{"type":"enabled","budget_tokens":%d}}`, tt.budget))
		got, err := applyClaudeCodeProfileOutputBudget(body, "claude-sonnet-4-6")
		if tt.bad {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.EqualValues(t, tt.want, gjson.GetBytes(got, "max_tokens").Int())
		}
	}
}

func TestProfileBetaUsesFeaturesAndEndpoint(t *testing.T) {
	base := []byte(`{"model":"claude-sonnet-4-6","messages":[],"tools":[{"name":"x","input_schema":{"cache_control":{"ttl":"1h"}}}]}`)
	require.Equal(t, claude.BetaClaudeCode+","+claude.BetaOAuth, resolveClaudeCodeProfileBeta("claude-sonnet-4-6", base, "unknown-beta", false, nil))
	body := []byte(`{"thinking":{"type":"adaptive","block_binding":"strict"},"context_management":{"edits":[]},"output_config":{"effort":"high"},"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"assistant","output_config":{"effort":"low"},"content":[]}]}`)
	got := resolveClaudeCodeProfileBeta("claude-sonnet-4-6", body, claude.BetaStructuredOutputs+",unknown-beta", false, nil)
	for _, token := range []string{claude.BetaInterleavedThinking, claude.BetaContextManagement, claude.BetaEffort, claude.BetaThinkingBindingControls, claude.BetaExtendedCacheTTL, claude.BetaMidConversationOutputConfig, claude.BetaStructuredOutputs} {
		require.True(t, containsBetaToken(got, token), token)
	}
	require.NotContains(t, got, "unknown-beta")
	count := resolveClaudeCodeProfileBeta("claude-sonnet-4-6", body, got, true, nil)
	require.Equal(t, strings.Join([]string{claude.BetaClaudeCode, claude.BetaOAuth, claude.BetaInterleavedThinking, claude.BetaTokenCounting}, ","), count)
	require.NotContains(t, resolveClaudeCodeProfileBeta("claude-sonnet-4-6", body, got, false, map[string]struct{}{claude.BetaExtendedCacheTTL: {}}), claude.BetaExtendedCacheTTL)
}

func TestProfileBetaWireIndependentOfUA(t *testing.T) {
	svc := &GatewayService{cfg: &config.Config{}}
	account := newAnthropicOAuthAccountForPartialUsageTest()
	ctx := WithClaudeCodeProfilePolicy(context.Background(), &Group{Platform: PlatformAnthropic, ForceClaudeCodeProfile: true})
	for _, count := range []bool{false, true} {
		var previous string
		for _, ua := range []string{"third-party/1.0", claude.DefaultUserAgent()} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			c.Request.Header.Set("User-Agent", ua)
			body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive"},"context_management":{"edits":[]}}`)
			var req *http.Request
			var wire []byte
			var err error
			if count {
				req, wire, err = svc.buildCountTokensRequest(ctx, c, account, body, "token", "oauth", "claude-sonnet-4-6", ua != claude.DefaultUserAgent())
			} else {
				req, wire, err = svc.buildUpstreamRequest(ctx, c, account, body, "token", "oauth", "claude-sonnet-4-6", false, ua != claude.DefaultUserAgent())
			}
			require.NoError(t, err)
			beta := getHeaderRaw(req.Header, "anthropic-beta")
			if previous != "" {
				require.Equal(t, previous, beta)
			}
			previous = beta
			if !count {
				uid := ParseMetadataUserID(gjson.GetBytes(wire, "metadata.user_id").String())
				require.NotNil(t, uid)
				require.Equal(t, uid.SessionID, getHeaderRaw(req.Header, "X-Claude-Code-Session-Id"))
			} else {
				require.False(t, gjson.GetBytes(wire, "metadata").Exists())
				require.NotEmpty(t, getHeaderRaw(req.Header, "X-Claude-Code-Session-Id"))
			}
		}
	}
}

func TestOAuthIdentityRetryAndAccountSwitch(t *testing.T) {
	svc := &GatewayService{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	account := &Account{ID: 10, Platform: PlatformAnthropic, Type: AccountTypeSetupToken, Credentials: map[string]any{"account_uuid": "credentials-uuid"}}
	fp := &Fingerprint{ClientID: strings.Repeat("a", 64)}
	raw := `{"device_id":"client-device","account_uuid":"client-account","session_id":"123e4567-e89b-42d3-a456-426614174000","parent_session_id":"parent","ti":"supported","unknown":"discard"}`
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"user_id": raw}, "messages": []any{}})
	ctx := WithClaudeCodeProfilePolicy(context.Background(), &Group{Platform: PlatformAnthropic, ForceClaudeCodeProfile: true})
	first, session := svc.rewriteAnthropicOAuthIdentity(ctx, c, account, body, fp, false)
	require.Equal(t, "credentials-uuid", gjson.Parse(gjson.GetBytes(first, "metadata.user_id").String()).Get("account_uuid").String())
	require.Equal(t, fp.ClientID, ParseMetadataUserID(gjson.GetBytes(first, "metadata.user_id").String()).DeviceID)
	require.Contains(t, gjson.GetBytes(first, "metadata.user_id").String(), `"parent_session_id":"parent"`)
	require.NotContains(t, gjson.GetBytes(first, "metadata.user_id").String(), "unknown")
	again, same := svc.rewriteAnthropicOAuthIdentity(ctx, c, account, first, fp, false)
	require.Equal(t, session, same)
	require.Equal(t, first, again)
	other := *account
	other.ID = 20
	other.Extra = map[string]any{"account_uuid": "extra-uuid"}
	changed, next := svc.rewriteAnthropicOAuthIdentity(ctx, c, &other, first, fp, false)
	require.NotEqual(t, session, next)
	require.Equal(t, generateUUIDFromSeed("20::123e4567-e89b-42d3-a456-426614174000"), next)
	require.Equal(t, "extra-uuid", ParseMetadataUserID(gjson.GetBytes(changed, "metadata.user_id").String()).AccountUUID)
}

func TestProfileBillingMergeValidation(t *testing.T) {
	base, _ := buildBillingAttributionTextForSample("hello")
	valid := "x-anthropic-billing-header: cc_version=fake; cc_entrypoint=evil; cch=123; cc_prev_req=req_abc-1; cc_prompt_id=123e4567-e89b-12d3-a456-426614174000; cc_turn_origin=main; cc_prompt_index=0; cc_turn_index=10000000; cc_is_subagent=true; cc_workload=Code_1;"
	body, _ := json.Marshal(map[string]any{"system": []any{map[string]any{"type": "text", "text": valid}, map[string]any{"type": "text", "text": valid}}})
	got := mergeClaudeBillingOptional(base, body)
	for _, key := range billingOptionalOrder {
		require.Equal(t, 1, strings.Count(got, key+"="), key)
	}
	require.NotContains(t, got, "cch=")
	require.NotContains(t, got, "evil")
	for _, invalid := range []string{"cc_turn_index=0;", "cc_prompt_index=10000001;", "cc_workload=x\ny;", "cc_prompt_id=bad;", "cc_prev_req=req_;", "cc_turn_origin=Bad;", "cc_is_subagent=1;", "cc_workload=" + strings.Repeat("a", 4096) + ";"} {
		source, _ := json.Marshal(map[string]any{"system": "x-anthropic-billing-header: " + invalid})
		require.Equal(t, base, mergeClaudeBillingOptional(base, source), invalid)
	}
	conflict, _ := json.Marshal(map[string]any{"system": []any{map[string]any{"text": valid}, map[string]any{"text": "x-anthropic-billing-header: cc_prev_req=req_other;"}}})
	require.NotContains(t, mergeClaudeBillingOptional(base, conflict), "cc_prev_req=")
}

func TestNativeClaudeEnvelopeValidation(t *testing.T) {
	valid := []byte(`{"model":"claude-sonnet-4-6","speed":"fast","top_p":0.9,"top_k":4,"tools":[{"name":"x","input_schema":{"unknown":{"type":"object"}}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"x","input":{"unknown":true,"model":123}}]}]}`)
	require.NoError(t, validateNativeClaudeFields(valid, false))
	for _, bad := range []string{`{"unknown":1}`, `{"messages":{}}`, `{"model":4}`, `{"stream":"yes"}`, `{"system":[{}]}`, `{"tools":[1]}`, `{"metadata":{"user_id":1}}`, `{"thinking":{"budget_tokens":1.5}}`, `{"thinking":[]}`, `{"messages":[{"role":"user","content":false}]}`} {
		require.Error(t, validateNativeClaudeFields([]byte(bad), true), bad)
	}
}

func TestMetadataPassthroughLegacyCacheDisabled(t *testing.T) {
	resetGatewayForwardingSettingsCacheForTest(t)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{metadataPassthrough: true, expiresAt: time.Now().Add(time.Minute).UnixNano()})
	_, passthrough, _ := (&SettingService{}).GetGatewayForwardingSettings(context.Background())
	require.False(t, passthrough)
	_, err := (&SettingService{}).buildSystemSettingsUpdates(context.Background(), &SystemSettings{EnableMetadataPassthrough: true})
	require.ErrorContains(t, err, "disabled")
}

func TestProfileCachePreservesRealMultiturnBreakpoints(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"private","input":{"x":1}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"result","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)
	got, err := rewriteClaudeCodeProfile(body, "claude-sonnet-4-6", "first", DefaultClaudeAntiBanSettings().Environment.Profile)
	require.NoError(t, err)
	_, messages, tools, system := collectCacheControlPaths(got)
	require.LessOrEqual(t, len(messages)+len(tools)+len(system), 4)
	require.NotEmpty(t, messages)
	requireProfileMessages(t, body, got)
	require.Equal(t, "private", gjson.GetBytes(got, "messages.1.content.0.name").String())
	require.EqualValues(t, 1, gjson.GetBytes(got, "messages.1.content.0.input.x").Int())
	noCache, _ := sjson.DeleteBytes(body, "messages.0.content.0.cache_control")
	noCache, _ = sjson.DeleteBytes(noCache, "messages.2.content.0.cache_control")
	got, err = rewriteClaudeCodeProfile(noCache, "claude-sonnet-4-6", "first", DefaultClaudeAntiBanSettings().Environment.Profile)
	require.NoError(t, err)
	_, messages, _, _ = collectCacheControlPaths(got)
	require.Empty(t, messages)
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestClaudeCodeProfilePolicy(t *testing.T) {
	group := &Group{Platform: PlatformAnthropic, ForceClaudeCodeProfile: true}
	ctx := WithClaudeCodeProfilePolicy(context.Background(), group)
	for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		require.True(t, forceClaudeCodeProfile(ctx, &Account{Platform: PlatformAnthropic, Type: typ}))
	}
	for _, account := range []*Account{nil, {Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, {Platform: PlatformOpenAI, Type: AccountTypeOAuth}, {Platform: PlatformAnthropic, Type: AccountTypeBedrock}} {
		require.False(t, forceClaudeCodeProfile(ctx, account))
	}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	require.False(t, forceClaudeCodeProfile(WithClaudeCodeProfilePolicy(ctx, nil), account))
	require.False(t, forceClaudeCodeProfile(WithClaudeCodeProfilePolicy(ctx, &Group{Platform: PlatformAnthropic}), account))
	require.False(t, forceClaudeCodeProfile(WithClaudeCodeProfilePolicy(ctx, &Group{Platform: PlatformOpenAI, ForceClaudeCodeProfile: true}), account))
}

func TestClaudeCodeProfileFixtures(t *testing.T) {
	require.Equal(t, "2.1.284", claude.CLICurrentVersion, "update the pinned profile together with the CLI version")
	require.Equal(t, "161fd06e38b9e274741e3686fa03666acfef2ea797a8366d3ed28aebd2e78e81", fmt.Sprintf("%x", sha256.Sum256([]byte(claudeCodeProfileSystem))))
	require.Equal(t, "3889c842df9977ad47d99c7adb061565bd317b346a55e3180c45193cbb55a471", fmt.Sprintf("%x", sha256.Sum256(claudeCodeProfileTools)))
	for _, marker := range []string{"# Harness", "<pasted_content>", "ultrareview", "EndConversation", "# Memory", "# Context management", "no dependencies between them"} {
		require.Contains(t, claudeCodeProfileSystem, marker)
	}
	for _, private := range []string{"/Users/yikangcao", "/Users/nsmith", "<total_tokens>"} {
		require.NotContains(t, claudeCodeProfileSystem, private)
		require.NotContains(t, string(claudeCodeProfileTools), private)
	}
	var tools []struct {
		Name   string         `json:"name"`
		Schema map[string]any `json:"input_schema"`
	}
	require.NoError(t, json.Unmarshal(claudeCodeProfileTools, &tools))
	names := map[string]bool{}
	for _, tool := range tools {
		require.NotEmpty(t, tool.Name)
		require.NotEmpty(t, tool.Schema)
		require.False(t, names[tool.Name], "duplicate tool name")
		names[tool.Name] = true
	}
}

func TestClaudeCodeProfileUsesConfiguredEnvironmentInOffAndObserveModes(t *testing.T) {
	for _, mode := range []string{ClaudeEnvironmentModeOff, ClaudeEnvironmentModeObserve} {
		t.Run(mode, func(t *testing.T) {
			settings := DefaultClaudeAntiBanSettings()
			settings.Environment.Mode = mode
			settings.Environment.Profile.WorkingDirectory = "/configured/work"
			settings.Environment.Profile.HomeDirectory = "/configured/home"
			settings.Environment.Profile.UserIdentity = "configured-user"
			svc := &GatewayService{settingService: &SettingService{}}
			svc.settingService.claudeAntiBanRuntime.Store(settings)
			ctx := WithClaudeMessagesPreprocessContext(context.Background(), ClaudeMessagesPreprocessResult{
				BillingSample: "original sample",
				Safeguards:    json.RawMessage(`[{"type":"dangerous_tool_use","classifier_context":{"platform":"caller-os","live_cwd":"/caller","home_dir":"/caller-home","user_identity":"caller"}}]`),
			})
			ctx, body, err := svc.prepareClaudeCodeProfile(ctx, []byte(`{"messages":[{"role":"user","content":"rewritten sample"}]}`), "claude-sonnet-4-6")
			require.NoError(t, err)
			require.Contains(t, gjson.GetBytes(body, "system.3.text").String(), "/configured/work")
			billing, err := buildBillingAttributionTextForSample("original sample")
			require.NoError(t, err)
			require.Equal(t, billing, gjson.GetBytes(body, "system.0.text").String())
			safeguards := claudeSafeguardsFromContext(ctx)
			require.Equal(t, "/configured/work", gjson.GetBytes(safeguards, "0.classifier_context.live_cwd").String())
			require.Equal(t, "/configured/home", gjson.GetBytes(safeguards, "0.classifier_context.home_dir").String())
			require.Equal(t, "configured-user", gjson.GetBytes(safeguards, "0.classifier_context.user_identity").String())
			require.Equal(t, "original sample", claudeBillingSampleFromContext(ctx, body))
		})
	}
}

func TestClaudeCodeProfileReplacesCallerSystemAndToolsPreservingHistory(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","system":"PRIVATE_SYSTEM","tools":[{"name":"private_tool"}],"tool_choice":{"type":"tool","name":"private_tool"},"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"private_tool","input":{"x":1}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"result"}]}]}`)
	profile := DefaultClaudeAntiBanSettings().Environment.Profile
	profile.WorkingDirectory = "/workspace/example"
	profile.MemoryDirectory = "/memory/example/"
	out, err := rewriteClaudeCodeProfile(body, "claude-sonnet-4-6", "hello", profile)
	require.NoError(t, err)
	require.True(t, json.Valid(out))
	require.NotContains(t, string(out), "PRIVATE_SYSTEM")
	require.False(t, gjson.GetBytes(out, "tool_choice").Exists())
	require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(out, "messages").Raw)
	require.Len(t, gjson.GetBytes(out, "tools").Array(), 23)
	require.Equal(t, "Agent", gjson.GetBytes(out, "tools.0.name").String())
	require.Contains(t, gjson.GetBytes(out, "system.2.text").String(), profile.MemoryDirectory)
	require.Contains(t, gjson.GetBytes(out, "system.3.text").String(), profile.WorkingDirectory)
	require.NotContains(t, string(out), "{{")
	_, messages, tools, system := collectCacheControlPaths(out)
	require.LessOrEqual(t, len(messages)+len(tools)+len(system), maxCacheControlBlocks)
	repeated, err := rewriteClaudeCodeProfile(out, "claude-sonnet-4-6", "hello", profile)
	require.NoError(t, err)
	require.JSONEq(t, string(out), string(repeated))
	fable, err := rewriteClaudeCodeProfile(body, "claude-fable-5-1", "hello", profile)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(fable, "system").Array(), 2)
	require.Equal(t, gjson.GetBytes(out, "tools").Raw, gjson.GetBytes(fable, "tools").Raw)
}

func TestClaudeCodeProfileNativeWireParity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetGatewayForwardingSettingsCacheForTest(t)
	var expectedSystem, expectedTools string
	for _, enabled := range []bool{false, true} {
		for _, endpoint := range []string{"/v1/messages", "/v1/messages/count_tokens", "/messages/count_tokens"} {
			for _, client := range []string{"third-party/1.0", claude.DefaultUserAgent()} {
				for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
					t.Run(fmt.Sprintf("enabled=%t/%s/%s/%s", enabled, endpoint, client, typ), func(t *testing.T) {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, endpoint, nil)
						c.Request.Header.Set("User-Agent", client)
						c.Request.Header.Set("x-stainless-os", "caller-os")
						body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":100,"system":"PRIVATE_SYSTEM","metadata":{"user_id":"client-user"},"tools":[{"name":"private_tool","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"private_tool"},"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"private_tool","input":{"x":1}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"result"}]}]}`)
						body, err := sjson.SetBytes(body, "thinking", map[string]any{"type": "adaptive"})
						require.NoError(t, err)
						body, err = sjson.SetBytes(body, "temperature", 0.7)
						require.NoError(t, err)
						if client == claude.DefaultUserAgent() {
							var err error
							body, err = sjson.SetBytes(body, "metadata.user_id", "user_a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2_account_550e8400-e29b-41d4-a716-446655440000_session_123e4567-e89b-12d3-a456-426614174000")
							require.NoError(t, err)
						}
						parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
						require.NoError(t, err)
						response := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`
						if endpoint != "/v1/messages" {
							response = `{"input_tokens":10}`
						}
						upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}}
						svc := newForwardPartialUsageServiceForTest(upstream)
						settings := DefaultClaudeAntiBanSettings()
						settings.Environment.Mode = ClaudeEnvironmentModeOff
						settings.Environment.Profile.Platform = "linux"
						settings.Environment.Profile.WorkingDirectory = "/workspace/forced"
						svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, svc.cfg)
						svc.settingService.claudeAntiBanRuntime.Store(settings)
						account := newAnthropicOAuthAccountForPartialUsageTest()
						account.Type = typ
						account.Credentials["api_key"] = "test-api-key"
						ctx := WithClaudeCodeProfilePolicy(context.Background(), &Group{Platform: PlatformAnthropic, ForceClaudeCodeProfile: enabled})
						c.Set(toolNameRewriteKey, &ToolNameRewrite{})
						if endpoint == "/v1/messages" {
							_, err = svc.Forward(ctx, c, account, parsed)
						} else {
							err = svc.ForwardCountTokens(ctx, c, account, parsed)
						}
						require.NoError(t, err)
						require.NotNil(t, upstream.lastReq)
						wire := upstream.lastBody
						if !enabled || typ == AccountTypeAPIKey {
							require.Len(t, gjson.GetBytes(wire, "tools").Array(), 1)
							require.NotContains(t, string(wire), "# Harness")
							if client == claude.DefaultUserAgent() || typ == AccountTypeAPIKey {
								require.Equal(t, "PRIVATE_SYSTEM", gjson.GetBytes(wire, "system").String())
							}
							return
						}
						require.NotContains(t, string(wire), "PRIVATE_SYSTEM")
						require.False(t, gjson.GetBytes(wire, "temperature").Exists(), "thinking requests must omit temperature")
						require.False(t, gjson.GetBytes(wire, "tool_choice").Exists())
						require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(wire, "messages").Raw)
						require.Equal(t, "linux", getHeaderRaw(upstream.lastReq.Header, "x-stainless-os"))
						require.Equal(t, claude.DefaultUserAgent(), getHeaderRaw(upstream.lastReq.Header, "User-Agent"))
						mapping, _ := c.Get(toolNameRewriteKey)
						require.True(t, mapping.(*ToolNameRewrite).PreserveNames)
						if expectedSystem == "" {
							expectedSystem = gjson.GetBytes(wire, "system").Raw
							expectedTools = gjson.GetBytes(wire, "tools").Raw
						}
						require.JSONEq(t, expectedSystem, gjson.GetBytes(wire, "system").Raw)
						require.JSONEq(t, expectedTools, gjson.GetBytes(wire, "tools").Raw)
					})
				}
			}
		}
	}
}

func TestClaudeCodeProfileAuthCacheRoundTrip(t *testing.T) {
	groupID := int64(71)
	apiKey := &APIKey{ID: 2, UserID: 1, Key: "test-key", Status: StatusActive, GroupID: &groupID, User: &User{ID: 1, Status: StatusActive}, Group: &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true, ForceClaudeCodeProfile: true}}
	svc := &APIKeyService{}
	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))
	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.True(t, materialized.Group.ForceClaudeCodeProfile)
}

func TestClaudeCodeProfileResponsePreservesToolNamesAndArguments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetGatewayForwardingSettingsCacheForTest(t)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-5-5","stream":%t,"max_tokens":100,"tool_choice":{"type":"tool","name":"caller-tool"},"system":"PRIVATE_SYSTEM","messages":[{"role":"user","content":"hello"}]}`, stream))
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			response := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo cc_ses_native"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":1}}`
			contentType := "application/json"
			if stream {
				contentType = "text/event-stream"
				response = strings.Join([]string{
					"event: message_start", `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`, "",
					"event: content_block_start", `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo cc_ses_native"}}}`, "",
					"event: content_block_stop", `data: {"type":"content_block_stop","index":0}`, "",
					"event: message_delta", `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`, "",
					"event: message_stop", `data: {"type":"message_stop"}`, "", "",
				}, "\n")
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			account := newAnthropicOAuthAccountForPartialUsageTest()
			ctx := WithClaudeCodeProfilePolicy(context.Background(), &Group{Platform: PlatformAnthropic, ForceClaudeCodeProfile: true})
			c.Set(toolNameRewriteKey, &ToolNameRewrite{ReverseOrdered: [][2]string{{"Bash", "caller-tool"}}})
			_, err = svc.Forward(ctx, c, account, parsed)
			require.NoError(t, err)
			require.Contains(t, rec.Body.String(), `"name":"Bash"`)
			require.Contains(t, rec.Body.String(), "cc_ses_native")
			require.NotContains(t, rec.Body.String(), "caller-tool")
			require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
		})
	}
}

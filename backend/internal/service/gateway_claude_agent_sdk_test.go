package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeAgentSDKGoldenFixtures(t *testing.T) {
	for _, name := range []string{
		"create_no_tools.json", "create_with_tools.json", "continue_tool_result.json",
		"stateless_opus.json", "image_input.json",
	} {
		raw, err := os.ReadFile("claude_agent_sdk_profile/0.3.310/testdata/" + name)
		require.NoError(t, err, name)
		require.True(t, json.Valid(raw), name)
		require.NotContains(t, string(raw), "sk-ant-", name)
		require.NotContains(t, string(raw), "/Users/yikangcao", name)
		require.Equal(t, "sdk-ts", agentSDKBillingField(gjson.GetBytes(raw, "system.0.text").String(), "cc_entrypoint"), name)
	}
	require.Equal(t, "create", gjson.GetBytes(mustReadAgentSDKFixture(t, "create_no_tools.json"), "thread.type").String())
	require.Equal(t, "continue", gjson.GetBytes(mustReadAgentSDKFixture(t, "continue_tool_result.json"), "thread.type").String())
	require.False(t, gjson.GetBytes(mustReadAgentSDKFixture(t, "stateless_opus.json"), "thread").Exists())
}

func mustReadAgentSDKFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("claude_agent_sdk_profile/0.3.310/testdata/" + name)
	require.NoError(t, err)
	return raw
}

func agentSDKBillingField(text, field string) string {
	for _, item := range strings.Split(text, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if ok && key == field {
			return value
		}
	}
	return ""
}

type agentSDKTestCache struct {
	stubGatewayCache
	mu   sync.Mutex
	data map[string]string
}

func (c *agentSDKTestCache) SetReasoningContent(_ context.Context, key, value string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		c.data = map[string]string{}
	}
	c.data[key] = value
	return nil
}

func (c *agentSDKTestCache) GetReasoningContent(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data[key], nil
}

func TestClaudeAgentSDKPolicyIsSetupTokenOnly(t *testing.T) {
	ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{
		Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true,
	})
	require.True(t, simulateClaudeAgentSDK(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}))
	require.False(t, simulateClaudeAgentSDK(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}))
	require.False(t, simulateClaudeAgentSDK(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}))
	require.False(t, simulateClaudeAgentSDK(WithClaudeAgentSDKPolicy(ctx, nil), &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}))
}

func TestClaudeAgentSDKBillingClasses(t *testing.T) {
	state := claudeAgentSDKThreadState{
		RequestID: "req_previous", PromptID: "11111111-1111-4111-8111-111111111111", TurnIndex: 2,
	}
	main := buildClaudeAgentSDKBilling([]byte(`{"messages":[{"role":"user","content":"x"}]}`), "main", state)
	require.Contains(t, main, "cc_prev_req=req_previous")
	require.Contains(t, main, "cc_turn_origin=sdk")
	require.Contains(t, main, "cc_turn_index=2")
	subagent := buildClaudeAgentSDKBilling([]byte(`{"messages":[]}`), "subagent", state)
	require.Contains(t, subagent, "cc_is_subagent=true")
	require.NotContains(t, subagent, "cc_turn_origin")
	compaction := buildClaudeAgentSDKBilling([]byte(`{"messages":[]}`), "compaction", state)
	require.Contains(t, compaction, "cc_prev_req=req_previous")
	require.NotContains(t, compaction, "cc_prompt_id")
}

func TestClaudeAgentSDKStatelessRewrite(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.AgentSDK.ConversationMode = ClaudeAgentSDKConversationStateless
	settings.Environment.Profile.WorkingDirectory = "/sdk/work"
	svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
	svc.settingService.claudeAntiBanRuntime.Store(settings)
	account := &Account{ID: 7, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
	ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	input := []byte(`{
		"model":"claude-sonnet-5-5",
		"system":[{"type":"text","text":"CLIENT SYSTEM"}],
		"tools":[{"name":"client_tool","description":"x","input_schema":{"type":"object"}}],
		"temperature":0.2,
		"output_config":{"effort":"high"},
		"metadata":{"user_id":"{\"device_id\":\"device\",\"account_uuid\":\"account\",\"session_id\":\"11111111-1111-4111-8111-111111111111\"}"},
		"messages":[{"role":"user","content":"hello"}]
	}`)
	ctx, body, err := svc.prepareClaudeAgentSDK(ctx, c, account, input, "claude-sonnet-5-5")
	require.NoError(t, err)
	require.Equal(t, "CLIENT SYSTEM", gjson.GetBytes(body, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(body, "messages.1.role").String())
	require.Equal(t, "system", gjson.GetBytes(body, "messages.2.role").String())
	require.Contains(t, gjson.GetBytes(body, "messages.2.content.0.text").String(), "Primary working directory: /sdk/work")
	require.Equal(t, "ephemeral", gjson.GetBytes(body, "messages.2.content.0.cache_control.type").String())
	require.Len(t, gjson.GetBytes(body, "system").Array(), 3)
	require.False(t, gjson.GetBytes(body, "system.1.cache_control").Exists(), "identity block intentionally has no cache_control")
	require.Equal(t, "client_tool", gjson.GetBytes(body, "tools.0.name").String())
	require.Equal(t, "high", gjson.GetBytes(body, "output_config.effort").String())
	require.Equal(t, "adaptive", gjson.GetBytes(body, "thinking.type").String())
	require.False(t, gjson.GetBytes(body, "temperature").Exists())
	require.False(t, gjson.GetBytes(body, "thread").Exists())
	require.True(t, gjson.GetBytes(body, "diagnostics.previous_message_id").Exists())
	require.Contains(t, gjson.GetBytes(body, "system.0.text").String(), "cc_entrypoint=sdk-ts")

	wire, err := applyClaudeAgentSDKOutputBudget(body, "claude-sonnet-5-5")
	require.NoError(t, err)
	require.Equal(t, int64(128000), gjson.GetBytes(wire, "max_tokens").Int())

	wireSvc := &GatewayService{cache: svc.cache}
	req, wire, err := wireSvc.buildUpstreamRequest(ctx, c, account, wire, "token", "oauth", "claude-sonnet-5-5", true, true)
	require.NoError(t, err)
	require.Equal(t, claudeAgentSDKUserAgent, req.Header.Get("User-Agent"))
	require.Contains(t, gjson.GetBytes(wire, "system.0.text").String(), "cc_entrypoint=sdk-ts")
	require.NotContains(t, gjson.GetBytes(wire, "system.0.text").String(), "cch=00000")
	parsed := ParseMetadataUserID(gjson.GetBytes(wire, "metadata.user_id").String())
	require.NotNil(t, parsed)
	require.Empty(t, parsed.AccountUUID)
}

func TestClaudeAgentSDKThreadCreateThenContinue(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.AgentSDK.ConversationMode = ClaudeAgentSDKConversationThread
	cache := &agentSDKTestCache{}
	svc := &GatewayService{settingService: &SettingService{}, cache: cache}
	svc.settingService.claudeAntiBanRuntime.Store(settings)
	account := &Account{ID: 8, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
	base := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	first := []byte(`{"model":"claude-sonnet-5-5","metadata":{"user_id":"{\"device_id\":\"device\",\"account_uuid\":\"\",\"session_id\":\"22222222-2222-4222-8222-222222222222\"}"},"messages":[{"role":"user","content":"first"}]}`)
	ctx1, body1, err := svc.prepareClaudeAgentSDK(base, c, account, first, "claude-sonnet-5-5")
	require.NoError(t, err)
	require.Equal(t, "create", gjson.GetBytes(body1, "thread.type").String())
	require.Len(t, gjson.GetBytes(body1, "system").Array(), 2, "no-tools shape omits the WebSearch system block")
	svc.recordClaudeAgentSDKResponse(ctx1, "msg_first", "req_first")

	second := []byte(`{"model":"claude-sonnet-5-5","metadata":{"user_id":"{\"device_id\":\"device\",\"account_uuid\":\"\",\"session_id\":\"22222222-2222-4222-8222-222222222222\"}"},"messages":[{"role":"user","content":"first"},{"role":"assistant","content":[{"type":"text","text":"answer"}]},{"role":"user","content":"second"}]}`)
	_, body2, err := svc.prepareClaudeAgentSDK(base, c, account, second, "claude-sonnet-5-5")
	require.NoError(t, err)
	require.Equal(t, "continue", gjson.GetBytes(body2, "thread.type").String())
	require.Equal(t, "msg_first", gjson.GetBytes(body2, "thread.previous_message_id").String())
	require.Len(t, gjson.GetBytes(body2, "system").Array(), 1)
	require.Contains(t, gjson.GetBytes(body2, "system.0.text").String(), "cc_prev_req=req_first")
	require.NotEmpty(t, gjson.GetBytes(body2, "messages").Array())
}

func TestClaudeAgentSDKCanDisableSystemMessageInjection(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.AgentSDK.InjectSystemMessages = false
	svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
	svc.settingService.claudeAntiBanRuntime.Store(settings)
	account := &Account{ID: 9, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
	ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	_, body, err := svc.prepareClaudeAgentSDK(ctx, c, account, []byte(`{
		"model":"claude-sonnet-5-5","system":"client policy",
		"messages":[{"role":"user","content":"hello"}]
	}`), "claude-sonnet-5-5")
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(body, "messages").Array(), 2)
	require.Equal(t, "system", gjson.GetBytes(body, "messages.0.role").String())
	require.Equal(t, "client policy", gjson.GetBytes(body, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(body, "messages.1.role").String())
}

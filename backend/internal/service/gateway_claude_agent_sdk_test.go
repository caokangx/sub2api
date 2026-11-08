package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeAgentSDKGoldenFixtures(t *testing.T) {
	for _, name := range []string{
		"create_no_tools.json", "create_with_tools.json", "continue_tool_result.json",
		"stateless_opus.json", "image_input.json", "create_no_tools_sonnet46.json",
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
	sonnet46 := mustReadAgentSDKFixture(t, "create_no_tools_sonnet46.json")
	require.Equal(t, int64(32000), gjson.GetBytes(sonnet46, "max_tokens").Int())
	requireNoAgentSDKSystemRole(t, sonnet46)
}

func TestClaudeAgentSDKOrderedMarshalMatchesGoldenStructure(t *testing.T) {
	for _, name := range []string{
		"create_no_tools.json", "create_with_tools.json", "continue_tool_result.json",
		"stateless_opus.json", "image_input.json", "create_no_tools_sonnet46.json",
	} {
		t.Run(name, func(t *testing.T) {
			raw := mustReadAgentSDKFixture(t, name)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(raw, &decoded))
			encoded, err := marshalClaudeAgentSDKJSON(decoded)
			require.NoError(t, err)
			require.Equal(t, agentSDKObjectKeys(t, raw), agentSDKObjectKeys(t, encoded))

			for _, path := range []string{"messages", "system", "tools"} {
				wantItems := gjson.GetBytes(raw, path).Array()
				gotItems := gjson.GetBytes(encoded, path).Array()
				require.Len(t, gotItems, len(wantItems), path)
				for i := range wantItems {
					require.Equal(t,
						agentSDKObjectKeys(t, []byte(wantItems[i].Raw)),
						agentSDKObjectKeys(t, []byte(gotItems[i].Raw)),
						"%s.%d", path, i,
					)
					if path != "messages" {
						continue
					}
					wantBlocks := wantItems[i].Get("content").Array()
					gotBlocks := gotItems[i].Get("content").Array()
					require.Len(t, gotBlocks, len(wantBlocks), "messages.%d.content", i)
					for j := range wantBlocks {
						if !wantBlocks[j].IsObject() {
							continue
						}
						require.Equal(t,
							agentSDKObjectKeys(t, []byte(wantBlocks[j].Raw)),
							agentSDKObjectKeys(t, []byte(gotBlocks[j].Raw)),
							"messages.%d.content.%d", i, j,
						)
					}
				}
			}
		})
	}
}

func requireNoAgentSDKSystemRole(t *testing.T, body []byte) {
	t.Helper()
	for i, msg := range gjson.GetBytes(body, "messages").Array() {
		require.NotEqual(t, "system", msg.Get("role").String(), "messages.%d: %s", i, gjson.GetBytes(body, "messages").Raw)
	}
}

var agentSDKFixtureProfile = ClaudeEnvironmentProfile{
	WorkingDirectory: "/Users/developer/project", GitRepository: true,
	Platform: "darwin", Shell: "zsh", OSVersion: "Darwin 24.5.0",
}

func TestAgentSDKEnvironmentMatchesFixtures(t *testing.T) {
	for _, tc := range []struct {
		fixture  string
		hasTools bool
	}{{"create_no_tools.json", false}, {"create_with_tools.json", true}} {
		t.Run(tc.fixture, func(t *testing.T) {
			raw := mustReadAgentSDKFixture(t, tc.fixture)
			require.Equal(t, "system", gjson.GetBytes(raw, "messages.1.role").String())
			want := gjson.GetBytes(raw, "messages.1.content.0.text").String()
			got := renderAgentSDKEnvironment(agentSDKFixtureProfile, "claude-sonnet-5-5", tc.hasTools, "2026-11-03")
			require.Equal(t, want, got)
			require.Equal(t, 1, strings.Count(got, "<total_tokens>"))
		})
	}
	raw := mustReadAgentSDKFixture(t, "create_with_tools.json")
	require.Equal(t, gjson.GetBytes(raw, "messages.0.content.0.text").String(), claudeAgentSDKGitAttribution)
}

func mustReadAgentSDKFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("claude_agent_sdk_profile/0.3.310/testdata/" + name)
	require.NoError(t, err)
	return raw
}

func agentSDKObjectKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := decoder.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), token)
	var keys []string
	for decoder.More() {
		token, err = decoder.Token()
		require.NoError(t, err)
		key, ok := token.(string)
		require.True(t, ok)
		keys = append(keys, key)
		var value json.RawMessage
		require.NoError(t, decoder.Decode(&value))
	}
	_, err = decoder.Token()
	require.NoError(t, err)
	return keys
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
	require.Equal(t, "user", gjson.GetBytes(body, "messages.0.role").String())
	require.Equal(t, "system", gjson.GetBytes(body, "messages.1.role").String())
	require.Equal(t, gjson.String, gjson.GetBytes(body, "messages.1.content").Type)
	require.Contains(t, gjson.GetBytes(body, "messages.1.content").String(), "Primary working directory: /sdk/work")
	require.NotContains(t, string(body), "cache_control", "the gateway adds no breakpoint of its own")
	require.Equal(t, "system", gjson.GetBytes(body, "messages.2.role").String())
	require.Equal(t, "CLIENT SYSTEM", gjson.GetBytes(body, "messages.2.content").String())
	require.Len(t, gjson.GetBytes(body, "system").Array(), 3)
	require.False(t, gjson.GetBytes(body, "system.1.cache_control").Exists(), "identity block intentionally has no cache_control")
	require.Equal(t, "client_tool", gjson.GetBytes(body, "tools.0.name").String())
	require.Equal(t, "high", gjson.GetBytes(body, "output_config.effort").String())
	require.Equal(t, "adaptive", gjson.GetBytes(body, "thinking.type").String())
	require.False(t, gjson.GetBytes(body, "temperature").Exists())
	require.False(t, gjson.GetBytes(body, "thread").Exists())
	require.True(t, gjson.GetBytes(body, "diagnostics.previous_message_id").Exists())
	require.Contains(t, gjson.GetBytes(body, "system.0.text").String(), "cc_entrypoint=sdk-ts")
	require.Equal(t,
		[]string{"model", "messages", "system", "tools", "metadata", "max_tokens", "thinking", "context_management", "output_config", "diagnostics"},
		agentSDKObjectKeys(t, body),
	)

	wire, err := applyClaudeAgentSDKOutputBudget(body, "claude-sonnet-5-5")
	require.NoError(t, err)
	require.Equal(t, int64(128000), gjson.GetBytes(wire, "max_tokens").Int())
	require.Equal(t, agentSDKObjectKeys(t, body), agentSDKObjectKeys(t, wire), "output budget update must preserve key order")

	wireSvc := &GatewayService{cache: svc.cache}
	req, wire, err := wireSvc.buildUpstreamRequest(ctx, c, account, wire, "token", "oauth", "claude-sonnet-5-5", true, true)
	require.NoError(t, err)
	require.Equal(t, claudeAgentSDKUserAgent, req.Header.Get("User-Agent"))
	require.Contains(t, gjson.GetBytes(wire, "system.0.text").String(), "cc_entrypoint=sdk-ts")
	require.NotContains(t, gjson.GetBytes(wire, "system.0.text").String(), "cch=00000")
	require.Equal(t, agentSDKObjectKeys(t, body), agentSDKObjectKeys(t, wire), "wire mutations must preserve top-level order")
	parsed := ParseMetadataUserID(gjson.GetBytes(wire, "metadata.user_id").String())
	require.NotNil(t, parsed)
	require.Empty(t, parsed.AccountUUID)
	assertJSONTokenOrder(t, gjson.GetBytes(wire, "metadata.user_id").String(),
		`"device_id"`, `"account_uuid"`, `"session_id"`)
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
	require.True(t, gjson.GetBytes(body1, "tools").IsArray())
	require.Empty(t, gjson.GetBytes(body1, "tools").Array())
	info1, _ := ctx1.Value(claudeAgentSDKContextKey{}).(*claudeAgentSDKRequestContext)
	require.NotNil(t, info1)
	require.True(t, gjson.GetBytes(info1.CreateBody, "tools").IsArray())
	fallbackSvc := &GatewayService{cache: cache}
	_, fallbackWire, err := fallbackSvc.buildUpstreamRequest(ctx1, c, account, info1.CreateBody, "token", "oauth", "claude-sonnet-5-5", true, true)
	require.NoError(t, err)
	require.NotContains(t, gjson.GetBytes(fallbackWire, "system.0.text").String(), "cch=00000")
	require.Equal(t,
		[]string{"model", "messages", "system", "tools", "metadata", "max_tokens", "thinking", "context_management", "output_config", "thread", "diagnostics"},
		agentSDKObjectKeys(t, fallbackWire),
	)
	svc.recordClaudeAgentSDKResponse(ctx1, "msg_first", "req_first")

	second := []byte(`{"model":"claude-sonnet-5-5","metadata":{"user_id":"{\"device_id\":\"device\",\"account_uuid\":\"\",\"session_id\":\"22222222-2222-4222-8222-222222222222\"}"},"messages":[{"role":"user","content":"first"},{"role":"assistant","content":[{"type":"text","text":"answer"}]},{"role":"user","content":"second"}]}`)
	_, body2, err := svc.prepareClaudeAgentSDK(base, c, account, second, "claude-sonnet-5-5")
	require.NoError(t, err)
	require.Equal(t, "continue", gjson.GetBytes(body2, "thread.type").String())
	require.Equal(t, "msg_first", gjson.GetBytes(body2, "thread.previous_message_id").String())
	require.Len(t, gjson.GetBytes(body2, "system").Array(), 1)
	require.Contains(t, gjson.GetBytes(body2, "system.0.text").String(), "cc_prev_req=req_first")
	require.NotEmpty(t, gjson.GetBytes(body2, "messages").Array())
	require.False(t, gjson.GetBytes(body2, "tools").Exists(), "unchanged tools are omitted on thread continue")
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
	require.Equal(t, "user", gjson.GetBytes(body, "messages.0.role").String())
	require.Equal(t, "system", gjson.GetBytes(body, "messages.1.role").String())
	require.Equal(t, "client policy", gjson.GetBytes(body, "messages.1.content").String())
}

func TestClaudeAgentSDKValidationIgnoresRewrittenFields(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5-5","max_tokens":1,"temperature":0.2,"tool_choice":{"type":"any"},"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello"}]}`)
	setupToken := &Account{ID: 10, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
	group := &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true}

	plain, rewritten := nativeClaudeValidationInput(context.Background(), setupToken, body)
	require.False(t, rewritten)
	require.ErrorContains(t, validateNativeClaudeRequest(plain, "claude-opus-5-5", rewritten), "requires adaptive thinking")

	ctx := WithClaudeAgentSDKPolicy(context.Background(), group)
	validation, rewritten := nativeClaudeValidationInput(ctx, setupToken, body)
	require.True(t, rewritten)
	require.NoError(t, validateNativeClaudeRequest(validation, "claude-opus-5-5", rewritten))

	settings := DefaultClaudeAntiBanSettings()
	svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
	svc.settingService.claudeAntiBanRuntime.Store(settings)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	_, out, err := svc.prepareClaudeAgentSDK(ctx, c, setupToken, body, "claude-opus-5-5")
	require.NoError(t, err)
	require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
	require.False(t, gjson.GetBytes(out, "temperature").Exists())
	require.False(t, gjson.GetBytes(out, "tool_choice").Exists())

	oauth := &Account{ID: 11, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	_, rewritten = nativeClaudeValidationInput(ctx, oauth, body)
	require.False(t, rewritten, "the Agent SDK profile applies to setup-token accounts only")
}

func TestAgentSDKInsertClientSystemNeverLeadsMessages(t *testing.T) {
	profile := DefaultClaudeAntiBanSettings().Environment.Profile
	for _, tc := range []struct {
		name     string
		messages []any
		inject   bool
		want     int
	}{
		{"injected", []any{map[string]any{"role": "user", "content": "hi"}}, true, 2},
		{"not injected", []any{map[string]any{"role": "user", "content": "hi"}}, false, 1},
		{"client control message", []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "<total_tokens>1 tokens left</total_tokens>"},
			map[string]any{"role": "assistant", "content": "ok"},
		}, true, 2},
		{"no user message", []any{map[string]any{"role": "assistant", "content": "ok"}}, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := agentSDKMessages(tc.messages, agentSDKMessagesOptions{
				Model: "claude-sonnet-5-5", Profile: profile, Inject: tc.inject, RemainingTokens: 1000,
				ClientSystem: agentSDKClientSystem{Text: "CLIENT SYSTEM"}, MidConversationSystem: true,
			})
			require.NotEqual(t, "system", out[0].(map[string]any)["role"])
			got := out[tc.want].(map[string]any)
			require.Equal(t, "system", got["role"])
			require.Equal(t, "CLIENT SYSTEM", got["content"])
		})
	}
	out, _ := agentSDKMessages(nil, agentSDKMessagesOptions{
		Model: "claude-sonnet-5-5", Profile: profile, Inject: true, RemainingTokens: 1000,
		ClientSystem: agentSDKClientSystem{Text: "CLIENT SYSTEM"}, MidConversationSystem: true,
	})
	require.Empty(t, out)
}

func TestAgentSDKMovesHermesClientSystemIntoFirstUser(t *testing.T) {
	out, _ := agentSDKMessages([]any{
		map[string]any{"role": "user", "content": "hello"},
	}, agentSDKMessagesOptions{
		Model: "claude-sonnet-5-5", Profile: agentSDKFixtureProfile, Inject: true,
		RemainingTokens: claudeAgentSDKTokenBudget, EnvironmentDate: "2026-11-08",
		ClientSystem: agentSDKClientSystem{
			Text:         "You are Hermes Agent, created by Nous Research.",
			CacheControl: map[string]any{"type": "ephemeral"},
		},
		MidConversationSystem: true,
	})

	require.Len(t, out, 2)
	require.Equal(t, "user", out[0].(map[string]any)["role"])
	content := out[0].(map[string]any)["content"].([]any)
	require.Len(t, content, 2)
	require.Equal(t,
		"You are Hermes Agent, created by Nous Research.",
		content[0].(map[string]any)["text"],
	)
	require.Equal(t, map[string]any{"type": "ephemeral"}, content[0].(map[string]any)["cache_control"])
	require.Equal(t, "hello", content[1].(map[string]any)["text"])
	require.Equal(t, "system", out[1].(map[string]any)["role"], "the Agent SDK environment remains a system message")
	require.NotContains(t, agentSDKSystemText(out[1].(map[string]any)["content"]), "Hermes")
}

func TestAgentSDKHermesClientSystemMatchIsConservative(t *testing.T) {
	for text, want := range map[string]bool{
		"You are Hermes Agent": true,
		"Policy.\nYou are Hermes Agent, created by Nous Research": true,
		"You are a Hermes Agent":                                  false,
		"you are Hermes Agent":                                    false,
		"Hermes Agent documentation":                              false,
		"Hermes":                                                  false,
	} {
		require.Equal(t, want, agentSDKIsHermesClientSystem(text), text)
	}
}

// requireAgentSDKSystemOrder enforces the upstream rule: a content-bearing
// system message must precede an assistant message or end the array.
func requireAgentSDKSystemOrder(t *testing.T, body []byte) {
	t.Helper()
	messages := gjson.GetBytes(body, "messages").Array()
	for i, msg := range messages {
		if msg.Get("role").String() != "system" {
			continue
		}
		next := i + 1
		for next < len(messages) && messages[next].Get("role").String() == "system" {
			next++
		}
		if next < len(messages) {
			require.Equal(t, "assistant", messages[next].Get("role").String(),
				"messages.%d: system must precede an assistant message or end the array: %s", i, gjson.GetBytes(body, "messages").Raw)
		}
	}
}

func TestClaudeAgentSDKNormalizesHistoryForSystemOrder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages string
		users    int
	}{
		{"consecutive users", `[{"role":"user","content":"ctx"},{"role":"user","content":"hi"},{"role":"assistant","content":"ok"}]`, 1},
		{"tool result then text", `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"tu","name":"t","input":{}}]},` +
			`{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu","content":"ok"}]},{"role":"user","content":"interject"}]`, 2},
		{"directive between users", `[{"role":"user","content":"a"},{"role":"system","content":[],"output_config":{"effort":"high"}},{"role":"user","content":"b"}]`, 1},
		{"client system before user", `[{"role":"system","content":"lead"},{"role":"user","content":"a"},{"role":"assistant","content":"ok"},` +
			`{"role":"system","content":[{"type":"text","text":"be nice"}]},{"role":"user","content":"b"}]`, 2},
		{"three users at the end", `[{"role":"user","content":"a"},{"role":"assistant","content":"ok"},{"role":"user","content":"b"},{"role":"user","content":"c"},{"role":"user","content":"d"}]`, 2},
	} {
		for _, inject := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/inject=%v", tc.name, inject), func(t *testing.T) {
				settings := DefaultClaudeAntiBanSettings()
				settings.AgentSDK.InjectSystemMessages = inject
				svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
				svc.settingService.claudeAntiBanRuntime.Store(settings)
				account := &Account{ID: 23, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
				ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				_, out, err := svc.prepareClaudeAgentSDK(ctx, c, account,
					[]byte(`{"model":"claude-sonnet-5-5","system":"CLIENT SYSTEM","messages":`+tc.messages+`}`), "claude-sonnet-5-5")
				require.NoError(t, err)
				requireAgentSDKSystemOrder(t, out)
				users := 0
				for _, msg := range gjson.GetBytes(out, "messages").Array() {
					if msg.Get("role").String() == "user" {
						users++
					}
				}
				require.Equal(t, tc.users, users)
				require.NotContains(t, string(out), "output_config\":{\"effort\":\"high\"}}", "directive-only messages are dropped")
			})
		}
	}
}

func TestAgentSDKNormalizeSourceMessages(t *testing.T) {
	user := map[string]any{"role": "user", "content": "hi"}
	alternating := []any{user, map[string]any{"role": "assistant", "content": "ok"}, map[string]any{"role": "system", "content": "tail"}}
	out := agentSDKNormalizeSourceMessages(alternating)
	require.Len(t, out, 3)
	for i := range alternating {
		require.Equal(t, reflect.ValueOf(alternating[i]).Pointer(), reflect.ValueOf(out[i]).Pointer(), "untouched messages keep their value")
	}

	cached := map[string]any{"type": "text", "text": "b", "cache_control": map[string]any{"type": "ephemeral"}}
	out = agentSDKNormalizeSourceMessages([]any{
		user,
		map[string]any{"role": "system", "content": "rules"},
		map[string]any{"role": "user", "content": []any{cached}},
	})
	require.Len(t, out, 1)
	require.Equal(t, []any{
		map[string]any{"type": "text", "text": "hi"},
		map[string]any{"type": "text", "text": "<system-reminder>\nrules\n</system-reminder>"},
		cached,
	}, out[0].(map[string]any)["content"])
	require.Equal(t, "hi", user["content"], "the source message is not mutated")
}

func TestAgentSDKTextualizesOnlyNonAnthropicToolHistory(t *testing.T) {
	const anthropicID = "toolu_01T1VRUqA2rPiR3k5UaBEAcC"
	source := []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "checking"},
			map[string]any{"type": "tool_use", "id": "call_00_foreign", "name": "terminal", "input": map[string]any{"command": "ls"}},
			map[string]any{"type": "tool_use", "id": anthropicID, "name": "Read", "input": map[string]any{"path": "a.txt"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "call_00_foreign", "content": "foreign output"},
			map[string]any{"type": "tool_result", "tool_use_id": anthropicID, "content": "anthropic output"},
			map[string]any{"type": "text", "text": "continue"},
		}},
	}

	out := agentSDKTextualizeForeignToolHistory(source)
	assistant := out[0].(map[string]any)["content"].([]any)
	require.Equal(t, "text", assistant[1].(map[string]any)["type"])
	require.Contains(t, assistant[1].(map[string]any)["text"], "[Imported tool call]\nTool: terminal")
	require.NotContains(t, assistant[1].(map[string]any)["text"], "call_00_foreign")
	require.Equal(t, "tool_use", assistant[2].(map[string]any)["type"])
	require.Equal(t, anthropicID, assistant[2].(map[string]any)["id"])

	user := out[1].(map[string]any)["content"].([]any)
	require.Equal(t, "tool_result", user[0].(map[string]any)["type"], "retained tool results must stay before text")
	require.Equal(t, anthropicID, user[0].(map[string]any)["tool_use_id"])
	require.Equal(t, "text", user[1].(map[string]any)["type"])
	require.Contains(t, user[1].(map[string]any)["text"], "[Imported tool result]\nTool: terminal")
	require.Contains(t, user[1].(map[string]any)["text"], "foreign output")
	require.Equal(t, "continue", user[2].(map[string]any)["text"])

	originalAssistant := source[0].(map[string]any)["content"].([]any)
	require.Equal(t, "tool_use", originalAssistant[1].(map[string]any)["type"], "source history is not mutated")
}

func TestAgentSDKDirectAnthropicToolUseIDFormat(t *testing.T) {
	for id, want := range map[string]bool{
		"toolu_01T1VRUqA2rPiR3k5UaBEAcC": true,
		"toolu_012cgM8jxFX4MFSaAXJDxRDa": true,
		"toolu_01":                       false,
		"toolu_01bad-id":                 false,
		"toolu_bdrk_123":                 false,
		"toolu_vrtx_123":                 false,
		"call_00_123":                    false,
		"":                               false,
	} {
		require.Equal(t, want, agentSDKIsDirectAnthropicToolUseID(id), id)
	}
}

type agentSDKLoopHarness struct {
	t       *testing.T
	svc     *GatewayService
	account *Account
	ctx     context.Context
	c       *gin.Context
	head    string
	model   string
	msgs    []string
	turn    int
}

func newAgentSDKLoopHarness(t *testing.T, settings *ClaudeAntiBanSettings, head string) *agentSDKLoopHarness {
	svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
	svc.settingService.claudeAntiBanRuntime.Store(settings)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	return &agentSDKLoopHarness{
		t: t, svc: svc, c: c, head: head, model: "claude-sonnet-5-5",
		account: &Account{ID: 21, Platform: PlatformAnthropic, Type: AccountTypeSetupToken},
		ctx:     WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true}),
		msgs:    []string{`{"role":"user","content":"do it"}`},
	}
}

// send forwards the current history and simulates a successful upstream reply.
func (h *agentSDKLoopHarness) send() []byte {
	h.t.Helper()
	body := h.head + `"messages":[` + strings.Join(h.msgs, ",") + `]}`
	ctx, out, err := h.svc.prepareClaudeAgentSDK(h.ctx, h.c, h.account, []byte(body), h.model)
	require.NoError(h.t, err)
	requireAgentSDKSystemOrder(h.t, out)
	h.turn++
	h.svc.recordClaudeAgentSDKResponse(ctx, fmt.Sprintf("msg_%d", h.turn), fmt.Sprintf("req_%d", h.turn))
	h.svc.recordClaudeAgentSDKUsage(ctx, map[string]any{
		"input_tokens": float64(3), "cache_read_input_tokens": float64(9249), "cache_creation_input_tokens": float64(1000 * h.turn),
	}, true)
	h.svc.recordClaudeAgentSDKUsage(ctx, map[string]any{"output_tokens": float64(200)}, false)
	return out
}

func (h *agentSDKLoopHarness) toolRoundTrip(cacheLatest bool) {
	id := fmt.Sprintf("toolu_01test%d", len(h.msgs))
	result := `{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}`
	if cacheLatest {
		for i, msg := range h.msgs {
			h.msgs[i] = strings.ReplaceAll(msg, `,"cache_control":{"type":"ephemeral"}`, "")
		}
		result = `{"type":"tool_result","tool_use_id":"` + id + `","content":"ok","cache_control":{"type":"ephemeral"}}`
	}
	h.msgs = append(h.msgs,
		`{"role":"assistant","content":[{"type":"tool_use","id":"`+id+`","name":"t","input":{}}]}`,
		`{"role":"user","content":[`+result+`]}`)
}

func agentSDKStrippedMessages(t *testing.T, body []byte) []string {
	t.Helper()
	var messages []any
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(body, "messages").Raw), &messages))
	out := make([]string, len(messages))
	for i, msg := range messages {
		encoded, err := json.Marshal(agentSDKStripCacheControl(msg))
		require.NoError(t, err)
		out[i] = string(encoded)
	}
	return out
}

func agentSDKTotalTokens(body []byte) []string {
	out := []string{}
	for _, msg := range gjson.GetBytes(body, "messages").Array() {
		text := msg.Get("content").String()
		if msg.Get("role").String() == "system" && strings.HasPrefix(text, "<total_tokens>") {
			out = append(out, text)
		}
	}
	return out
}

const agentSDKLoopHead = `{"model":"claude-sonnet-5-5","tools":[{"name":"t","description":"x","input_schema":{"type":"object"}}],` +
	`"metadata":{"user_id":"{\"device_id\":\"d\",\"account_uuid\":\"\",\"session_id\":\"33333333-3333-4333-8333-333333333333\"}"},`

func TestClaudeAgentSDKToolLoopKeepsHistoryPrefixStable(t *testing.T) {
	h := newAgentSDKLoopHarness(t, DefaultClaudeAntiBanSettings(), agentSDKLoopHead)
	previous, previousTokens := []string{}, []string{}
	for i := 0; i < 5; i++ {
		out := h.send()
		current := agentSDKStrippedMessages(t, out)
		require.GreaterOrEqual(t, len(current), len(previous))
		require.Equal(t, previous, current[:len(previous)], "request %d rewrote history", i+1)
		tokens := agentSDKTotalTokens(out)
		require.Equal(t, previousTokens, tokens[:len(previousTokens)])
		if i > 0 {
			require.Len(t, tokens, len(previousTokens)+1)
		}
		previous, previousTokens = current, tokens
		h.toolRoundTrip(true)
	}
	require.Len(t, previousTokens, 4)
	require.NotEqual(t, previousTokens[0], previousTokens[3], "new messages still use the current budget")
}

func TestClaudeAgentSDKNewTurnKeepsHistoryTokens(t *testing.T) {
	h := newAgentSDKLoopHarness(t, DefaultClaudeAntiBanSettings(), agentSDKLoopHead)
	h.send()
	h.toolRoundTrip(false)
	h.send()
	h.toolRoundTrip(false)
	before := agentSDKTotalTokens(h.send())
	require.Len(t, before, 2)
	h.msgs = append(h.msgs,
		`{"role":"assistant","content":[{"type":"text","text":"done"}]}`,
		`{"role":"user","content":"next question"}`)
	after := agentSDKTotalTokens(h.send())
	require.Len(t, after, 3)
	require.Equal(t, before, after[:2])
	require.Equal(t, "<total_tokens>15000000 tokens left</total_tokens>", after[2])
}

func TestClaudeAgentSDKEditedHistoryRecomputesFromChange(t *testing.T) {
	h := newAgentSDKLoopHarness(t, DefaultClaudeAntiBanSettings(), agentSDKLoopHead)
	h.send()
	for i := 0; i < 3; i++ {
		h.toolRoundTrip(false)
		h.send()
	}
	h.toolRoundTrip(false)
	before := agentSDKTotalTokens(h.send())
	require.Len(t, before, 4)

	h.msgs[4] = strings.Replace(h.msgs[4], `"content":"ok"`, `"content":"edited"`, 1)
	after := agentSDKTotalTokens(h.send())
	require.Len(t, after, 4)
	require.Equal(t, before[0], after[0], "messages before the edit keep their value")
	require.NotEqual(t, before[1], after[1], "the edited message takes the current value")
	require.Equal(t, after[1], after[2])
	require.Equal(t, after[1], after[3])
}

func TestClaudeAgentSDKEnvironmentDateIsFixedPerSession(t *testing.T) {
	h := newAgentSDKLoopHarness(t, DefaultClaudeAntiBanSettings(), agentSDKLoopHead)
	body := h.head + `"messages":[` + strings.Join(h.msgs, ",") + `]}`
	ctx, _, err := h.svc.prepareClaudeAgentSDK(h.ctx, h.c, h.account, []byte(body), "claude-sonnet-5-5")
	require.NoError(t, err)
	info := ctx.Value(claudeAgentSDKContextKey{}).(*claudeAgentSDKRequestContext)
	state := info.Pending
	state.MessageID = "msg_1"
	state.EnvironmentDate = "2026-11-04"
	h.svc.saveClaudeAgentSDKState(ctx, info.StateKey, state)

	h.toolRoundTrip(false)
	out := h.send()
	require.True(t, strings.HasSuffix(gjson.GetBytes(out, "messages.1.content").String(), "Today's date is 2026-11-04."))
}

func TestClaudeAgentSDKThreadModeContinuesAcrossToolLoops(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.AgentSDK.ConversationMode = ClaudeAgentSDKConversationThread
	h := newAgentSDKLoopHarness(t, settings, agentSDKLoopHead)
	require.Equal(t, "create", gjson.GetBytes(h.send(), "thread.type").String())
	for i := 0; i < 3; i++ {
		h.toolRoundTrip(true)
		out := h.send()
		require.Equal(t, "continue", gjson.GetBytes(out, "thread.type").String(), "round trip %d", i+1)
		require.Equal(t, fmt.Sprintf("msg_%d", i+1), gjson.GetBytes(out, "thread.previous_message_id").String())
	}
}

func TestClaudeAgentSDKKeepsClientSystemBreakpointAsIs(t *testing.T) {
	h := newAgentSDKLoopHarness(t, DefaultClaudeAntiBanSettings(),
		`{"model":"claude-sonnet-5-5","system":[{"type":"text","text":"CLIENT A"},{"type":"text","text":"CLIENT B","cache_control":{"type":"ephemeral"}}],`)
	out := h.send()
	require.Equal(t, "system", gjson.GetBytes(out, "messages.2.role").String())
	require.Equal(t, "CLIENT A\n\nCLIENT B", gjson.GetBytes(out, "messages.2.content.0.text").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.2.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(out, "messages.2.content.0.cache_control.ttl").Exists(), "client 5m breakpoint is not raised to 1h")
	require.Equal(t, 1, strings.Count(string(out), "cache_control"))
}

func TestClaudeAgentSDKNormalizesClientSystemDateline(t *testing.T) {
	for _, tc := range []struct {
		enabled string
		want    string
	}{
		{"true", "Today's date is 2026-11-05."},
		{"false", "Today’s date is 2026/11/05."},
	} {
		t.Run(tc.enabled, func(t *testing.T) {
			repo := &gatewayTTLSettingRepo{data: map[string]string{SettingKeyEnableClientDatelineNormalization: tc.enabled}}
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
			t.Cleanup(func() { gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{}) })
			svc := &GatewayService{settingService: NewSettingService(repo, &config.Config{}), cache: &agentSDKTestCache{}}
			svc.settingService.claudeAntiBanRuntime.Store(DefaultClaudeAntiBanSettings())
			account := &Account{ID: 22, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
			ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			_, out, err := svc.prepareClaudeAgentSDK(ctx, c, account, []byte(`{
				"model":"claude-sonnet-5-5","system":"Policy. Today’s date is 2026/11/05.",
				"messages":[{"role":"user","content":"hello"}]
			}`), "claude-sonnet-5-5")
			require.NoError(t, err)
			require.Equal(t, "Policy. "+tc.want, gjson.GetBytes(out, "messages.2.content").String())
			require.Contains(t, gjson.GetBytes(out, "messages.1.content").String(), "Today's date is ")
		})
	}
}

func agentSDKFoldedMessages(t *testing.T, messages string, opts agentSDKMessagesOptions) []any {
	t.Helper()
	var source []any
	require.NoError(t, json.Unmarshal([]byte(messages), &source))
	if opts.Model == "" {
		opts.Model = "claude-sonnet-4-6"
	}
	opts.Profile, opts.Inject, opts.EnvironmentDate = agentSDKFixtureProfile, true, "2026-11-06"
	if opts.RemainingTokens == 0 {
		opts.RemainingTokens = claudeAgentSDKTokenBudget
	}
	opts.MidConversationSystem = false
	out, _ := agentSDKMessages(agentSDKNormalizeSourceMessages(source), opts)
	return out
}

func agentSDKBlockTexts(t *testing.T, message any) []string {
	t.Helper()
	var texts []string
	for _, raw := range message.(map[string]any)["content"].([]any) {
		block := raw.(map[string]any)
		if block["type"] == "text" {
			texts = append(texts, block["text"].(string))
		} else {
			texts = append(texts, "["+block["type"].(string)+"]")
		}
	}
	return texts
}

func TestAgentSDKFoldMatchesSonnet46Fixture(t *testing.T) {
	fixture := mustReadAgentSDKFixture(t, "create_no_tools_sonnet46.json")
	var want []any
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(fixture, "messages").Raw), &want))
	clientBlocks := want[0].(map[string]any)["content"].([]any)[4:]
	client, err := json.Marshal([]any{map[string]any{"role": "user", "content": clientBlocks}})
	require.NoError(t, err)

	got := agentSDKFoldedMessages(t, string(client), agentSDKMessagesOptions{})
	require.Equal(t, want, got)
}

func TestAgentSDKFoldMultiTurnMatchesHaikuShape(t *testing.T) {
	out := agentSDKFoldedMessages(t,
		`[{"role":"user","content":"你好"},{"role":"assistant","content":[{"type":"text","text":"ok"}]},{"role":"user","content":"你能读取本地文件吗"}]`,
		agentSDKMessagesOptions{Model: "claude-haiku-4-5-20251001"})
	require.Len(t, out, 3)
	first := agentSDKBlockTexts(t, out[0])
	require.Len(t, first, 5)
	require.True(t, strings.HasPrefix(first[0], "<system-reminder>\n# Environment\n"))
	require.Equal(t, "<system-reminder>\nYou are powered by the model named Haiku 4.5. The exact model ID is claude-haiku-4-5-20251001. Assistant knowledge cutoff is February 2025.\n</system-reminder>", first[1])
	require.Equal(t, "<system-reminder>\n<total_tokens>15000000 tokens left</total_tokens>\n</system-reminder>", first[2])
	require.Equal(t, "<system-reminder>\nToday's date is 2026-11-06.\n</system-reminder>\n", first[3])
	require.Equal(t, "你好", first[4])
	require.Equal(t, []string{
		"<system-reminder>\n<total_tokens>15000000 tokens left</total_tokens>\n</system-reminder>\n", "你能读取本地文件吗",
	}, agentSDKBlockTexts(t, out[2]))
}

func TestAgentSDKFoldWithToolsMatchesHaikuOrder(t *testing.T) {
	out := agentSDKFoldedMessages(t, `[{"role":"user","content":"用一句话解释什么是幂等性。"}]`,
		agentSDKMessagesOptions{Model: "claude-haiku-4-5-20251001", HasTools: true})
	require.Len(t, out, 1)
	texts := agentSDKBlockTexts(t, out[0])
	prefixes := []string{
		"<system-reminder>\n# Environment\n",
		"<system-reminder>\nYou are powered by the model named Haiku 4.5.",
		"<system-reminder>\nThe following deferred tools are now available via ToolSearch.",
		"<system-reminder>\nAvailable agent types for the Agent tool:\n",
		"<system-reminder>\nThe following skills are available for use with the Skill tool:\n",
		"<system-reminder>\n<total_tokens>15000000 tokens left</total_tokens>\n</system-reminder>",
		"<system-reminder>\nToday's date is 2026-11-06.\n</system-reminder>",
		"<system-reminder>\nAttribution for git commits and pull requests",
		"用一句话解释什么是幂等性。",
	}
	require.Len(t, texts, len(prefixes))
	for i, prefix := range prefixes {
		require.True(t, strings.HasPrefix(texts[i], prefix), "block %d: %q", i, texts[i])
	}
	require.True(t, strings.HasSuffix(texts[3], "so they run concurrently.\n</system-reminder>"))
	require.True(t, strings.HasSuffix(texts[6], "</system-reminder>"), "a reminder followed by another reminder gets no newline")
	require.True(t, strings.HasSuffix(texts[7], "</system-reminder>\n"))
}

func TestAgentSDKFoldPlacesRemindersAfterToolResults(t *testing.T) {
	out := agentSDKFoldedMessages(t, `[{"role":"user","content":"do it"},`+
		`{"role":"assistant","content":[{"type":"tool_use","id":"tu","name":"t","input":{}}]},`+
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu","content":"ok"}]}]`,
		agentSDKMessagesOptions{RemainingTokens: 14984604})
	require.Len(t, out, 3)
	require.Equal(t, []string{
		"[tool_result]", "<system-reminder>\n<total_tokens>14984604 tokens left</total_tokens>\n</system-reminder>",
	}, agentSDKBlockTexts(t, out[2]))
}

func TestAgentSDKFoldKeepsClientSystemBreakpoint(t *testing.T) {
	out := agentSDKFoldedMessages(t, `[{"role":"user","content":"hello"}]`, agentSDKMessagesOptions{
		ClientSystem: agentSDKClientSystem{Text: "CLIENT SYSTEM", CacheControl: map[string]any{"type": "ephemeral"}},
	})
	require.Len(t, out, 1)
	content := out[0].(map[string]any)["content"].([]any)
	client := content[4].(map[string]any)
	require.Equal(t, "<system-reminder>\nCLIENT SYSTEM\n</system-reminder>\n", client["text"])
	require.Equal(t, map[string]any{"type": "ephemeral"}, client["cache_control"])
	require.Equal(t, "<system-reminder>\nToday's date is 2026-11-06.\n</system-reminder>", content[3].(map[string]any)["text"])
}

// The production request rejected with "role 'system' is not supported on this
// model": a Claude Code client on Sonnet 4.6 with a top-level system prompt and
// two adjacent user messages.
func TestClaudeAgentSDKSonnet46SendsNoSystemRole(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet-4-6","max_tokens":1024,"stream":true,"thinking":{"type":"disabled"},
		"output_config":{"effort":"high"},
		"metadata":{"user_id":"{\"device_id\":\"device\",\"account_uuid\":\"\",\"session_id\":\"5db67606-2eb6-4413-9158-27732d2d7989\"}"},
		"system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"<system-reminder>\nNotice from the API gateway.\n</system-reminder>"}]},
			{"role":"user","content":[{"type":"text","text":"ping","cache_control":{"type":"ephemeral"}}]}
		]
	}`)
	for _, inject := range []bool{true, false} {
		t.Run(fmt.Sprintf("inject=%v", inject), func(t *testing.T) {
			settings := DefaultClaudeAntiBanSettings()
			settings.AgentSDK.InjectSystemMessages = inject
			svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
			svc.settingService.claudeAntiBanRuntime.Store(settings)
			account := &Account{ID: 24, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
			ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

			ctx, out, err := svc.prepareClaudeAgentSDK(ctx, c, account, input, "claude-sonnet-4-6")
			require.NoError(t, err)
			requireNoAgentSDKSystemRole(t, out)
			require.Len(t, gjson.GetBytes(out, "messages").Array(), 1)
			texts := gjson.GetBytes(out, "messages.0.content.#.text").Array()
			require.Equal(t, "ping", texts[len(texts)-1].String())
			require.Equal(t, "<system-reminder>\nYou are Claude Code, Anthropic's official CLI for Claude.\n</system-reminder>", texts[len(texts)-3].String())
			require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
			require.NotContains(t, claudeAgentSDKBetaHeader(ctx), "mid-conversation-system")

			wire, err := applyClaudeAgentSDKOutputBudget(out, "claude-sonnet-4-6")
			require.NoError(t, err)
			require.Equal(t, int64(32000), gjson.GetBytes(wire, "max_tokens").Int())

			wireSvc := &GatewayService{cache: svc.cache}
			req, wire, err := wireSvc.buildUpstreamRequest(ctx, c, account, wire, "token", "oauth", "claude-sonnet-4-6", true, true)
			require.NoError(t, err)
			require.Equal(t, agentSDKBetas("claude-sonnet-4-6", false), getHeaderRaw(req.Header, "anthropic-beta"))
			requireNoAgentSDKSystemRole(t, wire)
		})
	}
}

func newSonnet46LoopHarness(t *testing.T, settings *ClaudeAntiBanSettings) *agentSDKLoopHarness {
	h := newAgentSDKLoopHarness(t, settings, strings.Replace(agentSDKLoopHead, "claude-sonnet-5-5", "claude-sonnet-4-6", 1))
	h.model = "claude-sonnet-4-6"
	return h
}

func TestClaudeAgentSDKSonnet46ToolLoopKeepsHistoryPrefixStable(t *testing.T) {
	h := newSonnet46LoopHarness(t, DefaultClaudeAntiBanSettings())
	previous := []string{}
	for i := 0; i < 4; i++ {
		out := h.send()
		requireNoAgentSDKSystemRole(t, out)
		current := agentSDKStrippedMessages(t, out)
		require.Len(t, current, 2*i+1)
		require.Equal(t, previous, current[:len(previous)], "request %d rewrote history", i+1)
		if i > 0 {
			require.Contains(t, current[len(current)-1], `"type":"tool_result"`)
			require.Contains(t, current[len(current)-1], "\\u003ctotal_tokens\\u003e")
		}
		previous = current
		h.toolRoundTrip(true)
	}
}

func TestClaudeAgentSDKSonnet46ThreadModeContinuesAcrossToolLoops(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.AgentSDK.ConversationMode = ClaudeAgentSDKConversationThread
	h := newSonnet46LoopHarness(t, settings)
	first := h.send()
	requireNoAgentSDKSystemRole(t, first)
	require.Equal(t, "create", gjson.GetBytes(first, "thread.type").String())
	for i := 0; i < 3; i++ {
		h.toolRoundTrip(true)
		out := h.send()
		requireNoAgentSDKSystemRole(t, out)
		require.Equal(t, "continue", gjson.GetBytes(out, "thread.type").String(), "round trip %d", i+1)
		require.Equal(t, fmt.Sprintf("msg_%d", i+1), gjson.GetBytes(out, "thread.previous_message_id").String())
		require.Len(t, gjson.GetBytes(out, "messages").Array(), 2, "only the new assistant/user pair is sent")
	}
}

func TestAgentSDKBetasMatchCaptures(t *testing.T) {
	const (
		sonnet55NoTools = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,per-turn-control-2026-07-01,advisor-tool-2026-03-01,mid-conversation-system-clear-at-2026-08-21,effort-2025-11-24,thinking-binding-controls-2026-08-01,thinking-display-updates-2026-08-18,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07,message-threads-2026-08-12"
		sonnet55Tools   = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,per-turn-control-2026-07-01,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,mid-conversation-system-clear-at-2026-08-21,effort-2025-11-24,thinking-binding-controls-2026-08-01,thinking-display-updates-2026-08-18,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07,message-threads-2026-08-12"
		sonnet46NoTools = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,effort-2025-11-24,thinking-binding-controls-2026-08-01,thinking-display-updates-2026-08-18,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07,message-threads-2026-08-12"
		haikuNoTools    = "oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,claude-code-20250219,advisor-tool-2026-03-01,thinking-binding-controls-2026-08-01,thinking-display-updates-2026-08-18,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07,message-threads-2026-08-12"
		haikuTools      = "oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,claude-code-20250219,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,thinking-binding-controls-2026-08-01,thinking-display-updates-2026-08-18,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07,message-threads-2026-08-12"
	)
	require.Equal(t, sonnet55NoTools, agentSDKBetas("claude-sonnet-5-5", false))
	require.Equal(t, sonnet55Tools, agentSDKBetas("claude-sonnet-5-5", true))
	require.Equal(t, sonnet46NoTools, agentSDKBetas("claude-sonnet-4-6", false))
	require.Equal(t, haikuNoTools, agentSDKBetas("claude-haiku-4-5-20251001", false))
	require.Equal(t, haikuTools, agentSDKBetas("claude-haiku-4-5-20251001", true))
	require.Equal(t, strings.Join(strings.Fields(claudeAgentSDKBetas), ","), claudeAgentSDKBetaHeader(context.Background()))
}

func TestMarshalClaudeAgentSDKJSONMatchesCapturedKeyOrder(t *testing.T) {
	body, err := marshalClaudeAgentSDKJSON(map[string]any{
		"stream":      true,
		"diagnostics": map[string]any{"previous_message_id": nil},
		"thread":      map[string]any{"previous_message_id": "msg_1", "type": "continue"},
		"output_config": map[string]any{
			"task_budget": 100, "effort": "medium",
		},
		"context_management": map[string]any{
			"edits": []any{map[string]any{"keep": "all", "type": "clear_thinking_20251015"}},
		},
		"thinking":   map[string]any{"display": "updates", "type": "adaptive"},
		"max_tokens": 128000,
		"metadata":   map[string]any{"user_id": "user"},
		"tools": []any{map[string]any{
			"input_schema": map[string]any{"type": "object"},
			"description":  "tool",
			"name":         "Read",
		}},
		"system": []any{map[string]any{
			"cache_control": map[string]any{"ttl": "1h", "type": "ephemeral"},
			"text":          "identity",
			"type":          "text",
		}},
		"messages": []any{
			map[string]any{
				"output_config": map[string]any{"effort": "medium"},
				"content": []any{
					map[string]any{"input": map[string]any{"path": "x"}, "name": "Read", "id": "toolu_1", "type": "tool_use"},
					map[string]any{"content": "ok", "type": "tool_result", "tool_use_id": "toolu_1"},
				},
				"role": "assistant",
			},
		},
		"model": "claude-sonnet-5-5",
	})
	require.NoError(t, err)
	require.Equal(t,
		[]string{"model", "messages", "system", "tools", "metadata", "max_tokens", "thinking", "context_management", "output_config", "thread", "diagnostics", "stream"},
		agentSDKObjectKeys(t, body),
	)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "messages.0").Raw, `"role"`, `"content"`, `"output_config"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "messages.0.content.0").Raw, `"type"`, `"id"`, `"name"`, `"input"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "messages.0.content.1").Raw, `"tool_use_id"`, `"type"`, `"content"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "system.0").Raw, `"type"`, `"text"`, `"cache_control"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "system.0.cache_control").Raw, `"type"`, `"ttl"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "tools.0").Raw, `"name"`, `"description"`, `"input_schema"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "thinking").Raw, `"type"`, `"display"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "context_management.edits.0").Raw, `"type"`, `"keep"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "output_config").Raw, `"effort"`, `"task_budget"`)
	assertJSONTokenOrder(t, gjson.GetBytes(body, "thread").Raw, `"type"`, `"previous_message_id"`)
}

func TestApplyClaudeAgentSDKDefaultEffort(t *testing.T) {
	for _, tc := range []struct {
		name     string
		model    string
		explicit string
		want     string
		exists   bool
	}{
		{name: "sonnet 55 default", model: "claude-sonnet-5-5", want: "medium", exists: true},
		{name: "opus 48 default", model: "claude-opus-4-8", want: "high", exists: true},
		{name: "sonnet 46 default", model: "claude-sonnet-4-6", want: "high", exists: true},
		{name: "explicit wins", model: "claude-sonnet-5-5", explicit: "max", want: "max", exists: true},
		{name: "haiku omitted", model: "claude-haiku-4-5-20251001"},
		{name: "unknown omitted", model: "custom-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := map[string]any{}
			if tc.explicit != "" {
				root["output_config"] = map[string]any{"effort": tc.explicit}
			}
			applyClaudeAgentSDKDefaultEffort(root, tc.model)
			encoded, err := json.Marshal(root)
			require.NoError(t, err)
			got := gjson.GetBytes(encoded, "output_config.effort")
			require.Equal(t, tc.exists, got.Exists())
			require.Equal(t, tc.want, got.String())
		})
	}
}

func TestClaudeAgentSDKPreservesClientEagerInputStreaming(t *testing.T) {
	tools := []any{
		map[string]any{"name": "explicit", "input_schema": map[string]any{}, "eager_input_streaming": false},
		map[string]any{"name": "absent", "input_schema": map[string]any{}},
	}
	body, err := marshalClaudeAgentSDKJSON(map[string]any{"model": "claude-sonnet-5-5", "tools": tools})
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "tools.0.eager_input_streaming").Exists())
	require.False(t, gjson.GetBytes(body, "tools.0.eager_input_streaming").Bool())
	require.False(t, gjson.GetBytes(body, "tools.1.eager_input_streaming").Exists())
}

func TestFormatClaudeAgentSDKMetadataUserIDOrderAndExtras(t *testing.T) {
	raw := `{"tk":"token-index","parent_session_id":"","ti":"turn-index","ignored":"value"}`
	got := formatClaudeAgentSDKMetadataUserID(raw, "device", "", "session")
	assertJSONTokenOrder(t, got,
		`"device_id"`, `"account_uuid"`, `"session_id"`, `"parent_session_id"`, `"ti"`, `"tk"`)
	require.Equal(t, "", gjson.Get(got, "parent_session_id").String())
	require.Equal(t, "turn-index", gjson.Get(got, "ti").String())
	require.Equal(t, "token-index", gjson.Get(got, "tk").String())
	require.False(t, gjson.Get(got, "ignored").Exists())
}

func TestClaudeAgentSDKBuildCanonicalizesSignatureRetryBody(t *testing.T) {
	body, err := marshalClaudeAgentSDKJSON(map[string]any{
		"model": "claude-sonnet-5-5",
		"messages": []any{map[string]any{
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "thinking", "thinking": "preserved reasoning", "signature": "invalid",
			}},
		}},
		"system": []any{map[string]any{
			"type": "text",
			"text": "x-anthropic-billing-header: cc_version=2.1.284.abc; cc_entrypoint=sdk-ts; cch=00000;",
		}},
		"tools":      []any{},
		"metadata":   map[string]any{},
		"max_tokens": 128000,
		"thinking":   map[string]any{"type": "adaptive", "display": "updates"},
		"context_management": map[string]any{
			"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}},
		},
		"output_config": map[string]any{"effort": "medium"},
		"diagnostics":   map[string]any{"previous_message_id": nil},
		"stream":        true,
	})
	require.NoError(t, err)
	filtered := FilterThinkingBlocksForRetry(body, "claude-sonnet-5-5")
	require.False(t, gjson.GetBytes(filtered, "thinking").Exists())

	account := &Account{ID: 77, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
	ctx := WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	svc := &GatewayService{}
	_, wire, err := svc.buildUpstreamRequest(ctx, c, account, filtered, "token", "oauth", "claude-sonnet-5-5", true, true)
	require.NoError(t, err)
	assertJSONTokenOrder(t, gjson.GetBytes(wire, "messages.0").Raw, `"role"`, `"content"`)
	assertJSONTokenOrder(t, gjson.GetBytes(wire, "messages.0.content.0").Raw, `"type"`, `"text"`)
	require.NotContains(t, gjson.GetBytes(wire, "system.0.text").String(), "cch=00000")
}

func TestApplyClaudeAgentSDKOutputBudgetUsesCatalogDefault(t *testing.T) {
	for model, want := range map[string]int64{
		"claude-sonnet-4-6": 32000, "claude-haiku-4-5-20251001": 32000, "claude-opus-4-5": 32000,
		"claude-opus-4-7": 64000, "claude-opus-5": 64000, "claude-sonnet-5": 64000,
		"claude-sonnet-5-5": 128000, "claude-opus-5-5": 128000,
	} {
		input := []byte(`{"model":"` + model + `","max_tokens":1,"messages":[]}`)
		out, err := applyClaudeAgentSDKOutputBudget(input, model)
		require.NoError(t, err)
		require.Equal(t, want, gjson.GetBytes(out, "max_tokens").Int(), model)
		require.Equal(t, []string{"model", "max_tokens", "messages"}, agentSDKObjectKeys(t, out), model)
	}
}

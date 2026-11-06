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
				ClientSystem: agentSDKClientSystem{Text: "CLIENT SYSTEM"},
			})
			require.NotEqual(t, "system", out[0].(map[string]any)["role"])
			got := out[tc.want].(map[string]any)
			require.Equal(t, "system", got["role"])
			require.Equal(t, "CLIENT SYSTEM", got["content"])
		})
	}
	out, _ := agentSDKMessages(nil, agentSDKMessagesOptions{
		Model: "claude-sonnet-5-5", Profile: profile, Inject: true, RemainingTokens: 1000,
		ClientSystem: agentSDKClientSystem{Text: "CLIENT SYSTEM"},
	})
	require.Empty(t, out)
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

type agentSDKLoopHarness struct {
	t       *testing.T
	svc     *GatewayService
	account *Account
	ctx     context.Context
	c       *gin.Context
	head    string
	msgs    []string
	turn    int
}

func newAgentSDKLoopHarness(t *testing.T, settings *ClaudeAntiBanSettings, head string) *agentSDKLoopHarness {
	svc := &GatewayService{settingService: &SettingService{}, cache: &agentSDKTestCache{}}
	svc.settingService.claudeAntiBanRuntime.Store(settings)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	return &agentSDKLoopHarness{
		t: t, svc: svc, c: c, head: head,
		account: &Account{ID: 21, Platform: PlatformAnthropic, Type: AccountTypeSetupToken},
		ctx:     WithClaudeAgentSDKPolicy(context.Background(), &Group{Platform: PlatformAnthropic, SimulateClaudeAgentSDK: true}),
		msgs:    []string{`{"role":"user","content":"do it"}`},
	}
}

// send forwards the current history and simulates a successful upstream reply.
func (h *agentSDKLoopHarness) send() []byte {
	h.t.Helper()
	body := h.head + `"messages":[` + strings.Join(h.msgs, ",") + `]}`
	ctx, out, err := h.svc.prepareClaudeAgentSDK(h.ctx, h.c, h.account, []byte(body), "claude-sonnet-5-5")
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
	id := fmt.Sprintf("tu_%d", len(h.msgs))
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

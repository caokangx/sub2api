package service

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropicfp"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	claudeAgentSDKVersion        = "0.3.310"
	claudeAgentSDKUserAgent      = "claude-cli/" + claude.CLICurrentVersion + " (external, sdk-ts, agent-sdk/" + claudeAgentSDKVersion + ")"
	claudeAgentSDKDispatchID     = "v2d"
	claudeAgentSDKThreadTTL      = time.Hour
	claudeAgentSDKTokenBudget    = 15_000_000
	claudeAgentSDKStateKeyPrefix = "claude-agent-sdk-thread:"
)

//go:embed claude_agent_sdk_profile/0.3.310/identity.txt
var claudeAgentSDKIdentity string

//go:embed claude_agent_sdk_profile/0.3.310/websearch.txt
var claudeAgentSDKWebSearch string

//go:embed claude_agent_sdk_profile/0.3.310/git_attribution.txt
var claudeAgentSDKGitAttribution string

//go:embed claude_agent_sdk_profile/0.3.310/environment_with_tools.txt
var claudeAgentSDKEnvironmentWithTools string

//go:embed claude_agent_sdk_profile/0.3.310/betas.txt
var claudeAgentSDKBetas string

type claudeAgentSDKPolicyKey struct{}
type claudeAgentSDKContextKey struct{}

type claudeAgentSDKRequestContext struct {
	StateKey     string
	RequestClass string
	PromptID     string
	Pending      claudeAgentSDKThreadState
	CreateBody   []byte
}

type claudeAgentSDKThreadState struct {
	MessageID           string `json:"message_id,omitempty"`
	RequestID           string `json:"request_id,omitempty"`
	HistoryHash         string `json:"history_hash,omitempty"`
	MessageCount        int    `json:"message_count,omitempty"`
	SourceMessageCount  int    `json:"source_message_count,omitempty"`
	ToolsHash           string `json:"tools_hash,omitempty"`
	Model               string `json:"model,omitempty"`
	PromptID            string `json:"prompt_id,omitempty"`
	TurnIndex           int    `json:"turn_index,omitempty"`
	ContextTokens       int64  `json:"context_tokens,omitempty"`
	ResponseInputTokens int64  `json:"response_input_tokens,omitempty"`
	TurnBaseTokens      int64  `json:"turn_base_tokens,omitempty"`
	LastRequestTime     int64  `json:"last_request_time,omitempty"`
	// InjectedTokens[k] is the <total_tokens> value assigned to the k-th source
	// user message. History must be replayed byte-for-byte or the upstream
	// prompt cache prefix stops matching.
	InjectedTokens  []agentSDKInjectedTokens `json:"injected_tokens,omitempty"`
	EnvironmentDate string                   `json:"environment_date,omitempty"`
}

type agentSDKInjectedTokens struct {
	Hash   string `json:"h"`
	Tokens int64  `json:"t"`
}

type agentSDKClientSystem struct {
	Text         string
	CacheControl map[string]any
}

type agentSDKMessagesOptions struct {
	Model           string
	Profile         ClaudeEnvironmentProfile
	HasTools        bool
	Inject          bool
	RemainingTokens int64
	EnvironmentDate string
	History         []agentSDKInjectedTokens
	ClientSystem    agentSDKClientSystem
}

func WithClaudeAgentSDKPolicy(ctx context.Context, group *Group) context.Context {
	enabled := group != nil && group.Platform == PlatformAnthropic && group.SimulateClaudeAgentSDK
	return context.WithValue(ctx, claudeAgentSDKPolicyKey{}, enabled)
}

func simulateClaudeAgentSDK(ctx context.Context, account *Account) bool {
	if ctx == nil || account == nil || account.Platform != PlatformAnthropic || account.Type != AccountTypeSetupToken {
		return false
	}
	enabled, _ := ctx.Value(claudeAgentSDKPolicyKey{}).(bool)
	return enabled
}

func (s *GatewayService) prepareClaudeAgentSDK(ctx context.Context, c *gin.Context, account *Account, body []byte, model string) (context.Context, []byte, error) {
	settings, err := s.settingService.GetClaudeAntiBanSettings(ctx)
	if err != nil {
		return ctx, nil, fmt.Errorf("load Claude Agent SDK settings: %w", err)
	}
	profile := settings.Environment.Profile
	ctx = context.WithValue(ctx, claudeCodeProfileEnvironmentKey{}, &profile)

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return ctx, nil, fmt.Errorf("Claude Agent SDK profile requires a JSON object: %w", err)
	}

	clientSystem := agentSDKClientSystemFrom(root["system"])
	if strings.Contains(clientSystem.Text, " date is ") && s.shouldNormalizeClientDateline(ctx, account) {
		clientSystem.Text, _ = anthropicfp.NormalizeText(clientSystem.Text)
	}
	rawMessages, _ := root["messages"].([]any)
	messages := agentSDKNormalizeSourceMessages(rawMessages)
	tools, _ := root["tools"].([]any)
	hasTools := len(tools) > 0

	requestClass := agentSDKRequestClass(c, body)
	rawSession := agentSDKRawSessionID(c, root)
	stateKey := agentSDKStateKey(account.ID, rawSession+"::"+requestClass)
	state := s.loadClaudeAgentSDKState(ctx, stateKey)
	newTurn := agentSDKStartsNewTurn(messages, state.SourceMessageCount)
	if state.PromptID == "" || newTurn {
		state.PromptID = uuid.NewString()
		if state.TurnIndex == 0 {
			state.TurnIndex = 1
		} else {
			state.TurnIndex++
		}
		state.TurnBaseTokens = state.ContextTokens
	}

	billing := buildClaudeAgentSDKBilling(body, requestClass, state)
	system := []any{
		map[string]any{"type": "text", "text": billing},
		map[string]any{"type": "text", "text": strings.TrimSpace(claudeAgentSDKIdentity)},
	}
	if hasTools {
		system = append(system, map[string]any{
			"type": "text",
			"text": strings.TrimSpace(claudeAgentSDKWebSearch),
		})
	}
	root["system"] = system

	remainingTokens := int64(claudeAgentSDKTokenBudget)
	if !newTurn && state.ContextTokens > state.TurnBaseTokens {
		remainingTokens -= state.ContextTokens - state.TurnBaseTokens
		if remainingTokens < 0 {
			remainingTokens = 0
		}
	}
	environmentDate := state.EnvironmentDate
	if environmentDate == "" {
		environmentDate = time.Now().Format("2006-01-02")
	}
	transformed, injectedTokens := agentSDKMessages(messages, agentSDKMessagesOptions{
		Model:           model,
		Profile:         profile,
		HasTools:        hasTools,
		Inject:          settings.AgentSDK.InjectSystemMessages,
		RemainingTokens: remainingTokens,
		EnvironmentDate: environmentDate,
		History:         state.InjectedTokens,
		ClientSystem:    clientSystem,
	})
	root["messages"] = transformed
	root["context_management"] = map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}}
	delete(root, "temperature")
	delete(root, "tool_choice")
	agentSDKNormalizeThinking(root, model)

	if metadata, ok := root["metadata"].(map[string]any); ok {
		if raw, ok := metadata["user_id"].(string); ok {
			if parsed := ParseMetadataUserID(raw); parsed != nil {
				metadata["user_id"] = FormatMetadataUserID(parsed.DeviceID, "", parsed.SessionID, claude.CLICurrentVersion)
			}
		}
	}

	fullTools, _ := json.Marshal(tools)
	next := state
	next.HistoryHash = agentSDKMessagesHash(transformed)
	next.MessageCount = len(transformed)
	next.SourceMessageCount = len(messages)
	next.ToolsHash = agentSDKHash(fullTools)
	next.Model = model
	next.LastRequestTime = time.Now().Unix()
	next.InjectedTokens = injectedTokens
	next.EnvironmentDate = environmentDate

	mode := settings.AgentSDK.ConversationMode
	createRoot := make(map[string]any, len(root)+1)
	for key, value := range root {
		createRoot[key] = value
	}
	createRoot["thread"] = map[string]any{"type": "create"}
	createBody, _ := marshalProfileJSON(createRoot)
	if mode == ClaudeAgentSDKConversationThread {
		prefixMatches := state.MessageCount > 0 && state.Model == model &&
			state.MessageCount <= len(transformed) &&
			agentSDKMessagePrefixHash(transformed, state.MessageCount) == state.HistoryHash &&
			state.MessageID != ""
		if prefixMatches {
			root["thread"] = map[string]any{"type": "continue", "previous_message_id": state.MessageID}
			root["messages"] = transformed[state.MessageCount:]
			root["system"] = []any{system[0]}
			if state.ToolsHash == next.ToolsHash {
				delete(root, "tools")
			}
		} else {
			root["thread"] = map[string]any{"type": "create"}
		}
	} else {
		delete(root, "thread")
		if state.MessageID != "" {
			root["diagnostics"] = map[string]any{"previous_message_id": state.MessageID}
		} else {
			root["diagnostics"] = map[string]any{"previous_message_id": nil}
		}
	}

	ctx = context.WithValue(ctx, claudeAgentSDKContextKey{}, &claudeAgentSDKRequestContext{
		StateKey: stateKey, RequestClass: requestClass, PromptID: state.PromptID, Pending: next, CreateBody: createBody,
	})
	encoded, err := marshalProfileJSON(root)
	if err != nil {
		return ctx, nil, err
	}
	return ctx, enforceCacheControlLimit(encoded), nil
}

// agentSDKClientSystemFrom flattens the caller's system prompt and keeps the
// caller's last breakpoint so it can follow the text into messages.
func agentSDKClientSystemFrom(value any) agentSDKClientSystem {
	switch v := value.(type) {
	case string:
		return agentSDKClientSystem{Text: strings.TrimSpace(v)}
	case []any:
		var out []string
		var cacheControl map[string]any
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			if cc, ok := block["cache_control"].(map[string]any); ok {
				cacheControl = cc
			}
			if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" &&
				!strings.HasPrefix(text, "x-anthropic-billing-header:") {
				out = append(out, text)
			}
		}
		text := strings.TrimSpace(strings.Join(out, "\n\n"))
		if text == "" {
			cacheControl = nil
		}
		return agentSDKClientSystem{Text: text, CacheControl: cacheControl}
	default:
		return agentSDKClientSystem{}
	}
}

// agentSDKNormalizeSourceMessages reshapes client history into what the Agent
// SDK itself sends: no two adjacent user messages. The upstream only accepts a
// content-bearing system message right before an assistant message or at the
// end of the array, and agentSDKMessages injects one after every user message.
//   - Empty system messages are dropped: the SDK beta set has no
//     mid-conversation-output-config, so the directive form is stripped before
//     sending anyway, which would otherwise rejoin the user messages around it.
//   - A content-bearing system message followed by a user message becomes a
//     <system-reminder> block at the start of that user message.
//   - Adjacent user messages are merged into one block array.
//
// Messages that need no change keep their original value.
func agentSDKNormalizeSourceMessages(messages []any) []any {
	kept := make([]any, 0, len(messages))
	for _, raw := range messages {
		if msg, ok := raw.(map[string]any); ok && msg["role"] == "system" && agentSDKSystemText(msg["content"]) == "" {
			continue
		}
		kept = append(kept, raw)
	}

	out := make([]any, 0, len(kept))
	var reminders []any
	for i, raw := range kept {
		msg, _ := raw.(map[string]any)
		switch msg["role"] {
		case "system":
			if agentSDKNextNonSystemRole(kept, i+1) == "user" {
				reminders = append(reminders, map[string]any{
					"type": "text",
					"text": "<system-reminder>\n" + agentSDKSystemText(msg["content"]) + "\n</system-reminder>",
				})
				continue
			}
		case "user":
			var previous map[string]any
			if len(out) > 0 {
				if last, ok := out[len(out)-1].(map[string]any); ok && last["role"] == "user" {
					previous = last
				}
			}
			if previous == nil && len(reminders) == 0 {
				break
			}
			var content []any
			if previous != nil {
				content = append(content, agentSDKContentBlocks(previous["content"])...)
			}
			content = append(content, reminders...)
			content = append(content, agentSDKContentBlocks(msg["content"])...)
			reminders = nil
			base := msg
			if previous != nil {
				base = previous
				out = out[:len(out)-1]
			}
			merged := make(map[string]any, len(base))
			for key, value := range base {
				merged[key] = value
			}
			merged["content"] = content
			out = append(out, merged)
			continue
		}
		out = append(out, raw)
	}
	return out
}

func agentSDKNextNonSystemRole(messages []any, start int) any {
	for _, raw := range messages[start:] {
		if msg, ok := raw.(map[string]any); !ok || msg["role"] != "system" {
			return msg["role"]
		}
	}
	return nil
}

func agentSDKSystemText(content any) string {
	switch value := content.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		var parts []string
		for _, item := range value {
			if block, ok := item.(map[string]any); ok {
				if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	}
	return ""
}

func agentSDKContentBlocks(content any) []any {
	switch value := content.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": value}}
	case []any:
		return value
	}
	return nil
}

func agentSDKMessages(messages []any, opts agentSDKMessagesOptions) ([]any, []agentSDKInjectedTokens) {
	out := make([]any, 0, len(messages)*2+1)
	var assigned []agentSDKInjectedTokens
	firstUser := true
	historyMatches := true
	for i, raw := range messages {
		out = append(out, raw)
		msg, _ := raw.(map[string]any)
		if !opts.Inject || msg["role"] != "user" {
			continue
		}
		ordinal := len(assigned)
		entry := agentSDKInjectedTokens{Hash: agentSDKContentHash(raw), Tokens: opts.RemainingTokens}
		if historyMatches && ordinal < len(opts.History) && opts.History[ordinal].Hash == entry.Hash {
			entry.Tokens = opts.History[ordinal].Tokens
		} else {
			historyMatches = false
		}
		assigned = append(assigned, entry)
		// Do not duplicate an Agent SDK control message already supplied after
		// this user message. An unrelated client system message is preserved and
		// does not suppress the SDK environment/token message.
		if i+1 < len(messages) {
			if next, ok := messages[i+1].(map[string]any); ok && agentSDKControlSystemMessage(next) {
				firstUser = false
				continue
			}
		}
		text := ""
		if firstUser {
			text = renderAgentSDKEnvironment(opts.Profile, opts.Model, opts.HasTools, opts.EnvironmentDate)
			firstUser = false
		} else {
			text = fmt.Sprintf("<total_tokens>%d tokens left</total_tokens>", entry.Tokens)
		}
		out = append(out, map[string]any{"role": "system", "content": text})
	}
	if opts.ClientSystem.Text != "" {
		out = agentSDKInsertClientSystem(out, opts.ClientSystem)
	}
	if opts.HasTools {
		agentSDKPrependGitAttribution(out)
	}
	return out, assigned
}

// agentSDKContentHash identifies a message regardless of where the client put
// its cache breakpoints, which clients move to the newest message every turn.
func agentSDKContentHash(value any) string {
	encoded, _ := json.Marshal(value)
	var decoded any
	if json.Unmarshal(encoded, &decoded) != nil {
		return agentSDKHash(encoded)[:16]
	}
	encoded, _ = json.Marshal(agentSDKStripCacheControl(decoded))
	return agentSDKHash(encoded)[:16]
}

func agentSDKStripCacheControl(value any) any {
	switch v := value.(type) {
	case map[string]any:
		delete(v, "cache_control")
		for key, item := range v {
			v[key] = agentSDKStripCacheControl(item)
		}
	case []any:
		for i, item := range v {
			v[i] = agentSDKStripCacheControl(item)
		}
	}
	return value
}

// agentSDKInsertClientSystem places the caller's system prompt after the first
// user message and the system messages that follow it. The upstream rejects a
// content-bearing system message at messages[0].
func agentSDKInsertClientSystem(messages []any, clientSystem agentSDKClientSystem) []any {
	if len(messages) == 0 {
		return messages
	}
	position := len(messages)
	for i, raw := range messages {
		if msg, ok := raw.(map[string]any); ok && msg["role"] == "user" {
			position = i + 1
			break
		}
	}
	for position < len(messages) {
		msg, ok := messages[position].(map[string]any)
		if !ok || msg["role"] != "system" {
			break
		}
		position++
	}
	var content any = clientSystem.Text
	if clientSystem.CacheControl != nil {
		content = []any{map[string]any{"type": "text", "text": clientSystem.Text, "cache_control": clientSystem.CacheControl}}
	}
	out := make([]any, 0, len(messages)+1)
	out = append(out, messages[:position]...)
	out = append(out, map[string]any{"role": "system", "content": content})
	return append(out, messages[position:]...)
}

func agentSDKControlSystemMessage(message map[string]any) bool {
	if message["role"] != "system" {
		return false
	}
	content := ""
	switch value := message["content"].(type) {
	case string:
		content = value
	case []any:
		for _, item := range value {
			if block, ok := item.(map[string]any); ok {
				content += fmt.Sprint(block["text"])
			}
		}
	}
	return strings.Contains(content, "# Environment\nYou have been invoked") ||
		strings.Contains(content, "<total_tokens>")
}

func agentSDKPrependGitAttribution(messages []any) {
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		block := map[string]any{"type": "text", "text": strings.TrimSpace(claudeAgentSDKGitAttribution)}
		switch content := msg["content"].(type) {
		case string:
			msg["content"] = []any{block, map[string]any{"type": "text", "text": content}}
		case []any:
			msg["content"] = append([]any{block}, content...)
		}
		return
	}
}

func renderAgentSDKEnvironment(profile ClaudeEnvironmentProfile, model string, hasTools bool, date string) string {
	base := renderProfileEnvironment(profile) + "\n\n" + claude.ModelIdentityText(model)
	if hasTools {
		template := claudeAgentSDKEnvironmentWithTools
		if idx := strings.Index(template, "The following deferred tools"); idx >= 0 {
			suffix := template[idx:]
			if date := strings.Index(suffix, "Today's date is "); date >= 0 {
				suffix = suffix[:date]
			}
			base += "\n\n" + strings.TrimSpace(suffix)
		}
	}
	return strings.TrimSpace(base) +
		fmt.Sprintf("\n\n<total_tokens>%d tokens left</total_tokens>\n\nToday's date is %s.",
			claudeAgentSDKTokenBudget, date)
}

func agentSDKNormalizeThinking(root map[string]any, model string) {
	normalized := claude.NormalizeModelID(model)
	_, upper := claude.OutputBudget(model)
	if strings.Contains(normalized, "haiku-4-5") {
		root["thinking"] = map[string]any{"type": "enabled", "budget_tokens": min(31999, upper-1), "display": "updates"}
		return
	}
	if strings.Contains(normalized, "claude-sonnet-5") || strings.Contains(normalized, "claude-opus-5") ||
		strings.Contains(normalized, "sonnet-4-6") || strings.Contains(normalized, "opus-4-6") ||
		strings.Contains(normalized, "opus-4-7") || strings.Contains(normalized, "opus-4-8") {
		root["thinking"] = map[string]any{"type": "adaptive", "display": "updates"}
		return
	}
	root["thinking"] = map[string]any{"type": "enabled", "budget_tokens": min(31999, upper-1), "display": "updates"}
}

func applyClaudeAgentSDKOutputBudget(body []byte, model string) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	_, upper := claude.OutputBudget(model)
	switch {
	case strings.Contains(claude.NormalizeModelID(model), "haiku-4-5"):
		root["max_tokens"] = 32000
	case upper >= 128000:
		root["max_tokens"] = 128000
	default:
		root["max_tokens"] = upper
	}
	return marshalProfileJSON(root)
}

func agentSDKRequestClass(c *gin.Context, body []byte) string {
	if c != nil {
		switch strings.ToLower(strings.TrimSpace(c.GetHeader("x-claude-code-request-class"))) {
		case "subagent", "compaction":
			return strings.ToLower(strings.TrimSpace(c.GetHeader("x-claude-code-request-class")))
		}
	}
	if strings.Contains(gjson.GetBytes(body, "system").String(), "cc_is_subagent=true") ||
		strings.Contains(string(body), "cc_is_subagent=true") {
		return "subagent"
	}
	return "main"
}

func buildClaudeAgentSDKBilling(body []byte, requestClass string, state claudeAgentSDKThreadState) string {
	sample := firstUserTextForBilling(body)
	fp := computeClaudeCodeFingerprintForText(sample, claude.CLICurrentVersion)
	text := fmt.Sprintf("x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=sdk-ts; cch=00000;",
		claude.CLICurrentVersion, fp)
	switch requestClass {
	case "subagent":
		text += " cc_is_subagent=true;"
		if state.RequestID != "" {
			text += " cc_prev_req=" + state.RequestID + ";"
		}
		text += " cc_prompt_id=" + state.PromptID + ";"
	case "compaction":
		if state.RequestID != "" {
			text += " cc_prev_req=" + state.RequestID + ";"
		}
	default:
		if state.RequestID != "" {
			text += " cc_prev_req=" + state.RequestID + ";"
		}
		text += " cc_prompt_id=" + state.PromptID +
			"; cc_turn_origin=sdk; cc_prompt_index=0; cc_turn_index=" + fmt.Sprintf("%d", state.TurnIndex) + ";"
	}
	return text
}

func agentSDKRawSessionID(c *gin.Context, root map[string]any) string {
	if metadata, ok := root["metadata"].(map[string]any); ok {
		if raw, ok := metadata["user_id"].(string); ok {
			if parsed := ParseMetadataUserID(raw); parsed != nil && parsed.SessionID != "" {
				return parsed.SessionID
			}
		}
	}
	if c != nil {
		if value := c.GetHeader("X-Claude-Code-Session-Id"); value != "" {
			return value
		}
	}
	encoded, _ := marshalProfileJSON(root)
	return agentSDKHash([]byte(firstUserTextForBilling(encoded)))
}

func agentSDKStateKey(accountID int64, session string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", accountID, session)))
	return claudeAgentSDKStateKeyPrefix + hex.EncodeToString(sum[:])
}

func (s *GatewayService) loadClaudeAgentSDKState(ctx context.Context, key string) claudeAgentSDKThreadState {
	if s == nil || s.cache == nil || key == "" {
		return claudeAgentSDKThreadState{}
	}
	raw, err := s.cache.GetReasoningContent(ctx, key)
	if err != nil || raw == "" {
		return claudeAgentSDKThreadState{}
	}
	var state claudeAgentSDKThreadState
	_ = json.Unmarshal([]byte(raw), &state)
	return state
}

func (s *GatewayService) saveClaudeAgentSDKState(ctx context.Context, key string, state claudeAgentSDKThreadState) {
	if s == nil || s.cache == nil || key == "" {
		return
	}
	raw, _ := json.Marshal(state)
	_ = s.cache.SetReasoningContent(ctx, key, string(raw), claudeAgentSDKThreadTTL)
}

func (s *GatewayService) recordClaudeAgentSDKResponse(ctx context.Context, messageID, requestID string) {
	info, _ := ctx.Value(claudeAgentSDKContextKey{}).(*claudeAgentSDKRequestContext)
	if info == nil || info.StateKey == "" || messageID == "" {
		return
	}
	next := info.Pending
	next.MessageID = messageID
	next.RequestID = requestID
	s.saveClaudeAgentSDKState(ctx, info.StateKey, next)
}

func (s *GatewayService) recordClaudeAgentSDKUsage(ctx context.Context, usage map[string]any, messageStart bool) {
	info, _ := ctx.Value(claudeAgentSDKContextKey{}).(*claudeAgentSDKRequestContext)
	if info == nil || len(usage) == 0 {
		return
	}
	state := s.loadClaudeAgentSDKState(ctx, info.StateKey)
	number := func(key string) int64 {
		switch value := usage[key].(type) {
		case float64:
			return int64(value)
		case json.Number:
			n, _ := value.Int64()
			return n
		case int64:
			return value
		case int:
			return int64(value)
		}
		return 0
	}
	if messageStart {
		state.ResponseInputTokens = number("input_tokens") + number("cache_read_input_tokens") + number("cache_creation_input_tokens")
		state.ContextTokens = state.ResponseInputTokens
	}
	if output := number("output_tokens"); output > 0 {
		state.ContextTokens = state.ResponseInputTokens + output
	}
	s.saveClaudeAgentSDKState(ctx, info.StateKey, state)
}

func agentSDKStartsNewTurn(messages []any, previousCount int) bool {
	start := previousCount
	if start < 0 || start > len(messages) {
		start = 0
	}
	for _, raw := range messages[start:] {
		msg, _ := raw.(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		content, ok := msg["content"].([]any)
		if !ok {
			return true
		}
		for _, item := range content {
			block, _ := item.(map[string]any)
			if block["type"] != "tool_result" {
				return true
			}
		}
	}
	return previousCount == 0
}

func agentSDKMessagePrefixHash(messages []any, count int) string {
	if count < 0 || count > len(messages) {
		return ""
	}
	return agentSDKMessagesHash(messages[:count])
}

func agentSDKMessagesHash(messages []any) string {
	normalized := make([]any, len(messages))
	for i, raw := range messages {
		encoded, _ := json.Marshal(raw)
		var msg map[string]any
		if json.Unmarshal(encoded, &msg) != nil {
			normalized[i] = raw
			continue
		}
		agentSDKStripCacheControl(msg)
		if msg["role"] == "system" {
			if content, ok := msg["content"].([]any); ok && len(content) == 1 {
				if block, ok := content[0].(map[string]any); ok && block["type"] == "text" {
					msg["content"] = block["text"]
				}
			}
		}
		normalized[i] = msg
	}
	raw, _ := json.Marshal(normalized)
	return agentSDKHash(raw)
}

func agentSDKHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func anthropicUpstreamRequestID(headers http.Header) string {
	if value := headers.Get("request-id"); value != "" {
		return value
	}
	return headers.Get("x-request-id")
}

func applyClaudeAgentSDKHeaders(ctx context.Context, account *Account, headers http.Header, stream bool) {
	if !simulateClaudeAgentSDK(ctx, account) {
		return
	}
	setHeaderRaw(headers, "User-Agent", claudeAgentSDKUserAgent)
	setHeaderRaw(headers, "x-app", "cli")
	setHeaderRaw(headers, "anthropic-dangerous-direct-browser-access", "true")
	setHeaderRaw(headers, "anthropic-dispatch-id", claudeAgentSDKDispatchID)
	setHeaderRaw(headers, "anthropic-beta", strings.Join(strings.Fields(claudeAgentSDKBetas), ","))
	if info, _ := ctx.Value(claudeAgentSDKContextKey{}).(*claudeAgentSDKRequestContext); info != nil {
		setHeaderRaw(headers, "x-claude-code-request-class", info.RequestClass)
		setHeaderRaw(headers, "x-claude-code-prompt-id", info.PromptID)
	}
	setHeaderRaw(headers, "X-Stainless-Lang", "js")
	setHeaderRaw(headers, "X-Stainless-Retry-Count", "0")
	setHeaderRaw(headers, "X-Stainless-Timeout", "600")
	if stream {
		setHeaderRaw(headers, "x-stainless-helper-method", "stream")
	}
}

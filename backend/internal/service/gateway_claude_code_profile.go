package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// These versioned resources are independent of the caller's tools and system.
//
//go:embed claude_code_profile/2.1.284/system.txt
var claudeCodeProfileSystem string

//go:embed claude_code_profile/2.1.284/tools.json
var claudeCodeProfileTools []byte

type claudeCodeProfilePolicyKey struct{}
type claudeCodeProfileEnvironmentKey struct{}

// WithClaudeCodeProfilePolicy must be called with the scheduler's final group.
// Writing false also clears a previous attempt's policy after group failover.
func WithClaudeCodeProfilePolicy(ctx context.Context, group *Group) context.Context {
	enabled := group != nil && group.Platform == PlatformAnthropic && group.ForceClaudeCodeProfile
	return context.WithValue(ctx, claudeCodeProfilePolicyKey{}, enabled)
}

func forceClaudeCodeProfile(ctx context.Context, account *Account) bool {
	if ctx == nil || account == nil || account.Platform != PlatformAnthropic || !account.IsOAuth() {
		return false
	}
	enabled, _ := ctx.Value(claudeCodeProfilePolicyKey{}).(bool)
	return enabled
}

func (s *GatewayService) prepareClaudeCodeProfile(ctx context.Context, body []byte, model string) (context.Context, []byte, error) {
	settings, err := s.settingService.GetClaudeAntiBanSettings(ctx)
	if err != nil {
		return ctx, nil, fmt.Errorf("load Claude Code environment profile: %w", err)
	}
	profile := settings.Environment.Profile
	ctx = context.WithValue(ctx, claudeCodeProfileEnvironmentKey{}, &profile)
	// Native preprocessing retains safeguards outside the inference body. Forced
	// profiles explicitly forward that caller-provided context after applying the
	// same environment profile as system and outbound headers. Do not invent a
	// safeguards field when the caller did not supply one.
	if !gjson.GetBytes(body, "safeguards").Exists() {
		if safeguards := claudeSafeguardsFromContext(ctx); len(safeguards) > 0 {
			body, err = sjson.SetRawBytes(body, "safeguards", safeguards)
			if err != nil {
				return ctx, nil, fmt.Errorf("restore Claude safeguards: %w", err)
			}
		}
	}
	next, err := rewriteClaudeCodeProfile(body, model, claudeBillingSampleFromContext(ctx, body), profile)
	return ctx, next, err
}

// rewriteClaudeCodeProfile replaces system/tools/tool_choice and the existing
// safeguards environment, and aligns temperature with the CLI's model/thinking
// rules. Historical tool calls and tool results retain their names.
func rewriteClaudeCodeProfile(body []byte, model, billingSample string, profile ClaudeEnvironmentProfile) ([]byte, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, fmt.Errorf("Claude Code profile requires a JSON object")
	}
	billing, err := buildBillingAttributionTextForSample(billingSample)
	if err != nil {
		return nil, err
	}
	billing = mergeClaudeBillingOptional(billing, body)
	blocks := []anthropicSystemTextBlockPayload{
		{Type: "text", Text: billing},
		{Type: "text", Text: claudeCodeSystemPrompt},
	}
	// Fable rejects the expanded harness; keep its existing identity-only policy.
	fable := isAnthropicFableModel(claude.NormalizeModelID(model))
	if !fable {
		text := strings.ReplaceAll(strings.TrimSpace(claudeCodeProfileSystem), "{{MEMORY_DIRECTORY}}", profile.MemoryDirectory)
		blocks = append(blocks,
			anthropicSystemTextBlockPayload{Type: "text", Text: text, CacheControl: &anthropicCacheControlPayload{Type: "ephemeral"}},
		)
	}
	system, err := marshalProfileJSON(blocks)
	if err != nil {
		return nil, err
	}
	tools, err := claudeCodeProfileToolsWithClientMCP(body)
	if err != nil {
		return nil, err
	}
	next, err := sjson.SetRawBytes(body, "system", system)
	if err != nil {
		return nil, err
	}
	next, err = sjson.SetRawBytes(next, "tools", tools)
	if err != nil {
		return nil, err
	}
	if !fable {
		next, err = injectClaudeCodeProfileAttachments(next, model, profile)
		if err != nil {
			return nil, err
		}
	}
	next, err = sjson.DeleteBytes(next, "tool_choice")
	if err != nil {
		return nil, err
	}
	// Reuse the Anti-Ban safeguard renderer without reserializing message history.
	if raw := gjson.GetBytes(next, "safeguards"); raw.Exists() {
		var safeguards any
		if err := json.Unmarshal([]byte(raw.Raw), &safeguards); err != nil {
			return nil, err
		}
		wrapper := map[string]any{"safeguards": safeguards}
		if rewriteSafeguardEnvironment(wrapper, profile) {
			next, err = sjson.SetBytes(next, "safeguards", wrapper["safeguards"])
			if err != nil {
				return nil, err
			}
		}
	}
	next, _ = normalizeClaudeCodeTemperature(next, model)
	return enforceCacheControlLimit(applyToolsLastCacheBreakpoint(next)), nil
}

func applyClaudeCodeProfileOutputBudget(body []byte, model string) ([]byte, error) {
	defaultTokens, upperTokens := claude.OutputBudget(model)
	requested := defaultTokens
	if budget := gjson.GetBytes(body, "thinking.budget_tokens"); gjson.GetBytes(body, "thinking.type").String() == "enabled" && budget.Exists() {
		if budget.Type != gjson.Number || budget.Float() != float64(budget.Int()) || budget.Float() < 1024 || budget.Float() >= float64(upperTokens) {
			return nil, fmt.Errorf("thinking.budget_tokens must be an integer from 1024 to less than model output limit %d", upperTokens)
		}
		value := int(budget.Int())
		if value > 0 && value+1 > requested {
			requested = value + 1
		}
		if value > upperTokens {
			return nil, fmt.Errorf("thinking.budget_tokens %d exceeds model max_tokens limit %d", value, upperTokens)
		}
	}
	if requested > upperTokens {
		return nil, fmt.Errorf("thinking budget requires max_tokens %d, exceeding model limit %d", requested, upperTokens)
	}
	next, err := sjson.SetBytes(body, "max_tokens", requested)
	if err != nil {
		return nil, err
	}
	return next, nil
}

// claudeCodeProfileToolsWithClientMCP appends the caller's mcp__ tools, sorted and
// deduplicated by name, after the fixed built-in set. defer_loading is dropped
// because the fixed set has no ToolSearch, and cache_control is dropped so the
// breakpoint is placed once by applyToolsLastCacheBreakpoint.
func claudeCodeProfileToolsWithClientMCP(body []byte) ([]byte, error) {
	mcp := map[string]json.RawMessage{}
	var names []string
	var walkErr error
	gjson.GetBytes(body, "tools").ForEach(func(_, tool gjson.Result) bool {
		name := tool.Get("name").String()
		if !strings.HasPrefix(name, "mcp__") {
			return true
		}
		if _, dup := mcp[name]; dup {
			return true
		}
		raw := []byte(tool.Raw)
		for _, field := range []string{"cache_control", "defer_loading"} {
			if raw, walkErr = sjson.DeleteBytes(raw, field); walkErr != nil {
				return false
			}
		}
		mcp[name] = raw
		names = append(names, name)
		return true
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(names) == 0 {
		return claudeCodeProfileTools, nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(claudeCodeProfileTools, &tools); err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		tools = append(tools, mcp[name])
	}
	return marshalProfileJSON(tools)
}

func applyClaudeCodeProfileHeaders(ctx context.Context, account *Account, headers http.Header) {
	if forceClaudeCodeProfile(ctx, account) {
		setHeaderRaw(headers, "User-Agent", claude.DefaultUserAgent())
		applyClaudeEnvironmentHeadersFromContext(ctx, headers)
	}
}

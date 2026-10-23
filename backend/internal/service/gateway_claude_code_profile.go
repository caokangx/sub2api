package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
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
	if !isAnthropicFableModel(claude.NormalizeModelID(model)) {
		text := strings.ReplaceAll(strings.TrimSpace(claudeCodeProfileSystem), "{{MEMORY_DIRECTORY}}", profile.MemoryDirectory)
		blocks = append(blocks,
			anthropicSystemTextBlockPayload{Type: "text", Text: text, CacheControl: &anthropicCacheControlPayload{Type: "ephemeral"}},
			anthropicSystemTextBlockPayload{Type: "text", Text: renderClaudeCodeProfileEnvironment(profile), CacheControl: &anthropicCacheControlPayload{Type: "ephemeral"}},
		)
	}
	system, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	next, err := sjson.SetRawBytes(body, "system", system)
	if err != nil {
		return nil, err
	}
	next, err = sjson.SetRawBytes(next, "tools", claudeCodeProfileTools)
	if err != nil {
		return nil, err
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

func renderClaudeCodeProfileEnvironment(profile ClaudeEnvironmentProfile) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Environment\nYou have been invoked in the following environment:\n - Primary working directory: %s\n - Is a git repository: %t\n - Platform: %s\n - Shell: %s\n - OS Version: %s\n", profile.WorkingDirectory, profile.GitRepository, profile.Platform, profile.Shell, profile.OSVersion)
	if len(profile.AdditionalWorkingDirectories) > 0 {
		out.WriteString(" - Additional working directories:\n")
		for _, directory := range profile.AdditionalWorkingDirectories {
			fmt.Fprintf(&out, "   - %s\n", directory)
		}
	}
	if profile.GitRepository {
		fmt.Fprintf(&out, "\ngitStatus:\nCurrent branch: %s\nMain branch: %s\nGit user: %s\nStatus:\n%s\nRecent commits:\n%s\n", profile.CurrentBranch, profile.MainBranch, profile.GitUser, profile.GitStatus, strings.Join(profile.RecentCommits, "\n"))
	}
	return strings.TrimSpace(out.String())
}

func applyClaudeCodeProfileHeaders(ctx context.Context, account *Account, headers http.Header) {
	if forceClaudeCodeProfile(ctx, account) {
		setHeaderRaw(headers, "User-Agent", claude.DefaultUserAgent())
		applyClaudeEnvironmentHeadersFromContext(ctx, headers)
	}
}

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
	"strings"
)

// resolveClaudeCodeProfileBeta never depends on the caller's User-Agent.
func resolveClaudeCodeProfileBeta(model string, body []byte, clientBeta string, countTokens bool, drop map[string]struct{}) string {
	tokens := []string{claude.BetaClaudeCode, claude.BetaOAuth}
	add := func(token string, active bool) {
		if active {
			tokens = append(tokens, token)
		}
	}
	thinking := gjson.GetBytes(body, "thinking.type").String()
	enabled := thinking != "" && thinking != "disabled" && claude.SupportsThinking(model)
	add(claude.BetaInterleavedThinking, enabled)
	if countTokens {
		// CLI allowlist plus the SDK's endpoint beta.
		add(claude.BetaTokenCounting, true)
	} else {
		add(claude.BetaContextManagement, claude.SupportsThinking(model) && gjson.GetBytes(body, "context_management").IsObject())
		effort := len(claude.EffortLevelsForModel(model)) > 0
		add(claude.BetaEffort, effort && gjson.GetBytes(body, "output_config.effort").Exists())
		add(claude.BetaThinkingBindingControls, enabled && gjson.GetBytes(body, "thinking.block_binding").Exists())
		add(claude.BetaRedactThinking, enabled && containsBetaToken(clientBeta, claude.BetaRedactThinking))
		add(claude.BetaFastMode, effort && gjson.GetBytes(body, "speed").String() == "fast")
		add(claude.BetaExtendedCacheTTL, profileHasCacheFeature(body, "ttl", "1h"))
		add(claude.BetaPromptCachingScope, profileHasCacheFeature(body, "scope", "global"))
		for _, token := range []string{claude.BetaStructuredOutputs, claude.BetaMidConversationToolChanges, claude.BetaInlineTools} {
			add(token, effort && containsBetaToken(clientBeta, token))
		}
		add(claude.BetaContext1M, effort && containsBetaToken(clientBeta, claude.BetaContext1M))
		add(claude.BetaFineGrainedToolStreaming, claude.SupportsThinking(model) && gjson.GetBytes(body, "stream").Bool() && containsBetaToken(clientBeta, claude.BetaFineGrainedToolStreaming))
		midOutput := false
		gjson.GetBytes(body, "messages").ForEach(func(_, msg gjson.Result) bool {
			if msg.Get("output_config").Exists() {
				midOutput = true
			}
			return true
		})
		add(claude.BetaMidConversationOutputConfig, effort && midOutput)
	}
	return filterSonnet55ToolsetBeta(mergeAnthropicBetaDropping(tokens, "", drop), body, model)
}

// Inspect cache locations, never tool schemas or tool_use.input.
func profileHasCacheFeature(body []byte, field, value string) bool {
	found := false
	check := func(block gjson.Result) {
		if block.Get("cache_control."+field).String() == value {
			found = true
		}
	}
	for _, path := range []string{"system", "tools"} {
		gjson.GetBytes(body, path).ForEach(func(_, block gjson.Result) bool { check(block); return true })
	}
	gjson.GetBytes(body, "messages").ForEach(func(_, msg gjson.Result) bool {
		msg.Get("content").ForEach(func(_, block gjson.Result) bool {
			check(block)
			if block.Get("type").String() == "tool_result" {
				block.Get("content").ForEach(func(_, nested gjson.Result) bool { check(nested); return true })
			}
			return true
		})
		return true
	})
	return found
}

func (s *GatewayService) finalClaudeCodeProfileBeta(ctx context.Context, account *Account, model string, body []byte, client string, countTokens bool) (string, error) {
	candidate := resolveClaudeCodeProfileBeta(model, body, client, countTokens, nil)
	betaInput := mergeAnthropicBeta(strings.Split(candidate, ","), client)
	if override, ok := account.HeaderOverrideValue("anthropic-beta"); ok {
		betaInput = override
	}
	policy := s.evaluateBetaPolicy(ctx, betaInput, account, model)
	if policy.blockErr != nil {
		return "", policy.blockErr
	}
	drop := mergeDropSets(policy.filterSet)
	return resolveClaudeCodeProfileBeta(model, body, betaInput, countTokens, drop), nil
}

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultClaudeMessagesModel     = "claude-sonnet-5-5"
	defaultClaudeMessagesMaxTokens = 128_000
	claude45MessagesMaxTokens      = 64_000
)

var (
	claudeMessageAllowedFields = map[string]struct{}{
		"model": {}, "messages": {}, "system": {}, "tools": {}, "metadata": {},
		"max_tokens": {}, "thinking": {}, "output_config": {}, "stream": {},
		"context_management": {}, "safeguards": {}, "temperature": {},
		"stop_sequences": {}, "tool_choice": {},
	}
	claude45ModelPattern = regexp.MustCompile(`^claude-(?:haiku|sonnet|opus)-4-5(?:-|$)`)
	probeReplyPattern    = regexp.MustCompile(`(?i)^reply[ \t]+([^\r\n]+)$`)
)

type claudePreprocessContextKey struct{}

type claudePreprocessContext struct {
	BillingSample string
	Safeguards    json.RawMessage
	Environment   *ClaudeEnvironmentProfile
}

type ClaudeMessagesPreprocessResult struct {
	Body              []byte
	ProbeReply        *string
	EnvironmentReport environmentRewriteReport
	BillingSample     string
	Safeguards        json.RawMessage
	Environment       *ClaudeEnvironmentProfile
	OriginalMaxTokens *int
}

type ClaudeMessagesPreprocessError struct {
	Status  int
	Code    string
	Message string
}

func (e *ClaudeMessagesPreprocessError) Error() string { return e.Message }

func WithClaudeMessagesPreprocessContext(
	ctx context.Context,
	result ClaudeMessagesPreprocessResult,
) context.Context {
	return context.WithValue(ctx, claudePreprocessContextKey{}, claudePreprocessContext{
		BillingSample: result.BillingSample,
		Safeguards:    append(json.RawMessage(nil), result.Safeguards...),
		Environment:   result.Environment,
	})
}

func claudeBillingSampleFromContext(ctx context.Context, body []byte) string {
	if ctx != nil {
		if value, ok := ctx.Value(claudePreprocessContextKey{}).(claudePreprocessContext); ok {
			return value.BillingSample
		}
	}
	return firstUserTextForBilling(body)
}

func claudeSafeguardsFromContext(ctx context.Context) json.RawMessage {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(claudePreprocessContextKey{}).(claudePreprocessContext)
	safeguards := append(json.RawMessage(nil), value.Safeguards...)
	if profile, ok := ctx.Value(claudeCodeProfileEnvironmentKey{}).(*ClaudeEnvironmentProfile); ok && profile != nil && len(safeguards) > 0 {
		var decoded any
		if json.Unmarshal(safeguards, &decoded) == nil {
			wrapper := map[string]any{"safeguards": decoded}
			if rewriteSafeguardEnvironment(wrapper, *profile) {
				if encoded, err := json.Marshal(wrapper["safeguards"]); err == nil {
					return encoded
				}
			}
		}
	}
	return safeguards
}

func claudeTelemetryBodyFromContext(ctx context.Context, wireBody []byte) []byte {
	safeguards := claudeSafeguardsFromContext(ctx)
	if len(safeguards) == 0 {
		return wireBody
	}
	var body map[string]any
	var value any
	if json.Unmarshal(wireBody, &body) != nil || json.Unmarshal(safeguards, &value) != nil {
		return wireBody
	}
	body["safeguards"] = value
	encoded, err := json.Marshal(body)
	if err != nil {
		return wireBody
	}
	return encoded
}

func claudeEnvironmentProfileFromContext(ctx context.Context) *ClaudeEnvironmentProfile {
	if ctx == nil {
		return nil
	}
	if forced, ok := ctx.Value(claudeCodeProfileEnvironmentKey{}).(*ClaudeEnvironmentProfile); ok && forced != nil {
		profile := *forced
		profile.AdditionalWorkingDirectories = append([]string(nil), forced.AdditionalWorkingDirectories...)
		profile.RecentCommits = append([]string(nil), forced.RecentCommits...)
		return &profile
	}
	value, _ := ctx.Value(claudePreprocessContextKey{}).(claudePreprocessContext)
	if value.Environment == nil {
		return nil
	}
	profile := *value.Environment
	profile.AdditionalWorkingDirectories = append([]string(nil), value.Environment.AdditionalWorkingDirectories...)
	profile.RecentCommits = append([]string(nil), value.Environment.RecentCommits...)
	return &profile
}

func applyClaudeEnvironmentHeadersFromContext(ctx context.Context, headers http.Header) {
	profile := claudeEnvironmentProfileFromContext(ctx)
	if profile == nil || headers == nil {
		return
	}
	for key, value := range map[string]string{
		"x-stainless-os":              profile.Platform,
		"x-stainless-arch":            profile.Arch,
		"x-stainless-runtime":         profile.Runtime,
		"x-stainless-runtime-version": profile.RuntimeVersion,
		"x-stainless-package-version": profile.PackageVersion,
		"x-claude-code-shell":         profile.Shell,
	} {
		if value != "" {
			setHeaderRaw(headers, key, value)
		}
	}
}

func PreprocessNativeClaudeMessages(
	body []byte,
	settings *ClaudeAntiBanSettings,
) (ClaudeMessagesPreprocessResult, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return ClaudeMessagesPreprocessResult{}, &ClaudeMessagesPreprocessError{
			Status: 400, Code: "invalid_json", Message: "请求体不是有效 JSON",
		}
	}
	decoded, ok := raw.(map[string]any)
	if !ok || decoded == nil {
		return ClaudeMessagesPreprocessResult{}, &ClaudeMessagesPreprocessError{
			Status: 400, Code: "invalid_request_body", Message: "请求体必须是 JSON 对象",
		}
	}
	var unknown []string
	for field := range decoded {
		if _, ok := claudeMessageAllowedFields[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		quoted := make([]string, len(unknown))
		for index, field := range unknown {
			quoted[index] = strconv.Quote(field)
		}
		return ClaudeMessagesPreprocessResult{}, &ClaudeMessagesPreprocessError{
			Status:  400,
			Code:    "unsupported_body_fields",
			Message: "请求体包含不支持的顶层字段：" + strings.Join(quoted, ", "),
		}
	}
	delete(decoded, "tool_choice")
	var originalMaxTokens *int
	if value, ok := decoded["max_tokens"].(float64); ok && value >= 0 && value == float64(int(value)) {
		parsed := int(value)
		originalMaxTokens = &parsed
	}

	probeReply, probeMatched := "", false
	if settings == nil {
		settings = DefaultClaudeAntiBanSettings()
	}
	if settings.ProbeInterceptionEnabled {
		probeReply, probeMatched = claudeProbeReply(decoded)
	}
	normalizeClaudeMessagesBody(decoded)
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return ClaudeMessagesPreprocessResult{}, err
	}
	if probeMatched {
		return ClaudeMessagesPreprocessResult{
			Body: normalized, ProbeReply: &probeReply, OriginalMaxTokens: originalMaxTokens,
		}, nil
	}

	billingSample := firstUserTextForBilling(normalized)
	var safeguards json.RawMessage
	if value, ok := decoded["safeguards"]; ok {
		safeguards, _ = json.Marshal(value)
		delete(decoded, "safeguards")
	}
	withoutSafeguards, err := json.Marshal(decoded)
	if err != nil {
		return ClaudeMessagesPreprocessResult{}, err
	}
	rewritten, report, err := rewriteClaudeEnvironmentInBody(withoutSafeguards, settings.Environment)
	if err != nil {
		return ClaudeMessagesPreprocessResult{}, err
	}
	if len(safeguards) > 0 {
		report.SafeguardsPreserved = true
	}
	if report.Rejected {
		return ClaudeMessagesPreprocessResult{}, &ClaudeMessagesPreprocessError{
			Status: 422, Code: "unmatched_environment", Message: "请求包含未识别的客户端环境上下文",
		}
	}
	rewritten = syncBillingHeaderVersionForSample(rewritten, billingSample)
	var outbound map[string]any
	if err := json.Unmarshal(rewritten, &outbound); err != nil {
		return ClaudeMessagesPreprocessResult{}, err
	}
	outbound["max_tokens"] = normalizedClaudeMessagesMaxTokens(claudeStringValue(outbound["model"]))
	delete(outbound, "safeguards")
	finalBody, err := json.Marshal(outbound)
	if err != nil {
		return ClaudeMessagesPreprocessResult{}, err
	}
	result := ClaudeMessagesPreprocessResult{
		Body: finalBody, EnvironmentReport: report, BillingSample: billingSample,
		Safeguards: safeguards, OriginalMaxTokens: originalMaxTokens,
	}
	if settings.Environment.Mode == ClaudeEnvironmentModeRewrite {
		profile := settings.Environment.Profile
		result.Environment = &profile
	}
	return result, nil
}

func normalizeClaudeMessagesBody(body map[string]any) {
	if _, ok := body["model"]; !ok {
		body["model"] = defaultClaudeMessagesModel
	}
	if _, ok := body["messages"]; !ok {
		body["messages"] = []any{}
	}
	if _, ok := body["system"]; !ok {
		body["system"] = []any{}
	}
	if _, ok := body["tools"]; !ok {
		body["tools"] = []any{}
	}
	if _, ok := body["metadata"]; !ok {
		body["metadata"] = map[string]any{}
	}
	if _, ok := body["max_tokens"]; !ok {
		body["max_tokens"] = normalizedClaudeMessagesMaxTokens(claudeStringValue(body["model"]))
	}
	if _, ok := body["stream"]; !ok {
		body["stream"] = true
	}
}

func normalizedClaudeMessagesMaxTokens(model string) int {
	if claude45ModelPattern.MatchString(model) {
		return claude45MessagesMaxTokens
	}
	return defaultClaudeMessagesMaxTokens
}

func claudeProbeReply(body map[string]any) (string, bool) {
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		return "", false
	}
	message, ok := messages[0].(map[string]any)
	if !ok || message["role"] != "user" {
		return "", false
	}
	text := ""
	switch content := message["content"].(type) {
	case string:
		text = content
	case []any:
		if len(content) != 1 {
			return "", false
		}
		block, ok := content[0].(map[string]any)
		if !ok || block["type"] != "text" {
			return "", false
		}
		text, _ = block["text"].(string)
	default:
		return "", false
	}
	trimmed := strings.TrimSpace(text)
	switch strings.ToLower(trimmed) {
	case "hi":
		return "Hi! How can I help you today?", true
	case "ping":
		return "pong", true
	}
	if match := probeReplyPattern.FindStringSubmatch(trimmed); len(match) == 2 {
		return strings.TrimSuffix(match[1], "."), true
	}
	return "", false
}

func claudeStringValue(value any) string {
	text, _ := value.(string)
	return text
}

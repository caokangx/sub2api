package claudetelemetry

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

var datadogAllowlist = map[string]struct{}{
	"tengu_api_success":        {},
	"tengu_feature_ok":         {},
	"tengu_tool_use_success":   {},
	"tengu_tool_use_error":     {},
	"tengu_turn_end":           {},
	"tengu_sdk_result":         {},
	"tengu_sdk_ttft":           {},
	"tengu_sdk_init_handshake": {},
	"tengu_started":            {},
	"tengu_init":               {},
}

func DatadogAllowed(eventName string) bool {
	_, ok := datadogAllowlist[eventName]
	return ok
}

func datadogLog(context EventContext, eventName string, metadata map[string]any) map[string]any {
	if !DatadogAllowed(eventName) {
		return nil
	}
	environment := context.Environment
	var userBucket any
	if context.Identity.DeviceID != "" {
		sum := sha256Hex(context.Identity.DeviceID)
		raw, _ := hex.DecodeString(sum[:8])
		value := 0
		for _, part := range raw {
			value = value<<8 | int(part)
		}
		userBucket = value % 100
	}
	tags := map[string]any{
		"event": eventName, "arch": environment.Arch, "client_type": valueOr(context.ClientType, "cli"),
		"entrypoint": valueOr(context.Entrypoint, "cli"), "model": valueOr(context.Model, "unknown"),
		"provider": context.Provider, "subscription_type": context.SubscriptionType,
		"user_bucket": userBucket, "user_type": valueOr(context.UserType, "external"),
		"version": environment.Version, "version_base": environment.VersionBase,
	}
	tagNames := []string{
		"event", "arch", "client_type", "entrypoint", "model", "provider",
		"subscription_type", "user_bucket", "user_type", "version", "version_base",
	}
	var ddTags []string
	for _, key := range tagNames {
		if value := tags[key]; value != nil && fmt.Sprint(value) != "" {
			ddTags = append(ddTags, key+":"+fmt.Sprint(value))
		}
	}
	out := compactMap(map[string]any{
		"ddsource": "nodejs", "ddtags": strings.Join(ddTags, ","),
		"message": eventName, "service": "claude-code", "env": "external",
		"model":      valueOr(context.Model, "unknown"),
		"session_id": context.Identity.SessionID, "user_type": valueOr(context.UserType, "external"),
		"betas": context.Betas, "entrypoint": valueOr(context.Entrypoint, "cli"), "client_type": valueOr(context.ClientType, "cli"),
		"agent_sdk_version": context.AgentSDKVersion,
		"subscription_type": context.SubscriptionType,
		"renderer_mode":     context.RendererMode, "platform": environment.Platform,
		"platform_raw": environment.PlatformRaw, "arch": environment.Arch,
		"node_version": environment.NodeVersion, "terminal": environment.Terminal,
		"shell": environment.Shell, "runtimes": environment.Runtime,
		"package_managers": environment.PackageManagers,
		"version":          environment.Version, "version_base": environment.VersionBase,
		"build_time":             environment.BuildTime,
		"deployment_environment": environment.DeploymentEnvironment,
		"user_bucket":            userBucket,
	})
	if context.Interactive != nil {
		out["is_interactive"] = strconv.FormatBool(*context.Interactive)
	}
	if environment.IsCI != nil {
		out["is_ci"] = *environment.IsCI
	}
	for key, value := range map[string]*bool{
		"is_claubbit": environment.IsClaubbit, "is_github_action": environment.IsGitHubAction,
		"is_claude_code_action": environment.IsClaudeCodeAction, "is_claude_code_remote": environment.IsClaudeCodeRemote,
		"is_local_agent_mode": environment.IsLocalAgentMode, "is_conductor": environment.IsConductor,
		"is_claude_ai_auth": environment.IsClaudeAIAuth,
	} {
		if value != nil {
			out[key] = *value
		}
	}
	for key, value := range metadata {
		if key == "renderer_mode" || key == "subscription_type" || value == nil {
			continue
		}
		out[camelToSnake(key)] = value
	}
	return out
}

func camelToSnake(value string) string {
	var result strings.Builder
	for _, character := range value {
		if character >= 'A' && character <= 'Z' {
			result.WriteByte('_')
			result.WriteRune(character + ('a' - 'A'))
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}

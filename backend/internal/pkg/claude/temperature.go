package claude

import "strings"

// ClaudeCodeSupportsTemperature mirrors Claude Code 2.1.284's built-in
// acceptsTemperature capability (J4n): Claude 3 and catalog entries before
// Opus 4.7. Unknown/custom models do not opt in implicitly.
func ClaudeCodeSupportsTemperature(model string) bool {
	id := normalizeEffortModelID(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(model)), "[1m]"))
	if strings.Contains(id, "claude-3-") {
		return true
	}
	switch id {
	case "claude-opus-4", "claude-opus-4-0", "claude-sonnet-4", "claude-sonnet-4-0",
		"claude-opus-4-1", "claude-sonnet-4-5", "claude-haiku-4-5",
		"claude-opus-4-5", "claude-opus-4-6", "claude-sonnet-4-6":
		return true
	default:
		return false
	}
}

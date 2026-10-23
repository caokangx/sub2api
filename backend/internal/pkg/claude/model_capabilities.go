package claude

import "strings"

// CanonicalModelID normalizes provider aliases, dated IDs and CLI context suffixes
// for capability lookups. It does not change an upstream routing ID.
func CanonicalModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	for strings.HasSuffix(id, "[1m]") {
		id = strings.TrimSpace(strings.TrimSuffix(id, "[1m]"))
	}
	id = normalizeEffortModelID(id)
	id = strings.TrimSuffix(id, "-latest")
	return id
}

// OutputBudget returns the CLI default and the model output limit separately.
// Unknown models retain the CLI fallback default of 32,000.
func OutputBudget(model string) (defaultTokens, upperTokens int) {
	switch CanonicalModelID(model) {
	case "claude-3-5-haiku", "claude-3-5-sonnet":
		return 8192, 8192
	case "claude-3-7-sonnet", "claude-sonnet-4", "claude-sonnet-4-0", "claude-sonnet-4-5", "claude-haiku-4-5":
		return 32000, 64000
	case "claude-sonnet-4-6":
		return 32000, 128000
	case "claude-sonnet-5":
		return 64000, 128000
	case "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5":
		return 128000, 128000
	case "claude-opus-4", "claude-opus-4-0", "claude-opus-4-1":
		return 32000, 32000
	case "claude-opus-4-5":
		return 32000, 64000
	case "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-fable-5", "claude-fable-5-1", "claude-mythos-5", "claude-mythos-5-1":
		return 64000, 128000
	default:
		return 32000, 128000
	}
}

func SupportsThinking(model string) bool {
	id := CanonicalModelID(model)
	if id == "claude-3-7-sonnet" {
		return true
	}
	switch id {
	case "claude-sonnet-4", "claude-sonnet-4-0", "claude-sonnet-4-5", "claude-haiku-4-5", "claude-opus-4", "claude-opus-4-0", "claude-opus-4-1":
		return true
	}
	return len(EffortLevelsForModel(id)) > 0
}

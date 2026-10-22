package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeCodeSupportsTemperature(t *testing.T) {
	for _, model := range []string{
		"claude-3-5-sonnet-20241022", "claude-3-7-sonnet-latest",
		"claude-sonnet-4-20250514", "claude-opus-4-0", "claude-opus-4-1-20250805",
		"claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001",
		"claude-opus-4-5", "claude-opus-4-6", "claude-sonnet-4-6",
		"anthropic/claude-sonnet-4-6", "us.anthropic.claude-sonnet-4-6-thinking",
		"claude-sonnet-4-5-20250929[1m]",
	} {
		require.True(t, ClaudeCodeSupportsTemperature(model), model)
	}
	for _, model := range []string{
		"claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5",
		"claude-sonnet-5", "claude-sonnet-5-5", "claude-haiku-5-5",
		"claude-fable-5", "claude-mythos-preview", "custom-model", "",
	} {
		require.False(t, ClaudeCodeSupportsTemperature(model), model)
	}
}

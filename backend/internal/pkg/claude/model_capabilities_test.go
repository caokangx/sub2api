package claude

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOutputBudgetCatalog(t *testing.T) {
	for _, tt := range []struct {
		model      string
		def, upper int
	}{
		{"claude-3-5-haiku-latest", 8192, 8192}, {"claude-3-5-sonnet-20241022", 8192, 8192},
		{"claude-3-7-sonnet-latest", 32000, 64000}, {"claude-sonnet-4-20250514", 32000, 64000},
		{"claude-sonnet-4-5", 32000, 64000}, {"claude-haiku-4-5", 32000, 64000}, {"claude-sonnet-4-6[1m][1m]", 32000, 128000},
		{"anthropic/claude-sonnet-5-20260701", 64000, 128000}, {"claude-sonnet-5.5", 128000, 128000},
		{"claude-opus-4-20250514", 32000, 32000}, {"claude-opus-4-1-20250805", 32000, 32000}, {"claude-opus-4-5", 32000, 64000},
		{"claude-opus-4-6", 64000, 128000}, {"claude-opus-4-7", 64000, 128000}, {"claude-opus-4-8", 64000, 128000}, {"claude-opus-5", 64000, 128000}, {"claude-opus-5-5", 128000, 128000},
		{"claude-fable-5", 64000, 128000}, {"claude-fable-5-1", 64000, 128000}, {"claude-mythos-5", 64000, 128000}, {"claude-mythos-5-1", 64000, 128000},
		{"custom-claude-sonnet-50", 32000, 128000}, {"unknown", 32000, 128000},
	} {
		t.Run(tt.model, func(t *testing.T) {
			def, upper := OutputBudget(tt.model)
			require.Equal(t, tt.def, def)
			require.Equal(t, tt.upper, upper)
		})
	}
}

package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
)

func TestClaudeBillingFingerprintMatchesProxyVectors(t *testing.T) {
	const fixture = "Read fixture.txt and report its content."
	const reminder = "<system-reminder>\nEnvironment and date context.\n</system-reminder>"
	tests := []struct {
		name        string
		messages    any
		fingerprint string
	}{
		{"string", []any{map[string]any{"role": "user", "content": fixture}}, "469"},
		{"reminder block", []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image"},
			map[string]any{"type": "text", "text": reminder},
			map[string]any{"type": "text", "text": fixture},
		}}}, "469"},
		{"reminder only message", []any{
			map[string]any{"role": "user", "content": reminder},
			map[string]any{"role": "user", "content": fixture},
		}, "469"},
		{"multiple reminders crlf", []any{map[string]any{
			"role": "user", "content": " \r\n" + reminder + "\r\n\r\n" + reminder + "\n\n" + fixture,
		}}, "469"},
		{"embedded reminder", []any{map[string]any{
			"role": "user", "content": fixture[:5] + reminder + fixture[5:],
		}}, "469"},
		{"unclosed reminder", []any{map[string]any{
			"role": "user", "content": "<system-reminder>incomplete",
		}}, "774"},
		{"preserve whitespace", []any{map[string]any{"role": "user", "content": "   " + fixture}}, "fd6"},
		{"short", []any{map[string]any{"role": "user", "content": "hi"}}, "12a"},
		{"zero based", []any{map[string]any{"role": "user", "content": "01234567890123456789012345"}}, "cbe"},
		{"utf16", []any{map[string]any{"role": "user", "content": "abc😀12🦊3456789🚀XYZ"}}, "e41"},
		{"empty text after reminder", []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": reminder},
			map[string]any{"type": "text", "text": ""},
			map[string]any{"type": "text", "text": fixture},
		}}}, "12a"},
		{"only reminder", []any{map[string]any{"role": "user", "content": reminder + "\n"}}, "12a"},
		{"tool result first user", []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "tool_1", "content": fixture},
			}},
			map[string]any{"role": "user", "content": fixture},
		}, "12a"},
		{"no user", []any{}, "12a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"messages": test.messages})
			require.NoError(t, err)
			require.Equal(t, test.fingerprint, computeClaudeCodeFingerprint(body, claude.CLICurrentVersion))
		})
	}
}

func TestFirstUserTextForBillingDoesNotMutateBody(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"<system-reminder>meta</system-reminder>\n\n  prompt"}]}`)
	original := append([]byte(nil), body...)
	require.Equal(t, "  prompt", firstUserTextForBilling(body))
	require.Equal(t, original, body)
}

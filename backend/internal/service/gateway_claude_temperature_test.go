package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeCodeTemperatureNormalization(t *testing.T) {
	for _, tt := range []struct {
		name, model, fields string
		present             bool
		want                float64
	}{
		{"missing defaults to one", "claude-sonnet-4-6", "", true, 1},
		{"disabled defaults to one", "claude-sonnet-4-6", `,"thinking":{"type":"disabled"}`, true, 1},
		{"preserves explicit zero", "claude-sonnet-4-6", `,"temperature":0`, true, 0},
		{"preserves override", "claude-sonnet-4-6", `,"thinking":{"type":"disabled"},"temperature":0.4`, true, 0.4},
		{"null defaults to one", "claude-sonnet-4-6", `,"temperature":null`, true, 1},
		{"enabled removes override", "claude-sonnet-4-6", `,"thinking":{"type":"enabled","budget_tokens":2048},"temperature":0.4`, false, 0},
		{"adaptive removes override", "claude-sonnet-4-6", `,"thinking":{"type":"adaptive"},"temperature":1`, false, 0},
		{"adaptive does not inject", "claude-sonnet-4-6", `,"thinking":{"type":"adaptive"}`, false, 0},
		{"between tools omits", "claude-sonnet-5-5", `,"thinking":{"type":"between_tools"},"temperature":1`, false, 0},
		{"unsupported removes override", "claude-opus-4-7", `,"thinking":{"type":"disabled"},"temperature":1`, false, 0},
		{"unsupported does not inject", "claude-opus-4-8", "", false, 0},
		{"unknown model omits", "custom-model", `,"temperature":0.4`, false, 0},
		{"new model top p preserved", "claude-haiku-5-5", `,"top_p":0.99`, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]%s}`, tt.model, tt.fields))
			for _, forceProfile := range []bool{false, true} {
				var out []byte
				if forceProfile {
					var err error
					out, err = rewriteClaudeCodeProfile(body, tt.model, "hello", DefaultClaudeAntiBanSettings().Environment.Profile)
					require.NoError(t, err)
				} else {
					out, _ = normalizeClaudeOAuthRequestBody(body, tt.model, claudeOAuthNormalizeOptions{})
				}
				temperature := gjson.GetBytes(out, "temperature")
				require.Equal(t, tt.present, temperature.Exists(), "force profile=%t", forceProfile)
				if tt.present {
					require.Equal(t, tt.want, temperature.Float())
				}
				require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(out, "messages").Raw)
				require.Equal(t, gjson.GetBytes(body, "thinking").Raw, gjson.GetBytes(out, "thinking").Raw)
				require.Equal(t, gjson.GetBytes(body, "top_p").Raw, gjson.GetBytes(out, "top_p").Raw)
			}
		})
	}
}

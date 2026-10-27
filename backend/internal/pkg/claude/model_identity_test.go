package claude

import "testing"

func TestModelIdentityText(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-4-7":            "You are powered by the model named Opus 4.7. The exact model ID is claude-opus-4-7. Assistant knowledge cutoff is January 2026.",
		"claude-haiku-4-5-20251001":  "You are powered by the model named Haiku 4.5. The exact model ID is claude-haiku-4-5-20251001. Assistant knowledge cutoff is February 2025.",
		"claude-sonnet-4-20250514":   "You are powered by the model named Sonnet 4. The exact model ID is claude-sonnet-4-20250514. Assistant knowledge cutoff is January 2025.",
		"claude-3-5-sonnet-20241022": "You are powered by the model named Sonnet 3.5. The exact model ID is claude-3-5-sonnet-20241022.",
		"claude-custom-model":        "You are powered by the model claude-custom-model.",
	} {
		if got := ModelIdentityText(model); got != want {
			t.Errorf("ModelIdentityText(%q) = %q, want %q", model, got, want)
		}
	}
}

package claude

type modelIdentity struct {
	displayName     string
	knowledgeCutoff string
}

// modelIdentities mirrors the Claude Code 2.1.284 built-in model table
// (display_name and knowledge_cutoff), keyed by CanonicalModelID.
var modelIdentities = map[string]modelIdentity{
	"claude-3-5-haiku":  {"Haiku 3.5", ""},
	"claude-3-5-sonnet": {"Sonnet 3.5", ""},
	"claude-3-7-sonnet": {"Sonnet 3.7", ""},
	"claude-haiku-4-5":  {"Haiku 4.5", "February 2025"},
	"claude-sonnet-4":   {"Sonnet 4", "January 2025"},
	"claude-sonnet-4-0": {"Sonnet 4", "January 2025"},
	"claude-sonnet-4-5": {"Sonnet 4.5", "January 2025"},
	"claude-sonnet-4-6": {"Sonnet 4.6", "August 2025"},
	"claude-sonnet-5":   {"Sonnet 5", "January 2026"},
	"claude-sonnet-5-5": {"Sonnet 5.5", "June 2026"},
	"claude-opus-4":     {"Opus 4", "January 2025"},
	"claude-opus-4-0":   {"Opus 4", "January 2025"},
	"claude-opus-4-1":   {"Opus 4.1", "January 2025"},
	"claude-opus-4-5":   {"Opus 4.5", "May 2025"},
	"claude-opus-4-6":   {"Opus 4.6", "May 2025"},
	"claude-opus-4-7":   {"Opus 4.7", "January 2026"},
	"claude-opus-4-8":   {"Opus 4.8", "January 2026"},
	"claude-opus-5":     {"Opus 5", "May 2026"},
	"claude-opus-5-5":   {"Opus 5.5", "June 2026"},
	"claude-fable-5":    {"Fable 5", "January 2026"},
	"claude-fable-5-1":  {"Fable 5.1", "June 2026"},
	"claude-mythos-5":   {"Mythos 5", "January 2026"},
	"claude-mythos-5-1": {"Mythos 5.1", "June 2026"},
}

// ModelIdentityText renders the CLI's model attachment text for modelID, which
// is reported verbatim as the exact model ID.
func ModelIdentityText(modelID string) string {
	identity, known := modelIdentities[CanonicalModelID(modelID)]
	text := "You are powered by the model " + modelID + "."
	if known {
		text = "You are powered by the model named " + identity.displayName + ". The exact model ID is " + modelID + "."
	}
	if known && identity.knowledgeCutoff != "" {
		text += " Assistant knowledge cutoff is " + identity.knowledgeCutoff + "."
	}
	return text
}

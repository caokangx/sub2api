package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRewriteClaudeEnvironmentUsesMacProfile(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings().Environment
	settings.Mode = ClaudeEnvironmentModeRewrite
	body := []byte(`{
		"system":[{"type":"text","text":"<env>\nWorking directory: /old/project\nPlatform: linux\nShell: bash\nOS Version: Linux 6.8\nAdditional working directories:\n  - /old/shared\n</env>\nYou have a persistent file-based memory at ` + "`/old/.claude/memory/`" + `"}],
		"messages":[{"role":"user","content":"hello"}]
	}`)
	rewritten, report, err := rewriteClaudeEnvironmentInBody(body, settings)
	require.NoError(t, err)
	require.True(t, report.Applied)
	require.Equal(t, 1, report.EnvironmentBlocks)
	require.GreaterOrEqual(t, report.MemoryBlocks, 1)
	text := gjson.GetBytes(rewritten, "system.0.text").String()
	require.Contains(t, text, "Working directory: /Users/developer/project")
	require.Contains(t, text, "Platform: darwin")
	require.Contains(t, text, "Shell: zsh")
	require.Contains(t, text, "OS Version: Darwin 24.5.0")
	require.Contains(t, text, "/Users/developer/.claude/memory/")
	require.NotContains(t, text, "/old/shared")
}

func TestRewriteClaudeEnvironmentObserveDoesNotMutate(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings().Environment
	settings.Mode = ClaudeEnvironmentModeObserve
	body := []byte(`{"system":"# Environment\n- Primary working directory: /old\n- Platform: linux\n- Shell: bash\n- OS Version: Linux"}`)
	rewritten, report, err := rewriteClaudeEnvironmentInBody(body, settings)
	require.NoError(t, err)
	require.Equal(t, body, rewritten)
	require.True(t, report.Changed)
	require.False(t, report.Applied)
	require.Equal(t, 1, report.EnvironmentBlocks)
}

func TestRewriteClaudeEnvironmentRejectsUnknownDynamicBlock(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings().Environment
	settings.Mode = ClaudeEnvironmentModeRewrite
	settings.OnUnmatched = ClaudeEnvironmentUnmatchedReject
	body := []byte(`{"system":"<env>\nUnknown host shape\n</env>"}`)
	rewritten, report, err := rewriteClaudeEnvironmentInBody(body, settings)
	require.NoError(t, err)
	require.Equal(t, body, rewritten)
	require.True(t, report.Rejected)
	require.Equal(t, 1, report.UnmatchedEnvironmentBlocks)
}

func TestRewriteClaudeEnvironmentRejectModeAllowsOrdinarySystemText(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings().Environment
	settings.Mode = ClaudeEnvironmentModeRewrite
	settings.OnUnmatched = ClaudeEnvironmentUnmatchedReject
	body := []byte(`{"system":"You are Claude Code. No dynamic environment is present."}`)
	rewritten, report, err := rewriteClaudeEnvironmentInBody(body, settings)
	require.NoError(t, err)
	require.Equal(t, body, rewritten)
	require.False(t, report.Applicable)
	require.False(t, report.Rejected)
}

func TestRewriteClaudeEnvironmentSkipsCodeFences(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings().Environment
	settings.Mode = ClaudeEnvironmentModeRewrite
	body := []byte("{\"system\":\"```text\\n# Environment\\n- Platform: linux\\n```\\n# Environment\\n- Platform: linux\\n\"}")
	rewritten, _, err := rewriteClaudeEnvironmentInBody(body, settings)
	require.NoError(t, err)
	text := gjson.GetBytes(rewritten, "system").String()
	require.Contains(t, text, "```text\n# Environment\n- Platform: linux\n```")
	require.Contains(t, text, "# Environment\n- Platform: darwin")
}

func TestRewriteClaudeEnvironmentHandlesLeadingUserReminderOnly(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings().Environment
	settings.Mode = ClaudeEnvironmentModeRewrite
	body := []byte(`{
		"messages":[
			{"role":"user","content":"<system-reminder># Environment\n- Primary working directory: /old\n- Platform: linux\n</system-reminder>\n\nkeep user task"},
			{"role":"user","content":"# Environment\n- Platform: linux\nquoted later task data"}
		]
	}`)
	rewritten, report, err := rewriteClaudeEnvironmentInBody(body, settings)
	require.NoError(t, err)
	require.True(t, report.Applied)
	require.Contains(t, gjson.GetBytes(rewritten, "messages.0.content").String(), "/Users/developer/project")
	require.Contains(t, gjson.GetBytes(rewritten, "messages.0.content").String(), "keep user task")
	require.Contains(t, gjson.GetBytes(rewritten, "messages.1.content").String(), "- Platform: linux")
}

func TestDefaultClaudeAntiBanSettingsAreValid(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	require.NoError(t, validateClaudeAntiBanSettings(settings))
	require.True(t, settings.ProbeInterceptionEnabled)
	require.Equal(t, "darwin", settings.Environment.Profile.Platform)
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	require.Contains(t, string(raw), "/Users/developer/project")
}

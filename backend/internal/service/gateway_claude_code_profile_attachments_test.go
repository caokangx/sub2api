package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func testProfileForAttachments() ClaudeEnvironmentProfile {
	profile := DefaultClaudeAntiBanSettings().Environment.Profile
	profile.WorkingDirectory = "/Users/developer/project"
	profile.AdditionalWorkingDirectories = []string{"/Users/developer/lib", "/Users/developer/docs"}
	profile.RecentCommits = []string{"abc1234 Fix parser", "def5678 Add tests"}
	return profile
}

func TestClaudeCodeProfileReminderTemplates(t *testing.T) {
	profile := testProfileForAttachments()
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	out, err := injectClaudeCodeProfileAttachments(body, "claude-opus-4-7", profile)
	require.NoError(t, err)

	content := gjson.GetBytes(out, "messages.0.content").Array()
	require.Len(t, content, 4)
	require.Equal(t, "<system-reminder>\n"+
		"As you answer the user's questions, you can use the following context:\n"+
		"# gitStatus\n"+
		"This is the git status at the start of the conversation. Note that this status is a snapshot in time, and will not update during the conversation.\n"+
		"Current branch: main\n"+
		"Main branch (you will usually use this for PRs): main\n"+
		"Git user: developer\n"+
		"Status:\n"+
		"(clean)\n"+
		"Recent commits:\n"+
		"abc1234 Fix parser\n"+
		"def5678 Add tests\n"+
		"\n"+
		"IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.\n"+
		"</system-reminder>", content[0].Get("text").String())
	require.Equal(t, "<system-reminder>\n"+
		"# Environment\n"+
		"You have been invoked in the following environment: \n"+
		" - Primary working directory: /Users/developer/project\n"+
		" - Is a git repository: true\n"+
		" - Additional working directories:\n"+
		"  - /Users/developer/lib\n"+
		"  - /Users/developer/docs\n"+
		" - Platform: darwin\n"+
		" - Shell: zsh\n"+
		" - OS Version: Darwin 24.5.0\n"+
		"</system-reminder>", content[1].Get("text").String())
	require.Equal(t, "<system-reminder>\n"+
		"You are powered by the model named Opus 4.7. The exact model ID is claude-opus-4-7. Assistant knowledge cutoff is January 2026.\n"+
		"</system-reminder>", content[2].Get("text").String())
	require.Equal(t, `{"type":"text","text":"hello"}`, content[3].Raw)
	for _, block := range content {
		require.False(t, block.Get("cache_control").Exists())
	}
	require.Contains(t, string(out), "<system-reminder>", "reminders are encoded without HTML escaping")
	require.NotContains(t, string(out), `\u003c`)
}

func TestClaudeCodeProfileRemindersWithoutGitRepository(t *testing.T) {
	profile := testProfileForAttachments()
	profile.GitRepository = false
	out, err := injectClaudeCodeProfileAttachments([]byte(`{"messages":[{"role":"user","content":"hello"}]}`), "claude-custom", profile)
	require.NoError(t, err)
	content := gjson.GetBytes(out, "messages.0.content").Array()
	require.Len(t, content, 3)
	require.Contains(t, content[0].Get("text").String(), " - Is a git repository: false")
	require.Equal(t, "<system-reminder>\nYou are powered by the model claude-custom.\n</system-reminder>", content[1].Get("text").String())
}

func TestClaudeCodeProfileOmitsUnavailableGitStatus(t *testing.T) {
	profile := testProfileForAttachments()
	profile.GitStatus = "unavailable"
	out, err := injectClaudeCodeProfileAttachments([]byte(`{"messages":[{"role":"user","content":"hello"}]}`), "claude-opus-4-7", profile)
	require.NoError(t, err)
	content := gjson.GetBytes(out, "messages.0.content").Array()
	require.Len(t, content, 3)
	require.Contains(t, content[0].Get("text").String(), " - Is a git repository: true")
	require.NotContains(t, string(out), "gitStatus")
	require.NotContains(t, string(out), "unavailable")
}

func TestClaudeCodeProfileSystemIsNotHTMLEscaped(t *testing.T) {
	out, err := rewriteClaudeCodeProfile([]byte(`{"messages":[{"role":"user","content":"hello"}]}`), "claude-sonnet-4-6", "hello", DefaultClaudeAntiBanSettings().Environment.Profile)
	require.NoError(t, err)
	system := gjson.GetBytes(out, "system").Raw
	require.Contains(t, system, "<pasted_content>")
	require.NotContains(t, string(out), `\u003c`)
}

func TestClaudeCodeProfileRemindersAreIdempotentAndReplaceClientCopies(t *testing.T) {
	profile := testProfileForAttachments()
	want, err := injectClaudeCodeProfileAttachments([]byte(`{"messages":[{"role":"user","content":"task"}]}`), "claude-sonnet-4-6", profile)
	require.NoError(t, err)

	again, err := injectClaudeCodeProfileAttachments(want, "claude-sonnet-4-6", profile)
	require.NoError(t, err)
	require.Equal(t, string(want), string(again))

	clientEnv := "<system-reminder>\n# Environment\nYou have been invoked in the following environment: \n - Primary working directory: /caller\n</system-reminder>"
	clientModel := "<system-reminder>\nYou are powered by the model named Opus 4.8. The exact model ID is claude-opus-4-8.\n</system-reminder>"
	clientContext := "<system-reminder>\nAs you answer the user's questions, you can use the following context:\n# userEmail\ncaller@example.com\n</system-reminder>"
	unrelated := "<system-reminder>\nCalled the Read tool.\n</system-reminder>"
	for name, body := range map[string]string{
		"array":           `{"messages":[{"role":"user","content":[{"type":"text","text":` + jsonString(clientContext) + `},{"type":"text","text":` + jsonString(clientEnv) + `},{"type":"text","text":` + jsonString(clientModel) + `},{"type":"text","text":"task"}]}]}`,
		"string":          `{"messages":[{"role":"user","content":` + jsonString(clientContext+clientEnv+"\n\n"+clientModel+"\ntask") + `}]}`,
		"shared block":    `{"messages":[{"role":"user","content":[{"type":"text","text":` + jsonString(clientEnv+"\ntask") + `}]}]}`,
		"assistant first": `{"messages":[{"role":"assistant","content":"hi"},{"role":"user","content":"task"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := injectClaudeCodeProfileAttachments([]byte(body), "claude-sonnet-4-6", profile)
			require.NoError(t, err)
			require.NotContains(t, string(out), "/caller")
			require.NotContains(t, string(out), "caller@example.com")
			require.NotContains(t, string(out), "claude-opus-4-8")
			if name == "assistant first" {
				require.Equal(t, `"hi"`, gjson.GetBytes(out, "messages.0.content").Raw)
				require.JSONEq(t, gjson.GetBytes(want, "messages.0.content").Raw, gjson.GetBytes(out, "messages.1.content").Raw)
				return
			}
			require.JSONEq(t, gjson.GetBytes(want, "messages.0.content").Raw, gjson.GetBytes(out, "messages.0.content").Raw)
		})
	}

	keep := `{"messages":[{"role":"user","content":[{"type":"text","text":` + jsonString(unrelated) + `},{"type":"text","text":"task"}]}]}`
	out, err := injectClaudeCodeProfileAttachments([]byte(keep), "claude-sonnet-4-6", profile)
	require.NoError(t, err)
	content := gjson.GetBytes(out, "messages.0.content").Array()
	require.Len(t, content, 5)
	require.Equal(t, unrelated, content[3].Get("text").String(), "reminders the profile does not own are kept")
}

func TestClaudeCodeProfileAppendsSortedClientMCPTools(t *testing.T) {
	body := []byte(`{"tools":[` +
		`{"name":"mcp__zeta__run","description":"z <b>","input_schema":{"type":"object"},"defer_loading":true},` +
		`{"name":"private_tool","input_schema":{"type":"object"}},` +
		`{"name":"mcp__alpha__read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}},` +
		`{"name":"mcp__alpha__read","input_schema":{"type":"object","duplicate":true}}` +
		`],"messages":[{"role":"user","content":"hello"}]}`)
	out, err := rewriteClaudeCodeProfile(body, "claude-sonnet-4-6", "hello", DefaultClaudeAntiBanSettings().Environment.Profile)
	require.NoError(t, err)
	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 25)
	require.Equal(t, "Write", tools[22].Get("name").String())
	require.Equal(t, "mcp__alpha__read", tools[23].Get("name").String())
	require.False(t, tools[23].Get("input_schema.duplicate").Exists(), "first definition wins")
	require.Equal(t, "mcp__zeta__run", tools[24].Get("name").String())
	require.False(t, tools[24].Get("defer_loading").Exists())
	require.Equal(t, "z <b>", tools[24].Get("description").String())
	require.NotContains(t, gjson.GetBytes(out, "tools").Raw, `\u003c`)
	require.NotContains(t, string(out), "private_tool")
	for i, tool := range tools {
		require.Equal(t, i == len(tools)-1, tool.Get("cache_control").Exists(), "only tools[-1] carries the breakpoint")
	}

	repeated, err := rewriteClaudeCodeProfile(out, "claude-sonnet-4-6", "hello", DefaultClaudeAntiBanSettings().Environment.Profile)
	require.NoError(t, err)
	require.JSONEq(t, gjson.GetBytes(out, "tools").Raw, gjson.GetBytes(repeated, "tools").Raw)
}

func TestClaudeCodeProfileSessionSeedIgnoresInjectedReminders(t *testing.T) {
	ctx := WithClaudeCodeProfilePolicy(context.Background(), &Group{Platform: PlatformAnthropic, ForceClaudeCodeProfile: true})
	account := &Account{ID: 7, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	profile := DefaultClaudeAntiBanSettings().Environment.Profile
	session := func(task string) string {
		body, err := rewriteClaudeCodeProfile([]byte(`{"messages":[{"role":"user","content":`+jsonString(task)+`}]}`), "claude-sonnet-4-6", task, profile)
		require.NoError(t, err)
		_, sessionID := (&GatewayService{}).rewriteAnthropicOAuthIdentity(ctx, nil, account, body, nil, false)
		require.NotEmpty(t, sessionID)
		return sessionID
	}
	require.NotEqual(t, session("first task"), session("second task"))
	require.Equal(t, session("first task"), session("first task"))
}

func jsonString(s string) string {
	encoded, _ := marshalProfileJSON(s)
	return string(encoded)
}

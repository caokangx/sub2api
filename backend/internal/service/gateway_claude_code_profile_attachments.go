package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// profileReminderPattern matches a leading <system-reminder> block that the
// forced profile owns (environment, model identity, or session context), so a
// re-run or a real Claude Code client's own copy is replaced rather than stacked.
var profileReminderPattern = regexp.MustCompile(
	`^[ \t\r\n]*<system-reminder>\n(?:# Environment\nYou have been invoked|You are powered by the model|As you answer the user's questions)[\s\S]*?</system-reminder>[ \t]*(?:\r?\n)*`,
)

// marshalProfileJSON encodes like the CLI's JSON.stringify: no HTML escaping of
// <, > and &, which the reminders and tool descriptions contain.
func marshalProfileJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// wrapSystemReminder mirrors the CLI's il() helper: <system-reminder>\n…\n</system-reminder>.
func wrapSystemReminder(text string) string {
	return "<system-reminder>\n" + text + "\n</system-reminder>"
}

// renderProfileEnvironment mirrors the CLI's lKe()/eon() environment attachment.
func renderProfileEnvironment(profile ClaudeEnvironmentProfile) string {
	lines := []string{
		"# Environment",
		"You have been invoked in the following environment: ",
	}
	if profile.WorkingDirectory != "" {
		lines = append(lines, " - Primary working directory: "+profile.WorkingDirectory)
	}
	lines = append(lines, fmt.Sprintf(" - Is a git repository: %t", profile.GitRepository))
	if len(profile.AdditionalWorkingDirectories) > 0 {
		lines = append(lines, " - Additional working directories:")
		for _, directory := range profile.AdditionalWorkingDirectories {
			lines = append(lines, "  - "+directory)
		}
	}
	lines = append(lines,
		" - Platform: "+profile.Platform,
		" - Shell: "+profile.Shell,
		" - OS Version: "+profile.OSVersion,
	)
	return strings.Join(lines, "\n")
}

// renderProfileSessionContext mirrors the CLI's GKe() session-context attachment,
// which carries gitStatus. Returns "" when there is no git repository or its
// status is unavailable, as the CLI omits gitStatus when git detection fails.
func renderProfileSessionContext(profile ClaudeEnvironmentProfile) string {
	if !profile.GitRepository || profile.GitStatus != "clean" {
		return ""
	}
	gitLines := []string{
		"This is the git status at the start of the conversation. Note that this status is a snapshot in time, and will not update during the conversation.",
		"Current branch: " + profile.CurrentBranch,
		"Main branch (you will usually use this for PRs): " + profile.MainBranch,
	}
	if profile.GitUser != "" {
		gitLines = append(gitLines, "Git user: "+profile.GitUser)
	}
	gitLines = append(gitLines,
		"Status:",
		"(clean)",
		"Recent commits:",
		strings.Join(profile.RecentCommits, "\n"),
	)
	return "As you answer the user's questions, you can use the following context:\n" +
		"# gitStatus\n" + strings.Join(gitLines, "\n") +
		"\n\nIMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task."
}

// injectClaudeCodeProfileAttachments prepends the environment, model-identity and
// session-context system-reminders to the first user message, mirroring the CLI's
// isMeta attachments. It first removes any matching leading reminders so repeated
// runs and a real client's own copies are replaced rather than duplicated.
func injectClaudeCodeProfileAttachments(body []byte, model string, profile ClaudeEnvironmentProfile) ([]byte, error) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body, nil
	}
	index := -1
	messages.ForEach(func(key, msg gjson.Result) bool {
		if msg.Get("role").String() == "user" {
			index = int(key.Int())
			return false
		}
		return true
	})
	if index < 0 {
		return body, nil
	}

	content := gjson.GetBytes(body, fmt.Sprintf("messages.%d.content", index))
	kept, err := keptUserContentBlocks(content)
	if err != nil {
		return body, err
	}

	reminders := []string{}
	if sessionContext := renderProfileSessionContext(profile); sessionContext != "" {
		reminders = append(reminders, sessionContext)
	}
	reminders = append(reminders,
		renderProfileEnvironment(profile),
		claude.ModelIdentityText(model),
	)

	blocks := make([]json.RawMessage, 0, len(reminders)+len(kept))
	for _, text := range reminders {
		raw, marshalErr := marshalProfileJSON(anthropicSystemTextBlockPayload{Type: "text", Text: wrapSystemReminder(text)})
		if marshalErr != nil {
			return body, marshalErr
		}
		blocks = append(blocks, raw)
	}
	blocks = append(blocks, kept...)

	encoded, err := marshalProfileJSON(blocks)
	if err != nil {
		return body, err
	}
	return sjson.SetRawBytes(body, fmt.Sprintf("messages.%d.content", index), encoded)
}

func stripLeadingProfileReminders(text string) string {
	for {
		loc := profileReminderPattern.FindStringIndex(text)
		if loc == nil {
			return text
		}
		text = text[loc[1]:]
	}
}

// keptUserContentBlocks returns the first user message content as raw blocks with
// the profile's own leading reminders removed. A string content is converted to a
// single text block (its leading profile reminders stripped first).
func keptUserContentBlocks(content gjson.Result) ([]json.RawMessage, error) {
	if content.Type == gjson.String {
		text := stripLeadingProfileReminders(content.String())
		if text == "" {
			return nil, nil
		}
		raw, err := marshalProfileJSON(anthropicSystemTextBlockPayload{Type: "text", Text: text})
		if err != nil {
			return nil, err
		}
		return []json.RawMessage{raw}, nil
	}
	if !content.IsArray() {
		return nil, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal([]byte(content.Raw), &blocks); err != nil {
		return nil, err
	}
	for len(blocks) > 0 {
		block := gjson.ParseBytes(blocks[0])
		if block.Get("type").String() != "text" {
			break
		}
		original := block.Get("text").String()
		rest := stripLeadingProfileReminders(original)
		if rest == original {
			break
		}
		if rest != "" {
			encoded, err := marshalProfileJSON(rest)
			if err != nil {
				return nil, err
			}
			next, err := sjson.SetRawBytes(blocks[0], "text", encoded)
			if err != nil {
				return nil, err
			}
			blocks[0] = next
			break
		}
		blocks = blocks[1:]
	}
	return blocks, nil
}

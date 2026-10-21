package service

import (
	"encoding/json"
	"regexp"
	"strings"
)

type environmentRewriteReport struct {
	Applicable                 bool     `json:"applicable"`
	Matched                    bool     `json:"matched"`
	Changed                    bool     `json:"changed"`
	Applied                    bool     `json:"applied"`
	EnvironmentBlocks          int      `json:"environment_blocks"`
	UnmatchedEnvironmentBlocks int      `json:"unmatched_environment_blocks"`
	MemoryBlocks               int      `json:"memory_blocks"`
	UserBlocks                 int      `json:"user_blocks"`
	Formats                    []string `json:"formats"`
	SafeguardsPreserved        bool     `json:"safeguards_preserved"`
	SafeguardsRewritten        bool     `json:"safeguards_rewritten"`
	Rejected                   bool     `json:"rejected"`
}

var (
	legacyEnvironmentPattern    = regexp.MustCompile(`(?is)(<env>[ \t]*\r?\n)(.*?)(\r?\n[ \t]*</env>)`)
	modernEnvironmentHeading    = regexp.MustCompile(`(?i)^[ \t]*# Environment[ \t]*$`)
	fencedCodeMarker            = regexp.MustCompile("^[ \\t]{0,3}(`{3,}|~{3,})")
	memoryDirectoryPattern      = regexp.MustCompile(`(You have a persistent(?:,)? file-based memory(?: system)? at ` + "`" + `)[^` + "`" + `]+(` + "`" + `)`)
	memoryLinePattern           = regexp.MustCompile(`(?m)^(Memory directory:[ \t]*` + "`" + `)[^` + "`" + `]+(` + "`" + `[^\r\n]*)$`)
	memoryContentsPattern       = regexp.MustCompile(`(?m)^(Contents of )[^\r\n]+[\\/]MEMORY\.md( \(user's auto-memory, persists across conversations\):[ \t]*\r?)$`)
	preservedContextPattern     = regexp.MustCompile(`(?m)^(?:gitStatus:|## User's Current IDE Context[ \t]*$|# VSCode Extension Context[ \t]*$)`)
	liveEnvironmentHint         = regexp.MustCompile(`(?i)\b(?:cwd|working directory|platform|shell|OS Version)\s*[:=]`)
	leadingSystemContext        = regexp.MustCompile(`(?s)^([ \t\r\n]*<system-reminder>)(.*?)(</system-reminder>)`)
	leadingEnvironmentContext   = regexp.MustCompile(`(?s)^([ \t\r\n]*<environment_context>)(.*?)(</environment_context>)`)
	leadingUnwrappedEnvironment = regexp.MustCompile(`(?i)^[ \t\r\n]*(?:# Environment[ \t]*\r?\n|<env>[ \t]*\r?\n)`)
)

func rewriteClaudeEnvironmentInBody(
	body []byte,
	settings ClaudeEnvironmentRewriteSettings,
) ([]byte, environmentRewriteReport, error) {
	var report environmentRewriteReport
	if settings.Mode == ClaudeEnvironmentModeOff {
		return body, report, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return body, report, err
	}
	if safeguards, ok := decoded["safeguards"].([]any); ok && len(safeguards) > 0 {
		report.SafeguardsPreserved = true
	}
	changed := false
	rewriteText := func(text string) string {
		return rewriteEnvironmentOutsideCodeFences(text, func(segment string) string {
			if preservedContextPattern.MatchString(segment) {
				report.Applicable = true
				report.Matched = true
			}
			next := rewriteLegacyEnvironmentText(segment, settings.Profile, &report)
			next = rewriteModernEnvironmentText(next, settings.Profile, &report)
			next = rewriteMemoryEnvironmentText(next, settings.Profile, &report)
			return next
		})
	}
	if system, ok := decoded["system"].(string); ok {
		next := rewriteText(system)
		if next != system {
			decoded["system"], changed = next, true
		}
	} else if blocks, ok := decoded["system"].([]any); ok {
		if rewriteEnvironmentTextBlocks(blocks, rewriteText, &report) {
			changed = true
		}
	}
	if messages, ok := decoded["messages"].([]any); ok {
		for _, rawMessage := range messages {
			message, ok := rawMessage.(map[string]any)
			if !ok || message["role"] != "system" {
				continue
			}
			if rewriteEnvironmentMessageContent(message, rewriteText, &report) {
				changed = true
			}
		}
		for _, rawMessage := range messages {
			message, ok := rawMessage.(map[string]any)
			if !ok || message["role"] != "user" {
				continue
			}
			if rewriteFirstUserEnvironmentContent(message, settings.Profile, &report) {
				changed = true
			}
			break
		}
	}
	if settings.Mode == ClaudeEnvironmentModeRewrite {
		if rewriteSafeguardEnvironment(decoded, settings.Profile) {
			report.SafeguardsRewritten = true
			changed = true
		}
	}
	report.Changed = changed
	report.Formats = uniqueStrings(report.Formats)
	report.Matched = report.Matched || report.EnvironmentBlocks > 0 || report.MemoryBlocks > 0
	report.Rejected = settings.Mode == ClaudeEnvironmentModeRewrite &&
		settings.OnUnmatched == ClaudeEnvironmentUnmatchedReject &&
		report.Applicable &&
		(!report.Matched || report.UnmatchedEnvironmentBlocks > 0)
	report.Applied = settings.Mode == ClaudeEnvironmentModeRewrite && !report.Rejected && changed
	if !report.Applied {
		return body, report, nil
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return body, report, err
	}
	return encoded, report, nil
}

func rewriteEnvironmentOutsideCodeFences(text string, rewrite func(string) string) string {
	lines := strings.SplitAfter(text, "\n")
	var output, pending strings.Builder
	var fence string
	for _, line := range lines {
		match := fencedCodeMarker.FindStringSubmatch(strings.TrimSuffix(line, "\n"))
		if fence != "" {
			output.WriteString(line)
			if len(match) > 1 && match[1][0] == fence[0] && len(match[1]) >= len(fence) &&
				strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(line, "\n"), match[0])) == "" {
				fence = ""
			}
			continue
		}
		if len(match) > 1 {
			output.WriteString(rewrite(pending.String()))
			pending.Reset()
			output.WriteString(line)
			fence = match[1]
			continue
		}
		pending.WriteString(line)
	}
	output.WriteString(rewrite(pending.String()))
	return output.String()
}

func rewriteLegacyEnvironmentText(text string, profile ClaudeEnvironmentProfile, report *environmentRewriteReport) string {
	return legacyEnvironmentPattern.ReplaceAllStringFunc(text, func(block string) string {
		match := legacyEnvironmentPattern.FindStringSubmatch(block)
		if len(match) != 4 {
			return block
		}
		newline := "\n"
		if strings.Contains(match[2], "\r\n") {
			newline = "\r\n"
		}
		lines := strings.Split(strings.ReplaceAll(match[2], "\r\n", "\n"), "\n")
		next, matched := rewriteEnvironmentLines(lines, profile, false)
		if !matched {
			report.Applicable = true
			report.UnmatchedEnvironmentBlocks++
			return block
		}
		report.Applicable = true
		report.EnvironmentBlocks++
		report.Formats = append(report.Formats, "legacy")
		return match[1] + strings.Join(next, newline) + match[3]
	})
}

func rewriteModernEnvironmentText(text string, profile ClaudeEnvironmentProfile, report *environmentRewriteReport) string {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for index := 0; index < len(lines); index++ {
		if !modernEnvironmentHeading.MatchString(lines[index]) {
			continue
		}
		end := modernEnvironmentSectionEnd(lines, index)
		section := append([]string(nil), lines[index:end]...)
		next, matched := rewriteEnvironmentLines(section, profile, true)
		if !matched {
			staticProduct := containsAnyLine(section, "The most recent Claude models", "Claude Code is available as a CLI")
			liveHost := liveEnvironmentHint.MatchString(strings.Join(section, "\n"))
			if !staticProduct || liveHost {
				report.Applicable = true
				report.UnmatchedEnvironmentBlocks++
			}
			continue
		}
		report.Applicable = true
		report.EnvironmentBlocks++
		report.Formats = append(report.Formats, "modern")
		lines = append(lines[:index], append(next, lines[end:]...)...)
		index += len(next) - 1
	}
	return strings.Join(lines, newline)
}

func modernEnvironmentSectionEnd(lines []string, start int) int {
	for index := start + 1; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		if strings.HasPrefix(trimmed, "</system-reminder>") ||
			strings.HasPrefix(trimmed, "</environment_context>") ||
			strings.HasPrefix(trimmed, "</env>") ||
			strings.HasPrefix(strings.ToLower(trimmed), "gitstatus:") {
			return index
		}
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level <= 6 && level < len(trimmed) && trimmed[level] == ' ' {
				return index
			}
		}
	}
	return len(lines)
}

func rewriteEnvironmentLines(lines []string, profile ClaudeEnvironmentProfile, modern bool) ([]string, bool) {
	matched := false
	prefixes := []struct {
		legacy string
		modern string
		value  string
	}{
		{"Working directory:", "- Primary working directory:", profile.WorkingDirectory},
		{"Platform:", "- Platform:", profile.Platform},
		{"Shell:", "- Shell:", profile.Shell},
		{"OS Version:", "- OS Version:", profile.OSVersion},
	}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, field := range prefixes {
			label := field.legacy
			if modern {
				label = field.modern
			}
			if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(label)) {
				colon := strings.Index(line, ":")
				lines[index] = line[:colon+1] + " " + field.value
				matched = true
				break
			}
		}
	}
	var output []string
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if !strings.HasPrefix(strings.ToLower(trimmed), "additional working directories:") {
			output = append(output, line)
			continue
		}
		matched = true
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if len(profile.AdditionalWorkingDirectories) > 0 {
			output = append(output, line[:strings.Index(line, ":")+1])
			for _, directory := range profile.AdditionalWorkingDirectories {
				output = append(output, indent+"  - "+directory)
			}
		}
		for index+1 < len(lines) {
			next := lines[index+1]
			nextIndent := len(next) - len(strings.TrimLeft(next, " \t"))
			if !strings.HasPrefix(strings.TrimSpace(next), "- ") || nextIndent <= len(indent) {
				break
			}
			index++
		}
	}
	if modern && matched && len(profile.AdditionalWorkingDirectories) > 0 &&
		!containsLinePrefix(output, "- Additional working directories:") {
		insertAt := len(output)
		for index, line := range output {
			if strings.HasPrefix(strings.TrimSpace(line), "- Platform:") {
				insertAt = index
				break
			}
		}
		added := []string{" - Additional working directories:"}
		for _, directory := range profile.AdditionalWorkingDirectories {
			added = append(added, "   - "+directory)
		}
		output = append(output[:insertAt], append(added, output[insertAt:]...)...)
	}
	return output, matched
}

func rewriteMemoryEnvironmentText(text string, profile ClaudeEnvironmentProfile, report *environmentRewriteReport) string {
	rewrite := func(pattern *regexp.Regexp, value string, replacement func([]string) string) string {
		return pattern.ReplaceAllStringFunc(value, func(match string) string {
			parts := pattern.FindStringSubmatch(match)
			if len(parts) < 3 {
				return match
			}
			report.Applicable = true
			report.MemoryBlocks++
			return replacement(parts)
		})
	}
	text = rewrite(memoryDirectoryPattern, text, func(parts []string) string {
		return parts[1] + profile.MemoryDirectory + parts[2]
	})
	text = rewrite(memoryLinePattern, text, func(parts []string) string {
		return parts[1] + profile.MemoryDirectory + parts[2]
	})
	return rewrite(memoryContentsPattern, text, func(parts []string) string {
		return parts[1] + strings.TrimRight(profile.MemoryDirectory, "/\\") + "/MEMORY.md" + parts[2]
	})
}

func rewriteEnvironmentTextBlocks(blocks []any, rewrite func(string) string, report *environmentRewriteReport) bool {
	changed := false
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		if kind, exists := block["type"]; exists && kind != "text" {
			continue
		}
		text, ok := block["text"].(string)
		if !ok {
			continue
		}
		next := rewrite(text)
		if next != text {
			block["text"] = next
			changed = true
		}
	}
	return changed
}

func rewriteEnvironmentMessageContent(message map[string]any, rewrite func(string) string, report *environmentRewriteReport) bool {
	switch content := message["content"].(type) {
	case string:
		next := rewrite(content)
		if next != content {
			message["content"] = next
			return true
		}
	case []any:
		return rewriteEnvironmentTextBlocks(content, rewrite, report)
	}
	return false
}

func rewriteFirstUserEnvironmentContent(message map[string]any, profile ClaudeEnvironmentProfile, report *environmentRewriteReport) bool {
	rewrite := func(text string) string {
		rewriteContext := func(value string) string {
			lower := strings.ToLower(value)
			hasEnvironment := strings.Contains(lower, "# environment\n") ||
				strings.Contains(lower, "<env>\n") || strings.Contains(lower, "<env>\r\n")
			hasContext := memoryDirectoryPattern.MatchString(value) || memoryLinePattern.MatchString(value) ||
				memoryContentsPattern.MatchString(value) || preservedContextPattern.MatchString(value)
			if !hasEnvironment && !hasContext {
				return value
			}
			report.Applicable = true
			report.UserBlocks++
			return rewriteEnvironmentOutsideCodeFences(value, func(segment string) string {
				next := rewriteLegacyEnvironmentText(segment, profile, report)
				next = rewriteModernEnvironmentText(next, profile, report)
				return rewriteMemoryEnvironmentText(next, profile, report)
			})
		}
		remaining := text
		var prefix strings.Builder
		for {
			var match []string
			for _, pattern := range []*regexp.Regexp{leadingSystemContext, leadingEnvironmentContext} {
				if candidate := pattern.FindStringSubmatch(remaining); len(candidate) == 4 {
					match = candidate
					break
				}
			}
			if len(match) != 4 {
				break
			}
			prefix.WriteString(match[1])
			prefix.WriteString(rewriteContext(match[2]))
			prefix.WriteString(match[3])
			remaining = strings.TrimPrefix(remaining, match[0])
		}
		if !leadingUnwrappedEnvironment.MatchString(remaining) {
			return prefix.String() + remaining
		}
		return prefix.String() + rewriteContext(remaining)
	}
	switch content := message["content"].(type) {
	case string:
		next := rewrite(content)
		if next != content {
			message["content"] = next
			return true
		}
	case []any:
		return rewriteEnvironmentTextBlocks(content, rewrite, report)
	}
	return false
}

func rewriteSafeguardEnvironment(body map[string]any, profile ClaudeEnvironmentProfile) bool {
	safeguards, ok := body["safeguards"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, raw := range safeguards {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "dangerous_tool_use" {
			continue
		}
		context, ok := item["classifier_context"].(map[string]any)
		if !ok {
			continue
		}
		for key, value := range map[string]string{
			"platform":      profile.Platform,
			"live_cwd":      profile.WorkingDirectory,
			"home_dir":      profile.HomeDirectory,
			"user_identity": profile.UserIdentity,
		} {
			if value != "" && context[key] != value {
				context[key] = value
				changed = true
			}
		}
	}
	return changed
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func containsAnyLine(lines []string, fragments ...string) bool {
	for _, line := range lines {
		for _, fragment := range fragments {
			if strings.Contains(line, fragment) {
				return true
			}
		}
	}
	return false
}

func containsLinePrefix(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

// fingerprintSalt 是计算 cc_version 后缀指纹的盐值。
//
// 来源：与 Parrot src/transform/cc_mimicry.py 的 FINGERPRINT_SALT 完全一致；
// 这是真实 Claude Code CLI 抓包推导出的常量，改动会导致 fp 与 CLI 不一致，
// 进一步触发 Anthropic 的第三方检测。
const fingerprintSalt = "59cf53e54c78"

var (
	leadingSystemReminderPattern = regexp.MustCompile(`^[ \t\r\n]*<system-reminder>[\s\S]*?</system-reminder>[ \t]*(?:\r?\n)*`)
	systemReminderPattern        = regexp.MustCompile(`<system-reminder>[\s\S]*?</system-reminder>`)
)

// computeClaudeCodeFingerprint 复刻真实 Claude Code CLI 的 cc_version 指纹算法：
//
//  1. 按 user 消息与 text block 顺序取首段非 system-reminder 正文
//  2. 取该文本的第 4、7、20 个 UTF-16 code unit（不足以 '0' 补齐）
//  3. SHA256(SALT + chars + cc_version) 取 hex 前 3 字符
//
// 算法来自 Parrot src/transform/cc_mimicry.py:compute_fingerprint，与官方 CLI 字节对齐。
// 任何偏差都会导致 cc_version=X.Y.Z.{fp} 在上游侧与真实 CLI 不一致。
func computeClaudeCodeFingerprint(body []byte, version string) string {
	return computeClaudeCodeFingerprintForText(firstUserTextForBilling(body), version)
}

func computeClaudeCodeFingerprintForText(firstText, version string) string {
	codeUnits := utf16.Encode([]rune(firstText))
	indices := []int{4, 7, 20}
	sampled := make([]uint16, 0, len(indices))
	for _, i := range indices {
		if i < len(codeUnits) {
			sampled = append(sampled, codeUnits[i])
		} else {
			sampled = append(sampled, uint16('0'))
		}
	}
	chars := string(utf16.Decode(sampled))
	sum := sha256.Sum256([]byte(fingerprintSalt + chars + version))
	return hex.EncodeToString(sum[:])[:3]
}

// firstUserTextForBilling 从入站 messages 选择真实用户正文。完整闭合的
// <system-reminder> 段只从指纹输入剥离，不修改实际请求消息。
func firstUserTextForBilling(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	first := ""
	found := false
	messages.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		skippedReminder := false
		visitText := func(text string) bool {
			original := text
			for leadingSystemReminderPattern.MatchString(text) {
				text = leadingSystemReminderPattern.ReplaceAllString(text, "")
			}
			text = systemReminderPattern.ReplaceAllString(text, "")
			if text != original {
				skippedReminder = true
				if strings.TrimSpace(text) == "" {
					return true
				}
			}
			first = text
			found = true
			return false
		}
		content := msg.Get("content")
		if content.Type == gjson.String {
			if !visitText(content.String()) {
				return false
			}
		} else if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				if block.Get("type").String() != "text" {
					return true
				}
				return visitText(block.Get("text").String())
			})
			if found {
				return false
			}
		}
		if !skippedReminder {
			return false
		}
		return true
	})
	return first
}

// extractFirstUserText returns the first raw user text for stable session
// derivation. Billing uses firstUserTextForBilling instead.
func extractFirstUserText(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	first := ""
	messages.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		content := msg.Get("content")
		if content.Type == gjson.String {
			first = content.String()
			return false
		}
		if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				if block.Get("type").String() == "text" {
					first = block.Get("text").String()
					return false
				}
				return true
			})
		}
		return false
	})
	return first
}

// buildBillingAttributionText 构造 system 数组的 billing attribution 文本。
//
// 形态对齐真实 Claude Code CLI：
//
//	x-anthropic-billing-header: cc_version=2.1.284.{fp}; cc_entrypoint=cli;
//
// Claude Code 2.1.284 has conditional cch branches. This gateway deliberately
// does not synthesize or retain cch; the server owns version and entrypoint.
//
// 此 block 不带 cache_control（与真实 CLI 一致；cache breakpoint 由后续的
// Claude Code prompt block 承担）。
func buildBillingAttributionText(body []byte) (string, error) {
	return buildBillingAttributionTextForSample(firstUserTextForBilling(body))
}

func buildBillingAttributionTextForSample(sample string) (string, error) {
	cliVersion := claude.CLICurrentVersion
	fp := computeClaudeCodeFingerprintForText(sample, cliVersion)
	return fmt.Sprintf(
		"x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=cli;",
		cliVersion, fp,
	), nil
}

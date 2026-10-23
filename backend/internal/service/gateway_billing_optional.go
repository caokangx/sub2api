package service

import (
	"fmt"
	"github.com/tidwall/gjson"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var billingOptionalPatterns = map[string]*regexp.Regexp{
	"cc_prev_req":    regexp.MustCompile(`^req_[A-Za-z0-9_-]{1,36}$`),
	"cc_prompt_id":   regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`),
	"cc_turn_origin": regexp.MustCompile(`^[a-z][a-z_]{0,31}$`),
	"cc_workload":    regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`),
}
var billingOptionalOrder = []string{"cc_workload", "cc_is_subagent", "cc_prev_req", "cc_prompt_id", "cc_turn_origin", "cc_prompt_index", "cc_turn_index"}

func mergeClaudeBillingOptional(base string, body []byte) string {
	values := map[string]string{}
	rejected := map[string]bool{}
	reject := func(key, reason string) {
		rejected[key] = true
		delete(values, key)
		field := key
		if _, known := billingOptionalPatterns[key]; !known && key != "cc_prompt_index" && key != "cc_turn_index" && key != "cc_is_subagent" {
			field = "unknown"
		}
		slog.Debug("claude_billing_optional_dropped", "field", field, "reason", reason)
	}
	parse := func(text string) {
		const prefix = "x-anthropic-billing-header:"
		text = strings.TrimSpace(text)
		if !strings.HasPrefix(text, prefix) {
			return
		}
		if len(text) > 4096 || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			slog.Debug("claude_billing_block_dropped", "reason", "oversize_or_control")
			return
		}
		for _, part := range strings.Split(strings.TrimPrefix(text, prefix), ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			key, value, ok := strings.Cut(part, "=")
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if !ok {
				if key != "" {
					reject(key, "malformed")
				}
				continue
			}
			valid := false
			if pattern, ok := billingOptionalPatterns[key]; ok {
				valid = pattern.MatchString(value)
			}
			switch key {
			case "cc_prompt_index", "cc_turn_index":
				min := int64(0)
				if key == "cc_turn_index" {
					min = 1
				}
				n, err := strconv.ParseInt(value, 10, 64)
				valid = err == nil && n >= min && n <= 10000000 && strconv.FormatInt(n, 10) == value
			case "cc_is_subagent":
				valid = value == "true" || value == "false"
			case "cc_version", "cc_entrypoint", "cch":
				continue
			}
			if !valid {
				reject(key, "invalid_or_unknown")
				continue
			}
			if previous, ok := values[key]; ok && previous != value {
				reject(key, "conflict")
				continue
			}
			if !rejected[key] {
				values[key] = value
			}
		}
	}
	system := gjson.GetBytes(body, "system")
	if system.Type == gjson.String {
		parse(system.String())
	} else if system.IsArray() {
		system.ForEach(func(_, block gjson.Result) bool { parse(block.Get("text").String()); return true })
	}
	for _, key := range billingOptionalOrder {
		if value, ok := values[key]; ok && !(key == "cc_is_subagent" && value == "false") {
			base += fmt.Sprintf(" %s=%s;", key, value)
		}
	}
	return base
}

package service

import (
	"fmt"
	"github.com/tidwall/gjson"
	"math"
	"sort"
)

// validateNativeClaudeFields validates the protocol envelope only. Tool schemas,
// tool inputs and vendor content blocks remain open objects.
func validateNativeClaudeFields(body []byte, ignoreMaxTokens bool) error {
	root := gjson.ParseBytes(body)
	if !gjson.ValidBytes(body) || !root.IsObject() {
		return fmt.Errorf("request body must be a JSON object")
	}
	var unknown []string
	root.ForEach(func(key, _ gjson.Result) bool {
		if _, ok := claudeMessageAllowedFields[key.String()]; !ok {
			unknown = append(unknown, key.String())
		}
		return true
	})
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unsupported top-level fields: %v", unknown)
	}
	fail := func(path, kind string) error { return fmt.Errorf("%s must be %s", path, kind) }
	for _, field := range []string{"model", "speed"} {
		if v := root.Get(field); v.Exists() && v.Type != gjson.String {
			return fail(field, "a string")
		}
	}
	if v := root.Get("stream"); v.Exists() && v.Type != gjson.True && v.Type != gjson.False {
		return fail("stream", "a boolean")
	}
	for _, field := range []string{"metadata", "thinking", "output_config", "context_management"} {
		if v := root.Get(field); v.Exists() && !v.IsObject() {
			return fail(field, "an object")
		}
	}
	if v := root.Get("metadata.user_id"); v.Exists() && v.Type != gjson.String {
		return fail("metadata.user_id", "a string")
	}
	for _, field := range []string{"thinking.type", "output_config.effort"} {
		if v := root.Get(field); v.Exists() && v.Type != gjson.String {
			return fail(field, "a string")
		}
	}
	if v := root.Get("thinking.budget_tokens"); v.Exists() && !nativeClaudeInteger(v) {
		return fail("thinking.budget_tokens", "an integer")
	}
	if !ignoreMaxTokens {
		if v := root.Get("max_tokens"); v.Exists() && (!nativeClaudeInteger(v) || v.Float() <= 0) {
			return fail("max_tokens", "a positive integer")
		}
	}
	for _, field := range []string{"temperature", "top_p", "top_k"} {
		if v := root.Get(field); v.Exists() && v.Type != gjson.Null && v.Type != gjson.Number {
			return fail(field, "a number")
		}
	}
	if v := root.Get("top_k"); v.Exists() && v.Type != gjson.Null && !nativeClaudeInteger(v) {
		return fail("top_k", "an integer")
	}
	if v := root.Get("thinking.type"); v.Exists() {
		switch v.String() {
		case "enabled", "disabled", "adaptive", "between_tools":
		default:
			return fmt.Errorf("unsupported thinking.type")
		}
	}
	for _, field := range []string{"tools", "messages", "stop_sequences"} {
		if v := root.Get(field); v.Exists() && !v.IsArray() {
			return fail(field, "an array")
		}
	}
	if v := root.Get("system"); v.Exists() && v.Type != gjson.Null && v.Type != gjson.String && !v.IsArray() {
		return fail("system", "a string or text block array")
	}
	var err error
	if root.Get("system").IsArray() {
		root.Get("system").ForEach(func(_, block gjson.Result) bool {
			if !block.IsObject() || block.Get("type").String() != "text" || block.Get("text").Type != gjson.String {
				err = fail("system", "text blocks")
			}
			return err == nil
		})
		if err != nil {
			return err
		}
	}
	root.Get("tools").ForEach(func(_, tool gjson.Result) bool {
		if !tool.IsObject() {
			err = fail("tools", "objects")
			return false
		}
		if v := tool.Get("name"); v.Exists() && v.Type != gjson.String {
			err = fail("tools.name", "a string")
		}
		if v := tool.Get("input_schema"); v.Exists() && !v.IsObject() {
			err = fail("tools.input_schema", "an object")
		}
		return err == nil
	})
	if err != nil {
		return err
	}
	root.Get("messages").ForEach(func(_, msg gjson.Result) bool {
		if !msg.IsObject() {
			err = fail("messages", "objects")
			return false
		}
		role := msg.Get("role").String()
		if role != "user" && role != "assistant" && role != "system" {
			err = fmt.Errorf("messages.role must be user, assistant or system")
			return false
		}
		content := msg.Get("content")
		if content.Type != gjson.String && !content.IsArray() {
			err = fail("messages.content", "a string or block array")
			return false
		}
		if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				if !block.IsObject() || block.Get("type").Type != gjson.String {
					err = fail("messages.content", "typed block objects")
				}
				return err == nil
			})
		}
		return err == nil
	})
	return err
}

func nativeClaudeInteger(v gjson.Result) bool {
	return v.Type == gjson.Number && v.Float() == math.Trunc(v.Float()) && v.Float() >= -9007199254740991 && v.Float() <= 9007199254740991
}

func validateNativeClaudeRequest(body []byte, model string, forceProfile bool) error {
	if err := validateNativeClaudeFields(body, forceProfile); err != nil {
		return err
	}
	if forceProfile {
		body, _ = deleteJSONPathBytes(body, "max_tokens")
		body, _ = deleteJSONPathBytes(body, "tool_choice")
		body, _ = normalizeClaudeCodeTemperature(body, model)
	}
	return validateClaude55Request(body, model)
}

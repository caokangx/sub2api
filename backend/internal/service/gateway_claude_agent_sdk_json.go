package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type agentSDKJSONContext uint8

const (
	agentSDKJSONUnknown agentSDKJSONContext = iota
	agentSDKJSONRoot
	agentSDKJSONMessage
	agentSDKJSONContentBlock
	agentSDKJSONSystemBlock
	agentSDKJSONTool
	agentSDKJSONSource
	agentSDKJSONCacheControl
	agentSDKJSONThinking
	agentSDKJSONContextManagement
	agentSDKJSONContextEdit
	agentSDKJSONOutputConfig
	agentSDKJSONThread
	agentSDKJSONDiagnostics
	agentSDKJSONMetadata
	agentSDKJSONMetadataUserID
)

// marshalClaudeAgentSDKJSON serializes the protocol-owned request envelope in
// the same key order as the recorded Agent SDK 0.3.310 traffic. Arbitrary
// client-owned objects (for example tool input_schema and tool_use.input) use
// stable JSON ordering but are otherwise left untouched.
func marshalClaudeAgentSDKJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := writeClaudeAgentSDKJSON(&out, value, agentSDKJSONRoot); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func canonicalizeClaudeAgentSDKJSON(body []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	return marshalClaudeAgentSDKJSON(root)
}

func writeClaudeAgentSDKJSON(out *bytes.Buffer, value any, context agentSDKJSONContext) error {
	switch typed := value.(type) {
	case map[string]any:
		return writeClaudeAgentSDKObject(out, typed, context)
	case []any:
		out.WriteByte('[')
		childContext := agentSDKArrayChildContext(context)
		for i, item := range typed {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeClaudeAgentSDKJSON(out, item, childContext); err != nil {
				return err
			}
		}
		out.WriteByte(']')
		return nil
	case json.RawMessage:
		if !json.Valid(typed) {
			return fmt.Errorf("invalid raw Agent SDK JSON")
		}
		out.Write(typed)
		return nil
	default:
		encoded, err := marshalProfileJSON(value)
		if err != nil {
			return err
		}
		out.Write(encoded)
		return nil
	}
}

func writeClaudeAgentSDKObject(out *bytes.Buffer, object map[string]any, context agentSDKJSONContext) error {
	order := agentSDKObjectOrder(context, object)
	seen := make(map[string]struct{}, len(order))
	keys := make([]string, 0, len(object))
	for _, key := range order {
		if _, ok := object[key]; ok {
			keys = append(keys, key)
			seen[key] = struct{}{}
		}
	}
	extra := make([]string, 0, len(object)-len(keys))
	for key := range object {
		if _, ok := seen[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	keys = append(keys, extra...)

	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		encodedKey, err := marshalProfileJSON(key)
		if err != nil {
			return err
		}
		out.Write(encodedKey)
		out.WriteByte(':')
		if err := writeClaudeAgentSDKJSON(out, object[key], agentSDKChildContext(context, key)); err != nil {
			return err
		}
	}
	out.WriteByte('}')
	return nil
}

func agentSDKObjectOrder(context agentSDKJSONContext, object map[string]any) []string {
	switch context {
	case agentSDKJSONRoot:
		return []string{
			"model", "messages", "system", "tools", "metadata", "max_tokens",
			"thinking", "context_management", "safeguards", "fallbacks",
			"fallback_credit_token", "output_config", "thread", "diagnostics", "stream",
		}
	case agentSDKJSONMessage:
		return []string{"role", "content", "output_config"}
	case agentSDKJSONContentBlock:
		switch object["type"] {
		case "tool_result":
			return []string{"tool_use_id", "type", "content", "is_error", "cache_control"}
		case "tool_use":
			return []string{"type", "id", "name", "input", "cache_control"}
		case "thinking":
			return []string{"type", "thinking", "signature"}
		case "redacted_thinking":
			return []string{"type", "data"}
		case "image", "document":
			return []string{"type", "source", "title", "context", "citations", "cache_control"}
		default:
			return []string{"type", "text", "cache_control"}
		}
	case agentSDKJSONSystemBlock:
		return []string{"type", "text", "cache_control"}
	case agentSDKJSONTool:
		return []string{
			"name", "description", "input_schema", "eager_input_streaming",
			"defer_loading", "cache_control",
		}
	case agentSDKJSONSource:
		return []string{"type", "media_type", "data", "url"}
	case agentSDKJSONCacheControl:
		return []string{"type", "ttl"}
	case agentSDKJSONThinking:
		return []string{"type", "budget_tokens", "display"}
	case agentSDKJSONContextManagement:
		return []string{"edits"}
	case agentSDKJSONContextEdit:
		return []string{"type", "keep"}
	case agentSDKJSONOutputConfig:
		return []string{"effort", "task_budget"}
	case agentSDKJSONThread:
		return []string{"type", "previous_message_id"}
	case agentSDKJSONDiagnostics:
		return []string{"previous_message_id"}
	case agentSDKJSONMetadata:
		return []string{"user_id"}
	case agentSDKJSONMetadataUserID:
		return []string{"device_id", "account_uuid", "session_id", "parent_session_id", "ti", "tk"}
	default:
		return nil
	}
}

func agentSDKArrayChildContext(context agentSDKJSONContext) agentSDKJSONContext {
	switch context {
	case agentSDKJSONMessage:
		return agentSDKJSONMessage
	case agentSDKJSONContentBlock:
		return agentSDKJSONContentBlock
	case agentSDKJSONSystemBlock:
		return agentSDKJSONSystemBlock
	case agentSDKJSONTool:
		return agentSDKJSONTool
	case agentSDKJSONContextEdit:
		return agentSDKJSONContextEdit
	default:
		return agentSDKJSONUnknown
	}
}

func agentSDKChildContext(parent agentSDKJSONContext, key string) agentSDKJSONContext {
	switch parent {
	case agentSDKJSONRoot:
		switch key {
		case "messages":
			return agentSDKJSONMessage
		case "system":
			return agentSDKJSONSystemBlock
		case "tools":
			return agentSDKJSONTool
		case "metadata":
			return agentSDKJSONMetadata
		case "thinking":
			return agentSDKJSONThinking
		case "context_management":
			return agentSDKJSONContextManagement
		case "output_config":
			return agentSDKJSONOutputConfig
		case "thread":
			return agentSDKJSONThread
		case "diagnostics":
			return agentSDKJSONDiagnostics
		}
	case agentSDKJSONMessage:
		switch key {
		case "content":
			return agentSDKJSONContentBlock
		case "output_config":
			return agentSDKJSONOutputConfig
		}
	case agentSDKJSONContentBlock:
		switch key {
		case "source":
			return agentSDKJSONSource
		case "cache_control":
			return agentSDKJSONCacheControl
		}
	case agentSDKJSONSystemBlock, agentSDKJSONTool:
		if key == "cache_control" {
			return agentSDKJSONCacheControl
		}
	case agentSDKJSONContextManagement:
		if key == "edits" {
			return agentSDKJSONContextEdit
		}
	}
	return agentSDKJSONUnknown
}

func marshalClaudeAgentSDKMetadataUserID(fields map[string]any) ([]byte, error) {
	var out bytes.Buffer
	if err := writeClaudeAgentSDKJSON(&out, fields, agentSDKJSONMetadataUserID); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func formatClaudeAgentSDKMetadataUserID(raw, deviceID, accountUUID, sessionID string) string {
	fields := map[string]any{
		"device_id": deviceID, "account_uuid": accountUUID, "session_id": sessionID,
	}
	var extra map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &extra) == nil {
		for _, key := range []string{"parent_session_id", "ti", "tk"} {
			var value string
			if json.Unmarshal(extra[key], &value) == nil && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00") {
				fields[key] = value
			}
		}
	}
	encoded, _ := marshalClaudeAgentSDKMetadataUserID(fields)
	return string(encoded)
}

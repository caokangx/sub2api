package claudetelemetry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntegrationEventSequenceAndPrivacy(t *testing.T) {
	integration, exporter := newTestIntegration(t)
	body := []byte(`{
		"model":"claude-test","stream":false,
		"messages":[{"role":"user","content":[{"type":"text","text":"PROMPT_SECRET_123"}]}],
		"system":[
			{"type":"text","text":"SYSTEM_SECRET_ALPHA","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"SYSTEM_SECRET_BETA","cache_control":{"type":"ephemeral"}}
		],
		"tools":[{"name":"ToolSearch","description":"TOOL_DESCRIPTION_SECRET","input_schema":{"type":"object"}}],
		"output_config":{"effort":"high"}
	}`)
	request, err := integration.Begin(context.Background(), BeginInput{
		EventContext: testEventContext(), RequestBody: body, ObserverMode: ObserveJSON,
		IsClaudeCode: true,
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"tengu_feature_ok", "tengu_input_prompt", "tengu_tool_search_mode_decision",
		"tengu_api_before_normalize", "tengu_api_after_normalize", "tengu_sysprompt_block",
		"tengu_sysprompt_boundary_found", "tengu_sysprompt_boundary_found",
		"tengu_api_cache_breakpoints", "tengu_api_query", "tengu_api_cache_breakpoints",
	}, eventNames(request.PendingEvents()))
	require.Equal(t, Inferred, request.PendingEvents()[1].Provenance["prompt_index"])
	require.Equal(t, Derived, request.PendingEvents()[1].Provenance["prompt_length"])

	response := `{
		"type":"message","id":"msg_1","model":"claude-test","stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":7},
		"content":[{"type":"text","text":"OUTPUT_SECRET_456"}]
	}`
	require.NoError(t, integration.Observe(request, []byte(response)))
	require.NoError(t, integration.Complete(context.Background(), request, Completion{RequestID: "req_1"}))

	exporter.mu.Lock()
	events := append([]*exportItem(nil), exporter.queue...)
	exporter.mu.Unlock()
	names := make([]string, 0, len(events))
	payloads := make([]any, 0, len(events)*2)
	for _, item := range events {
		names = append(names, item.event.Name)
		payloads = append(payloads, item.event.FirstParty)
		if item.event.Datadog != nil {
			payloads = append(payloads, item.event.Datadog)
		}
	}
	require.Equal(t, append(eventNames(request.PendingEvents()),
		"tengu_feature_ok", "tengu_api_success", "tengu_feature_ok", "tengu_turn_end"), names)

	payload := string(mustJSON(payloads))
	for _, secret := range []string{
		"PROMPT_SECRET_123", "SYSTEM_SECRET_ALPHA", "SYSTEM_SECRET_BETA",
		"TOOL_DESCRIPTION_SECRET", "OUTPUT_SECRET_456", "secret-route-token",
	} {
		require.NotContains(t, payload, secret)
	}
	for _, unsupported := range []string{
		"tengu_init", "tengu_started", "startup_telemetry", "spinner",
		"process_metrics", "plugin_lifecycle", "mcp_lifecycle",
	} {
		require.NotContains(t, payload, unsupported)
	}
	success := findQueuedEvent(t, events, "tengu_api_success")
	require.Equal(t, utf16Length("OUTPUT_SECRET_456"), success.event.Metadata["textContentLength"])
	require.Equal(t, utf16Length("SYSTEM_SECRET_ALPHA")+utf16Length("SYSTEM_SECRET_BETA"),
		success.event.Metadata["systemCharLength"])
	require.Equal(t, 5, success.event.Metadata["inputTokens"])
	require.Equal(t, CompatibilityBuildTime, success.event.FirstParty.EventData.Environment.BuildTime)
	query := findQueuedEvent(t, events, "tengu_api_query")
	for _, item := range []*exportItem{query, success} {
		require.Equal(t, ProviderFirstParty, item.event.Metadata["provider"])
		require.IsType(t, int64(0), item.event.Metadata["buildAgeMins"])
		require.Equal(t, Derived, item.event.Provenance["buildAgeMins"])
	}
}

func TestIntegrationDefaultsProviderToFirstParty(t *testing.T) {
	integration, _ := newTestIntegration(t)
	eventContext := testEventContext()
	eventContext.Provider = ""
	request, err := integration.Begin(context.Background(), BeginInput{
		EventContext: eventContext, ObserverMode: ObserveJSON,
		RequestBody: []byte(`{"model":"claude-test","messages":[{"role":"user","content":"hi"}]}`),
	})
	require.NoError(t, err)
	require.Equal(t, ProviderFirstParty, request.context.Provider)
	integration.Abort(request)
}

func TestIntegrationRetryFingerprintPromptSuggestionAndToolDedup(t *testing.T) {
	integration, _ := newTestIntegration(t)
	mainBody := []byte(`{
		"model":"claude-test",
		"messages":[{"role":"user","content":"hello"}]
	}`)
	first := completeJSONRequest(t, integration, mainBody, 0, `{
		"type":"message","id":"msg_1","model":"claude-test","stop_reason":"tool_use",
		"usage":{},"content":[{"type":"tool_use","id":"tool_1","name":"mcp__fs__read","input":{"path":"private"}}]
	}`)
	require.Contains(t, eventNames(first.PendingEvents()), "tengu_input_prompt")

	retry, err := integration.Begin(context.Background(), BeginInput{
		EventContext: testEventContext(), RequestBody: mainBody,
		ObserverMode: ObserveJSON, RetryCount: 1,
	})
	require.NoError(t, err)
	require.NotContains(t, eventNames(retry.PendingEvents()), "tengu_input_prompt")
	integration.Abort(retry)

	continuationBody := []byte(`{
		"model":"claude-test",
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"mcp__fs__read","input":{"path":"PRIVATE_INPUT"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"PRIVATE_RESULT"}]}
		]
	}`)
	continuation := completeJSONRequest(t, integration, continuationBody, 0, `{
		"type":"message","id":"msg_2","model":"claude-test","stop_reason":"end_turn",
		"usage":{},"content":[{"type":"text","text":"done"}]
	}`)
	require.Contains(t, eventNames(continuation.PendingEvents()), "tengu_query_before_attachments")
	require.Contains(t, eventNames(continuation.PendingEvents()), "tengu_tool_use_success")
	duplicate, err := integration.Begin(context.Background(), BeginInput{
		EventContext: testEventContext(), RequestBody: continuationBody, ObserverMode: ObserveJSON,
	})
	require.NoError(t, err)
	require.NotContains(t, eventNames(duplicate.PendingEvents()), "tengu_tool_use_success")
	integration.Abort(duplicate)

	suggestionBody := []byte(`{
		"model":"claude-test","query_source":"prompt_suggestion",
		"messages":[{"role":"user","content":"suggest privately"}]
	}`)
	suggestion, err := integration.Begin(context.Background(), BeginInput{
		EventContext: testEventContext(), RequestBody: suggestionBody, ObserverMode: ObserveJSON,
	})
	require.NoError(t, err)
	require.NotContains(t, eventNames(suggestion.PendingEvents()), "tengu_input_prompt")
	require.NotContains(t, eventNames(suggestion.PendingEvents()), "tengu_tool_search_mode_decision")
	require.Contains(t, eventNames(suggestion.PendingEvents()), "tengu_api_query")
	integration.Abort(suggestion)
}

func TestAbortExportsNothing(t *testing.T) {
	integration, exporter := newTestIntegration(t)
	request, err := integration.Begin(context.Background(), BeginInput{
		EventContext: testEventContext(), RequestBody: []byte(
			`{"model":"claude-test","messages":[{"role":"user","content":"secret"}]}`,
		), ObserverMode: ObserveJSON,
	})
	require.NoError(t, err)
	integration.Abort(request)
	require.Zero(t, exporter.Status().QueuedEvents)
}

func TestTerminalErrorCommitsRequestEventsWithoutSuccess(t *testing.T) {
	integration, exporter := newTestIntegration(t)
	request, err := integration.Begin(context.Background(), BeginInput{
		EventContext: testEventContext(), RequestBody: []byte(
			`{"model":"claude-test","messages":[{"role":"user","content":"private"}]}`,
		), ObserverMode: ObserveJSON,
	})
	require.NoError(t, err)
	require.NoError(t, integration.Complete(context.Background(), request, Completion{RequestID: "failed"}))

	exporter.mu.Lock()
	items := append([]*exportItem(nil), exporter.queue...)
	exporter.mu.Unlock()
	require.NotEmpty(t, items)
	var names []string
	for _, item := range items {
		names = append(names, item.event.Name)
	}
	require.NotContains(t, names, "tengu_api_success")
	require.NotContains(t, names, "tengu_turn_end")
}

func completeJSONRequest(t *testing.T, integration *Integration, body []byte, retry int, response string) *Request {
	t.Helper()
	request, err := integration.Begin(context.Background(), BeginInput{
		EventContext: testEventContext(), RequestBody: body,
		ObserverMode: ObserveJSON, RetryCount: retry,
	})
	require.NoError(t, err)
	require.NoError(t, integration.Observe(request, []byte(response)))
	require.NoError(t, integration.Complete(context.Background(), request, Completion{}))
	return request
}

func newTestIntegration(t *testing.T) (*Integration, *TelemetryExporter) {
	t.Helper()
	config := DefaultConfig()
	config.RecordFile = t.TempDir() + "/events.ndjson"
	config.BatchDelay = time.Hour
	config.FlushOnExit = false
	exporter, err := NewTelemetryExporter(config)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, exporter.Close(context.Background()))
	})
	integration, err := NewIntegration(config, exporter)
	require.NoError(t, err)
	return integration, exporter
}

func findQueuedEvent(t *testing.T, events []*exportItem, name string) *exportItem {
	t.Helper()
	for _, event := range events {
		if event.event.Name == name {
			return event
		}
	}
	require.FailNow(t, "queued event not found", name)
	return nil
}

func decodeMetadata(t *testing.T, event ClaudeCodeInternalEvent) map[string]any {
	t.Helper()
	var metadata map[string]any
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	return metadata
}

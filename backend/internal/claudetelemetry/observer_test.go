package claudetelemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSSEObserverHandlesChunkBoundariesAndCompletion(t *testing.T) {
	observer := NewObserver(ObserveSSE)
	now := time.Unix(100, 0)
	observer.now = func() time.Time {
		now = now.Add(10 * time.Millisecond)
		return now
	}
	chunks := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-test\",\"usage\":{\"input_tokens\":12}}}\n\n",
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"",
		"hi🙂\"}}\r\n\r\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_1\",\"name\":\"Read\",\"input\":{}}}\n\n",
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"partial_json\":\"{\\\"path\\\":\\\"文\\\"}\"}}\n\n",
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":8}}\n\n",
		"data: {\"type\":\"message_stop\"}\n\n",
	}
	for _, chunk := range chunks {
		require.NoError(t, observer.Observe([]byte(chunk)))
	}
	result := observer.Finish()
	require.True(t, result.Successful())
	require.Equal(t, "msg_1", result.MessageID)
	require.Equal(t, "tool_use", result.StopReason)
	require.Equal(t, 4, result.TextContentLength, "JavaScript-compatible UTF-16 length")
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, "Read", result.ToolUses[1].Name)
	require.Greater(t, result.ToolUses[1].InputLength, 2)
	require.False(t, result.FirstByteAt.IsZero())
	require.False(t, result.FirstContentAt.IsZero())
}

func TestObserverRejectsStreamErrorAndTruncation(t *testing.T) {
	errorObserver := NewObserver(ObserveSSE)
	require.NoError(t, errorObserver.Observe([]byte(
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\"}}\n\n"+
			"data: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n"+
			"data: {\"type\":\"message_stop\"}\n\n",
	)))
	require.False(t, errorObserver.Finish().Successful())

	truncated := NewObserver(ObserveSSE)
	require.NoError(t, truncated.Observe([]byte(
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\"}}\n\n",
	)))
	require.False(t, truncated.Finish().Successful())
}

func TestJSONObserverRequiresCompleteMessageAndCountsOnlyShapes(t *testing.T) {
	observer := NewObserver(ObserveJSON)
	body := `{
		"type":"message","id":"msg_json","model":"claude-test","stop_reason":"end_turn",
		"usage":{"input_tokens":2,"output_tokens":3},
		"content":[
			{"type":"text","text":"private output"},
			{"type":"thinking","thinking":"secret thought"},
			{"type":"tool_use","id":"tool","name":"Bash","input":{"command":"secret"}}
		]
	}`
	require.NoError(t, observer.Observe([]byte(body[:40])))
	require.NoError(t, observer.Observe([]byte(body[40:])))
	result := observer.Finish()
	require.True(t, result.Successful())
	require.Equal(t, utf16Length("private output"), result.TextContentLength)
	require.Equal(t, utf16Length("secret thought"), result.ThinkingContentLength)
	require.Equal(t, "Bash", result.ToolUses[2].Name)

	invalid := NewObserver(ObserveJSON)
	require.NoError(t, invalid.Observe([]byte(`{"type":"message","id":"missing-stop","content":[]}`)))
	require.False(t, invalid.Finish().Successful())
}

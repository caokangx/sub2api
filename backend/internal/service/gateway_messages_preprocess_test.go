package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPreprocessNativeClaudeMessagesStrictNormalization(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.ProbeInterceptionEnabled = false
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"tool_choice":{"type":"auto"},
		"safeguards":[{"type":"dangerous_tool_use","classifier_context":{"permission_mode":"plan"}}],
		"max_tokens":12
	}`)
	result, err := PreprocessNativeClaudeMessages(body, settings)
	require.NoError(t, err)
	require.EqualValues(t, 64_000, gjson.GetBytes(result.Body, "max_tokens").Int())
	require.True(t, gjson.GetBytes(result.Body, "stream").Bool())
	require.True(t, gjson.GetBytes(result.Body, "system").IsArray())
	require.True(t, gjson.GetBytes(result.Body, "tools").IsArray())
	require.True(t, gjson.GetBytes(result.Body, "metadata").IsObject())
	require.False(t, gjson.GetBytes(result.Body, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(result.Body, "safeguards").Exists())
	require.NotEmpty(t, result.Safeguards)

	telemetryBody := claudeTelemetryBodyFromContext(
		WithClaudeMessagesPreprocessContext(context.Background(), result),
		result.Body,
	)
	require.Equal(t, "plan", gjson.GetBytes(telemetryBody, "safeguards.0.classifier_context.permission_mode").String())
}

func TestPreprocessNativeClaudeMessagesRejectsUnknownField(t *testing.T) {
	_, err := PreprocessNativeClaudeMessages([]byte(`{"messages":[],"unknown":true}`), DefaultClaudeAntiBanSettings())
	var requestErr *ClaudeMessagesPreprocessError
	require.ErrorAs(t, err, &requestErr)
	require.Equal(t, 400, requestErr.Status)
	require.Equal(t, "unsupported_body_fields", requestErr.Code)
}

func TestPreprocessNativeClaudeMessagesPreservesOriginalMaxTokensForLocalIntercept(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.ProbeInterceptionEnabled = false
	result, err := PreprocessNativeClaudeMessages(
		[]byte(`{"model":"claude-haiku-4-5","max_tokens":1,"messages":[{"role":"user","content":"health"}]}`),
		settings,
	)
	require.NoError(t, err)
	require.NotNil(t, result.OriginalMaxTokens)
	require.Equal(t, 1, *result.OriginalMaxTokens)
	require.EqualValues(t, 64_000, gjson.GetBytes(result.Body, "max_tokens").Int())
}

func TestPreprocessNativeClaudeMessagesProbe(t *testing.T) {
	tests := []struct {
		input string
		reply string
	}{
		{`{"model":"claude-test","stream":false,"messages":[{"role":"user","content":" hi "}]}`, "Hi! How can I help you today?"},
		{`{"model":"claude-test","messages":[{"role":"user","content":[{"type":"text","text":"PING"}]}]}`, "pong"},
		{`{"model":"claude-test","messages":[{"role":"user","content":"Reply Ready."}]}`, "Ready"},
	}
	for _, test := range tests {
		result, err := PreprocessNativeClaudeMessages([]byte(test.input), DefaultClaudeAntiBanSettings())
		require.NoError(t, err)
		require.NotNil(t, result.ProbeReply)
		require.Equal(t, test.reply, *result.ProbeReply)
	}

	settings := DefaultClaudeAntiBanSettings()
	settings.ProbeInterceptionEnabled = false
	result, err := PreprocessNativeClaudeMessages([]byte(`{"messages":[{"role":"user","content":"ping"}]}`), settings)
	require.NoError(t, err)
	require.Nil(t, result.ProbeReply)
}

func TestPreprocessNativeClaudeMessagesBillingUsesInboundSampleBeforeRewrite(t *testing.T) {
	settings := DefaultClaudeAntiBanSettings()
	settings.ProbeInterceptionEnabled = false
	settings.Environment.Mode = ClaudeEnvironmentModeRewrite
	body := []byte(`{
		"model":"claude-test",
		"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=1.0.0.abc; cc_entrypoint=cli;"}],
		"messages":[{"role":"user","content":"<system-reminder><env>\nWorking directory: /old\nPlatform: linux\n</env></system-reminder>\n\nRead fixture.txt and report its content."}]
	}`)
	result, err := PreprocessNativeClaudeMessages(body, settings)
	require.NoError(t, err)
	require.Contains(t, string(result.Body), "/Users/developer/project")
	require.Contains(t, gjson.GetBytes(result.Body, "system.0.text").String(),
		"cc_version="+claude.CLICurrentVersion+".469")

	ctx := WithClaudeMessagesPreprocessContext(context.Background(), result)
	headers := http.Header{}
	applyClaudeEnvironmentHeadersFromContext(ctx, headers)
	require.Equal(t, "darwin", getHeaderRaw(headers, "x-stainless-os"))
	require.Equal(t, "arm64", getHeaderRaw(headers, "x-stainless-arch"))
	require.Equal(t, "zsh", getHeaderRaw(headers, "x-claude-code-shell"))
}

func TestPreprocessNativeClaudeMessagesProbeBodyIsValidJSON(t *testing.T) {
	result, err := PreprocessNativeClaudeMessages(
		[]byte(`{"messages":[{"role":"user","content":"ping"}]}`),
		DefaultClaudeAntiBanSettings(),
	)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(result.Body, &decoded))
	require.Equal(t, defaultClaudeMessagesModel, decoded["model"])
}

package claudetelemetry

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEligibleAccountAndIdentityProvenance(t *testing.T) {
	require.True(t, EligibleAccount("anthropic", "oauth", false))
	require.False(t, EligibleAccount("anthropic", "setup-token", false))
	require.True(t, EligibleAccount("ANTHROPIC", "setup-token", true))
	require.False(t, EligibleAccount("anthropic", "apikey", true))
	require.False(t, EligibleAccount("bedrock", "oauth", true))

	input := IdentityInput{
		MetadataUserID: `{"account_uuid":"embedded-account","device_id":"device","session_id":"session"}`,
		AccountUUID:    "db-account", OrganizationUUID: "db-org",
		HMACSeed: []byte("01234567890123456789012345678901"),
	}
	identity, source := ResolveIdentity(input)
	require.Equal(t, "db-account", identity.AccountUUID)
	require.Equal(t, "db-org", identity.OrganizationUUID)
	require.Equal(t, "device", identity.DeviceID)
	require.Equal(t, "session", identity.SessionID)
	require.Equal(t, Observed, source["account_uuid"])

	fallback := IdentityInput{
		AccountFallback: "account-7", DeviceFallback: "api-key-9",
		SessionFallback: "request-session", HMACSeed: input.HMACSeed,
	}
	first, firstSource := ResolveIdentity(fallback)
	second, _ := ResolveIdentity(fallback)
	require.Equal(t, first, second)
	require.NotEmpty(t, first.AccountUUID)
	require.Equal(t, Derived, firstSource["device_id"])
	require.NotContains(t, mustJSON(first), "account-7")
}

func TestTitleClassificationRequiresExplicitSourceOrStrongSignal(t *testing.T) {
	base := requestBody{
		Messages: []message{{Role: "user", Content: json.RawMessage(`"title this"`)}},
		OutputConfig: outputConfig{Format: json.RawMessage(`{
			"type":"json_schema",
			"schema":{"type":"object","properties":{"title":{"type":"string"}},
			"required":["title"],"additionalProperties":false}
		}`)},
	}
	title, _, source := classifyRequest(base, false)
	require.False(t, title)
	require.Equal(t, "repl_main_thread", source)

	withSignal := base
	withSignal.System = json.RawMessage(`[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.284"}]`)
	title, _, _ = classifyRequest(withSignal, false)
	require.True(t, title)

	nonStreaming := base
	nonStreaming.Stream = false
	nonStreaming.Model = "anything"
	title, _, _ = classifyRequest(nonStreaming, true)
	require.True(t, title, "model and stream are not hard title-classification conditions")

	explicit := requestBody{QuerySource: "generate_session_title"}
	title, _, source = classifyRequest(explicit, false)
	require.True(t, title)
	require.Equal(t, "generate_session_title", source)

	suggestion := requestBody{Metadata: requestMetadata{QuerySource: "prompt_suggestion"}}
	_, promptSuggestion, source := classifyRequest(suggestion, false)
	require.True(t, promptSuggestion)
	require.Equal(t, "prompt_suggestion", source)
}

func TestInternalEventEnvelopeAndDatadogAllowlist(t *testing.T) {
	context := testEventContext()
	built, err := buildEvent(time.Unix(1, 0), context, "tengu_api_success", map[string]any{
		"inputTokens": 4,
	}, FieldProvenance{"inputTokens": Observed})
	require.NoError(t, err)
	require.Equal(t, "ClaudeCodeInternalEvent", built.FirstParty.EventType)
	require.Equal(t, CompatibilityProfile, built.FirstParty.EventData.Environment.Version)
	require.NotEmpty(t, built.Datadog)
	require.Equal(t, 4, built.Datadog["input_tokens"])
	require.Nil(t, datadogLog(context, "tengu_api_query", map[string]any{"model": "x"}))

	raw, err := base64.StdEncoding.DecodeString(built.FirstParty.EventData.AdditionalMetadata)
	require.NoError(t, err)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(raw, &metadata))
	require.EqualValues(t, 4, metadata["inputTokens"])

	serialized := string(mustJSON(built.FirstParty))
	for _, forbidden := range []string{"process_metrics", `"process"`, "plugin_lifecycle", "mcp_lifecycle"} {
		require.NotContains(t, serialized, forbidden)
	}
}

func TestSessionRuntimeTTLAndCapacity(t *testing.T) {
	runtime := NewSessionRuntime(time.Minute, 2)
	now := time.Unix(100, 0)
	runtime.now = func() time.Time { return now }
	for _, key := range []string{"one", "two"} {
		mutation, _, _ := runtime.begin(key, "repl_main_thread", key, false, false, nil)
		runtime.commit(mutation, "", "", now)
		now = now.Add(time.Second)
	}
	require.Equal(t, 2, runtime.Len())
	mutation, _, _ := runtime.begin("three", "repl_main_thread", "three", false, false, nil)
	runtime.commit(mutation, "", "", now)
	require.Equal(t, 2, runtime.Len())
	require.NotContains(t, runtime.sessions, "one")
	now = now.Add(2 * time.Minute)
	require.Zero(t, runtime.Len())
}

func TestSessionRuntimeReservesConcurrentPromptStateAtBegin(t *testing.T) {
	runtime := NewSessionRuntime(time.Minute, 4)
	now := time.Unix(200, 0)
	runtime.now = func() time.Time { return now }

	first, _, _ := runtime.begin("session", "repl_main_thread", "request-a", false, false, nil)
	second, _, _ := runtime.begin("session", "repl_main_thread", "request-b", false, false, nil)

	require.Equal(t, 1, first.PromptIndex)
	require.Equal(t, 2, second.PromptIndex)
	require.NotEqual(t, first.QueryChainID, second.QueryChainID)
}

func testEventContext() EventContext {
	interactive := true
	return EventContext{
		Identity: Identity{
			AccountUUID: "account", OrganizationUUID: "org",
			DeviceID: "device", SessionID: "session",
		},
		Model: "claude-test", Betas: "prompt-caching-2024-07-31",
		Environment: ClientEnvironment{
			Platform: "darwin", PlatformRaw: "darwin", Arch: "arm64",
			Version: CompatibilityProfile, VersionBase: CompatibilityProfile,
		},
		Provider: "anthropic", SubscriptionType: "pro", Interactive: &interactive,
		Route: DeliveryRoute{Key: "account-route", Token: "secret-route-token"},
	}
}

func eventNames(events []EventDescriptor) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Name)
	}
	return names
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

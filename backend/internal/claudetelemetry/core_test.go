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
	}
	identity, source := ResolveIdentity(input)
	require.Equal(t, "db-account", identity.AccountUUID)
	require.Equal(t, "db-org", identity.OrganizationUUID)
	require.Equal(t, "device", identity.DeviceID)
	require.Equal(t, "session", identity.SessionID)
	require.Equal(t, Observed, source["account_uuid"])

	embeddedOnly, _ := ResolveIdentity(IdentityInput{MetadataUserID: input.MetadataUserID})
	require.Equal(t, "embedded-account", embeddedOnly.AccountUUID)

	headerSession, _ := ResolveIdentity(IdentityInput{SessionIDHeader: "header-session"})
	require.Equal(t, "header-session", headerSession.SessionID)

	missing, missingSource := ResolveIdentity(IdentityInput{})
	require.Equal(t, Identity{}, missing)
	require.Empty(t, missingSource)

	built, err := buildEvent(time.Unix(1, 0), EventContext{Identity: missing}, "tengu_api_success", nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, built.Datadog)
	serialized := string(mustJSON(built.FirstParty))
	for _, field := range []string{"account_uuid", "organization_uuid", "device_id", "session_id"} {
		require.NotContains(t, serialized, field)
	}
	require.NotContains(t, built.Datadog, "user_bucket")
}

func TestNormalizePlatformAndShellMatchClaudeCode(t *testing.T) {
	for input, expected := range map[string]string{
		"MacOS": "darwin", "darwin": "darwin", "Linux": "linux", "Windows": "win32",
		"win32": "win32", "FreeBSD": "freebsd", "Unknown": "", "Other:haiku": "", "": "",
	} {
		require.Equal(t, expected, NormalizePlatform(input), input)
	}
	for input, expected := range map[string]string{
		"zsh": "zsh", "/bin/bash": "bash", `C:\Windows\System32\cmd.exe`: "cmd",
		"/usr/local/bin/custom-shell": "other", "": "",
	} {
		require.Equal(t, expected, NormalizeShell(input), input)
	}
}

func TestBuildAgeMinutesUsesCompiledBuildTime(t *testing.T) {
	built, err := time.Parse(time.RFC3339, CompatibilityBuildTime)
	require.NoError(t, err)
	require.EqualValues(t, 90, buildAgeMinutes(CompatibilityBuildTime, built.Add(90*time.Minute+59*time.Second)))
	require.Nil(t, buildAgeMinutes("", built))
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
		Provider: ProviderFirstParty, SubscriptionType: "pro", Interactive: &interactive,
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

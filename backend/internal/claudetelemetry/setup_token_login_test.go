package claudetelemetry

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func setupTokenLoginNames(t *testing.T, events []BuiltEvent) []string {
	t.Helper()
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Name)
	}
	return names
}

func TestSetupTokenLoginEventsFollowCLIManualPaste(t *testing.T) {
	interactive := true
	start := time.Date(2026, 11, 4, 4, 0, 0, 0, time.UTC)
	events, err := SetupTokenLoginEvents(EventContext{
		Identity:    Identity{AccountUUID: "ignored", DeviceID: "device", SessionID: "session"},
		Interactive: &interactive, Entrypoint: "cli", ClientType: "cli",
	}, SetupTokenLogin{
		CodeReceivedAt: start, ExchangedAt: start.Add(time.Second),
		ProfileFetchedAt: start.Add(2 * time.Second), ProfileStatus: 403,
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"tengu_oauth_manual_entry",
		"tengu_oauth_auth_code_received",
		"tengu_oauth_token_exchange_success",
		"tengu_feature_ok",
		"tengu_feature_sad",
		"tengu_oauth_profile_fetch_success",
		"tengu_feature_ok",
	}, setupTokenLoginNames(t, events))

	require.Equal(t, map[string]any{"automatic": false}, events[1].Metadata)
	require.Equal(t, map[string]any{"feature_name": "oauth_token_exchange"}, events[3].Metadata)
	require.Equal(t, map[string]any{"feature_name": "oauth_profile_fetch", "error_code": "token_no_auth"}, events[4].Metadata)
	require.Equal(t, map[string]any{"feature_name": "oauth_login"}, events[6].Metadata)

	for _, event := range events {
		data := event.FirstParty.EventData
		require.Nil(t, data.Auth, "a logged-out CLI sends no auth block")
		require.Equal(t, "cli", data.Entrypoint)
		require.Equal(t, "cli", data.ClientType)
		require.Equal(t, "device", data.DeviceID)
		require.Equal(t, "session", data.SessionID)
		raw, err := base64.StdEncoding.DecodeString(data.AdditionalMetadata)
		require.NoError(t, err)
		var additional map[string]any
		require.NoError(t, json.Unmarshal(raw, &additional))
		require.Equal(t, event.Metadata, additional)
		switch event.Name {
		case "tengu_feature_ok", "tengu_feature_sad":
			require.NotNil(t, event.Datadog)
		default:
			require.Nil(t, event.Datadog, "%s is not on the CLI Datadog allowlist", event.Name)
		}
	}
	require.Equal(t, start.Format(time.RFC3339Nano), events[0].FirstParty.EventData.ClientTimestamp)
	require.Equal(t, start.Add(2*time.Second).Format(time.RFC3339Nano), events[6].FirstParty.EventData.ClientTimestamp)
}

func TestSetupTokenLoginEventsExchangeFailure(t *testing.T) {
	events, err := SetupTokenLoginEvents(EventContext{}, SetupTokenLogin{
		CodeReceivedAt: time.Now(), ExchangeFailed: true,
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"tengu_oauth_manual_entry", "tengu_oauth_auth_code_received", "tengu_feature_bad",
	}, setupTokenLoginNames(t, events))
	require.Equal(t, map[string]any{"feature_name": "oauth_login", "error_code": "oauth_login_failed"}, events[2].Metadata)
}

func TestSetupTokenLoginProfileFailureClassification(t *testing.T) {
	for _, test := range []struct {
		status   int
		timedOut bool
		name     string
		code     string
	}{
		{status: 200},
		{status: 401, name: "tengu_feature_sad", code: "token_no_auth"},
		{status: 429, name: "tengu_feature_sad", code: "token_rate_limited"},
		{status: 0, timedOut: true, name: "tengu_feature_sad", code: "token_timeout"},
		{status: 0, name: "tengu_feature_sad", code: "token_network_error"},
		{status: 500, name: "tengu_feature_bad", code: "token_http_500"},
	} {
		name, code := profileFetchFailure(test.status, test.timedOut)
		require.Equal(t, test.name, name, "status %d", test.status)
		require.Equal(t, test.code, code, "status %d", test.status)
	}
}

package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/claudetelemetry"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newClaudeTelemetryManagerForTest(t *testing.T, mutate func(*ClaudeTelemetryRuntimeConfig)) (*SettingService, *ClaudeTelemetryManager, *ClaudeTelemetryRuntimeConfig) {
	t.Helper()
	t.Chdir(t.TempDir())
	settings := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})
	runtime := DefaultClaudeTelemetryRuntimeConfig()
	runtime.RecordFile = "data/telemetry/events.ndjson"
	runtime.Batch.DelayMS = 3_600_000
	runtime.Batch.Size = 2
	runtime.Batch.QueueSize = 16
	if mutate != nil {
		mutate(runtime)
	}
	_, err := settings.SaveClaudeTelemetrySettings(context.Background(), runtime)
	require.NoError(t, err)
	manager, err := NewClaudeTelemetryManager(settings)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, manager.Close(ctx))
	})
	return settings, manager, runtime
}

func TestClaudeTelemetryManagerEligibleAccounts(t *testing.T) {
	_, manager, runtime := newClaudeTelemetryManagerForTest(t, nil)
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{"anthropic oauth", &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, true},
		{"synthetic oauth", &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"synthetic_ui_test": true}}, false},
		{"setup token enabled", &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}, true},
		{"api key", &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, false},
		{"bedrock", &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock}, false},
		{"service account", &Account{Platform: PlatformAnthropic, Type: AccountTypeServiceAccount}, false},
		{"foreign oauth", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, manager.Eligible(test.account))
		})
	}

	next := cloneClaudeTelemetryConfig(runtime)
	next.SetupTokenEnabled = false
	_, err := manager.settings.SaveClaudeTelemetrySettings(context.Background(), next)
	require.NoError(t, err)
	require.False(t, manager.Eligible(&Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}))
	require.False(t, manager.Eligible(&Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}))
	require.False(t, manager.Eligible(&Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock}))
	require.False(t, manager.Eligible(&Account{Platform: PlatformAnthropic, Type: AccountTypeServiceAccount}))
}

func TestClaudeTelemetryManagerHotUpdatesGenerationAndStatus(t *testing.T) {
	settings, manager, initial := newClaudeTelemetryManagerForTest(t, nil)
	manager.mu.Lock()
	oldGeneration := manager.current
	manager.mu.Unlock()

	attempt, err := manager.Begin(context.Background(), claudetelemetry.BeginInput{
		EventContext: claudetelemetry.EventContext{
			Identity: claudetelemetry.Identity{
				AccountUUID: "account", OrganizationUUID: "org",
				DeviceID: "device", SessionID: "session",
			},
			Route: claudetelemetry.DeliveryRoute{Key: "old-generation"},
		},
		RequestBody:  []byte(`{"model":"claude-test","messages":[{"role":"user","content":"private"}]}`),
		ObserverMode: claudetelemetry.ObserveJSON,
	})
	require.NoError(t, err)
	require.NotNil(t, attempt)

	next := cloneClaudeTelemetryConfig(initial)
	next.RecordFile = "data/telemetry/next.ndjson"
	next.SetupTokenEnabled = true
	next.Batch.QueueSize = 32
	_, err = settings.SaveClaudeTelemetrySettings(context.Background(), next)
	require.NoError(t, err)

	manager.mu.Lock()
	newGeneration := manager.current
	oldRetiring := oldGeneration.retiring
	oldActive := oldGeneration.active
	manager.mu.Unlock()
	require.NotSame(t, oldGeneration, newGeneration)
	require.True(t, oldRetiring)
	require.Equal(t, 1, oldActive, "in-flight requests retain the old generation")

	status, err := settings.GetClaudeTelemetrySettings(context.Background())
	require.NoError(t, err)
	require.True(t, status.Runtime.Available)
	require.Equal(t, 32, status.Runtime.QueueCapacity)
	require.True(t, manager.Eligible(&Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}))

	attempt.ObserveJSON([]byte(`{"type":"message","id":"msg-old","model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[]}`))
	attempt.Complete(context.Background(), "request-old")
	select {
	case <-oldGeneration.closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("retired generation did not flush after its final request completed")
	}
	recorded, err := os.ReadFile(initial.RecordFile)
	require.NoError(t, err)
	require.Contains(t, string(recorded), "tengu_api_success")
}

func TestClaudeTelemetryManagerEmitSetupTokenLogin(t *testing.T) {
	settings, manager, runtime := newClaudeTelemetryManagerForTest(t, nil)
	now := time.Now()
	manager.EmitSetupTokenLogin(context.Background(), claudetelemetry.SetupTokenLogin{
		CodeReceivedAt: now, ExchangedAt: now, ProfileFetchedAt: now, ProfileStatus: 403,
	}, "")
	manager.mu.Lock()
	generation := manager.current
	manager.mu.Unlock()
	require.NoError(t, generation.exporter.Flush(context.Background()))

	recorded, err := os.ReadFile(runtime.RecordFile)
	require.NoError(t, err)
	var firstParty struct {
		Payload struct {
			Events []claudetelemetry.ClaudeCodeInternalEvent `json:"events"`
		} `json:"payload"`
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		var entry struct {
			Route string `json:"route"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry.Route != "first_party" {
			continue
		}
		require.NoError(t, json.Unmarshal([]byte(line), &firstParty))
		for _, event := range firstParty.Payload.Events {
			data := event.EventData
			names = append(names, data.EventName)
			require.Nil(t, data.Auth)
			require.Equal(t, "cli", data.Entrypoint)
			require.Len(t, data.DeviceID, 64)
			require.NotEmpty(t, data.SessionID)
			require.Equal(t, "darwin", data.Environment.Platform)
		}
	}
	require.Contains(t, names, "tengu_oauth_token_exchange_success")
	require.Contains(t, names, "tengu_feature_sad")

	next := cloneClaudeTelemetryConfig(runtime)
	next.SetupTokenEnabled = false
	next.RecordFile = "data/telemetry/disabled.ndjson"
	_, err = settings.SaveClaudeTelemetrySettings(context.Background(), next)
	require.NoError(t, err)
	manager.EmitSetupTokenLogin(context.Background(), claudetelemetry.SetupTokenLogin{ExchangeFailed: true}, "")
	manager.mu.Lock()
	generation = manager.current
	manager.mu.Unlock()
	require.NoError(t, generation.exporter.Flush(context.Background()))
	_, statErr := os.Stat(next.RecordFile)
	require.True(t, os.IsNotExist(statErr), "setup-token telemetry switch off must suppress login events")
}

func TestClaudeTelemetryManagerShutdownRespectsFlushOnExit(t *testing.T) {
	for _, test := range []struct {
		name     string
		flush    bool
		wantFile bool
	}{
		{name: "disabled", flush: false, wantFile: false},
		{name: "enabled", flush: true, wantFile: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, manager, runtime := newClaudeTelemetryManagerForTest(t, func(runtime *ClaudeTelemetryRuntimeConfig) {
				runtime.FlushOnExit = test.flush
			})
			attempt, err := manager.Begin(context.Background(), claudetelemetry.BeginInput{
				EventContext: claudetelemetry.EventContext{
					Identity: claudetelemetry.Identity{
						AccountUUID: "account", OrganizationUUID: "org",
						DeviceID: "device", SessionID: "session",
					},
					Route: claudetelemetry.DeliveryRoute{Key: "shutdown"},
				},
				RequestBody:  []byte(`{"model":"claude-test","messages":[{"role":"user","content":"private"}]}`),
				ObserverMode: claudetelemetry.ObserveJSON,
			})
			require.NoError(t, err)
			attempt.Complete(context.Background(), "")

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			require.NoError(t, manager.Close(ctx))
			_, statErr := os.Stat(runtime.RecordFile)
			require.Equal(t, test.wantFile, statErr == nil)
		})
	}
}

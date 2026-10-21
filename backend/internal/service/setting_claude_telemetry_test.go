package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type claudeTelemetrySettingRepo struct {
	mu   sync.Mutex
	data map[string]string
}

func newClaudeTelemetrySettingRepo() *claudeTelemetrySettingRepo {
	return &claudeTelemetrySettingRepo{data: map[string]string{}}
}

func (r *claudeTelemetrySettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.data[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *claudeTelemetrySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data[key], nil
}

func (r *claudeTelemetrySettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[key] = value
	return nil
}

func (r *claudeTelemetrySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.data[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r *claudeTelemetrySettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range values {
		r.data[key] = value
	}
	return nil
}

func (r *claudeTelemetrySettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]string, len(r.data))
	for key, value := range r.data {
		values[key] = value
	}
	return values, nil
}

func (r *claudeTelemetrySettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, key)
	return nil
}

func TestClaudeTelemetrySettingsDefaults(t *testing.T) {
	svc := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})

	got, err := svc.GetClaudeTelemetrySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, ClaudeTelemetryProfile, got.Profile)
	require.Equal(t, ClaudeTelemetryModeRecord, got.Mode)
	require.False(t, got.SetupTokenEnabled)
	require.Equal(t, 10_000, got.Batch.DelayMS)
	require.Equal(t, 400, got.Batch.Size)
	require.Equal(t, 8192, got.Batch.QueueSize)
	require.False(t, got.Datadog.APIKeyConfigured)
	require.Empty(t, got.Datadog.APIKey)
	require.False(t, got.Runtime.Available)
	require.Equal(t, 8192, got.Runtime.QueueCapacity)
}

func TestClaudeTelemetrySettingsSecretMaskKeepAndClear(t *testing.T) {
	ctx := context.Background()
	repo := newClaudeTelemetrySettingRepo()
	svc := NewSettingService(repo, &config.Config{})

	cfg := DefaultClaudeTelemetryRuntimeConfig()
	cfg.Mode = ClaudeTelemetryModeForward
	cfg.Datadog.Enabled = true
	cfg.Datadog.APIKey = "dd-secret"
	created, err := svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.NoError(t, err)
	require.True(t, created.Datadog.APIKeyConfigured)
	require.Empty(t, created.Datadog.APIKey)

	keep := cloneClaudeTelemetryConfig(&created.ClaudeTelemetryRuntimeConfig)
	keep.Datadog.APIKey = ""
	updated, err := svc.SaveClaudeTelemetrySettings(ctx, keep)
	require.NoError(t, err)
	require.True(t, updated.Datadog.APIKeyConfigured)

	var persisted ClaudeTelemetryRuntimeConfig
	require.NoError(t, json.Unmarshal([]byte(repo.data[SettingKeyClaudeTelemetrySettings]), &persisted))
	require.Equal(t, "dd-secret", persisted.Datadog.APIKey)

	clear := cloneClaudeTelemetryConfig(&updated.ClaudeTelemetryRuntimeConfig)
	clear.Mode = ClaudeTelemetryModeRecord
	clear.Datadog.Enabled = false
	clear.Datadog.ClearAPIKey = true
	cleared, err := svc.SaveClaudeTelemetrySettings(ctx, clear)
	require.NoError(t, err)
	require.False(t, cleared.Datadog.APIKeyConfigured)

	persisted = ClaudeTelemetryRuntimeConfig{}
	require.NoError(t, json.Unmarshal([]byte(repo.data[SettingKeyClaudeTelemetrySettings]), &persisted))
	require.Empty(t, persisted.Datadog.APIKey)
}

func TestClaudeTelemetrySettingsValidationDoesNotReplaceSnapshot(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})

	valid := DefaultClaudeTelemetryRuntimeConfig()
	valid.Mode = ClaudeTelemetryModeOff
	_, err := svc.SaveClaudeTelemetrySettings(ctx, valid)
	require.NoError(t, err)

	invalid := cloneClaudeTelemetryConfig(valid)
	invalid.Mode = "invalid"
	_, err = svc.SaveClaudeTelemetrySettings(ctx, invalid)
	require.Error(t, err)

	snapshot, err := svc.GetClaudeTelemetryRuntimeConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, ClaudeTelemetryModeOff, snapshot.Mode)
}

func TestClaudeTelemetrySettingsRequiresForwardTargetAndDatadogKey(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})

	cfg := DefaultClaudeTelemetryRuntimeConfig()
	cfg.Mode = ClaudeTelemetryModeForward
	cfg.FirstParty.Enabled = false
	_, err := svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "at least one forwarding target")

	cfg.Datadog.Enabled = true
	_, err = svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "api key is required")
}

func TestClaudeTelemetrySettingsRefreshesChangesFromAnotherReplica(t *testing.T) {
	ctx := context.Background()
	repo := newClaudeTelemetrySettingRepo()
	first := NewSettingService(repo, &config.Config{})
	second := NewSettingService(repo, &config.Config{})

	initial, err := first.GetClaudeTelemetryRuntimeConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, ClaudeTelemetryModeRecord, initial.Mode)

	next := DefaultClaudeTelemetryRuntimeConfig()
	next.Mode = ClaudeTelemetryModeOff
	_, err = second.SaveClaudeTelemetrySettings(ctx, next)
	require.NoError(t, err)
	require.NoError(t, first.RefreshClaudeTelemetryRuntimeConfig(ctx))

	refreshed, err := first.GetClaudeTelemetryRuntimeConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, ClaudeTelemetryModeOff, refreshed.Mode)
}

func TestClaudeTelemetrySettingsValidatesRangesAndURLs(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})

	cfg := DefaultClaudeTelemetryRuntimeConfig()
	cfg.Batch.QueueSize = cfg.Batch.Size - 1
	_, err := svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "queue_size")

	cfg = DefaultClaudeTelemetryRuntimeConfig()
	cfg.FirstParty.URL = "file:///tmp/events"
	_, err = svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "http(s)")

	cfg = DefaultClaudeTelemetryRuntimeConfig()
	cfg.FirstParty.URL = "http://telemetry.example.test/events"
	_, err = svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "must use HTTPS")

	cfg = DefaultClaudeTelemetryRuntimeConfig()
	cfg.Sampling.EventRates["tengu_api_success"] = 1.1
	_, err = svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "must be between 0 and 1")

	cfg = DefaultClaudeTelemetryRuntimeConfig()
	cfg.RecordFile = "/tmp/telemetry.ndjson"
	_, err = svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "under data/telemetry")

	cfg = DefaultClaudeTelemetryRuntimeConfig()
	cfg.Datadog.Enabled = false
	cfg.Datadog.APIKey = "replacement"
	cfg.Datadog.ClearAPIKey = true
	_, err = svc.SaveClaudeTelemetrySettings(ctx, cfg)
	require.ErrorContains(t, err, "cannot be set together")
}

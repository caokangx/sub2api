package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestClaudeAntiBanSettingsDefaultsAndSave(t *testing.T) {
	repo := newClaudeTelemetrySettingRepo()
	settings := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	initial, err := settings.GetClaudeAntiBanSettings(ctx)
	require.NoError(t, err)
	require.True(t, initial.ProbeInterceptionEnabled)
	require.Equal(t, ClaudeEnvironmentModeRewrite, initial.Environment.Mode)
	require.Equal(t, "darwin", initial.Environment.Profile.Platform)
	require.Equal(t, "/Users/developer/project", initial.Environment.Profile.WorkingDirectory)
	require.NotNil(t, initial.Environment.Profile.AdditionalWorkingDirectories)
	require.NotNil(t, initial.Environment.Profile.RecentCommits)

	initial.Environment.Mode = ClaudeEnvironmentModeRewrite
	initial.Environment.OnUnmatched = ClaudeEnvironmentUnmatchedReject
	initial.ProbeInterceptionEnabled = false
	saved, err := settings.SaveClaudeAntiBanSettings(ctx, initial)
	require.NoError(t, err)
	require.False(t, saved.ProbeInterceptionEnabled)
	require.Equal(t, ClaudeEnvironmentModeRewrite, saved.Environment.Mode)

	reloaded := NewSettingService(repo, &config.Config{})
	got, err := reloaded.GetClaudeAntiBanSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, saved, got)
}

func TestClaudeAntiBanThinkingStripDefaultsAndRoundTrip(t *testing.T) {
	repo := newClaudeTelemetrySettingRepo()
	settings := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	initial, err := settings.GetClaudeAntiBanSettings(ctx)
	require.NoError(t, err)
	require.False(t, initial.ThinkingStrip.Enabled)
	require.Zero(t, initial.ThinkingStrip.ForceStripPercent)

	initial.ThinkingStrip = ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 35}
	_, err = settings.SaveClaudeAntiBanSettings(ctx, initial)
	require.NoError(t, err)
	require.Equal(t, initial.ThinkingStrip, settings.ClaudeThinkingStripRuntime(ctx))

	reloaded := NewSettingService(repo, &config.Config{})
	require.Equal(t, initial.ThinkingStrip, reloaded.ClaudeThinkingStripRuntime(ctx))
}

func TestClaudeAntiBanThinkingStripLegacyJSONUsesDefaults(t *testing.T) {
	repo := newClaudeTelemetrySettingRepo()
	ctx := context.Background()
	legacy := DefaultClaudeAntiBanSettings()
	encoded, err := json.Marshal(map[string]any{
		"probe_interception_enabled": legacy.ProbeInterceptionEnabled,
		"environment":                legacy.Environment,
	})
	require.NoError(t, err)
	require.NoError(t, repo.Set(ctx, SettingKeyClaudeAntiBanSettings, string(encoded)))

	got := NewSettingService(repo, &config.Config{}).ClaudeThinkingStripRuntime(ctx)
	require.Equal(t, ClaudeThinkingStripSettings{}, got)
}

func TestClaudeAntiBanThinkingStripRejectsOutOfRangePercent(t *testing.T) {
	settings := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})
	for _, percent := range []int{-1, 101} {
		input := DefaultClaudeAntiBanSettings()
		input.ThinkingStrip = ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: percent}
		_, err := settings.SaveClaudeAntiBanSettings(context.Background(), input)
		require.Error(t, err, "percent=%d", percent)
	}
}

func TestClaudeAntiBanSettingsRejectInvalidProfile(t *testing.T) {
	settings := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})
	input := DefaultClaudeAntiBanSettings()
	input.Environment.Profile.WorkingDirectory = "relative"
	_, err := settings.SaveClaudeAntiBanSettings(context.Background(), input)
	require.Error(t, err)
}

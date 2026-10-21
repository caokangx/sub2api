package service

import (
	"context"
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
	require.Equal(t, ClaudeEnvironmentModeOff, initial.Environment.Mode)
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

func TestClaudeAntiBanSettingsRejectInvalidProfile(t *testing.T) {
	settings := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})
	input := DefaultClaudeAntiBanSettings()
	input.Environment.Profile.WorkingDirectory = "relative"
	_, err := settings.SaveClaudeAntiBanSettings(context.Background(), input)
	require.Error(t, err)
}

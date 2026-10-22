package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClaudeCodeProfileGroupAuthProjection(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "claude-code-profile@test.com")
	group, err := client.Group.Create().SetName("claude-code-profile").SetPlatform(service.PlatformAnthropic).SetForceClaudeCodeProfile(true).Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, Key: "profile-test-key", Name: "Profile", GroupID: &group.ID, Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))
	loaded, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.True(t, loaded.Group.ForceClaudeCodeProfile)
}

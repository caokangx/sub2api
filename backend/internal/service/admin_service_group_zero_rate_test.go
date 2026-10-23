//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminServiceGroupZeroRate(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}
	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "local mock", Platform: PlatformAnthropic, RateMultiplier: 0,
		IsExclusive: true, ForceClaudeCodeProfile: true,
	})
	require.NoError(t, err)
	require.Zero(t, group.RateMultiplier)
	repo.getByID = &Group{ID: 1, Name: "local mock", Platform: PlatformAnthropic, RateMultiplier: 1}
	zero := 0.0
	updated, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{RateMultiplier: &zero})
	require.NoError(t, err)
	require.Zero(t, updated.RateMultiplier)
	_, err = svc.CreateGroup(context.Background(), &CreateGroupInput{Name: "bad", RateMultiplier: -1})
	require.ErrorContains(t, err, ">= 0")
	negative := -1.0
	_, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{RateMultiplier: &negative})
	require.ErrorContains(t, err, ">= 0")
}

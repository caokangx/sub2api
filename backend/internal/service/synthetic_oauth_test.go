package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSyntheticOAuthIsolation(t *testing.T) {
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "fake-local-token", "refresh_token": "must-not-use", "expires_at": "2000-01-01T00:00:00Z"},
		Extra:       map[string]any{"synthetic_ui_test": true, UpstreamBillingProbeEnabledExtraKey: true}}
	ctx := context.Background()
	// Nil dependencies deliberately panic if a refresh, cache or network path runs.
	token, err := (&ClaudeTokenProvider{}).GetAccessToken(ctx, account)
	require.NoError(t, err)
	require.Equal(t, "fake-local-token", token)
	_, err = (&OAuthRefreshAPI{}).RefreshIfNeeded(ctx, account, nil, time.Hour)
	require.ErrorContains(t, err, "disabled")
	_, err = (&OAuthService{}).RefreshAccountToken(ctx, account)
	require.ErrorContains(t, err, "disabled")
	refresher := &ClaudeTokenRefresher{}
	require.False(t, refresher.CanRefresh(account))
	require.False(t, refresher.NeedsRefresh(account, time.Hour))
	require.False(t, upstreamBillingProbeEnabled(account))
	delete(account.Extra, "synthetic_ui_test")
	require.True(t, refresher.CanRefresh(account))
	require.True(t, refresher.NeedsRefresh(account, time.Hour))
	require.True(t, upstreamBillingProbeEnabled(account))
}

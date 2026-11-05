package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

type sevenDayWindowStatsLogRepo struct {
	usageBatchLogRepoStub
	startTimes []time.Time
}

func (r *sevenDayWindowStatsLogRepo) GetAccountWindowStats(_ context.Context, _ int64, start time.Time) (*usagestats.AccountStats, error) {
	r.startTimes = append(r.startTimes, start)
	if time.Since(start) > 24*time.Hour {
		return &usagestats.AccountStats{Requests: 20, Tokens: 2000, Cost: 12}, nil
	}
	return &usagestats.AccountStats{Requests: 2, Tokens: 200, Cost: 2}, nil
}

func TestAccountUsageService_PassiveUsage_AddsSevenDayWindowStats(t *testing.T) {
	t.Parallel()

	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			t.Parallel()

			reset := time.Now().Add(72 * time.Hour).Truncate(time.Second)
			account := &Account{
				ID:       8101,
				Platform: PlatformAnthropic,
				Type:     accountType,
				Extra: map[string]any{
					"passive_usage_7d_utilization": 0.4,
					"passive_usage_7d_reset":       float64(reset.Unix()),
				},
			}
			logRepo := &sevenDayWindowStatsLogRepo{}
			svc := &AccountUsageService{usageLogRepo: logRepo, cache: NewUsageCache()}

			usage, err := svc.getPassiveUsageForAccount(context.Background(), account)
			if err != nil {
				t.Fatalf("getPassiveUsageForAccount() error = %v", err)
			}
			if usage.SevenDay == nil || usage.SevenDay.WindowStats == nil {
				t.Fatalf("expected 7d window stats, got %#v", usage.SevenDay)
			}
			if got := usage.SevenDay.WindowStats.Cost; got != 12 {
				t.Fatalf("7d window cost = %v, want 12", got)
			}
			if usage.FiveHour == nil || usage.FiveHour.WindowStats == nil || usage.FiveHour.WindowStats.Cost != 2 {
				t.Fatalf("expected 5h window stats with cost 2, got %#v", usage.FiveHour)
			}

			wantStart := reset.Add(-7 * 24 * time.Hour)
			found := false
			for _, start := range logRepo.startTimes {
				if start.Equal(wantStart) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected 7d stats query from %v, got %v", wantStart, logRepo.startTimes)
			}

			// Second call within TTL is served from cache.
			calls := len(logRepo.startTimes)
			if _, err := svc.getPassiveUsageForAccount(context.Background(), account); err != nil {
				t.Fatalf("second getPassiveUsageForAccount() error = %v", err)
			}
			if len(logRepo.startTimes) != calls {
				t.Fatalf("expected cached window stats, got %d extra queries", len(logRepo.startTimes)-calls)
			}
		})
	}
}

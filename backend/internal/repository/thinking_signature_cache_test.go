package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type thinkingSigClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *thinkingSigClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *thinkingSigClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newThinkingSigCacheForTest(t *testing.T, cap int) (*thinkingSignatureCache, *miniredis.Miniredis, *thinkingSigClock) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	clock := &thinkingSigClock{now: time.Date(2026, 10, 27, 10, 30, 0, 0, time.UTC)}
	return newThinkingSignatureCache(rdb, cap, clock.Now), mr, clock
}

func TestThinkingSignatureCacheRegisterAndVerify(t *testing.T) {
	ctx := context.Background()
	cache, _, _ := newThinkingSigCacheForTest(t, thinkingSigDefaultCap)

	require.NoError(t, cache.RegisterSignatures(ctx, 7, []uint64{1, 2, 1 << 63}))

	got, err := cache.VerifySignatures(ctx, 7, []uint64{1, 2, 1 << 63})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureOwned, got)

	got, err = cache.VerifySignatures(ctx, 8, []uint64{1})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureForeign, got)

	got, err = cache.VerifySignatures(ctx, 7, []uint64{1, 999})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureUnknown, got)

	got, err = cache.VerifySignatures(ctx, 7, nil)
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureOwned, got)
}

func TestThinkingSignatureCacheLooksBackOnlyOneDay(t *testing.T) {
	ctx := context.Background()
	cache, _, clock := newThinkingSigCacheForTest(t, thinkingSigDefaultCap)
	require.NoError(t, cache.RegisterSignatures(ctx, 7, []uint64{42}))

	clock.Advance(23 * time.Hour)
	got, err := cache.VerifySignatures(ctx, 7, []uint64{42})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureOwned, got)

	clock.Advance(time.Hour)
	got, err = cache.VerifySignatures(ctx, 7, []uint64{42})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureUnknown, got)
}

func TestThinkingSignatureCacheSetsTTL(t *testing.T) {
	ctx := context.Background()
	cache, mr, clock := newThinkingSigCacheForTest(t, thinkingSigDefaultCap)
	require.NoError(t, cache.RegisterSignatures(ctx, 7, []uint64{42}))

	hour := clock.Now().Unix() / 3600
	bucketKey := fmt.Sprintf("%sr:%d:%d", thinkingSigKeyPrefix, hour, uint64(42)>>(64-thinkingSigBucketBits))
	require.Equal(t, time.Duration(thinkingSigKeyTTLSeconds)*time.Second, mr.TTL(bucketKey))
	require.Equal(t, time.Duration(thinkingSigKeyTTLSeconds)*time.Second, mr.TTL(fmt.Sprintf("%sc:%d", thinkingSigKeyPrefix, hour)))
}

func TestThinkingSignatureCacheEvictsOldestHourOverCap(t *testing.T) {
	ctx := context.Background()
	cache, mr, clock := newThinkingSigCacheForTest(t, 4)

	require.NoError(t, cache.RegisterSignatures(ctx, 1, []uint64{11, 12}))
	oldHour := clock.Now().Unix() / 3600
	clock.Advance(time.Hour)
	require.NoError(t, cache.RegisterSignatures(ctx, 1, []uint64{21, 22}))
	clock.Advance(time.Hour)
	require.NoError(t, cache.RegisterSignatures(ctx, 1, []uint64{31}))

	got, err := cache.VerifySignatures(ctx, 1, []uint64{11})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureUnknown, got, "oldest hour should be evicted once over cap")
	require.False(t, mr.Exists(fmt.Sprintf("%sc:%d", thinkingSigKeyPrefix, oldHour)))

	got, err = cache.VerifySignatures(ctx, 1, []uint64{21, 22, 31})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureOwned, got)
}

func TestThinkingSignatureCacheNeverEvictsCurrentHour(t *testing.T) {
	ctx := context.Background()
	cache, _, _ := newThinkingSigCacheForTest(t, 2)
	require.NoError(t, cache.RegisterSignatures(ctx, 1, []uint64{1, 2, 3}))

	got, err := cache.VerifySignatures(ctx, 1, []uint64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, service.ThinkingSignatureOwned, got)
}

func TestThinkingSignatureCacheDuplicateRegistrationDoesNotInflateCount(t *testing.T) {
	ctx := context.Background()
	cache, mr, clock := newThinkingSigCacheForTest(t, thinkingSigDefaultCap)
	require.NoError(t, cache.RegisterSignatures(ctx, 1, []uint64{5}))
	require.NoError(t, cache.RegisterSignatures(ctx, 1, []uint64{5}))

	count, err := mr.Get(fmt.Sprintf("%sc:%d", thinkingSigKeyPrefix, clock.Now().Unix()/3600))
	require.NoError(t, err)
	require.Equal(t, "1", count)
}

func TestThinkingSignatureCacheSessionState(t *testing.T) {
	ctx := context.Background()
	cache, mr, _ := newThinkingSigCacheForTest(t, thinkingSigDefaultCap)

	state, err := cache.GetSessionState(ctx, "1:abc")
	require.NoError(t, err)
	require.Empty(t, state)

	final, err := cache.SetSessionStateNX(ctx, "1:abc", "keep")
	require.NoError(t, err)
	require.Equal(t, "keep", final)

	final, err = cache.SetSessionStateNX(ctx, "1:abc", "strip:random")
	require.NoError(t, err)
	require.Equal(t, "keep", final, "an existing draw must win")

	changed, err := cache.SetSessionState(ctx, "1:abc", "strip:invalid")
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = cache.SetSessionState(ctx, "1:abc", "strip:invalid")
	require.NoError(t, err)
	require.False(t, changed, "same state must report no change")
	mr.FastForward(time.Hour)
	state, err = cache.GetSessionState(ctx, "1:abc")
	require.NoError(t, err)
	require.Equal(t, "strip:invalid", state)
	require.Equal(t, time.Duration(thinkingSessionTTLSeconds)*time.Second, mr.TTL(thinkingSessionKey("1:abc")),
		"reading the state should refresh its TTL")
}

func TestThinkingSignatureCacheSessionDrawIsAtomicUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	cache, _, _ := newThinkingSigCacheForTest(t, thinkingSigDefaultCap)

	var wg sync.WaitGroup
	results := make([]string, 20)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			desired := "keep"
			if i%2 == 0 {
				desired = "strip:random"
			}
			final, err := cache.SetSessionStateNX(ctx, "1:race", desired)
			require.NoError(t, err)
			results[i] = final
		}(i)
	}
	wg.Wait()
	for _, r := range results {
		require.Equal(t, results[0], r)
	}
}

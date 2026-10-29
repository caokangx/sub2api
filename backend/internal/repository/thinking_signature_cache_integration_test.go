//go:build integration

package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ThinkingSignatureCacheSuite struct {
	IntegrationRedisSuite
	now time.Time
}

func (s *ThinkingSignatureCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.now = time.Date(2026, 10, 27, 10, 30, 0, 0, time.UTC)
}

func (s *ThinkingSignatureCacheSuite) cache(cap int) *thinkingSignatureCache {
	return newThinkingSignatureCache(s.rdb, cap, func() time.Time { return s.now })
}

func (s *ThinkingSignatureCacheSuite) TestRegisterVerifyAndEvict() {
	cache := s.cache(3)
	s.RequireNoError(cache.RegisterSignatures(s.ctx, 7, []uint64{1, 2}))
	s.now = s.now.Add(time.Hour)
	s.RequireNoError(cache.RegisterSignatures(s.ctx, 7, []uint64{3, 4}))

	got, err := cache.VerifySignatures(s.ctx, 7, []uint64{3, 4})
	s.RequireNoError(err)
	require.Equal(s.T(), service.ThinkingSignatureOwned, got)

	got, err = cache.VerifySignatures(s.ctx, 7, []uint64{1})
	s.RequireNoError(err)
	require.Equal(s.T(), service.ThinkingSignatureUnknown, got, "oldest hour evicted over cap")

	got, err = cache.VerifySignatures(s.ctx, 8, []uint64{3})
	s.RequireNoError(err)
	require.Equal(s.T(), service.ThinkingSignatureForeign, got)
}

func (s *ThinkingSignatureCacheSuite) TestBucketsUseCompactEncoding() {
	cache := s.cache(thinkingSigDefaultCap)
	hashes := make([]uint64, 100)
	for i := range hashes {
		hashes[i] = uint64(i) << 1
	}
	s.RequireNoError(cache.RegisterSignatures(s.ctx, 7, hashes))
	encoding, err := s.rdb.Eval(s.ctx, "return redis.call('OBJECT', 'ENCODING', KEYS[1] .. ARGV[1])",
		[]string{thinkingSigKeyPrefix}, fmt.Sprintf("r:%d:0", s.now.Unix()/3600)).Text()
	s.RequireNoError(err)
	require.Equal(s.T(), "listpack", encoding)
}

func (s *ThinkingSignatureCacheSuite) TestSessionState() {
	cache := s.cache(thinkingSigDefaultCap)
	final, err := cache.SetSessionStateNX(s.ctx, "9:abc", "strip:random")
	s.RequireNoError(err)
	require.Equal(s.T(), "strip:random", final)
	final, err = cache.SetSessionStateNX(s.ctx, "9:abc", "keep")
	s.RequireNoError(err)
	require.Equal(s.T(), "strip:random", final)
	state, err := cache.GetSessionState(s.ctx, "9:abc")
	s.RequireNoError(err)
	require.Equal(s.T(), "strip:random", state)
	ttl, err := s.rdb.TTL(s.ctx, thinkingSessionKey("9:abc")).Result()
	s.RequireNoError(err)
	s.AssertTTLWithin(ttl, 23*time.Hour, 24*time.Hour)
}

func TestThinkingSignatureCacheSuite(t *testing.T) {
	suite.Run(t, new(ThinkingSignatureCacheSuite))
}

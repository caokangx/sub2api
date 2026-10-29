package repository

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 所有 key 共用 {thinking_sig} 哈希标签，保证 Lua 访问的 key 落在同一个槽。
const (
	thinkingSigKeyPrefix = "{thinking_sig}:"

	thinkingSigBucketBits    = 10
	thinkingSigBucketCount   = 1 << thinkingSigBucketBits
	thinkingSigWindowHours   = 24
	thinkingSigKeyTTLSeconds = 25 * 3600
	thinkingSigDefaultCap    = 2_000_000

	thinkingSessionTTLSeconds = 24 * 3600
)

// KEYS[1]: key 前缀（实际 key 由前缀与小时号拼出，均带同一哈希标签）
// ARGV: hour, window, ttl, cap, buckets, accountID, 然后是成对的 bucket, field
var thinkingSigRegisterScript = redis.NewScript(`
local prefix = KEYS[1]
local hour = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local cap = tonumber(ARGV[4])
local buckets = tonumber(ARGV[5])
local account = ARGV[6]

local added = 0
for i = 7, #ARGV, 2 do
	local key = prefix .. 'r:' .. hour .. ':' .. ARGV[i]
	added = added + redis.call('HSET', key, ARGV[i + 1], account)
	if redis.call('TTL', key) < 0 then
		redis.call('EXPIRE', key, ttl)
	end
end

local counter = prefix .. 'c:' .. hour
if added > 0 then
	redis.call('INCRBY', counter, added)
	if redis.call('TTL', counter) < 0 then
		redis.call('EXPIRE', counter, ttl)
	end
end

local total = 0
for h = hour - window, hour do
	total = total + (tonumber(redis.call('GET', prefix .. 'c:' .. h)) or 0)
end

local evicted = 0
local h = hour - window
while total > cap and h < hour do
	local count = tonumber(redis.call('GET', prefix .. 'c:' .. h)) or 0
	if count > 0 then
		for b = 0, buckets - 1 do
			redis.call('UNLINK', prefix .. 'r:' .. h .. ':' .. b)
		end
		redis.call('UNLINK', prefix .. 'c:' .. h)
		total = total - count
		evicted = evicted + count
	end
	h = h + 1
end
return {added, evicted}
`)

// KEYS[1]: key 前缀；ARGV: hour, window, accountID, 然后是成对的 bucket, field
// 返回 1 = 全部由该账号签发，0 = 存在查不到的签名，-1 = 存在其他账号签发的签名。
var thinkingSigVerifyScript = redis.NewScript(`
local prefix = KEYS[1]
local hour = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local account = ARGV[3]
for i = 4, #ARGV, 2 do
	local found = false
	for h = hour, hour - window + 1, -1 do
		local owner = redis.call('HGET', prefix .. 'r:' .. h .. ':' .. ARGV[i], ARGV[i + 1])
		if owner then
			if owner ~= account then
				return -1
			end
			found = true
			break
		end
	end
	if not found then
		return 0
	end
end
return 1
`)

// KEYS[1]: 会话 key；ARGV: state, ttl。已存在时只续期并返回旧值。
var thinkingSessionSetNXScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
	redis.call('EXPIRE', KEYS[1], ARGV[2])
	return current
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
return ARGV[1]
`)

// KEYS[1]: 会话 key；ARGV: state, ttl。状态已相同时只续期并返回 0，否则写入并返回 1。
var thinkingSessionSetScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current == ARGV[1] then
	redis.call('EXPIRE', KEYS[1], ARGV[2])
	return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
return 1
`)

type thinkingSignatureCache struct {
	rdb *redis.Client
	cap int
	now func() time.Time
}

func NewThinkingSignatureCache(rdb *redis.Client) service.ThinkingSignatureCache {
	return newThinkingSignatureCache(rdb, thinkingSigDefaultCap, time.Now)
}

func newThinkingSignatureCache(rdb *redis.Client, cap int, now func() time.Time) *thinkingSignatureCache {
	return &thinkingSignatureCache{rdb: rdb, cap: cap, now: now}
}

func (c *thinkingSignatureCache) currentHour() int64 {
	return c.now().Unix() / 3600
}

func thinkingSigBucketArgs(hashes []uint64) []any {
	args := make([]any, 0, len(hashes)*2)
	for _, h := range hashes {
		field := make([]byte, 8)
		binary.BigEndian.PutUint64(field, h)
		args = append(args, strconv.FormatUint(h>>(64-thinkingSigBucketBits), 10), string(field))
	}
	return args
}

func (c *thinkingSignatureCache) RegisterSignatures(ctx context.Context, accountID int64, hashes []uint64) error {
	if c == nil || c.rdb == nil || len(hashes) == 0 {
		return nil
	}
	args := []any{
		c.currentHour(),
		thinkingSigWindowHours,
		thinkingSigKeyTTLSeconds,
		c.cap,
		thinkingSigBucketCount,
		strconv.FormatInt(accountID, 10),
	}
	args = append(args, thinkingSigBucketArgs(hashes)...)
	return thinkingSigRegisterScript.Run(ctx, c.rdb, []string{thinkingSigKeyPrefix}, args...).Err()
}

func (c *thinkingSignatureCache) VerifySignatures(ctx context.Context, accountID int64, hashes []uint64) (service.ThinkingSignatureOwnership, error) {
	if len(hashes) == 0 {
		return service.ThinkingSignatureOwned, nil
	}
	if c == nil || c.rdb == nil {
		return service.ThinkingSignatureUnknown, errors.New("thinking signature cache unavailable")
	}
	args := []any{
		c.currentHour(),
		thinkingSigWindowHours,
		strconv.FormatInt(accountID, 10),
	}
	args = append(args, thinkingSigBucketArgs(hashes)...)
	code, err := thinkingSigVerifyScript.Run(ctx, c.rdb, []string{thinkingSigKeyPrefix}, args...).Int()
	if err != nil {
		return service.ThinkingSignatureUnknown, err
	}
	switch code {
	case 1:
		return service.ThinkingSignatureOwned, nil
	case -1:
		return service.ThinkingSignatureForeign, nil
	default:
		return service.ThinkingSignatureUnknown, nil
	}
}

func thinkingSessionKey(sessionKey string) string {
	return thinkingSigKeyPrefix + "s:" + sessionKey
}

func (c *thinkingSignatureCache) GetSessionState(ctx context.Context, sessionKey string) (string, error) {
	if c == nil || c.rdb == nil {
		return "", errors.New("thinking signature cache unavailable")
	}
	state, err := c.rdb.GetEx(ctx, thinkingSessionKey(sessionKey), thinkingSessionTTLSeconds*time.Second).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return state, err
}

func (c *thinkingSignatureCache) SetSessionStateNX(ctx context.Context, sessionKey, state string) (string, error) {
	if c == nil || c.rdb == nil {
		return "", errors.New("thinking signature cache unavailable")
	}
	return thinkingSessionSetNXScript.Run(ctx, c.rdb, []string{thinkingSessionKey(sessionKey)}, state, thinkingSessionTTLSeconds).Text()
}

func (c *thinkingSignatureCache) SetSessionState(ctx context.Context, sessionKey, state string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("thinking signature cache unavailable")
	}
	changed, err := thinkingSessionSetScript.Run(ctx, c.rdb, []string{thinkingSessionKey(sessionKey)}, state, thinkingSessionTTLSeconds).Int()
	return changed == 1, err
}

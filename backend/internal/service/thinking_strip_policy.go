package service

import (
	"bytes"
	"context"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ThinkingSignatureOwnership 是一组签名相对某个账号的归属结论。
type ThinkingSignatureOwnership int

const (
	ThinkingSignatureUnknown ThinkingSignatureOwnership = iota
	ThinkingSignatureOwned
	ThinkingSignatureForeign
)

// ThinkingSignatureCache 登记 thinking 签名的签发账号，并保存会话级删除状态。
type ThinkingSignatureCache interface {
	RegisterSignatures(ctx context.Context, accountID int64, hashes []uint64) error
	VerifySignatures(ctx context.Context, accountID int64, hashes []uint64) (ThinkingSignatureOwnership, error)
	// GetSessionState 返回会话状态并续期；不存在时返回空串。
	GetSessionState(ctx context.Context, sessionKey string) (string, error)
	// SetSessionStateNX 仅在会话没有状态时写入 state，返回最终生效的状态。
	SetSessionStateNX(ctx context.Context, sessionKey, state string) (string, error)
	// SetSessionState 原子写入 state；返回本次是否真的改变了状态（已是该状态时只续期）。
	SetSessionState(ctx context.Context, sessionKey, state string) (bool, error)
}

const (
	thinkingSessionStateKeep         = "keep"
	thinkingSessionStateStripInvalid = "strip:invalid"
	thinkingSessionStateStripRandom  = "strip:random"

	thinkingStripPlaceholder = `[{"type":"text","text":"(assistant content removed)"}]`

	thinkingSignatureRegisterTimeout = 200 * time.Millisecond
)

type ThinkingStripMode int

const (
	ThinkingStripModeMessages ThinkingStripMode = iota
	// ThinkingStripModeCountTokens 只读：不写会话状态，也不抽签。
	ThinkingStripModeCountTokens
)

type ThinkingStripReason string

const (
	ThinkingStripReasonNone       ThinkingStripReason = ""
	ThinkingStripReasonSession    ThinkingStripReason = "session"
	ThinkingStripReasonForeign    ThinkingStripReason = "foreign"
	ThinkingStripReasonUnknown    ThinkingStripReason = "unknown"
	ThinkingStripReasonRandom     ThinkingStripReason = "random"
	ThinkingStripReasonRedisError ThinkingStripReason = "redis_error"
)

type ThinkingStripDecision struct {
	Stripped bool
	Reason   ThinkingStripReason
	Blocks   int
}

type ThinkingStripPolicy struct {
	cache    ThinkingSignatureCache
	settings *SettingService
	randIntn func(n int) int
}

func NewThinkingStripPolicy(cache ThinkingSignatureCache, settings *SettingService) *ThinkingStripPolicy {
	if cache == nil {
		return nil
	}
	return &ThinkingStripPolicy{cache: cache, settings: settings, randIntn: rand.IntN}
}

// SetThinkingStripPolicy 注入删除 thinking 块策略；nil 表示关闭。
func (s *GatewayService) SetThinkingStripPolicy(policy *ThinkingStripPolicy) {
	if s != nil {
		s.thinkingStrip = policy
	}
}

func thinkingStripApplies(account *Account) bool {
	return account != nil && account.IsAnthropicOAuthOrSetupToken()
}

func (p *ThinkingStripPolicy) enabled(ctx context.Context) (ClaudeThinkingStripSettings, bool) {
	if p == nil || p.cache == nil || p.settings == nil {
		return ClaudeThinkingStripSettings{}, false
	}
	cfg := p.settings.ClaudeThinkingStripRuntime(ctx)
	return cfg, cfg.Enabled
}

// thinkingStripSessionKey 使用客户端原始会话 ID，并按 API Key 隔离。
func thinkingStripSessionKey(c *gin.Context, parsed *ParsedRequest) string {
	if parsed == nil {
		return ""
	}
	sessionID := ""
	if uid := ParseMetadataUserID(parsed.MetadataUserID); uid != nil {
		sessionID = strings.TrimSpace(uid.SessionID)
	}
	if sessionID == "" {
		sessionID = ClaudeCodeSessionIDFromHeader(c)
	}
	if sessionID == "" {
		return ""
	}
	var apiKeyID int64
	if parsed.SessionContext != nil {
		apiKeyID = parsed.SessionContext.APIKeyID
	}
	return strconv.FormatInt(apiKeyID, 10) + ":" + strconv.FormatUint(xxhash.Sum64String(sessionID), 16)
}

// Apply 在请求转发给 account 之前执行删除 thinking 块策略。
func (p *ThinkingStripPolicy) Apply(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, body []byte, mode ThinkingStripMode) ([]byte, ThinkingStripDecision) {
	if !thinkingStripApplies(account) {
		return body, ThinkingStripDecision{}
	}
	cfg, ok := p.enabled(ctx)
	if !ok {
		return body, ThinkingStripDecision{}
	}
	hashes, blocks, missing := collectThinkingSignatureHashes(body)
	if blocks == 0 {
		return body, ThinkingStripDecision{}
	}
	sessionKey := thinkingStripSessionKey(c, parsed)
	reason, firstInvalid := p.decide(ctx, account, sessionKey, hashes, missing, cfg, mode)
	if reason == ThinkingStripReasonNone {
		return body, ThinkingStripDecision{Blocks: blocks}
	}
	stripped, removed := StripAllThinkingBlocks(body)
	fields := []zap.Field{
		zap.String("reason", string(reason)),
		zap.Int("blocks", removed),
		zap.Int("signatures", len(hashes)),
		zap.Bool("missing_signature", missing),
		zap.Int("body_bytes_before", len(body)),
		zap.Int("body_bytes_after", len(stripped)),
		zap.Bool("first_invalid", firstInvalid),
	}
	if firstInvalid {
		fields = append(fields, zap.Bool(logger.OpsSystemLogIndexField, true))
	}
	thinkingStripLogger(ctx, account, sessionKey, mode).Info("thinking_strip.stripped", fields...)
	return stripped, ThinkingStripDecision{Stripped: true, Reason: reason, Blocks: removed}
}

func (m ThinkingStripMode) String() string {
	if m == ThinkingStripModeCountTokens {
		return "count_tokens"
	}
	return "messages"
}

// thinkingStripLogger 基于请求上下文 logger（带 request_id 等字段）附加本功能的定位字段。
func thinkingStripLogger(ctx context.Context, account *Account, sessionKey string, mode ThinkingStripMode) *zap.Logger {
	l := logger.FromContext(ctx).With(
		zap.String("component", "service.thinking_strip"),
		zap.String("mode", mode.String()),
		zap.String("session_key", sessionKey),
	)
	if account != nil {
		l = l.With(zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
	}
	return l
}

// decide 返回删除原因，以及本次是否是该会话第一次因签名不合法被删除
// （没有会话 ID 的 messages 请求每次都算第一次；count_tokens 永远不算）。
func (p *ThinkingStripPolicy) decide(ctx context.Context, account *Account, sessionKey string, hashes []uint64, missing bool, cfg ClaudeThinkingStripSettings, mode ThinkingStripMode) (ThinkingStripReason, bool) {
	writable := mode == ThinkingStripModeMessages && sessionKey != ""
	log := thinkingStripLogger(ctx, account, sessionKey, mode)

	state := ""
	if sessionKey != "" {
		current, err := p.cache.GetSessionState(ctx, sessionKey)
		if err != nil {
			log.Warn("thinking_strip.redis_error", zap.String("op", "get_session_state"), zap.Error(err))
			return ThinkingStripReasonRedisError, false
		}
		state = current
		if strings.HasPrefix(state, "strip") {
			return ThinkingStripReasonSession, false
		}
	}

	ownership := ThinkingSignatureUnknown
	if !missing {
		got, err := p.cache.VerifySignatures(ctx, account.ID, hashes)
		if err != nil {
			log.Warn("thinking_strip.redis_error", zap.String("op", "verify_signatures"), zap.Error(err))
			return ThinkingStripReasonRedisError, false
		}
		ownership = got
	}
	if ownership != ThinkingSignatureOwned {
		firstInvalid := mode == ThinkingStripModeMessages && sessionKey == ""
		if writable {
			changed, err := p.cache.SetSessionState(ctx, sessionKey, thinkingSessionStateStripInvalid)
			if err != nil {
				log.Warn("thinking_strip.redis_error", zap.String("op", "set_session_state"), zap.Error(err))
			}
			firstInvalid = changed
		}
		if ownership == ThinkingSignatureForeign {
			return ThinkingStripReasonForeign, firstInvalid
		}
		return ThinkingStripReasonUnknown, firstInvalid
	}

	if state != "" || !writable || cfg.ForceStripPercent <= 0 {
		return ThinkingStripReasonNone, false
	}
	desired := thinkingSessionStateKeep
	if p.randIntn(100) < cfg.ForceStripPercent {
		desired = thinkingSessionStateStripRandom
	}
	final, err := p.cache.SetSessionStateNX(ctx, sessionKey, desired)
	if err != nil {
		log.Warn("thinking_strip.redis_error", zap.String("op", "set_session_draw"), zap.Error(err))
		final = desired
	} else {
		log.Info("thinking_strip.session_drawn",
			zap.String("state", final),
			zap.Int("force_strip_percent", cfg.ForceStripPercent),
			zap.Bool("won_race", final == desired),
		)
	}
	if strings.HasPrefix(final, "strip") {
		return ThinkingStripReasonRandom, false
	}
	return ThinkingStripReasonNone, false
}

// RegisterIssued 登记 account 刚签发的 thinking 签名（signature 或 redacted_thinking.data）。
// 功能关闭时也登记：否则开启的瞬间，所有进行中会话的历史签名都会被判为未知。
func (p *ThinkingStripPolicy) RegisterIssued(ctx context.Context, account *Account, signatures ...string) {
	if p == nil || p.cache == nil || !thinkingStripApplies(account) || len(signatures) == 0 {
		return
	}
	hashes := make([]uint64, 0, len(signatures))
	for _, sig := range signatures {
		if sig != "" {
			hashes = append(hashes, xxhash.Sum64String(sig))
		}
	}
	if len(hashes) == 0 {
		return
	}
	regCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), thinkingSignatureRegisterTimeout)
	defer cancel()
	if err := p.cache.RegisterSignatures(regCtx, account.ID, hashes); err != nil {
		thinkingStripLogger(ctx, account, "", ThinkingStripModeMessages).Warn("thinking_strip.redis_error",
			zap.String("op", "register_signatures"), zap.Int("signatures", len(hashes)), zap.Error(err))
	}
}

// RegisterIssuedFromResponse 登记非流式响应 content[] 中的签名。
func (p *ThinkingStripPolicy) RegisterIssuedFromResponse(ctx context.Context, account *Account, body []byte) {
	if p == nil || !thinkingStripApplies(account) || !bytes.Contains(body, []byte("thinking")) {
		return
	}
	var signatures []string
	gjson.GetBytes(body, "content").ForEach(func(_, block gjson.Result) bool {
		if sig := issuedThinkingSignature(block); sig != "" {
			signatures = append(signatures, sig)
		}
		return true
	})
	p.RegisterIssued(ctx, account, signatures...)
}

// thinkingSignatureTracker 从 Anthropic SSE 事件中拼出每个 thinking 块的完整签名。
// signature_delta 可能分多段到达，在 content_block_stop 时才算完整。
type thinkingSignatureTracker struct {
	pending map[int]*strings.Builder
}

func newThinkingSignatureTracker() *thinkingSignatureTracker {
	return &thinkingSignatureTracker{pending: map[int]*strings.Builder{}}
}

// observe 处理一个已解析的 SSE 事件，返回本事件完成的签名。
func (t *thinkingSignatureTracker) observe(eventType string, event map[string]any) []string {
	switch eventType {
	case "message_start":
		clear(t.pending)
	case "content_block_start":
		idx, ok := sseEventIndex(event)
		block, _ := event["content_block"].(map[string]any)
		if !ok || block == nil {
			return nil
		}
		switch block["type"] {
		case "thinking":
			b := &strings.Builder{}
			if sig, _ := block["signature"].(string); sig != "" {
				b.WriteString(sig)
			}
			t.pending[idx] = b
		case "redacted_thinking":
			if data, _ := block["data"].(string); data != "" {
				return []string{data}
			}
		}
	case "content_block_delta":
		idx, ok := sseEventIndex(event)
		b := t.pending[idx]
		delta, _ := event["delta"].(map[string]any)
		if !ok || b == nil || delta == nil || delta["type"] != "signature_delta" {
			return nil
		}
		if sig, _ := delta["signature"].(string); sig != "" {
			b.WriteString(sig)
		}
	case "content_block_stop":
		idx, ok := sseEventIndex(event)
		b := t.pending[idx]
		if !ok || b == nil {
			return nil
		}
		delete(t.pending, idx)
		if b.Len() > 0 {
			return []string{b.String()}
		}
	}
	return nil
}

func issuedThinkingSignature(block gjson.Result) string {
	switch block.Get("type").String() {
	case "thinking":
		return block.Get("signature").String()
	case "redacted_thinking":
		return block.Get("data").String()
	}
	return ""
}

func isThinkingBlockType(blockType string) bool {
	return blockType == "thinking" || blockType == "redacted_thinking"
}

// collectThinkingSignatureHashes 返回请求历史中所有 thinking 块签名的去重哈希、块数，
// 以及是否存在没有签名的 thinking 块。
func collectThinkingSignatureHashes(body []byte) (hashes []uint64, blocks int, missing bool) {
	if !bytes.Contains(body, []byte("thinking")) {
		return nil, 0, false
	}
	seen := map[uint64]struct{}{}
	gjson.GetBytes(body, "messages").ForEach(func(_, msg gjson.Result) bool {
		msg.Get("content").ForEach(func(_, block gjson.Result) bool {
			if !isThinkingBlockType(block.Get("type").String()) {
				return true
			}
			blocks++
			sig := issuedThinkingSignature(block)
			if sig == "" {
				missing = true
				return true
			}
			h := xxhash.Sum64String(sig)
			if _, dup := seen[h]; !dup {
				seen[h] = struct{}{}
				hashes = append(hashes, h)
			}
			return true
		})
		return true
	})
	return hashes, blocks, missing
}

// StripAllThinkingBlocks 删除所有消息中的 thinking 与 redacted_thinking 块，
// 只重写受影响消息的 content 数组，其余字节保持不变。删空的 content 用固定占位文本补齐。
func StripAllThinkingBlocks(body []byte) ([]byte, int) {
	if !bytes.Contains(body, []byte("thinking")) {
		return body, 0
	}
	type replacement struct {
		start, end int
		raw        string
	}
	var replacements []replacement
	removed := 0
	gjson.GetBytes(body, "messages").ForEach(func(_, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() || content.Index <= 0 {
			return true
		}
		var kept []string
		dropped := 0
		content.ForEach(func(_, block gjson.Result) bool {
			if isThinkingBlockType(block.Get("type").String()) {
				dropped++
			} else {
				kept = append(kept, block.Raw)
			}
			return true
		})
		if dropped == 0 {
			return true
		}
		removed += dropped
		raw := thinkingStripPlaceholder
		if len(kept) > 0 {
			raw = "[" + strings.Join(kept, ",") + "]"
		}
		replacements = append(replacements, replacement{start: content.Index, end: content.Index + len(content.Raw), raw: raw})
		return true
	})
	if len(replacements) == 0 {
		return body, 0
	}
	out := make([]byte, 0, len(body))
	prev := 0
	for _, r := range replacements {
		out = append(out, body[prev:r.start]...)
		out = append(out, r.raw...)
		prev = r.end
	}
	out = append(out, body[prev:]...)
	return out, removed
}

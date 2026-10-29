package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type fakeThinkingSignatureCache struct {
	owners    map[uint64]int64
	sessions  map[string]string
	verifyErr error
	stateErr  error
	verifies  int
	nxWrites  int
}

func newFakeThinkingSignatureCache() *fakeThinkingSignatureCache {
	return &fakeThinkingSignatureCache{owners: map[uint64]int64{}, sessions: map[string]string{}}
}

func (f *fakeThinkingSignatureCache) RegisterSignatures(_ context.Context, accountID int64, hashes []uint64) error {
	for _, h := range hashes {
		f.owners[h] = accountID
	}
	return nil
}

func (f *fakeThinkingSignatureCache) VerifySignatures(_ context.Context, accountID int64, hashes []uint64) (ThinkingSignatureOwnership, error) {
	f.verifies++
	if f.verifyErr != nil {
		return ThinkingSignatureUnknown, f.verifyErr
	}
	for _, h := range hashes {
		owner, ok := f.owners[h]
		if !ok {
			return ThinkingSignatureUnknown, nil
		}
		if owner != accountID {
			return ThinkingSignatureForeign, nil
		}
	}
	return ThinkingSignatureOwned, nil
}

func (f *fakeThinkingSignatureCache) GetSessionState(_ context.Context, key string) (string, error) {
	if f.stateErr != nil {
		return "", f.stateErr
	}
	return f.sessions[key], nil
}

func (f *fakeThinkingSignatureCache) SetSessionStateNX(_ context.Context, key, state string) (string, error) {
	f.nxWrites++
	if current, ok := f.sessions[key]; ok {
		return current, nil
	}
	f.sessions[key] = state
	return state, nil
}

func (f *fakeThinkingSignatureCache) SetSessionState(_ context.Context, key, state string) (bool, error) {
	if f.sessions[key] == state {
		return false, nil
	}
	f.sessions[key] = state
	return true, nil
}

func newThinkingStripPolicyForTest(t *testing.T, cfg ClaudeThinkingStripSettings, roll int) (*ThinkingStripPolicy, *fakeThinkingSignatureCache) {
	t.Helper()
	settings := NewSettingService(newClaudeTelemetrySettingRepo(), &config.Config{})
	anti := DefaultClaudeAntiBanSettings()
	anti.ThinkingStrip = cfg
	settings.claudeAntiBanRuntime.Store(anti)
	cache := newFakeThinkingSignatureCache()
	policy := NewThinkingStripPolicy(cache, settings)
	policy.randIntn = func(int) int { return roll }
	return policy, cache
}

func oauthAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
}

const thinkingStripTestSession = "8b7f0e6a-1111-4222-8333-944455556666"

func thinkingStripParsed(withSession bool) *ParsedRequest {
	parsed := &ParsedRequest{SessionContext: &SessionContext{APIKeyID: 9}}
	if withSession {
		parsed.MetadataUserID = `{"device_id":"dev","account_uuid":"","session_id":"` + thinkingStripTestSession + `"}`
	}
	return parsed
}

func thinkingStripBody(signatures ...string) []byte {
	content := ""
	for _, sig := range signatures {
		content += `{"type":"thinking","thinking":"","signature":"` + sig + `"},`
	}
	return []byte(`{"model":"claude-sonnet-4-6","messages":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":[` + content + `{"type":"text","text":"Done."}]},` +
		`{"role":"user","content":"next"}]}`)
}

func testGinContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	return c
}

func TestThinkingStripPolicyDecisions(t *testing.T) {
	enabled := ClaudeThinkingStripSettings{Enabled: true}
	tests := []struct {
		name         string
		cfg          ClaudeThinkingStripSettings
		account      *Account
		session      bool
		roll         int
		setup        func(*fakeThinkingSignatureCache, string)
		body         []byte
		mode         ThinkingStripMode
		wantReason   ThinkingStripReason
		wantStripped bool
		wantState    string
	}{
		{name: "disabled keeps everything", cfg: ClaudeThinkingStripSettings{}, account: oauthAccount(1), session: true,
			body: thinkingStripBody("unknown"), wantState: ""},
		{name: "api key account out of scope", cfg: enabled, account: &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, session: true,
			body: thinkingStripBody("unknown"), wantState: ""},
		{name: "owned signatures kept", cfg: enabled, account: oauthAccount(1), session: true,
			setup: func(f *fakeThinkingSignatureCache, _ string) { f.owners[xxhash.Sum64String("a")] = 1 },
			body:  thinkingStripBody("a"), wantState: ""},
		{name: "unknown signature strips and poisons session", cfg: enabled, account: oauthAccount(1), session: true,
			body: thinkingStripBody("a"), wantReason: ThinkingStripReasonUnknown, wantStripped: true, wantState: thinkingSessionStateStripInvalid},
		{name: "foreign signature strips and poisons session", cfg: enabled, account: oauthAccount(1), session: true,
			setup: func(f *fakeThinkingSignatureCache, _ string) {
				f.owners[xxhash.Sum64String("a")] = 1
				f.owners[xxhash.Sum64String("b")] = 2
			},
			body: thinkingStripBody("a", "b"), wantReason: ThinkingStripReasonForeign, wantStripped: true, wantState: thinkingSessionStateStripInvalid},
		{name: "missing signature counts as unknown", cfg: enabled, account: oauthAccount(1), session: true,
			body: thinkingStripBody(""), wantReason: ThinkingStripReasonUnknown, wantStripped: true, wantState: thinkingSessionStateStripInvalid},
		{name: "poisoned session strips owned signatures", cfg: enabled, account: oauthAccount(1), session: true,
			setup: func(f *fakeThinkingSignatureCache, key string) {
				f.owners[xxhash.Sum64String("a")] = 1
				f.sessions[key] = thinkingSessionStateStripRandom
			},
			body: thinkingStripBody("a"), wantReason: ThinkingStripReasonSession, wantStripped: true, wantState: thinkingSessionStateStripRandom},
		{name: "keep session turns invalid later", cfg: enabled, account: oauthAccount(1), session: true,
			setup: func(f *fakeThinkingSignatureCache, key string) { f.sessions[key] = thinkingSessionStateKeep },
			body:  thinkingStripBody("a"), wantReason: ThinkingStripReasonUnknown, wantStripped: true, wantState: thinkingSessionStateStripInvalid},
		{name: "100 percent draw strips owned", cfg: ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 100}, account: oauthAccount(1), session: true, roll: 99,
			setup: func(f *fakeThinkingSignatureCache, _ string) { f.owners[xxhash.Sum64String("a")] = 1 },
			body:  thinkingStripBody("a"), wantReason: ThinkingStripReasonRandom, wantStripped: true, wantState: thinkingSessionStateStripRandom},
		{name: "missed draw records keep", cfg: ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 30}, account: oauthAccount(1), session: true, roll: 30,
			setup: func(f *fakeThinkingSignatureCache, _ string) { f.owners[xxhash.Sum64String("a")] = 1 },
			body:  thinkingStripBody("a"), wantState: thinkingSessionStateKeep},
		{name: "already drawn session is not redrawn", cfg: ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 100}, account: oauthAccount(1), session: true, roll: 0,
			setup: func(f *fakeThinkingSignatureCache, key string) {
				f.owners[xxhash.Sum64String("a")] = 1
				f.sessions[key] = thinkingSessionStateKeep
			},
			body: thinkingStripBody("a"), wantState: thinkingSessionStateKeep},
		{name: "no session: invalid strips without state", cfg: enabled, account: oauthAccount(1), session: false,
			body: thinkingStripBody("a"), wantReason: ThinkingStripReasonUnknown, wantStripped: true},
		{name: "no session: never drawn", cfg: ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 100}, account: oauthAccount(1), session: false,
			setup: func(f *fakeThinkingSignatureCache, _ string) { f.owners[xxhash.Sum64String("a")] = 1 },
			body:  thinkingStripBody("a")},
		{name: "count_tokens strips invalid without poisoning", cfg: enabled, account: oauthAccount(1), session: true, mode: ThinkingStripModeCountTokens,
			body: thinkingStripBody("a"), wantReason: ThinkingStripReasonUnknown, wantStripped: true, wantState: ""},
		{name: "count_tokens never draws", cfg: ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 100}, account: oauthAccount(1), session: true, mode: ThinkingStripModeCountTokens,
			setup: func(f *fakeThinkingSignatureCache, _ string) { f.owners[xxhash.Sum64String("a")] = 1 },
			body:  thinkingStripBody("a"), wantState: ""},
		{name: "setup token account in scope", cfg: enabled, account: &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeSetupToken}, session: true,
			body: thinkingStripBody("a"), wantReason: ThinkingStripReasonUnknown, wantStripped: true, wantState: thinkingSessionStateStripInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, cache := newThinkingStripPolicyForTest(t, tt.cfg, tt.roll)
			parsed := thinkingStripParsed(tt.session)
			key := thinkingStripSessionKey(nil, thinkingStripParsed(true))
			if tt.setup != nil {
				tt.setup(cache, key)
			}
			out, decision := policy.Apply(context.Background(), testGinContext(), tt.account, parsed, tt.body, tt.mode)
			require.Equal(t, tt.wantStripped, decision.Stripped)
			require.Equal(t, tt.wantReason, decision.Reason)
			require.Equal(t, tt.wantStripped, !gjson.GetBytes(out, `messages.1.content.#(type=="thinking")`).Exists())
			require.Equal(t, tt.wantState, cache.sessions[key])
		})
	}
}

func TestThinkingStripPolicyRedisErrorStripsWithoutPoisoning(t *testing.T) {
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{Enabled: true}, 0)
	cache.verifyErr = errors.New("redis down")
	key := thinkingStripSessionKey(nil, thinkingStripParsed(true))

	_, decision := policy.Apply(context.Background(), testGinContext(), oauthAccount(1), thinkingStripParsed(true), thinkingStripBody("a"), ThinkingStripModeMessages)
	require.True(t, decision.Stripped)
	require.Equal(t, ThinkingStripReasonRedisError, decision.Reason)
	require.Empty(t, cache.sessions[key])

	cache.verifyErr = nil
	cache.stateErr = errors.New("redis down")
	_, decision = policy.Apply(context.Background(), testGinContext(), oauthAccount(1), thinkingStripParsed(true), thinkingStripBody("a"), ThinkingStripModeMessages)
	require.Equal(t, ThinkingStripReasonRedisError, decision.Reason)
}

func TestThinkingStripPolicyLogsStructuredEvents(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core).With(zap.String("request_id", "req-1")))
	policy, _ := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{Enabled: true}, 0)

	_, decision := policy.Apply(ctx, testGinContext(), oauthAccount(1), thinkingStripParsed(true), thinkingStripBody("a"), ThinkingStripModeMessages)
	require.True(t, decision.Stripped)

	stripped := logs.FilterMessage("thinking_strip.stripped").All()
	require.Len(t, stripped, 1)
	fields := stripped[0].ContextMap()
	require.Equal(t, "req-1", fields["request_id"])
	require.Equal(t, "unknown", fields["reason"])
	require.Equal(t, "messages", fields["mode"])
	require.Equal(t, int64(1), fields["account_id"])
	require.Equal(t, int64(1), fields["blocks"])
	require.Equal(t, true, fields["first_invalid"])
	require.Equal(t, true, fields[logger.OpsSystemLogIndexField])
	require.NotEmpty(t, fields["session_key"])
}

// 只有会话第一次因签名不合法被删时才写入面板，同一会话后续删除不再写。
func TestThinkingStripPolicyIndexesOnlyFirstInvalidPerSession(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core))
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{Enabled: true}, 0)

	for i := 0; i < 3; i++ {
		_, decision := policy.Apply(ctx, testGinContext(), oauthAccount(1), thinkingStripParsed(true), thinkingStripBody("a"), ThinkingStripModeMessages)
		require.True(t, decision.Stripped)
	}
	// 并发请求在读到 keep 后各自判定不合法：只有真正改变状态的那次写入面板。
	key := thinkingStripSessionKey(nil, thinkingStripParsed(true))
	changed, err := cache.SetSessionState(context.Background(), key, thinkingSessionStateStripInvalid)
	require.NoError(t, err)
	require.False(t, changed)

	indexed := 0
	for _, entry := range logs.FilterMessage("thinking_strip.stripped").All() {
		if entry.ContextMap()[logger.OpsSystemLogIndexField] == true {
			indexed++
		}
	}
	require.Equal(t, 1, indexed)
	require.Len(t, logs.FilterMessage("thinking_strip.stripped").All(), 3)
}

func TestThinkingStripPolicyDoesNotIndexRandomOrCountTokens(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core))

	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 100}, 0)
	cache.owners[xxhash.Sum64String("a")] = 1
	_, decision := policy.Apply(ctx, testGinContext(), oauthAccount(1), thinkingStripParsed(true), thinkingStripBody("a"), ThinkingStripModeMessages)
	require.Equal(t, ThinkingStripReasonRandom, decision.Reason)

	countPolicy, _ := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{Enabled: true}, 0)
	_, decision = countPolicy.Apply(ctx, testGinContext(), oauthAccount(1), thinkingStripParsed(true), thinkingStripBody("b"), ThinkingStripModeCountTokens)
	require.Equal(t, ThinkingStripReasonUnknown, decision.Reason)

	for _, entry := range logs.All() {
		require.NotContains(t, entry.ContextMap(), logger.OpsSystemLogIndexField, entry.Message)
	}
}

func TestThinkingStripPolicyLogsSessionDraw(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core))
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 40}, 10)
	cache.owners[xxhash.Sum64String("a")] = 1

	_, decision := policy.Apply(ctx, testGinContext(), oauthAccount(1), thinkingStripParsed(true), thinkingStripBody("a"), ThinkingStripModeMessages)
	require.Equal(t, ThinkingStripReasonRandom, decision.Reason)

	drawn := logs.FilterMessage("thinking_strip.session_drawn").All()
	require.Len(t, drawn, 1)
	require.Equal(t, thinkingSessionStateStripRandom, drawn[0].ContextMap()["state"])
	require.Equal(t, int64(40), drawn[0].ContextMap()["force_strip_percent"])
}

func TestThinkingStripPolicySkipsRequestsWithoutThinking(t *testing.T) {
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{Enabled: true, ForceStripPercent: 100}, 0)
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out, decision := policy.Apply(context.Background(), testGinContext(), oauthAccount(1), thinkingStripParsed(true), body, ThinkingStripModeMessages)
	require.Equal(t, body, out)
	require.False(t, decision.Stripped)
	require.Zero(t, cache.verifies)
	require.Zero(t, cache.nxWrites)
}

func TestThinkingStripSessionKeyFallsBackToHeaderAndIsolatesAPIKeys(t *testing.T) {
	c := testGinContext()
	c.Request.Header.Set("X-Claude-Code-Session-Id", thinkingStripTestSession)
	fromHeader := thinkingStripSessionKey(c, thinkingStripParsed(false))
	fromMetadata := thinkingStripSessionKey(nil, thinkingStripParsed(true))
	require.NotEmpty(t, fromHeader)
	require.Equal(t, fromMetadata, fromHeader)

	other := thinkingStripParsed(true)
	other.SessionContext = &SessionContext{APIKeyID: 10}
	require.NotEqual(t, fromMetadata, thinkingStripSessionKey(nil, other))

	require.Empty(t, thinkingStripSessionKey(testGinContext(), thinkingStripParsed(false)))
}

func TestThinkingStripPolicyRegisterIssued(t *testing.T) {
	policy, cache := newThinkingStripPolicyForTest(t, ClaudeThinkingStripSettings{}, 0)
	resp := []byte(`{"content":[{"type":"thinking","thinking":"","signature":"sig1"},{"type":"redacted_thinking","data":"red1"},{"type":"text","text":"ok"}]}`)

	policy.RegisterIssuedFromResponse(context.Background(), oauthAccount(3), resp)
	require.Equal(t, int64(3), cache.owners[xxhash.Sum64String("sig1")])
	require.Equal(t, int64(3), cache.owners[xxhash.Sum64String("red1")])

	policy.RegisterIssued(context.Background(), &Account{ID: 4, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, "sig2")
	_, ok := cache.owners[xxhash.Sum64String("sig2")]
	require.False(t, ok, "out-of-scope accounts must not be registered")
}

func TestStripAllThinkingBlocks(t *testing.T) {
	body := []byte(`{"model":"m","messages":[` +
		`{"role":"user","content":[{"type":"text","text":"q"}]},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"t1","signature":"s1"},{"type":"tool_use","id":"tu1","name":"Read","input":{}},{"type":"thinking","thinking":"t2","signature":"s2"},{"type":"text","text":"a"}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"r"}]},` +
		`{"role":"assistant","content":[{"type":"redacted_thinking","data":"d"}]},` +
		`{"role":"assistant","content":"plain string"}],"thinking":{"type":"adaptive"}}`)

	out, removed := StripAllThinkingBlocks(body)
	require.Equal(t, 3, removed)
	require.True(t, gjson.ValidBytes(out))
	require.Equal(t, `[{"type":"tool_use","id":"tu1","name":"Read","input":{}},{"type":"text","text":"a"}]`,
		gjson.GetBytes(out, "messages.1.content").Raw)
	require.Equal(t, thinkingStripPlaceholder, gjson.GetBytes(out, "messages.3.content").Raw)
	require.Equal(t, `"plain string"`, gjson.GetBytes(out, "messages.4.content").Raw)
	require.Equal(t, gjson.GetBytes(body, "messages.0").Raw, gjson.GetBytes(out, "messages.0").Raw)
	require.Equal(t, `{"type":"adaptive"}`, gjson.GetBytes(out, "thinking").Raw)

	again, removedAgain := StripAllThinkingBlocks(out)
	require.Zero(t, removedAgain)
	require.Equal(t, out, again)
}

func TestStripAllThinkingBlocksIsDeterministic(t *testing.T) {
	body := thinkingStripBody("a", "b")
	first, _ := StripAllThinkingBlocks(body)
	second, _ := StripAllThinkingBlocks(body)
	require.Equal(t, first, second)
}

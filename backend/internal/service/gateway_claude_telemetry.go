package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/claudetelemetry"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const claudeTelemetryAttemptContextKey = "claude_telemetry_attempt"

type claudeTelemetryRouteContextKey struct{}

// WithClaudeTelemetryRouteEligible marks the native Anthropic messages path.
// Account eligibility is checked separately after scheduling; keeping this
// marker explicit prevents OpenAI/composite conversion paths from producing
// Claude telemetry merely because they happen to select an Anthropic account.
func WithClaudeTelemetryRouteEligible(ctx context.Context, eligible bool) context.Context {
	return context.WithValue(ctx, claudeTelemetryRouteContextKey{}, eligible)
}

func claudeTelemetryRouteEligible(ctx context.Context) bool {
	eligible, _ := ctx.Value(claudeTelemetryRouteContextKey{}).(bool)
	return eligible
}

type claudeTelemetryHTTPDoer struct {
	upstream    HTTPUpstream
	proxyURL    string
	accountID   int64
	concurrency int
	profile     *tlsfingerprint.Profile
}

func (d *claudeTelemetryHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return d.upstream.DoWithTLS(request, d.proxyURL, d.accountID, d.concurrency, d.profile)
}

func (s *GatewayService) beginClaudeTelemetryAttempt(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	wireBody []byte,
	wireRequest *http.Request,
	token string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	stream bool,
	isClaudeCode bool,
) *ClaudeTelemetryAttempt {
	if s == nil || s.claudeTelemetry == nil || !s.claudeTelemetry.Eligible(account) ||
		wireRequest == nil || s.httpUpstream == nil {
		return nil
	}
	if proxyURL == "" && account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	metadataUserID := gjson.GetBytes(wireBody, "metadata.user_id").String()
	accountUUID := telemetryFirstNonEmpty(
		account.GetExtraString("account_uuid"),
		account.GetExtraString("anthropic_account_uuid"),
		account.GetCredential("account_uuid"),
		account.GetCredential("anthropic_account_uuid"),
	)
	organizationUUID := telemetryFirstNonEmpty(
		account.GetExtraString("organization_uuid"),
		account.GetExtraString("org_uuid"),
		account.GetCredential("organization_uuid"),
		account.GetCredential("org_uuid"),
	)
	hmacSeed := sha256.Sum256([]byte("sub2api:claude-telemetry:" + token))
	identity, sources := claudetelemetry.ResolveIdentity(claudetelemetry.IdentityInput{
		MetadataUserID:   metadataUserID,
		AccountUUID:      accountUUID,
		OrganizationUUID: organizationUUID,
		SessionIDHeader:  wireRequest.Header.Get("X-Claude-Code-Session-Id"),
		AccountFallback:  strconv.FormatInt(account.ID, 10),
		DeviceFallback:   strconv.FormatInt(account.ID, 10) + ":" + metadataUserID,
		SessionFallback:  strconv.FormatInt(account.ID, 10) + ":" + metadataUserID,
		HMACSeed:         hmacSeed[:],
	})
	if parsed := ParseMetadataUserID(metadataUserID); parsed != nil {
		identity.DeviceID = parsed.DeviceID
		identity.SessionID = parsed.SessionID
		sources["device_id"] = claudetelemetry.Observed
		sources["session_id"] = claudetelemetry.Observed
	}

	headers := wireRequest.Header
	version := ExtractCLIVersion(headers.Get("User-Agent"))
	if version == "" {
		version = ClaudeTelemetryProfile
	}
	environment := claudetelemetry.ClientEnvironment{
		Platform:    headers.Get("x-stainless-os"),
		PlatformRaw: headers.Get("x-stainless-os"),
		Arch:        headers.Get("x-stainless-arch"),
		NodeVersion: headers.Get("x-stainless-runtime-version"),
		Runtime:     headers.Get("x-stainless-runtime"),
		Terminal:    headers.Get("x-claude-code-terminal"),
		Shell:       headers.Get("x-claude-code-shell"),
		Version:     version,
		VersionBase: version,
	}
	route := claudetelemetry.DeliveryRoute{
		Key:   fmt.Sprintf("anthropic:%d:%s", account.ID, proxyURL),
		Token: token,
		HTTPDoer: &claudeTelemetryHTTPDoer{
			upstream: s.httpUpstream, proxyURL: proxyURL, accountID: account.ID,
			concurrency: account.Concurrency, profile: tlsProfile,
		},
	}
	mode := claudetelemetry.ObserveJSON
	if stream {
		mode = claudetelemetry.ObserveSSE
	}
	retryCount, _ := strconv.Atoi(strings.TrimSpace(headers.Get("x-stainless-retry-count")))
	if retryCount < 0 {
		retryCount = 0
	}
	attempt, err := s.claudeTelemetry.Begin(ctx, claudetelemetry.BeginInput{
		EventContext: claudetelemetry.EventContext{
			Identity: identity, IdentitySources: sources,
			Model:       gjson.GetBytes(wireBody, "model").String(),
			Betas:       headers.Get("anthropic-beta"),
			Environment: environment,
			SubscriptionType: telemetryFirstNonEmpty(
				account.GetExtraString("subscription_type"),
				account.GetCredential("subscription_type"),
				account.GetCredential("plan_type"),
			),
			Provider: "anthropic", Route: route,
		},
		RequestBody: wireBody, IsClaudeCode: isClaudeCode,
		RetryCount: retryCount, ObserverMode: mode,
	})
	if err != nil {
		return nil
	}
	if c != nil {
		c.Set(claudeTelemetryAttemptContextKey, attempt)
	}
	return attempt
}

func observeClaudeTelemetrySSEJSON(c *gin.Context, raw []byte) {
	if c == nil {
		return
	}
	if value, ok := c.Get(claudeTelemetryAttemptContextKey); ok {
		if attempt, ok := value.(*ClaudeTelemetryAttempt); ok {
			attempt.ObserveEventJSON(raw)
		}
	}
}

func observeClaudeTelemetryJSON(c *gin.Context, raw []byte) {
	if c == nil {
		return
	}
	if value, ok := c.Get(claudeTelemetryAttemptContextKey); ok {
		if attempt, ok := value.(*ClaudeTelemetryAttempt); ok {
			attempt.ObserveJSON(raw)
		}
	}
}

func finishClaudeTelemetryAttempt(ctx context.Context, attempt *ClaudeTelemetryAttempt, requestID string) {
	if attempt == nil {
		return
	}
	attempt.Complete(context.WithoutCancel(ctx), requestID)
}

func (e *UpstreamFailoverError) attachClaudeTelemetryAttempt(attempt *ClaudeTelemetryAttempt, requestID string) {
	if e == nil || attempt == nil {
		return
	}
	e.claudeTelemetryAttempt = attempt
	e.claudeTelemetryRequestID = requestID
}

// AbortClaudeTelemetry discards a failed internal attempt before another
// account is selected.
func (e *UpstreamFailoverError) AbortClaudeTelemetry() {
	if e == nil || e.claudeTelemetryAttempt == nil {
		return
	}
	e.claudeTelemetryAttempt.Abort()
	e.claudeTelemetryAttempt = nil
}

// CompleteClaudeTelemetry preserves request-side events when failover is
// terminal (pool exhausted, response already started, or policy stops retry).
func (e *UpstreamFailoverError) CompleteClaudeTelemetry(ctx context.Context) {
	if e == nil || e.claudeTelemetryAttempt == nil {
		return
	}
	e.claudeTelemetryAttempt.Complete(context.WithoutCancel(ctx), e.claudeTelemetryRequestID)
	e.claudeTelemetryAttempt = nil
}

func telemetryFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

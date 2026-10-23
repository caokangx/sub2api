package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// One cache entry per selected account avoids deriving an already rewritten
// session again when a caller rebuilds a request after an upstream retry.
type oauthRequestIdentity struct{ userID, sessionID string }

const oauthRequestIdentitiesKey = "anthropicOAuthRequestIdentities"
const oauthRequestIdentitySourceKey = "anthropicOAuthRequestIdentitySource"

func claudeAccountUUID(account *Account) string {
	if value := strings.TrimSpace(account.GetExtraString("account_uuid")); value != "" {
		return value
	}
	return strings.TrimSpace(account.GetCredential("account_uuid"))
}

func (s *GatewayService) rewriteAnthropicOAuthIdentity(ctx context.Context, c *gin.Context, account *Account, body []byte, fp *Fingerprint, countTokens bool) ([]byte, string) {
	if account == nil || account.Platform != PlatformAnthropic || !account.IsOAuth() {
		return body, ""
	}
	if !forceClaudeCodeProfile(ctx, account) {
		uid := gjson.GetBytes(body, "metadata.user_id").String()
		if parsed := ParseMetadataUserID(uid); parsed != nil {
			return body, parsed.SessionID
		}
		return body, ""
	}
	var identities map[int64]oauthRequestIdentity
	if c != nil {
		if cached, ok := c.Get(oauthRequestIdentitiesKey); ok {
			identities, _ = cached.(map[int64]oauthRequestIdentity)
		}
	}
	metadata := gjson.GetBytes(body, "metadata")
	hasMetadata := metadata.IsObject()
	if cached, ok := identities[account.ID]; ok {
		if !countTokens || hasMetadata {
			body, _ = sjson.SetBytes(body, "metadata.user_id", cached.userID)
		}
		return body, cached.sessionID
	}
	raw := metadata.Get("user_id").String()
	if c != nil {
		if source, ok := c.Get(oauthRequestIdentitySourceKey); ok {
			raw, _ = source.(string)
		} else {
			c.Set(oauthRequestIdentitySourceKey, raw)
		}
	}
	parsed := ParseMetadataUserID(raw)
	device := strings.TrimSpace(account.GetClaudeUserID())
	if device == "" && fp != nil {
		device = fp.ClientID
	}
	if device == "" {
		device = generateSessionDeviceID(account.ID)
	}
	session := ""
	if parsed != nil {
		session = generateUUIDFromSeed(strconv.FormatInt(account.ID, 10) + "::" + parsed.SessionID)
	}
	if session == "" && c != nil && c.Request != nil {
		if source := getHeaderRaw(c.Request.Header, "X-Claude-Code-Session-Id"); source != "" {
			session = generateUUIDFromSeed(strconv.FormatInt(account.ID, 10) + "::" + source)
		}
	}
	if session == "" {
		session = generateSessionUUID(buildStableSessionSeed(account.ID, device, extractFirstUserText(body)))
	}
	uid := FormatMetadataUserID(device, claudeAccountUUID(account), session, claude.CLICurrentVersion)
	// Reuse the account's established masking policy, but never derive a
	// second session from the intermediate identity.
	if account.IsSessionIDMaskingEnabled() && s.identityService != nil && s.identityService.cache != nil {
		maskedBody, _ := sjson.SetBytes(body, "metadata.user_id", uid)
		if rewritten, err := s.identityService.RewriteUserIDWithMasking(ctx, maskedBody, account, claudeAccountUUID(account), device, claude.DefaultUserAgent()); err == nil {
			if masked := ParseMetadataUserID(gjson.GetBytes(rewritten, "metadata.user_id").String()); masked != nil {
				session = masked.SessionID
			}
		}
	}
	fields := map[string]any{"device_id": device, "account_uuid": claudeAccountUUID(account), "session_id": session}
	if parsed != nil && parsed.IsNewFormat {
		var extra map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &extra) == nil {
			for _, key := range []string{"parent_session_id", "ti", "tk"} {
				var value string
				if json.Unmarshal(extra[key], &value) == nil && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00") {
					fields[key] = value
				}
			}
		}
	}
	encoded, _ := json.Marshal(fields)
	uid = string(encoded)
	if c != nil {
		if identities == nil {
			identities = make(map[int64]oauthRequestIdentity)
		}
		identities[account.ID] = oauthRequestIdentity{uid, session}
		c.Set(oauthRequestIdentitiesKey, identities)
	}
	if !countTokens || hasMetadata {
		body, _ = sjson.SetBytes(body, "metadata.user_id", uid)
	}
	return body, session
}

func generateSessionDeviceID(accountID int64) string {
	// Stable account-scoped fallback while the persistent fingerprint cache is
	// unavailable; normal requests use its existing 64-character device ID.
	return fmt.Sprintf("%x", sha256.Sum256([]byte("anthropic-device:"+strconv.FormatInt(accountID, 10))))
}

func syncAnthropicOAuthSessionHeader(headers http.Header, sessionID string) {
	if sessionID != "" {
		setHeaderRaw(headers, "X-Claude-Code-Session-Id", sessionID)
	}
}

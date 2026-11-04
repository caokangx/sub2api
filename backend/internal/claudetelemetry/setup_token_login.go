package claudetelemetry

import (
	"fmt"
	"net/http"
	"time"
)

// SetupTokenLogin is one manual-paste `claude setup-token` authorization: the
// pasted code, the token exchange, then the profile lookup the CLI always runs.
type SetupTokenLogin struct {
	CodeReceivedAt   time.Time
	ExchangeFailed   bool
	ExchangedAt      time.Time
	ProfileFetchedAt time.Time
	// ProfileStatus is 0 when the lookup never received a response.
	ProfileStatus   int
	ProfileTimedOut bool
}

// SetupTokenLoginEvents reproduces the events Claude Code 2.1.284 emits along
// that path. The CLI keeps the token only to print it, so the process stays
// logged out and events carry no auth block.
func SetupTokenLoginEvents(context EventContext, login SetupTokenLogin) ([]BuiltEvent, error) {
	type timedDraft struct {
		at time.Time
		eventDraft
	}
	drafts := []timedDraft{
		{login.CodeReceivedAt, draft("tengu_oauth_manual_entry", map[string]any{}, nil)},
		{login.CodeReceivedAt, draft("tengu_oauth_auth_code_received", map[string]any{"automatic": false}, inferredFields("automatic"))},
	}
	if login.ExchangeFailed {
		drafts = append(drafts, timedDraft{login.CodeReceivedAt, featureDraft("tengu_feature_bad", "oauth_login", "oauth_login_failed")})
	} else {
		drafts = append(drafts,
			timedDraft{login.ExchangedAt, draft("tengu_oauth_token_exchange_success", map[string]any{}, nil)},
			timedDraft{login.ExchangedAt, featureDraft("tengu_feature_ok", "oauth_token_exchange", "")},
		)
		if name, code := profileFetchFailure(login.ProfileStatus, login.ProfileTimedOut); name != "" {
			drafts = append(drafts, timedDraft{login.ProfileFetchedAt, featureDraft(name, "oauth_profile_fetch", code)})
		}
		drafts = append(drafts,
			timedDraft{login.ProfileFetchedAt, draft("tengu_oauth_profile_fetch_success", map[string]any{}, nil)},
			timedDraft{login.ProfileFetchedAt, featureDraft("tengu_feature_ok", "oauth_login", "")},
		)
	}
	context.OmitAuth = true
	built := make([]BuiltEvent, 0, len(drafts))
	for _, item := range drafts {
		event, err := buildEvent(item.at, context, item.name, item.metadata, item.provenance)
		if err != nil {
			return nil, err
		}
		built = append(built, event)
	}
	return built, nil
}

// profileFetchFailure mirrors the CLI's profile error classification: auth,
// rate-limit and transport failures are "sad", other HTTP statuses are "bad".
// The CLI then still reports tengu_oauth_profile_fetch_success.
func profileFetchFailure(status int, timedOut bool) (eventName, errorCode string) {
	switch {
	case status >= 200 && status < 300:
		return "", ""
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "tengu_feature_sad", "token_no_auth"
	case status == http.StatusTooManyRequests:
		return "tengu_feature_sad", "token_rate_limited"
	case status == 0 && timedOut:
		return "tengu_feature_sad", "token_timeout"
	case status == 0:
		return "tengu_feature_sad", "token_network_error"
	default:
		return "tengu_feature_bad", fmt.Sprintf("token_http_%d", status)
	}
}

func featureDraft(name, feature, errorCode string) eventDraft {
	metadata := map[string]any{"feature_name": feature}
	if errorCode != "" {
		metadata["error_code"] = errorCode
	}
	return draft(name, metadata, inferredFields("feature_name", "error_code"))
}

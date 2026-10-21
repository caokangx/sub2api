package claudetelemetry

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

type Provenance string

const (
	Observed    Provenance = "observed"
	Derived     Provenance = "derived"
	Inferred    Provenance = "inferred"
	Unsupported Provenance = "unsupported"
)

type FieldProvenance map[string]Provenance

// ProvenanceCapabilities describes the package's evidence boundary. It is
// suitable for a read-only management status view.
func ProvenanceCapabilities() FieldProvenance {
	return FieldProvenance{
		"request_shape":       Observed,
		"response_shape":      Observed,
		"usage":               Observed,
		"latency":             Derived,
		"content_lengths":     Derived,
		"content_hashes":      Derived,
		"query_chain":         Inferred,
		"prompt_index":        Inferred,
		"turn_start":          Inferred,
		"process":             Unsupported,
		"ui":                  Unsupported,
		"plugin_lifecycle":    Unsupported,
		"mcp_lifecycle":       Unsupported,
		"startup_local_state": Unsupported,
	}
}

type Identity struct {
	AccountUUID      string
	OrganizationUUID string
	DeviceID         string
	SessionID        string
}

type IdentityInput struct {
	MetadataUserID   string
	AccountUUID      string
	OrganizationUUID string
	SessionIDHeader  string

	// Stable fallback material should contain opaque internal IDs, never raw
	// credentials. It is HMACed and is not included in telemetry.
	AccountFallback string
	DeviceFallback  string
	SessionFallback string
	HMACSeed        []byte
}

type identityMetadata struct {
	AccountUUID string `json:"account_uuid"`
	DeviceID    string `json:"device_id"`
	SessionID   string `json:"session_id"`
}

func ResolveIdentity(in IdentityInput) (Identity, FieldProvenance) {
	var embedded identityMetadata
	_ = json.Unmarshal([]byte(in.MetadataUserID), &embedded)
	provenance := FieldProvenance{}
	identity := Identity{}

	if in.AccountUUID != "" {
		identity.AccountUUID = in.AccountUUID
		provenance["account_uuid"] = Observed
	} else if embedded.AccountUUID != "" {
		identity.AccountUUID = embedded.AccountUUID
		provenance["account_uuid"] = Observed
	} else {
		identity.AccountUUID = stableID(in.HMACSeed, "account", in.AccountFallback, true)
		provenance["account_uuid"] = Derived
	}
	if in.OrganizationUUID != "" {
		identity.OrganizationUUID = in.OrganizationUUID
		provenance["organization_uuid"] = Observed
	} else {
		identity.OrganizationUUID = stableID(in.HMACSeed, "organization", in.AccountFallback, true)
		provenance["organization_uuid"] = Derived
	}
	if embedded.DeviceID != "" {
		identity.DeviceID = embedded.DeviceID
		provenance["device_id"] = Observed
	} else {
		identity.DeviceID = stableID(in.HMACSeed, "device", in.DeviceFallback, false)
		provenance["device_id"] = Derived
	}
	switch {
	case embedded.SessionID != "":
		identity.SessionID = embedded.SessionID
		provenance["session_id"] = Observed
	case in.SessionIDHeader != "":
		identity.SessionID = in.SessionIDHeader
		provenance["session_id"] = Observed
	default:
		identity.SessionID = stableID(in.HMACSeed, "session", in.SessionFallback, true)
		provenance["session_id"] = Derived
	}
	return identity, provenance
}

func stableID(seed []byte, kind, value string, uuid bool) string {
	if len(seed) == 0 {
		seed = []byte("sub2api-claude-telemetry-missing-seed")
	}
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte(kind + "\x00" + value))
	sum := mac.Sum(nil)
	if !uuid {
		return hex.EncodeToString(sum)
	}
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	text := hex.EncodeToString(sum[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", text[:8], text[8:12], text[12:16], text[16:20], text[20:])
}

type ClientEnvironment struct {
	Platform              string `json:"platform,omitempty"`
	PlatformRaw           string `json:"platform_raw,omitempty"`
	Arch                  string `json:"arch,omitempty"`
	NodeVersion           string `json:"node_version,omitempty"`
	Runtime               string `json:"runtimes,omitempty"`
	Terminal              string `json:"terminal,omitempty"`
	Shell                 string `json:"shell,omitempty"`
	Version               string `json:"version"`
	VersionBase           string `json:"version_base"`
	BuildTime             string `json:"build_time,omitempty"`
	DeploymentEnvironment string `json:"deployment_environment,omitempty"`
	IsCI                  *bool  `json:"is_ci,omitempty"`
}

type EventContext struct {
	Identity         Identity
	IdentitySources  FieldProvenance
	Model            string
	Betas            string
	Environment      ClientEnvironment
	SubscriptionType string
	RendererMode     string
	Interactive      *bool
	Provider         string
	Route            DeliveryRoute
}

type ClaudeCodeInternalEvent struct {
	EventType string            `json:"event_type"`
	EventData InternalEventData `json:"event_data"`
}

type InternalEventData struct {
	EventName          string            `json:"event_name"`
	ClientTimestamp    string            `json:"client_timestamp"`
	EventID            string            `json:"event_id"`
	Model              string            `json:"model"`
	SessionID          string            `json:"session_id,omitempty"`
	DeviceID           string            `json:"device_id,omitempty"`
	UserType           string            `json:"user_type"`
	Betas              string            `json:"betas,omitempty"`
	Environment        ClientEnvironment `json:"env"`
	Entrypoint         string            `json:"entrypoint"`
	IsInteractive      *bool             `json:"is_interactive,omitempty"`
	ClientType         string            `json:"client_type"`
	Auth               EventAuth         `json:"auth"`
	AdditionalMetadata string            `json:"additional_metadata"`
}

type EventAuth struct {
	AccountUUID      string `json:"account_uuid,omitempty"`
	OrganizationUUID string `json:"organization_uuid,omitempty"`
}

type BuiltEvent struct {
	FirstParty ClaudeCodeInternalEvent
	Datadog    map[string]any
	Name       string
	Metadata   map[string]any
	Provenance FieldProvenance
}

func buildEvent(now time.Time, context EventContext, name string, metadata map[string]any, provenance FieldProvenance) (BuiltEvent, error) {
	additional := compactMap(map[string]any{
		"subscription_type": context.SubscriptionType,
		"renderer_mode":     context.RendererMode,
	})
	for key, value := range metadata {
		if value != nil {
			additional[key] = value
		}
	}
	raw, err := json.Marshal(additional)
	if err != nil {
		return BuiltEvent{}, fmt.Errorf("marshal telemetry metadata: %w", err)
	}
	event := ClaudeCodeInternalEvent{
		EventType: "ClaudeCodeInternalEvent",
		EventData: InternalEventData{
			EventName:       name,
			ClientTimestamp: now.UTC().Format(time.RFC3339Nano),
			EventID:         randomUUID(),
			Model:           valueOr(context.Model, "unknown"),
			SessionID:       context.Identity.SessionID,
			DeviceID:        context.Identity.DeviceID,
			UserType:        "external",
			Betas:           context.Betas,
			Environment:     context.Environment,
			Entrypoint:      "cli",
			IsInteractive:   context.Interactive,
			ClientType:      "cli",
			Auth: EventAuth{
				AccountUUID:      context.Identity.AccountUUID,
				OrganizationUUID: context.Identity.OrganizationUUID,
			},
			AdditionalMetadata: base64.StdEncoding.EncodeToString(raw),
		},
	}
	return BuiltEvent{
		FirstParty: event,
		Datadog:    datadogLog(context, name, additional),
		Name:       name,
		Metadata:   additional,
		Provenance: cloneProvenance(provenance),
	}, nil
}

func randomUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		sum := sha256.Sum256([]byte(time.Now().String()))
		copy(raw[:], sum[:16])
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	text := hex.EncodeToString(raw[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", text[:8], text[8:12], text[12:16], text[16:20], text[20:])
}

func cloneProvenance(in FieldProvenance) FieldProvenance {
	if len(in) == 0 {
		return nil
	}
	out := make(FieldProvenance, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func compactMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		switch typed := value.(type) {
		case string:
			if typed != "" {
				out[key] = typed
			}
		case nil:
		default:
			out[key] = value
		}
	}
	return out
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

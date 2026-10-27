package claudetelemetry

import (
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
}

type identityMetadata struct {
	AccountUUID string `json:"account_uuid"`
	DeviceID    string `json:"device_id"`
	SessionID   string `json:"session_id"`
}

// ResolveIdentity never synthesizes identifiers: a field that neither the
// account nor the wire request provides stays empty and is omitted from events.
func ResolveIdentity(in IdentityInput) (Identity, FieldProvenance) {
	var embedded identityMetadata
	_ = json.Unmarshal([]byte(in.MetadataUserID), &embedded)
	identity := Identity{
		AccountUUID:      valueOr(in.AccountUUID, embedded.AccountUUID),
		OrganizationUUID: in.OrganizationUUID,
		DeviceID:         embedded.DeviceID,
		SessionID:        valueOr(embedded.SessionID, in.SessionIDHeader),
	}
	provenance := FieldProvenance{}
	for field, value := range map[string]string{
		"account_uuid":      identity.AccountUUID,
		"organization_uuid": identity.OrganizationUUID,
		"device_id":         identity.DeviceID,
		"session_id":        identity.SessionID,
	} {
		if value != "" {
			provenance[field] = Observed
		}
	}
	return identity, provenance
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

// NormalizePlatform maps Stainless OS names (for example "MacOS") to Node's
// process.platform values, which Claude Code reports as env.platform.
func NormalizePlatform(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "macos", "darwin":
		return "darwin"
	case "linux":
		return "linux"
	case "windows", "win32":
		return "win32"
	case "freebsd":
		return "freebsd"
	case "openbsd":
		return "openbsd"
	case "android":
		return "android"
	default:
		return ""
	}
}

var knownShells = map[string]struct{}{
	"zsh": {}, "bash": {}, "fish": {}, "sh": {}, "dash": {}, "ash": {}, "ksh": {},
	"tcsh": {}, "csh": {}, "nu": {}, "nushell": {}, "pwsh": {}, "powershell": {},
	"cmd": {}, "elvish": {}, "xonsh": {}, "ion": {},
}

// NormalizeShell reproduces Claude Code's shell bucketing: the executable
// basename when it is a known shell, otherwise "other".
func NormalizeShell(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.LastIndexAny(value, `/\`); index >= 0 {
		value = value[index+1:]
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".exe")
	if _, ok := knownShells[value]; ok {
		return value
	}
	return "other"
}

// buildAgeMinutes mirrors Claude Code's buildAgeMins: whole minutes since the
// compiled-in BUILD_TIME.
func buildAgeMinutes(buildTime string, now time.Time) any {
	built, err := time.Parse(time.RFC3339, buildTime)
	if err != nil {
		return nil
	}
	return int64(now.Sub(built) / time.Minute)
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

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ClaudeTelemetryProfile = claude.CLICurrentVersion

	ClaudeTelemetryModeOff     = "off"
	ClaudeTelemetryModeRecord  = "record"
	ClaudeTelemetryModeForward = "forward"
	ClaudeTelemetryModeBoth    = "both"

	defaultClaudeTelemetryRecordFile    = "./data/telemetry/telemetry-batches.ndjson"
	defaultClaudeTelemetryFirstPartyURL = "https://api.anthropic.com/api/event_logging/v2/batch"
	defaultClaudeTelemetryDatadogURL    = "https://http-intake.logs.us5.datadoghq.com/api/v2/logs"
)

// ClaudeTelemetryTargetConfig configures an outbound telemetry target.
type ClaudeTelemetryTargetConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// ClaudeTelemetryDatadogConfig contains the write-only Datadog credential.
// APIKeyConfigured is response-only. An empty APIKey preserves the stored key;
// ClearAPIKey must be set to explicitly remove it.
type ClaudeTelemetryDatadogConfig struct {
	Enabled          bool   `json:"enabled"`
	URL              string `json:"url"`
	APIKey           string `json:"api_key,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	ClearAPIKey      bool   `json:"clear_api_key,omitempty"`
}

type ClaudeTelemetryBatchConfig struct {
	DelayMS   int `json:"delay_ms"`
	Size      int `json:"size"`
	QueueSize int `json:"queue_size"`
}

type ClaudeTelemetryRetryConfig struct {
	MaxAttempts int `json:"max_attempts"`
	BaseDelayMS int `json:"base_delay_ms"`
	MaxDelayMS  int `json:"max_delay_ms"`
}

type ClaudeTelemetrySamplingConfig struct {
	DefaultRate float64            `json:"default_rate"`
	EventRates  map[string]float64 `json:"event_rates"`
}

// ClaudeTelemetryRuntimeConfig is the immutable, secret-bearing snapshot used
// by future gateway/exporter integration. It deliberately does not depend on
// the optional claudetelemetry core package.
type ClaudeTelemetryRuntimeConfig struct {
	Profile           string                        `json:"profile"`
	Mode              string                        `json:"mode"`
	SetupTokenEnabled bool                          `json:"setup_token_enabled"`
	RecordFile        string                        `json:"record_file"`
	FirstParty        ClaudeTelemetryTargetConfig   `json:"first_party"`
	Datadog           ClaudeTelemetryDatadogConfig  `json:"datadog"`
	Batch             ClaudeTelemetryBatchConfig    `json:"batch"`
	TimeoutMS         int                           `json:"timeout_ms"`
	Retry             ClaudeTelemetryRetryConfig    `json:"retry"`
	Sampling          ClaudeTelemetrySamplingConfig `json:"sampling"`
	FlushOnExit       bool                          `json:"flush_on_exit"`
}

type ClaudeTelemetryDeliveryStatus struct {
	Successes     uint64     `json:"successes"`
	Failures      uint64     `json:"failures"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastError     string     `json:"last_error"`
}

// ClaudeTelemetryRuntimeStatus is safe to expose to administrators. Until the
// exporter is wired, Available remains false and counters are placeholders.
type ClaudeTelemetryRuntimeStatus struct {
	Available     bool                          `json:"available"`
	Profile       string                        `json:"profile"`
	QueueLength   int                           `json:"queue_length"`
	QueueCapacity int                           `json:"queue_capacity"`
	DroppedEvents uint64                        `json:"dropped_events"`
	LastFlushAt   *time.Time                    `json:"last_flush_at"`
	FirstParty    ClaudeTelemetryDeliveryStatus `json:"first_party"`
	Datadog       ClaudeTelemetryDeliveryStatus `json:"datadog"`
}

type ClaudeTelemetrySettings struct {
	ClaudeTelemetryRuntimeConfig
	Runtime ClaudeTelemetryRuntimeStatus `json:"runtime"`
}

func DefaultClaudeTelemetryRuntimeConfig() *ClaudeTelemetryRuntimeConfig {
	return &ClaudeTelemetryRuntimeConfig{
		Profile:           ClaudeTelemetryProfile,
		Mode:              ClaudeTelemetryModeRecord,
		SetupTokenEnabled: false,
		RecordFile:        defaultClaudeTelemetryRecordFile,
		FirstParty: ClaudeTelemetryTargetConfig{
			Enabled: true,
			URL:     defaultClaudeTelemetryFirstPartyURL,
		},
		Datadog: ClaudeTelemetryDatadogConfig{
			Enabled: false,
			URL:     defaultClaudeTelemetryDatadogURL,
		},
		Batch: ClaudeTelemetryBatchConfig{
			DelayMS:   10_000,
			Size:      400,
			QueueSize: 8192,
		},
		TimeoutMS: 15_000,
		Retry: ClaudeTelemetryRetryConfig{
			MaxAttempts: 3,
			BaseDelayMS: 500,
			MaxDelayMS:  10_000,
		},
		Sampling: ClaudeTelemetrySamplingConfig{
			DefaultRate: 1,
			EventRates:  map[string]float64{},
		},
		FlushOnExit: false,
	}
}

func cloneClaudeTelemetryConfig(cfg *ClaudeTelemetryRuntimeConfig) *ClaudeTelemetryRuntimeConfig {
	if cfg == nil {
		return DefaultClaudeTelemetryRuntimeConfig()
	}
	cloned := *cfg
	cloned.Sampling.EventRates = make(map[string]float64, len(cfg.Sampling.EventRates))
	for event, rate := range cfg.Sampling.EventRates {
		cloned.Sampling.EventRates[event] = rate
	}
	return &cloned
}

func normalizeClaudeTelemetryConfig(cfg *ClaudeTelemetryRuntimeConfig) {
	cfg.Profile = ClaudeTelemetryProfile
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	cfg.RecordFile = strings.TrimSpace(cfg.RecordFile)
	cfg.FirstParty.URL = strings.TrimSpace(cfg.FirstParty.URL)
	cfg.Datadog.URL = strings.TrimSpace(cfg.Datadog.URL)
	cfg.Datadog.APIKey = strings.TrimSpace(cfg.Datadog.APIKey)
	if cfg.Sampling.EventRates == nil {
		cfg.Sampling.EventRates = map[string]float64{}
	}
}

func validateClaudeTelemetryURL(name, raw string, required bool) error {
	if raw == "" {
		if required {
			return fmt.Errorf("%s url is required when enabled", name)
		}
		return nil
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s url must be an absolute http(s) URL", name)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s url must not contain credentials", name)
	}
	host := parsed.Hostname()
	isLoopback := strings.EqualFold(host, "localhost")
	if address := net.ParseIP(host); address != nil {
		isLoopback = address.IsLoopback()
	}
	if required && parsed.Scheme != "https" && !isLoopback {
		return fmt.Errorf("%s url must use HTTPS unless it targets loopback", name)
	}
	return nil
}

func validateClaudeTelemetryConfig(cfg *ClaudeTelemetryRuntimeConfig) error {
	if cfg == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	switch cfg.Mode {
	case ClaudeTelemetryModeOff, ClaudeTelemetryModeRecord, ClaudeTelemetryModeForward, ClaudeTelemetryModeBoth:
	default:
		return fmt.Errorf("mode must be one of off, record, forward, both")
	}
	if len(cfg.RecordFile) > 2048 || strings.ContainsRune(cfg.RecordFile, '\x00') {
		return fmt.Errorf("record_file is invalid")
	}
	if cfg.RecordFile != "" {
		clean := filepath.Clean(cfg.RecordFile)
		root := filepath.Clean("data/telemetry")
		relative, err := filepath.Rel(root, clean)
		if err != nil || filepath.IsAbs(cfg.RecordFile) || relative == "." || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("record_file must be a file under data/telemetry")
		}
	}
	if (cfg.Mode == ClaudeTelemetryModeRecord || cfg.Mode == ClaudeTelemetryModeBoth) && cfg.RecordFile == "" {
		return fmt.Errorf("record_file is required in record or both mode")
	}
	if err := validateClaudeTelemetryURL("first_party", cfg.FirstParty.URL, cfg.FirstParty.Enabled); err != nil {
		return err
	}
	if err := validateClaudeTelemetryURL("datadog", cfg.Datadog.URL, cfg.Datadog.Enabled); err != nil {
		return err
	}
	if cfg.Datadog.Enabled && cfg.Datadog.APIKey == "" {
		return fmt.Errorf("datadog api key is required when Datadog is enabled")
	}
	if len(cfg.Datadog.APIKey) > 4096 || strings.ContainsAny(cfg.Datadog.APIKey, "\r\n") {
		return fmt.Errorf("datadog api key is invalid")
	}
	if cfg.Mode == ClaudeTelemetryModeForward || cfg.Mode == ClaudeTelemetryModeBoth {
		if !cfg.FirstParty.Enabled && !cfg.Datadog.Enabled {
			return fmt.Errorf("at least one forwarding target must be enabled in forward or both mode")
		}
	}
	if cfg.Batch.DelayMS < 100 || cfg.Batch.DelayMS > 3_600_000 {
		return fmt.Errorf("batch.delay_ms must be between 100 and 3600000")
	}
	if cfg.Batch.Size < 1 || cfg.Batch.Size > 1000 {
		return fmt.Errorf("batch.size must be between 1 and 1000")
	}
	if cfg.Batch.QueueSize < cfg.Batch.Size || cfg.Batch.QueueSize > 100_000 {
		return fmt.Errorf("batch.queue_size must be between batch.size and 100000")
	}
	if cfg.TimeoutMS < 100 || cfg.TimeoutMS > 120_000 {
		return fmt.Errorf("timeout_ms must be between 100 and 120000")
	}
	if cfg.Retry.MaxAttempts < 1 || cfg.Retry.MaxAttempts > 10 {
		return fmt.Errorf("retry.max_attempts must be between 1 and 10")
	}
	if cfg.Retry.BaseDelayMS < 1 || cfg.Retry.BaseDelayMS > 60_000 {
		return fmt.Errorf("retry.base_delay_ms must be between 1 and 60000")
	}
	if cfg.Retry.MaxDelayMS < cfg.Retry.BaseDelayMS || cfg.Retry.MaxDelayMS > 300_000 {
		return fmt.Errorf("retry.max_delay_ms must be between base_delay_ms and 300000")
	}
	if math.IsNaN(cfg.Sampling.DefaultRate) || math.IsInf(cfg.Sampling.DefaultRate, 0) ||
		cfg.Sampling.DefaultRate < 0 || cfg.Sampling.DefaultRate > 1 {
		return fmt.Errorf("sampling.default_rate must be between 0 and 1")
	}
	if len(cfg.Sampling.EventRates) > 200 {
		return fmt.Errorf("sampling.event_rates supports at most 200 events")
	}
	for event, rate := range cfg.Sampling.EventRates {
		if strings.TrimSpace(event) == "" || len(event) > 200 {
			return fmt.Errorf("sampling.event_rates contains an invalid event name")
		}
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
			return fmt.Errorf("sampling.event_rates[%q] must be between 0 and 1", event)
		}
	}
	return nil
}

func (s *SettingService) loadClaudeTelemetryConfig(ctx context.Context) (*ClaudeTelemetryRuntimeConfig, error) {
	cfg := DefaultClaudeTelemetryRuntimeConfig()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyClaudeTelemetrySettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return cfg, nil
		}
		return nil, fmt.Errorf("get Claude telemetry settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		slog.Warn("invalid Claude telemetry settings JSON; using safe defaults", "error", err)
		return DefaultClaudeTelemetryRuntimeConfig(), nil
	}
	normalizeClaudeTelemetryConfig(cfg)
	if err := validateClaudeTelemetryConfig(cfg); err != nil {
		slog.Warn("invalid persisted Claude telemetry settings; using safe defaults", "error", err)
		return DefaultClaudeTelemetryRuntimeConfig(), nil
	}
	return cfg, nil
}

// GetClaudeTelemetryRuntimeConfig returns the current immutable snapshot.
func (s *SettingService) GetClaudeTelemetryRuntimeConfig(ctx context.Context) (*ClaudeTelemetryRuntimeConfig, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultClaudeTelemetryRuntimeConfig(), nil
	}
	if snapshot := s.claudeTelemetryRuntime.Load(); snapshot != nil {
		return snapshot, nil
	}
	cfg, err := s.loadClaudeTelemetryConfig(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := cloneClaudeTelemetryConfig(cfg)
	s.claudeTelemetryRuntime.Store(snapshot)
	return snapshot, nil
}

// RefreshClaudeTelemetryRuntimeConfig reloads the DB snapshot so settings
// saved through another application replica become effective without restart.
func (s *SettingService) RefreshClaudeTelemetryRuntimeConfig(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	loaded, err := s.loadClaudeTelemetryConfig(ctx)
	if err != nil {
		return err
	}
	if current := s.claudeTelemetryRuntime.Load(); current != nil && reflect.DeepEqual(current, loaded) {
		return nil
	}
	snapshot := cloneClaudeTelemetryConfig(loaded)
	s.claudeTelemetryRuntime.Store(snapshot)
	s.notifyClaudeTelemetryRuntimeListeners(snapshot)
	return nil
}

func (s *SettingService) claudeTelemetryRuntimeStatus(cfg *ClaudeTelemetryRuntimeConfig) ClaudeTelemetryRuntimeStatus {
	status := ClaudeTelemetryRuntimeStatus{
		Profile:       ClaudeTelemetryProfile,
		QueueCapacity: cfg.Batch.QueueSize,
	}
	if current := s.claudeTelemetryStatus.Load(); current != nil {
		status = *current
		status.Profile = ClaudeTelemetryProfile
		status.QueueCapacity = cfg.Batch.QueueSize
	}
	return status
}

// PublishClaudeTelemetryRuntimeStatus lets the future exporter publish a safe
// status snapshot without coupling this package to its implementation.
func (s *SettingService) PublishClaudeTelemetryRuntimeStatus(status ClaudeTelemetryRuntimeStatus) {
	status.Profile = ClaudeTelemetryProfile
	cloned := status
	s.claudeTelemetryStatus.Store(&cloned)
}

// RegisterClaudeTelemetryRuntimeListener subscribes a process-local runtime to
// validated DB-backed snapshot changes.
func (s *SettingService) RegisterClaudeTelemetryRuntimeListener(listener func(*ClaudeTelemetryRuntimeConfig)) {
	if s == nil || listener == nil {
		return
	}
	s.claudeTelemetryListenersMu.Lock()
	s.claudeTelemetryListeners = append(s.claudeTelemetryListeners, listener)
	s.claudeTelemetryListenersMu.Unlock()
}

func (s *SettingService) notifyClaudeTelemetryRuntimeListeners(cfg *ClaudeTelemetryRuntimeConfig) {
	s.claudeTelemetryListenersMu.Lock()
	listeners := append([]func(*ClaudeTelemetryRuntimeConfig){}, s.claudeTelemetryListeners...)
	s.claudeTelemetryListenersMu.Unlock()
	for _, listener := range listeners {
		listener(cloneClaudeTelemetryConfig(cfg))
	}
}

func sanitizedClaudeTelemetrySettings(cfg *ClaudeTelemetryRuntimeConfig, status ClaudeTelemetryRuntimeStatus) *ClaudeTelemetrySettings {
	safe := cloneClaudeTelemetryConfig(cfg)
	safe.Datadog.APIKeyConfigured = safe.Datadog.APIKey != ""
	safe.Datadog.APIKey = ""
	safe.Datadog.ClearAPIKey = false
	return &ClaudeTelemetrySettings{
		ClaudeTelemetryRuntimeConfig: *safe,
		Runtime:                      status,
	}
}

func (s *SettingService) GetClaudeTelemetrySettings(ctx context.Context) (*ClaudeTelemetrySettings, error) {
	cfg, err := s.GetClaudeTelemetryRuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return sanitizedClaudeTelemetrySettings(cfg, s.claudeTelemetryRuntimeStatus(cfg)), nil
}

// SaveClaudeTelemetrySettings validates, atomically persists, and publishes a
// new runtime snapshot. Empty Datadog keys preserve the existing secret.
func (s *SettingService) SaveClaudeTelemetrySettings(ctx context.Context, incoming *ClaudeTelemetryRuntimeConfig) (*ClaudeTelemetrySettings, error) {
	if incoming == nil {
		return nil, infraerrors.BadRequest("INVALID_CLAUDE_TELEMETRY_CONFIG", "settings cannot be nil")
	}
	next := cloneClaudeTelemetryConfig(incoming)
	normalizeClaudeTelemetryConfig(next)

	existing, err := s.loadClaudeTelemetryConfig(ctx)
	if err != nil {
		return nil, err
	}
	if next.Datadog.ClearAPIKey && next.Datadog.APIKey != "" {
		return nil, infraerrors.BadRequest(
			"INVALID_CLAUDE_TELEMETRY_CONFIG",
			"datadog api_key and clear_api_key cannot be set together",
		)
	}
	switch {
	case next.Datadog.ClearAPIKey:
		next.Datadog.APIKey = ""
	case next.Datadog.APIKey == "":
		next.Datadog.APIKey = existing.Datadog.APIKey
	}
	next.Datadog.APIKeyConfigured = false
	next.Datadog.ClearAPIKey = false

	if err := validateClaudeTelemetryConfig(next); err != nil {
		return nil, infraerrors.BadRequest("INVALID_CLAUDE_TELEMETRY_CONFIG", err.Error())
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("marshal Claude telemetry settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyClaudeTelemetrySettings, string(encoded)); err != nil {
		return nil, fmt.Errorf("save Claude telemetry settings: %w", err)
	}

	snapshot := cloneClaudeTelemetryConfig(next)
	s.claudeTelemetryRuntime.Store(snapshot)
	s.notifyClaudeTelemetryRuntimeListeners(snapshot)
	return sanitizedClaudeTelemetrySettings(snapshot, s.claudeTelemetryRuntimeStatus(snapshot)), nil
}

package claudetelemetry

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	CompatibilityProfile = "2.1.284"
	defaultFirstPartyURL = "https://api.anthropic.com/api/event_logging/v2/batch"
	defaultDatadogURL    = "https://http-intake.logs.us5.datadoghq.com/api/v2/logs"
)

type Mode string

const (
	ModeOff     Mode = "off"
	ModeRecord  Mode = "record"
	ModeForward Mode = "forward"
	ModeBoth    Mode = "both"
)

func (m Mode) Valid() bool {
	switch m {
	case ModeOff, ModeRecord, ModeForward, ModeBoth:
		return true
	default:
		return false
	}
}

func (m Mode) records() bool  { return m == ModeRecord || m == ModeBoth }
func (m Mode) forwards() bool { return m == ModeForward || m == ModeBoth }

// Config is an immutable snapshot from the caller's point of view. NewExporter
// normalizes defaults and clones maps before retaining it.
type Config struct {
	Mode              Mode
	SetupTokenEnabled bool
	RecordFile        string

	FirstPartyEnabled bool
	FirstPartyURL     string
	DatadogEnabled    bool
	DatadogURL        string
	DatadogAPIKey     string

	BatchDelay      time.Duration
	BatchSize       int
	QueueSize       int
	HTTPTimeout     time.Duration
	MaxAttempts     int
	RetryBase       time.Duration
	RetryMax        time.Duration
	FlushOnExit     bool
	Sampling        map[string]float64
	SamplingDefault *float64
	SessionTTL      time.Duration
	SessionCapacity int
	SlowFirstByte   time.Duration

	Provider         string
	SubscriptionType string
	RendererMode     string
	Interactive      *bool
}

func DefaultConfig() Config {
	return Config{
		Mode:              ModeRecord,
		RecordFile:        "data/telemetry/telemetry-batches.ndjson",
		FirstPartyEnabled: true,
		FirstPartyURL:     defaultFirstPartyURL,
		DatadogEnabled:    true,
		DatadogURL:        defaultDatadogURL,
		BatchDelay:        10 * time.Second,
		BatchSize:         400,
		QueueSize:         8192,
		HTTPTimeout:       15 * time.Second,
		MaxAttempts:       3,
		RetryBase:         500 * time.Millisecond,
		RetryMax:          10 * time.Second,
		FlushOnExit:       true,
		SessionTTL:        30 * time.Minute,
		SessionCapacity:   4096,
		SlowFirstByte:     30 * time.Second,
		Provider:          "anthropic",
	}
}

func (c Config) normalized() Config {
	d := DefaultConfig()
	if c.Mode == "" {
		c.Mode = d.Mode
	}
	if c.FirstPartyURL == "" {
		c.FirstPartyURL = d.FirstPartyURL
	}
	if c.DatadogURL == "" {
		c.DatadogURL = d.DatadogURL
	}
	if c.BatchDelay <= 0 {
		c.BatchDelay = d.BatchDelay
	}
	if c.BatchSize <= 0 {
		c.BatchSize = d.BatchSize
	}
	if c.QueueSize <= 0 {
		c.QueueSize = d.QueueSize
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = d.HTTPTimeout
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = d.MaxAttempts
	}
	if c.RetryBase <= 0 {
		c.RetryBase = d.RetryBase
	}
	if c.RetryMax <= 0 {
		c.RetryMax = d.RetryMax
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = d.SessionTTL
	}
	if c.SessionCapacity <= 0 {
		c.SessionCapacity = d.SessionCapacity
	}
	if c.SlowFirstByte <= 0 {
		c.SlowFirstByte = d.SlowFirstByte
	}
	if c.Provider == "" {
		c.Provider = d.Provider
	}
	c.Sampling = cloneSampling(c.Sampling)
	if c.SamplingDefault != nil {
		rate := *c.SamplingDefault
		c.SamplingDefault = &rate
	}
	return c
}

func (c Config) Validate() error {
	c = c.normalized()
	if !c.Mode.Valid() {
		return fmt.Errorf("invalid telemetry mode %q", c.Mode)
	}
	if c.BatchSize > c.QueueSize {
		return errors.New("telemetry batch size must not exceed queue size")
	}
	if c.MaxAttempts > 10 {
		return errors.New("telemetry max attempts must not exceed 10")
	}
	if c.RetryMax < c.RetryBase {
		return errors.New("telemetry retry max must not be less than retry base")
	}
	if c.Mode.records() && strings.TrimSpace(c.RecordFile) == "" {
		return errors.New("telemetry record file is required in record and both modes")
	}
	for name, rate := range c.Sampling {
		if strings.TrimSpace(name) == "" || rate < 0 || rate > 1 {
			return fmt.Errorf("invalid sampling rate for %q", name)
		}
	}
	if c.SamplingDefault != nil && (*c.SamplingDefault < 0 || *c.SamplingDefault > 1) {
		return errors.New("invalid default telemetry sampling rate")
	}
	for name, raw := range map[string]string{
		"first-party": c.FirstPartyURL,
		"datadog":     c.DatadogURL,
	} {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("invalid %s telemetry URL", name)
		}
	}
	return nil
}

func cloneSampling(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// EligibleAccount deliberately accepts strings so service-layer account types
// do not become a dependency of this package.
func EligibleAccount(platform, accountType string, setupTokenEnabled bool) bool {
	if !strings.EqualFold(strings.TrimSpace(platform), "anthropic") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(accountType)) {
	case "oauth":
		return true
	case "setup-token":
		return setupTokenEnabled
	default:
		return false
	}
}

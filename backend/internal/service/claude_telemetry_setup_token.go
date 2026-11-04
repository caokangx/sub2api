package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/claudetelemetry"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/google/uuid"
)

// EmitSetupTokenLogin reports a setup-token authorization as a fresh, logged-out
// interactive `claude setup-token` process would: new device and session IDs,
// no auth block, and no Authorization header on the first-party batch.
func (m *ClaudeTelemetryManager) EmitSetupTokenLogin(ctx context.Context, login claudetelemetry.SetupTokenLogin, proxyURL string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	generation := m.current
	if m.closed || generation == nil || !generation.enabled || !generation.setupToken {
		m.mu.Unlock()
		return
	}
	generation.active++
	m.mu.Unlock()
	defer m.release(generation)

	httpClient, err := httpclient.GetClient(httpclient.Options{ProxyURL: proxyURL, Timeout: 30 * time.Second})
	if err != nil {
		slog.Warn("build setup-token login telemetry client", "error", err)
		return
	}
	events, err := claudetelemetry.SetupTokenLoginEvents(m.setupTokenLoginContext(ctx), login)
	if err != nil {
		slog.Warn("build setup-token login telemetry", "error", err)
		return
	}
	route := claudetelemetry.DeliveryRoute{
		Key:        "anthropic:setup-token-login:" + proxyURL,
		HTTPClient: httpClient,
	}
	if err := generation.exporter.EnqueueBatch(ctx, route, events); err != nil {
		slog.Warn("enqueue setup-token login telemetry", "error", err)
	}
}

func (m *ClaudeTelemetryManager) setupTokenLoginContext(ctx context.Context) claudetelemetry.EventContext {
	interactive := true
	claudeAIAuth := false
	environment := claudetelemetry.ClientEnvironment{
		Version:        ClaudeTelemetryProfile,
		VersionBase:    ClaudeTelemetryProfile,
		BuildTime:      claudetelemetry.CompatibilityBuildTime,
		IsClaudeAIAuth: &claudeAIAuth,
	}
	if m.settings != nil {
		if settings, err := m.settings.GetClaudeAntiBanSettings(ctx); err == nil && settings != nil {
			profile := settings.Environment.Profile
			platform := claudetelemetry.NormalizePlatform(profile.Platform)
			environment.Platform = platform
			environment.PlatformRaw = platform
			environment.Arch = profile.Arch
			environment.NodeVersion = profile.RuntimeVersion
			environment.Runtime = profile.Runtime
			environment.Shell = claudetelemetry.NormalizeShell(profile.Shell)
		}
	}
	identity := claudetelemetry.Identity{DeviceID: randomDeviceID(), SessionID: uuid.NewString()}
	return claudetelemetry.EventContext{
		Identity: identity,
		IdentitySources: claudetelemetry.FieldProvenance{
			"device_id": claudetelemetry.Inferred, "session_id": claudetelemetry.Inferred,
		},
		Environment: environment,
		Interactive: &interactive,
		Entrypoint:  "cli",
		ClientType:  "cli",
		UserType:    "external",
	}
}

func randomDeviceID() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/claudetelemetry"
)

type claudeTelemetryGeneration struct {
	integration *claudetelemetry.Integration
	exporter    *claudetelemetry.TelemetryExporter
	enabled     bool
	setupToken  bool
	active      int
	retiring    bool
	closing     bool
	flushOnExit bool
	flushRetire bool
	closeDone   chan struct{}
}

// ClaudeTelemetryManager owns hot-swappable telemetry generations. Requests
// retain the generation they started with; retired exporters flush only after
// the last retained request releases it.
type ClaudeTelemetryManager struct {
	mu       sync.Mutex
	settings *SettingService
	current  *claudeTelemetryGeneration
	all      []*claudeTelemetryGeneration
	closed   bool
	stop     chan struct{}
	done     chan struct{}
}

type ClaudeTelemetryAttempt struct {
	manager    *ClaudeTelemetryManager
	generation *claudeTelemetryGeneration
	request    *claudetelemetry.Request
	once       sync.Once
}

func NewClaudeTelemetryManager(settings *SettingService) (*ClaudeTelemetryManager, error) {
	manager := &ClaudeTelemetryManager{
		settings: settings,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	cfg := DefaultClaudeTelemetryRuntimeConfig()
	if settings != nil {
		loaded, err := settings.GetClaudeTelemetryRuntimeConfig(context.Background())
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}
	generation, err := newClaudeTelemetryGeneration(cfg)
	if err != nil {
		return nil, err
	}
	manager.current = generation
	manager.all = append(manager.all, generation)
	if settings != nil {
		settings.RegisterClaudeTelemetryRuntimeListener(manager.update)
	}
	go manager.statusLoop()
	manager.publishStatus()
	return manager, nil
}

func newClaudeTelemetryGeneration(runtime *ClaudeTelemetryRuntimeConfig) (*claudeTelemetryGeneration, error) {
	cfg := toClaudeTelemetryConfig(runtime)
	exporter, err := claudetelemetry.NewTelemetryExporter(cfg)
	if err != nil {
		return nil, err
	}
	integration, err := claudetelemetry.NewIntegration(cfg, exporter)
	if err != nil {
		_ = exporter.Close(context.Background())
		return nil, err
	}
	return &claudeTelemetryGeneration{
		integration: integration,
		exporter:    exporter,
		enabled:     cfg.Mode != claudetelemetry.ModeOff,
		setupToken:  cfg.SetupTokenEnabled,
		flushOnExit: cfg.FlushOnExit,
		closeDone:   make(chan struct{}),
	}, nil
}

func toClaudeTelemetryConfig(runtime *ClaudeTelemetryRuntimeConfig) claudetelemetry.Config {
	if runtime == nil {
		runtime = DefaultClaudeTelemetryRuntimeConfig()
	}
	defaultRate := runtime.Sampling.DefaultRate
	return claudetelemetry.Config{
		Mode:              claudetelemetry.Mode(runtime.Mode),
		SetupTokenEnabled: runtime.SetupTokenEnabled,
		RecordFile:        runtime.RecordFile,
		FirstPartyEnabled: runtime.FirstParty.Enabled,
		FirstPartyURL:     runtime.FirstParty.URL,
		DatadogEnabled:    runtime.Datadog.Enabled,
		DatadogURL:        runtime.Datadog.URL,
		DatadogAPIKey:     runtime.Datadog.APIKey,
		BatchDelay:        time.Duration(runtime.Batch.DelayMS) * time.Millisecond,
		BatchSize:         runtime.Batch.Size,
		QueueSize:         runtime.Batch.QueueSize,
		HTTPTimeout:       time.Duration(runtime.TimeoutMS) * time.Millisecond,
		MaxAttempts:       runtime.Retry.MaxAttempts,
		RetryBase:         time.Duration(runtime.Retry.BaseDelayMS) * time.Millisecond,
		RetryMax:          time.Duration(runtime.Retry.MaxDelayMS) * time.Millisecond,
		FlushOnExit:       runtime.FlushOnExit,
		Sampling:          runtime.Sampling.EventRates,
		SamplingDefault:   &defaultRate,
		Provider:          "anthropic",
	}
}

func (m *ClaudeTelemetryManager) update(runtime *ClaudeTelemetryRuntimeConfig) {
	next, err := newClaudeTelemetryGeneration(runtime)
	if err != nil {
		slog.Error("apply Claude telemetry settings", "error", err)
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = next.exporter.Close(context.Background())
		return
	}
	previous := m.current
	m.current = next
	m.all = append(m.all, next)
	if previous != nil {
		previous.retiring = true
		// Hot reload must drain the old queue even when process-exit flushing
		// is disabled; otherwise a settings save silently drops accepted events.
		previous.flushRetire = true
	}
	m.mu.Unlock()
	m.retireIfIdle(previous)
	m.publishStatus()
}

func (m *ClaudeTelemetryManager) Begin(ctx context.Context, input claudetelemetry.BeginInput) (*ClaudeTelemetryAttempt, error) {
	m.mu.Lock()
	if m.closed || m.current == nil {
		m.mu.Unlock()
		return nil, nil
	}
	generation := m.current
	generation.active++
	m.mu.Unlock()

	request, err := generation.integration.Begin(ctx, input)
	if err != nil {
		m.release(generation)
		return nil, err
	}
	return &ClaudeTelemetryAttempt{manager: m, generation: generation, request: request}, nil
}

func (m *ClaudeTelemetryManager) Eligible(account *Account) bool {
	if m == nil || account == nil {
		return false
	}
	m.mu.Lock()
	generation := m.current
	m.mu.Unlock()
	return generation != nil && generation.enabled &&
		claudetelemetry.EligibleAccount(account.Platform, account.Type, generation.setupToken)
}

func (a *ClaudeTelemetryAttempt) ObserveEventJSON(raw []byte) {
	if a == nil || a.generation == nil {
		return
	}
	_ = a.generation.integration.ObserveEventJSON(a.request, raw)
}

func (a *ClaudeTelemetryAttempt) ObserveJSON(raw []byte) {
	if a == nil || a.generation == nil {
		return
	}
	_ = a.generation.integration.Observe(a.request, raw)
}

func (a *ClaudeTelemetryAttempt) Complete(ctx context.Context, requestID string) {
	if a == nil {
		return
	}
	a.once.Do(func() {
		if err := a.generation.integration.Complete(ctx, a.request, claudetelemetry.Completion{
			RequestID: requestID, CompletedAt: time.Now(),
		}); err != nil {
			slog.Warn("complete Claude telemetry attempt", "error", err)
		}
		a.manager.release(a.generation)
	})
}

func (a *ClaudeTelemetryAttempt) Abort() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		a.generation.integration.Abort(a.request)
		a.manager.release(a.generation)
	})
}

func (m *ClaudeTelemetryManager) release(generation *claudeTelemetryGeneration) {
	m.mu.Lock()
	if generation != nil && generation.active > 0 {
		generation.active--
	}
	m.mu.Unlock()
	m.retireIfIdle(generation)
	m.publishStatus()
}

func (m *ClaudeTelemetryManager) retireIfIdle(generation *claudeTelemetryGeneration) {
	if generation == nil {
		return
	}
	m.mu.Lock()
	if !generation.retiring || generation.active != 0 || generation.closing {
		m.mu.Unlock()
		return
	}
	generation.closing = true
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			for index, candidate := range m.all {
				if candidate == generation {
					m.all = append(m.all[:index], m.all[index+1:]...)
					break
				}
			}
			m.mu.Unlock()
			close(generation.closeDone)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		if generation.flushRetire {
			err = generation.exporter.CloseAndFlush(ctx)
		} else {
			err = generation.exporter.Close(ctx)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("retire Claude telemetry exporter", "error", err)
		}
	}()
}

func (m *ClaudeTelemetryManager) publishStatus() {
	if m == nil || m.settings == nil {
		return
	}
	m.mu.Lock()
	generation := m.current
	available := !m.closed && generation != nil
	m.mu.Unlock()
	status := ClaudeTelemetryRuntimeStatus{Available: available, Profile: ClaudeTelemetryProfile}
	if generation != nil {
		exported := generation.exporter.Status()
		status.QueueLength = exported.QueuedEvents
		status.QueueCapacity = generation.exporter.QueueCapacity()
		if exported.DroppedEvents > 0 {
			status.DroppedEvents = uint64(exported.DroppedEvents)
		}
		if !exported.LastFlush.IsZero() {
			last := exported.LastFlush
			status.LastFlushAt = &last
		}
		status.FirstParty = claudeTelemetryDeliveryStatus(exported.FirstParty)
		status.Datadog = claudeTelemetryDeliveryStatus(exported.Datadog)
	}
	m.settings.PublishClaudeTelemetryRuntimeStatus(status)
}

func claudeTelemetryDeliveryStatus(status claudetelemetry.TargetStatus) ClaudeTelemetryDeliveryStatus {
	out := ClaudeTelemetryDeliveryStatus{LastError: status.LastError}
	if status.Successes > 0 {
		out.Successes = uint64(status.Successes)
	}
	if status.Failures > 0 {
		out.Failures = uint64(status.Failures)
	}
	if !status.LastSuccess.IsZero() {
		last := status.LastSuccess
		out.LastSuccessAt = &last
	}
	return out
}

func (m *ClaudeTelemetryManager) statusLoop() {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-ticker.C:
			ticks++
			if ticks%5 == 0 && m.settings != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := m.settings.RefreshClaudeTelemetryRuntimeConfig(ctx); err != nil {
					slog.Warn("refresh Claude telemetry settings", "error", err)
				}
				cancel()
			}
			m.publishStatus()
		case <-m.stop:
			return
		}
	}
}

func (m *ClaudeTelemetryManager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	close(m.stop)
	generations := append([]*claudeTelemetryGeneration(nil), m.all...)
	for _, generation := range generations {
		if !generation.retiring {
			generation.retiring = true
			generation.flushRetire = generation.flushOnExit
		}
	}
	m.mu.Unlock()
	select {
	case <-m.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, generation := range generations {
		m.retireIfIdle(generation)
	}
	for {
		m.mu.Lock()
		active := false
		for _, generation := range generations {
			if generation.active > 0 {
				active = true
				break
			}
		}
		m.mu.Unlock()
		if !active {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	for _, generation := range generations {
		m.retireIfIdle(generation)
		select {
		case <-generation.closeDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

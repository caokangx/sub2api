package claudetelemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DeliveryRoute struct {
	Key        string
	Token      string
	HTTPClient *http.Client
	HTTPDoer   HTTPDoer
}

// HTTPDoer is implemented by http.Client and by gateway transport adapters
// that preserve an account's proxy and TLS route.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ExporterStatus struct {
	QueuedEvents     int          `json:"queued_events"`
	DroppedEvents    int64        `json:"dropped_events"`
	RecordedBatches  int64        `json:"recorded_batches"`
	ForwardedBatches int64        `json:"forwarded_batches"`
	FailedBatches    int64        `json:"failed_batches"`
	AcceptedEvents   int64        `json:"accepted_events"`
	LastFlush        time.Time    `json:"last_flush_at,omitempty"`
	FirstParty       TargetStatus `json:"first_party"`
	Datadog          TargetStatus `json:"datadog"`
}

type TargetStatus struct {
	Successes   int64     `json:"successes"`
	Failures    int64     `json:"failures"`
	LastSuccess time.Time `json:"last_success_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

type exportItem struct {
	event BuiltEvent
	route DeliveryRoute

	firstPartyRecorded  bool
	firstPartyForwarded bool
	datadogRecorded     bool
	datadogForwarded    bool
}

type TelemetryExporter struct {
	config Config

	mu      sync.Mutex
	queue   []*exportItem
	status  ExporterStatus
	closed  bool
	stop    chan struct{}
	done    chan struct{}
	flushMu sync.Mutex

	sleep func(context.Context, time.Duration) error
}

func NewTelemetryExporter(config Config) (*TelemetryExporter, error) {
	config = config.normalized()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	exporter := &TelemetryExporter{
		config: config, stop: make(chan struct{}), done: make(chan struct{}),
		sleep: sleepContext,
	}
	go exporter.run()
	return exporter, nil
}

func (e *TelemetryExporter) run() {
	defer close(e.done)
	timer := time.NewTimer(e.config.BatchDelay)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			_ = e.Flush(context.Background())
			timer.Reset(e.config.BatchDelay)
		case <-e.stop:
			return
		}
	}
}

func (e *TelemetryExporter) EnqueueBatch(_ context.Context, route DeliveryRoute, events []BuiltEvent) error {
	if e.config.Mode == ModeOff || len(events) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("telemetry exporter is closed")
	}
	for _, event := range events {
		rate, exists := e.config.Sampling[event.Name]
		if !exists && e.config.SamplingDefault != nil {
			rate, exists = *e.config.SamplingDefault, true
		}
		if exists && rate < 1 && (rate <= 0 || rand.Float64() >= rate) {
			continue
		}
		item := &exportItem{event: event, route: snapshotRoute(route)}
		if !e.itemPending(item) {
			continue
		}
		if len(e.queue) >= e.config.QueueSize {
			e.status.DroppedEvents++
			continue
		}
		e.queue = append(e.queue, item)
	}
	return nil
}

func (e *TelemetryExporter) Status() ExporterStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	status := e.status
	status.QueuedEvents = len(e.queue)
	return status
}

func (e *TelemetryExporter) QueueCapacity() int {
	return e.config.QueueSize
}

func (e *TelemetryExporter) Flush(ctx context.Context) error {
	e.flushMu.Lock()
	defer e.flushMu.Unlock()

	e.mu.Lock()
	snapshot := e.queue
	e.queue = nil
	e.mu.Unlock()
	if len(snapshot) == 0 {
		return nil
	}

	groups := make(map[string][]*exportItem)
	var order []string
	for _, item := range snapshot {
		key := routeKey(item.route)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], item)
	}
	var errs []error
	for _, key := range order {
		items := groups[key]
		for offset := 0; offset < len(items); offset += e.config.BatchSize {
			end := min(offset+e.config.BatchSize, len(items))
			batch := items[offset:end]
			if err := e.deliverFirstParty(ctx, batch); err != nil {
				errs = append(errs, err)
			}
			if err := e.deliverDatadog(ctx, batch); err != nil {
				errs = append(errs, err)
			}
		}
	}
	var pending []*exportItem
	for _, item := range snapshot {
		if e.itemPending(item) {
			pending = append(pending, item)
		}
	}
	e.mu.Lock()
	merged := append(pending, e.queue...)
	if len(merged) > e.config.QueueSize {
		e.status.DroppedEvents += int64(len(merged) - e.config.QueueSize)
		merged = merged[:e.config.QueueSize]
	}
	e.queue = merged
	e.status.LastFlush = time.Now()
	e.mu.Unlock()
	return errors.Join(errs...)
}

func (e *TelemetryExporter) Close(ctx context.Context) error {
	return e.close(ctx, e.config.FlushOnExit)
}

// CloseAndFlush retires a hot-reloaded exporter without dropping its queue.
func (e *TelemetryExporter) CloseAndFlush(ctx context.Context) error {
	return e.close(ctx, true)
}

func (e *TelemetryExporter) close(ctx context.Context, flush bool) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.stop)
	e.mu.Unlock()
	select {
	case <-e.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if flush {
		return e.Flush(ctx)
	}
	return nil
}

func (e *TelemetryExporter) itemPending(item *exportItem) bool {
	firstParty := e.config.FirstPartyEnabled &&
		(e.config.Mode.records() && !item.firstPartyRecorded ||
			e.config.Mode.forwards() && !item.firstPartyForwarded)
	datadog := e.config.DatadogEnabled && item.event.Datadog != nil &&
		(e.config.Mode.records() && !item.datadogRecorded ||
			e.config.Mode.forwards() && !item.datadogForwarded)
	return firstParty || datadog
}

func (e *TelemetryExporter) deliverFirstParty(ctx context.Context, items []*exportItem) error {
	if !e.config.FirstPartyEnabled {
		return nil
	}
	if e.config.Mode.records() {
		var pending []*exportItem
		for _, item := range items {
			if !item.firstPartyRecorded {
				pending = append(pending, item)
			}
		}
		if len(pending) > 0 {
			events := make([]ClaudeCodeInternalEvent, 0, len(pending))
			for _, item := range pending {
				events = append(events, item.event.FirstParty)
			}
			if err := e.record("first_party", map[string]any{"events": events}, len(events)); err != nil {
				e.failed("first_party", err)
				return fmt.Errorf("record first-party telemetry: %w", err)
			}
			for _, item := range pending {
				item.firstPartyRecorded = true
			}
		}
	}
	if !e.config.Mode.forwards() {
		return nil
	}
	var pending []*exportItem
	for _, item := range items {
		if !item.firstPartyForwarded {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	events := make([]ClaudeCodeInternalEvent, 0, len(pending))
	for _, item := range pending {
		events = append(events, item.event.FirstParty)
	}
	payload := map[string]any{"events": events}
	headers := map[string]string{
		"accept":         "application/json, text/plain, */*",
		"anthropic-beta": "oauth-2025-04-20",
		"user-agent":     "claude-code/" + CompatibilityProfile,
		"x-service-name": "claude-code",
	}
	if pending[0].route.Token != "" && sendsOAuthCredential(e.config.FirstPartyURL) {
		token := strings.TrimSpace(pending[0].route.Token)
		if len(token) >= 7 && strings.EqualFold(token[:7], "bearer ") {
			token = strings.TrimSpace(token[7:])
		}
		headers["authorization"] = "Bearer " + token
	}
	result, err := e.post(ctx, pending[0].route, e.config.FirstPartyURL, payload, headers)
	if statusCode(err) == http.StatusUnauthorized && headers["authorization"] != "" {
		// Claude Code retries a rejected authenticated batch once without
		// credentials. This fallback is deliberately single-shot; post treats
		// 401 as permanent, so it will not loop anonymously.
		delete(headers, "authorization")
		result, err = e.post(ctx, pending[0].route, e.config.FirstPartyURL, payload, headers)
	}
	if err != nil {
		e.failed("first_party", err)
		if permanentDeliveryError(err) {
			for _, item := range pending {
				item.firstPartyForwarded = true
			}
		}
		return fmt.Errorf("forward first-party telemetry: %w", err)
	}
	accepted := len(pending)
	if result.AcceptedCount != nil {
		accepted = min(*result.AcceptedCount, len(pending))
	}
	// accepted_count does not identify which individual events were accepted.
	// Mark the whole successfully delivered batch complete instead of guessing
	// indexes and resending an arbitrary suffix (which could duplicate events).
	for _, item := range pending {
		item.firstPartyForwarded = true
	}
	e.forwarded("first_party", accepted)
	if accepted < len(pending) {
		err := fmt.Errorf("first-party telemetry accepted %d of %d events", accepted, len(pending))
		e.mu.Lock()
		e.status.DroppedEvents += int64(len(pending) - accepted)
		e.mu.Unlock()
		e.failed("first_party", err)
		return err
	}
	return nil
}

func sendsOAuthCredential(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), "api.anthropic.com")
}

func (e *TelemetryExporter) deliverDatadog(ctx context.Context, items []*exportItem) error {
	if !e.config.DatadogEnabled {
		return nil
	}
	var eligible []*exportItem
	for _, item := range items {
		if item.event.Datadog != nil {
			eligible = append(eligible, item)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	if e.config.Mode.records() {
		var pending []*exportItem
		var logs []map[string]any
		for _, item := range eligible {
			if !item.datadogRecorded {
				pending = append(pending, item)
				logs = append(logs, item.event.Datadog)
			}
		}
		if len(logs) > 0 {
			if err := e.record("datadog", logs, len(logs)); err != nil {
				e.failed("datadog", err)
				return fmt.Errorf("record datadog telemetry: %w", err)
			}
			for _, item := range pending {
				item.datadogRecorded = true
			}
		}
	}
	if !e.config.Mode.forwards() {
		return nil
	}
	var pending []*exportItem
	var logs []map[string]any
	for _, item := range eligible {
		if !item.datadogForwarded {
			pending = append(pending, item)
			logs = append(logs, item.event.Datadog)
		}
	}
	if len(logs) == 0 {
		return nil
	}
	if e.config.DatadogAPIKey == "" {
		for _, item := range pending {
			item.datadogForwarded = true
		}
		return nil
	}
	_, err := e.post(ctx, pending[0].route, e.config.DatadogURL, logs, map[string]string{
		"accept":     "application/json, text/plain, */*",
		"dd-api-key": e.config.DatadogAPIKey,
		"user-agent": "axios/1.15.2",
	})
	if err != nil {
		e.failed("datadog", err)
		if permanentDeliveryError(err) {
			for _, item := range pending {
				item.datadogForwarded = true
			}
		}
		return fmt.Errorf("forward datadog telemetry: %w", err)
	}
	for _, item := range pending {
		item.datadogForwarded = true
	}
	e.forwarded("datadog", len(pending))
	return nil
}

type postResult struct {
	Status        int
	AcceptedCount *int
}

type httpStatusError struct {
	status int
}

func (e *httpStatusError) Error() string { return "HTTP " + strconv.Itoa(e.status) }

func (e *TelemetryExporter) post(ctx context.Context, route DeliveryRoute, url string, payload any, headers map[string]string) (postResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return postResult{}, err
	}
	var doer HTTPDoer = route.HTTPDoer
	if doer == nil {
		doer = route.HTTPClient
	}
	if doer == nil {
		doer = http.DefaultClient
	}
	var lastErr error
	for attempt := 1; attempt <= e.config.MaxAttempts; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, e.config.HTTPTimeout)
		request, requestErr := http.NewRequestWithContext(requestCtx, http.MethodPost, url, bytes.NewReader(raw))
		if requestErr != nil {
			cancel()
			return postResult{}, requestErr
		}
		request.Header.Set("content-type", "application/json")
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, requestErr := doer.Do(request)
		if requestErr != nil {
			lastErr = requestErr
			cancel()
		} else {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
			_ = response.Body.Close()
			cancel()
			if readErr != nil {
				lastErr = readErr
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				lastErr = &httpStatusError{status: response.StatusCode}
			} else {
				result := postResult{Status: response.StatusCode}
				var accepted struct {
					AcceptedCount *int `json:"accepted_count"`
				}
				if json.Unmarshal(body, &accepted) == nil && accepted.AcceptedCount != nil && *accepted.AcceptedCount >= 0 {
					result.AcceptedCount = accepted.AcceptedCount
				}
				return result, nil
			}
		}
		if !transientDeliveryError(lastErr) || attempt == e.config.MaxAttempts {
			break
		}
		delay := min(e.config.RetryBase*time.Duration(attempt*attempt), e.config.RetryMax)
		if err := e.sleep(ctx, delay); err != nil {
			return postResult{}, err
		}
	}
	return postResult{}, lastErr
}

func (e *TelemetryExporter) record(route string, payload any, eventCount int) error {
	if err := os.MkdirAll(filepath.Dir(e.config.RecordFile), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(e.config.RecordFile); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("telemetry record file must not be a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entry := map[string]any{
		"recorded_at": time.Now().UTC().Format(time.RFC3339Nano),
		"route":       route, "event_count": eventCount, "payload": payload,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(e.config.RecordFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return err
	}
	e.mu.Lock()
	e.status.RecordedBatches++
	e.mu.Unlock()
	return nil
}

func (e *TelemetryExporter) failed(target string, err error) {
	e.mu.Lock()
	e.status.FailedBatches++
	status := e.targetStatus(target)
	status.Failures++
	if code := statusCode(err); code > 0 {
		status.LastError = "HTTP " + strconv.Itoa(code)
	} else {
		status.LastError = "delivery failed"
	}
	e.mu.Unlock()
}

func (e *TelemetryExporter) forwarded(target string, accepted int) {
	e.mu.Lock()
	e.status.ForwardedBatches++
	e.status.AcceptedEvents += int64(accepted)
	status := e.targetStatus(target)
	status.Successes += int64(accepted)
	status.LastSuccess = time.Now()
	status.LastError = ""
	e.mu.Unlock()
}

func (e *TelemetryExporter) targetStatus(target string) *TargetStatus {
	if target == "datadog" {
		return &e.status.Datadog
	}
	return &e.status.FirstParty
}

func snapshotRoute(route DeliveryRoute) DeliveryRoute {
	return DeliveryRoute{Key: route.Key, Token: route.Token, HTTPClient: route.HTTPClient, HTTPDoer: route.HTTPDoer}
}

func routeKey(route DeliveryRoute) string {
	sum := sha256.Sum256([]byte(route.Token))
	return route.Key + ":" + hex.EncodeToString(sum[:8])
}

func statusCode(err error) int {
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.status
	}
	return 0
}

func transientDeliveryError(err error) bool {
	status := statusCode(err)
	return status == 0 || status == http.StatusTooManyRequests || status >= 500
}

func permanentDeliveryError(err error) bool {
	status := statusCode(err)
	return status > 0 && status != http.StatusTooManyRequests && status < 500
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

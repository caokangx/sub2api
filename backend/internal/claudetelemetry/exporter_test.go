package claudetelemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExporterKeepsFirstPartyAndDatadogPendingIndependent(t *testing.T) {
	var acceptAll atomic.Bool
	var firstPartyCalls atomic.Int32
	var datadogCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/first":
			firstPartyCalls.Add(1)
			w.Header().Set("content-type", "application/json")
			if acceptAll.Load() {
				_, _ = w.Write([]byte(`{"accepted_count":2}`))
			} else {
				_, _ = w.Write([]byte(`{"accepted_count":1}`))
			}
		case "/datadog":
			datadogCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	config := forwardingConfig(server.URL)
	exporter, err := NewTelemetryExporter(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, exporter.Close(context.Background())) })
	require.NoError(t, exporter.EnqueueBatch(context.Background(),
		DeliveryRoute{Key: "route", Token: "oauth-token", HTTPClient: server.Client()},
		testBuiltEvents(t, 3),
	))
	err = exporter.Flush(context.Background())
	require.ErrorContains(t, err, "accepted 1 of 3")
	require.Zero(t, exporter.Status().QueuedEvents, "partial acceptance must not guess and resend an arbitrary suffix")
	require.EqualValues(t, 1, datadogCalls.Load(), "datadog completed independently")

	acceptAll.Store(true)
	require.NoError(t, exporter.Flush(context.Background()))
	require.Zero(t, exporter.Status().QueuedEvents)
	require.EqualValues(t, 1, datadogCalls.Load(), "completed datadog items were not retried")
	require.EqualValues(t, 1, firstPartyCalls.Load())
}

func TestExporterRetriesThenFallsBackOnceWithoutOAuthTokenOn401(t *testing.T) {
	var calls atomic.Int32
	var authSequence []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		authSequence = append(authSequence, request.Header.Get("Authorization"))
		if call <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	config := forwardingConfig(server.URL)
	config.FirstPartyURL = "https://api.anthropic.com/api/event_logging/v2/batch"
	config.DatadogEnabled = false
	config.MaxAttempts = 3
	config.RetryBase = time.Microsecond
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	baseTransport := server.Client().Transport
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		cloned := request.Clone(request.Context())
		cloned.URL.Scheme = target.Scheme
		cloned.URL.Host = target.Host
		return baseTransport.RoundTrip(cloned)
	})}
	exporter, err := NewTelemetryExporter(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, exporter.Close(context.Background())) })
	require.NoError(t, exporter.EnqueueBatch(context.Background(),
		DeliveryRoute{Key: "route", Token: "token", HTTPClient: client},
		testBuiltEvents(t, 1),
	))
	require.Error(t, exporter.Flush(context.Background()))
	require.EqualValues(t, 4, calls.Load(), "two 5xx retries, one authenticated 401, then one anonymous fallback")
	require.Equal(t, []string{"Bearer token", "Bearer token", "Bearer token", ""}, authSequence)
	require.Zero(t, exporter.Status().QueuedEvents)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestExporterRecordBatchesAreBoundedNDJSONAndSecretFree(t *testing.T) {
	recordFile := t.TempDir() + "/nested/telemetry.ndjson"
	config := DefaultConfig()
	config.Mode = ModeRecord
	config.RecordFile = recordFile
	config.BatchSize = 2
	config.QueueSize = 8
	config.BatchDelay = time.Hour
	config.FlushOnExit = false
	exporter, err := NewTelemetryExporter(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, exporter.Close(context.Background())) })
	require.NoError(t, exporter.EnqueueBatch(context.Background(),
		DeliveryRoute{Key: "route", Token: "NEVER_RECORD_THIS_TOKEN"},
		testBuiltEvents(t, 3),
	))
	require.NoError(t, exporter.Flush(context.Background()))
	require.Zero(t, exporter.Status().QueuedEvents)

	file, err := os.Open(recordFile)
	require.NoError(t, err)
	defer file.Close()
	var lines []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line))
		lines = append(lines, line)
		require.LessOrEqual(t, int(line["event_count"].(float64)), 2)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, lines, 4, "two first-party and two datadog batches")
	raw, err := os.ReadFile(recordFile)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "NEVER_RECORD_THIS_TOKEN")
	info, err := os.Stat(recordFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestExporterBothRecordsAndForwards(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path == "/first" {
			_, _ = w.Write([]byte(`{"accepted_count":1}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	config := forwardingConfig(server.URL)
	config.Mode = ModeBoth
	config.RecordFile = t.TempDir() + "/both.ndjson"
	exporter, err := NewTelemetryExporter(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, exporter.Close(context.Background())) })
	require.NoError(t, exporter.EnqueueBatch(context.Background(),
		DeliveryRoute{Key: "both", HTTPClient: server.Client()}, testBuiltEvents(t, 1),
	))
	require.NoError(t, exporter.Flush(context.Background()))
	require.EqualValues(t, 2, calls.Load())
	require.EqualValues(t, 2, exporter.Status().RecordedBatches)
	require.EqualValues(t, 2, exporter.Status().ForwardedBatches)
	require.Zero(t, exporter.Status().QueuedEvents)
}

func TestExporterOffAndQueueBound(t *testing.T) {
	off := DefaultConfig()
	off.Mode = ModeOff
	off.BatchDelay = time.Hour
	off.FlushOnExit = false
	exporter, err := NewTelemetryExporter(off)
	require.NoError(t, err)
	require.NoError(t, exporter.EnqueueBatch(context.Background(), DeliveryRoute{}, testBuiltEvents(t, 2)))
	require.Zero(t, exporter.Status().QueuedEvents)
	require.NoError(t, exporter.Close(context.Background()))

	bounded := DefaultConfig()
	bounded.RecordFile = t.TempDir() + "/events.ndjson"
	bounded.QueueSize = 2
	bounded.BatchSize = 2
	bounded.BatchDelay = time.Hour
	bounded.FlushOnExit = false
	exporter, err = NewTelemetryExporter(bounded)
	require.NoError(t, err)
	require.NoError(t, exporter.EnqueueBatch(context.Background(), DeliveryRoute{}, testBuiltEvents(t, 3)))
	require.Equal(t, 2, exporter.Status().QueuedEvents)
	require.EqualValues(t, 1, exporter.Status().DroppedEvents)
	require.NoError(t, exporter.Close(context.Background()))
}

func TestOAuthCredentialOnlyTargetsAnthropicHTTPS(t *testing.T) {
	require.True(t, sendsOAuthCredential("https://api.anthropic.com/api/event_logging/v2/batch"))
	require.False(t, sendsOAuthCredential("http://api.anthropic.com/api/event_logging/v2/batch"))
	require.False(t, sendsOAuthCredential("https://api.anthropic.com.evil.test/api/event_logging/v2/batch"))
	require.False(t, sendsOAuthCredential("https://telemetry.example.test/batch"))
}

func forwardingConfig(baseURL string) Config {
	config := DefaultConfig()
	config.Mode = ModeForward
	config.FirstPartyURL = strings.TrimSuffix(baseURL, "/") + "/first"
	config.DatadogURL = strings.TrimSuffix(baseURL, "/") + "/datadog"
	config.DatadogAPIKey = "datadog-test-key"
	config.BatchDelay = time.Hour
	config.HTTPTimeout = time.Second
	config.MaxAttempts = 1
	config.RetryBase = time.Microsecond
	config.FlushOnExit = false
	return config
}

func testBuiltEvents(t *testing.T, count int) []BuiltEvent {
	t.Helper()
	events := make([]BuiltEvent, 0, count)
	for index := 0; index < count; index++ {
		event, err := buildEvent(time.Unix(int64(index+1), 0), testEventContext(),
			"tengu_api_success", map[string]any{"inputTokens": index + 1},
			observedFields("inputTokens"),
		)
		require.NoError(t, err)
		events = append(events, event)
	}
	return events
}

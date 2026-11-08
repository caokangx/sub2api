package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// Shared plumbing for the admin "is this relay really <provider>" probes.

const (
	upstreamVerifyStepTimeout      = 120 * time.Second
	upstreamVerifyMaxResponseBytes = 2 << 20
	upstreamVerifyMaxStreamBytes   = 64 << 10
	upstreamVerifyMaxErrorChars    = 2000
	upstreamVerifyMaxEvidenceChars = 200
	// The probe prompt is ~17-24 tokens; anything far above means the relay injects its own prompt.
	upstreamVerifyInjectedPromptTokens = 60
	upstreamVerifyProbePrompt          = "写一篇 800 字的文章介绍长城"
)

const (
	UpstreamVerifyStatusOK     = "ok"
	UpstreamVerifyStatusFailed = "failed"

	UpstreamVerifyEvidenceInfo = "info"
)

var upstreamVerifyModelUnavailablePattern = regexp.MustCompile(`(?i)model_not_found|no available channel|无可用渠道|deploymentnotfound|not_found_error|model[^\n]{0,80}(not found|does not exist)`)

type UpstreamVerifyModelsResult struct {
	Status int      `json:"status"`
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

type UpstreamVerifyEvidence struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Value  string `json:"value,omitempty"`
}

type UpstreamVerifyStep struct {
	Name       string              `json:"name"`
	Status     int                 `json:"status"`
	OK         bool                `json:"ok"`
	DurationMs int64               `json:"duration_ms"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type upstreamVerifyHTTPResult struct {
	status   int
	headers  http.Header
	body     []byte
	duration time.Duration
	err      error
}

type upstreamVerifyClient struct {
	upstream HTTPUpstream
	// setAuth writes the provider-specific credential headers.
	setAuth func(h http.Header, apiKey string)
	// reportedHeaderPrefixes selects which response headers are echoed back to the admin UI.
	reportedHeaderPrefixes []string
}

func (c *upstreamVerifyClient) listModels(ctx context.Context, baseURL, apiKey string) UpstreamVerifyModelsResult {
	res := c.do(ctx, http.MethodGet, upstreamVerifyEndpoint(baseURL, "/v1/models"), apiKey, nil)
	out := UpstreamVerifyModelsResult{Status: res.status, Models: []string{}}
	if res.err != nil {
		out.Error = res.err.Error()
		return out
	}
	if res.status != http.StatusOK {
		out.Error = upstreamVerifyErrorText(res.status, res.body)
		return out
	}
	gjson.GetBytes(res.body, "data.#.id").ForEach(func(_, id gjson.Result) bool {
		if v := strings.TrimSpace(id.String()); v != "" {
			out.Models = append(out.Models, v)
		}
		return true
	})
	if len(out.Models) == 0 {
		out.Error = "upstream returned no models"
	}
	return out
}

// do sends one request and buffers the whole (size-capped) response.
func (c *upstreamVerifyClient) do(ctx context.Context, method, url, apiKey string, body []byte) upstreamVerifyHTTPResult {
	ctx, cancel := context.WithTimeout(ctx, upstreamVerifyStepTimeout)
	defer cancel()
	start := time.Now()
	resp, err := c.open(ctx, method, url, apiKey, body)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = errors.New("upstream request timed out")
		}
		return upstreamVerifyHTTPResult{err: err, duration: time.Since(start)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, upstreamVerifyMaxResponseBytes+1))
	out := upstreamVerifyHTTPResult{status: resp.StatusCode, headers: resp.Header, duration: time.Since(start)}
	if err != nil {
		out.err = fmt.Errorf("read upstream response: %w", err)
		return out
	}
	if len(raw) > upstreamVerifyMaxResponseBytes {
		out.err = errors.New("upstream response too large")
		return out
	}
	out.body = raw
	return out
}

// open sends one request and hands the live response to the caller (used for streams).
func (c *upstreamVerifyClient) open(ctx context.Context, method, url, apiKey string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	c.setAuth(req.Header, apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.upstream.Do(req, "", 0, 1)
}

func (c *upstreamVerifyClient) newStep(name string, res upstreamVerifyHTTPResult) UpstreamVerifyStep {
	return UpstreamVerifyStep{
		Name:       name,
		Status:     res.status,
		DurationMs: res.duration.Milliseconds(),
		Headers:    c.reportedHeaders(res.headers),
	}
}

func (c *upstreamVerifyClient) reportedHeaders(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for name, values := range h {
		lower := strings.ToLower(name)
		for _, prefix := range c.reportedHeaderPrefixes {
			if strings.HasPrefix(lower, prefix) {
				out[lower] = append(out[lower], values...)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeUpstreamVerifyHeaders(dst map[string][]string, src map[string][]string) map[string][]string {
	for name, values := range src {
		if dst == nil {
			dst = make(map[string][]string)
		}
		dst[name] = append(dst[name], values...)
	}
	return dst
}

// upstreamVerifyEvidenceList accumulates evidence entries, capping long values
// (some headers carry kilobyte-sized opaque tokens).
type upstreamVerifyEvidenceList []UpstreamVerifyEvidence

func (l *upstreamVerifyEvidenceList) add(kind, detail, value string) {
	if r := []rune(value); len(r) > upstreamVerifyMaxEvidenceChars {
		value = string(r[:upstreamVerifyMaxEvidenceChars]) + "…"
	}
	*l = append(*l, UpstreamVerifyEvidence{Kind: kind, Detail: detail, Value: value})
}

func upstreamVerifyModelUnavailable(errs []string) bool {
	for _, msg := range errs {
		if upstreamVerifyModelUnavailablePattern.MatchString(msg) {
			return true
		}
	}
	return false
}

func upstreamVerifyHasNewAPIHeaders(headers []http.Header) bool {
	for _, h := range headers {
		for name := range h {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-new-api-") || strings.HasPrefix(lower, "x-oneapi-") {
				return true
			}
		}
	}
	return false
}

func upstreamVerifyEndpoint(baseURL, path string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/v1")
	return base + path
}

func upstreamVerifyErrorText(status int, body []byte) string {
	msg := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if msg == "" {
		msg = strings.TrimSpace(gjson.GetBytes(body, "message").String())
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if code := gjson.GetBytes(body, "error.code").String(); code != "" && !strings.Contains(msg, code) {
		msg = code + ": " + msg
	}
	if msg == "" {
		msg = fmt.Sprintf("upstream returned HTTP %d", status)
	} else if status != 0 && status != http.StatusOK {
		msg = fmt.Sprintf("HTTP %d: %s", status, msg)
	}
	return upstreamVerifyTruncate(msg)
}

func upstreamVerifyTruncate(s string) string {
	r := []rune(s)
	if len(r) > upstreamVerifyMaxErrorChars {
		return string(r[:upstreamVerifyMaxErrorChars])
	}
	return s
}

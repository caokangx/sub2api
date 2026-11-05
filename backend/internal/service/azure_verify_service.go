package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	azureVerifyStepTimeout       = 120 * time.Second
	azureVerifyMaxResponseBytes  = 2 << 20
	azureVerifyMaxStreamBytes    = 64 << 10
	azureVerifyMaxStreamChunks   = 3
	azureVerifyMaxErrorChars     = 2000
	azureVerifyMaxEvidenceChars  = 200
	azureVerifyMaxCompletionToks = 64
	// The probe prompt is ~17 tokens; anything far above means the relay injects its own prompt.
	azureVerifyInjectedPromptTokens = 200
	azureVerifyProbePrompt          = "写一篇 800 字的文章介绍长城"
	azureVerifyRememberPrompt       = "记住数字 4729，只回复OK"
	azureVerifyRecallPrompt         = "我刚才让你记住的数字是多少？只回复数字"
	azureVerifyRememberedNumber     = "4729"
)

const (
	AzureVerifyStatusOK     = "ok"
	AzureVerifyStatusFailed = "failed"

	AzureVerifyEvidenceAzure    = "azure"
	AzureVerifyEvidenceNonAzure = "non_azure"
	AzureVerifyEvidenceInfo     = "info"

	AzureVerifyStepChat      = "chat_completions"
	AzureVerifyStepStream    = "chat_completions_stream"
	AzureVerifyStepResponses = "responses_continuation"
)

var azureVerifyAzureHeaders = []string{
	"apim-request-id",
	"x-ms-region",
	"x-ms-rai-invoked",
	"azureml-model-session",
	"x-ms-served-model",
}

var azureVerifyReportedHeaderPrefixes = []string{
	"apim-",
	"azureml-",
	"x-ms-",
	"openai-",
	"x-codex-",
	"x-request-id",
	"x-new-api-",
	"x-oneapi-",
	"x-ratelimit-limit-",
	"server",
}

var azureVerifyAzureErrorPatterns = []struct {
	re     *regexp.Regexp
	detail string
}{
	{regexp.MustCompile(`(?i)azure openai`), "报错提到 Azure OpenAI"},
	{regexp.MustCompile(`(?i)deploymentnotfound|api deployment for this resource does not exist`), "报错为 Azure 的 DeploymentNotFound"},
	{regexp.MustCompile(`(?i)content management policy|responsibleaipolicyviolation`), "报错为 Azure 内容安全策略拦截"},
	{regexp.MustCompile(`(?i)\bin [a-z0-9]+ have exceeded (token )?rate limit`), "报错为 Azure 区域级速率限制"},
}

var azureVerifyNonAzureErrorPatterns = []struct {
	re     *regexp.Regexp
	detail string
}{
	{regexp.MustCompile(`(?i)usage_limit_reached|you'?ve hit your usage limit`), "报错为 ChatGPT 订阅额度用尽（usage_limit_reached）"},
	{regexp.MustCompile(`(?i)with a chatgpt account`), "报错提到 ChatGPT 账号"},
}

var azureVerifyModelUnavailablePattern = regexp.MustCompile(`(?i)model_not_found|no available channel|无可用渠道|deploymentnotfound|model[^\n]{0,80}(not found|does not exist)`)

var azureVerifyDatedModelPattern = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// AzureVerifyService probes an admin-supplied OpenAI-compatible relay and judges
// whether the models behind it are served by Azure OpenAI.
type AzureVerifyService struct {
	upstream HTTPUpstream
}

func NewAzureVerifyService(upstream HTTPUpstream) *AzureVerifyService {
	return &AzureVerifyService{upstream: upstream}
}

type AzureVerifyModelsResult struct {
	Status int      `json:"status"`
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

type AzureVerifyEvidence struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Value  string `json:"value,omitempty"`
}

type AzureVerifyStep struct {
	Name       string              `json:"name"`
	Status     int                 `json:"status"`
	OK         bool                `json:"ok"`
	DurationMs int64               `json:"duration_ms"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type AzureVerifyProbeResult struct {
	Model            string                `json:"model"`
	Status           string                `json:"status"`
	IsAzure          bool                  `json:"is_azure"`
	Suspicious       bool                  `json:"suspicious"`
	ModelUnavailable bool                  `json:"model_unavailable"`
	ServedModel      string                `json:"served_model,omitempty"`
	Evidences        []AzureVerifyEvidence `json:"evidences"`
	Steps            []AzureVerifyStep     `json:"steps"`
	Error            string                `json:"error,omitempty"`
}

// azureVerifyObservation is everything the probe requests saw; evaluateAzureEvidence
// derives the verdict from it without doing any I/O.
type azureVerifyObservation struct {
	Model   string
	Headers []http.Header
	Errors  []string

	ChatOK           bool
	ChatFinishReason string
	ChatPromptTokens int64
	ChatOutputTokens int64
	ChatFilterFields bool
	ChatModel        string

	StreamOK          bool
	StreamAzureChunk  bool
	StreamFilterField bool

	ContinuationTried bool
	ContinuationOK    bool
}

type azureVerdict struct {
	Status           string
	IsAzure          bool
	Suspicious       bool
	ModelUnavailable bool
	ServedModel      string
	Evidences        []AzureVerifyEvidence
}

type azureVerifyHTTPResult struct {
	status   int
	headers  http.Header
	body     []byte
	duration time.Duration
	err      error
}

func (s *AzureVerifyService) ListModels(ctx context.Context, baseURL, apiKey string) AzureVerifyModelsResult {
	res := s.do(ctx, http.MethodGet, azureVerifyEndpoint(baseURL, "/v1/models"), apiKey, nil)
	out := AzureVerifyModelsResult{Status: res.status, Models: []string{}}
	if res.err != nil {
		out.Error = res.err.Error()
		return out
	}
	if res.status != http.StatusOK {
		out.Error = azureVerifyErrorText(res.status, res.body)
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

func (s *AzureVerifyService) ProbeModel(ctx context.Context, baseURL, apiKey, model string) AzureVerifyProbeResult {
	obs := azureVerifyObservation{Model: model}
	steps := make([]AzureVerifyStep, 0, 3)

	steps = append(steps, s.probeChat(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeStream(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeContinuation(ctx, baseURL, apiKey, &obs))

	verdict := evaluateAzureEvidence(obs)
	out := AzureVerifyProbeResult{
		Model:            model,
		Status:           verdict.Status,
		IsAzure:          verdict.IsAzure,
		Suspicious:       verdict.Suspicious,
		ModelUnavailable: verdict.ModelUnavailable,
		ServedModel:      verdict.ServedModel,
		Evidences:        verdict.Evidences,
		Steps:            steps,
	}
	if verdict.Status == AzureVerifyStatusFailed && len(obs.Errors) > 0 {
		out.Error = obs.Errors[0]
	}
	return out
}

func (s *AzureVerifyService) probeChat(ctx context.Context, baseURL, apiKey string, obs *azureVerifyObservation) AzureVerifyStep {
	body, _ := json.Marshal(map[string]any{
		"model":                 obs.Model,
		"max_completion_tokens": azureVerifyMaxCompletionToks,
		"messages":              []map[string]string{{"role": "user", "content": azureVerifyProbePrompt}},
	})
	res := s.do(ctx, http.MethodPost, azureVerifyEndpoint(baseURL, "/v1/chat/completions"), apiKey, body)
	step := newAzureVerifyStep(AzureVerifyStepChat, res)
	if !s.recordResult(obs, &step, res) {
		return step
	}
	if !gjson.ValidBytes(res.body) || gjson.GetBytes(res.body, "error").Exists() {
		step.OK = false
		step.Error = azureVerifyErrorText(res.status, res.body)
		obs.Errors = append(obs.Errors, step.Error)
		return step
	}
	obs.ChatOK = true
	obs.ChatFinishReason = gjson.GetBytes(res.body, "choices.0.finish_reason").String()
	obs.ChatPromptTokens = gjson.GetBytes(res.body, "usage.prompt_tokens").Int()
	obs.ChatOutputTokens = gjson.GetBytes(res.body, "usage.completion_tokens").Int()
	obs.ChatModel = gjson.GetBytes(res.body, "model").String()
	obs.ChatFilterFields = gjson.GetBytes(res.body, "prompt_filter_results").Exists() ||
		gjson.GetBytes(res.body, "choices.0.content_filter_results").Exists()
	return step
}

func (s *AzureVerifyService) probeStream(ctx context.Context, baseURL, apiKey string, obs *azureVerifyObservation) AzureVerifyStep {
	body, _ := json.Marshal(map[string]any{
		"model":    obs.Model,
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "say hi"}},
	})
	ctx, cancel := context.WithTimeout(ctx, azureVerifyStepTimeout)
	defer cancel()

	start := time.Now()
	req, err := s.newRequest(ctx, http.MethodPost, azureVerifyEndpoint(baseURL, "/v1/chat/completions"), apiKey, body)
	if err != nil {
		return s.failStep(obs, AzureVerifyStepStream, 0, start, err.Error())
	}
	resp, err := s.upstream.Do(req, "", 0, 1)
	if err != nil {
		return s.failStep(obs, AzureVerifyStepStream, 0, start, "request failed: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	step := AzureVerifyStep{Name: AzureVerifyStepStream, Status: resp.StatusCode, Headers: azureVerifyReportedHeaders(resp.Header)}
	obs.Headers = append(obs.Headers, resp.Header)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, azureVerifyMaxErrorChars*4))
		step.Error = azureVerifyErrorText(resp.StatusCode, raw)
		step.DurationMs = time.Since(start).Milliseconds()
		obs.Errors = append(obs.Errors, step.Error)
		return step
	}

	scanner := bufio.NewScanner(io.LimitReader(resp.Body, azureVerifyMaxStreamBytes))
	scanner.Buffer(make([]byte, 0, 16<<10), azureVerifyMaxStreamBytes)
	chunks := 0
	for scanner.Scan() && chunks < azureVerifyMaxStreamChunks {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		chunks++
		if gjson.Get(payload, "error").Exists() {
			step.Error = azureVerifyTruncate(payload)
			obs.Errors = append(obs.Errors, step.Error)
			break
		}
		if gjson.Get(payload, "prompt_filter_results").Exists() || gjson.Get(payload, "choices.0.content_filter_results").Exists() {
			obs.StreamFilterField = true
		}
		if chunks == 1 && gjson.Get(payload, "id").String() == "" &&
			len(gjson.Get(payload, "choices").Array()) == 0 &&
			gjson.Get(payload, "prompt_filter_results").Exists() {
			obs.StreamAzureChunk = true
		}
	}
	step.DurationMs = time.Since(start).Milliseconds()
	if step.Error == "" {
		if chunks == 0 {
			step.Error = "stream returned no data chunks"
			obs.Errors = append(obs.Errors, step.Error)
		} else {
			step.OK = true
			obs.StreamOK = true
		}
	}
	return step
}

func (s *AzureVerifyService) probeContinuation(ctx context.Context, baseURL, apiKey string, obs *azureVerifyObservation) AzureVerifyStep {
	obs.ContinuationTried = true
	endpoint := azureVerifyEndpoint(baseURL, "/v1/responses")
	first, _ := json.Marshal(map[string]any{"model": obs.Model, "store": true, "input": azureVerifyRememberPrompt})
	res := s.do(ctx, http.MethodPost, endpoint, apiKey, first)
	step := newAzureVerifyStep(AzureVerifyStepResponses, res)
	if !s.recordResult(obs, &step, res) {
		return step
	}
	responseID := gjson.GetBytes(res.body, "id").String()
	if responseID == "" {
		step.OK = false
		step.Error = azureVerifyErrorText(res.status, res.body)
		obs.Errors = append(obs.Errors, step.Error)
		return step
	}

	second, _ := json.Marshal(map[string]any{
		"model":                obs.Model,
		"previous_response_id": responseID,
		"input":                azureVerifyRecallPrompt,
	})
	res2 := s.do(ctx, http.MethodPost, endpoint, apiKey, second)
	step.Status = res2.status
	step.DurationMs += res2.duration.Milliseconds()
	for name, values := range azureVerifyReportedHeaders(res2.headers) {
		if step.Headers == nil {
			step.Headers = make(map[string][]string)
		}
		step.Headers[name] = append(step.Headers[name], values...)
	}
	if !s.recordResult(obs, &step, res2) {
		return step
	}
	if !strings.Contains(azureVerifyResponsesText(res2.body), azureVerifyRememberedNumber) {
		step.OK = false
		step.Error = "previous_response_id did not carry the earlier turn: " + azureVerifyTruncate(string(res2.body))
		return step
	}
	obs.ContinuationOK = true
	return step
}

// recordResult folds one HTTP exchange into the observation and reports whether
// the exchange succeeded at the HTTP level.
func (s *AzureVerifyService) recordResult(obs *azureVerifyObservation, step *AzureVerifyStep, res azureVerifyHTTPResult) bool {
	if res.headers != nil {
		obs.Headers = append(obs.Headers, res.headers)
	}
	if res.err != nil {
		step.OK = false
		step.Error = "request failed: " + res.err.Error()
		obs.Errors = append(obs.Errors, step.Error)
		return false
	}
	if res.status != http.StatusOK {
		step.OK = false
		step.Error = azureVerifyErrorText(res.status, res.body)
		obs.Errors = append(obs.Errors, step.Error)
		return false
	}
	step.OK = true
	return true
}

func (s *AzureVerifyService) failStep(obs *azureVerifyObservation, name string, status int, start time.Time, msg string) AzureVerifyStep {
	obs.Errors = append(obs.Errors, msg)
	return AzureVerifyStep{Name: name, Status: status, DurationMs: time.Since(start).Milliseconds(), Error: msg}
}

func (s *AzureVerifyService) do(ctx context.Context, method, url, apiKey string, body []byte) azureVerifyHTTPResult {
	ctx, cancel := context.WithTimeout(ctx, azureVerifyStepTimeout)
	defer cancel()
	start := time.Now()
	req, err := s.newRequest(ctx, method, url, apiKey, body)
	if err != nil {
		return azureVerifyHTTPResult{err: err, duration: time.Since(start)}
	}
	resp, err := s.upstream.Do(req, "", 0, 1)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = errors.New("upstream request timed out")
		}
		return azureVerifyHTTPResult{err: err, duration: time.Since(start)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, azureVerifyMaxResponseBytes+1))
	out := azureVerifyHTTPResult{status: resp.StatusCode, headers: resp.Header, duration: time.Since(start)}
	if err != nil {
		out.err = fmt.Errorf("read upstream response: %w", err)
		return out
	}
	if len(raw) > azureVerifyMaxResponseBytes {
		out.err = errors.New("upstream response too large")
		return out
	}
	out.body = raw
	return out
}

func (s *AzureVerifyService) newRequest(ctx context.Context, method, url, apiKey string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func newAzureVerifyStep(name string, res azureVerifyHTTPResult) AzureVerifyStep {
	return AzureVerifyStep{
		Name:       name,
		Status:     res.status,
		DurationMs: res.duration.Milliseconds(),
		Headers:    azureVerifyReportedHeaders(res.headers),
	}
}

func evaluateAzureEvidence(obs azureVerifyObservation) azureVerdict {
	var v azureVerdict
	add := func(kind, detail, value string) {
		if r := []rune(value); len(r) > azureVerifyMaxEvidenceChars {
			value = string(r[:azureVerifyMaxEvidenceChars]) + "…"
		}
		v.Evidences = append(v.Evidences, AzureVerifyEvidence{Kind: kind, Detail: detail, Value: value})
	}
	azureHits, counterHits := 0, 0

	seenHeader := map[string]bool{}
	requestIDs := map[string]bool{}
	newAPI := false
	for _, h := range obs.Headers {
		for _, name := range azureVerifyAzureHeaders {
			if val := strings.TrimSpace(h.Get(name)); val != "" && !seenHeader[name] {
				seenHeader[name] = true
				azureHits++
				add(AzureVerifyEvidenceAzure, "响应头包含 Azure 特有字段 "+name, val)
				if name == "x-ms-served-model" && v.ServedModel == "" {
					v.ServedModel = val
				}
			}
		}
		for name := range h {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-codex-") && !seenHeader[lower] {
				seenHeader[lower] = true
				counterHits++
				add(AzureVerifyEvidenceNonAzure, "响应头包含 ChatGPT 订阅（Codex）特有字段 "+lower, h.Get(name))
			}
			if strings.HasPrefix(lower, "x-new-api-") || strings.HasPrefix(lower, "x-oneapi-") {
				newAPI = true
			}
		}
		for _, id := range h.Values("x-request-id") {
			requestIDs[id] = true
		}
	}

	if obs.ChatFilterFields {
		azureHits++
		add(AzureVerifyEvidenceAzure, "非流式响应体包含 Azure 内容过滤字段（prompt_filter_results / content_filter_results）", "")
	}
	if obs.StreamAzureChunk {
		azureHits++
		add(AzureVerifyEvidenceAzure, "流式响应的第一个块是 Azure 特有的内容过滤空块", "")
	} else if obs.StreamFilterField {
		azureHits++
		add(AzureVerifyEvidenceAzure, "流式响应包含 Azure 内容过滤字段", "")
	}

	for _, msg := range obs.Errors {
		for _, p := range azureVerifyAzureErrorPatterns {
			if p.re.MatchString(msg) {
				azureHits++
				add(AzureVerifyEvidenceAzure, p.detail, azureVerifyTruncate(msg))
			}
		}
		for _, p := range azureVerifyNonAzureErrorPatterns {
			if p.re.MatchString(msg) {
				counterHits++
				add(AzureVerifyEvidenceNonAzure, p.detail, azureVerifyTruncate(msg))
			}
		}
	}

	if obs.ChatOK {
		if obs.ChatOutputTokens > azureVerifyMaxCompletionToks {
			counterHits++
			add(AzureVerifyEvidenceNonAzure,
				fmt.Sprintf("长度限制未生效：要求最多 %d 个 token，实际输出 %d 个", azureVerifyMaxCompletionToks, obs.ChatOutputTokens),
				"finish_reason="+obs.ChatFinishReason)
		} else {
			add(AzureVerifyEvidenceInfo,
				fmt.Sprintf("长度限制生效：输出 %d 个 token", obs.ChatOutputTokens),
				"finish_reason="+obs.ChatFinishReason)
		}
		if obs.ChatPromptTokens > azureVerifyInjectedPromptTokens {
			counterHits++
			add(AzureVerifyEvidenceNonAzure,
				fmt.Sprintf("疑似注入了提示词：一句话的提示词被计为 %d 个输入 token", obs.ChatPromptTokens), "")
		} else if obs.ChatPromptTokens > 0 {
			add(AzureVerifyEvidenceInfo, fmt.Sprintf("输入 token 数正常（%d）", obs.ChatPromptTokens), "")
		}
		if obs.ChatModel != "" && obs.ChatModel != obs.Model {
			detail := "返回的 model 字段与请求不同"
			if azureVerifyDatedModelPattern.MatchString(obs.ChatModel) {
				detail = "返回了带日期的部署版本"
				if v.ServedModel == "" {
					v.ServedModel = obs.ChatModel
				}
			}
			add(AzureVerifyEvidenceInfo, detail, obs.ChatModel)
		}
	}

	if obs.ContinuationTried {
		if obs.ContinuationOK {
			add(AzureVerifyEvidenceInfo, "Responses API 的 previous_response_id 续接成功", "")
		} else {
			add(AzureVerifyEvidenceInfo, "Responses API 的 previous_response_id 续接未成功", "")
		}
	}
	if newAPI {
		add(AzureVerifyEvidenceInfo, "响应头显示经过 new-api / one-api 中转", "")
	}
	if len(requestIDs) > 1 {
		add(AzureVerifyEvidenceInfo, fmt.Sprintf("单个模型的请求中出现 %d 个不同的 x-request-id，可能套了多层中转", len(requestIDs)), "")
	}

	anySuccess := obs.ChatOK || obs.StreamOK || obs.ContinuationOK
	if !anySuccess && azureHits == 0 && counterHits == 0 {
		v.Status = AzureVerifyStatusFailed
		for _, msg := range obs.Errors {
			if azureVerifyModelUnavailablePattern.MatchString(msg) {
				v.ModelUnavailable = true
				break
			}
		}
		return v
	}
	v.Status = AzureVerifyStatusOK
	v.IsAzure = azureHits > 0 && counterHits == 0
	v.Suspicious = azureHits > 0 && counterHits > 0
	return v
}

func azureVerifyEndpoint(baseURL, path string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/v1")
	return base + path
}

func azureVerifyReportedHeaders(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for name, values := range h {
		lower := strings.ToLower(name)
		for _, prefix := range azureVerifyReportedHeaderPrefixes {
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

func azureVerifyErrorText(status int, body []byte) string {
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
	return azureVerifyTruncate(msg)
}

func azureVerifyResponsesText(body []byte) string {
	var sb strings.Builder
	gjson.GetBytes(body, "output").ForEach(func(_, item gjson.Result) bool {
		item.Get("content").ForEach(func(_, c gjson.Result) bool {
			sb.WriteString(c.Get("text").String())
			return true
		})
		return true
	})
	if sb.Len() == 0 {
		sb.WriteString(gjson.GetBytes(body, "output_text").String())
	}
	return sb.String()
}

func azureVerifyTruncate(s string) string {
	r := []rune(s)
	if len(r) > azureVerifyMaxErrorChars {
		return string(r[:azureVerifyMaxErrorChars])
	}
	return s
}

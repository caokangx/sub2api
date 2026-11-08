package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	azureVerifyMaxStreamChunks   = 3
	azureVerifyMaxCompletionToks = 64
	azureVerifyRememberPrompt    = "记住数字 4729，只回复OK"
	azureVerifyRecallPrompt      = "我刚才让你记住的数字是多少？只回复数字"
	azureVerifyRememberedNumber  = "4729"

	// The prompt-cache probe sends one ~500-token block (below the 1024-token caching
	// threshold, so it can never be a cache write), then the same block repeated, twice.
	azureVerifyCacheBlockLines = 25
	azureVerifyCacheRepeat     = 3
	// The repeated prompt shares the chat-template overhead once, so it comes in a few
	// percent under repeat × block; relays that bill cache writes as inflated input land far above.
	azureVerifyCacheMinRatio = 0.9
	azureVerifyCacheMaxRatio = 1.05
	// OpenAI / Azure report cache hits in 128-token increments.
	azureVerifyCacheHitGranularity = 128
)

const (
	AzureVerifyStatusOK     = UpstreamVerifyStatusOK
	AzureVerifyStatusFailed = UpstreamVerifyStatusFailed

	AzureVerifyEvidenceAzure    = "azure"
	AzureVerifyEvidenceNonAzure = "non_azure"
	// AzureVerifyEvidenceTamper marks relay misbehavior (dropped parameters, injected
	// prompts, rewritten responses or usage) that says nothing about the backend itself.
	AzureVerifyEvidenceTamper = "relay_tamper"
	AzureVerifyEvidenceInfo   = UpstreamVerifyEvidenceInfo

	AzureVerifyStepChat        = "chat_completions"
	AzureVerifyStepStream      = "chat_completions_stream"
	AzureVerifyStepResponses   = "responses_continuation"
	AzureVerifyStepPromptCache = "prompt_cache"
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

var azureVerifyDatedModelPattern = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

var azureVerifyChatIDPattern = regexp.MustCompile(`^chatcmpl-[A-Za-z0-9]{29}$`)

// AzureVerifyService probes an admin-supplied OpenAI-compatible relay and judges
// whether the models behind it are served by Azure OpenAI.
type AzureVerifyService struct {
	client *upstreamVerifyClient
}

func NewAzureVerifyService(upstream HTTPUpstream) *AzureVerifyService {
	return &AzureVerifyService{client: &upstreamVerifyClient{
		upstream: upstream,
		setAuth: func(h http.Header, apiKey string) {
			h.Set("Authorization", "Bearer "+apiKey)
		},
		reportedHeaderPrefixes: azureVerifyReportedHeaderPrefixes,
	}}
}

type AzureVerifyModelsResult = UpstreamVerifyModelsResult

type AzureVerifyEvidence = UpstreamVerifyEvidence

type AzureVerifyStep = UpstreamVerifyStep

type AzureVerifyProbeResult struct {
	Model            string                `json:"model"`
	Status           string                `json:"status"`
	IsAzure          bool                  `json:"is_azure"`
	Suspicious       bool                  `json:"suspicious"`
	RelayTampered    bool                  `json:"relay_tampered"`
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
	ChatID           string

	StreamOK          bool
	StreamAzureChunk  bool
	StreamFilterField bool
	StreamChunkID     string

	ContinuationTried bool
	ContinuationOK    bool

	// CacheBaseTokens is prompt_tokens for the single block; CacheLongTokens and
	// CacheReadTokens hold prompt_tokens / cached_tokens for each repeated-block call.
	CacheBaseTokens int64
	CacheLongTokens []int64
	CacheReadTokens []int64
}

type azureVerdict struct {
	Status           string
	IsAzure          bool
	Suspicious       bool
	RelayTampered    bool
	ModelUnavailable bool
	ServedModel      string
	Evidences        []AzureVerifyEvidence
}

func (s *AzureVerifyService) ListModels(ctx context.Context, baseURL, apiKey string) AzureVerifyModelsResult {
	return s.client.listModels(ctx, baseURL, apiKey)
}

func (s *AzureVerifyService) ProbeModel(ctx context.Context, baseURL, apiKey, model string) AzureVerifyProbeResult {
	obs := azureVerifyObservation{Model: model}
	steps := make([]AzureVerifyStep, 0, 4)

	// The cache probe is three requests on its own; run it alongside the others.
	cacheObs := azureVerifyObservation{Model: model}
	cacheStep := make(chan AzureVerifyStep, 1)
	go func() { cacheStep <- s.probePromptCache(ctx, baseURL, apiKey, &cacheObs) }()

	steps = append(steps, s.probeChat(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeStream(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeContinuation(ctx, baseURL, apiKey, &obs))
	steps = append(steps, <-cacheStep)
	obs.Headers = append(obs.Headers, cacheObs.Headers...)
	obs.Errors = append(obs.Errors, cacheObs.Errors...)
	obs.CacheBaseTokens = cacheObs.CacheBaseTokens
	obs.CacheLongTokens = cacheObs.CacheLongTokens
	obs.CacheReadTokens = cacheObs.CacheReadTokens

	verdict := evaluateAzureEvidence(obs)
	out := AzureVerifyProbeResult{
		Model:            model,
		Status:           verdict.Status,
		IsAzure:          verdict.IsAzure,
		Suspicious:       verdict.Suspicious,
		RelayTampered:    verdict.RelayTampered,
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
		"messages":              []map[string]string{{"role": "user", "content": upstreamVerifyProbePrompt}},
	})
	res := s.client.do(ctx, http.MethodPost, upstreamVerifyEndpoint(baseURL, "/v1/chat/completions"), apiKey, body)
	step := s.client.newStep(AzureVerifyStepChat, res)
	if !recordAzureVerifyResult(obs, &step, res) {
		return step
	}
	if !gjson.ValidBytes(res.body) || gjson.GetBytes(res.body, "error").Exists() {
		step.OK = false
		step.Error = upstreamVerifyErrorText(res.status, res.body)
		obs.Errors = append(obs.Errors, step.Error)
		return step
	}
	obs.ChatOK = true
	obs.ChatFinishReason = gjson.GetBytes(res.body, "choices.0.finish_reason").String()
	obs.ChatPromptTokens = gjson.GetBytes(res.body, "usage.prompt_tokens").Int()
	obs.ChatOutputTokens = gjson.GetBytes(res.body, "usage.completion_tokens").Int()
	obs.ChatModel = gjson.GetBytes(res.body, "model").String()
	obs.ChatID = gjson.GetBytes(res.body, "id").String()
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
	ctx, cancel := context.WithTimeout(ctx, upstreamVerifyStepTimeout)
	defer cancel()

	start := time.Now()
	resp, err := s.client.open(ctx, http.MethodPost, upstreamVerifyEndpoint(baseURL, "/v1/chat/completions"), apiKey, body)
	if err != nil {
		msg := "request failed: " + err.Error()
		obs.Errors = append(obs.Errors, msg)
		return AzureVerifyStep{Name: AzureVerifyStepStream, DurationMs: time.Since(start).Milliseconds(), Error: msg}
	}
	defer func() { _ = resp.Body.Close() }()

	step := AzureVerifyStep{Name: AzureVerifyStepStream, Status: resp.StatusCode, Headers: s.client.reportedHeaders(resp.Header)}
	obs.Headers = append(obs.Headers, resp.Header)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, upstreamVerifyMaxErrorChars*4))
		step.Error = upstreamVerifyErrorText(resp.StatusCode, raw)
		step.DurationMs = time.Since(start).Milliseconds()
		obs.Errors = append(obs.Errors, step.Error)
		return step
	}

	scanner := bufio.NewScanner(io.LimitReader(resp.Body, upstreamVerifyMaxStreamBytes))
	scanner.Buffer(make([]byte, 0, 16<<10), upstreamVerifyMaxStreamBytes)
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
			step.Error = upstreamVerifyTruncate(payload)
			obs.Errors = append(obs.Errors, step.Error)
			break
		}
		if gjson.Get(payload, "prompt_filter_results").Exists() || gjson.Get(payload, "choices.0.content_filter_results").Exists() {
			obs.StreamFilterField = true
		}
		chunkID := gjson.Get(payload, "id").String()
		if chunks == 1 && chunkID == "" &&
			len(gjson.Get(payload, "choices").Array()) == 0 &&
			gjson.Get(payload, "prompt_filter_results").Exists() {
			obs.StreamAzureChunk = true
		}
		if obs.StreamChunkID == "" {
			obs.StreamChunkID = chunkID
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
	endpoint := upstreamVerifyEndpoint(baseURL, "/v1/responses")
	first, _ := json.Marshal(map[string]any{"model": obs.Model, "store": true, "input": azureVerifyRememberPrompt})
	res := s.client.do(ctx, http.MethodPost, endpoint, apiKey, first)
	step := s.client.newStep(AzureVerifyStepResponses, res)
	if !recordAzureVerifyResult(obs, &step, res) {
		return step
	}
	responseID := gjson.GetBytes(res.body, "id").String()
	if responseID == "" {
		step.OK = false
		step.Error = upstreamVerifyErrorText(res.status, res.body)
		obs.Errors = append(obs.Errors, step.Error)
		return step
	}

	second, _ := json.Marshal(map[string]any{
		"model":                obs.Model,
		"previous_response_id": responseID,
		"input":                azureVerifyRecallPrompt,
	})
	res2 := s.client.do(ctx, http.MethodPost, endpoint, apiKey, second)
	step.Status = res2.status
	step.DurationMs += res2.duration.Milliseconds()
	step.Headers = mergeUpstreamVerifyHeaders(step.Headers, s.client.reportedHeaders(res2.headers))
	if !recordAzureVerifyResult(obs, &step, res2) {
		return step
	}
	if !strings.Contains(azureVerifyResponsesText(res2.body), azureVerifyRememberedNumber) {
		step.OK = false
		step.Error = "previous_response_id did not carry the earlier turn: " + upstreamVerifyTruncate(string(res2.body))
		return step
	}
	obs.ContinuationOK = true
	return step
}

// probePromptCache checks that usage is reported honestly around prompt caching: the
// repeated prompt must cost about repeat × the single block, and hits must be 128-aligned.
func (s *AzureVerifyService) probePromptCache(ctx context.Context, baseURL, apiKey string, obs *azureVerifyObservation) AzureVerifyStep {
	// A fresh nonce at the head of the prompt guarantees the first long call is a cache miss.
	nonce := fmt.Sprintf("probe-%d", time.Now().UnixNano())
	var block strings.Builder
	for i := 0; i < azureVerifyCacheBlockLines; i++ {
		fmt.Fprintf(&block, "Rule %d: The system shall process record %d with checksum %d and log it.\n", i, i, i*7919%100003)
	}

	endpoint := upstreamVerifyEndpoint(baseURL, "/v1/chat/completions")
	step := AzureVerifyStep{Name: AzureVerifyStepPromptCache}
	for i, repeat := range []int{1, azureVerifyCacheRepeat, azureVerifyCacheRepeat} {
		body, _ := json.Marshal(map[string]any{
			"model":                 obs.Model,
			"max_completion_tokens": 16,
			"prompt_cache_key":      nonce,
			"messages": []map[string]string{
				{"role": "system", "content": nonce + "\n" + strings.Repeat(block.String(), repeat)},
				{"role": "user", "content": "Reply with OK."},
			},
		})
		res := s.client.do(ctx, http.MethodPost, endpoint, apiKey, body)
		step.Status = res.status
		step.DurationMs += res.duration.Milliseconds()
		step.Headers = mergeUpstreamVerifyHeaders(step.Headers, s.client.reportedHeaders(res.headers))
		if !recordAzureVerifyResult(obs, &step, res) {
			return step
		}
		usage := gjson.GetBytes(res.body, "usage")
		promptTokens := usage.Get("prompt_tokens").Int()
		if promptTokens <= 0 {
			step.OK = false
			step.Error = "response carried no usage.prompt_tokens: " + upstreamVerifyTruncate(string(res.body))
			return step
		}
		if i == 0 {
			obs.CacheBaseTokens = promptTokens
			continue
		}
		obs.CacheLongTokens = append(obs.CacheLongTokens, promptTokens)
		obs.CacheReadTokens = append(obs.CacheReadTokens, usage.Get("prompt_tokens_details.cached_tokens").Int())
	}
	return step
}

// recordAzureVerifyResult folds one HTTP exchange into the observation and reports
// whether the exchange succeeded at the HTTP level.
func recordAzureVerifyResult(obs *azureVerifyObservation, step *AzureVerifyStep, res upstreamVerifyHTTPResult) bool {
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
		step.Error = upstreamVerifyErrorText(res.status, res.body)
		obs.Errors = append(obs.Errors, step.Error)
		return false
	}
	step.OK = true
	return true
}

func evaluateAzureEvidence(obs azureVerifyObservation) azureVerdict {
	var v azureVerdict
	var ev upstreamVerifyEvidenceList
	// Headers are one nginx line away from being forged, so they only count as weak
	// evidence; body-level markers and Azure-specific errors are strong evidence.
	strongHits, headerHits, counterHits, tamperHits := 0, 0, 0, 0

	seenHeader := map[string]bool{}
	maxRequestIDs := 0
	for _, h := range obs.Headers {
		for _, name := range azureVerifyAzureHeaders {
			if val := strings.TrimSpace(h.Get(name)); val != "" && !seenHeader[name] {
				seenHeader[name] = true
				headerHits++
				ev.add(AzureVerifyEvidenceAzure, "响应头包含 Azure 特有字段 "+name, val)
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
				ev.add(AzureVerifyEvidenceNonAzure, "响应头包含 ChatGPT 订阅（Codex）特有字段 "+lower, h.Get(name))
			}
		}
		maxRequestIDs = max(maxRequestIDs, len(h.Values("x-request-id")))
	}

	if obs.ChatFilterFields {
		strongHits++
		ev.add(AzureVerifyEvidenceAzure, "非流式响应体包含 Azure 内容过滤字段（prompt_filter_results / content_filter_results）", "")
	}
	if obs.StreamAzureChunk {
		strongHits++
		ev.add(AzureVerifyEvidenceAzure, "流式响应的第一个块是 Azure 特有的内容过滤空块", "")
	} else if obs.StreamFilterField {
		strongHits++
		ev.add(AzureVerifyEvidenceAzure, "流式响应包含 Azure 内容过滤字段", "")
	}

	for _, msg := range obs.Errors {
		for _, p := range azureVerifyAzureErrorPatterns {
			if p.re.MatchString(msg) {
				strongHits++
				ev.add(AzureVerifyEvidenceAzure, p.detail, upstreamVerifyTruncate(msg))
			}
		}
		for _, p := range azureVerifyNonAzureErrorPatterns {
			if p.re.MatchString(msg) {
				counterHits++
				ev.add(AzureVerifyEvidenceNonAzure, p.detail, upstreamVerifyTruncate(msg))
			}
		}
	}

	if headerHits > 0 && strongHits == 0 && (obs.ChatOK || obs.StreamOK) {
		counterHits++
		ev.add(AzureVerifyEvidenceNonAzure, "响应头带有 Azure 字段，但响应体里没有 Azure 内容过滤字段：响应头可能是伪造的，或者响应体被中转站重写过", "")
	}

	// A ChatGPT-subscription backend ignores the length limit and carries a large system
	// prompt; once the body itself proves Azure, the same symptoms can only come from the relay.
	behaviorKind := AzureVerifyEvidenceNonAzure
	behaviorHits := &counterHits
	if strongHits > 0 {
		behaviorKind = AzureVerifyEvidenceTamper
		behaviorHits = &tamperHits
	}

	if obs.ChatOK {
		if obs.ChatOutputTokens > azureVerifyMaxCompletionToks {
			*behaviorHits++
			ev.add(behaviorKind,
				fmt.Sprintf("长度限制未生效：要求最多 %d 个 token，实际输出 %d 个", azureVerifyMaxCompletionToks, obs.ChatOutputTokens),
				"finish_reason="+obs.ChatFinishReason)
		} else {
			ev.add(AzureVerifyEvidenceInfo,
				fmt.Sprintf("长度限制生效：输出 %d 个 token", obs.ChatOutputTokens),
				"finish_reason="+obs.ChatFinishReason)
		}
		if obs.ChatPromptTokens > upstreamVerifyInjectedPromptTokens {
			*behaviorHits++
			ev.add(behaviorKind,
				fmt.Sprintf("疑似注入了提示词：一句话的提示词被计为 %d 个输入 token", obs.ChatPromptTokens), "")
		} else if obs.ChatPromptTokens > 0 {
			ev.add(AzureVerifyEvidenceInfo, fmt.Sprintf("输入 token 数正常（%d）", obs.ChatPromptTokens), "")
		}
		if obs.ChatModel != "" && obs.ChatModel != obs.Model {
			detail := "返回的 model 字段与请求不同"
			if azureVerifyDatedModelPattern.MatchString(obs.ChatModel) {
				detail = "返回了带日期的部署版本"
				if v.ServedModel == "" {
					v.ServedModel = obs.ChatModel
				}
			}
			ev.add(AzureVerifyEvidenceInfo, detail, obs.ChatModel)
		}
		if obs.ChatID != "" && !azureVerifyChatIDPattern.MatchString(obs.ChatID) {
			tamperHits++
			ev.add(AzureVerifyEvidenceTamper, "非流式响应的 id 不是官方格式（chatcmpl- 加 29 位），响应可能由中转站重新生成", obs.ChatID)
		}
	}
	if obs.StreamOK && obs.StreamChunkID != "" && !azureVerifyChatIDPattern.MatchString(obs.StreamChunkID) {
		tamperHits++
		ev.add(AzureVerifyEvidenceTamper, "流式响应的 id 不是官方格式（chatcmpl- 加 29 位），流式响应可能由中转站重新生成", obs.StreamChunkID)
	}

	tamperHits += evaluateAzurePromptCache(obs, &ev)

	if obs.ContinuationTried {
		if obs.ContinuationOK {
			ev.add(AzureVerifyEvidenceInfo, "Responses API 的 previous_response_id 续接成功", "")
		} else {
			ev.add(AzureVerifyEvidenceInfo, "Responses API 的 previous_response_id 续接未成功", "")
		}
	}
	if upstreamVerifyHasNewAPIHeaders(obs.Headers) {
		ev.add(AzureVerifyEvidenceInfo, "响应头显示经过 new-api / one-api 中转", "")
	}
	if maxRequestIDs > 1 {
		ev.add(AzureVerifyEvidenceInfo, fmt.Sprintf("同一个响应带有 %d 个 x-request-id，可能套了多层中转", maxRequestIDs), "")
	}
	if headerHits > 0 && strongHits == 0 && counterHits == 0 {
		ev.add(AzureVerifyEvidenceInfo, "只有 Azure 响应头这类容易伪造的证据，不足以确认是 Azure", "")
	}

	v.Evidences = ev
	azureHits := strongHits + headerHits
	anySuccess := obs.ChatOK || obs.StreamOK || obs.ContinuationOK || obs.CacheBaseTokens > 0
	if !anySuccess && azureHits == 0 && counterHits == 0 {
		v.Status = AzureVerifyStatusFailed
		v.ModelUnavailable = upstreamVerifyModelUnavailable(obs.Errors)
		return v
	}
	v.Status = AzureVerifyStatusOK
	v.IsAzure = strongHits > 0 && counterHits == 0
	v.Suspicious = azureHits > 0 && counterHits > 0
	v.RelayTampered = tamperHits > 0
	return v
}

// evaluateAzurePromptCache adds the prompt-cache findings to ev and returns how many
// relay-tamper signals it found.
func evaluateAzurePromptCache(obs azureVerifyObservation, ev *upstreamVerifyEvidenceList) int {
	if obs.CacheBaseTokens <= 0 || len(obs.CacheLongTokens) == 0 {
		return 0
	}
	hits := 0
	expected := float64(obs.CacheBaseTokens * azureVerifyCacheRepeat)
	for _, n := range obs.CacheLongTokens {
		if float64(n) < expected*azureVerifyCacheMinRatio || float64(n) > expected*azureVerifyCacheMaxRatio {
			hits++
			ev.add(AzureVerifyEvidenceTamper,
				fmt.Sprintf("输入 token 数不可信：同一段内容重复 %d 遍应约为单遍（%d）的 %d 倍，实际报告 %d",
					azureVerifyCacheRepeat, obs.CacheBaseTokens, azureVerifyCacheRepeat, n),
				fmt.Sprintf("prompt_tokens=%v", obs.CacheLongTokens))
			break
		}
	}
	if len(obs.CacheLongTokens) == 2 && obs.CacheLongTokens[0] != obs.CacheLongTokens[1] && hits == 0 {
		hits++
		ev.add(AzureVerifyEvidenceTamper,
			fmt.Sprintf("同一个请求发两次，报告的输入 token 数不同（%d / %d）", obs.CacheLongTokens[0], obs.CacheLongTokens[1]), "")
	}

	var hit int64
	for i, cached := range obs.CacheReadTokens {
		if cached <= 0 {
			continue
		}
		if cached%azureVerifyCacheHitGranularity != 0 || cached > obs.CacheLongTokens[i] {
			hits++
			ev.add(AzureVerifyEvidenceTamper,
				fmt.Sprintf("缓存命中数 %d 不是 %d 的倍数或超过了输入总数，usage 可能被改写", cached, azureVerifyCacheHitGranularity),
				fmt.Sprintf("prompt_tokens=%d", obs.CacheLongTokens[i]))
			return hits
		}
		hit = cached
	}
	if hit > 0 {
		ev.add(AzureVerifyEvidenceInfo, fmt.Sprintf("提示词缓存命中 %d 个 token", hit), "")
	} else {
		ev.add(AzureVerifyEvidenceInfo, "提示词缓存未命中", "")
	}
	return hits
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

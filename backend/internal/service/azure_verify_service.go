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
)

const (
	AzureVerifyStatusOK     = UpstreamVerifyStatusOK
	AzureVerifyStatusFailed = UpstreamVerifyStatusFailed

	AzureVerifyEvidenceAzure    = "azure"
	AzureVerifyEvidenceNonAzure = "non_azure"
	AzureVerifyEvidenceInfo     = UpstreamVerifyEvidenceInfo

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

var azureVerifyDatedModelPattern = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

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

func (s *AzureVerifyService) ListModels(ctx context.Context, baseURL, apiKey string) AzureVerifyModelsResult {
	return s.client.listModels(ctx, baseURL, apiKey)
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
	azureHits, counterHits := 0, 0

	seenHeader := map[string]bool{}
	requestIDs := map[string]bool{}
	for _, h := range obs.Headers {
		for _, name := range azureVerifyAzureHeaders {
			if val := strings.TrimSpace(h.Get(name)); val != "" && !seenHeader[name] {
				seenHeader[name] = true
				azureHits++
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
		for _, id := range h.Values("x-request-id") {
			requestIDs[id] = true
		}
	}

	if obs.ChatFilterFields {
		azureHits++
		ev.add(AzureVerifyEvidenceAzure, "非流式响应体包含 Azure 内容过滤字段（prompt_filter_results / content_filter_results）", "")
	}
	if obs.StreamAzureChunk {
		azureHits++
		ev.add(AzureVerifyEvidenceAzure, "流式响应的第一个块是 Azure 特有的内容过滤空块", "")
	} else if obs.StreamFilterField {
		azureHits++
		ev.add(AzureVerifyEvidenceAzure, "流式响应包含 Azure 内容过滤字段", "")
	}

	for _, msg := range obs.Errors {
		for _, p := range azureVerifyAzureErrorPatterns {
			if p.re.MatchString(msg) {
				azureHits++
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

	if obs.ChatOK {
		if obs.ChatOutputTokens > azureVerifyMaxCompletionToks {
			counterHits++
			ev.add(AzureVerifyEvidenceNonAzure,
				fmt.Sprintf("长度限制未生效：要求最多 %d 个 token，实际输出 %d 个", azureVerifyMaxCompletionToks, obs.ChatOutputTokens),
				"finish_reason="+obs.ChatFinishReason)
		} else {
			ev.add(AzureVerifyEvidenceInfo,
				fmt.Sprintf("长度限制生效：输出 %d 个 token", obs.ChatOutputTokens),
				"finish_reason="+obs.ChatFinishReason)
		}
		if obs.ChatPromptTokens > upstreamVerifyInjectedPromptTokens {
			counterHits++
			ev.add(AzureVerifyEvidenceNonAzure,
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
	}

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
	if len(requestIDs) > 1 {
		ev.add(AzureVerifyEvidenceInfo, fmt.Sprintf("单个模型的请求中出现 %d 个不同的 x-request-id，可能套了多层中转", len(requestIDs)), "")
	}

	v.Evidences = ev
	anySuccess := obs.ChatOK || obs.StreamOK || obs.ContinuationOK
	if !anySuccess && azureHits == 0 && counterHits == 0 {
		v.Status = AzureVerifyStatusFailed
		v.ModelUnavailable = upstreamVerifyModelUnavailable(obs.Errors)
		return v
	}
	v.Status = AzureVerifyStatusOK
	v.IsAzure = azureHits > 0 && counterHits == 0
	v.Suspicious = azureHits > 0 && counterHits > 0
	return v
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

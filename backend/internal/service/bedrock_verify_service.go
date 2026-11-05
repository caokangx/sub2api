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
	bedrockVerifyMaxTokens       = 16
	bedrockVerifyToolMaxTokens   = 256
	bedrockVerifyMaxStreamEvents = 10
	bedrockVerifyAnthropicVer    = "2023-06-01"
	bedrockVerifyToolName        = "get_weather"
	bedrockVerifyToolPrompt      = "北京天气怎么样？请调用 get_weather 工具查询"
)

const (
	BedrockVerifyEvidenceBedrock    = "bedrock"
	BedrockVerifyEvidenceNonBedrock = "non_bedrock"

	BedrockVerifyStepMessages = "messages"
	BedrockVerifyStepToolUse  = "tool_use"
	BedrockVerifyStepStream   = "messages_stream"

	BedrockVerifySourceBedrock      = "bedrock"
	BedrockVerifySourceVertex       = "vertex"
	BedrockVerifySourceAnthropic    = "anthropic"
	BedrockVerifySourceSubscription = "subscription"
	BedrockVerifySourceUnknown      = "unknown"

	BedrockVerifyNoteForcedToolUnsupported = "forced_tool_choice_unsupported"
)

var bedrockVerifyReportedHeaderPrefixes = []string{
	"x-amzn-",
	"request-id",
	"x-request-id",
	"anthropic-",
	"x-new-api-",
	"x-oneapi-",
	"server",
}

// Checked in order; the first match per error message is recorded so one AWS
// error does not turn into four evidence rows.
var bedrockVerifyBedrockErrorPatterns = []struct {
	re     *regexp.Regexp
	detail string
}{
	{regexp.MustCompile(`Bedrock Runtime`), "报错来自 AWS Bedrock Runtime"},
	{regexp.MustCompile(`\bInvokeModel(WithResponseStream)?\b`), "报错提到 Bedrock 的 InvokeModel 接口"},
	{regexp.MustCompile(`aws_invoke_error`), "报错类型为 aws_invoke_error"},
	{regexp.MustCompile(`\b(ValidationException|ThrottlingException|ServiceUnavailableException|ModelErrorException|ModelTimeoutException|ModelNotReadyException|AccessDeniedException)\b`), "报错为 AWS 风格的异常名"},
	{regexp.MustCompile(`Too many tokens, please wait before trying again`), "报错为 Bedrock 的限流提示"},
}

var bedrockVerifySubscriptionErrorPattern = regexp.MustCompile(`(?i)only authorized for use with claude code|oauth authentication is currently not supported`)

var bedrockVerifyForcedToolRejectedPattern = regexp.MustCompile(`(?i)tool_choice`)

// BedrockVerifyService probes an admin-supplied Anthropic-compatible relay and
// judges whether the Claude models behind it are served by AWS Bedrock.
type BedrockVerifyService struct {
	client *upstreamVerifyClient
}

func NewBedrockVerifyService(upstream HTTPUpstream) *BedrockVerifyService {
	return &BedrockVerifyService{client: &upstreamVerifyClient{
		upstream: upstream,
		// new-api accepts either header; sub2api only reads x-api-key.
		setAuth: func(h http.Header, apiKey string) {
			h.Set("x-api-key", apiKey)
			h.Set("Authorization", "Bearer "+apiKey)
			h.Set("anthropic-version", bedrockVerifyAnthropicVer)
		},
		reportedHeaderPrefixes: bedrockVerifyReportedHeaderPrefixes,
	}}
}

type BedrockVerifyProbeResult struct {
	Model            string                   `json:"model"`
	Status           string                   `json:"status"`
	IsBedrock        bool                     `json:"is_bedrock"`
	Suspicious       bool                     `json:"suspicious"`
	ModelUnavailable bool                     `json:"model_unavailable"`
	DetectedSource   string                   `json:"detected_source"`
	Notes            []string                 `json:"notes"`
	Evidences        []UpstreamVerifyEvidence `json:"evidences"`
	Steps            []UpstreamVerifyStep     `json:"steps"`
	Error            string                   `json:"error,omitempty"`
}

// bedrockVerifyObservation is everything the probe requests saw; evaluateBedrockEvidence
// derives the verdict from it without doing any I/O.
type bedrockVerifyObservation struct {
	Model   string
	Headers []http.Header
	Errors  []string

	MessageOK   bool
	InputTokens int64
	StreamOK    bool
	ToolOK      bool

	MessageIDs []string
	ToolUseIDs []string

	ForcedToolRejected bool
}

type bedrockVerdict struct {
	Status           string
	IsBedrock        bool
	Suspicious       bool
	ModelUnavailable bool
	DetectedSource   string
	Notes            []string
	Evidences        []UpstreamVerifyEvidence
}

func (s *BedrockVerifyService) ListModels(ctx context.Context, baseURL, apiKey string) UpstreamVerifyModelsResult {
	return s.client.listModels(ctx, baseURL, apiKey)
}

func (s *BedrockVerifyService) ProbeModel(ctx context.Context, baseURL, apiKey, model string) BedrockVerifyProbeResult {
	obs := bedrockVerifyObservation{Model: model}
	steps := make([]UpstreamVerifyStep, 0, 3)

	steps = append(steps, s.probeMessage(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeToolUse(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeStream(ctx, baseURL, apiKey, &obs))

	verdict := evaluateBedrockEvidence(obs)
	out := BedrockVerifyProbeResult{
		Model:            model,
		Status:           verdict.Status,
		IsBedrock:        verdict.IsBedrock,
		Suspicious:       verdict.Suspicious,
		ModelUnavailable: verdict.ModelUnavailable,
		DetectedSource:   verdict.DetectedSource,
		Notes:            verdict.Notes,
		Evidences:        verdict.Evidences,
		Steps:            steps,
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	if verdict.Status == UpstreamVerifyStatusFailed && len(obs.Errors) > 0 {
		out.Error = obs.Errors[0]
	}
	return out
}

func (s *BedrockVerifyService) probeMessage(ctx context.Context, baseURL, apiKey string, obs *bedrockVerifyObservation) UpstreamVerifyStep {
	body, _ := json.Marshal(map[string]any{
		"model":      obs.Model,
		"max_tokens": bedrockVerifyMaxTokens,
		"messages":   []map[string]string{{"role": "user", "content": upstreamVerifyProbePrompt}},
	})
	res := s.client.do(ctx, http.MethodPost, upstreamVerifyEndpoint(baseURL, "/v1/messages"), apiKey, body)
	step := s.client.newStep(BedrockVerifyStepMessages, res)
	if !recordBedrockVerifyResult(obs, &step, res) {
		return step
	}
	obs.MessageOK = true
	obs.InputTokens = gjson.GetBytes(res.body, "usage.input_tokens").Int()
	if id := gjson.GetBytes(res.body, "id").String(); id != "" {
		obs.MessageIDs = append(obs.MessageIDs, id)
	}
	return step
}

func (s *BedrockVerifyService) probeToolUse(ctx context.Context, baseURL, apiKey string, obs *bedrockVerifyObservation) UpstreamVerifyStep {
	endpoint := upstreamVerifyEndpoint(baseURL, "/v1/messages")
	request := func(toolChoice map[string]any) []byte {
		body, _ := json.Marshal(map[string]any{
			"model":      obs.Model,
			"max_tokens": bedrockVerifyToolMaxTokens,
			"tools": []map[string]any{{
				"name":        bedrockVerifyToolName,
				"description": "查询城市天气",
				"input_schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"city": map[string]any{"type": "string"}},
					"required":   []string{"city"},
				},
			}},
			"tool_choice": toolChoice,
			"messages":    []map[string]string{{"role": "user", "content": bedrockVerifyToolPrompt}},
		})
		return body
	}

	res := s.client.do(ctx, http.MethodPost, endpoint, apiKey, request(map[string]any{"type": "tool", "name": bedrockVerifyToolName}))
	step := s.client.newStep(BedrockVerifyStepToolUse, res)
	if !recordBedrockVerifyResult(obs, &step, res) {
		// Newer Claude models on Bedrock reject a forced tool; "auto" still yields a tool_use id.
		if res.err != nil || res.status != http.StatusBadRequest || !bedrockVerifyForcedToolRejectedPattern.MatchString(step.Error) {
			return step
		}
		obs.ForcedToolRejected = true
		forcedErr := step.Error
		res = s.client.do(ctx, http.MethodPost, endpoint, apiKey, request(map[string]any{"type": "auto"}))
		step.Status = res.status
		step.DurationMs += res.duration.Milliseconds()
		step.Headers = mergeUpstreamVerifyHeaders(step.Headers, s.client.reportedHeaders(res.headers))
		step.Error = ""
		if !recordBedrockVerifyResult(obs, &step, res) {
			step.Error = forcedErr + "; auto retry: " + step.Error
			return step
		}
	}
	obs.ToolOK = true
	if id := gjson.GetBytes(res.body, "id").String(); id != "" {
		obs.MessageIDs = append(obs.MessageIDs, id)
	}
	gjson.GetBytes(res.body, "content").ForEach(func(_, block gjson.Result) bool {
		if block.Get("type").String() == "tool_use" {
			if id := block.Get("id").String(); id != "" {
				obs.ToolUseIDs = append(obs.ToolUseIDs, id)
			}
		}
		return true
	})
	return step
}

func (s *BedrockVerifyService) probeStream(ctx context.Context, baseURL, apiKey string, obs *bedrockVerifyObservation) UpstreamVerifyStep {
	body, _ := json.Marshal(map[string]any{
		"model":      obs.Model,
		"max_tokens": bedrockVerifyMaxTokens,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "say hi"}},
	})
	ctx, cancel := context.WithTimeout(ctx, upstreamVerifyStepTimeout)
	defer cancel()

	start := time.Now()
	resp, err := s.client.open(ctx, http.MethodPost, upstreamVerifyEndpoint(baseURL, "/v1/messages"), apiKey, body)
	if err != nil {
		msg := "request failed: " + err.Error()
		obs.Errors = append(obs.Errors, msg)
		return UpstreamVerifyStep{Name: BedrockVerifyStepStream, DurationMs: time.Since(start).Milliseconds(), Error: msg}
	}
	defer func() { _ = resp.Body.Close() }()

	step := UpstreamVerifyStep{Name: BedrockVerifyStepStream, Status: resp.StatusCode, Headers: s.client.reportedHeaders(resp.Header)}
	obs.Headers = append(obs.Headers, resp.Header)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, upstreamVerifyMaxErrorChars*4))
		step.Error = bedrockVerifyErrorText(resp.StatusCode, raw)
		step.DurationMs = time.Since(start).Milliseconds()
		obs.Errors = append(obs.Errors, step.Error)
		return step
	}

	scanner := bufio.NewScanner(io.LimitReader(resp.Body, upstreamVerifyMaxStreamBytes))
	scanner.Buffer(make([]byte, 0, 16<<10), upstreamVerifyMaxStreamBytes)
	events := 0
	for scanner.Scan() && events < bedrockVerifyMaxStreamEvents {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		events++
		if gjson.Get(payload, "type").String() == "error" || gjson.Get(payload, "error").Exists() {
			step.Error = bedrockVerifyErrorText(0, []byte(payload))
			obs.Errors = append(obs.Errors, step.Error)
			break
		}
		if gjson.Get(payload, "type").String() == "message_start" {
			if id := gjson.Get(payload, "message.id").String(); id != "" {
				obs.MessageIDs = append(obs.MessageIDs, id)
			}
			step.OK = true
			obs.StreamOK = true
			break
		}
	}
	step.DurationMs = time.Since(start).Milliseconds()
	if !step.OK && step.Error == "" {
		step.Error = "stream ended before message_start"
		obs.Errors = append(obs.Errors, step.Error)
	}
	return step
}

func recordBedrockVerifyResult(obs *bedrockVerifyObservation, step *UpstreamVerifyStep, res upstreamVerifyHTTPResult) bool {
	if res.headers != nil {
		obs.Headers = append(obs.Headers, res.headers)
	}
	if res.err != nil {
		step.OK = false
		step.Error = "request failed: " + res.err.Error()
		obs.Errors = append(obs.Errors, step.Error)
		return false
	}
	if res.status != http.StatusOK || !gjson.ValidBytes(res.body) ||
		gjson.GetBytes(res.body, "type").String() == "error" || gjson.GetBytes(res.body, "error").Exists() {
		step.OK = false
		step.Error = bedrockVerifyErrorText(res.status, res.body)
		obs.Errors = append(obs.Errors, step.Error)
		return false
	}
	step.OK = true
	return true
}

// bedrockVerifyErrorText keeps the Anthropic-style error.type (e.g. aws_invoke_error),
// which often carries the only hint of the real upstream.
func bedrockVerifyErrorText(status int, body []byte) string {
	msg := upstreamVerifyErrorText(status, body)
	if typ := strings.TrimSpace(gjson.GetBytes(body, "error.type").String()); typ != "" && !strings.Contains(msg, typ) {
		msg = upstreamVerifyTruncate(msg + " (" + typ + ")")
	}
	return msg
}

func evaluateBedrockEvidence(obs bedrockVerifyObservation) bedrockVerdict {
	var v bedrockVerdict
	var ev upstreamVerifyEvidenceList
	bedrockHits, counterHits := 0, 0
	vertexSeen, anthropicSeen, subscriptionSeen := false, false, false

	classifyID := func(id, bedrockPrefix, vertexPrefix, anthropicPrefix, label string) {
		switch {
		case strings.HasPrefix(id, bedrockPrefix):
			bedrockHits++
			ev.add(BedrockVerifyEvidenceBedrock, label+"以 "+bedrockPrefix+" 开头（Bedrock 格式）", id)
		case strings.HasPrefix(id, vertexPrefix):
			counterHits++
			vertexSeen = true
			ev.add(BedrockVerifyEvidenceNonBedrock, label+"以 "+vertexPrefix+" 开头（Google Vertex 格式）", id)
		case strings.HasPrefix(id, anthropicPrefix):
			counterHits++
			anthropicSeen = true
			ev.add(BedrockVerifyEvidenceNonBedrock, label+"是 Anthropic 官方格式（"+anthropicPrefix+"...）", id)
		default:
			ev.add(UpstreamVerifyEvidenceInfo, label+"格式无法识别，可能被中转站改写", id)
		}
	}
	seenPrefix := map[string]bool{}
	for _, id := range obs.MessageIDs {
		key := "msg:" + bedrockVerifyIDKind(id)
		if seenPrefix[key] {
			continue
		}
		seenPrefix[key] = true
		classifyID(id, "msg_bdrk_", "msg_vrtx_", "msg_01", "消息 ID ")
	}
	for _, id := range obs.ToolUseIDs {
		key := "tool:" + bedrockVerifyIDKind(id)
		if seenPrefix[key] {
			continue
		}
		seenPrefix[key] = true
		classifyID(id, "toolu_bdrk_", "toolu_vrtx_", "toolu_01", "工具调用 ID ")
	}

	seenHeader := map[string]bool{}
	for _, h := range obs.Headers {
		for name := range h {
			lower := strings.ToLower(name)
			if seenHeader[lower] {
				continue
			}
			val := h.Get(name)
			switch {
			case lower == "x-amzn-requestid" || strings.HasPrefix(lower, "x-amzn-bedrock-"):
				seenHeader[lower] = true
				bedrockHits++
				ev.add(BedrockVerifyEvidenceBedrock, "响应头包含 AWS Bedrock 特有字段 "+lower, val)
			case strings.HasPrefix(lower, "anthropic-ratelimit-unified-"):
				// One row for the whole unified-* family is enough.
				if seenHeader["anthropic-ratelimit-unified-"] {
					continue
				}
				seenHeader["anthropic-ratelimit-unified-"] = true
				counterHits++
				subscriptionSeen = true
				ev.add(BedrockVerifyEvidenceNonBedrock, "响应头包含 Claude 订阅账号特有的 anthropic-ratelimit-unified-* 字段", lower+": "+val)
			case lower == "anthropic-organization-id":
				seenHeader[lower] = true
				counterHits++
				anthropicSeen = true
				ev.add(BedrockVerifyEvidenceNonBedrock, "响应头包含 Anthropic 官方 API 的 anthropic-organization-id", val)
			case lower == "request-id" && strings.HasPrefix(val, "req_"):
				seenHeader[lower] = true
				counterHits++
				anthropicSeen = true
				ev.add(BedrockVerifyEvidenceNonBedrock, "响应头 request-id 是 Anthropic 官方格式（req_...）", val)
			}
		}
	}

	for _, msg := range obs.Errors {
		for _, p := range bedrockVerifyBedrockErrorPatterns {
			if p.re.MatchString(msg) {
				bedrockHits++
				ev.add(BedrockVerifyEvidenceBedrock, p.detail, msg)
				break
			}
		}
		if bedrockVerifySubscriptionErrorPattern.MatchString(msg) {
			counterHits++
			subscriptionSeen = true
			ev.add(BedrockVerifyEvidenceNonBedrock, "报错显示上游是 Claude 订阅账号（仅限 Claude Code 使用的凭证）", msg)
		}
	}

	if obs.MessageOK {
		if obs.InputTokens > upstreamVerifyInjectedPromptTokens {
			counterHits++
			ev.add(BedrockVerifyEvidenceNonBedrock,
				fmt.Sprintf("疑似注入了提示词：一句话的提示词被计为 %d 个输入 token", obs.InputTokens), "")
		} else if obs.InputTokens > 0 {
			ev.add(UpstreamVerifyEvidenceInfo, fmt.Sprintf("输入 token 数正常（%d）", obs.InputTokens), "")
		}
	}
	if obs.ForcedToolRejected {
		v.Notes = append(v.Notes, BedrockVerifyNoteForcedToolUnsupported)
		ev.add(UpstreamVerifyEvidenceInfo, "该模型不支持强制工具调用（tool_choice 为 tool / any），已改用 auto 重试", "")
	}
	if upstreamVerifyHasNewAPIHeaders(obs.Headers) {
		ev.add(UpstreamVerifyEvidenceInfo, "响应头显示经过 new-api / one-api 中转", "")
	}

	switch {
	case vertexSeen:
		v.DetectedSource = BedrockVerifySourceVertex
	case subscriptionSeen:
		v.DetectedSource = BedrockVerifySourceSubscription
	case anthropicSeen:
		v.DetectedSource = BedrockVerifySourceAnthropic
	case bedrockHits > 0:
		v.DetectedSource = BedrockVerifySourceBedrock
	default:
		v.DetectedSource = BedrockVerifySourceUnknown
	}

	v.Evidences = ev
	anySuccess := obs.MessageOK || obs.ToolOK || obs.StreamOK
	if !anySuccess && bedrockHits == 0 && counterHits == 0 {
		v.Status = UpstreamVerifyStatusFailed
		v.ModelUnavailable = upstreamVerifyModelUnavailable(obs.Errors)
		return v
	}
	v.Status = UpstreamVerifyStatusOK
	v.IsBedrock = bedrockHits > 0 && counterHits == 0
	v.Suspicious = bedrockHits > 0 && counterHits > 0
	return v
}

// bedrockVerifyIDKind buckets an id by its provider prefix so three msg_bdrk_ ids
// produce one evidence row, not three.
func bedrockVerifyIDKind(id string) string {
	for _, p := range []string{"msg_bdrk_", "msg_vrtx_", "msg_01", "toolu_bdrk_", "toolu_vrtx_", "toolu_01"} {
		if strings.HasPrefix(id, p) {
			return p
		}
	}
	return "other"
}

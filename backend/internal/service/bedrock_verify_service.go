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
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

const (
	bedrockVerifyMaxTokens       = 16
	bedrockVerifyToolMaxTokens   = 256
	bedrockVerifyMaxStreamEvents = 200
	bedrockVerifyAnthropicVer    = "2023-06-01"
	bedrockVerifyToolName        = "get_weather"
	bedrockVerifyToolPrompt      = "北京天气怎么样？请调用 get_weather 工具查询"
	bedrockVerifyComputerTool    = "computer_20251124"
	bedrockVerifyComputerPrompt  = "Reply with the single word: pong"

	// Parameter-validation probes: requests real Bedrock must reject with a ValidationException.
	bedrockVerifyValidationMaxTokens = 8
	bedrockVerifyOverflowMaxTokens   = 999999 // above every Claude model's output limit
	bedrockVerifyUnknownEffort       = "ultra"
	bedrockVerifyThinkingBudget      = 1024 // larger than bedrockVerifyValidationMaxTokens

	bedrockVerifyTokenizerText = "The quick brown fox jumps over 13 lazy dogs on 2026-11-16 at 09:47:52.\n" +
		"人工智能模型的分词器在不同代次之间会发生变化, token 数量因此不同.\n" +
		"def fib(n):\n" +
		"    return n if n<2 else fib(n-1)+fib(n-2)  # O(2^n) naive\n" +
		"Digits: 31415926535897932384626433832795028841971693993751058209749.\n" +
		"Unicode: cafe naive resume done star -- 日本語 한국어 العربية Omega.\n" +
		"Repeat: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbbbbbbbbbbbbbbbbbbbbbbb.\n"
)

const (
	// BedrockVerifyEvidenceBedrock is behaviour a relay can hardly fake (real Bedrock
	// validation errors, AWS headers); BedrockVerifyEvidenceBedrockWeak is a Bedrock
	// format (id prefixes, error wrappers) any relay can copy.
	BedrockVerifyEvidenceBedrock     = "bedrock"
	BedrockVerifyEvidenceBedrockWeak = "bedrock_weak"
	BedrockVerifyEvidenceNonBedrock  = "non_bedrock"

	BedrockVerifyStepMessages          = "messages"
	BedrockVerifyStepToolUse           = "tool_use"
	BedrockVerifyStepStream            = "messages_stream"
	BedrockVerifyStepComputer          = "computer_use"
	BedrockVerifyStepMaxTokensOverflow = "max_tokens_overflow"
	BedrockVerifyStepEffortUnknown     = "effort_unknown_variant"
	BedrockVerifyStepThinkingEnabled   = "thinking_type_enabled"
	BedrockVerifyStepTokenizer         = "tokenizer"

	BedrockVerifySourceBedrock      = "bedrock"
	BedrockVerifySourceVertex       = "vertex"
	BedrockVerifySourceAnthropic    = "anthropic"
	BedrockVerifySourceSubscription = "subscription"
	BedrockVerifySourceUnknown      = "unknown"

	BedrockVerifyNoteForcedToolUnsupported = "forced_tool_choice_unsupported"
	BedrockVerifyNoteUnconfirmed           = "bedrock_unconfirmed"
	BedrockVerifyNoteTokenizerMismatch     = "tokenizer_mismatch"
)

var (
	// Bedrock message ids are msg_bdrk_ + 52 lowercase base32 chars. Tool-use ids are not
	// checked this way: Bedrock returns both toolu_bdrk_<base32> and toolu_bdrk_01<base62>.
	bedrockVerifyNativeIDBody    = regexp.MustCompile(`^[a-z2-7]{52}$`)
	bedrockVerifyAnthropicIDBody = regexp.MustCompile(`^01[0-9A-Za-z]{20,}$`)
)

// Models whose Bedrock tool schema has no computer_2025xxxx tag, so a computer_20251124
// tool must come back as a ValidationException with or without the beta header.
var bedrockVerifyComputerRejectingModels = []string{"claude-opus-5-5", "claude-fable-5-2"}

var bedrockVerifyComputerRejectedPattern = regexp.MustCompile(`Input tag '` + bedrockVerifyComputerTool + `' found using 'type' does not match any of the expected tags`)

// input_tokens for bedrockVerifyTokenizerText at max_tokens=1, measured 2026-11-20 on a channel
// whose validation errors all matched real Bedrock. Another count means another tokenizer
// (a different model generation) or text added by the relay.
var bedrockVerifyTokenizerReference = map[string]int64{
	"claude-opus-5":   241,
	"claude-opus-5-5": 243,
}

var bedrockVerifyValidationLabels = map[string]string{
	BedrockVerifyStepMaxTokensOverflow: fmt.Sprintf("max_tokens=%d（超过所有 Claude 模型的输出上限）", bedrockVerifyOverflowMaxTokens),
	BedrockVerifyStepEffortUnknown:     fmt.Sprintf("output_config.effort=%q（不存在的取值）", bedrockVerifyUnknownEffort),
	BedrockVerifyStepThinkingEnabled: fmt.Sprintf("thinking.type=enabled 且 budget_tokens=%d 大于 max_tokens=%d",
		bedrockVerifyThinkingBudget, bedrockVerifyValidationMaxTokens),
}

// Wording real Bedrock uses for each validation probe (captured from AWS ValidationExceptions),
// and, where known, the Anthropic first-party wording for the same mistake.
var bedrockVerifyValidationPatterns = map[string]struct{ genuine, firstParty *regexp.Regexp }{
	BedrockVerifyStepMaxTokensOverflow: {
		genuine:    regexp.MustCompile(`maximum allowed number of output tokens for (?:[a-z]{2,6}\.)?anthropic\.claude`),
		firstParty: regexp.MustCompile(`maximum allowed number of output tokens for claude`),
	},
	BedrockVerifyStepEffortUnknown: {
		genuine: regexp.MustCompile(`Request does not satisfy the required constraints`),
	},
	BedrockVerifyStepThinkingEnabled: {
		genuine: regexp.MustCompile(`"thinking\.type\.enabled" is not supported for this model`),
	},
}

var bedrockVerifyReportedHeaderPrefixes = []string{
	"x-amzn-",
	"request-id",
	"x-request-id",
	"anthropic-",
	"x-new-api-",
	"x-oneapi-",
	"server",
}

// Bedrock error wrappers; relays copy them freely, so they are weak evidence only. Checked in
// order and the first match per message is recorded so one AWS error yields one evidence row.
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

// bedrockVerifyValidation is the relay's answer to one request real Bedrock rejects.
type bedrockVerifyValidation struct {
	Step   string
	Status int // 0 when the request never got a reply
	Error  string
}

// bedrockVerifyObservation is everything the probe requests saw; evaluateBedrockEvidence
// derives the verdict from it without doing any I/O.
type bedrockVerifyObservation struct {
	Model   string
	Headers []http.Header
	Errors  []string

	MessageOK        bool
	InputTokens      int64
	InferenceGeoSeen bool
	StreamOK         bool
	ToolOK           bool

	// StreamBedrockMetrics: an event carried amazon-bedrock-invocationMetrics.
	StreamBedrockMetrics bool

	MessageIDs []string
	ToolUseIDs []string

	ForcedToolRejected bool
	ForcedToolError    string

	// Result of sending computer_20251124 without the beta header; kept out of Errors
	// because a rejection is the expected outcome.
	ComputerProbed   bool
	ComputerAccepted bool
	ComputerError    string

	// Kept out of Errors for the same reason.
	Validations []bedrockVerifyValidation

	TokenizerTokens int64
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

type bedrockVerifyStepResult struct {
	step       UpstreamVerifyStep
	headers    http.Header
	validation bedrockVerifyValidation
	tokens     int64
	messageID  string
	err        string
}

func (s *BedrockVerifyService) ListModels(ctx context.Context, baseURL, apiKey string) UpstreamVerifyModelsResult {
	return s.client.listModels(ctx, baseURL, apiKey)
}

func (s *BedrockVerifyService) ProbeModel(ctx context.Context, baseURL, apiKey, model string) BedrockVerifyProbeResult {
	obs := bedrockVerifyObservation{Model: model}

	// The small validation and tokenizer requests are independent of each other and of
	// obs, so they run alongside the sequential probes to keep the total time unchanged.
	validationSteps := []string{BedrockVerifyStepMaxTokensOverflow, BedrockVerifyStepEffortUnknown, BedrockVerifyStepThinkingEnabled}
	side := make([]bedrockVerifyStepResult, len(validationSteps)+1)
	var wg sync.WaitGroup
	for i, name := range validationSteps {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			side[i] = s.probeValidation(ctx, baseURL, apiKey, model, name)
		}(i, name)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		side[len(validationSteps)] = s.probeTokenizer(ctx, baseURL, apiKey, model)
	}()

	steps := make([]UpstreamVerifyStep, 0, 4+len(side))
	steps = append(steps, s.probeMessage(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeToolUse(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeStream(ctx, baseURL, apiKey, &obs))
	steps = append(steps, s.probeComputerUse(ctx, baseURL, apiKey, &obs))
	wg.Wait()

	for i, r := range side {
		steps = append(steps, r.step)
		if r.headers != nil {
			obs.Headers = append(obs.Headers, r.headers)
		}
		if i < len(validationSteps) {
			obs.Validations = append(obs.Validations, r.validation)
			continue
		}
		if r.err != "" {
			obs.Errors = append(obs.Errors, r.err)
			continue
		}
		obs.TokenizerTokens = r.tokens
		if r.messageID != "" {
			obs.MessageIDs = append(obs.MessageIDs, r.messageID)
		}
	}

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
	obs.InferenceGeoSeen = gjson.GetBytes(res.body, "usage.inference_geo").Exists()
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
		// Some Claude models reject a forced tool; "auto" still yields a tool_use id.
		if res.err != nil || res.status != http.StatusBadRequest || !bedrockVerifyForcedToolRejectedPattern.MatchString(step.Error) {
			return step
		}
		obs.ForcedToolRejected = true
		forcedErr := step.Error
		obs.ForcedToolError = forcedErr
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

// probeStream reads the whole (short) stream: Bedrock appends amazon-bedrock-invocationMetrics
// to the final events.
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
		typ := gjson.Get(payload, "type").String()
		if typ == "error" || gjson.Get(payload, "error").Exists() {
			step.Error = bedrockVerifyErrorText(0, []byte(payload))
			obs.Errors = append(obs.Errors, step.Error)
			break
		}
		if gjson.Get(payload, "amazon-bedrock-invocationMetrics").Exists() {
			obs.StreamBedrockMetrics = true
		}
		if typ == "message_start" {
			if id := gjson.Get(payload, "message.id").String(); id != "" {
				obs.MessageIDs = append(obs.MessageIDs, id)
			}
			step.OK = true
			obs.StreamOK = true
		}
		if typ == "message_stop" {
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

// probeComputerUse sends a computer_20251124 tool without the computer-use beta header.
// Bedrock validates server tool types itself, so a relay that answers 200 has rewritten
// the tool or is not talking to Bedrock.
func (s *BedrockVerifyService) probeComputerUse(ctx context.Context, baseURL, apiKey string, obs *bedrockVerifyObservation) UpstreamVerifyStep {
	body, _ := json.Marshal(map[string]any{
		"model":      obs.Model,
		"max_tokens": bedrockVerifyMaxTokens,
		"tools": []map[string]any{{
			"type":              bedrockVerifyComputerTool,
			"name":              "computer",
			"display_width_px":  1024,
			"display_height_px": 768,
		}},
		"messages": []map[string]string{{"role": "user", "content": bedrockVerifyComputerPrompt}},
	})
	res := s.client.do(ctx, http.MethodPost, upstreamVerifyEndpoint(baseURL, "/v1/messages"), apiKey, body)
	step := s.client.newStep(BedrockVerifyStepComputer, res)
	if res.headers != nil {
		obs.Headers = append(obs.Headers, res.headers)
	}
	if res.err != nil {
		step.Error = "request failed: " + res.err.Error()
		return step
	}
	obs.ComputerProbed = true
	if bedrockVerifyAccepted(res) {
		step.OK = true
		obs.ComputerAccepted = true
		return step
	}
	step.Error = bedrockVerifyErrorText(res.status, res.body)
	obs.ComputerError = step.Error
	return step
}

func bedrockVerifyValidationBody(model, name string) []byte {
	body := map[string]any{
		"model":      model,
		"max_tokens": bedrockVerifyValidationMaxTokens,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
	switch name {
	case BedrockVerifyStepMaxTokensOverflow:
		body["max_tokens"] = bedrockVerifyOverflowMaxTokens
	case BedrockVerifyStepEffortUnknown:
		body["thinking"] = map[string]any{"type": "adaptive"}
		body["output_config"] = map[string]any{"effort": bedrockVerifyUnknownEffort}
	case BedrockVerifyStepThinkingEnabled:
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": bedrockVerifyThinkingBudget}
	}
	raw, _ := json.Marshal(body)
	return raw
}

// probeValidation sends a request real Bedrock rejects. The step is OK when the relay
// rejects it too; whether the wording matches Bedrock is judged in evaluateBedrockEvidence.
func (s *BedrockVerifyService) probeValidation(ctx context.Context, baseURL, apiKey, model, name string) bedrockVerifyStepResult {
	res := s.client.do(ctx, http.MethodPost, upstreamVerifyEndpoint(baseURL, "/v1/messages"), apiKey, bedrockVerifyValidationBody(model, name))
	out := bedrockVerifyStepResult{step: s.client.newStep(name, res), headers: res.headers, validation: bedrockVerifyValidation{Step: name}}
	switch {
	case res.err != nil:
		out.step.Error = "request failed: " + res.err.Error()
	case bedrockVerifyAccepted(res):
		out.validation.Status = res.status
		out.step.Error = "请求被正常受理，真 Bedrock 会返回 400 ValidationException"
	default:
		out.validation.Status = res.status
		out.validation.Error = bedrockVerifyErrorText(res.status, res.body)
		out.step.OK = res.status >= 400 && res.status < 500 && !upstreamVerifyModelUnavailablePattern.MatchString(out.validation.Error)
		if !out.step.OK {
			out.step.Error = out.validation.Error
		}
	}
	return out
}

// probeTokenizer counts a fixed multilingual text; the count identifies the tokenizer.
func (s *BedrockVerifyService) probeTokenizer(ctx context.Context, baseURL, apiKey, model string) bedrockVerifyStepResult {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": bedrockVerifyTokenizerText}},
	})
	res := s.client.do(ctx, http.MethodPost, upstreamVerifyEndpoint(baseURL, "/v1/messages"), apiKey, body)
	out := bedrockVerifyStepResult{step: s.client.newStep(BedrockVerifyStepTokenizer, res), headers: res.headers}
	switch {
	case res.err != nil:
		out.err = "request failed: " + res.err.Error()
	case !bedrockVerifyAccepted(res):
		out.err = bedrockVerifyErrorText(res.status, res.body)
	default:
		out.step.OK = true
		out.tokens = gjson.GetBytes(res.body, "usage.input_tokens").Int()
		out.messageID = gjson.GetBytes(res.body, "id").String()
		return out
	}
	out.step.Error = out.err
	return out
}

func bedrockVerifyAccepted(res upstreamVerifyHTTPResult) bool {
	return res.err == nil && res.status == http.StatusOK && gjson.ValidBytes(res.body) &&
		gjson.GetBytes(res.body, "type").String() != "error" && !gjson.GetBytes(res.body, "error").Exists()
}

func bedrockVerifyRejectsComputerTool(model string) bool {
	m := strings.ToLower(model)
	for _, name := range bedrockVerifyComputerRejectingModels {
		if strings.Contains(m, name) {
			return true
		}
	}
	return false
}

// bedrockVerifyBedrockErrorDetail returns the evidence text of the first Bedrock error
// pattern the message matches, or "" when none does.
func bedrockVerifyBedrockErrorDetail(msg string) string {
	for _, p := range bedrockVerifyBedrockErrorPatterns {
		if p.re.MatchString(msg) {
			return p.detail
		}
	}
	return ""
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
	if !bedrockVerifyAccepted(res) {
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
	// strongHits decide the verdict; weakHits are Bedrock formats any relay can copy and
	// never make a model count as Bedrock on their own.
	strongHits, weakHits, counterHits := 0, 0, 0
	vertexSeen, anthropicSeen, subscriptionSeen := false, false, false

	classifyID := func(id, bedrockPrefix, vertexPrefix, anthropicPrefix, label string) {
		switch {
		case strings.HasPrefix(id, bedrockPrefix):
			weakHits++
			ev.add(BedrockVerifyEvidenceBedrockWeak, label+"以 "+bedrockPrefix+" 开头（Bedrock 格式，中转站可以伪造）", id)
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
	msgKinds := 0
	for _, id := range obs.MessageIDs {
		kind := bedrockVerifyIDKind(id)
		key := "msg:" + kind
		if seenPrefix[key] {
			continue
		}
		seenPrefix[key] = true
		msgKinds++
		if kind == bedrockVerifyIDKindDisguised {
			counterHits++
			ev.add(BedrockVerifyEvidenceNonBedrock,
				"消息 ID 是 Anthropic 官方格式（01...）加上了 bdrk_ 前缀；Bedrock 的消息 ID 是 msg_bdrk_ 加 52 位小写字母和数字", id)
			continue
		}
		classifyID(id, "msg_bdrk_", "msg_vrtx_", "msg_01", "消息 ID ")
	}
	if msgKinds > 1 {
		ev.add(UpstreamVerifyEvidenceInfo, "同一个模型返回了多种格式的消息 ID，可能是混合渠道", strings.Join(obs.MessageIDs, ", "))
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
				strongHits++
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

	seenWrapper := map[string]bool{}
	for _, msg := range obs.Errors {
		if detail := bedrockVerifyBedrockErrorDetail(msg); detail != "" {
			weakHits++
			if !seenWrapper[detail] {
				seenWrapper[detail] = true
				ev.add(BedrockVerifyEvidenceBedrockWeak, detail+"（只是报错的外层格式，中转站可以伪造）", msg)
			}
		}
		if bedrockVerifySubscriptionErrorPattern.MatchString(msg) {
			counterHits++
			subscriptionSeen = true
			ev.add(BedrockVerifyEvidenceNonBedrock, "报错显示上游是 Claude 订阅账号（仅限 Claude Code 使用的凭证）", msg)
		}
	}

	validationAccepted := false
	unavailableErrors := append([]string(nil), obs.Errors...)
	for _, val := range obs.Validations {
		label := bedrockVerifyValidationLabels[val.Step]
		switch {
		case val.Status == 0:
			continue
		case val.Status == http.StatusOK:
			counterHits++
			validationAccepted = true
			ev.add(BedrockVerifyEvidenceNonBedrock, label+" 被正常受理；真 Bedrock 会返回 400 ValidationException", "")
			continue
		case upstreamVerifyModelUnavailablePattern.MatchString(val.Error):
			unavailableErrors = append(unavailableErrors, val.Error)
			continue
		}
		patterns := bedrockVerifyValidationPatterns[val.Step]
		switch {
		case patterns.genuine != nil && patterns.genuine.MatchString(val.Error):
			strongHits++
			ev.add(BedrockVerifyEvidenceBedrock, label+" 被拒绝，报错内容与真 Bedrock 一致", val.Error)
		case patterns.firstParty != nil && patterns.firstParty.MatchString(val.Error):
			counterHits++
			anthropicSeen = true
			ev.add(BedrockVerifyEvidenceNonBedrock, label+" 被拒绝，但报错是 Anthropic 官方 API 的写法（模型名没有 Bedrock 的 anthropic. 前缀）", val.Error)
		case bedrockVerifyBedrockErrorDetail(val.Error) != "":
			weakHits++
			ev.add(BedrockVerifyEvidenceBedrockWeak, label+" 被拒绝，报错有 Bedrock 的外层格式，但具体内容和已知的 Bedrock 报错对不上", val.Error)
		default:
			ev.add(UpstreamVerifyEvidenceInfo, label+" 被拒绝，报错不是 Bedrock 格式", val.Error)
		}
	}

	if obs.StreamBedrockMetrics {
		weakHits++
		ev.add(BedrockVerifyEvidenceBedrockWeak, "流式响应里带有 amazon-bedrock-invocationMetrics（Bedrock 格式，中转站可以伪造）", "")
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
	if obs.TokenizerTokens > 0 {
		ref, known := bedrockVerifyTokenizerReference[strings.ToLower(strings.TrimSpace(obs.Model))]
		switch {
		case !known:
			ev.add(UpstreamVerifyEvidenceInfo, fmt.Sprintf("分词探测的输入 token 数为 %d（该模型没有参考值）", obs.TokenizerTokens), "")
		case obs.TokenizerTokens == ref:
			ev.add(UpstreamVerifyEvidenceInfo, fmt.Sprintf("分词探测的输入 token 数为 %d，与该模型的参考值一致", obs.TokenizerTokens), "")
		default:
			v.Notes = append(v.Notes, BedrockVerifyNoteTokenizerMismatch)
			ev.add(UpstreamVerifyEvidenceInfo,
				fmt.Sprintf("分词探测的输入 token 数为 %d，该模型的参考值是 %d：可能换成了别的模型，或者中转站加了内容", obs.TokenizerTokens, ref), "")
		}
	}
	if obs.InferenceGeoSeen {
		ev.add(UpstreamVerifyEvidenceInfo, "usage 里有 inference_geo 字段：Anthropic 官方 API 会返回，目前见到的 Bedrock 响应里没有", "")
	}
	if obs.ForcedToolRejected {
		v.Notes = append(v.Notes, BedrockVerifyNoteForcedToolUnsupported)
		if bedrockVerifyBedrockErrorDetail(obs.ForcedToolError) != "" {
			ev.add(UpstreamVerifyEvidenceInfo, "强制工具调用（tool_choice 为 tool / any）被拒绝，报错有 Bedrock 的外层格式，已改用 auto 重试", obs.ForcedToolError)
		} else {
			ev.add(UpstreamVerifyEvidenceInfo, "强制工具调用被中转站直接拒绝（报错没有 Bedrock 包装，请求没有到达上游），已改用 auto 重试", obs.ForcedToolError)
		}
	}
	if obs.ComputerProbed {
		switch {
		case obs.ComputerAccepted && bedrockVerifyRejectsComputerTool(obs.Model):
			counterHits++
			ev.add(BedrockVerifyEvidenceNonBedrock,
				"不带 beta 头的 "+bedrockVerifyComputerTool+" 工具被正常受理；Bedrock 会返回 400 ValidationException，说明中转站改写了工具，或者上游不是 Bedrock", "")
		case obs.ComputerAccepted:
			ev.add(UpstreamVerifyEvidenceInfo, "不带 beta 头的 "+bedrockVerifyComputerTool+" 工具被正常受理（该模型在 Bedrock 上的表现未核对，仅供参考）", "")
		case bedrockVerifyComputerRejectedPattern.MatchString(obs.ComputerError):
			strongHits++
			ev.add(BedrockVerifyEvidenceBedrock, "不带 beta 头的 "+bedrockVerifyComputerTool+" 工具被拒绝，报错内容与 Bedrock 的工具类型校验一致", obs.ComputerError)
		case bedrockVerifyBedrockErrorDetail(obs.ComputerError) != "":
			weakHits++
			ev.add(BedrockVerifyEvidenceBedrockWeak, "不带 beta 头的 "+bedrockVerifyComputerTool+" 工具被拒绝，报错有 Bedrock 的外层格式", obs.ComputerError)
		default:
			ev.add(UpstreamVerifyEvidenceInfo, bedrockVerifyComputerTool+" 探测被拒绝，但报错不是 Bedrock 格式", obs.ComputerError)
		}
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
	case strongHits > 0 || (weakHits > 0 && counterHits == 0):
		v.DetectedSource = BedrockVerifySourceBedrock
	default:
		v.DetectedSource = BedrockVerifySourceUnknown
	}

	v.Evidences = ev
	anySuccess := obs.MessageOK || obs.ToolOK || obs.StreamOK || obs.ComputerAccepted || obs.TokenizerTokens > 0 || validationAccepted
	if !anySuccess && strongHits == 0 && counterHits == 0 {
		v.Status = UpstreamVerifyStatusFailed
		v.ModelUnavailable = upstreamVerifyModelUnavailable(unavailableErrors)
		return v
	}
	if strongHits == 0 && weakHits > 0 && counterHits == 0 {
		v.Notes = append(v.Notes, BedrockVerifyNoteUnconfirmed)
	}
	v.Status = UpstreamVerifyStatusOK
	v.IsBedrock = strongHits > 0 && counterHits == 0
	v.Suspicious = strongHits+weakHits > 0 && counterHits > 0
	return v
}

const bedrockVerifyIDKindDisguised = "msg_bdrk_01"

// bedrockVerifyIDKind buckets an id by its provider prefix (and, for msg_bdrk_, by the
// shape of the rest) so repeated ids produce one evidence row while mixed formats each
// get their own.
func bedrockVerifyIDKind(id string) string {
	if body, ok := strings.CutPrefix(id, "msg_bdrk_"); ok {
		switch {
		case bedrockVerifyNativeIDBody.MatchString(body):
			return "msg_bdrk_native"
		case bedrockVerifyAnthropicIDBody.MatchString(body):
			return bedrockVerifyIDKindDisguised
		}
		return "msg_bdrk_"
	}
	for _, p := range []string{"msg_vrtx_", "msg_01", "toolu_bdrk_", "toolu_vrtx_", "toolu_01"} {
		if strings.HasPrefix(id, p) {
			return p
		}
	}
	return "other"
}

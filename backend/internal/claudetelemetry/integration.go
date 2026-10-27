package claudetelemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Integration struct {
	config   Config
	runtime  *SessionRuntime
	exporter *TelemetryExporter
	now      func() time.Time
}

type BeginInput struct {
	EventContext   EventContext
	RequestBody    []byte
	IsClaudeCode   bool
	RetryCount     int
	ObserverMode   ObserverMode
	PermissionMode string
}

type Completion struct {
	RequestID   string
	CompletedAt time.Time
}

type eventDraft struct {
	name       string
	metadata   map[string]any
	provenance FieldProvenance
}

type EventDescriptor struct {
	Name       string
	Provenance FieldProvenance
}

type Request struct {
	mu               sync.Mutex
	owner            *Integration
	context          EventContext
	observer         *Observer
	request          []eventDraft
	mutation         sessionMutation
	startedAt        time.Time
	title            bool
	suggestion       bool
	continuation     bool
	querySource      string
	queryChainID     string
	queryDepth       int
	turnStartedAt    time.Time
	retryCount       int
	messageCount     int
	toolsCount       int
	toolsCharLength  int
	systemCharLength int
	input            inputStats
	effort           string
	permissionMode   string
	finished         bool
}

type inputStats struct {
	TextCharLength     int
	ImageBlockCount    int
	ImageTotalBytes    int
	DocumentBlockCount int
	DocumentTotalBytes int
}

func NewIntegration(config Config, exporter *TelemetryExporter) (*Integration, error) {
	config = config.normalized()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if exporter == nil {
		var err error
		exporter, err = NewTelemetryExporter(config)
		if err != nil {
			return nil, err
		}
	}
	return &Integration{
		config:   config,
		runtime:  NewSessionRuntime(config.SessionTTL, config.SessionCapacity),
		exporter: exporter,
		now:      time.Now,
	}, nil
}

func (i *Integration) Runtime() *SessionRuntime     { return i.runtime }
func (i *Integration) Exporter() *TelemetryExporter { return i.exporter }

// PendingEvents exposes names and field origins for diagnostics without
// exposing metadata values.
func (r *Request) PendingEvents() []EventDescriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]EventDescriptor, 0, len(r.request))
	for _, event := range r.request {
		out = append(out, EventDescriptor{
			Name: event.name, Provenance: cloneProvenance(event.provenance),
		})
	}
	return out
}

// Begin calculates request-side telemetry but buffers it in Request. Nothing is
// exported until Complete, so an internal failover attempt can safely Abort.
func (i *Integration) Begin(_ context.Context, input BeginInput) (*Request, error) {
	body, err := parseRequest(input.RequestBody)
	if err != nil {
		return nil, fmt.Errorf("decode telemetry request: %w", err)
	}
	eventContext := input.EventContext
	if eventContext.Model == "" {
		eventContext.Model = body.Model
	}
	if eventContext.Environment.Version == "" {
		eventContext.Environment.Version = CompatibilityProfile
	}
	if eventContext.Environment.VersionBase == "" {
		eventContext.Environment.VersionBase = CompatibilityProfile
	}
	if eventContext.Environment.BuildTime == "" {
		eventContext.Environment.BuildTime = CompatibilityBuildTime
	}
	if eventContext.Provider == "" {
		eventContext.Provider = i.config.Provider
	}
	title, suggestion, source := classifyRequest(body, input.IsClaudeCode)
	latestUser := lastUser(body)
	continuation := blockCount(latestUser, "tool_result") > 0
	summaries := summarizeToolResults(body)
	toolIDs := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if summary.ID != "" {
			toolIDs = append(toolIDs, summary.ID)
		}
	}
	fingerprint := requestFingerprint(body)
	mutation, retry, seenResults := i.runtime.begin(
		sessionKey(eventContext), source, fingerprint, continuation, input.RetryCount > 0, toolIDs,
	)
	now := i.now()
	req := &Request{
		owner: i, context: eventContext, observer: NewObserver(input.ObserverMode),
		mutation: mutation, startedAt: now, title: title, suggestion: suggestion,
		continuation: continuation, querySource: source,
		queryChainID: mutation.QueryChainID, queryDepth: mutation.QueryDepth,
		turnStartedAt: mutation.TurnStartedAt, retryCount: input.RetryCount,
		messageCount: len(body.Messages), toolsCount: len(body.Tools),
		toolsCharLength:  toolDefinitionsLength(body.Tools),
		systemCharLength: systemLength(body), input: inspectInput(body),
		effort:         body.OutputConfig.Effort,
		permissionMode: permissionMode(body, input.PermissionMode),
	}
	req.observer.now = i.now
	req.request = requestEvents(body, req, retry, summaries, seenResults)
	return req, nil
}

func (i *Integration) Observe(request *Request, chunk []byte) error {
	if request == nil || request.owner != i {
		return errors.New("invalid telemetry request")
	}
	request.mu.Lock()
	defer request.mu.Unlock()
	if request.finished {
		return errors.New("telemetry request already finished")
	}
	return request.observer.Observe(chunk)
}

// ObserveEventJSON records one raw JSON object extracted by the gateway's
// existing SSE parser.
func (i *Integration) ObserveEventJSON(request *Request, raw []byte) error {
	if request == nil || request.owner != i {
		return errors.New("invalid telemetry request")
	}
	request.mu.Lock()
	defer request.mu.Unlock()
	if request.finished {
		return errors.New("telemetry request already finished")
	}
	return request.observer.ObserveEventJSON(raw)
}

// Complete commits request-side events even when the observed model response is
// incomplete. Success and turn events require Observation.Successful.
func (i *Integration) Complete(ctx context.Context, request *Request, completion Completion) error {
	if request == nil || request.owner != i {
		return errors.New("invalid telemetry request")
	}
	request.mu.Lock()
	if request.finished {
		request.mu.Unlock()
		return errors.New("telemetry request already finished")
	}
	request.finished = true
	observation := request.observer.Finish()
	request.mu.Unlock()

	endedAt := completion.CompletedAt
	if endedAt.IsZero() {
		endedAt = i.now()
	}
	drafts := append([]eventDraft(nil), request.request...)
	if !observation.FirstByteAt.IsZero() &&
		observation.FirstByteAt.Sub(request.startedAt) >= i.config.SlowFirstByte {
		drafts = append(drafts, draft("tengu_api_slow_first_byte", map[string]any{
			"model": request.context.Model, "provider": request.context.Provider,
			"attempt":    request.retryCount + 1,
			"elapsed_ms": observation.FirstByteAt.Sub(request.startedAt).Milliseconds(),
		}, observedFields("model", "provider", "attempt", "elapsed_ms")))
	}
	if observation.Successful() {
		drafts = append(drafts, completionEvents(request, observation, completion.RequestID, endedAt)...)
	}
	built := make([]BuiltEvent, 0, len(drafts))
	for _, item := range drafts {
		event, err := buildEvent(endedAt, request.context, item.name, item.metadata, item.provenance)
		if err != nil {
			return err
		}
		built = append(built, event)
	}
	if err := i.exporter.EnqueueBatch(ctx, request.context.Route, built); err != nil {
		return err
	}
	i.runtime.commit(request.mutation, completion.RequestID, observation.MessageID, endedAt)
	return nil
}

func (i *Integration) Abort(request *Request) {
	if request == nil || request.owner != i {
		return
	}
	request.mu.Lock()
	request.finished = true
	request.observer.sseBuffer = ""
	request.observer.jsonBody.Reset()
	request.mu.Unlock()
}

func requestEvents(body requestBody, req *Request, retry bool, summaries []toolResultSummary, seen map[string]bool) []eventDraft {
	events := make([]eventDraft, 0, 14)
	if !req.title && !req.suggestion && !retry && !req.continuation {
		events = append(events,
			draft("tengu_feature_ok", map[string]any{"feature_name": "prompt_submit"}, observedFields("feature_name")),
			draft("tengu_input_prompt", compactMap(map[string]any{
				"prompt_index":  req.mutation.PromptIndex,
				"prompt_length": latestPromptLength(body),
				"effort_level":  body.OutputConfig.Effort,
			}), FieldProvenance{
				"prompt_index": Inferred, "prompt_length": Derived, "effort_level": Observed,
			}),
		)
	} else if !req.title && !req.suggestion && !retry && req.continuation {
		assistantBlocks := 0
		if assistant := lastAssistant(body); assistant != nil {
			assistantBlocks = len(contentBlocks(assistant.Content))
		}
		beforeDepth := req.queryDepth
		if beforeDepth > 0 {
			beforeDepth--
		}
		events = append(events,
			draft("tengu_query_before_attachments", map[string]any{
				"messagesForQueryCount":  len(body.Messages),
				"assistantMessagesCount": assistantBlocks,
				"toolResultsCount":       blockCount(lastUser(body), "tool_result"),
				"queryChainId":           req.queryChainID, "queryDepth": beforeDepth,
			}, mixedProvenance([]string{"queryChainId", "queryDepth"}, []string{
				"messagesForQueryCount", "assistantMessagesCount", "toolResultsCount",
			})),
			draft("tengu_query_after_attachments", map[string]any{
				"totalToolResultsCount": blockCount(lastUser(body), "tool_result"),
				"queryChainId":          req.queryChainID, "queryDepth": beforeDepth,
			}, mixedProvenance([]string{"queryChainId", "queryDepth"}, []string{"totalToolResultsCount"})),
		)
	}
	if !req.title && !req.suggestion {
		names := sortedToolNames(body)
		enabled := contains(names, "ToolSearch")
		mcpCount := 0
		for _, name := range names {
			if strings.HasPrefix(strings.ToLower(name), "mcp__") {
				mcpCount++
			}
		}
		mode, reason := "standard", "tool_search_unavailable"
		if enabled {
			mode, reason = "tst", "tst_enabled"
		}
		events = append(events, draft("tengu_tool_search_mode_decision", map[string]any{
			"enabled": enabled, "mode": mode, "reason": reason,
			"checkedModel": req.context.Model, "mcpToolCount": mcpCount, "userType": "external",
		}, observedFields("enabled", "mode", "reason", "checkedModel", "mcpToolCount", "userType")))
	}
	for _, summary := range summaries {
		if summary.ID != "" && seen[summary.ID] {
			continue
		}
		req.mutation.NewToolResultIDs = append(req.mutation.NewToolResultIDs, summary.ID)
		name := "tengu_tool_use_success"
		if summary.IsError {
			name = "tengu_tool_use_error"
		}
		depth := req.queryDepth
		if req.continuation && depth > 0 {
			depth--
		}
		events = append(events, draft(name, compactMap(map[string]any{
			"messageID": req.mutation.PreviousMessageID,
			"toolName":  summary.Name, "isMcp": summary.IsMCP,
			"effort_level":          body.OutputConfig.Effort,
			"toolResultSizeBytes":   summary.ResultBytes,
			"toolResultMediaBlocks": summary.MediaBlocks,
			"toolInputSizeBytes":    summary.InputBytes,
			"queryChainId":          req.queryChainID, "queryDepth": depth,
			"requestId": req.mutation.PreviousRequestID,
		}), mixedProvenance(
			[]string{"queryChainId", "queryDepth"},
			[]string{"messageID", "toolName", "isMcp", "effort_level", "toolResultSizeBytes", "toolResultMediaBlocks", "toolInputSizeBytes", "requestId"},
		)))
	}
	preCount := 0
	for _, message := range body.Messages {
		count := len(contentBlocks(message.Content))
		if count < 1 {
			count = 1
		}
		preCount += count
	}
	if len(systemBlocks(body.System)) > 0 {
		preCount += 2
	}
	events = append(events,
		draft("tengu_api_before_normalize", map[string]any{"preNormalizedMessageCount": preCount}, inferredFields("preNormalizedMessageCount")),
		draft("tengu_api_after_normalize", map[string]any{"postNormalizedMessageCount": len(body.Messages)}, observedFields("postNormalizedMessageCount")),
	)
	systems := systemBlocks(body.System)
	if len(systems) > 0 && systems[0].Text != "" {
		events = append(events, draft("tengu_sysprompt_block", map[string]any{
			"length": utf16Length(systems[0].Text), "hash": sha256Hex(systems[0].Text),
		}, FieldProvenance{"length": Observed, "hash": Derived}))
	}
	var cached []systemBlock
	for _, block := range systems {
		if block.CacheControl != nil && block.Text != "" {
			cached = append(cached, block)
		}
	}
	if len(cached) >= 2 {
		dynamic := 0
		for _, block := range cached[1:] {
			dynamic += utf16Length(block.Text)
		}
		metadata := map[string]any{
			"blockCount": len(systems), "staticBlockLength": utf16Length(cached[0].Text),
			"dynamicBlockLength": dynamic,
		}
		events = append(events,
			draft("tengu_sysprompt_boundary_found", metadata, observedFields("blockCount", "staticBlockLength", "dynamicBlockLength")),
			draft("tengu_sysprompt_boundary_found", metadata, observedFields("blockCount", "staticBlockLength", "dynamicBlockLength")),
		)
	}
	cache := cacheMetadata(body, req.context.Betas)
	events = append(events, draft("tengu_api_cache_breakpoints", cache, observedFields("totalMessageCount", "cachingEnabled", "markerCount")))
	query := compactMap(map[string]any{
		"model": req.context.Model, "messagesLength": len(body.Messages),
		"temperature": temperature(body), "provider": req.context.Provider,
		"buildAgeMins": buildAgeMinutes(req.context.Environment.BuildTime, req.startedAt),
		"betas":        req.context.Betas, "permissionMode": req.permissionMode,
		"querySource": req.querySource, "queryChainId": req.queryChainID,
		"queryDepth": req.queryDepth, "previousRequestId": req.mutation.PreviousRequestID,
		"thinkingType": valueOr(body.Thinking.Type, "disabled"),
		"effortValue":  body.OutputConfig.Effort,
	})
	events = append(events,
		draft("tengu_api_query", query, withDerived(mixedProvenance(
			[]string{"querySource", "queryChainId", "queryDepth"},
			[]string{"model", "messagesLength", "temperature", "provider", "betas", "permissionMode", "previousRequestId", "thinkingType", "effortValue"},
		), "buildAgeMins")),
		draft("tengu_api_cache_breakpoints", cache, observedFields("totalMessageCount", "cachingEnabled", "markerCount")),
	)
	return events
}

func completionEvents(req *Request, observation Observation, requestID string, endedAt time.Time) []eventDraft {
	duration := endedAt.Sub(req.startedAt)
	ttftAt := observation.FirstContentAt
	if ttftAt.IsZero() {
		ttftAt = observation.FirstByteAt
	}
	if ttftAt.IsZero() {
		ttftAt = endedAt
	}
	toolLengths := make(map[string]int)
	for _, tool := range observation.ToolUses {
		toolLengths[valueOr(tool.Name, "unknown")] += tool.InputLength
	}
	toolLengthsJSON := ""
	if len(toolLengths) > 0 {
		raw, _ := json.Marshal(toolLengths)
		toolLengthsJSON = string(raw)
	}
	events := []eventDraft{
		draft("tengu_feature_ok", map[string]any{"feature_name": "api_request"}, observedFields("feature_name")),
		draft("tengu_api_success", compactMap(map[string]any{
			"model": valueOr(observation.Model, req.context.Model),
			"betas": req.context.Betas, "messageCount": req.messageCount,
			"inputTokens":         observation.Usage.InputTokens,
			"outputTokens":        observation.Usage.OutputTokens,
			"cachedInputTokens":   observation.Usage.CacheReadInputTokens,
			"uncachedInputTokens": observation.Usage.CacheCreationInputTokens,
			"durationMs":          duration.Milliseconds(), "attempt": req.retryCount + 1,
			"ttftMs":         ttftAt.Sub(req.startedAt).Milliseconds(),
			"firstContentMs": optionalDuration(observation.FirstContentAt, req.startedAt),
			"buildAgeMins":   buildAgeMinutes(req.context.Environment.BuildTime, endedAt),
			"provider":       req.context.Provider, "requestId": requestID,
			"stop_reason": observation.StopReason, "querySource": req.querySource,
			"queryChainId": req.queryChainID, "queryDepth": req.queryDepth,
			"previousRequestId": req.mutation.PreviousRequestID,
			"permissionMode":    req.permissionMode, "toolsCount": req.toolsCount,
			"toolsCharLength": req.toolsCharLength, "systemCharLength": req.systemCharLength,
			"effort_level": req.effort, "textContentLength": observation.TextContentLength,
			"thinkingContentLength":  optionalPositive(observation.ThinkingContentLength),
			"toolUseContentLengths":  toolLengthsJSON,
			"imageBlockCount":        req.input.ImageBlockCount,
			"imageTotalBytes":        req.input.ImageTotalBytes,
			"documentBlockCount":     req.input.DocumentBlockCount,
			"documentTotalBytes":     req.input.DocumentTotalBytes,
			"inputTextCharLength":    req.input.TextCharLength,
			"timeSinceLastApiCallMs": optionalBetween(req.startedAt, req.mutation.LastAPICallAt),
			"turn_origin":            turnOrigin(req),
		}), withDerived(mixedProvenance(
			[]string{"querySource", "queryChainId", "queryDepth", "turn_origin"},
			[]string{"model", "betas", "messageCount", "inputTokens", "outputTokens", "cachedInputTokens",
				"uncachedInputTokens", "durationMs", "attempt", "ttftMs", "firstContentMs", "provider",
				"requestId", "stop_reason", "previousRequestId", "permissionMode", "toolsCount",
				"toolsCharLength", "systemCharLength", "effort_level", "textContentLength",
				"thinkingContentLength", "toolUseContentLengths", "imageBlockCount", "imageTotalBytes",
				"documentBlockCount", "documentTotalBytes", "inputTextCharLength", "timeSinceLastApiCallMs"},
		), "buildAgeMins")),
	}
	if len(observation.ToolUses) > 0 {
		completionType := "tool_use_single"
		if len(observation.ToolUses) > 1 {
			completionType = "tool_use_multiple"
		}
		events = append(events, draft("tengu_unary_event", map[string]any{
			"event": "response", "completion_type": completionType,
			"language_name": "none", "message_id": observation.MessageID,
			"platform": req.context.Environment.Platform,
		}, observedFields("event", "completion_type", "language_name", "message_id", "platform")))
	}
	if req.title {
		events = append(events, draft("tengu_session_title_generated", map[string]any{"success": true}, observedFields("success")))
	} else if observation.StopReason == "end_turn" {
		events = append(events,
			draft("tengu_feature_ok", map[string]any{"feature_name": "turn"}, observedFields("feature_name")),
			draft("tengu_turn_end", compactMap(map[string]any{
				"terminal_reason": "completed", "is_error": false, "is_subagent": false,
				"goal_active": false, "duration_ms": optionalDuration(endedAt, req.turnStartedAt),
				"query_source":          req.querySource,
				"query_source_category": ternary(req.suggestion, "auxiliary", "main"),
			}), mixedProvenance(
				[]string{"duration_ms", "query_source", "query_source_category"},
				[]string{"terminal_reason", "is_error", "is_subagent", "goal_active"},
			)),
		)
	}
	return events
}

func draft(name string, metadata map[string]any, provenance FieldProvenance) eventDraft {
	return eventDraft{name: name, metadata: metadata, provenance: provenance}
}

func observedFields(fields ...string) FieldProvenance {
	out := make(FieldProvenance, len(fields))
	for _, field := range fields {
		out[field] = Observed
	}
	return out
}

func inferredFields(fields ...string) FieldProvenance {
	out := make(FieldProvenance, len(fields))
	for _, field := range fields {
		out[field] = Inferred
	}
	return out
}

func mixedProvenance(inferred, observed []string) FieldProvenance {
	out := observedFields(observed...)
	for _, field := range inferred {
		out[field] = Inferred
	}
	return out
}

func withDerived(provenance FieldProvenance, fields ...string) FieldProvenance {
	for _, field := range fields {
		provenance[field] = Derived
	}
	return provenance
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func temperature(body requestBody) float64 {
	if body.Temperature == nil {
		return 1
	}
	return *body.Temperature
}

func permissionMode(body requestBody, configured string) string {
	modes := map[string]struct{}{}
	for _, safeguard := range body.Safeguards {
		if safeguard.Type == "dangerous_tool_use" && safeguard.ClassifierContext.PermissionMode != "" {
			modes[safeguard.ClassifierContext.PermissionMode] = struct{}{}
		}
	}
	if len(modes) == 1 {
		for mode := range modes {
			return mode
		}
	}
	return configured
}

func latestPromptLength(body requestBody) int {
	user := lastUser(body)
	if user == nil {
		return 0
	}
	total := 0
	for _, block := range contentBlocks(user.Content) {
		if block.Type == "text" {
			total += utf16Length(block.Text)
		}
	}
	return total
}

func systemLength(body requestBody) int {
	total := 0
	for _, block := range systemBlocks(body.System) {
		total += utf16Length(block.Text)
	}
	return total
}

func toolDefinitionsLength(tools []tool) int {
	if len(tools) == 0 {
		return 0
	}
	return utf16Length(string(mustJSON(tools)))
}

func inspectInput(body requestBody) inputStats {
	var stats inputStats
	var scan func(json.RawMessage)
	scan = func(raw json.RawMessage) {
		for _, block := range contentBlocks(raw) {
			switch block.Type {
			case "text":
				stats.TextCharLength += utf16Length(block.Text)
			case "thinking":
				stats.TextCharLength += utf16Length(block.Thinking)
			case "tool_result":
				scan(block.Content)
			case "image":
				stats.ImageBlockCount++
				stats.ImageTotalBytes += base64ApproxBytes(block.Source.Data)
			case "document":
				stats.DocumentBlockCount++
				stats.DocumentTotalBytes += base64ApproxBytes(block.Source.Data)
			}
		}
	}
	for _, message := range body.Messages {
		scan(message.Content)
	}
	return stats
}

func base64ApproxBytes(value string) int {
	if value == "" {
		return 0
	}
	return len(value) * 3 / 4
}

type toolResultSummary struct {
	ID, Name    string
	IsMCP       bool
	IsError     bool
	ResultBytes int
	MediaBlocks int
	InputBytes  int
}

func summarizeToolResults(body requestBody) []toolResultSummary {
	type use struct {
		name  string
		bytes int
	}
	uses := make(map[string]use)
	for _, message := range body.Messages {
		if message.Role != "assistant" {
			continue
		}
		for _, block := range contentBlocks(message.Content) {
			if block.Type == "tool_use" && block.ID != "" {
				uses[block.ID] = use{name: block.Name, bytes: jsonLength(block.Input)}
			}
		}
	}
	user := lastUser(body)
	if user == nil {
		return nil
	}
	var out []toolResultSummary
	for _, block := range contentBlocks(user.Content) {
		if block.Type != "tool_result" {
			continue
		}
		related := uses[block.ToolUseID]
		media := 0
		for _, nested := range contentBlocks(block.Content) {
			if nested.Type == "image" || nested.Type == "document" {
				media++
			}
		}
		out = append(out, toolResultSummary{
			ID: block.ToolUseID, Name: related.name,
			IsMCP:   strings.HasPrefix(strings.ToLower(related.name), "mcp__"),
			IsError: block.IsError, ResultBytes: contentByteLength(block.Content),
			MediaBlocks: media, InputBytes: related.bytes,
		})
	}
	return out
}

func contentByteLength(raw json.RawMessage) int {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return len([]byte(text))
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		return len(mustJSON(value))
	}
	return len(raw)
}

func cacheMetadata(body requestBody, betas string) map[string]any {
	raw := mustJSON(body)
	var value any
	_ = json.Unmarshal(raw, &value)
	count := countCacheControls(value)
	return map[string]any{
		"totalMessageCount": len(body.Messages),
		"cachingEnabled":    count > 0 || strings.Contains(betas, "prompt-caching"),
		"markerCount":       max(1, countCacheControlsFromMessages(body.Messages)),
	}
}

func countCacheControlsFromMessages(messages []message) int {
	var value any
	_ = json.Unmarshal(mustJSON(messages), &value)
	return countCacheControls(value)
}

func countCacheControls(value any) int {
	switch typed := value.(type) {
	case []any:
		total := 0
		for _, item := range typed {
			total += countCacheControls(item)
		}
		return total
	case map[string]any:
		total := 0
		if _, ok := typed["cache_control"]; ok {
			total++
		}
		for _, item := range typed {
			total += countCacheControls(item)
		}
		return total
	default:
		return 0
	}
}

func optionalPositive(value int) any {
	if value > 0 {
		return value
	}
	return nil
}

func optionalDuration(end, start time.Time) any {
	if end.IsZero() || start.IsZero() {
		return nil
	}
	return end.Sub(start).Milliseconds()
}

func optionalBetween(end, start time.Time) any {
	return optionalDuration(end, start)
}

func turnOrigin(req *Request) any {
	if req.title || req.suggestion {
		return nil
	}
	return "human"
}

func ternary(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}

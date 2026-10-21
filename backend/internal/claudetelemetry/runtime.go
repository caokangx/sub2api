package claudetelemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

type SessionRuntime struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	now      func() time.Time
	sessions map[string]*sessionEntry
}

type sessionEntry struct {
	lastSeen time.Time
	main     queryState
	aux      map[string]*queryState
}

type queryState struct {
	PromptIndex         int
	TurnStartedAt       time.Time
	LastFingerprint     string
	ReportedToolResults map[string]struct{}
	QueryChainID        string
	QueryDepth          int
	PreviousRequestID   string
	PreviousMessageID   string
	LastAPICallAt       time.Time
}

type sessionMutation struct {
	Key               string
	QuerySource       string
	Fingerprint       string
	PromptIndex       int
	TurnStartedAt     time.Time
	QueryChainID      string
	QueryDepth        int
	PreviousRequestID string
	PreviousMessageID string
	LastAPICallAt     time.Time
	NewToolResultIDs  []string
	ResetToolResults  bool
}

func NewSessionRuntime(ttl time.Duration, capacity int) *SessionRuntime {
	if ttl <= 0 {
		ttl = DefaultConfig().SessionTTL
	}
	if capacity <= 0 {
		capacity = DefaultConfig().SessionCapacity
	}
	return &SessionRuntime{
		ttl: ttl, capacity: capacity, now: time.Now,
		sessions: make(map[string]*sessionEntry),
	}
}

func sessionKey(context EventContext) string {
	return strings.Join([]string{
		valueOr(context.Identity.OrganizationUUID, "unknown-org"),
		valueOr(context.Identity.AccountUUID, "unknown-account"),
		valueOr(context.Identity.SessionID, "unknown-session"),
	}, ":")
}

func (r *SessionRuntime) begin(key, source, fingerprint string, continuation, retryHeader bool, toolIDs []string) (sessionMutation, bool, map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	entry := r.sessions[key]
	if entry == nil {
		r.ensureCapacityLocked()
		entry = &sessionEntry{lastSeen: now, aux: make(map[string]*queryState)}
		r.sessions[key] = entry
	}
	entry.lastSeen = now
	state := &entry.main
	if source != "repl_main_thread" {
		state = entry.aux[source]
		if state == nil {
			state = &queryState{}
			entry.aux[source] = state
		}
	}
	retry := retryHeader && state.LastFingerprint == fingerprint
	mutation := sessionMutation{
		Key: key, QuerySource: source, Fingerprint: fingerprint,
		PromptIndex: state.PromptIndex, TurnStartedAt: state.TurnStartedAt,
		QueryChainID: state.QueryChainID, QueryDepth: state.QueryDepth,
		PreviousRequestID: state.PreviousRequestID,
		PreviousMessageID: state.PreviousMessageID,
		LastAPICallAt:     state.LastAPICallAt,
	}
	if state.ReportedToolResults == nil {
		state.ReportedToolResults = make(map[string]struct{})
	}
	seen := make(map[string]bool, len(toolIDs))
	for _, id := range toolIDs {
		_, seen[id] = state.ReportedToolResults[id]
	}
	if source == "repl_main_thread" && !retry && !continuation {
		mutation.PromptIndex++
		mutation.TurnStartedAt = now
		mutation.QueryChainID = randomUUID()
		mutation.QueryDepth = 0
		mutation.ResetToolResults = true
	} else if mutation.QueryChainID == "" && source != "generate_session_title" {
		mutation.QueryChainID = randomUUID()
	} else if continuation && !retry && source != "generate_session_title" {
		mutation.QueryDepth++
	}
	// Reserve request-order state at Begin rather than Complete. Multiple
	// in-flight requests for the same session must not receive the same prompt
	// index/query depth. Aborted internal attempts may leave gaps, which is
	// safer than duplicate native-looking prompt events.
	state.PromptIndex = mutation.PromptIndex
	state.TurnStartedAt = mutation.TurnStartedAt
	state.QueryChainID = mutation.QueryChainID
	state.QueryDepth = mutation.QueryDepth
	if mutation.ResetToolResults {
		state.ReportedToolResults = make(map[string]struct{})
	}
	return mutation, retry, seen
}

func (r *SessionRuntime) commit(m sessionMutation, requestID, messageID string, completedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.sessions[m.Key]
	if entry == nil {
		return
	}
	state := &entry.main
	if m.QuerySource != "repl_main_thread" {
		state = entry.aux[m.QuerySource]
		if state == nil {
			return
		}
	}
	state.LastFingerprint = m.Fingerprint
	if state.ReportedToolResults == nil {
		state.ReportedToolResults = make(map[string]struct{})
	}
	for _, id := range m.NewToolResultIDs {
		if id != "" {
			state.ReportedToolResults[id] = struct{}{}
		}
	}
	if requestID != "" {
		state.PreviousRequestID = requestID
	}
	if messageID != "" {
		state.PreviousMessageID = messageID
	}
	if !completedAt.IsZero() {
		state.LastAPICallAt = completedAt
	}
	entry.lastSeen = r.now()
}

func (r *SessionRuntime) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.now())
	return len(r.sessions)
}

func (r *SessionRuntime) pruneLocked(now time.Time) {
	for key, entry := range r.sessions {
		if now.Sub(entry.lastSeen) >= r.ttl {
			delete(r.sessions, key)
		}
	}
}

func (r *SessionRuntime) ensureCapacityLocked() {
	if len(r.sessions) < r.capacity {
		return
	}
	var oldestKey string
	var oldest time.Time
	for key, entry := range r.sessions {
		if oldestKey == "" || entry.lastSeen.Before(oldest) {
			oldestKey, oldest = key, entry.lastSeen
		}
	}
	delete(r.sessions, oldestKey)
}

type requestBody struct {
	Model        string          `json:"model"`
	Stream       bool            `json:"stream"`
	Messages     []message       `json:"messages"`
	System       json.RawMessage `json:"system"`
	Tools        []tool          `json:"tools"`
	Metadata     requestMetadata `json:"metadata"`
	QuerySource  string          `json:"query_source"`
	QuerySource2 string          `json:"querySource"`
	OutputConfig outputConfig    `json:"output_config"`
	Thinking     struct {
		Type string `json:"type"`
	} `json:"thinking"`
	Safeguards []struct {
		Type              string `json:"type"`
		ClassifierContext struct {
			PermissionMode string `json:"permission_mode"`
		} `json:"classifier_context"`
	} `json:"safeguards"`
	Temperature *float64 `json:"temperature"`
}

type requestMetadata struct {
	UserID       string `json:"user_id"`
	QuerySource  string `json:"query_source"`
	QuerySource2 string `json:"querySource"`
}

type outputConfig struct {
	Effort string          `json:"effort"`
	Format json.RawMessage `json:"format"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
	raw         json.RawMessage
}

func (t *tool) UnmarshalJSON(raw []byte) error {
	type wireTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	var decoded wireTool
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	t.Name = decoded.Name
	t.Description = decoded.Description
	t.InputSchema = decoded.InputSchema
	t.raw = append(t.raw[:0], raw...)
	return nil
}

func (t tool) MarshalJSON() ([]byte, error) {
	if len(t.raw) > 0 {
		return t.raw, nil
	}
	type wireTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	return json.Marshal(wireTool{
		Name: t.Name, Description: t.Description, InputSchema: t.InputSchema,
	})
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Source    struct {
		Data string `json:"data"`
	} `json:"source"`
	CacheControl *struct {
		Type string `json:"type"`
		TTL  string `json:"ttl"`
	} `json:"cache_control"`
}

type systemBlock struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	CacheControl *struct {
		Type string `json:"type"`
		TTL  string `json:"ttl"`
	} `json:"cache_control"`
}

func parseRequest(raw []byte) (requestBody, error) {
	var body requestBody
	err := json.Unmarshal(raw, &body)
	return body, err
}

func contentBlocks(raw json.RawMessage) []contentBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []contentBlock
	if raw[0] == '[' && json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []contentBlock{{Type: "text", Text: text}}
	}
	return nil
}

func systemBlocks(raw json.RawMessage) []systemBlock {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var blocks []systemBlock
	if raw[0] == '[' && json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []systemBlock{{Type: "text", Text: text}}
	}
	return nil
}

func explicitQuerySource(body requestBody) string {
	for _, value := range []string{body.QuerySource, body.QuerySource2, body.Metadata.QuerySource, body.Metadata.QuerySource2} {
		if value != "" {
			return value
		}
	}
	return ""
}

func classifyRequest(body requestBody, isClaudeCode bool) (title, suggestion bool, source string) {
	explicit := explicitQuerySource(body)
	if explicit == "generate_session_title" {
		return true, false, explicit
	}
	if explicit == "prompt_suggestion" {
		return false, true, explicit
	}
	if strictTitleSchema(body) && (isClaudeCode || hasStrongClaudeCodeSignal(body)) {
		return true, false, "generate_session_title"
	}
	return false, false, "repl_main_thread"
}

func strictTitleSchema(body requestBody) bool {
	if len(body.Tools) != 0 || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
		return false
	}
	var format struct {
		Type   string `json:"type"`
		Schema struct {
			Type                 string                     `json:"type"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
		} `json:"schema"`
	}
	if json.Unmarshal(body.OutputConfig.Format, &format) != nil ||
		format.Type != "json_schema" || format.Schema.Type != "object" ||
		len(format.Schema.Properties) != 1 || len(format.Schema.Required) != 1 ||
		format.Schema.Required[0] != "title" || format.Schema.AdditionalProperties == nil ||
		*format.Schema.AdditionalProperties {
		return false
	}
	var property struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(format.Schema.Properties["title"], &property) == nil && property.Type == "string"
}

func hasStrongClaudeCodeSignal(body requestBody) bool {
	for _, block := range systemBlocks(body.System) {
		lower := strings.ToLower(block.Text)
		if strings.Contains(lower, "x-anthropic-billing-header:") &&
			strings.Contains(lower, "cc_version=") {
			return true
		}
	}
	return false
}

func lastUser(body requestBody) *message {
	for index := len(body.Messages) - 1; index >= 0; index-- {
		if body.Messages[index].Role == "user" {
			return &body.Messages[index]
		}
	}
	return nil
}

func lastAssistant(body requestBody) *message {
	for index := len(body.Messages) - 1; index >= 0; index-- {
		if body.Messages[index].Role == "assistant" {
			return &body.Messages[index]
		}
	}
	return nil
}

func blockCount(message *message, kind string) int {
	if message == nil {
		return 0
	}
	count := 0
	for _, block := range contentBlocks(message.Content) {
		if block.Type == kind {
			count++
		}
	}
	return count
}

func requestFingerprint(body requestBody) string {
	sum := sha256.Sum256(mustJSON(map[string]any{
		"model": body.Model, "messages": body.Messages, "system": body.System,
		"tools": body.Tools, "thinking": body.Thinking, "output_config": body.OutputConfig,
	}))
	return hex.EncodeToString(sum[:])
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func sortedToolNames(body requestBody) []string {
	names := make([]string, 0, len(body.Tools))
	for _, tool := range body.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

package claudetelemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type ObserverMode string

const (
	ObserveSSE           ObserverMode = "sse"
	ObserveJSON          ObserverMode = "json"
	maxObservedJSONBytes              = 128 * 1024 * 1024
)

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type ObservedToolUse struct {
	ID          string
	Name        string
	InputLength int
}

type Observation struct {
	MessageID             string
	Model                 string
	StopReason            string
	Usage                 Usage
	FirstByteAt           time.Time
	FirstContentAt        time.Time
	TextContentLength     int
	ThinkingContentLength int
	ToolUses              map[int]ObservedToolUse
	Completed             bool
	StreamError           bool
}

func (o Observation) Successful() bool {
	return o.Completed && !o.StreamError && o.MessageID != "" && o.StopReason != ""
}

type Observer struct {
	mode      ObserverMode
	now       func() time.Time
	sseBuffer string
	jsonBody  bytes.Buffer
	result    Observation
	finished  bool
}

func NewObserver(mode ObserverMode) *Observer {
	return &Observer{
		mode: mode, now: time.Now,
		result: Observation{ToolUses: make(map[int]ObservedToolUse)},
	}
}

func (o *Observer) Observe(chunk []byte) error {
	if o.finished {
		return errors.New("telemetry observer already finished")
	}
	if len(chunk) == 0 {
		return nil
	}
	if o.result.FirstByteAt.IsZero() {
		o.result.FirstByteAt = o.now()
	}
	if o.mode == ObserveJSON {
		if o.jsonBody.Len()+len(chunk) > maxObservedJSONBytes {
			return errors.New("telemetry JSON observation exceeds 128 MiB")
		}
		_, _ = o.jsonBody.Write(chunk)
		return nil
	}
	o.sseBuffer += string(chunk)
	for {
		index, width := sseBoundary(o.sseBuffer)
		if index < 0 {
			break
		}
		o.consumeSSEFrame(o.sseBuffer[:index])
		o.sseBuffer = o.sseBuffer[index+width:]
	}
	return nil
}

// ObserveEventJSON consumes one raw upstream Anthropic JSON event. The caller
// may use this while already parsing SSE so telemetry never needs to wrap,
// buffer, or alter bytes written to the downstream client.
func (o *Observer) ObserveEventJSON(raw []byte) error {
	if o.finished {
		return errors.New("telemetry observer already finished")
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
		return nil
	}
	if o.result.FirstByteAt.IsZero() {
		o.result.FirstByteAt = o.now()
	}
	var event streamEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil
	}
	o.consumeEvent(event)
	return nil
}

func (o *Observer) Finish() Observation {
	if o.finished {
		return cloneObservation(o.result)
	}
	o.finished = true
	if o.mode == ObserveSSE {
		if strings.TrimSpace(o.sseBuffer) != "" {
			o.consumeSSEFrame(o.sseBuffer)
		}
		o.sseBuffer = ""
	} else {
		o.consumeJSONMessage(o.jsonBody.Bytes())
		o.jsonBody.Reset()
	}
	return cloneObservation(o.result)
}

func (o *Observer) Snapshot() Observation {
	return cloneObservation(o.result)
}

func sseBoundary(value string) (int, int) {
	crlf := strings.Index(value, "\r\n\r\n")
	lf := strings.Index(value, "\n\n")
	switch {
	case crlf >= 0 && (lf < 0 || crlf <= lf):
		return crlf, 4
	case lf >= 0:
		return lf, 2
	default:
		return -1, 0
	}
}

func (o *Observer) consumeSSEFrame(frame string) {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(frame, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(lines) == 0 || strings.Join(lines, "\n") == "[DONE]" {
		return
	}
	var event streamEvent
	if json.Unmarshal([]byte(strings.Join(lines, "\n")), &event) == nil {
		o.consumeEvent(event)
	}
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Usage      Usage  `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
	} `json:"content_block"`
	Delta *struct {
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage Usage           `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func (o *Observer) consumeEvent(event streamEvent) {
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			o.result.MessageID = valueOr(event.Message.ID, o.result.MessageID)
			o.result.Model = valueOr(event.Message.Model, o.result.Model)
			o.result.StopReason = valueOr(event.Message.StopReason, o.result.StopReason)
			mergeUsage(&o.result.Usage, event.Message.Usage)
		}
	case "content_block_start":
		if event.ContentBlock == nil {
			return
		}
		switch event.ContentBlock.Type {
		case "tool_use":
			o.result.ToolUses[event.Index] = ObservedToolUse{
				ID: event.ContentBlock.ID, Name: event.ContentBlock.Name,
				InputLength: jsonTextLength(event.ContentBlock.Input),
			}
		case "text":
			o.markContent(utf16Length(event.ContentBlock.Text), false)
		case "thinking":
			o.markContent(utf16Length(event.ContentBlock.Thinking), true)
		}
	case "content_block_delta":
		if event.Delta == nil {
			return
		}
		o.markContent(utf16Length(event.Delta.Text), false)
		o.markContent(utf16Length(event.Delta.Thinking), true)
		if event.Delta.PartialJSON != "" {
			tool := o.result.ToolUses[event.Index]
			tool.InputLength += utf16Length(event.Delta.PartialJSON)
			o.result.ToolUses[event.Index] = tool
			o.markFirstContent()
		}
	case "message_delta":
		if event.Delta != nil {
			o.result.StopReason = valueOr(event.Delta.StopReason, o.result.StopReason)
		}
		mergeUsage(&o.result.Usage, event.Usage)
	case "message_stop":
		o.result.Completed = true
	case "error":
		o.result.StreamError = true
	}
}

func (o *Observer) consumeJSONMessage(raw []byte) {
	var message struct {
		Type       string          `json:"type"`
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Usage      Usage           `json:"usage"`
		Content    []contentBlock  `json:"content"`
		Error      json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return
	}
	o.result.MessageID = message.ID
	o.result.Model = message.Model
	o.result.StopReason = message.StopReason
	o.result.Usage = message.Usage
	o.result.StreamError = len(message.Error) > 0 && string(message.Error) != "null"
	o.result.Completed = message.Type == "message" && message.ID != "" &&
		message.StopReason != "" && message.Content != nil && !o.result.StreamError
	for index, block := range message.Content {
		switch block.Type {
		case "text":
			o.markContent(utf16Length(block.Text), false)
		case "thinking":
			o.markContent(utf16Length(block.Thinking), true)
		case "tool_use":
			o.result.ToolUses[index] = ObservedToolUse{
				ID: block.ID, Name: block.Name, InputLength: jsonTextLength(block.Input),
			}
		}
	}
}

func (o *Observer) markContent(length int, thinking bool) {
	if length <= 0 {
		return
	}
	o.markFirstContent()
	if thinking {
		o.result.ThinkingContentLength += length
	} else {
		o.result.TextContentLength += length
	}
}

func (o *Observer) markFirstContent() {
	if o.result.FirstContentAt.IsZero() {
		o.result.FirstContentAt = o.now()
	}
}

func mergeUsage(target *Usage, update Usage) {
	if update.InputTokens != 0 {
		target.InputTokens = update.InputTokens
	}
	if update.OutputTokens != 0 {
		target.OutputTokens = update.OutputTokens
	}
	if update.CacheReadInputTokens != 0 {
		target.CacheReadInputTokens = update.CacheReadInputTokens
	}
	if update.CacheCreationInputTokens != 0 {
		target.CacheCreationInputTokens = update.CacheCreationInputTokens
	}
}

func jsonLength(raw json.RawMessage) int {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return len("{}")
	}
	return len(raw)
}

func jsonTextLength(raw json.RawMessage) int {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 2
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		canonical, _ := json.Marshal(value)
		return utf16Length(string(canonical))
	}
	return utf16Length(string(raw))
}

func cloneObservation(in Observation) Observation {
	out := in
	out.ToolUses = make(map[int]ObservedToolUse, len(in.ToolUses))
	for index, tool := range in.ToolUses {
		out.ToolUses[index] = tool
	}
	return out
}

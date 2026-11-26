//go:build unit

package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func runVertexVerify(t *testing.T, path string, up *azureVerifyFakeUpstream, payload any) (*httptest.ResponseRecorder, json.RawMessage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewVertexVerifyHandler(&config.Config{}, up)
	r := gin.New()
	r.POST("/models", h.Models)
	r.POST("/probe", h.Probe)

	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w, env.Data
}

func vertexReply(text string, promptTokens int, extraUsage string, thought string) string {
	parts := `[{"text":` + strconvQuote(text) + `}]`
	if thought != "" {
		parts = `[{"text":` + strconvQuote(thought) + `,"thought":true},{"text":` + strconvQuote(text) + `}]`
	}
	return `{"candidates":[{"content":{"parts":` + parts + `,"role":"model"},"finishReason":"STOP"}],` +
		`"usageMetadata":{"promptTokenCount":` + itoa(promptTokens) + `,"candidatesTokenCount":2,"trafficType":"ON_DEMAND"` + extraUsage + `},` +
		`"modelVersion":"gemini-4-hydrogen","createTime":"2026-11-25T09:32:59.336023Z","responseId":"S6sGa5fBFPi5sbwP6JesiAI"}`
}

func strconvQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func googleError(code int, status, msg string) string {
	return `{"error":{"code":` + itoa(code) + `,"message":` + strconvQuote(msg) + `,"status":"` + status + `"}}`
}

// vertexLikeUpstream answers like 192.6.47.3:3000 gemini-4-hydrogen (real Vertex) did.
func vertexLikeUpstream() *azureVerifyFakeUpstream {
	return &azureVerifyFakeUpstream{respond: func(req *http.Request, body string) (int, http.Header, string) {
		switch {
		case strings.Contains(body, `"thinkingBudget":1024,"thinkingLevel"`):
			return http.StatusBadRequest, nil, googleError(400, "INVALID_ARGUMENT", "Unable to submit request because thinking_budget and thinking_level are not supported together.. Learn more: https://cloud.google.com/vertex-ai/generative-ai/docs/thinking")
		case strings.Contains(body, `"thinkingBudget":1024`):
			return http.StatusBadRequest, nil, googleError(400, "INVALID_ARGUMENT", "Thinking budget is not supported for this model. Use thinking level instead.")
		case strings.Contains(body, `"thinkingLevel":"MINIMAL"`):
			return http.StatusBadRequest, nil, googleError(400, "INVALID_ARGUMENT", "Thinking level is unsupported: THINKING_LEVEL_MINIMAL")
		case strings.Contains(body, `gs://`):
			return http.StatusNotFound, nil, googleError(404, "NOT_FOUND", "No such object: cloud-samples-data/generative-ai/image/sub2api-vertex-verify-missing.jpg")
		case strings.Contains(body, `"includeThoughts":true`):
			return http.StatusOK, nil, vertexReply("4227", 56, `,"thoughtsTokenCount":2544`, "**Summing primes**")
		case strings.Contains(body, "today's date"):
			return http.StatusOK, nil, vertexReply("UNKNOWN", 62, "", "")
		case strings.Contains(body, "YES/NO"):
			return http.StatusOK, nil, vertexReply("1. NO\n9. YES — EFFORT LEVEL: 0.50\n10. NO", 138, "", "")
		default:
			return http.StatusOK, nil, vertexReply("OK", 36, "", "")
		}
	}}
}

func TestVertexVerifyProbeDetectsVertex(t *testing.T) {
	up := vertexLikeUpstream()
	w, data := runVertexVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com/v1beta",
		"api_key":  "  k  ",
		"model":    "gemini-4-hydrogen",
	})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.VertexVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.UpstreamVerifyStatusOK, res.Status)
	require.True(t, res.IsVertex, "%+v", res.Evidences)
	require.Equal(t, service.VertexVerifySourceVertex, res.DetectedSource)
	require.Empty(t, res.Notes)
	require.NotNil(t, res.Thinking)
	require.EqualValues(t, 2544, res.Thinking.ThoughtsTokens)
	require.Equal(t, 1, res.Thinking.ThoughtParts)
	require.True(t, res.Thinking.AnswerCorrect)

	// basic, date, persona, gcs, thinking + thinkingBudget, MINIMAL and both-params for hydrogen.
	require.Len(t, up.requests, 8)
	for _, req := range up.requests {
		require.Equal(t, "/v1beta/models/gemini-4-hydrogen:generateContent", req.URL.Path)
		require.Equal(t, "k", req.Header.Get("x-goog-api-key"))
		require.Equal(t, "Bearer k", req.Header.Get("Authorization"))
	}
}

func TestVertexVerifyProbeFlagsRelayThatAcceptsEverything(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(_ *http.Request, body string) (int, http.Header, string) {
		h := http.Header{}
		h.Set("X-Oneapi-Request-Id", "202611251145012950334838268d9d6MQi67QHJ")
		if strings.Contains(body, `"includeThoughts":true`) {
			return http.StatusOK, h, `{"candidates":[{"content":{"parts":[{"text":"4227"}]}}],"usageMetadata":{"promptTokenCount":26,"candidatesTokenCount":10033},"responseId":"gem-9e479c2724d849609275"}`
		}
		return http.StatusOK, h, `{"candidates":[{"content":{"parts":[{"text":"OK"}]}}],"usageMetadata":{"promptTokenCount":6,"candidatesTokenCount":1},"responseId":"gem-b7c121e898e04c528053"}`
	}}
	_, data := runVertexVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com",
		"api_key":  "k",
		"model":    "gemini-3.1-pro-preview",
	})

	var res service.VertexVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.UpstreamVerifyStatusOK, res.Status)
	require.False(t, res.IsVertex)
	require.Equal(t, service.VertexVerifySourceRelay, res.DetectedSource)
	require.Contains(t, res.Notes, service.VertexVerifyNoteThinkingMerged)
	// 3.1 Pro gets MINIMAL, both-params and XHIGH validation probes.
	require.Len(t, up.requests, 8)
	for _, step := range res.Steps {
		if step.Name == service.VertexVerifyStepThinkingBoth {
			require.False(t, step.OK)
			require.Contains(t, step.Error, "INVALID_ARGUMENT")
		}
	}
}

func TestVertexVerifyProbeReportsUnavailableModel(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(*http.Request, string) (int, http.Header, string) {
		return http.StatusNotFound, nil, `{"error":{"code":404,"message":"Model \"gemini-4-neon\" is not supported by any configured account in this group","status":"NOT_FOUND"}}`
	}}
	w, data := runVertexVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com",
		"api_key":  "k",
		"model":    "gemini-4-neon",
	})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.VertexVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.UpstreamVerifyStatusFailed, res.Status)
	require.True(t, res.ModelUnavailable)
	require.Equal(t, `HTTP 404: NOT_FOUND: Model "gemini-4-neon" is not supported by any configured account in this group`, res.Error)
}

func TestVertexVerifyModelsFallsBackToNativeList(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(req *http.Request, _ string) (int, http.Header, string) {
		require.Equal(t, http.MethodGet, req.Method)
		switch req.URL.Path {
		case "/v1/models":
			return http.StatusOK, nil, `{"data":[],"object":"list"}`
		case "/v1beta/models":
			require.Equal(t, "k", req.Header.Get("x-goog-api-key"))
			return http.StatusOK, nil, `{"models":[{"name":"models/gemini-4-hydrogen"},{"name":"models/gemini-3.1-pro-preview"}]}`
		}
		return http.StatusNotFound, nil, `{}`
	}}
	w, data := runVertexVerify(t, "/models", up, map[string]any{"base_url": "https://relay.example.com", "api_key": "k"})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.UpstreamVerifyModelsResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, []string{"gemini-4-hydrogen", "gemini-3.1-pro-preview"}, res.Models)
	require.Empty(t, res.Error)
}

func TestVertexVerifyModelsKeepsOriginalErrorWhenBothListsEmpty(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(*http.Request, string) (int, http.Header, string) {
		return http.StatusOK, nil, `{"data":[]}`
	}}
	_, data := runVertexVerify(t, "/models", up, map[string]any{"base_url": "https://relay.example.com", "api_key": "k"})

	var res service.UpstreamVerifyModelsResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Empty(t, res.Models)
	require.Equal(t, "upstream returned no models", res.Error)
	require.Len(t, up.requests, 2)
}

func TestVertexVerifyRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		path    string
		payload map[string]any
	}{
		"private host":  {"/models", map[string]any{"base_url": "https://10.0.0.1", "api_key": "k"}},
		"insecure http": {"/probe", map[string]any{"base_url": "http://relay.example.com", "api_key": "k", "model": "m"}},
		"blank key":     {"/models", map[string]any{"base_url": "https://relay.example.com", "api_key": " "}},
		"blank model":   {"/probe", map[string]any{"base_url": "https://relay.example.com", "api_key": "k", "model": " "}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			up := &azureVerifyFakeUpstream{respond: func(*http.Request, string) (int, http.Header, string) {
				return http.StatusOK, nil, `{}`
			}}
			w, _ := runVertexVerify(t, tc.path, up, tc.payload)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Empty(t, up.requests)
		})
	}
}

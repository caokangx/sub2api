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

func runBedrockVerify(t *testing.T, path string, up *azureVerifyFakeUpstream, payload any) (*httptest.ResponseRecorder, json.RawMessage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewBedrockVerifyHandler(&config.Config{}, up)
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

// bedrockLikeUpstream mimics a new-api relay in front of Bedrock for a model that
// rejects forced tool_choice (claude-sonnet-5-5 on 84.32.61.162:3000).
func bedrockLikeUpstream() *azureVerifyFakeUpstream {
	return &azureVerifyFakeUpstream{respond: func(req *http.Request, body string) (int, http.Header, string) {
		h := http.Header{}
		h.Set("X-Oneapi-Request-Id", "202611050836047487683158268d9d6Yz6j0pFR")
		switch {
		case strings.Contains(body, `"stream":true`):
			return http.StatusOK, h, "event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_bdrk_przdnsjuk5be6qscvbb7m3f7jii4g","type":"message","role":"assistant","content":[]}}` + "\n\n"
		case strings.Contains(body, `"tool_choice":{"name":"get_weather","type":"tool"}`):
			return http.StatusBadRequest, h, `{"type":"error","error":{"type":"aws_invoke_error","message":"InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 4bcea973, ValidationException: tool_choice: type \"tool\" and \"any\" are not supported for this model."}}`
		case strings.Contains(body, `"tool_choice":{"type":"auto"}`):
			return http.StatusOK, h, `{"id":"msg_bdrk_tool1","type":"message","content":[{"type":"tool_use","id":"toolu_bdrk_01Em2RBgy1bpkBm7onxpSdZ5","name":"get_weather","input":{"city":"北京"}}],"usage":{"input_tokens":400,"output_tokens":40}}`
		default:
			return http.StatusOK, h, `{"id":"msg_bdrk_dload2bakidnxonutpvyfr6pyhbpfjnkvccobu7bq","type":"message","content":[{"type":"text","text":"长城"}],"usage":{"input_tokens":24,"output_tokens":16}}`
		}
	}}
}

func TestBedrockVerifyProbeDetectsBedrockAndRetriesForcedTool(t *testing.T) {
	up := bedrockLikeUpstream()
	w, data := runBedrockVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com/v1",
		"api_key":  "  sk-test  ",
		"model":    "claude-sonnet-5-5",
	})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.BedrockVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.UpstreamVerifyStatusOK, res.Status)
	require.True(t, res.IsBedrock)
	require.False(t, res.Suspicious)
	require.Equal(t, service.BedrockVerifySourceBedrock, res.DetectedSource)
	require.Equal(t, []string{service.BedrockVerifyNoteForcedToolUnsupported}, res.Notes)
	require.Len(t, res.Steps, 3)
	for _, step := range res.Steps {
		require.True(t, step.OK, step.Name)
	}
	require.Contains(t, res.Steps[0].Headers, "x-oneapi-request-id")

	// message, forced tool, auto retry, stream
	require.Len(t, up.requests, 4)
	for _, req := range up.requests {
		require.Equal(t, "relay.example.com", req.URL.Host)
		require.Equal(t, "/v1/messages", req.URL.Path)
		require.Equal(t, "sk-test", req.Header.Get("x-api-key"))
		require.Equal(t, "Bearer sk-test", req.Header.Get("Authorization"))
		require.Equal(t, "2023-06-01", req.Header.Get("anthropic-version"))
		require.True(t, service.HTTPUpstreamRedirectsDisabled(req.Context()))
		require.True(t, service.HTTPUpstreamPublicHostsOnly(req.Context()))
	}
	require.Contains(t, up.bodies[2], `"tool_choice":{"type":"auto"}`)
}

func TestBedrockVerifyProbeReportsOfficialAnthropic(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(_ *http.Request, body string) (int, http.Header, string) {
		h := http.Header{}
		h.Set("request-id", "req_011CgjUcfKijU")
		if strings.Contains(body, `"stream":true`) {
			return http.StatusOK, h, `data: {"type":"message_start","message":{"id":"msg_01AbC"}}` + "\n\n"
		}
		if strings.Contains(body, `"tools"`) {
			return http.StatusOK, h, `{"id":"msg_01Def","content":[{"type":"tool_use","id":"toolu_01XyZ"}]}`
		}
		return http.StatusOK, h, `{"id":"msg_01Ghi","content":[],"usage":{"input_tokens":24}}`
	}}
	_, data := runBedrockVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com",
		"api_key":  "k",
		"model":    "claude-sonnet-4-6",
	})

	var res service.BedrockVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.UpstreamVerifyStatusOK, res.Status)
	require.False(t, res.IsBedrock)
	require.Equal(t, service.BedrockVerifySourceAnthropic, res.DetectedSource)
	require.Empty(t, res.Notes)
	// Forced tool_choice succeeded, so there is no auto retry.
	require.Len(t, up.requests, 3)
}

func TestBedrockVerifyProbeReportsUnavailableModel(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(*http.Request, string) (int, http.Header, string) {
		return http.StatusServiceUnavailable, nil, `{"error":{"code":"model_not_found","message":"分组 Azure高级 下模型 claude-opus-4-8 无可用渠道（distributor）","type":"new_api_error"}}`
	}}
	w, data := runBedrockVerify(t, "/probe", up, map[string]any{
		"base_url": "https://relay.example.com",
		"api_key":  "k",
		"model":    "claude-opus-4-8",
	})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.BedrockVerifyProbeResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, service.UpstreamVerifyStatusFailed, res.Status)
	require.True(t, res.ModelUnavailable)
	require.Contains(t, res.Error, "无可用渠道")
}

func TestBedrockVerifyModelsUsesAnthropicAuth(t *testing.T) {
	up := &azureVerifyFakeUpstream{respond: func(req *http.Request, _ string) (int, http.Header, string) {
		require.Equal(t, "/v1/models", req.URL.Path)
		require.Equal(t, "k", req.Header.Get("x-api-key"))
		return http.StatusOK, nil, `{"data":[{"id":"claude-sonnet-5-5"},{"id":"claude-opus-4-8"}]}`
	}}
	w, data := runBedrockVerify(t, "/models", up, map[string]any{"base_url": "https://relay.example.com", "api_key": "k"})
	require.Equal(t, http.StatusOK, w.Code)

	var res service.UpstreamVerifyModelsResult
	require.NoError(t, json.Unmarshal(data, &res))
	require.Equal(t, []string{"claude-sonnet-5-5", "claude-opus-4-8"}, res.Models)
}

func TestBedrockVerifyRejectsBadInput(t *testing.T) {
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
			w, _ := runBedrockVerify(t, tc.path, up, tc.payload)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Empty(t, up.requests)
		})
	}
}

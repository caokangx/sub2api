//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func azureVerifyHeaders(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestEvaluateAzureEvidence(t *testing.T) {
	cases := []struct {
		name            string
		obs             azureVerifyObservation
		wantStatus      string
		wantAzure       bool
		wantSuspicious  bool
		wantUnavailable bool
		wantServed      string
	}{
		{
			// api.vakv.cn: Azure headers, filter fields and the empty filter chunk all pass through.
			name: "full azure passthrough",
			obs: azureVerifyObservation{
				Model: "gpt-6-sol",
				Headers: []http.Header{azureVerifyHeaders(
					"apim-request-id", "24895689-0741-4b93-b9e0-6b1a90a4f62b",
					"azureml-model-session", "d20261028071325-baf495db",
					"x-ms-region", "East US 2",
					"x-ms-served-model", "gpt-6-sol-2026-09-22",
					"x-new-api-version", "v1.0.0-rc.47",
				)},
				ChatOK: true, ChatFinishReason: "length", ChatPromptTokens: 17, ChatOutputTokens: 64,
				ChatFilterFields: true, ChatModel: "gpt-6-sol-2026-09-22",
				StreamOK: true, StreamAzureChunk: true,
				ContinuationTried: true, ContinuationOK: true,
			},
			wantStatus: AzureVerifyStatusOK, wantAzure: true, wantServed: "gpt-6-sol-2026-09-22",
		},
		{
			// b.kylenqaq.com / metacode.cc: filter fields stripped, Azure headers kept.
			name: "azure headers only",
			obs: azureVerifyObservation{
				Model:   "gpt-6-astra",
				Headers: []http.Header{azureVerifyHeaders("apim-request-id", "43649dca", "x-ms-region", "East US 2", "x-ms-rai-invoked", "true")},
				ChatOK:  true, ChatFinishReason: "length", ChatPromptTokens: 17, ChatOutputTokens: 64,
				StreamOK: true, ContinuationTried: true, ContinuationOK: true,
			},
			wantStatus: AzureVerifyStatusOK, wantAzure: true,
		},
		{
			// api.xiaojue.cloud: no Azure markers, length limit ignored, ~4.4k injected prompt tokens.
			name: "subscription relay",
			obs: azureVerifyObservation{
				Model: "gpt-6-astra",
				Headers: []http.Header{azureVerifyHeaders(
					"x-request-id", "2e3f9204-01aa-4c2a-87e9-84e30bb6f1a7",
					"x-request-id", "2e58bfe5-5701-4ec6-86c5-1ed8504cfc83",
				)},
				ChatOK: true, ChatFinishReason: "stop", ChatPromptTokens: 4399, ChatOutputTokens: 629,
				StreamOK: true,
			},
			wantStatus: AzureVerifyStatusOK,
		},
		{
			name: "azure headers but behaves like a subscription relay",
			obs: azureVerifyObservation{
				Model:   "gpt-6-sol",
				Headers: []http.Header{azureVerifyHeaders("apim-request-id", "fake")},
				ChatOK:  true, ChatFinishReason: "stop", ChatPromptTokens: 1056, ChatOutputTokens: 300,
			},
			wantStatus: AzureVerifyStatusOK, wantSuspicious: true,
		},
		{
			name: "codex headers",
			obs: azureVerifyObservation{
				Model:   "gpt-6-sol",
				Headers: []http.Header{azureVerifyHeaders("x-codex-primary-used-percent", "12")},
				ChatOK:  true, ChatFinishReason: "length", ChatPromptTokens: 17, ChatOutputTokens: 64,
			},
			wantStatus: AzureVerifyStatusOK,
		},
		{
			name: "no available channel",
			obs: azureVerifyObservation{
				Model:  "gpt-6.1-terra",
				Errors: []string{"HTTP 503: No available channel for model gpt-6.1-terra under group az-gpt-新渠道 (distributor)"},
			},
			wantStatus: AzureVerifyStatusFailed, wantUnavailable: true,
		},
		{
			name: "chinese no channel message",
			obs: azureVerifyObservation{
				Model:  "gpt-6-luna",
				Errors: []string{"HTTP 503: 分组 Azure高级 下模型 gpt-6-luna 无可用渠道（distributor）"},
			},
			wantStatus: AzureVerifyStatusFailed, wantUnavailable: true,
		},
		{
			name: "generic upstream failure",
			obs: azureVerifyObservation{
				Model:  "gpt-6",
				Errors: []string{"HTTP 502: Upstream service temporarily unavailable. Please retry later."},
			},
			wantStatus: AzureVerifyStatusFailed,
		},
		{
			// Every request failed, but the rate-limit message itself is Azure-specific.
			name: "azure regional rate limit only",
			obs: azureVerifyObservation{
				Model:  "gpt-6-astra",
				Errors: []string{"HTTP 429: Your requests to gpt-6-astra for gpt-6-astra in eastus2 have exceeded token rate limit."},
			},
			wantStatus: AzureVerifyStatusOK, wantAzure: true,
		},
		{
			name: "cross resource continuation error",
			obs: azureVerifyObservation{
				Model:  "gpt-6.1-sol",
				ChatOK: true, ChatFinishReason: "length", ChatPromptTokens: 17, ChatOutputTokens: 64,
				ContinuationTried: true,
				Errors:            []string{"HTTP 400: The requested item was created under a different Azure OpenAI resource."},
			},
			wantStatus: AzureVerifyStatusOK, wantAzure: true,
		},
		{
			name: "group name mentioning azure is not evidence",
			obs: azureVerifyObservation{
				Model:  "gpt-6-luna",
				ChatOK: true, ChatFinishReason: "length", ChatPromptTokens: 17, ChatOutputTokens: 64,
				Errors: []string{"HTTP 503: 分组 Azure高级 下模型 gpt-6-luna 无可用渠道（distributor）"},
			},
			wantStatus: AzureVerifyStatusOK,
		},
		{
			name: "usage limit reached",
			obs: azureVerifyObservation{
				Model:  "gpt-6-sol",
				Errors: []string{`HTTP 429: {"type":"usage_limit_reached","resets_in_seconds":100}`},
			},
			wantStatus: AzureVerifyStatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evaluateAzureEvidence(tc.obs)
			require.Equal(t, tc.wantStatus, v.Status)
			require.Equal(t, tc.wantAzure, v.IsAzure)
			require.Equal(t, tc.wantSuspicious, v.Suspicious)
			require.Equal(t, tc.wantUnavailable, v.ModelUnavailable)
			require.Equal(t, tc.wantServed, v.ServedModel)
		})
	}
}

func TestAzureVerifyEndpoint(t *testing.T) {
	require.Equal(t, "https://a.example.com/v1/models", azureVerifyEndpoint("https://a.example.com", "/v1/models"))
	require.Equal(t, "https://a.example.com/v1/models", azureVerifyEndpoint("https://a.example.com/", "/v1/models"))
	require.Equal(t, "https://a.example.com/v1/models", azureVerifyEndpoint("https://a.example.com/v1/", "/v1/models"))
	require.Equal(t, "https://a.example.com/api/v1/models", azureVerifyEndpoint("https://a.example.com/api", "/v1/models"))
}

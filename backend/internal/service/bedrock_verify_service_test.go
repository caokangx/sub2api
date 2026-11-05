//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluateBedrockEvidence(t *testing.T) {
	const awsForcedToolErr = `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 4bcea973-f19e-453c-a7b1-82c84903feda, ValidationException: tool_choice: type "tool" and "any" are not supported for this model. (aws_invoke_error)`

	cases := []struct {
		name            string
		obs             bedrockVerifyObservation
		wantStatus      string
		wantBedrock     bool
		wantSuspicious  bool
		wantUnavailable bool
		wantSource      string
		wantNotes       []string
	}{
		{
			// 84.32.61.162:3000 claude-sonnet-4-6: x-amzn-* headers stripped, ids pass through.
			name: "bedrock ids behind new-api",
			obs: bedrockVerifyObservation{
				Model:     "claude-sonnet-4-6",
				Headers:   []http.Header{azureVerifyHeaders("X-Oneapi-Request-Id", "202611050835330375896128268d9d6SckIWYIL")},
				MessageOK: true, InputTokens: 24, ToolOK: true, StreamOK: true,
				MessageIDs: []string{
					"msg_bdrk_ies6fu62e4lpxdvumcsclenhxamv6fh32qjhcpsozkhmnzist2yq",
					"msg_bdrk_dltzzlkr3q2kzv2sv7hjlahx55hyv7rxo2nji2itetjo3kym45oq",
					"msg_bdrk_jahijji2ruvc23lpraui4cu22qo4uj66km4smy7t3nokrger2otq",
				},
				ToolUseIDs: []string{"toolu_bdrk_01TY9scnnM11jL3QhbgESz4J"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
		},
		{
			// 84.32.61.162:3000 claude-sonnet-5-5: forced tool rejected with the raw AWS error, auto retry worked.
			name: "forced tool rejected by bedrock",
			obs: bedrockVerifyObservation{
				Model:     "claude-sonnet-5-5",
				MessageOK: true, InputTokens: 24, ToolOK: true, StreamOK: true,
				MessageIDs:         []string{"msg_bdrk_dload2bakidnxonutpvyfr6pyhbpfjnkvccobu7bqyo23l3hgooa"},
				ToolUseIDs:         []string{"toolu_bdrk_01Em2RBgy1bpkBm7onxpSdZ5"},
				ForcedToolRejected: true,
				Errors:             []string{awsForcedToolErr},
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
			wantNotes: []string{BedrockVerifyNoteForcedToolUnsupported},
		},
		{
			name: "bedrock headers passed through",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-4-7",
				Headers:   []http.Header{azureVerifyHeaders("x-amzn-requestid", "319a6902-a32a-4e70-b1f9-2a8579562fe4", "x-amzn-bedrock-invocation-latency", "812")},
				MessageOK: true, InputTokens: 24,
				MessageIDs: []string{"msg_rewritten_by_relay"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
		},
		{
			name: "vertex",
			obs: bedrockVerifyObservation{
				Model:     "claude-sonnet-4-6",
				MessageOK: true, InputTokens: 24, ToolOK: true,
				MessageIDs: []string{"msg_vrtx_01AbCdEf"},
				ToolUseIDs: []string{"toolu_vrtx_01AbCdEf"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: BedrockVerifySourceVertex,
		},
		{
			name: "anthropic official api",
			obs: bedrockVerifyObservation{
				Model:     "claude-sonnet-4-6",
				Headers:   []http.Header{azureVerifyHeaders("request-id", "req_011CgjUcfKijU", "anthropic-organization-id", "org-1")},
				MessageOK: true, InputTokens: 24,
				MessageIDs: []string{"msg_01XyZ"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: BedrockVerifySourceAnthropic,
		},
		{
			name: "claude subscription relay",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-4-7",
				Headers:   []http.Header{azureVerifyHeaders("anthropic-ratelimit-unified-5h-utilization", "0.12", "anthropic-ratelimit-unified-7d-utilization", "0.40")},
				MessageOK: true, InputTokens: 3120,
				MessageIDs: []string{"msg_01XyZ"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: BedrockVerifySourceSubscription,
		},
		{
			name: "bedrock ids but injected prompt",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-4-7",
				MessageOK: true, InputTokens: 4100,
				MessageIDs: []string{"msg_bdrk_abc"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSuspicious: true, wantSource: BedrockVerifySourceBedrock,
		},
		{
			name: "group name mentioning bedrock is not evidence",
			obs: bedrockVerifyObservation{
				Model:  "claude-opus-4-8",
				Errors: []string{"HTTP 503: model_not_found: 分组 bedrock-aws 下模型 claude-opus-4-8 无可用渠道（distributor）"},
			},
			wantStatus: UpstreamVerifyStatusFailed, wantUnavailable: true, wantSource: BedrockVerifySourceUnknown,
		},
		{
			name: "anthropic not_found_error",
			obs: bedrockVerifyObservation{
				Model:  "claude-nope",
				Errors: []string{"HTTP 404: model: claude-nope (not_found_error)"},
			},
			wantStatus: UpstreamVerifyStatusFailed, wantUnavailable: true, wantSource: BedrockVerifySourceUnknown,
		},
		{
			// Every request failed, but the throttling message itself is Bedrock-specific.
			name: "bedrock throttling only",
			obs: bedrockVerifyObservation{
				Model:  "claude-opus-5-5",
				Errors: []string{"HTTP 429: Too many tokens, please wait before trying again."},
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
		},
		{
			name: "rewritten ids and no other signal",
			obs: bedrockVerifyObservation{
				Model:     "claude-sonnet-4-6",
				MessageOK: true, InputTokens: 24,
				MessageIDs: []string{"chatcmpl-123"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: BedrockVerifySourceUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evaluateBedrockEvidence(tc.obs)
			require.Equal(t, tc.wantStatus, v.Status)
			require.Equal(t, tc.wantBedrock, v.IsBedrock)
			require.Equal(t, tc.wantSuspicious, v.Suspicious)
			require.Equal(t, tc.wantUnavailable, v.ModelUnavailable)
			require.Equal(t, tc.wantSource, v.DetectedSource)
			require.Equal(t, tc.wantNotes, v.Notes)
		})
	}
}

func TestEvaluateBedrockEvidenceDedupesIDRows(t *testing.T) {
	v := evaluateBedrockEvidence(bedrockVerifyObservation{
		Model:      "claude-sonnet-4-6",
		MessageOK:  true,
		MessageIDs: []string{"msg_bdrk_a", "msg_bdrk_b", "msg_bdrk_c"},
		Errors: []string{
			`HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, ValidationException: bad (aws_invoke_error)`,
		},
	})
	bedrockRows := 0
	for _, e := range v.Evidences {
		if e.Kind == BedrockVerifyEvidenceBedrock {
			bedrockRows++
		}
	}
	// One row for the msg_bdrk_ ids, one for the AWS error (first matching pattern only).
	require.Equal(t, 2, bedrockRows)
}

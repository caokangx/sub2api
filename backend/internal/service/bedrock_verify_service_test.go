//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Validation errors captured 2026-11-20 from vip.aerorelay.one claude-opus-5 (real Bedrock).
const (
	awsMaxTokensErr = `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 78bd30a9-7af6-44dc-83d3-38dacaf6cba1, ValidationException: max_tokens: 999999 > 128000, which is the maximum allowed number of output tokens for anthropic.claude-opus-5 (request id: 20261120153552746600550A27xhdQQ) (aws_invoke_error)`
	awsEffortErr    = `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 1753a436-d538-4ee9-b6f1-8b9b5da0d1bd, ValidationException: Request does not satisfy the required constraints. (request id: 20261120153553517360702Ibqzqidj) (aws_invoke_error)`
	awsThinkingErr  = `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 8dfc5177-9a12-40b2-b7fb-fcdbce506486, ValidationException: "thinking.type.enabled" is not supported for this model. Use "thinking.type.adaptive" and "output_config.effort" to control thinking behavior. (aws_invoke_error)`
)

func genuineBedrockValidations() []bedrockVerifyValidation {
	return []bedrockVerifyValidation{
		{Step: BedrockVerifyStepMaxTokensOverflow, Status: 400, Error: awsMaxTokensErr},
		{Step: BedrockVerifyStepEffortUnknown, Status: 400, Error: awsEffortErr},
		{Step: BedrockVerifyStepThinkingEnabled, Status: 400, Error: awsThinkingErr},
	}
}

func TestEvaluateBedrockEvidence(t *testing.T) {
	const awsForcedToolErr = `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 4bcea973-f19e-453c-a7b1-82c84903feda, ValidationException: tool_choice: type "tool" and "any" are not supported for this model. (aws_invoke_error)`
	const awsComputerErr = `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 3d5fe9ed-4df9-4d9f-bbc4-b5d1437e2a20, ValidationException: tools.0: Input tag 'computer_20251124' found using 'type' does not match any of the expected tags: 'bash_20250124', 'computer_toolset_20260801', 'custom' (aws_invoke_error)`

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
			name: "bedrock ids alone are unconfirmed",
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
			wantStatus: UpstreamVerifyStatusOK, wantSource: BedrockVerifySourceBedrock,
			wantNotes: []string{BedrockVerifyNoteUnconfirmed},
		},
		{
			// vip.aerorelay.one claude-opus-5: every validation probe answered with real Bedrock wording.
			name: "genuine bedrock validation errors",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-5",
				MessageOK: true, InputTokens: 24, ToolOK: true, StreamOK: true, StreamBedrockMetrics: true,
				MessageIDs:      []string{"msg_bdrk_hew5gwpqcd2mzkyjv4xuvr3wfgx5pmnnwsrdzp3fwbwkwvzxjbuq"},
				ToolUseIDs:      []string{"toolu_bdrk_01hdAMHO3WcmZz50X81TAL0Z"},
				Validations:     genuineBedrockValidations(),
				TokenizerTokens: 241,
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
		},
		{
			// www.modelshift.art claude-fable-5-2 (2026-11-19): native-looking Bedrock ids and error
			// wrappers, but max_tokens and thinking mistakes Bedrock rejects are accepted.
			name: "bedrock formats but validations accepted",
			obs: bedrockVerifyObservation{
				Model:     "claude-fable-5-2",
				MessageOK: true, InputTokens: 24, ToolOK: true, StreamOK: true, StreamBedrockMetrics: true,
				MessageIDs: []string{"msg_bdrk_xrduf5djamcmnx763xjobrglvjfiiennvit4ur2fc4prexxqnrda"},
				ToolUseIDs: []string{"toolu_bdrk_vsdrte747agczkv46ohf5rki4nwfptrgfhtymvbx33hsquevfouq"},
				Validations: []bedrockVerifyValidation{
					{Step: BedrockVerifyStepMaxTokensOverflow, Status: 200},
					{Step: BedrockVerifyStepEffortUnknown, Status: 400, Error: `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, RequestID: 33f430a4-b846-4718-a353-a7858058e680, ValidationException: output_config.effort: Input should be 'low', 'medium', 'high', 'xhigh' or 'max'`},
					{Step: BedrockVerifyStepThinkingEnabled, Status: 200},
				},
				TokenizerTokens: 243,
			},
			wantStatus: UpstreamVerifyStatusOK, wantSuspicious: true, wantSource: BedrockVerifySourceUnknown,
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
				ForcedToolError:    awsForcedToolErr,
				Errors:             []string{awsForcedToolErr},
				Validations:        genuineBedrockValidations()[:1],
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
			name: "anthropic wording for max_tokens overflow",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-5",
				MessageOK: true, InputTokens: 24,
				MessageIDs: []string{"msg_bdrk_hew5gwpqcd2mzkyjv4xuvr3wfgx5pmnnwsrdzp3fwbwkwvzxjbuq"},
				Validations: []bedrockVerifyValidation{{
					Step: BedrockVerifyStepMaxTokensOverflow, Status: 400,
					Error: "HTTP 400: max_tokens: 999999 > 128000, which is the maximum allowed number of output tokens for claude-opus-5 (invalid_request_error)",
				}},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSuspicious: true, wantSource: BedrockVerifySourceAnthropic,
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
			wantStatus: UpstreamVerifyStatusOK, wantSuspicious: true, wantSource: BedrockVerifySourceUnknown,
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
			name: "validation probes on an unavailable model",
			obs: bedrockVerifyObservation{
				Model: "claude-opus-4-8",
				Validations: []bedrockVerifyValidation{
					{Step: BedrockVerifyStepMaxTokensOverflow, Status: 503, Error: "HTTP 503: model_not_found: 无可用渠道"},
				},
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
			// Every request failed; the Bedrock throttling text hints at the source but cannot
			// prove what serves successful requests.
			name: "bedrock throttling only",
			obs: bedrockVerifyObservation{
				Model:  "claude-opus-5-5",
				Errors: []string{"HTTP 429: Too many tokens, please wait before trying again."},
			},
			wantStatus: UpstreamVerifyStatusFailed, wantSource: BedrockVerifySourceBedrock,
		},
		{
			name: "bedrock markers only in errors",
			obs: bedrockVerifyObservation{
				Model:     "claude-sonnet-4-6",
				MessageOK: true, InputTokens: 24, ToolOK: true,
				MessageIDs:         []string{"chatcmpl-123"},
				ForcedToolRejected: true,
				ForcedToolError:    awsForcedToolErr,
				Errors:             []string{awsForcedToolErr},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: BedrockVerifySourceBedrock,
			wantNotes: []string{BedrockVerifyNoteForcedToolUnsupported, BedrockVerifyNoteUnconfirmed},
		},
		{
			// vip.aerorelay.one claude-opus-5-5: sub2api rejects forced tool_choice locally, and
			// Bedrock rejects computer_20251124 without the beta header.
			name: "genuine bedrock behind sub2api",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-5-5",
				MessageOK: true, InputTokens: 24, ToolOK: true, StreamOK: true,
				MessageIDs: []string{
					"msg_bdrk_voz4wt73rrfk2hlw6jkni66cukgpbbv65ygevdoueerm6iijjez3",
					"msg_bdrk_bjcdcthojqbgyewtl2biaeom567ster4vjtxymbkr6gf2bj5dcgl",
				},
				ToolUseIDs:         []string{"toolu_bdrk_01vAK5XluyCrWzrrfCOGrnGn"},
				ForcedToolRejected: true,
				ForcedToolError:    "HTTP 400: claude-opus-5-5 does not support forced tool_choice; use auto or none (invalid_request_error)",
				Errors:             []string{"HTTP 400: claude-opus-5-5 does not support forced tool_choice; use auto or none (invalid_request_error)"},
				ComputerProbed:     true,
				ComputerError:      awsComputerErr,
				TokenizerTokens:    243,
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
			wantNotes: []string{BedrockVerifyNoteForcedToolUnsupported},
		},
		{
			// api.aixuanhub.me claude-opus-5-5: Anthropic-style ids with bdrk_ spliced in, and the
			// computer tool is swallowed instead of rejected.
			name: "anthropic ids disguised as bedrock",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-5-5",
				MessageOK: true, InputTokens: 19, InferenceGeoSeen: true, ToolOK: true, StreamOK: true,
				MessageIDs:       []string{"msg_bdrk_011CgtQuJwQkovFBlPZUMgan", "msg_bdrk_011CgtQuDRJzMcy4XLVywfVp"},
				ToolUseIDs:       []string{"toolu_bdrk_01uYACitENd5GsGfME11lftt"},
				ComputerProbed:   true,
				ComputerAccepted: true,
			},
			wantStatus: UpstreamVerifyStatusOK, wantSuspicious: true, wantSource: BedrockVerifySourceUnknown,
		},
		{
			// www.modelshift.art claude-fable-5-2: native Bedrock ids, but the relay answers a tool
			// Bedrock itself rejects.
			name: "bedrock ids but computer tool rewritten",
			obs: bedrockVerifyObservation{
				Model:     "claude-fable-5-2",
				MessageOK: true, InputTokens: 24, ToolOK: true, StreamOK: true,
				MessageIDs:       []string{"msg_bdrk_57bu5ydxxpa2p5rzkliwv6ykmo7fvva2lhls7mrwldi2qkfh4veq"},
				ToolUseIDs:       []string{"toolu_bdrk_gxjqeravgrbjvawr5dh3g4bl6sjlmiajpnfreyxifsqezqtz2umq"},
				ComputerProbed:   true,
				ComputerAccepted: true,
			},
			wantStatus: UpstreamVerifyStatusOK, wantSuspicious: true, wantSource: BedrockVerifySourceUnknown,
		},
		{
			name: "computer tool accepted on unchecked model is info only",
			obs: bedrockVerifyObservation{
				Model:     "claude-sonnet-4-6",
				MessageOK: true, InputTokens: 24,
				MessageIDs:       []string{"msg_bdrk_ies6fu62e4lpxdvumcsclenhxamv6fh32qjhcpsozkhmnzist2yq"},
				ComputerProbed:   true,
				ComputerAccepted: true,
				Validations:      genuineBedrockValidations(),
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
		},
		{
			name: "tokenizer count differs from reference",
			obs: bedrockVerifyObservation{
				Model:     "claude-opus-5",
				MessageOK: true, InputTokens: 24,
				MessageIDs:      []string{"msg_bdrk_hew5gwpqcd2mzkyjv4xuvr3wfgx5pmnnwsrdzp3fwbwkwvzxjbuq"},
				Validations:     genuineBedrockValidations(),
				TokenizerTokens: 243,
			},
			wantStatus: UpstreamVerifyStatusOK, wantBedrock: true, wantSource: BedrockVerifySourceBedrock,
			wantNotes: []string{BedrockVerifyNoteTokenizerMismatch},
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
	const wrapped = `HTTP 400: InvokeModel: operation error Bedrock Runtime: InvokeModel, ValidationException: bad (aws_invoke_error)`
	v := evaluateBedrockEvidence(bedrockVerifyObservation{
		Model:      "claude-sonnet-4-6",
		MessageOK:  true,
		MessageIDs: []string{"msg_bdrk_a", "msg_bdrk_b", "msg_bdrk_c"},
		Errors:     []string{wrapped, wrapped},
	})
	weakRows := 0
	for _, e := range v.Evidences {
		require.NotEqual(t, BedrockVerifyEvidenceBedrock, e.Kind)
		if e.Kind == BedrockVerifyEvidenceBedrockWeak {
			weakRows++
		}
	}
	// One row for the msg_bdrk_ ids, one for the AWS error wrapper (first pattern, once).
	require.Equal(t, 2, weakRows)
}

func TestEvaluateBedrockEvidenceSeparatesMixedMessageIDFormats(t *testing.T) {
	v := evaluateBedrockEvidence(bedrockVerifyObservation{
		Model:     "claude-opus-5-5",
		MessageOK: true, InputTokens: 24, StreamOK: true,
		MessageIDs: []string{
			"msg_bdrk_voz4wt73rrfk2hlw6jkni66cukgpbbv65ygevdoueerm6iijjez3",
			"msg_bdrk_dehrpanxzxfcl3wuty4r56eozsu3xzfkr2exidz7kamauq57yzwh",
			"msg_bdrk_011CgtQuJwQkovFBlPZUMgan",
		},
	})
	kinds := map[string]int{}
	for _, e := range v.Evidences {
		kinds[e.Kind]++
	}
	require.Equal(t, 1, kinds[BedrockVerifyEvidenceBedrockWeak])
	require.Equal(t, 1, kinds[BedrockVerifyEvidenceNonBedrock])
	require.True(t, v.Suspicious)
	require.False(t, v.IsBedrock)
}

func TestBedrockVerifyValidationBody(t *testing.T) {
	require.JSONEq(t, `{"model":"m","max_tokens":999999,"messages":[{"role":"user","content":"hi"}]}`,
		string(bedrockVerifyValidationBody("m", BedrockVerifyStepMaxTokensOverflow)))
	require.JSONEq(t, `{"model":"m","max_tokens":8,"thinking":{"type":"adaptive"},"output_config":{"effort":"ultra"},"messages":[{"role":"user","content":"hi"}]}`,
		string(bedrockVerifyValidationBody("m", BedrockVerifyStepEffortUnknown)))
	require.JSONEq(t, `{"model":"m","max_tokens":8,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hi"}]}`,
		string(bedrockVerifyValidationBody("m", BedrockVerifyStepThinkingEnabled)))
}

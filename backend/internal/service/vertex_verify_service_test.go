//go:build unit

package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Validation errors captured 2026-11-25 from 192.6.47.3:3000 gemini-4-hydrogen (real Vertex).
const (
	vertexBudgetErr  = `HTTP 400: Thinking budget is not supported for this model. Use thinking level instead. See https://***.com/***/*** for details.`
	vertexMinimalErr = `HTTP 400: Thinking level is unsupported: THINKING_LEVEL_MINIMAL`
	vertexBothErr    = `HTTP 400: Unable to submit request because thinking_budget and thinking_level are not supported together.. Learn more: https://***.com/***/***/***/***/***`
	vertexGCSErr     = `HTTP 404: No such object: cloud-samples-data/generative-ai/image/***.jpg`
)

func TestEvaluateVertexEvidence(t *testing.T) {
	now := time.Date(2026, 11, 25, 11, 45, 0, 0, time.UTC)
	cases := []struct {
		name            string
		obs             vertexVerifyObservation
		wantStatus      string
		wantVertex      bool
		wantSuspicious  bool
		wantUnavailable bool
		wantSource      string
		wantNotes       []string
	}{
		{
			// 192.6.47.3:3000 gemini-4-hydrogen: every validation probe answered with Vertex wording.
			name: "genuine vertex",
			obs: vertexVerifyObservation{
				Model: "gemini-4-hydrogen", Now: now,
				BasicOK: true, PromptTokens: 36,
				DateReplied: true, DateReply: "UNKNOWN",
				PersonaReplied: true, PersonaReply: "1. NO\n2. NO\n9. YES — Effort level 0.50 (plus a silent thinking instruction).\n10. NO",
				ResponseIDs: []string{"S6sGa5fBFPi5sbwP6JesiAI", "fM8Ga7tpgPyxvA_-oc3oDg"},
				CreateTimes: []string{"2026-11-25T09:32:59.336023Z"},
				TrafficType: true,
				GCS:         &vertexVerifyValidation{Step: VertexVerifyStepGCSMissing, Status: 404, Error: vertexGCSErr},
				Validations: []vertexVerifyValidation{
					{Step: VertexVerifyStepThinkingBudget, Status: 400, Error: vertexBudgetErr},
					{Step: VertexVerifyStepThinkingBoth, Status: 400, Error: vertexBothErr},
				},
				Thinking:      &VertexVerifyThinking{DurationMs: 18_341, ThoughtsTokens: 2544, ThoughtsReported: true, CandidatesTokens: 8, ThoughtParts: 1, AnswerCorrect: true},
				ThinkingReply: "4227",
			},
			wantStatus: UpstreamVerifyStatusOK, wantVertex: true, wantSource: VertexVerifySourceVertex,
		},
		{
			// dqai.top gemini-3.1-pro-preview behind an airelay-type upstream.
			name: "relay accepts invalid thinking config and hides thinking",
			obs: vertexVerifyObservation{
				Model: "gemini-3.1-pro-preview", Now: now,
				BasicOK: true, PromptTokens: 6,
				DateReplied: true, DateReply: "UNKNOWN",
				ResponseIDs: []string{"PMoGawhQXhZlQIG5YgG7AIM"},
				CreateTimes: []string{"2026-11-25T11:45:00.672731Z"},
				GCS: &vertexVerifyValidation{Step: VertexVerifyStepGCSMissing, Status: 400,
					Error: "HTTP 400: The request could not be completed. (request id: 20261125114501555397718268d9d6PgNHQZ2H)"},
				Validations: []vertexVerifyValidation{
					{Step: VertexVerifyStepThinkingMinimal, Status: 200, Accepted: true, Reply: "OK"},
					{Step: VertexVerifyStepThinkingBoth, Status: 200, Accepted: true, Reply: "OK"},
					{Step: VertexVerifyStepThinkingXHigh, Status: 200, Accepted: true, Reply: "OK"},
				},
				Thinking:      &VertexVerifyThinking{DurationMs: 40_868, CandidatesTokens: 4, AnswerCorrect: true},
				ThinkingReply: "4227",
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: VertexVerifySourceRelay,
			wantNotes: []string{VertexVerifyNoteThinkingHidden, VertexVerifyNoteNestedRelay},
		},
		{
			// www.ominiapi.com gemini-4-argon: Antigravity identity block injected, mixed channels.
			name: "antigravity",
			obs: vertexVerifyObservation{
				Model: "gemini-4-argon", Now: now,
				BasicOK: true, PromptTokens: 117,
				DateReplied: true, DateReply: "UNKNOWN",
				PersonaReplied: true, PersonaReply: "1. NO\n4. NO\n5. YES (Antigravity)\n9. YES (EFFORT LEVEL: 0.20 / think silently if needed)",
				ResponseIDs: []string{"mKkGa6fINueP8L0P7u2CuQk", "resp_1795598745624402511_9338"},
				GCS: &vertexVerifyValidation{Step: VertexVerifyStepGCSMissing, Status: 404,
					Error: "HTTP 404: No such object: cloud-samples-data/generative-ai/image/***.jpg (request id: 202611250925382992727908268d9d6oCXODVj4)"},
				Thinking:      &VertexVerifyThinking{DurationMs: 64_439, ThoughtsTokens: 2265, ThoughtsReported: true, CandidatesTokens: 8, ThoughtParts: 2, AnswerCorrect: true},
				ThinkingReply: "4227",
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: VertexVerifySourceAntigravity,
			wantNotes: []string{VertexVerifyNotePromptInjected, VertexVerifyNoteMixedChannel, VertexVerifyNoteNestedRelay},
		},
		{
			// dqai.top gemini-4-argon behind a Gemini web-app reverse proxy.
			name: "gemini app",
			obs: vertexVerifyObservation{
				Model: "gemini-4-argon", Now: now,
				BasicOK: true, PromptTokens: 13,
				DateReplied: true, DateReply: "2026-11-25",
				PersonaReplied: true, PersonaReply: "- **Current date & time:** Wednesday, November 25, 2026, at 6:45 AM EST\n- **Location:** United States\n- **Name:** Gemini",
				ResponseIDs: []string{"PMoGawRDM6ESAAOfFcJUfxQ"},
				Validations: []vertexVerifyValidation{
					{Step: VertexVerifyStepThinkingBudget, Status: 200, Accepted: true, Reply: "OK"},
					{Step: VertexVerifyStepThinkingMinimal, Status: 200, Accepted: true, Reply: "OK"},
				},
				Thinking:      &VertexVerifyThinking{DurationMs: 35_882, ThoughtsTokens: 83, ThoughtsReported: true, CandidatesTokens: 85, AnswerCorrect: true},
				ThinkingReply: "\n```python?code_reference&code_event_index=1\nprint(sum(primes))\n```\n4227",
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: VertexVerifySourceGeminiApp,
			wantNotes: []string{VertexVerifyNoteDateInjected, VertexVerifyNotePromptInjected, VertexVerifyNoteThinkingEstimated},
		},
		{
			// airelay.buzz gemini-4-hydrogen: thinking folded into candidatesTokenCount.
			name: "thinking merged into output tokens",
			obs: vertexVerifyObservation{
				Model: "gemini-4-hydrogen", Now: now,
				BasicOK: true, PromptTokens: 36,
				ResponseIDs:   []string{"bJYGa_9De8-sLNBz0KooAhg"},
				Thinking:      &VertexVerifyThinking{DurationMs: 53_327, CandidatesTokens: 10033, AnswerCorrect: true},
				ThinkingReply: "4227",
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: VertexVerifySourceRelay,
			wantNotes: []string{VertexVerifyNoteThinkingMerged},
		},
		{
			name: "genuine validation plus injected prompt is suspicious",
			obs: vertexVerifyObservation{
				Model: "gemini-4-hydrogen", Now: now,
				BasicOK: true, PromptTokens: 72,
				Validations: []vertexVerifyValidation{{Step: VertexVerifyStepThinkingBoth, Status: 400, Error: vertexBothErr}},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSuspicious: true, wantSource: VertexVerifySourceRelay,
			wantNotes: []string{VertexVerifyNotePromptInjected},
		},
		{
			name: "only copyable google formats",
			obs: vertexVerifyObservation{
				Model: "gemini-3.5-flash", Now: now,
				BasicOK: true, PromptTokens: 5,
				ResponseIDs: []string{"IK0Da8PVKYmbpucPnaL1mQg"},
				CreateTimes: []string{"2026-11-23T03:04:00.682691Z"},
			},
			wantStatus: UpstreamVerifyStatusOK, wantSource: VertexVerifySourceVertex,
			wantNotes: []string{VertexVerifyNoteUnconfirmed},
		},
		{
			// airelay.buzz gemini-4-hydrogen while its upstream was at capacity: only the
			// validation probes answered, and their errors carry the relay's own request id.
			name: "verdict from partial probes is flagged incomplete",
			obs: vertexVerifyObservation{
				Model: "gemini-4-hydrogen", Now: now,
				Headers:     []http.Header{{"X-Oneapi-Request-Id": []string{"202611260808570201665158268d9d6eF7iXJ3T"}}},
				Errors:      []string{"HTTP 404: The service is currently at capacity due to high demand. Please try again later."},
				FailedSteps: []string{VertexVerifyStepBasic, VertexVerifyStepThinking},
				GCS: &vertexVerifyValidation{Step: VertexVerifyStepGCSMissing, Status: 404,
					Error: "HTTP 404: The service is currently at capacity due to high demand. Please try again later. (request id: 202611260808570201665158268d9d6eF7iXJ3T)"},
				Validations: []vertexVerifyValidation{
					{Step: VertexVerifyStepThinkingBudget, Status: 400, Error: vertexBudgetErr},
					{Step: VertexVerifyStepThinkingBoth, Status: 400, Error: vertexBothErr},
				},
			},
			wantStatus: UpstreamVerifyStatusOK, wantVertex: true, wantSource: VertexVerifySourceVertex,
			wantNotes: []string{VertexVerifyNoteIncomplete},
		},
		{
			name: "model unavailable",
			obs: vertexVerifyObservation{
				Model: "gemini-4-neon", Now: now,
				Errors: []string{`HTTP 404: Model "gemini-4-neon" is not supported by any configured account in this group`},
				Validations: []vertexVerifyValidation{
					{Step: VertexVerifyStepThinkingBoth, Status: 404, Error: `HTTP 404: Model "gemini-4-neon" is not supported by any configured account in this group`},
				},
			},
			wantStatus: UpstreamVerifyStatusFailed, wantUnavailable: true, wantSource: VertexVerifySourceUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evaluateVertexEvidence(tc.obs)
			require.Equal(t, tc.wantStatus, v.Status)
			require.Equal(t, tc.wantVertex, v.IsVertex)
			require.Equal(t, tc.wantSuspicious, v.Suspicious)
			require.Equal(t, tc.wantUnavailable, v.ModelUnavailable)
			require.Equal(t, tc.wantSource, v.DetectedSource)
			require.ElementsMatch(t, tc.wantNotes, v.Notes)
		})
	}
}

func TestVertexVerifyValidationSteps(t *testing.T) {
	cases := map[string][]string{
		"gemini-4-hydrogen":      {VertexVerifyStepThinkingBudget, VertexVerifyStepThinkingMinimal, VertexVerifyStepThinkingBoth},
		"gemini-4-carbon":        {VertexVerifyStepThinkingBudget, VertexVerifyStepThinkingMinimal, VertexVerifyStepThinkingBoth, VertexVerifyStepThinkingXHigh},
		"gemini-4-neon":          {VertexVerifyStepThinkingBudget, VertexVerifyStepThinkingBoth},
		"gemini-3.1-pro-preview": {VertexVerifyStepThinkingMinimal, VertexVerifyStepThinkingBoth, VertexVerifyStepThinkingXHigh},
		"gemini-3.5-flash-lite":  {VertexVerifyStepThinkingBoth, VertexVerifyStepThinkingXHigh},
		"gemini-3.8-flash-tts":   {VertexVerifyStepThinkingBoth},
		"gemini-2.5-flash":       nil,
	}
	for model, want := range cases {
		require.Equal(t, want, vertexVerifyValidationSteps(model), model)
	}
}

func TestVertexVerifyIDKind(t *testing.T) {
	cases := map[string]string{
		"S6sGa5fBFPi5sbwP6JesiAI":              vertexVerifyIDKindNative,
		"gZ4Ga-2NBVBsyt_tneauGjI":              vertexVerifyIDKindNative,
		"eb8fa794-9dbe-4647-9d49-40fd18c70412": vertexVerifyIDKindUUID,
		"resp_1795503343029978351_71272":       vertexVerifyIDKindResp,
		"gem-7696eab419de4a1aade6":             vertexVerifyIDKindGem,
		"gem_9fbe15db9c707a2d0e023c22":         vertexVerifyIDKindGem,
		"d4075ba778ec4cecb6f6781aac50e":        vertexVerifyIDKindHex,
	}
	for id, want := range cases {
		require.Equal(t, want, vertexVerifyIDKind(id), id)
	}
}

func TestVertexVerifyMentionsDate(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 30, 0, 0, time.UTC)
	require.True(t, vertexVerifyMentionsDate("2026-11-25", now))
	require.True(t, vertexVerifyMentionsDate("Today is November 24, 2026.", now))
	require.True(t, vertexVerifyMentionsDate("今天是2026年11月26日", now))
	require.False(t, vertexVerifyMentionsDate("UNKNOWN", now))
	require.False(t, vertexVerifyMentionsDate("2026-11-20", now))
}

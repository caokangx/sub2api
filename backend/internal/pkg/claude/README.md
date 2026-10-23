# Claude model adaptation

This package is the single entry point for Claude Code model compatibility.
`NormalizeModelID` handles client aliases and dated IDs; `OutputBudget` returns
the CLI default output budget and hard limit; `ClaudeCodeSupportsTemperature`
and the effort catalog describe capability gates. Gateway beta construction is
in `backend/internal/service/gateway_upstream_request.go`, profile rewriting is
in `gateway_claude_code_profile.go`, and the Fable exception is kept in the
profile builder.

| Family | Default | Upper | Temperature | Thinking / effort |
| --- | ---: | ---: | --- | --- |
| Claude 3.5 Haiku/Sonnet | 8,192 | 8,192 | yes | legacy thinking |
| Claude 3.7, Sonnet 4.0/4.5, Haiku 4.5 | 32,000 | 64,000 | model dependent | enabled/adaptive |
| Sonnet 4.6 | 32,000 | 128,000 | yes | enabled/adaptive |
| Sonnet 5 | 64,000 | 128,000 | no | adaptive/effort |
| Sonnet 5.5, Haiku 5.5 | 128,000 | 128,000 | no | adaptive/effort |
| Opus 4.0/4.1 | 32,000 | 32,000 | model dependent | enabled/adaptive |
| Opus 4.5 | 32,000 | 64,000 | yes | enabled/adaptive |
| Opus 4.6–5 | 64,000 | 128,000 | model dependent | adaptive/effort |
| Fable 5/5.1, Mythos 5/5.1 | 64,000 | 128,000 | model dependent | profile exception |

The values above distinguish the CLI default from the provider output limit.
Temperature is accepted for Claude 3.x, Opus 4.0/4.1/4.5/4.6 and Sonnet 4.0/4.5/4.6; it is omitted for other catalog models. Thinking capability and effort levels are queried with `SupportsThinking` and `EffortLevelsForModel`. Unknown models use a 32,000 default and 128,000 upper limit, and do not opt into temperature or thinking.

Profile Messages betas start with Claude Code and OAuth identity, then add
interleaved thinking, context management, effort, thinking binding controls,
structured output, tool changes, fast mode, 1-hour cache, and 1M context only
when the final request/model uses those features. Count tokens uses the CLI
counting subset plus `token-counting-2024-11-01`. Admin beta filters apply last.
Fable uses its two-block identity profile; normal OAuth requests keep their
existing normalization and budget behavior.

When adding a model, verify the official ID and aliases, update capabilities
and budgets, check display and pricing metadata, add focused tests, and inspect
the final outbound request. A new model does not by itself require changing the
fixed Claude Code prompt or tool profile; do that only when request behavior
changes. Keep this README synchronized with code changes.

Run `go test ./internal/pkg/claude ./internal/service` from `backend`. The
profile consistency tests use the gateway's HTTP recorder for Messages and
count_tokens. The dated audit artifacts at repository root retain the static
CLI evidence; local credentials and service replay are environment-specific.
Baseline: Claude Code 2.1.284, checked 2026-10-22.

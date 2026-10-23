# Claude Code profile implementation verification

Baseline: Claude Code 2.1.284. Implementation and focused regression tests were run on 2026-10-22.

## Automated results

- `go test -tags unit ./internal/pkg/claude ./internal/service`: PASS (final service package run: 198s).
- `go test -tags unit ./internal/pkg/claude ./internal/service ./internal/handler/...`: Claude package and all handler packages PASS; the service package in that earlier run exposed two ordinary OAuth identity regressions. Those were corrected, and both exact failing tests passed afterward.
- Focused profile tests cover default/upper output budgets, ignored client `max_tokens` values, thinking-budget upper bounds, UA-independent Messages/count_tokens betas, metadata normalization, retry/account identity stability, billing-field validation, top-level envelope validation, multiround cache breakpoints, Fable's two-block system, and Messages plus both count_tokens routes.
- `node_modules/.bin/vue-tsc -b`: PASS.
- `node_modules/.bin/vite build`: PASS. The standard `npm run build` wrapper could not start because `pnpm` is unavailable, so the equivalent configured typecheck/build steps were run directly.
- `node_modules/.bin/vitest run src/i18n/__tests__/localeKeyCompleteness.spec.ts`: PASS (3 tests).
- `git diff --check`: PASS.

## Request verification boundary

The service tests exercise `GatewayService.Forward` and `ForwardCountTokens` with the local HTTP upstream recorder and assert the exact request body and headers handed to that transport. This verifies the final gateway-to-upstream request construction without contacting Anthropic.

The previous local test account and profile group in the development database were already soft-deleted and inactive. The running API container also predates this source change. An API-level replay through `/v1/messages` and both count_tokens URLs was therefore not run in this pass. The earlier `gateway_debug.log` and request snapshots remain untouched; they are historical artifacts and do not represent this implementation's output.

No live upstream call was made. The existing container was not restarted, its debug logging was not changed, and no database test resources were created or modified during this pass.

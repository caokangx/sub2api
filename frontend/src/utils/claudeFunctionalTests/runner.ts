import type { ReasoningConn } from '@/utils/claudeReasoning'
import { FUNCTIONAL_CASES, isFableModel } from './cases'
import { replyUsage, sendRaw } from './send'
import {
  FunctionalTestAborted,
  type Assertion,
  type CaseContext,
  type CaseDefinition,
  type CaseResult,
  type ExpectOptions,
  type StepRecord
} from './types'

type Sender = typeof sendRaw

function stringify(value: unknown): string | undefined {
  if (value === undefined) return undefined
  if (value === null) return 'null'
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}

/** Selected cases in run order; the expensive long-context cases always go last. */
export function orderCases(ids: Iterable<string>): CaseDefinition[] {
  const wanted = new Set(ids)
  const picked = FUNCTIONAL_CASES.filter((c) => wanted.has(c.id))
  return [...picked.filter((c) => c.group !== 'longContext'), ...picked.filter((c) => c.group === 'longContext')]
}

export function estimateInputTokens(ids: Iterable<string>): number {
  return orderCases(ids).reduce((sum, c) => sum + c.estInputTokens, 0)
}

export function skippedResult(def: CaseDefinition): CaseResult {
  return {
    id: def.id,
    group: def.group,
    status: 'skipped',
    steps: [],
    assertions: [],
    elapsedMs: 0,
    usage: { input: 0, output: 0 }
  }
}

/** Run one case; upstream failures become assertions, relay failures mark the case as error. */
export async function runCase(
  def: CaseDefinition,
  conn: ReasoningConn,
  signal?: AbortSignal,
  send: Sender = sendRaw
): Promise<CaseResult> {
  const result: CaseResult = { ...skippedResult(def), status: 'pass' }
  const isFable = isFableModel(conn.model)
  const started = Date.now()

  const ctx: CaseContext = {
    conn,
    model: conn.model.trim(),
    isFable,
    async send(label, body, opts = {}) {
      const step: StepRecord = { label, request: body, beta: opts.beta, reply: null }
      result.steps.push(step)
      const reply = await send(conn, body, { ...opts, signal })
      step.reply = reply
      const usage = replyUsage(reply)
      result.usage.input += usage.input
      result.usage.output += usage.output
      return reply
    },
    expect(key, ok, opts: ExpectOptions = {}) {
      const level = opts.level ?? 'required'
      const scope = opts.scope ?? 'generic'
      const assertion: Assertion = {
        key,
        params: opts.params,
        level,
        scope,
        step: result.steps.length,
        actual: stringify(opts.actual),
        status: scope === 'fable' && !isFable ? 'info' : ok ? 'pass' : level === 'bonus' ? 'warn' : 'fail'
      }
      result.assertions.push(assertion)
      return ok
    }
  }

  try {
    await def.run(ctx)
    result.status = result.assertions.some((a) => a.status === 'fail') ? 'fail' : 'pass'
  } catch (err) {
    if (err instanceof FunctionalTestAborted) {
      result.status = 'skipped'
    } else {
      result.status = 'error'
      result.error = err instanceof Error ? err.message : String(err)
    }
  }
  result.elapsedMs = Date.now() - started
  return result
}

export interface RunOptions {
  signal?: AbortSignal
  onCaseStart?: (id: string, index: number, total: number) => void
  onCaseDone?: (result: CaseResult, index: number, total: number) => void
  send?: Sender
}

/** Run the selected cases one by one; after an abort the remaining cases are marked skipped. */
export async function runFunctionalTests(
  conn: ReasoningConn,
  ids: Iterable<string>,
  opts: RunOptions = {}
): Promise<CaseResult[]> {
  const defs = orderCases(ids)
  const results: CaseResult[] = []
  for (const [index, def] of defs.entries()) {
    if (opts.signal?.aborted) {
      results.push(skippedResult(def))
      continue
    }
    opts.onCaseStart?.(def.id, index, defs.length)
    const res = await runCase(def, conn, opts.signal, opts.send)
    results.push(res)
    opts.onCaseDone?.(res, index, defs.length)
  }
  return results
}

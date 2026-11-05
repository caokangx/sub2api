// Estimate how many input tokens an Anthropic-compatible relay injects per request.
//
// Differential method: send "hi" padded with N copies of PADDING_UNIT for a few N.
// The reported input count is linear in N:
//
//   reported = inflation × (baseline + injected + tokensPerUnit × N)
//
// baseline and tokensPerUnit come from INJECTION_CALIBRATION, measured on an upstream
// with no injection. The fitted slope gives the inflation factor, the intercept the
// injected tokens.

import { callMessages, type ReasoningConn } from '@/utils/claudeReasoning'

export const PADDING_UNIT = ' hello'
export const PADDING_STEPS = [0, 200, 800]

/** Injection below this is within the ±10 token measurement noise. */
const NONE_THRESHOLD = 20
/** Above this the relay is injecting a whole system prompt (Claude Code mimicry is ~4k). */
const MAJOR_THRESHOLD = 500
const INFLATION_TOLERANCE = 0.05
/** A genuine upstream fits the line exactly; allow a little slack for odd relays. */
const MAX_RESIDUAL = 3
/** Default for models missing from the calibration table. */
const DEFAULT_BASELINE = 10

export interface InjectionCalibration {
  baseline: number
  tokensPerUnit: number
}

// Measured against a Bedrock relay with no injection (2026-11): every model fits the
// line exactly. Ordered: the first matching pattern wins.
const INJECTION_CALIBRATION: { pattern: RegExp; calibration: InjectionCalibration }[] = [
  { pattern: /claude-opus-4-7/, calibration: { baseline: 13, tokensPerUnit: 2 } },
  { pattern: /claude-(opus|sonnet|haiku)-5-5|claude-fable-5-1/, calibration: { baseline: 10, tokensPerUnit: 2 } },
  { pattern: /claude-opus-4-8|claude-(opus|sonnet|fable)-5(?!-\d)/, calibration: { baseline: 8, tokensPerUnit: 2 } },
  { pattern: /claude-(sonnet|opus|haiku)-4(-[0-6])?(?!-?\d)/, calibration: { baseline: 8, tokensPerUnit: 1 } }
]

export function findInjectionCalibration(model: string): InjectionCalibration | null {
  const id = model.trim().toLowerCase().replace(/-\d{8}$/, '')
  return INJECTION_CALIBRATION.find((c) => c.pattern.test(id))?.calibration ?? null
}

export interface InjectionUsage {
  input: number
  cacheCreation: number
  cacheRead: number
  total: number
}

export interface InjectionSample {
  units: number
  usage: InjectionUsage
}

export type InjectionVerdict = 'none' | 'minor' | 'major'

export type InjectionHint = 'uncalibrated' | 'unstable' | 'cached' | 'belowBaseline'

export interface InjectionRow {
  units: number
  /** Tokens a genuine upstream would report; null when the model is not calibrated. */
  expected: number | null
  usage: InjectionUsage
}

export interface InjectionReport {
  model: string
  calibrated: boolean
  intercept: number
  slope: number
  maxResidual: number
  /** Reported / genuine token ratio; null when the model is not calibrated. */
  inflation: number | null
  inflated: boolean
  injectedTokens: number
  verdict: InjectionVerdict
  hints: InjectionHint[]
  rows: InjectionRow[]
}

function num(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

/** Cached prompt tokens count as input too; injected system prompts are often cached. */
export function totalInputTokens(usage: unknown): InjectionUsage {
  const u = usage && typeof usage === 'object' ? (usage as Record<string, unknown>) : {}
  const input = num(u.input_tokens)
  const cacheCreation = num(u.cache_creation_input_tokens)
  const cacheRead = num(u.cache_read_input_tokens)
  return { input, cacheCreation, cacheRead, total: input + cacheCreation + cacheRead }
}

export interface LineFit {
  intercept: number
  slope: number
  maxResidual: number
}

/** Least-squares line through (x, y) points. */
export function fitLine(points: { x: number; y: number }[]): LineFit {
  const n = points.length
  if (n === 0) return { intercept: 0, slope: 0, maxResidual: 0 }
  const meanX = points.reduce((s, p) => s + p.x, 0) / n
  const meanY = points.reduce((s, p) => s + p.y, 0) / n
  let sxx = 0
  let sxy = 0
  for (const p of points) {
    sxx += (p.x - meanX) ** 2
    sxy += (p.x - meanX) * (p.y - meanY)
  }
  const slope = sxx === 0 ? 0 : sxy / sxx
  const intercept = meanY - slope * meanX
  const maxResidual = Math.max(...points.map((p) => Math.abs(p.y - (intercept + slope * p.x))))
  return { intercept, slope, maxResidual }
}

export function evaluateInjection(model: string, samples: InjectionSample[]): InjectionReport {
  const calibration = findInjectionCalibration(model)
  const fit = fitLine(samples.map((s) => ({ x: s.units, y: s.usage.total })))
  const hints: InjectionHint[] = []

  let inflation: number | null = null
  let injected: number
  if (calibration) {
    inflation = fit.slope / calibration.tokensPerUnit
    // Undo proportional inflation before subtracting the genuine framing overhead.
    injected = (inflation > 0 ? fit.intercept / inflation : fit.intercept) - calibration.baseline
  } else {
    hints.push('uncalibrated')
    injected = fit.intercept - DEFAULT_BASELINE
  }

  if (fit.maxResidual > MAX_RESIDUAL) hints.push('unstable')
  if (samples.some((s) => s.usage.cacheCreation > 0 || s.usage.cacheRead > 0)) hints.push('cached')
  if (injected < -NONE_THRESHOLD) hints.push('belowBaseline')

  const injectedTokens = Math.max(0, Math.round(injected))
  const verdict: InjectionVerdict =
    injectedTokens <= NONE_THRESHOLD ? 'none' : injectedTokens <= MAJOR_THRESHOLD ? 'minor' : 'major'

  return {
    model,
    calibrated: !!calibration,
    intercept: fit.intercept,
    slope: fit.slope,
    maxResidual: fit.maxResidual,
    inflation,
    inflated: inflation !== null && inflation > 1 + INFLATION_TOLERANCE,
    injectedTokens,
    verdict,
    hints,
    rows: samples.map((s) => ({
      units: s.units,
      expected: calibration ? calibration.baseline + calibration.tokensPerUnit * s.units : null,
      usage: s.usage
    }))
  }
}

export function buildInjectionProbe(model: string, units: number): Record<string, unknown> {
  return {
    model,
    max_tokens: 16,
    messages: [{ role: 'user', content: 'hi' + PADDING_UNIT.repeat(units) }]
  }
}

/** Send the padded probes in order and evaluate them; `onProgress` gets the finished count. */
export async function analyzeInjection(
  conn: ReasoningConn,
  onProgress?: (done: number, total: number) => void
): Promise<InjectionReport> {
  const samples: InjectionSample[] = []
  for (const units of PADDING_STEPS) {
    const data = await callMessages(conn, buildInjectionProbe(conn.model, units))
    const usage = data && typeof data === 'object' ? (data as Record<string, unknown>).usage : undefined
    samples.push({ units, usage: totalInputTokens(usage) })
    onProgress?.(samples.length, PADDING_STEPS.length)
  }
  return evaluateInjection(conn.model, samples)
}

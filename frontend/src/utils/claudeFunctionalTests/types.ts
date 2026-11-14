import type { ReasoningConn } from '@/utils/claudeReasoning'

export type AnyRecord = Record<string, unknown>

/** Upstream reply as relayed by the backend; never thrown for upstream errors. */
export interface RawReply {
  status: number
  body: unknown
  text: string
  contentType: string
  elapsedMs: number
}

export interface StepRecord {
  /** i18n key under admin.signatureDecoder.functional.steps */
  label: string
  request: AnyRecord
  beta?: string
  reply: RawReply | null
}

/** pass / fail are judged; warn is a failed bonus check; info is recorded only. */
export type AssertionStatus = 'pass' | 'fail' | 'warn' | 'info'
export type AssertionLevel = 'required' | 'bonus'
/** 'fable' checks only hold for claude-fable-5-2 and are informational on other models. */
export type AssertionScope = 'generic' | 'fable'

export interface Assertion {
  /** i18n key under admin.signatureDecoder.functional.assertions */
  key: string
  params?: Record<string, string | number>
  level: AssertionLevel
  scope: AssertionScope
  status: AssertionStatus
  /** 1-based index of the step the check was made on. */
  step: number
  actual?: string
}

export type CaseStatus = 'pass' | 'fail' | 'error' | 'skipped'

export interface CaseUsage {
  input: number
  output: number
}

export interface CaseResult {
  id: string
  group: CaseGroup
  status: CaseStatus
  steps: StepRecord[]
  assertions: Assertion[]
  elapsedMs: number
  usage: CaseUsage
  error?: string
}

export type CaseGroup =
  | 'basic'
  | 'multimodal'
  | 'tools'
  | 'negative'
  | 'stream'
  | 'longContext'
  | 'safety'

export interface SendOptions {
  beta?: string
}

export interface ExpectOptions {
  level?: AssertionLevel
  scope?: AssertionScope
  params?: Record<string, string | number>
  actual?: unknown
}

export interface CaseContext {
  conn: ReasoningConn
  model: string
  isFable: boolean
  send(label: string, body: AnyRecord, opts?: SendOptions): Promise<RawReply>
  expect(key: string, ok: boolean, opts?: ExpectOptions): boolean
}

export interface CaseDefinition {
  id: string
  group: CaseGroup
  /** Rough input tokens the case consumes, for the pre-run estimate. */
  estInputTokens: number
  run(ctx: CaseContext): Promise<void>
}

export class FunctionalTestError extends Error {}

export class FunctionalTestAborted extends Error {
  constructor() {
    super('aborted')
  }
}

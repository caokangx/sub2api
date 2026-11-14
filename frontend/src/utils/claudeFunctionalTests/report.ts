import type { Assertion, AssertionStatus, CaseResult, CaseStatus, CaseUsage, StepRecord } from './types'

export const I18N_PREFIX = 'admin.signatureDecoder.functional'

/** Strings longer than this are cut to their head and tail in exports. */
export const EXPORT_MAX_STRING = 2000
const KEEP_HEAD = 1200
const KEEP_TAIL = 400

export type Translate = (key: string, params?: Record<string, unknown>) => string

export interface FunctionalReport {
  model: string
  baseUrl: string
  isFable: boolean
  startedAt: string
  finishedAt: string
  elapsedMs: number
  summary: Record<CaseStatus, number> & { total: number }
  assertions: Record<AssertionStatus, number>
  usage: CaseUsage
  results: CaseResult[]
}

export function buildReport(
  meta: { model: string; baseUrl: string; isFable: boolean; startedAt: Date; finishedAt: Date },
  results: CaseResult[]
): FunctionalReport {
  const summary = { total: results.length, pass: 0, fail: 0, error: 0, skipped: 0 }
  const assertions = { pass: 0, fail: 0, warn: 0, info: 0 }
  const usage = { input: 0, output: 0 }
  for (const r of results) {
    summary[r.status]++
    for (const a of r.assertions) assertions[a.status]++
    usage.input += r.usage.input
    usage.output += r.usage.output
  }
  return {
    model: meta.model,
    baseUrl: meta.baseUrl,
    isFable: meta.isFable,
    startedAt: meta.startedAt.toISOString(),
    finishedAt: meta.finishedAt.toISOString(),
    elapsedMs: meta.finishedAt.getTime() - meta.startedAt.getTime(),
    summary,
    assertions,
    usage,
    results
  }
}

export function truncateString(s: string, max = EXPORT_MAX_STRING): string {
  if (s.length <= max) return s
  const cut = s.length - KEEP_HEAD - KEEP_TAIL
  return `${s.slice(0, KEEP_HEAD)}\n…[truncated ${cut} of ${s.length} chars]…\n${s.slice(-KEEP_TAIL)}`
}

/** Deep copy with the API key masked and long strings shortened. */
export function sanitize<T>(value: T, apiKey: string): T {
  const key = apiKey.trim()
  const walk = (v: unknown): unknown => {
    if (typeof v === 'string') {
      const masked = key.length >= 4 ? v.split(key).join('***') : v
      return truncateString(masked)
    }
    if (Array.isArray(v)) return v.map(walk)
    if (v && typeof v === 'object') {
      return Object.fromEntries(Object.entries(v as Record<string, unknown>).map(([k, x]) => [k, walk(x)]))
    }
    return v
  }
  return walk(value) as T
}

export function toJSON(report: FunctionalReport, apiKey: string): string {
  return JSON.stringify(sanitize(report, apiKey), null, 2)
}

export function caseTitle(t: Translate, id: string): string {
  return t(`${I18N_PREFIX}.cases.c${id}`)
}

export function assertionText(t: Translate, a: Assertion): string {
  return t(`${I18N_PREFIX}.assertions.${a.key}`, a.params ?? {})
}

export function stepLabel(t: Translate, step: StepRecord): string {
  return t(`${I18N_PREFIX}.steps.${step.label.replace(/\d+$/, '')}`, { n: step.label.match(/\d+$/)?.[0] ?? '' })
}

function fence(content: string, lang = ''): string {
  const ticks = content.includes('```') ? '````' : '```'
  return `${ticks}${lang}\n${content}\n${ticks}`
}

function replyContent(step: StepRecord): { content: string; lang: string } {
  const reply = step.reply
  if (!reply) return { content: '-', lang: '' }
  if (reply.body !== null && reply.body !== undefined) {
    return { content: JSON.stringify(reply.body, null, 2), lang: 'json' }
  }
  return { content: reply.text, lang: 'text' }
}

const STATUS_MARK: Record<AssertionStatus, string> = { pass: 'PASS', fail: 'FAIL', warn: 'WARN', info: 'INFO' }

export function toMarkdown(report: FunctionalReport, apiKey: string, t: Translate): string {
  const r = sanitize(report, apiKey)
  const p = I18N_PREFIX
  const lines: string[] = []
  const status = (s: CaseStatus) => t(`${p}.status.${s}`)

  lines.push(`# ${t(`${p}.report.title`)}`, '')
  lines.push(`- ${t(`${p}.report.model`)}: \`${r.model}\``)
  lines.push(`- Base URL: \`${r.baseUrl}\``)
  lines.push(`- ${t(`${p}.report.startedAt`)}: ${r.startedAt}`)
  lines.push(`- ${t(`${p}.report.elapsed`)}: ${(r.elapsedMs / 1000).toFixed(1)}s`)
  lines.push(`- ${t(`${p}.report.tokens`)}: input ${r.usage.input.toLocaleString()} / output ${r.usage.output.toLocaleString()}`)
  if (!r.isFable) lines.push(`- ${t(`${p}.report.nonFableNote`)}`)
  lines.push('')

  lines.push(`## ${t(`${p}.report.summary`)}`, '')
  lines.push(`| ${status('pass')} | ${status('fail')} | ${status('error')} | ${status('skipped')} | ${t(`${p}.report.total`)} |`)
  lines.push('|---|---|---|---|---|')
  lines.push(`| ${r.summary.pass} | ${r.summary.fail} | ${r.summary.error} | ${r.summary.skipped} | ${r.summary.total} |`, '')
  lines.push(
    `${t(`${p}.report.assertionCounts`, { ...r.assertions })}`,
    ''
  )

  lines.push(`| # | ${t(`${p}.report.case`)} | ${t(`${p}.report.status`)} | ${t(`${p}.report.elapsed`)} | input / output |`)
  lines.push('|---|---|---|---|---|')
  for (const c of r.results) {
    lines.push(
      `| ${c.id} | ${caseTitle(t, c.id)} | ${status(c.status)} | ${(c.elapsedMs / 1000).toFixed(1)}s | ${c.usage.input} / ${c.usage.output} |`
    )
  }
  lines.push('')

  lines.push(`## ${t(`${p}.report.details`)}`, '')
  for (const c of r.results) {
    lines.push(`### ${c.id}. ${caseTitle(t, c.id)} — ${status(c.status)}`, '')
    if (c.error) lines.push(`> ${t(`${p}.report.error`)}: ${c.error}`, '')
    for (const a of c.assertions) {
      const actual = a.actual !== undefined ? ` — \`${a.actual.replace(/`/g, "'")}\`` : ''
      const tags = [a.level === 'bonus' ? t(`${p}.level.bonus`) : '', a.scope === 'fable' ? t(`${p}.scope.fable`) : '']
        .filter(Boolean)
        .join(', ')
      lines.push(`- [${STATUS_MARK[a.status]}] #${a.step} ${assertionText(t, a)}${tags ? ` (${tags})` : ''}${actual}`)
    }
    if (c.assertions.length) lines.push('')
    c.steps.forEach((step, i) => {
      const reply = step.reply
      const meta = reply ? `HTTP ${reply.status} · ${reply.elapsedMs} ms` : '-'
      lines.push(`#### #${i + 1} ${stepLabel(t, step)} (${meta})`, '')
      if (step.beta) lines.push(`anthropic-beta: \`${step.beta}\``, '')
      lines.push(`${t(`${p}.report.request`)}:`, '', fence(JSON.stringify(step.request, null, 2), 'json'), '')
      const resp = replyContent(step)
      lines.push(`${t(`${p}.report.response`)}:`, '', fence(resp.content, resp.lang), '')
    })
  }
  return lines.join('\n')
}

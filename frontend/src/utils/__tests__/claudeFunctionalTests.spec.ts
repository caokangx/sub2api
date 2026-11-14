import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/signatureVerify', () => ({
  adminSignatureVerifyAPI: { messages: vi.fn() }
}))

import { FUNCTIONAL_CASES, isFableModel } from '../claudeFunctionalTests/cases'
import { buildHaystack, HAYSTACK_QUESTION, HAYSTACK_SIZES } from '../claudeFunctionalTests/haystack'
import { buildReport, sanitize, toJSON, toMarkdown, truncateString } from '../claudeFunctionalTests/report'
import { estimateInputTokens, orderCases, runCase, runFunctionalTests } from '../claudeFunctionalTests/runner'
import { analyzeSSE, parseSSE, sseUsage } from '../claudeFunctionalTests/sse'
import type { AnyRecord, RawReply } from '../claudeFunctionalTests/types'
import type { ReasoningConn } from '../claudeReasoning'

const FABLE: ReasoningConn = { baseUrl: 'https://gw.example.com', apiKey: 'sk-secret-key', model: 'claude-fable-5-2' }
const SONNET: ReasoningConn = { ...FABLE, model: 'claude-sonnet-4-6' }

function json(status: number, body: unknown): RawReply {
  return { status, body, text: '', contentType: 'application/json', elapsedMs: 5 }
}

function message(content: AnyRecord[], extra: AnyRecord = {}): AnyRecord {
  return {
    type: 'message',
    role: 'assistant',
    content,
    stop_reason: 'end_turn',
    stop_sequence: null,
    usage: { input_tokens: 18, output_tokens: 20 },
    ...extra
  }
}

const THINKING = { type: 'thinking', thinking: '', signature: 'sig' }

function sse(events: [string, AnyRecord][]): string {
  return events.map(([e, d]) => `event: ${e}\ndata: ${JSON.stringify(d)}\n\n`).join('')
}

const GREETING_STREAM = sse([
  ['message_start', { type: 'message_start', message: { content: [], usage: { input_tokens: 19, output_tokens: 1 } } }],
  ['content_block_start', { type: 'content_block_start', index: 0, content_block: { type: 'thinking', thinking: '' } }],
  ['content_block_delta', { type: 'content_block_delta', index: 0, delta: { type: 'signature_delta', signature: 'x' } }],
  ['content_block_stop', { type: 'content_block_stop', index: 0 }],
  ['content_block_start', { type: 'content_block_start', index: 1, content_block: { type: 'text', text: '' } }],
  ['content_block_delta', { type: 'content_block_delta', index: 1, delta: { type: 'text_delta', text: 'Hello there, friend' } }],
  ['content_block_delta', { type: 'content_block_delta', index: 1, delta: { type: 'text_delta', text: '!' } }],
  ['content_block_stop', { type: 'content_block_stop', index: 1 }],
  ['message_delta', { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 26 } }],
  ['message_stop', { type: 'message_stop' }]
])

function caseById(id: string) {
  const def = FUNCTIONAL_CASES.find((c) => c.id === id)
  if (!def) throw new Error(`no case ${id}`)
  return def
}

describe('sse', () => {
  it('parses and validates a complete stream', () => {
    const events = parseSSE(GREETING_STREAM)
    const a = analyzeSSE(events)
    expect(a.firstType).toBe('message_start')
    expect(a.lastType).toBe('message_stop')
    expect(a.blocksPaired).toBe(true)
    expect(a.blockTypes).toEqual(['thinking', 'text'])
    expect(a.text).toBe('Hello there, friend!')
    expect(a.stopReason).toBe('end_turn')
    expect(sseUsage(events)).toMatchObject({ input_tokens: 19, output_tokens: 26 })
  })

  it('detects a stream without message_stop', () => {
    const truncated = GREETING_STREAM.slice(0, GREETING_STREAM.lastIndexOf('event: message_stop'))
    expect(analyzeSSE(parseSSE(truncated)).lastType).toBe('message_delta')
  })

  it('detects unpaired content blocks', () => {
    const raw = sse([
      ['message_start', { type: 'message_start', message: {} }],
      ['content_block_start', { type: 'content_block_start', index: 0, content_block: { type: 'text' } }],
      ['message_stop', { type: 'message_stop' }]
    ])
    expect(analyzeSSE(parseSSE(raw)).blocksPaired).toBe(false)

    const strayStop = sse([['content_block_stop', { type: 'content_block_stop', index: 3 }]])
    expect(analyzeSSE(parseSSE(strayStop)).blocksPaired).toBe(false)
  })

  it('ignores comments and pings', () => {
    const raw = `: keepalive\n\nevent: ping\ndata: {"type":"ping"}\n\n${GREETING_STREAM}`
    expect(analyzeSSE(parseSSE(raw)).firstType).toBe('message_start')
  })
})

describe('haystack', () => {
  it.each(Object.values(HAYSTACK_SIZES))('builds about %i characters with all needles', (size) => {
    const hay = buildHaystack(size)
    expect(Math.abs(hay.content.length - size) / size).toBeLessThan(0.01)
    expect(hay.content.endsWith(HAYSTACK_QUESTION)).toBe(true)
    expect(new Set(hay.nicknames).size).toBe(3)
    for (const nick of hay.nicknames) {
      expect(hay.content.split(nick).length - 1).toBe(1)
    }
    const positions = hay.nicknames.map((n) => hay.content.indexOf(n) / hay.content.length)
    expect(positions[0]).toBeLessThan(positions[1])
    expect(positions[1]).toBeLessThan(positions[2])
  })
})

describe('case selection', () => {
  it('runs long-context cases last and estimates tokens', () => {
    const order = orderCases(['22a', '1', '23']).map((c) => c.id)
    expect(order).toEqual(['1', '23', '22a'])
    const all = estimateInputTokens(FUNCTIONAL_CASES.map((c) => c.id))
    expect(all).toBeGreaterThan(1_400_000)
    expect(all).toBeLessThan(1_500_000)
  })

  it('recognizes claude-fable-5-2', () => {
    expect(isFableModel('claude-fable-5-2')).toBe(true)
    expect(isFableModel('anthropic.claude-fable-5-2')).toBe(true)
    expect(isFableModel('claude-fable-5-1')).toBe(false)
  })
})

describe('cases', () => {
  it('case 1 passes on pong with a leading thinking block', async () => {
    const send = vi.fn(async () => json(200, message([THINKING, { type: 'text', text: 'pong' }])))
    const res = await runCase(caseById('1'), FABLE, undefined, send)
    expect(send).toHaveBeenCalledTimes(2)
    expect(res.status).toBe('pass')
    expect(res.assertions.every((a) => a.status === 'pass')).toBe(true)
    expect(res.usage).toEqual({ input: 36, output: 40 })
  })

  it('case 1 fails when the text is wrong', async () => {
    const send = vi.fn(async () => json(200, message([{ type: 'text', text: 'ping' }])))
    const res = await runCase(caseById('1'), FABLE, undefined, send)
    expect(res.status).toBe('fail')
    expect(res.assertions.find((a) => a.key === 'textEquals')?.status).toBe('fail')
    expect(res.assertions.find((a) => a.key === 'hasThinking')?.status).toBe('fail')
  })

  it('case 1 records the thinking check as info on other models', async () => {
    const send = vi.fn(async () => json(200, message([{ type: 'text', text: 'pong' }])))
    const res = await runCase(caseById('1'), SONNET, undefined, send)
    expect(res.status).toBe('pass')
    expect(res.assertions.filter((a) => a.key === 'hasThinking').map((a) => a.status)).toEqual(['info', 'info'])
  })

  it('case 7 checks the stop sequence', async () => {
    const send = vi.fn(async () =>
      json(200, message([{ type: 'text', text: 'START alpha beta ' }], { stop_reason: 'stop_sequence', stop_sequence: 'STOP' }))
    )
    const res = await runCase(caseById('7'), FABLE, undefined, send)
    expect(res.status).toBe('pass')
    const body = send.mock.calls[0][1] as AnyRecord
    expect(body.stop_sequences).toEqual(['STOP'])
  })

  it('case 9 sends both beta headers', async () => {
    const send = vi.fn(async () => json(200, message([{ type: 'text', text: 'pong' }])))
    await runCase(caseById('9'), FABLE, undefined, send)
    expect(send.mock.calls.map((c) => (c[2] as { beta?: string }).beta)).toEqual([
      'prompt-caching-2024-07-31,context-1m-2025-08-07',
      'unknown-beta-2099-01-01'
    ])
  })

  it('case 19 requires a strict 400 without the beta header', async () => {
    const err = {
      type: 'error',
      error: { type: 'aws_invoke_error', message: "tools.0: Input tag 'computer_20251124' found using 'type'" }
    }
    const send = vi.fn(async () => json(400, err))
    const res = await runCase(caseById('19'), FABLE, undefined, send)
    expect((send.mock.calls[0][2] as { beta?: string }).beta).toBeUndefined()
    expect(res.status).toBe('pass')

    const autoBeta = vi.fn(async () => json(200, message([{ type: 'text', text: 'pong' }])))
    expect((await runCase(caseById('19'), FABLE, undefined, autoBeta)).status).toBe('fail')
    const other = await runCase(caseById('19'), SONNET, undefined, autoBeta)
    expect(other.status).toBe('pass')
    expect(other.assertions.every((a) => a.status === 'info')).toBe(true)
  })

  it('case 20 expects the adaptive-thinking error, then the answer 12', async () => {
    const send = vi
      .fn()
      .mockResolvedValueOnce(
        json(400, {
          type: 'error',
          error: {
            type: 'invalid_request_error',
            message: 'claude-fable-5-2 requires adaptive thinking; omit thinking or use thinking.type=adaptive'
          }
        })
      )
      .mockResolvedValueOnce(json(200, message([THINKING, { type: 'text', text: 'Fermat...\n\n**12**' }])))
    const res = await runCase(caseById('20'), FABLE, undefined, send)
    expect(res.status).toBe('pass')
    expect(res.assertions.find((a) => a.key === 'finalNumber')).toMatchObject({ status: 'pass', step: 2 })
    expect((send.mock.calls[1][1] as AnyRecord).thinking).toBeUndefined()
  })

  it('case 21a validates the event stream', async () => {
    const send = vi.fn(async () => ({
      status: 200,
      body: null,
      text: GREETING_STREAM,
      contentType: 'text/event-stream',
      elapsedMs: 9
    }))
    const res = await runCase(caseById('21a'), FABLE, undefined, send)
    expect(res.status).toBe('pass')
    expect(res.usage).toEqual({ input: 19, output: 26 })
  })

  it('case 21a fails on a non-stream reply', async () => {
    const send = vi.fn(async () => json(200, message([{ type: 'text', text: 'Hello there, friend!' }])))
    const res = await runCase(caseById('21a'), FABLE, undefined, send)
    expect(res.status).toBe('fail')
    expect(res.assertions.find((a) => a.key === 'eventStream')?.status).toBe('fail')
  })

  it('marks relay failures as error and keeps going; abort skips the rest', async () => {
    const failing = vi.fn(async () => {
      throw new Error('upstream request failed')
    })
    const results = await runFunctionalTests(FABLE, ['1', '2'], { send: failing })
    expect(results.map((r) => r.status)).toEqual(['error', 'error'])
    expect(results[0].error).toBe('upstream request failed')

    const controller = new AbortController()
    controller.abort()
    const skipped = await runFunctionalTests(FABLE, ['1', '2'], { send: failing, signal: controller.signal })
    expect(skipped.map((r) => r.status)).toEqual(['skipped', 'skipped'])
  })
})

describe('report', () => {
  const t = (key: string, params?: Record<string, unknown>) =>
    params && Object.keys(params).length ? `${key}${JSON.stringify(params)}` : key

  it('truncates long strings keeping head and tail', () => {
    const s = 'a'.repeat(5000) + 'TAIL'
    const out = truncateString(s)
    expect(out.length).toBeLessThan(2000)
    expect(out.startsWith('aaa')).toBe(true)
    expect(out.endsWith('TAIL')).toBe(true)
    expect(out).toContain('5004 chars')
    expect(truncateString('short')).toBe('short')
  })

  it('masks the API key and shortens huge request bodies in exports', async () => {
    const send = vi.fn(async () =>
      json(401, { type: 'error', error: { type: 'authentication_error', message: 'bad key sk-secret-key' } })
    )
    const results = await runFunctionalTests(FABLE, ['1', '22a'], { send })
    const report = buildReport(
      { model: FABLE.model, baseUrl: FABLE.baseUrl, isFable: true, startedAt: new Date(0), finishedAt: new Date(1500) },
      results
    )
    expect(report.summary).toMatchObject({ total: 2, fail: 2 })
    expect(report.elapsedMs).toBe(1500)

    const exported = toJSON(report, FABLE.apiKey)
    expect(exported).not.toContain('sk-secret-key')
    expect(exported).toContain('bad key ***')
    expect(exported.length).toBeLessThan(20_000)

    const md = toMarkdown(report, FABLE.apiKey, t)
    expect(md).not.toContain('sk-secret-key')
    expect(md).toContain('admin.signatureDecoder.functional.cases.c22a')
    expect(md).toContain('[FAIL]')

    expect(sanitize({ k: 'x sk-secret-key y' }, FABLE.apiKey)).toEqual({ k: 'x *** y' })
  })
})

import type { AnyRecord } from './types'

export interface SSEEvent {
  event: string
  /** Parsed JSON payload; null when the data line is not JSON. */
  data: AnyRecord | null
}

/** Split a raw text/event-stream body into events. */
export function parseSSE(raw: string): SSEEvent[] {
  const events: SSEEvent[] = []
  for (const chunk of raw.split(/\r?\n\r?\n/)) {
    let event = ''
    const dataLines: string[] = []
    for (const line of chunk.split(/\r?\n/)) {
      if (!line || line.startsWith(':')) continue
      const idx = line.indexOf(':')
      const name = idx === -1 ? line : line.slice(0, idx)
      const value = idx === -1 ? '' : line.slice(idx + 1).replace(/^ /, '')
      if (name === 'event') event = value
      else if (name === 'data') dataLines.push(value)
    }
    if (!event && dataLines.length === 0) continue
    let data: AnyRecord | null = null
    try {
      const parsed = JSON.parse(dataLines.join('\n'))
      data = parsed && typeof parsed === 'object' ? (parsed as AnyRecord) : null
    } catch {
      data = null
    }
    const type = typeof data?.type === 'string' ? data.type : ''
    events.push({ event: event || type, data })
  }
  return events
}

export interface SSEAnalysis {
  eventCount: number
  firstType: string
  lastType: string
  /** Every content_block_start has a matching content_block_stop and vice versa. */
  blocksPaired: boolean
  blockTypes: string[]
  text: string
  stopReason: string
  errorMessage: string
}

function typeOf(e: SSEEvent): string {
  const t = e.data?.type
  return typeof t === 'string' ? t : e.event
}

function rec(value: unknown): AnyRecord | null {
  return value && typeof value === 'object' ? (value as AnyRecord) : null
}

export function analyzeSSE(events: SSEEvent[]): SSEAnalysis {
  const types = events.map(typeOf).filter((t) => t !== 'ping')
  const open = new Set<number>()
  let paired = true
  const blockTypes: string[] = []
  let text = ''
  let stopReason = ''
  let errorMessage = ''

  for (const e of events) {
    const d = e.data
    const index = typeof d?.index === 'number' ? d.index : -1
    switch (typeOf(e)) {
      case 'content_block_start': {
        if (open.has(index)) paired = false
        open.add(index)
        const block = rec(d?.content_block)
        blockTypes.push(typeof block?.type === 'string' ? block.type : '')
        break
      }
      case 'content_block_stop':
        if (!open.delete(index)) paired = false
        break
      case 'content_block_delta': {
        const delta = rec(d?.delta)
        if (delta?.type === 'text_delta' && typeof delta.text === 'string') text += delta.text
        break
      }
      case 'message_delta': {
        const reason = rec(d?.delta)?.stop_reason
        if (typeof reason === 'string') stopReason = reason
        break
      }
      case 'error': {
        const message = rec(d?.error)?.message
        errorMessage = typeof message === 'string' ? message : 'error'
        break
      }
    }
  }
  if (open.size > 0) paired = false

  return {
    eventCount: events.length,
    firstType: types[0] ?? '',
    lastType: types[types.length - 1] ?? '',
    blocksPaired: paired,
    blockTypes,
    text,
    stopReason,
    errorMessage
  }
}

/** message_start usage, with the final counts from message_delta layered on top. */
export function sseUsage(events: SSEEvent[]): AnyRecord {
  let usage: AnyRecord = {}
  for (const e of events) {
    const t = typeOf(e)
    if (t === 'message_start') {
      usage = { ...usage, ...(rec(rec(e.data?.message)?.usage) ?? {}) }
    } else if (t === 'message_delta') {
      usage = { ...usage, ...(rec(e.data?.usage) ?? {}) }
    }
  }
  return usage
}

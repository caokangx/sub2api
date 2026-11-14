import axios from 'axios'
import { adminSignatureVerifyAPI } from '@/api/admin/signatureVerify'
import type { ReasoningConn } from '@/utils/claudeReasoning'
import { parseSSE, sseUsage } from './sse'
import {
  FunctionalTestAborted,
  FunctionalTestError,
  type AnyRecord,
  type CaseUsage,
  type RawReply,
  type SendOptions
} from './types'

export function asRecord(value: unknown): AnyRecord | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as AnyRecord) : null
}

function isCanceled(err: unknown): boolean {
  return axios.isCancel(err) || asRecord(err)?.code === 'ERR_CANCELED'
}

/**
 * Send one Messages request through the backend relay. Upstream errors (4xx/5xx) come
 * back as a normal reply so negative cases can assert on them; only relay failures throw.
 */
export async function sendRaw(
  conn: ReasoningConn,
  body: AnyRecord,
  opts: SendOptions & { signal?: AbortSignal } = {}
): Promise<RawReply> {
  if (opts.signal?.aborted) throw new FunctionalTestAborted()
  try {
    const res = await adminSignatureVerifyAPI.messages(
      {
        base_url: conn.baseUrl.trim().replace(/\/+$/, ''),
        api_key: conn.apiKey.trim(),
        body,
        ...(opts.beta ? { anthropic_beta: opts.beta } : {})
      },
      opts.signal
    )
    return {
      status: res.status,
      body: res.body ?? null,
      text: res.text ?? '',
      contentType: res.content_type ?? '',
      elapsedMs: res.elapsed_ms ?? 0
    }
  } catch (err) {
    if (isCanceled(err) || opts.signal?.aborted) throw new FunctionalTestAborted()
    const message = asRecord(err)?.message
    throw new FunctionalTestError(typeof message === 'string' && message ? message : String(err))
  }
}

export function contentBlocks(reply: RawReply | null): AnyRecord[] {
  const content = asRecord(reply?.body)?.content
  return Array.isArray(content) ? content.map(asRecord).filter((b): b is AnyRecord => !!b) : []
}

export function blocksOf(reply: RawReply | null, type: string): AnyRecord[] {
  return contentBlocks(reply).filter((b) => b.type === type)
}

/** All text blocks joined; never assumes content[0] is the text. */
export function textOf(reply: RawReply | null): string {
  return blocksOf(reply, 'text')
    .map((b) => (typeof b.text === 'string' ? b.text : ''))
    .join('')
}

export function field(reply: RawReply | null, key: string): unknown {
  return asRecord(reply?.body)?.[key]
}

export function usageOf(reply: RawReply | null): AnyRecord {
  return asRecord(field(reply, 'usage')) ?? {}
}

export function num(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

export function errorOf(reply: RawReply | null): { type: string; message: string } {
  const err = asRecord(field(reply, 'error'))
  return {
    type: typeof err?.type === 'string' ? err.type : '',
    message: typeof err?.message === 'string' ? err.message : reply?.text ?? ''
  }
}

export function isEventStream(reply: RawReply | null): boolean {
  return !!reply && reply.contentType.toLowerCase().startsWith('text/event-stream')
}

/** Input (including cached) and output tokens of a JSON or SSE reply. */
export function replyUsage(reply: RawReply | null): CaseUsage {
  if (!reply) return { input: 0, output: 0 }
  const u = isEventStream(reply) ? sseUsage(parseSSE(reply.text)) : usageOf(reply)
  return {
    input: num(u.input_tokens) + num(u.cache_creation_input_tokens) + num(u.cache_read_input_tokens),
    output: num(u.output_tokens)
  }
}

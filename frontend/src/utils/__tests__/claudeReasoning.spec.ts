import { beforeEach, describe, expect, it, vi } from 'vitest'

const messagesMock = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/signatureVerify', () => ({
  adminSignatureVerifyAPI: { messages: messagesMock }
}))

import {
  buildHarvestBody,
  buildMemorizePrompt,
  buildMessagesCurl,
  buildReplayBody,
  extractAnswer,
  extractSignature,
  harvest,
  randomSecret,
  ReasoningError,
  replay,
  VERIFY_ELICITORS,
  VERIFY_PRECEDING_USER
} from '../claudeReasoning'

const conn = { baseUrl: 'https://gw.example.com/', apiKey: ' sk-1 ', model: 'claude-sonnet-4-6' }

describe('backend relay', () => {
  beforeEach(() => messagesMock.mockReset())

  it('sends harvest through the backend and reads the signature', async () => {
    messagesMock.mockResolvedValue({
      status: 200,
      body: {
        content: [
          { type: 'thinking', thinking: '', signature: 'SIG' },
          { type: 'text', text: 'Done.' }
        ],
        usage: { output_tokens_details: { thinking_tokens: 42 } }
      }
    })
    const res = await harvest(conn, 'prompt')

    expect(res).toMatchObject({ signature: 'SIG', answer: 'Done.', thinkingTokens: 42 })
    const payload = messagesMock.mock.calls[0][0]
    expect(payload.base_url).toBe('https://gw.example.com')
    expect(payload.api_key).toBe('sk-1')
    expect(payload.body).toMatchObject({
      model: 'claude-sonnet-4-6',
      thinking: { type: 'adaptive', display: 'omitted' },
      messages: [{ role: 'user', content: 'prompt' }]
    })
  })

  it('replays with the bound model and the signature in an assistant thinking block', async () => {
    messagesMock.mockResolvedValue({ status: 200, body: { content: [{ type: 'text', text: 'X' }] } })
    await replay(conn, 'SIG', { precedingUser: 'p', elicit: 'e' }, 'claude-opus-4-7')

    const body = messagesMock.mock.calls[0][0].body
    expect(body.model).toBe('claude-opus-4-7')
    expect(body.messages[1].content[0]).toEqual({ type: 'thinking', thinking: '', signature: 'SIG' })
  })

  it('maps upstream error bodies, bad statuses, non-JSON and relay failures', async () => {
    messagesMock.mockResolvedValueOnce({ status: 401, body: { error: { message: 'invalid x-api-key' } } })
    await expect(harvest(conn, 'p')).rejects.toThrow('invalid x-api-key')

    messagesMock.mockResolvedValueOnce({ status: 529, body: { content: [] } })
    await expect(harvest(conn, 'p')).rejects.toThrow('http:529')

    messagesMock.mockResolvedValueOnce({ status: 502, text: '<html>' })
    await expect(harvest(conn, 'p')).rejects.toThrow('nonJson:<html>')

    messagesMock.mockRejectedValueOnce({ status: 400, message: 'Invalid base_url: host is not allowed' })
    await expect(harvest(conn, 'p')).rejects.toThrow('proxy:Invalid base_url: host is not allowed')
  })

  it('validates inputs before calling the backend', async () => {
    await expect(harvest({ ...conn, baseUrl: ' ' }, 'p')).rejects.toBeInstanceOf(ReasoningError)
    await expect(harvest({ ...conn, apiKey: '' }, 'p')).rejects.toThrow('missingApiKey')
    await expect(replay(conn, '  ', { precedingUser: 'p', elicit: 'e' })).rejects.toThrow('missingSignature')
    expect(messagesMock).not.toHaveBeenCalled()
  })
})

describe('randomSecret', () => {
  it('uses the WORD-XXXXX-WORD-NN format', () => {
    for (let i = 0; i < 200; i++) {
      expect(randomSecret()).toMatch(/^[A-Z]+-[A-HJ-NP-Z2-9]{5}-[A-Z]+-[1-9]\d$/)
    }
  })

  it('differs between calls', () => {
    const seen = new Set(Array.from({ length: 50 }, () => randomSecret()))
    expect(seen.size).toBeGreaterThan(45)
  })
})

describe('verification prompts', () => {
  it('plants the secret only in the harvest prompt', () => {
    const secret = randomSecret()
    expect(buildMemorizePrompt(secret)).toContain(secret)
    expect(VERIFY_PRECEDING_USER).not.toContain(secret)
    for (const elicit of VERIFY_ELICITORS) {
      expect(elicit).not.toContain(secret)
    }
  })
})

describe('curl builder', () => {
  it('mirrors the relay endpoint, headers and body for a harvest request', () => {
    const body = buildHarvestBody('claude-sonnet-4-6', 'prompt')
    const curl = buildMessagesCurl(conn, body)
    expect(curl).toContain("curl 'https://gw.example.com/v1/messages'")
    expect(curl).toContain("-H 'content-type: application/json'")
    expect(curl).toContain("-H 'x-api-key:  sk-1 '")
    expect(curl).toContain("-H 'anthropic-version: 2023-06-01'")
    expect(curl).toContain('"display": "omitted"')
    expect(curl).toContain('"content": "prompt"')
  })

  it('serializes the replayed signature into the second request body', () => {
    const body = buildReplayBody('claude-opus-4-7', 'SIG', {
      precedingUser: VERIFY_PRECEDING_USER,
      elicit: VERIFY_ELICITORS[0]
    })
    const curl = buildMessagesCurl({ ...conn, baseUrl: 'https://api.anthropic.com' }, body)
    expect(curl).toContain("curl 'https://api.anthropic.com/v1/messages'")
    expect(curl).toContain('"signature": "SIG"')
    expect(curl).toContain('"model": "claude-opus-4-7"')
  })

  it('single-quote-escapes an api key so the command stays valid', () => {
    const curl = buildMessagesCurl({ ...conn, apiKey: "sk-a'b" }, buildHarvestBody('m', 'p'))
    expect(curl).toContain("-H 'x-api-key: sk-a'\\''b'")
  })
})

describe('response extraction', () => {
  const data = {
    content: [
      { type: 'thinking', thinking: '', signature: 'SIG' },
      { type: 'text', text: 'Hello ' },
      { type: 'text', text: 'world' }
    ]
  }

  it('reads the signature from the thinking block', () => {
    expect(extractSignature(data)).toBe('SIG')
    expect(extractSignature({ content: [{ type: 'redacted_thinking', signature: 'R' }] })).toBe('R')
    expect(extractSignature({ content: [{ type: 'text', text: 'x' }] })).toBeNull()
  })

  it('joins visible text blocks', () => {
    expect(extractAnswer(data)).toBe('Hello world')
    expect(extractAnswer({})).toBe('')
  })
})

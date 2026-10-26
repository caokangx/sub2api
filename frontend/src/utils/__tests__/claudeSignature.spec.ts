import { describe, expect, it, vi } from 'vitest'
import {
  flattenProtoFields,
  normalizeSignatureInput,
  parseClaudeSignature,
  SignatureParseError,
  toLocalTime
} from '../claudeSignature'

const SAMPLE =
  'EvsCCsABCBIYAipAmFArqoMaEOs1eo+ObC8OY4HgvH4hUDKdqq1sczZsuB8NOKMugxEH41+OWF16IF5qFMdhEl5VlyUHN+7eL17fSjIRY2xhdWRlLXNvbm5ldC00LTY4AEIIdGhpbmtpbmdaJDFhZTcwOTk1LTY4MTktNDZmMi05MTEzLTkxZTBjNzJjZGQ1NXIQCFObsh/6yvSRk0HKO6B8KIgBAZoBEwoRY2xhdWRlLXNvbm5ldC00LTaoAarzrtYGsAECEgzSE9viGW/16rmTqDUaDIE5rt2ZRgG/eZkfryIwhoO4mPV14e8KcyzmSqHlooWVYlDv+AdFMR8PXCoyxQzi/3l5jBCNdPb+mn57qOG6KmgItNxhKCxcyTcohtVdRIoTeuMK40hcdxpx1mvMJByMMWHYI5ocHPnACxUCBmuuh7p9pixBQUQp1omcf0pPiK+q8U7eJyEwNe0q38+/jY4mqa+KoFNn47UMxWh5qys+D5A2HwCedDsEKxgB'

describe('parseClaudeSignature', () => {
  it('decodes the plaintext metadata of a Claude thinking signature', () => {
    const parsed = parseClaudeSignature(SAMPLE)

    expect(parsed.byteLength).toBe(384)
    expect(parsed.summary).toEqual({
      model: 'claude-sonnet-4-6',
      requestedModel: 'claude-sonnet-4-6',
      blockType: 'thinking',
      id: '1ae70995-6819-46f2-9113-91e0c72cdd55',
      createdAt: '2026-10-11T16:30:34.000Z',
      createdAtUnix: '1791736234'
    })

    const flat = flattenProtoFields(parsed.fields)
    const byPath = Object.fromEntries(flat.map((f) => [f.path, f]))
    expect(byPath['2.1.5']).toMatchObject({ kind: 'bytes', length: 64 })
    expect(byPath['2.2']).toMatchObject({ kind: 'bytes', length: 12 })
    expect(byPath['2.5']).toMatchObject({ kind: 'bytes', length: 104 })
    expect(byPath['3']).toMatchObject({ kind: 'varint', value: '1' })
  })

  it('accepts a pasted JSON content block', () => {
    const block = `{"type":"thinking","thinking":"...","signature":"${SAMPLE}"}`
    expect(normalizeSignatureInput(block)).toBe(SAMPLE)
    expect(parseClaudeSignature(block).summary.model).toBe('claude-sonnet-4-6')
  })

  it('converts timestamps to local time with the zone offset', () => {
    const result = toLocalTime('2026-10-11T16:30:34.000Z')
    expect(result.local).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
    expect(result.utc).toBe('2026-10-11 16:30:34 UTC')

    const offset = vi.spyOn(Date.prototype, 'getTimezoneOffset')
    offset.mockReturnValue(-480)
    expect(toLocalTime('2026-10-11T16:30:34.000Z').zone).toBe('UTC+8')
    offset.mockReturnValue(-330)
    expect(toLocalTime('2026-10-11T16:30:34.000Z').zone).toBe('UTC+5:30')
    offset.mockReturnValue(300)
    expect(toLocalTime('2026-10-11T16:30:34.000Z').zone).toBe('UTC-5')
    offset.mockRestore()
  })

  it('rejects input that is not a protobuf signature', () => {
    expect(() => parseClaudeSignature('')).toThrow(SignatureParseError)
    expect(() => parseClaudeSignature('not base64!')).toThrow(SignatureParseError)
    expect(() => parseClaudeSignature(btoa('hello world'))).toThrow(SignatureParseError)
  })
})

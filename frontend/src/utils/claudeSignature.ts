export type ProtoFieldKind = 'varint' | 'fixed64' | 'fixed32' | 'string' | 'bytes' | 'message'

export interface ProtoField {
  path: string
  field: number
  kind: ProtoFieldKind
  length?: number
  value: string
  timestamp?: string
  children?: ProtoField[]
}

export interface ClaudeSignatureSummary {
  model?: string
  requestedModel?: string
  blockType?: string
  id?: string
  createdAt?: string
  createdAtUnix?: string
}

export interface ParsedClaudeSignature {
  byteLength: number
  fields: ProtoField[]
  summary: ClaudeSignatureSummary
}

// Anthropic does not publish this format; the meanings are inferred from observed samples.
export const KNOWN_SIGNATURE_FIELDS: Record<string, string> = {
  '2.1.1': 'version',
  '2.1.3': 'type',
  '2.1.5': 'mac',
  '2.1.6': 'model',
  '2.1.8': 'blockType',
  '2.1.11': 'id',
  '2.1.14': 'digest',
  '2.1.19.1': 'requestedModel',
  '2.1.21': 'createdAt',
  '2.2': 'nonce',
  '2.3': 'nonce',
  '2.4': 'wrappedKey',
  '2.5': 'ciphertext',
  '3': 'outerVersion'
}

const MIN_TIMESTAMP = 1_500_000_000n
const MAX_TIMESTAMP = 2_500_000_000n

export class SignatureParseError extends Error {}

export function normalizeSignatureInput(input: string): string {
  let text = input.trim()
  const jsonMatch = text.match(/"signature"\s*:\s*"([^"]*)"/)
  if (jsonMatch) {
    text = jsonMatch[1]
  }
  return text.replace(/^["']|["']$/g, '').replace(/\s+/g, '')
}

export function decodeBase64(input: string): Uint8Array {
  let text = input.replace(/-/g, '+').replace(/_/g, '/')
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(text)) {
    throw new SignatureParseError('invalidBase64')
  }
  text = text.replace(/=+$/, '')
  if (text.length % 4 === 1) {
    throw new SignatureParseError('invalidBase64')
  }
  text += '='.repeat((4 - (text.length % 4)) % 4)
  const binary = atob(text)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}

function readVarint(bytes: Uint8Array, offset: number): [bigint, number] {
  let result = 0n
  let shift = 0n
  let i = offset
  while (i < bytes.length) {
    const byte = bytes[i++]
    result |= BigInt(byte & 0x7f) << shift
    if (byte < 0x80) {
      return [result, i]
    }
    shift += 7n
    if (shift > 63n) break
  }
  throw new SignatureParseError('truncated')
}

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

const utf8 = new TextDecoder('utf-8', { fatal: true })

function asPrintableString(bytes: Uint8Array): string | null {
  if (bytes.length === 0) return ''
  try {
    const text = utf8.decode(bytes)
    return /^[^\p{Cc}\p{Cs}\uFFFD]+$/u.test(text) ? text : null
  } catch {
    return null
  }
}

function isLikelyMessage(bytes: Uint8Array): boolean {
  if (bytes.length === 0) return false
  let i = 0
  try {
    while (i < bytes.length) {
      const [key, next] = readVarint(bytes, i)
      i = next
      const field = Number(key >> 3n)
      const wire = Number(key & 7n)
      if (field === 0 || field > 10_000) return false
      if (wire === 0) {
        i = readVarint(bytes, i)[1]
      } else if (wire === 1) {
        i += 8
      } else if (wire === 2) {
        const [len, after] = readVarint(bytes, i)
        i = after + Number(len)
      } else if (wire === 5) {
        i += 4
      } else {
        return false
      }
      if (i > bytes.length) return false
    }
  } catch {
    return false
  }
  return true
}

function parseMessage(bytes: Uint8Array, parentPath: string): ProtoField[] {
  const fields: ProtoField[] = []
  let i = 0
  while (i < bytes.length) {
    const [key, next] = readVarint(bytes, i)
    i = next
    const field = Number(key >> 3n)
    const wire = Number(key & 7n)
    const path = parentPath ? `${parentPath}.${field}` : String(field)

    if (wire === 0) {
      const [value, after] = readVarint(bytes, i)
      i = after
      const entry: ProtoField = { path, field, kind: 'varint', value: value.toString() }
      if (value >= MIN_TIMESTAMP && value <= MAX_TIMESTAMP) {
        entry.timestamp = new Date(Number(value) * 1000).toISOString()
      }
      fields.push(entry)
    } else if (wire === 1 || wire === 5) {
      const size = wire === 1 ? 8 : 4
      if (i + size > bytes.length) throw new SignatureParseError('truncated')
      fields.push({
        path,
        field,
        kind: wire === 1 ? 'fixed64' : 'fixed32',
        length: size,
        value: toHex(bytes.subarray(i, i + size))
      })
      i += size
    } else if (wire === 2) {
      const [len, after] = readVarint(bytes, i)
      const end = after + Number(len)
      if (end > bytes.length) throw new SignatureParseError('truncated')
      const chunk = bytes.subarray(after, end)
      i = end
      const text = asPrintableString(chunk)
      if (text !== null) {
        fields.push({ path, field, kind: 'string', length: chunk.length, value: text })
      } else if (isLikelyMessage(chunk)) {
        fields.push({
          path,
          field,
          kind: 'message',
          length: chunk.length,
          value: '',
          children: parseMessage(chunk, path)
        })
      } else {
        fields.push({ path, field, kind: 'bytes', length: chunk.length, value: toHex(chunk) })
      }
    } else {
      throw new SignatureParseError('unsupportedWireType')
    }
  }
  return fields
}

function findField(fields: ProtoField[], path: string): ProtoField | undefined {
  for (const field of fields) {
    if (field.path === path) return field
    if (field.children && path.startsWith(`${field.path}.`)) {
      const found = findField(field.children, path)
      if (found) return found
    }
  }
  return undefined
}

export function parseClaudeSignature(input: string): ParsedClaudeSignature {
  const normalized = normalizeSignatureInput(input)
  if (!normalized) {
    throw new SignatureParseError('empty')
  }
  const bytes = decodeBase64(normalized)
  if (!isLikelyMessage(bytes)) {
    throw new SignatureParseError('notProtobuf')
  }
  const fields = parseMessage(bytes, '')
  const createdAt = findField(fields, '2.1.21')
  return {
    byteLength: bytes.length,
    fields,
    summary: {
      model: findField(fields, '2.1.6')?.value,
      requestedModel: findField(fields, '2.1.19.1')?.value,
      blockType: findField(fields, '2.1.8')?.value,
      id: findField(fields, '2.1.11')?.value,
      createdAt: createdAt?.timestamp,
      createdAtUnix: createdAt?.timestamp ? createdAt.value : undefined
    }
  }
}

export interface LocalTime {
  local: string
  zone: string
  utc: string
}

export function toLocalTime(iso: string): LocalTime {
  const date = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  const local =
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  const offset = -date.getTimezoneOffset()
  const hours = Math.floor(Math.abs(offset) / 60)
  const minutes = Math.abs(offset) % 60
  const zone = `UTC${offset >= 0 ? '+' : '-'}${hours}${minutes ? `:${pad(minutes)}` : ''}`
  const utc = date.toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC')
  return { local, zone, utc }
}

export interface FlatProtoField extends ProtoField {
  depth: number
}

export function flattenProtoFields(fields: ProtoField[], depth = 0): FlatProtoField[] {
  return fields.flatMap((field) => [
    { ...field, depth },
    ...(field.children ? flattenProtoFields(field.children, depth + 1) : [])
  ])
}

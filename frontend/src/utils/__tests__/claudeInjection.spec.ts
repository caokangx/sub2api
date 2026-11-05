import { beforeEach, describe, expect, it, vi } from 'vitest'

const messagesMock = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/signatureVerify', () => ({
  adminSignatureVerifyAPI: { messages: messagesMock }
}))

import {
  analyzeInjection,
  buildInjectionProbe,
  evaluateInjection,
  findInjectionCalibration,
  fitLine,
  PADDING_STEPS,
  PADDING_UNIT,
  totalInputTokens,
  type InjectionSample
} from '../claudeInjection'

function usage(input: number, cacheCreation = 0, cacheRead = 0) {
  return { input, cacheCreation, cacheRead, total: input + cacheCreation + cacheRead }
}

/** Samples a relay would report: reported = inflation × (baseline + injected + perUnit × N). */
function samples(baseline: number, perUnit: number, injected = 0, inflation = 1): InjectionSample[] {
  return PADDING_STEPS.map((units) => ({
    units,
    usage: usage(Math.round(inflation * (baseline + injected + perUnit * units)))
  }))
}

describe('totalInputTokens', () => {
  it('adds cached prompt tokens to the input count', () => {
    expect(
      totalInputTokens({ input_tokens: 9, cache_creation_input_tokens: 4000, cache_read_input_tokens: 300 })
    ).toEqual({ input: 9, cacheCreation: 4000, cacheRead: 300, total: 4309 })
  })

  it('treats missing or malformed fields as zero', () => {
    expect(totalInputTokens(undefined)).toEqual(usage(0))
    expect(totalInputTokens({ input_tokens: '12' })).toEqual(usage(0))
  })
})

describe('fitLine', () => {
  it('fits an exact line', () => {
    const fit = fitLine([
      { x: 0, y: 8 },
      { x: 200, y: 208 },
      { x: 800, y: 808 }
    ])
    expect(fit.intercept).toBeCloseTo(8)
    expect(fit.slope).toBeCloseTo(1)
    expect(fit.maxResidual).toBeCloseTo(0)
  })

  it('reports the largest residual of a noisy line', () => {
    const fit = fitLine([
      { x: 0, y: 10 },
      { x: 200, y: 260 },
      { x: 800, y: 810 }
    ])
    expect(fit.maxResidual).toBeGreaterThan(3)
  })
})

describe('findInjectionCalibration', () => {
  it.each([
    ['claude-sonnet-4-6', 8, 1],
    ['claude-sonnet-4-5-20250929', 8, 1],
    ['claude-haiku-4-5-20251001', 8, 1],
    ['claude-opus-4-6', 8, 1],
    ['claude-opus-4-7', 13, 2],
    ['claude-opus-4-8', 8, 2],
    ['claude-opus-5', 8, 2],
    ['claude-sonnet-5', 8, 2],
    ['claude-fable-5', 8, 2],
    ['claude-sonnet-5-5', 10, 2],
    ['claude-opus-5-5', 10, 2],
    ['claude-haiku-5-5', 10, 2],
    ['claude-fable-5-1', 10, 2]
  ])('%s', (model, baseline, tokensPerUnit) => {
    expect(findInjectionCalibration(model)).toEqual({ baseline, tokensPerUnit })
  })

  it('returns null for unknown models', () => {
    expect(findInjectionCalibration('claude-3-5-sonnet-20241022')).toBeNull()
    expect(findInjectionCalibration('gpt-6-sol')).toBeNull()
  })
})

describe('evaluateInjection', () => {
  it('finds no injection on a genuine upstream', () => {
    // Measured on the Bedrock relay: 8 / 208 / 808.
    const report = evaluateInjection('claude-sonnet-4-6', samples(8, 1))
    expect(report.verdict).toBe('none')
    expect(report.injectedTokens).toBe(0)
    expect(report.inflation).toBeCloseTo(1)
    expect(report.inflated).toBe(false)
    expect(report.hints).toEqual([])
    expect(report.rows.map((r) => r.expected)).toEqual([8, 208, 808])
  })

  it('measures a fixed injected system prompt', () => {
    const report = evaluateInjection('claude-sonnet-5-5', samples(10, 2, 4000))
    expect(report.verdict).toBe('major')
    expect(report.injectedTokens).toBe(4000)
    expect(report.inflated).toBe(false)
  })

  it('classifies a small injection as minor', () => {
    expect(evaluateInjection('claude-opus-4-8', samples(8, 2, 120)).verdict).toBe('minor')
  })

  it('detects proportional inflation and undoes it before measuring injection', () => {
    const report = evaluateInjection('claude-opus-4-7', samples(13, 2, 0, 1.3))
    expect(report.inflated).toBe(true)
    expect(report.inflation).toBeCloseTo(1.3, 2)
    expect(report.injectedTokens).toBeLessThanOrEqual(1)
    expect(report.verdict).toBe('none')
  })

  it('counts cached injected tokens and flags the cache', () => {
    const cached: InjectionSample[] = PADDING_STEPS.map((units, i) => ({
      units,
      // First call writes the injected prompt to cache, later calls read it.
      usage: i === 0 ? usage(8 + units, 3000, 0) : usage(8 + units, 0, 3000)
    }))
    const report = evaluateInjection('claude-sonnet-4-6', cached)
    expect(report.injectedTokens).toBe(3000)
    expect(report.hints).toContain('cached')
  })

  it('flags an unstable count', () => {
    const noisy: InjectionSample[] = [
      { units: 0, usage: usage(8) },
      { units: 200, usage: usage(260) },
      { units: 800, usage: usage(808) }
    ]
    expect(evaluateInjection('claude-sonnet-4-6', noisy).hints).toContain('unstable')
  })

  it('falls back to the default baseline for uncalibrated models', () => {
    const report = evaluateInjection('claude-3-5-sonnet-20241022', samples(10, 1, 500))
    expect(report.calibrated).toBe(false)
    expect(report.hints).toContain('uncalibrated')
    expect(report.inflation).toBeNull()
    expect(report.injectedTokens).toBe(500)
    expect(report.rows.every((r) => r.expected === null)).toBe(true)
  })

  it('flags counts below the genuine baseline', () => {
    const report = evaluateInjection('claude-opus-4-7', samples(-30, 2))
    expect(report.injectedTokens).toBe(0)
    expect(report.hints).toContain('belowBaseline')
  })
})

describe('analyzeInjection', () => {
  // Block body: a function returned from beforeEach is run as teardown.
  beforeEach(() => {
    messagesMock.mockReset()
  })

  it('sends the padded probes in order through the backend relay', async () => {
    messagesMock.mockImplementation(async ({ body }: { body: { messages: { content: string }[] } }) => {
      const units = (body.messages[0].content.length - 2) / PADDING_UNIT.length
      return { status: 200, body: { usage: { input_tokens: 8 + units } } }
    })
    const progress: number[] = []
    const conn = { baseUrl: 'https://gw.example.com/', apiKey: ' sk-1 ', model: 'claude-sonnet-4-6' }

    const report = await analyzeInjection(conn, (done) => progress.push(done))

    expect(progress).toEqual([1, 2, 3])
    expect(messagesMock).toHaveBeenCalledTimes(PADDING_STEPS.length)
    const first = messagesMock.mock.calls[0][0]
    expect(first.base_url).toBe('https://gw.example.com')
    expect(first.api_key).toBe('sk-1')
    expect(first.body).toEqual(buildInjectionProbe('claude-sonnet-4-6', 0))
    expect(report.verdict).toBe('none')
    expect(report.rows.map((r) => r.usage.total)).toEqual([8, 208, 808])
  })

  it('propagates upstream errors', async () => {
    messagesMock.mockResolvedValue({ status: 400, body: { error: { message: 'bad model' } } })
    await expect(
      analyzeInjection({ baseUrl: 'https://gw.example.com', apiKey: 'k', model: 'claude-x' })
    ).rejects.toThrow('bad model')
  })
})

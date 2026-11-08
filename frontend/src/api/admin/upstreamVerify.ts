// Shared shapes for the admin "is this relay really <provider>" probes (Azure, Bedrock).

export interface UpstreamVerifyConnection {
  base_url: string
  api_key: string
}

/** `status` is the upstream HTTP status of GET /v1/models (0 when the request never got a reply). */
export interface UpstreamVerifyModelsResponse {
  status: number
  models: string[]
  error?: string
}

export interface UpstreamVerifyEvidence {
  /** Provider-specific "for" / "against" kinds plus the shared `info`. */
  kind: string
  detail: string
  value?: string
}

export interface UpstreamVerifyStep {
  name: string
  status: number
  ok: boolean
  duration_ms: number
  headers?: Record<string, string[]>
  error?: string
}

export interface UpstreamVerifyProbeBase {
  model: string
  status: 'ok' | 'failed'
  suspicious: boolean
  model_unavailable: boolean
  served_model?: string
  evidences: UpstreamVerifyEvidence[] | null
  steps: UpstreamVerifyStep[] | null
  error?: string
}

export const UPSTREAM_VERIFY_MODELS_TIMEOUT_MS = 2 * 60 * 1000
// One probe sends up to four sequential upstream requests (Azure runs its cache check
// alongside them), each allowed up to two minutes.
export const UPSTREAM_VERIFY_PROBE_TIMEOUT_MS = 6 * 60 * 1000

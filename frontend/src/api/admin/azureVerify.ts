import { apiClient } from '@/api/client'

export interface AzureVerifyConnection {
  base_url: string
  api_key: string
}

/** `status` is the upstream HTTP status of GET /v1/models (0 when the request never got a reply). */
export interface AzureVerifyModelsResponse {
  status: number
  models: string[]
  error?: string
}

export type AzureVerifyEvidenceKind = 'azure' | 'non_azure' | 'info'

export interface AzureVerifyEvidence {
  kind: AzureVerifyEvidenceKind
  detail: string
  value?: string
}

export interface AzureVerifyStep {
  name: 'chat_completions' | 'chat_completions_stream' | 'responses_continuation'
  status: number
  ok: boolean
  duration_ms: number
  headers?: Record<string, string[]>
  error?: string
}

export interface AzureVerifyProbeResponse {
  model: string
  status: 'ok' | 'failed'
  is_azure: boolean
  suspicious: boolean
  model_unavailable: boolean
  served_model?: string
  evidences: AzureVerifyEvidence[] | null
  steps: AzureVerifyStep[] | null
  error?: string
}

const MODELS_TIMEOUT_MS = 2 * 60 * 1000
// One probe sends four upstream requests, each allowed up to two minutes.
const PROBE_TIMEOUT_MS = 6 * 60 * 1000

export const adminAzureVerifyAPI = {
  async listModels(conn: AzureVerifyConnection): Promise<AzureVerifyModelsResponse> {
    const { data } = await apiClient.post<AzureVerifyModelsResponse>('/admin/azure-verify/models', conn, {
      timeout: MODELS_TIMEOUT_MS
    })
    return data
  },

  async probe(conn: AzureVerifyConnection, model: string): Promise<AzureVerifyProbeResponse> {
    const { data } = await apiClient.post<AzureVerifyProbeResponse>(
      '/admin/azure-verify/probe',
      { ...conn, model },
      { timeout: PROBE_TIMEOUT_MS }
    )
    return data
  }
}

export default adminAzureVerifyAPI

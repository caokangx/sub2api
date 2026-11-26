import { apiClient } from '@/api/client'
import {
  UPSTREAM_VERIFY_MODELS_TIMEOUT_MS,
  UPSTREAM_VERIFY_PROBE_TIMEOUT_MS,
  type UpstreamVerifyConnection,
  type UpstreamVerifyModelsResponse,
  type UpstreamVerifyProbeBase
} from '@/api/admin/upstreamVerify'

export type VertexVerifySource = 'vertex' | 'antigravity' | 'gemini_app' | 'relay' | 'unknown'

export type VertexVerifyNote =
  | 'vertex_unconfirmed'
  | 'prompt_injected'
  | 'date_injected'
  | 'thinking_hidden'
  | 'thinking_merged'
  | 'thinking_estimated'
  | 'mixed_channel'
  | 'nested_relay'
  | 'incomplete'

export interface VertexVerifyThinking {
  duration_ms: number
  thoughts_tokens: number
  thoughts_reported: boolean
  candidates_tokens: number
  thought_parts: number
  answer_correct: boolean
}

export interface VertexVerifyProbeResponse extends UpstreamVerifyProbeBase {
  is_vertex: boolean
  detected_source: VertexVerifySource
  notes: VertexVerifyNote[]
  thinking?: VertexVerifyThinking
}

export const adminVertexVerifyAPI = {
  async listModels(conn: UpstreamVerifyConnection): Promise<UpstreamVerifyModelsResponse> {
    const { data } = await apiClient.post<UpstreamVerifyModelsResponse>('/admin/vertex-verify/models', conn, {
      timeout: UPSTREAM_VERIFY_MODELS_TIMEOUT_MS
    })
    return data
  },

  async probe(conn: UpstreamVerifyConnection, model: string, signal?: AbortSignal): Promise<VertexVerifyProbeResponse> {
    const { data } = await apiClient.post<VertexVerifyProbeResponse>(
      '/admin/vertex-verify/probe',
      { ...conn, model },
      { timeout: UPSTREAM_VERIFY_PROBE_TIMEOUT_MS, signal }
    )
    return data
  }
}

export default adminVertexVerifyAPI

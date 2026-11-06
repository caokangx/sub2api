import { apiClient } from '@/api/client'
import {
  UPSTREAM_VERIFY_MODELS_TIMEOUT_MS,
  UPSTREAM_VERIFY_PROBE_TIMEOUT_MS,
  type UpstreamVerifyConnection,
  type UpstreamVerifyModelsResponse,
  type UpstreamVerifyProbeBase
} from '@/api/admin/upstreamVerify'

export type BedrockVerifySource = 'bedrock' | 'vertex' | 'anthropic' | 'subscription' | 'unknown'

export type BedrockVerifyNote = 'forced_tool_choice_unsupported'

export interface BedrockVerifyProbeResponse extends UpstreamVerifyProbeBase {
  is_bedrock: boolean
  detected_source: BedrockVerifySource
  notes: BedrockVerifyNote[]
}

export const adminBedrockVerifyAPI = {
  async listModels(conn: UpstreamVerifyConnection): Promise<UpstreamVerifyModelsResponse> {
    const { data } = await apiClient.post<UpstreamVerifyModelsResponse>('/admin/bedrock-verify/models', conn, {
      timeout: UPSTREAM_VERIFY_MODELS_TIMEOUT_MS
    })
    return data
  },

  async probe(conn: UpstreamVerifyConnection, model: string, signal?: AbortSignal): Promise<BedrockVerifyProbeResponse> {
    const { data } = await apiClient.post<BedrockVerifyProbeResponse>(
      '/admin/bedrock-verify/probe',
      { ...conn, model },
      { timeout: UPSTREAM_VERIFY_PROBE_TIMEOUT_MS, signal }
    )
    return data
  }
}

export default adminBedrockVerifyAPI

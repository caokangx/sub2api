import { apiClient } from '@/api/client'
import {
  UPSTREAM_VERIFY_MODELS_TIMEOUT_MS,
  UPSTREAM_VERIFY_PROBE_TIMEOUT_MS,
  type UpstreamVerifyConnection,
  type UpstreamVerifyEvidence,
  type UpstreamVerifyModelsResponse,
  type UpstreamVerifyProbeBase,
  type UpstreamVerifyStep
} from '@/api/admin/upstreamVerify'

export type AzureVerifyConnection = UpstreamVerifyConnection
export type AzureVerifyModelsResponse = UpstreamVerifyModelsResponse
export type AzureVerifyEvidenceKind = 'azure' | 'non_azure' | 'info'
export type AzureVerifyEvidence = UpstreamVerifyEvidence
export type AzureVerifyStep = UpstreamVerifyStep

export interface AzureVerifyProbeResponse extends UpstreamVerifyProbeBase {
  is_azure: boolean
}

export const adminAzureVerifyAPI = {
  async listModels(conn: AzureVerifyConnection): Promise<AzureVerifyModelsResponse> {
    const { data } = await apiClient.post<AzureVerifyModelsResponse>('/admin/azure-verify/models', conn, {
      timeout: UPSTREAM_VERIFY_MODELS_TIMEOUT_MS
    })
    return data
  },

  async probe(conn: AzureVerifyConnection, model: string): Promise<AzureVerifyProbeResponse> {
    const { data } = await apiClient.post<AzureVerifyProbeResponse>(
      '/admin/azure-verify/probe',
      { ...conn, model },
      { timeout: UPSTREAM_VERIFY_PROBE_TIMEOUT_MS }
    )
    return data
  }
}

export default adminAzureVerifyAPI

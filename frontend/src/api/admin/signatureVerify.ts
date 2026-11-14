import { apiClient } from '@/api/client'

export interface SignatureVerifyMessagesRequest {
  base_url: string
  api_key: string
  body: Record<string, unknown>
  /** Value for the upstream anthropic-beta header; omitted when empty. */
  anthropic_beta?: string
}

/** Upstream reply relayed by the backend; `status` is the upstream HTTP status. */
export interface SignatureVerifyMessagesResponse {
  status: number
  body?: unknown
  /** Non-JSON reply; complete for text/event-stream, otherwise truncated. */
  text?: string
  content_type?: string
  elapsed_ms?: number
}

// A high-effort harvest can think for several minutes before the upstream replies.
const SIGNATURE_VERIFY_TIMEOUT_MS = 15 * 60 * 1000

export const adminSignatureVerifyAPI = {
  async messages(
    payload: SignatureVerifyMessagesRequest,
    signal?: AbortSignal
  ): Promise<SignatureVerifyMessagesResponse> {
    const { data } = await apiClient.post<SignatureVerifyMessagesResponse>(
      '/admin/signature-verify/messages',
      payload,
      { timeout: SIGNATURE_VERIFY_TIMEOUT_MS, signal }
    )
    return data
  }
}

export default adminSignatureVerifyAPI

import { apiClient } from '@/api/client'

export interface SignatureVerifyMessagesRequest {
  base_url: string
  api_key: string
  body: Record<string, unknown>
}

/** Upstream reply relayed by the backend; `status` is the upstream HTTP status. */
export interface SignatureVerifyMessagesResponse {
  status: number
  body?: unknown
  text?: string
}

// A high-effort harvest can think for several minutes before the upstream replies.
const SIGNATURE_VERIFY_TIMEOUT_MS = 15 * 60 * 1000

export const adminSignatureVerifyAPI = {
  async messages(payload: SignatureVerifyMessagesRequest): Promise<SignatureVerifyMessagesResponse> {
    const { data } = await apiClient.post<SignatureVerifyMessagesResponse>(
      '/admin/signature-verify/messages',
      payload,
      { timeout: SIGNATURE_VERIFY_TIMEOUT_MS }
    )
    return data
  }
}

export default adminSignatureVerifyAPI

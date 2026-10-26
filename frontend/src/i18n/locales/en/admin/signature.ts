export default {
  signatureDecoder: {
    title: 'Signature Decoder',
    description:
      'Decode the signature of a Claude thinking block locally in your browser; nothing is uploaded. The plaintext part reveals the model, block type, ID and creation time. The thinking content is encrypted and cannot be recovered, and validity can only be checked by the upstream.',
    inputLabel: 'Signature',
    inputPlaceholder: 'Paste a signature string, or a whole thinking content block JSON',
    clear: 'Clear',
    errors: {
      empty: 'Please enter a signature',
      invalidBase64: 'Not a valid base64 string',
      notProtobuf: 'Decoded bytes are not protobuf; this may not be a Claude signature',
      truncated: 'Data is truncated; make sure you copied the whole value',
      unsupportedWireType: 'Contains an unsupported protobuf wire type'
    },
    summary: {
      title: 'Overview',
      model: 'Model',
      requestedModel: 'Requested model',
      blockType: 'Block type',
      id: 'ID',
      createdAt: 'Created at',
      byteLength: 'Decoded size',
      bytes: '{n} bytes'
    },
    time: {
      unix: 'Unix seconds',
      utc: 'UTC time'
    },
    fields: {
      title: 'Field structure',
      hint: 'Meanings are inferred from samples; Anthropic does not publish this format.',
      path: 'Path',
      kind: 'Type',
      length: 'Length',
      value: 'Value',
      meaning: 'Likely meaning'
    },
    meanings: {
      version: 'Format or key version',
      type: 'Type or algorithm id',
      mac: 'Signature / MAC (tamper protection)',
      model: 'Model that produced the thinking',
      blockType: 'Content block type',
      id: 'Request / session / org related ID',
      digest: 'Content hash or nonce',
      requestedModel: 'Model requested by the client',
      createdAt: 'Creation time (Unix seconds)',
      nonce: 'AES-GCM nonce',
      wrappedKey: 'Encrypted data key',
      ciphertext: 'Encrypted thinking content',
      outerVersion: 'Outer version'
    },
    sections: {
      decode: 'Signature decoding',
      verify: 'Genuineness check'
    },
    verify: {
      description:
        'Each check generates a random secret. The model first memorizes it only inside its hidden reasoning, and the upstream returns just the encrypted signature. The signature is then replayed and the model is asked to recite the secret. The replay request never contains the secret, which exists only inside the encrypted signature, so recovering it shows the upstream really is a Claude that can decrypt thinking signatures.',
      copy: 'Copy',
      copied: 'Copied',
      connection: {
        title: 'Connection',
        hint: 'This app\'s backend relays the requests to the gateway you enter, so browser CORS restrictions do not apply. The API key is used only for the relay; it is not stored or written to the audit log.',
        baseUrl: 'Base URL',
        apiKey: 'API Key',
        apiKeyPlaceholder: 'Kept in browser memory only; lost on refresh',
        model: 'Model'
      },
      secretLabel: 'Random secret for this check',
      secretPending: 'Generated when the check starts',
      start: 'Start check',
      retry: 'Retry recovery with the same signature',
      steps: {
        harvest: 'Step 1: harvesting the signature while the model memorizes the secret in hidden reasoning…',
        replay: 'Step 2: replaying the signature to recover the secret…'
      },
      verdict: {
        genuine: 'Recovered this check\'s random secret {secret}. This test indicates genuine Claude.',
        notRecovered: 'The random secret was not recovered this time. Please retry; this does not mean it is fake, so run it a few more times to be sure.'
      },
      noSignature: 'No signature in the upstream response (the model or gateway returned no thinking block), so it cannot be checked.',
      details: {
        title: 'Request details',
        signature: 'Harvested signature',
        signatureMeta: 'Bound model {model} · {chars} chars · {tokens} hidden thinking tokens',
        openInDecoder: 'Decode above',
        recovered: 'Recovered by replay',
        rawResponse: 'Raw replay response',
        empty: '(no content returned)'
      },
      reqErrors: {
        missingBaseUrl: 'Please enter a Base URL',
        missingApiKey: 'Please enter an API Key',
        missingSignature: 'Please provide a signature first',
        proxy: 'Backend relay failed: {detail}',
        upstreamError: 'Upstream returned an error',
        nonJson: 'Upstream returned non-JSON content: {detail}',
        http: 'Upstream returned HTTP {status}'
      }
    }
  }
}

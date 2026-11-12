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
      verify: 'Genuineness check',
      injection: 'Injection analysis'
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
      curl: {
        harvest: 'Extract first request curl',
        replay: 'Extract second request curl',
        harvestTitle: 'First request (harvest signature) curl',
        replayTitle: 'Second request (replay signature) curl',
        needSignature: 'Harvest a signature first, or paste one above'
      },
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
        edited: 'Edited',
        restore: 'Restore original',
        editHint: 'You can edit the signature here. "Retry recovery with the same signature" will replay the edited signature.',
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
    },
    injection: {
      description:
        'Uses the connection above to send 3 probes of known length ("hi" followed by 0, 200 and 800 copies of " hello"). The input tokens the upstream reports (input + cache_creation + cache_read) are compared with the genuine count to estimate how many tokens the upstream injects into every request, and whether it inflates token counts proportionally. The analysis costs about 2,000 input tokens, plus whatever the upstream injects.',
      scopeNote:
        'Only these built-in probes are analyzed. The upstream may inject different content into other kinds of requests, for example ones with tools or Claude Code headers.',
      start: 'Analyze injection',
      progress: 'Sending probes: {done}/{total} done',
      verdict: {
        none: 'No injection found',
        minor: 'Minor injection',
        major: 'Major injection'
      },
      inflatedTag: 'Token counts look inflated',
      injectedLabel: 'Estimated injection',
      injectedValue: '~{n} tokens per request',
      injectedNote: 'Accurate to about ±10 tokens. More than 500 tokens usually means a whole system prompt is injected, e.g. to impersonate Claude Code.',
      inflationLabel: 'Inflation factor',
      inflationNote: 'Reported / genuine token ratio; 1.00x is normal.',
      table: {
        units: 'Padding units',
        expected: 'Genuine tokens (no injection)',
        reported: 'Reported total'
      },
      hints: {
        uncalibrated: 'This model is not in the calibration table, so a default baseline was used: the injection estimate may be less accurate and the inflation factor cannot be computed.',
        unstable: 'The three counts do not fall on a straight line. The upstream count is unstable, so treat the result as a rough guide.',
        cached: 'The probes do not enable caching, yet the upstream reported cached tokens: the upstream adds caching itself, so the injected content is likely cached.',
        belowBaseline: 'The upstream reports fewer tokens than the genuine count; it may truncate the content or rewrite the count.'
      }
    }
  }
}

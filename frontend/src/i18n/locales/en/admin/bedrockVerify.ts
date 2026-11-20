export default {
  bedrockVerify: {
    title: 'Bedrock Verify',
    description:
      'Enter a relay Base URL and API key. The server sends probe requests through /v1/messages: a plain message, tool use, streaming, computer use without the beta header, three requests real Bedrock must reject (max_tokens above the limit, an unknown effort value, enabled thinking with budget_tokens above max_tokens), and a fixed text for tokenizer comparison. A model counts as Bedrock only when the relay behaves like real Bedrock, e.g. its validation errors match what Bedrock returns; ID prefixes and AWS error wrappers can be forged and are only supporting evidence. Each model costs a few hundred tokens.',
    connection: {
      baseUrl: 'Base URL',
      apiKey: 'API Key',
      fetchModels: 'Fetch models',
      modelsCount: '{n} models',
      modelsFailed: 'Failed to fetch models (HTTP {status}): {error}'
    },
    reference: {
      title: 'Newer model check',
      hint: 'Checks whether the relay offers the newer Claude models in the reference list. "Listed but unavailable" means the model is listed, but probing it returned model-not-found or no available channel.',
      states: {
        missing: 'Not offered',
        unavailable: 'Listed but unavailable',
        listed: 'Offered'
      }
    },
    models: {
      title: 'Models to probe',
      selectDefault: 'Claude only',
      selectAll: 'Select all',
      clear: 'Clear',
      start: 'Start ({n})',
      stop: 'Stop',
      hint: 'All selected models are probed at once; this usually finishes within 10 to 30 seconds.'
    },
    results: {
      title: 'Results',
      summary: '{finished}/{total} done: {match} Bedrock, {notMatch} not Bedrock, {failed} failed',
      servedModel: 'Deployed as {model}',
      suspicious: 'Evidence both for and against Bedrock was seen. This may be a mixed channel, or the relay imitates Bedrock formats.',
      details: 'Show evidence',
      evidences: 'Evidence',
      steps: 'Probe requests',
      source: 'Detected source: {source}',
      sources: {
        bedrock: 'AWS Bedrock',
        vertex: 'Google Vertex',
        anthropic: 'Anthropic API',
        subscription: 'Claude subscription account',
        unknown: 'Unknown'
      },
      notes: {
        forced_tool_choice_unsupported: 'Forced tool use (tool_choice tool / any) is not supported',
        bedrock_unconfirmed:
          'Only forgeable Bedrock formats (ID prefixes, error wrappers) were seen; the validation probes did not confirm real Bedrock',
        tokenizer_mismatch: 'Tokenizer count differs from the reference; the model may have been swapped'
      },
      states: {
        running: 'Probing',
        stopped: 'Stopped',
        match: 'Bedrock',
        notMatch: 'Not Bedrock',
        failed: 'Failed',
        unavailable: 'Model unavailable'
      },
      evidenceKinds: {
        bedrock: 'For Bedrock',
        bedrock_weak: 'Bedrock format (forgeable)',
        non_bedrock: 'Against Bedrock',
        info: 'Info'
      },
      stepNames: {
        messages: 'Messages',
        tool_use: 'Tool use',
        messages_stream: 'Messages (stream)',
        computer_use: 'Computer use without beta',
        max_tokens_overflow: 'max_tokens above limit',
        effort_unknown_variant: 'Unknown effort value',
        thinking_type_enabled: 'thinking.type=enabled',
        tokenizer: 'Tokenizer count'
      }
    }
  }
}

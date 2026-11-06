export default {
  bedrockVerify: {
    title: 'Bedrock Verify',
    description:
      'Enter a relay Base URL and API key. The server sends a few probe requests through /v1/messages (plain message, tool use, streaming) and uses message and tool-use ID prefixes, AWS-specific response headers and errors to judge whether each Claude model is really served by AWS Bedrock. Each model costs a few hundred tokens.',
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
      hint: 'All selected models are probed at once; this usually finishes within 5 to 20 seconds.'
    },
    results: {
      title: 'Results',
      summary: '{finished}/{total} done: {match} Bedrock, {notMatch} not Bedrock, {failed} failed',
      servedModel: 'Deployed as {model}',
      suspicious: 'Both Bedrock and non-Bedrock markers were seen. This may be a mixed channel, or the relay rewrites IDs.',
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
        forced_tool_choice_unsupported: 'Forced tool use (tool_choice tool / any) is not supported'
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
        non_bedrock: 'Against Bedrock',
        info: 'Info'
      },
      stepNames: {
        messages: 'Messages',
        tool_use: 'Tool use',
        messages_stream: 'Messages (stream)'
      }
    }
  }
}

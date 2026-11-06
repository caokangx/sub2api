export default {
  azureVerify: {
    title: 'Azure Verify',
    description:
      'Enter a relay Base URL and API key. The server sends a few probe requests (non-streaming, streaming, Responses continuation) and uses Azure-specific response headers, content-filter fields and parameter behavior to judge whether each model is really served by Azure OpenAI. Each model costs a few hundred tokens.',
    connection: {
      baseUrl: 'Base URL',
      apiKey: 'API Key',
      fetchModels: 'Fetch models',
      modelsCount: '{n} models',
      modelsFailed: 'Failed to fetch models (HTTP {status}): {error}'
    },
    reference: {
      title: 'Newer model check',
      hint: 'Checks whether the relay offers the newer models in the reference list. "Listed but unavailable" means the model is listed, but probing it returned model-not-found or no available channel.',
      states: {
        missing: 'Not offered',
        unavailable: 'Listed but unavailable',
        listed: 'Offered'
      }
    },
    models: {
      title: 'Models to probe',
      selectDefault: 'gpt-6 only',
      selectAll: 'Select all',
      clear: 'Clear',
      start: 'Start ({n})',
      stop: 'Stop',
      hint: 'All selected models are probed at once; this usually finishes within 10 to 60 seconds.'
    },
    results: {
      title: 'Results',
      summary: '{finished}/{total} done: {match} Azure, {notMatch} not Azure, {failed} failed',
      servedModel: 'Deployed as {model}',
      suspicious: 'Both Azure markers and non-Azure behavior were seen. This may be a mixed channel, or forged Azure headers.',
      details: 'Show evidence',
      evidences: 'Evidence',
      steps: 'Probe requests',
      states: {
        running: 'Probing',
        stopped: 'Stopped',
        match: 'Azure',
        notMatch: 'Not Azure',
        failed: 'Failed',
        unavailable: 'Model unavailable'
      },
      evidenceKinds: {
        azure: 'For Azure',
        non_azure: 'Against Azure',
        info: 'Info'
      },
      stepNames: {
        chat_completions: 'Chat Completions',
        chat_completions_stream: 'Chat Completions (stream)',
        responses_continuation: 'Responses continuation'
      }
    }
  }
}

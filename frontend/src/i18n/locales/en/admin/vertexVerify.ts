export default {
  vertexVerify: {
    title: 'Vertex Verify',
    description:
      'Enter a relay Base URL and API key. The server sends probe requests through the Gemini native API (/v1beta/models/MODEL:generateContent): a plain request, a question about today\'s date, a question about hidden instructions, a read of a missing gs:// file, thinking settings real Vertex must reject (thinkingBudget on Gemini 4, MINIMAL / XHIGH levels the model does not support, thinkingLevel together with thinkingBudget), and a reasoning question that shows whether thinking tokens are reported. A model counts as Vertex only when the validation errors match real Vertex and no evidence points elsewhere; responseId and createTime formats can be forged and are only supporting evidence. Each model sends about 9 requests and costs 6,000 to 8,000 tokens, mostly on the reasoning question.',
    connection: {
      baseUrl: 'Base URL',
      apiKey: 'API Key',
      fetchModels: 'Fetch models',
      modelsCount: '{n} models',
      modelsFailed: 'Failed to fetch models (HTTP {status}): {error}'
    },
    reference: {
      title: 'Newer model check',
      hint: 'Checks whether the relay offers the newer Gemini models in the reference list. "Listed but unavailable" means the model is listed, but probing it returned model-not-found or no available channel.',
      states: {
        missing: 'Not offered',
        unavailable: 'Listed but unavailable',
        listed: 'Offered'
      }
    },
    models: {
      title: 'Models to probe',
      selectDefault: 'Gemini 4 and 3.1 Pro only',
      selectAll: 'Select all',
      clear: 'Clear',
      manualPlaceholder: 'Type a model name, e.g. gemini-4-hydrogen',
      manualAdd: 'Add model',
      empty: 'The model list is empty. Add the models to probe below.',
      start: 'Start ({n})',
      stop: 'Stop',
      hint: 'All selected models are probed at once. The reasoning question is slow; this usually finishes within 1 to 2 minutes.'
    },
    results: {
      title: 'Results',
      summary: '{finished}/{total} done: {match} Vertex, {notMatch} not Vertex, {failed} failed',
      servedModel: 'Deployment: {model}',
      suspicious: 'Validation errors match real Vertex, but other evidence points elsewhere: possibly a mixed channel, or a middle layer rewrites some requests.',
      details: 'Show evidence',
      evidences: 'Evidence',
      steps: 'Probe requests',
      source: 'Detected source: {source}',
      thinking: 'Reasoning question: {thoughts} thinking tokens / {output} output tokens / {parts} thought parts / {seconds}s / {answer}',
      answerCorrect: 'correct answer',
      answerWrong: 'wrong answer',
      sources: {
        vertex: 'Google Vertex',
        antigravity: 'Antigravity reverse proxy',
        gemini_app: 'Gemini web app reverse proxy',
        relay: 'Rewritten by a middle layer',
        unknown: 'Unknown'
      },
      notes: {
        vertex_unconfirmed: 'Only forgeable Google formats were seen; validation probes did not confirm real Vertex',
        prompt_injected: 'Injected prompt suspected',
        date_injected: "Today's date is in the context",
        thinking_hidden: 'Thinking is not counted in usage',
        thinking_merged: 'Thinking is folded into output tokens',
        thinking_estimated: 'Thinking tokens look estimated',
        mixed_channel: 'Mixed channel suspected',
        nested_relay: 'Another relay sits behind this one',
        incomplete: 'Some probes got no answer; the verdict uses the rest'
      },
      states: {
        running: 'Probing',
        stopped: 'Stopped',
        match: 'Vertex',
        notMatch: 'Not Vertex',
        failed: 'Failed',
        unavailable: 'Model unavailable'
      },
      evidenceKinds: {
        vertex: 'For Vertex',
        vertex_weak: 'Google format (forgeable)',
        non_vertex: 'Against Vertex',
        info: 'Info'
      },
      stepNames: {
        basic: 'Plain request',
        date: "Today's date",
        persona: 'Hidden instructions',
        gcs_missing: 'Missing gs:// file',
        thinking_budget: 'thinkingBudget (unsupported on Gemini 4)',
        thinking_minimal: 'thinkingLevel=MINIMAL',
        thinking_both: 'thinkingLevel with thinkingBudget',
        thinking_xhigh: 'thinkingLevel=XHIGH',
        thinking: 'Reasoning question (thinking tokens)'
      }
    }
  }
}

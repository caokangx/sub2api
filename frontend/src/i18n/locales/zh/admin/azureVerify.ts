export default {
  azureVerify: {
    title: 'Azure 真假测试',
    description:
      '填写中转站的 Base URL 和 API Key，由服务器向对方发送几条探测请求（非流式、流式、Responses 续接、提示词缓存），根据 Azure 特有的内容过滤字段、响应头和参数行为，判断每个模型背后是不是 Azure OpenAI，并检查中转站有没有篡改参数、响应或用量。只有响应头这类容易伪造的证据时不会判为 Azure。每个模型约消耗 4000 个输入 token。',
    connection: {
      baseUrl: 'Base URL',
      apiKey: 'API Key',
      fetchModels: '获取模型列表',
      modelsCount: '共 {n} 个模型',
      modelsFailed: '获取模型列表失败（HTTP {status}）：{error}'
    },
    reference: {
      title: '较新模型核对',
      hint: '对照参考清单检查该站是否提供较新的模型。“列出但不可用”表示模型列表里有，但测试时返回模型不存在或无可用渠道。',
      states: {
        missing: '该站未提供',
        unavailable: '列出但不可用',
        listed: '已提供'
      }
    },
    models: {
      title: '选择要测试的模型',
      selectDefault: '只选 gpt-6 系列',
      selectAll: '全选',
      clear: '清空',
      start: '开始测试（{n} 个）',
      stop: '停止测试',
      hint: '所有选中的模型同时测试，通常 10 到 60 秒内全部完成。'
    },
    results: {
      title: '测试结果',
      summary: '已完成 {finished}/{total}：{match} 个是 Azure，{notMatch} 个不是 Azure，{failed} 个测试失败',
      servedModel: '实际部署：{model}',
      suspicious: '同时出现了 Azure 特征和非 Azure 行为，可能是混合渠道，或者伪造了 Azure 响应头。',
      relayTampered:
        '中转站篡改了请求参数、响应内容或用量（例如去掉了长度限制、注入了提示词、虚报了输入 token），无论背后是不是 Azure，计费都可能不准。',
      details: '查看依据',
      evidences: '判断依据',
      steps: '探测请求',
      states: {
        running: '测试中',
        stopped: '已停止',
        match: '是 Azure',
        notMatch: '不是 Azure',
        failed: '测试失败',
        unavailable: '模型不可用'
      },
      evidenceKinds: {
        azure: '支持 Azure',
        non_azure: '反对 Azure',
        relay_tamper: '中转站篡改',
        info: '参考'
      },
      stepNames: {
        chat_completions: 'Chat Completions 非流式',
        chat_completions_stream: 'Chat Completions 流式',
        responses_continuation: 'Responses 续接对话',
        prompt_cache: '提示词缓存与用量核对'
      }
    }
  }
}

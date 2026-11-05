export default {
  bedrockVerify: {
    title: 'Bedrock 真假测试',
    description:
      '填写中转站的 Base URL 和 API Key，由服务器通过 /v1/messages 向对方发送几条探测请求（普通消息、工具调用、流式），根据消息 ID 和工具调用 ID 的前缀、AWS 特有的响应头和报错，判断每个 Claude 模型背后是不是 AWS Bedrock。每个模型约消耗几百个 token。',
    connection: {
      baseUrl: 'Base URL',
      apiKey: 'API Key',
      fetchModels: '获取模型列表',
      modelsCount: '共 {n} 个模型',
      modelsFailed: '获取模型列表失败（HTTP {status}）：{error}'
    },
    reference: {
      title: '较新模型核对',
      hint: '对照参考清单检查该站是否提供较新的 Claude 模型。“列出但不可用”表示模型列表里有，但测试时返回模型不存在或无可用渠道。',
      states: {
        missing: '该站未提供',
        unavailable: '列出但不可用',
        listed: '已提供'
      }
    },
    models: {
      title: '选择要测试的模型',
      selectDefault: '只选 Claude 模型',
      selectAll: '全选',
      clear: '清空',
      start: '开始测试（{n} 个）',
      stop: '测完当前模型后停止',
      hint: '按顺序逐个测试，每个模型通常需要 5 到 20 秒。'
    },
    results: {
      title: '测试结果',
      summary: '已完成 {finished}/{total}：{match} 个是 Bedrock，{notMatch} 个不是 Bedrock，{failed} 个测试失败',
      servedModel: '实际部署：{model}',
      suspicious: '同时出现了 Bedrock 特征和非 Bedrock 特征，可能是混合渠道，或者中转站改写了 ID。',
      details: '查看依据',
      evidences: '判断依据',
      steps: '探测请求',
      source: '识别来源：{source}',
      sources: {
        bedrock: 'AWS Bedrock',
        vertex: 'Google Vertex',
        anthropic: 'Anthropic 官方 API',
        subscription: 'Claude 订阅账号',
        unknown: '无法识别'
      },
      notes: {
        forced_tool_choice_unsupported: '不支持强制工具调用（tool_choice 为 tool / any）'
      },
      states: {
        pending: '等待中',
        running: '测试中',
        match: '是 Bedrock',
        notMatch: '不是 Bedrock',
        failed: '测试失败',
        unavailable: '模型不可用'
      },
      evidenceKinds: {
        bedrock: '支持 Bedrock',
        non_bedrock: '反对 Bedrock',
        info: '参考'
      },
      stepNames: {
        messages: 'Messages 非流式',
        tool_use: '工具调用',
        messages_stream: 'Messages 流式'
      }
    }
  }
}

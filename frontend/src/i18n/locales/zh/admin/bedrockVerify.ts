export default {
  bedrockVerify: {
    title: 'Bedrock 真假测试',
    description:
      '填写中转站的 Base URL 和 API Key，由服务器通过 /v1/messages 向对方发送探测请求：普通消息、工具调用、流式、不带 beta 头的 computer use，三个真 Bedrock 必定拒绝的请求（max_tokens 超上限、不存在的 effort 取值、budget_tokens 大于 max_tokens 的 enabled thinking），以及一段用来比对分词器的固定文本。只有对方的行为和真 Bedrock 一致（比如参数校验的报错内容对得上），才判定为 Bedrock；消息 ID 前缀、报错的 AWS 外层格式可以伪造，只作为参考。每个模型约消耗几百个 token。',
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
      manualPlaceholder: '手动输入模型名，例如 gemini-4-hydrogen',
      manualAdd: '添加模型',
      empty: '模型列表为空，可以在下面手动添加要测试的模型。',
      start: '开始测试（{n} 个）',
      stop: '停止测试',
      hint: '所有选中的模型同时测试，通常 10 到 30 秒内全部完成。'
    },
    results: {
      title: '测试结果',
      summary: '已完成 {finished}/{total}：{match} 个是 Bedrock，{notMatch} 个不是 Bedrock，{failed} 个测试失败',
      servedModel: '实际部署：{model}',
      suspicious: '同时出现了支持和反对 Bedrock 的证据，可能是混合渠道，或者中转站在模仿 Bedrock 的格式。',
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
        forced_tool_choice_unsupported: '不支持强制工具调用（tool_choice 为 tool / any）',
        bedrock_unconfirmed: '只看到了可伪造的 Bedrock 格式（ID 前缀、报错外层），参数校验探测没能确认是真 Bedrock',
        tokenizer_mismatch: '分词探测的 token 数与参考值不符，可能换了模型'
      },
      states: {
        running: '测试中',
        stopped: '已停止',
        match: '是 Bedrock',
        notMatch: '不是 Bedrock',
        failed: '测试失败',
        unavailable: '模型不可用'
      },
      evidenceKinds: {
        bedrock: '支持 Bedrock',
        bedrock_weak: 'Bedrock 格式（可伪造）',
        non_bedrock: '反对 Bedrock',
        info: '参考'
      },
      stepNames: {
        messages: 'Messages 非流式',
        tool_use: '工具调用',
        messages_stream: 'Messages 流式',
        computer_use: 'Computer use 不带 beta 头',
        max_tokens_overflow: 'max_tokens 超上限',
        effort_unknown_variant: '不存在的 effort 取值',
        thinking_type_enabled: 'thinking.type=enabled',
        tokenizer: '分词器比对'
      }
    }
  }
}

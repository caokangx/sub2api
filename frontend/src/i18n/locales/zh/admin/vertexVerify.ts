export default {
  vertexVerify: {
    title: 'Vertex 检测',
    description:
      '填写中转站的 Base URL 和 API Key，由服务器通过 Gemini 原生接口（/v1beta/models/模型名:generateContent）向对方发送探测请求：普通请求、问当天日期、问上下文里有没有隐藏指令、读一个不存在的 gs:// 文件，几个真 Vertex 必定拒绝的思考参数（Gemini 4 的 thinkingBudget、模型不支持的 MINIMAL / XHIGH 档、同时传 thinkingLevel 和 thinkingBudget），以及一道需要推理的题（用来看思考 token 是否如实列出）。只有参数校验的报错内容和真 Vertex 一致、且没有任何反证，才判定为 Vertex；responseId、createTime 这类格式可以伪造，只作为参考。每个模型约发 9 个请求、消耗 6000 到 8000 个 token，主要花在推理题上。',
    connection: {
      baseUrl: 'Base URL',
      apiKey: 'API Key',
      fetchModels: '获取模型列表',
      modelsCount: '共 {n} 个模型',
      modelsFailed: '获取模型列表失败（HTTP {status}）：{error}'
    },
    reference: {
      title: '较新模型核对',
      hint: '对照参考清单检查该站是否提供较新的 Gemini 模型。“列出但不可用”表示模型列表里有，但测试时返回模型不存在或无可用渠道。',
      states: {
        missing: '该站未提供',
        unavailable: '列出但不可用',
        listed: '已提供'
      }
    },
    models: {
      title: '选择要测试的模型',
      selectDefault: '只选 4 系和 3.1 Pro',
      selectAll: '全选',
      clear: '清空',
      manualPlaceholder: '手动输入模型名，例如 gemini-4-hydrogen',
      manualAdd: '添加模型',
      empty: '模型列表为空，可以在下面手动添加要测试的模型。',
      start: '开始测试（{n} 个）',
      stop: '停止测试',
      hint: '所有选中的模型同时测试。推理题比较慢，通常 1 到 2 分钟全部完成。'
    },
    results: {
      title: '测试结果',
      summary: '已完成 {finished}/{total}：{match} 个是 Vertex，{notMatch} 个不是 Vertex，{failed} 个测试失败',
      servedModel: '实际部署：{model}',
      suspicious: '参数校验和真 Vertex 一致，但同时出现了反对的证据，可能是混合渠道，或者中间层改写了部分请求。',
      details: '查看依据',
      evidences: '判断依据',
      steps: '探测请求',
      source: '识别来源：{source}',
      thinking: '推理题：思考 {thoughts} token / 输出 {output} token / {parts} 段思考内容 / {seconds} 秒 / {answer}',
      answerCorrect: '答案正确',
      answerWrong: '答案错误',
      sources: {
        vertex: 'Google Vertex',
        antigravity: 'Antigravity 逆向',
        gemini_app: 'Gemini 网页版逆向',
        relay: '中间层改写过',
        unknown: '无法识别'
      },
      notes: {
        vertex_unconfirmed: '只看到了可伪造的 Google 格式，参数校验探测没能确认是真 Vertex',
        prompt_injected: '疑似注入了提示词',
        date_injected: '上下文里有当天日期',
        thinking_hidden: '思考没有计入用量',
        thinking_merged: '思考被并进了输出 token',
        thinking_estimated: '思考 token 疑似是估算的',
        mixed_channel: '疑似混合渠道',
        nested_relay: '后面还有一层中转站',
        incomplete: '部分探测没有拿到回答，结论只基于其余探测'
      },
      states: {
        running: '测试中',
        stopped: '已停止',
        match: '是 Vertex',
        notMatch: '不是 Vertex',
        failed: '测试失败',
        unavailable: '模型不可用'
      },
      evidenceKinds: {
        vertex: '支持 Vertex',
        vertex_weak: 'Google 格式（可伪造）',
        non_vertex: '反对 Vertex',
        info: '参考'
      },
      stepNames: {
        basic: '普通请求',
        date: '问当天日期',
        persona: '问隐藏指令',
        gcs_missing: '读不存在的 gs:// 文件',
        thinking_budget: 'thinkingBudget（Gemini 4 不支持）',
        thinking_minimal: 'thinkingLevel=MINIMAL',
        thinking_both: '同时传 thinkingLevel 和 thinkingBudget',
        thinking_xhigh: 'thinkingLevel=XHIGH',
        thinking: '推理题（思考 token）'
      }
    }
  }
}

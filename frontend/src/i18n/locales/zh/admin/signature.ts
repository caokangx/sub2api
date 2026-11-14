export default {
  signatureDecoder: {
    title: '签名解析',
    description:
      '解析 Claude thinking 块的 signature，仅在浏览器本地解码，不会上传。明文部分可读出模型、块类型、ID 与生成时间；思考内容是加密的，无法还原，签名有效性也只能由上游校验。',
    inputLabel: 'Signature',
    inputPlaceholder: '粘贴 signature 字符串，或整段 thinking 内容块 JSON',
    clear: '清空',
    errors: {
      empty: '请输入 signature',
      invalidBase64: '不是合法的 base64 字符串',
      notProtobuf: '解码后不是 protobuf 结构，可能不是 Claude 的 signature',
      truncated: '数据被截断，请确认复制完整',
      unsupportedWireType: '包含不支持的 protobuf 字段类型'
    },
    summary: {
      title: '概览',
      model: '模型',
      requestedModel: '请求模型',
      blockType: '块类型',
      id: 'ID',
      createdAt: '生成时间',
      byteLength: '解码长度',
      bytes: '{n} 字节'
    },
    time: {
      unix: 'Unix 秒',
      utc: 'UTC 时间'
    },
    fields: {
      title: '字段结构',
      hint: '字段含义根据样本推测，Anthropic 未公开格式定义。',
      path: '路径',
      kind: '类型',
      length: '长度',
      value: '值',
      meaning: '推测含义'
    },
    meanings: {
      version: '格式或密钥版本',
      type: '类型或算法标识',
      mac: '签名 / MAC（防篡改）',
      model: '生成 thinking 的模型',
      blockType: '内容块类型',
      id: '请求 / 会话 / 组织相关 ID',
      digest: '内容哈希或随机数',
      requestedModel: '请求指定的模型',
      createdAt: '生成时间（Unix 秒）',
      nonce: 'AES-GCM nonce',
      wrappedKey: '加密后的数据密钥',
      ciphertext: '加密的思考内容',
      outerVersion: '外层版本'
    },
    sections: {
      decode: '签名解析',
      verify: '签名真假校验',
      injection: '注入量分析',
      functional: '功能测试'
    },
    verify: {
      description:
        '每次校验都会随机生成一个秘密：先让模型只在隐藏推理里记住它，上游只返回加密的签名；再把签名回放给模型，让它复述出这个秘密。回放请求里不包含秘密本身，秘密只存在于加密签名中，所以能还原出来，就说明上游确实是能解密 thinking 签名的真 Claude。',
      copy: '复制',
      copied: '已复制',
      connection: {
        title: '连接配置',
        hint: '请求由本服务后端代为转发到你填写的网关，不受浏览器跨域（CORS）限制。API Key 仅用于本次转发，不会保存，也不会写入审计日志。',
        baseUrl: 'Base URL',
        apiKey: 'API Key',
        apiKeyPlaceholder: '仅保存在浏览器内存，刷新即丢失',
        model: '模型',
        modelPlaceholder: '从列表选择，或直接输入任意模型 ID'
      },
      secretLabel: '本次随机秘密',
      secretPending: '开始校验后随机生成',
      start: '开始校验',
      retry: '用同一签名重试还原',
      curl: {
        harvest: '提取第一次请求的 curl',
        replay: '提取第二次请求的 curl',
        harvestTitle: '第一次请求（采集签名）curl',
        replayTitle: '第二次请求（回放签名）curl',
        needSignature: '需先采集签名或在上方填入 signature'
      },
      steps: {
        harvest: '第 1 步：采集签名，模型正在隐藏推理中记住秘密…',
        replay: '第 2 步：回放签名，尝试还原秘密…'
      },
      verdict: {
        genuine: '还原出了本次的随机秘密 {secret}，本次测试 Claude 为真货。',
        notRecovered: '本次没有还原出随机秘密，请重试（不代表为假货，请多试几次加以确定）。'
      },
      noSignature: '上游响应里没有签名（该模型或网关未返回 thinking 块），无法校验。',
      details: {
        title: '查看请求详情',
        signature: '采集到的签名',
        signatureMeta: '绑定模型 {model} · {chars} 字符 · 隐藏推理 {tokens} tokens',
        openInDecoder: '放到上方解析',
        edited: '已修改',
        restore: '恢复原始签名',
        editHint: '可直接修改签名，修改后点击“用同一签名重试还原”会使用修改后的签名回放。',
        recovered: '回放还原出的内容',
        rawResponse: '回放原始响应',
        empty: '（未返回内容）'
      },
      reqErrors: {
        missingBaseUrl: '请填写 Base URL',
        missingApiKey: '请填写 API Key',
        missingSignature: '请先提供签名',
        proxy: '后端转发失败：{detail}',
        upstreamError: '上游返回错误',
        nonJson: '上游返回了非 JSON 内容：{detail}',
        http: '上游返回 HTTP {status}'
      }
    },
    injection: {
      description:
        '使用上方的连接配置，发送 3 条长度已知的测试请求（“hi” 后面分别加 0、200、800 个 “ hello”），把上游报告的输入 token 数（input + cache_creation + cache_read）和无注入时的真实值对比，估算上游每次请求额外注入了多少 token，以及是否按比例虚报 token。整个分析约消耗 2,000 个输入 token，上游注入的部分另计。',
      scopeNote:
        '只能分析这几条内置的测试请求。对于不同类型的请求（比如带工具、带 Claude Code 请求头的请求），上游注入的内容可能不同。',
      start: '分析注入量',
      progress: '正在发送测试请求：已完成 {done}/{total}',
      verdict: {
        none: '未发现注入',
        minor: '少量注入',
        major: '大量注入'
      },
      inflatedTag: '疑似按比例虚报 token',
      injectedLabel: '估算的注入量',
      injectedValue: '约 {n} token / 每次请求',
      injectedNote: '误差约 ±10 token。超过 500 token 通常是注入了整段系统提示词，例如伪装成 Claude Code。',
      inflationLabel: '虚报倍率',
      inflationNote: '上游报告的 token 数与真实值之比，正常为 1.00x。',
      table: {
        units: '填充单位数',
        expected: '真实 token 数（无注入时）',
        reported: '上游报告合计'
      },
      hints: {
        uncalibrated: '该模型不在校准表中，使用了默认基线，注入量误差可能更大，也无法计算虚报倍率。',
        unstable: '三条请求的计数不在一条直线上，上游计数不稳定，结果仅供参考。',
        cached: '测试请求本身没有开启缓存，但上游报告了缓存 token，说明上游自己加了缓存，注入的内容很可能被缓存了。',
        belowBaseline: '上游报告的 token 数比无注入时的真实值还少，可能截断了内容或改写了计数。'
      }
    },
    functional: {
      description:
        '使用上方的连接配置，复现针对 claude-fable-5-2 录制的 Messages API 测试用例（文本、停止词、beta 头、缓存、图片、引用、工具、computer use、负向用例、流式、超长上下文、安全），逐条校验响应并生成可导出的报告。',
      modelNote: '部分断言只对 claude-fable-5-2 成立（默认返回 thinking 块、用例 19 和 20 的 400 错误、920K 上下文）。',
      fableStrict: '当前模型是 claude-fable-5-2，这些断言严格判定',
      fableInfo: '当前模型不是 claude-fable-5-2，这些断言只记录实际表现',
      selectAll: '全选',
      invert: '反选',
      selectedCount: '已选 {n} / {total} 个用例，预计消耗约 {tokens} 输入 token',
      start: '开始测试',
      stop: '停止',
      progress: '正在执行用例 {id} {title}（已完成 {done}/{total}）',
      exportMarkdown: '导出 Markdown',
      exportJSON: '导出 JSON',
      copyCurl: '复制 curl',
      confirm: {
        title: '开始功能测试',
        message: '将依次向网关发送 {n} 个用例的请求，预计消耗约 {tokens} 输入 token。',
        longContext:
          '已选中超长上下文用例：483K 和 920K 两档费用较高，单次可能耗时数分钟。920K 需要目标环境支持 1M 上下文；如果本服务前面有反向代理，请求体上限需不小于 8 MB。'
      },
      groups: {
        basic: '基础',
        multimodal: '图片与文档',
        tools: '工具',
        negative: '负向用例',
        stream: '流式',
        longContext: '超长上下文',
        safety: '安全'
      },
      cases: {
        c1: '基础文本对话',
        c2: 'effort low + 空 stop_sequences',
        c3: '极小 max_tokens + 重复文本',
        c4: '中等长度回答',
        c5: '列表回答',
        c6: '简短问候 + service_tier',
        c7: 'stop_sequences（单个）',
        c8: 'stop_sequences（多个）',
        c9: 'Anthropic-Beta 头透传',
        c10: 'system prompt 缓存',
        c11: '图片输入 PNG',
        c12: '图片输入 WebP',
        c13: '文档 + citations',
        c14: '文档多轮对话',
        c15: '自定义工具调用',
        c16: 'Computer use 截图（带 beta 头）',
        c17: 'Computer use 回传截图',
        c18: 'Computer use 工具 + 普通问题',
        c19: 'Computer use 不带 beta 头',
        c20: '显式关闭 thinking',
        c21a: '流式输出：问候语',
        c21b: '流式输出：JSON 字面量',
        c22a: '大海捞针 46K',
        c22b: '大海捞针 483K',
        c22c: '大海捞针 920K',
        c23: '诚实性：夸大简历'
      },
      steps: {
        request: '请求',
        maxTokens: 'max_tokens {n}',
        betaKnown: '普通 beta 头',
        betaUnknown: '未知 beta 头',
        cacheWrite: '写入缓存',
        cacheRead: '读取缓存',
        toolResult: '回传截图',
        thinkingDisabled: '关闭 thinking',
        thinkingOmitted: '不带 thinking 重发'
      },
      assertions: {
        httpStatus: 'HTTP 状态码为 {expected}',
        textEquals: '文本为 “{expected}”',
        textContains: '文本包含 {expected}',
        textNotContains: '文本不包含 {expected}',
        stopReason: 'stop_reason 为 {expected}',
        stopReasonOneOf: 'stop_reason 为 {expected} 之一',
        stopSequence: 'stop_sequence 为 {expected}',
        hasThinking: '响应包含 thinking 块',
        noThinking: '响应不包含 thinking 块',
        outputTokensAtMost: 'output_tokens 不超过 {n}',
        wordCountBetween: '词数在 {min} 到 {max} 之间',
        wordCountEquals: '恰好 {n} 个词',
        noPreamble: '没有开场白',
        numberedItems: '恰好 {n} 条编号列表',
        nonEmptyText: '文本非空',
        serviceTier: 'usage.service_tier 为 {expected}',
        cacheCreated: '写入了（或读到了）提示缓存',
        cacheRead: '第二次请求命中缓存',
        hasCitations: '文本带有 char_location 引用',
        citationStart: '有引用从第 {n} 个字符开始',
        toolUse: '调用了工具 {name}',
        toolInput: '工具参数 {field} 为 {expected}',
        noToolUse: '没有调用工具，直接回答',
        errorType: '错误类型为 {expected}',
        errorMessageContains: '错误信息包含 {expected}',
        eventStream: 'Content-Type 为 text/event-stream',
        sseFirstEvent: '第一个事件是 {expected}',
        sseLastEvent: '最后一个事件是 {expected}',
        sseBlocksPaired: '每个 content_block_start 都有对应的 stop',
        jsonEquals: '文本可解析为 {expected}',
        finalNumber: '最后一行是 {expected}',
        needleFound: '找到昵称 {needle}',
        premiseCorrected: '纠正了 “prize-winning” 这个误导性前提',
        refusal: '拒绝夸大',
        alternativeOffered: '给出诚实的替代方案（超过 {n} 字符）'
      },
      status: {
        pass: '通过',
        fail: '失败',
        error: '出错',
        skipped: '已跳过',
        total: '合计'
      },
      level: {
        bonus: '加分项'
      },
      scope: {
        fable: '仅 fable-5-2'
      },
      report: {
        title: '功能测试报告',
        model: '模型',
        startedAt: '开始时间',
        elapsed: '耗时',
        tokens: 'Token',
        summary: '汇总',
        total: '合计',
        assertionCounts: '断言：通过 {pass}，失败 {fail}，加分项未达成 {warn}，仅记录 {info}',
        nonFableNote: '当前模型不是 claude-fable-5-2，fable 特有的断言只记录实际表现，不参与判定。',
        case: '用例',
        status: '状态',
        checks: '断言',
        details: '明细',
        error: '错误',
        request: '请求',
        response: '响应'
      }
    }
  }
}

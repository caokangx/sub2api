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
      verify: '签名真假校验'
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
        model: '模型'
      },
      secretLabel: '本次随机秘密',
      secretPending: '开始校验后随机生成',
      start: '开始校验',
      retry: '用同一签名重试还原',
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
    }
  }
}

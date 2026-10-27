# Claude Code 2.1.284 固定请求配置

`system.txt` 和 `tools.json` 通过 `go:embed` 编译进服务，构建时不读取调用方工具列表，也不探测工具是否可用。工具定义固定为样本中的 23 项，完整保留 description 和 input_schema。

来源为本地 `record-2217750.json` 的 `request.system[1].text`、根级 `tools`，并对照 Claude Code 2.1.284 二进制中的提示词构造代码。原始记录包含控制字符及非 JSON 转义；提取时进行了 JSON 字符串修复。原始抓包和凭据不纳入仓库。

- 原始样本 SHA-256：`74620eefb1b8f6d21ab98c7a01790e2096964d9c9660b188e28c85dca79ec60c`
- system.txt SHA-256：`161fd06e38b9e274741e3686fa03666acfef2ea797a8366d3ed28aebd2e78e81`
- tools.json SHA-256：`3889c842df9977ad47d99c7adb061565bd317b346a55e3180c45193cbb55a471`

样本中的私人 Memory 目录替换为 `{{MEMORY_DIRECTORY}}`；会话 token 预算删除。ultrareview、EndConversation 和末尾并行依赖规则采用本地版本的固定文本，运行时无条件列出。样本不含 `<pasted_content>` 防注入段，当前段落为显式补充规则，并非已核验的上游逐字原文。

`force_claude_code_profile` 默认关闭，只由管理员配置。最终生效分组必须为 Anthropic，账号必须为 OAuth/setup-token。原生 Messages 和两个 Count Tokens 路径共享构建器，完整丢弃原 system、删除 tool_choice；不会将原 system 搬到 messages，也不改历史工具名。tools 为固定的 23 项加上调用方名字以 `mcp__` 开头的工具（按名字去重排序后追加在末尾，去掉 `defer_loading` 与 `cache_control`），其余调用方工具丢弃。

system 只含 billing、identity 与 `system.txt` 三块静态内容。与 2.1.284 的 isMeta attachment 一致，动态内容放在第一条 user 消息开头的独立 `<system-reminder>` text block 中，依次为：session context（gitStatus，仅 git 仓库时）、Environment、模型身份（展示名与知识截止日期取自 2.1.284 内置模型表）。构建器会先移除同类的已有 reminder，重复执行结果不变，真实客户端自带的副本会被替换；其余 messages 字节不变。三个 reminder 在消息中的相对顺序、是否为独立 block，以及 MCP 工具相对内置工具的顺序，尚未用抓包核对。

构建器从 Anti-Ban 设置读取环境 profile，忽略旧环境识别开关的 off/observe/rewrite 模式。Memory、cwd、git、OS、shell 与环境 headers 使用同一份 profile；User-Agent 和 billing 版本使用固定 CLI 版本。保留现有 billing 指纹算法及最多 4 个 cache_control 断点约束。Fable 沿用 billing + identity 的精简 system，不注入 messages reminder，仍替换 tools。

temperature 按 2.1.284 主请求构建器的 `!thinkingEnabled && acceptsTemperature ? override ?? 1 : undefined` 规则处理。开启 thinking 或模型不支持时删除该字段；其余情况保留客户端值，缺失/null 时补 `1`。模型能力使用该版本内置规则：Claude 3 和 Opus 4.7 之前的相应 Claude 4.x 型号支持，未知/自定义模型默认不支持。该规则与既有 OAuth 请求归一化共用，不依赖入站 User-Agent。count_tokens 最终仍会删除生成参数。

关闭策略时沿用既有转发；API Key、Bedrock、其他平台及 OpenAI 协议转换路径不使用本配置。客户端须能执行固定工具定义及其自带的 `mcp__` 工具，上游返回的工具名不再反向映射成客户端原工具名。

升级 CLI 版本时应同步更新两个资源、哈希与 fixture 测试，并重新验证 Fable 上游兼容性。当前回归使用本地上游 recorder，没有向真实 Anthropic 账号发送 smoke 请求。

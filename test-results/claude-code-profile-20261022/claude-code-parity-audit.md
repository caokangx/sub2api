# 与 Claude Code 2.1.284 的请求一致性审计

审计对象为本目录已保存的六组 HTTP 请求，以及本地 Claude Code 2.1.284 可执行文件中的请求构建代码。未启动 CLI，未请求真实 Anthropic，也未修改业务逻辑。

这些快照生成于 temperature 修复之前。此前六组测试的 PASS 表示约定的 system/tools/消息保留等断言通过，**不表示完整请求与原生 Claude Code 一致**。temperature 修复已经写入工作区，但这些历史快照没有重新生成。

静态证据保存在 `claude-code-reference-evidence.json`，包括函数片段、源码行号或原始二进制字节偏移；每组快照的审计事实保存在 `parity-audit-facts.json`。参考版本的二进制 SHA-256 为 `50a14c2f50f56668380fdda490167f1d3630d5cc18fb8aed3073c2c7ea7314fe`。

## 1. 缺少 X-Claude-Code-Session-Id（明确差异，优先处理）

六份出站请求均不含该头。CLI 的 `cV` 创建客户端时将 `[Zpt]: q()` 加入默认 headers，`Zpt` 定义为 `X-Claude-Code-Session-Id`；metadata 的 `bA` 同样使用 `q()` 生成 `session_id`。

当前网关只在该头已经存在时同步其值，不会为第三方请求补齐。应确保出站 session header 与最终 metadata 中的 session_id 同源，而不是另造独立随机值。

网关位置：`backend/internal/service/gateway_upstream_request.go` 的“同步 X-Claude-Code-Session-Id”分支；count_tokens 构建器也有同样分支。CLI 证据：`CLI client default headers and session header`、`Session header name`、`Metadata JSON serialization`。

## 2. beta 列表依旧受入站客户端身份影响（明确差异，优先处理）

案例 01 和 04 的最终 model/system/tools 相同，但 beta 列表不同：

- 案例 01：9 项固定 mimic beta，包括 prompt-caching-scope、effort、thinking-binding-controls、mid-conversation-output-config、extended-cache-ttl。
- 案例 04：4 项默认 beta，包括 fine-grained-tool-streaming，而缺少上述多项。

原因是 `forceProfile` 没有替代 `shouldMimicClaudeCode` 对最终 beta 的选择。仅修改客户端 UA/metadata 就能改变强制复写后的协议特征。

原生 CLI 按模型能力、provider、缓存模式、功能开关和会话状态构造 beta，不能用“任意请求统一加满 beta”来完整复现。尤其 extended-cache-ttl 在 CLI 主请求代码中与 1h TTL 条件关联；测试请求只有默认/5m 缓存标记，却无条件带有它。现有固定列表与真正的条件构建有差异，但携带额外 beta 本身不等于上游一定拒绝。

网关位置：`computeFinalAnthropicBeta`、`FullClaudeCodeMimicryBetas`。CLI 主请求位置：`mac-claude-analysis/evidence/modules/module-184839320.js:1843`。

## 3. max_tokens 强制改成 128000（明确行为差异，优先处理）

所有五组 Messages 请求的客户端 body 都是 `max_tokens:16`，最终出站均为 `128000`，包括案例 04。

这不只是“未提供时补默认值”：前置处理器无条件覆盖了客户端值。CLI 的 `cst` 根据模型默认值、模型上限和 `CLAUDE_CODE_MAX_OUTPUT_TOKENS` 计算预算，主请求再用 `Math.min(override || default, modelBudget)` 选择实际值。

因此 `128000` 并非绝对不可能由原生 CLI 发送；差异是当前网关把显式限制和模型/会话选择压成了固定值。这也会改变客户端要求的输出上限。

网关位置：`PreprocessClaudeMessages` 中 `outbound["max_tokens"] = normalizedClaudeMessagesMaxTokens(...)`。CLI 证据：`Max tokens model/environment selection`、`Max tokens model capability`、`Main request max tokens and thinking`。

## 4. 缓存断点和 system 分块只实现了固定方案（条件性差异）

案例 01–05 的固定形态是 billing、identity、主提示词、环境共四块；主提示词和环境带 ephemeral，最后一个工具 Write 带 ephemeral + 显式 ttl=5m；messages 均没有缓存断点。

CLI 的 `Ipe`/`hRo` 按缓存配置和 system 边界选择合并/分块，可使用 global scope；默认 5m 时通常省略 ttl，1h 时显式携带；开启消息缓存时，`ECo`/`PCo` 会将相应字符串 content 转为文本块数组，并在选定消息块设置 cache_control。

所以：

- 当前实现没有完整复现 CLI 的 system/global/dynamic 缓存边界。
- 对标开启 prompt caching 的典型主对话时，缺少消息断点是差异。
- 显式 ttl=5m 和省略 ttl 的默认语义可能相同，不应把字面差异都视为功能错误。
- CLI 允许关闭 prompt caching，因此“任何消息都必须有缓存断点”也不成立。

CLI 证据：`System cache boundaries`、`System cache control rendering`、`Cache TTL selection`、`Message cache control rendering`。

## 5. count_tokens 不是 CLI 自己的计数请求形态（明确差异，但与当前既定设计有关）

案例 05 发送完整四块 system 和 23 个工具，并携带完整 mimic beta 列表加 token-counting-2024-11-01。

该版本 CLI 的 `_ke` 调用 `beta.messages.countTokens` 时构造 model/messages/tools；历史存在 thinking 时补 enabled + budget_tokens=1024；betas 经过专用 allowlist。该调用不传 system、metadata，也不会直接复用主对话的完整 beta 集合。CLI 还会清理工具的 eager_input_streaming/defer_loading/strict 等计数不需要的字段。

CLI 的计数 beta allowlist 在此版本为 claude-code、interleaved-thinking、context-management、oauth-auth 这四类，最终出现哪些取决于输入 beta。测试中的 token-counting-2024-11-01 和多项主对话 beta 不来自这条原生 allowlist。

注意：此前方案明确要求 Messages/count_tokens 共用完整 system/tools 复写。这是已选择的业务行为与原生 CLI 之间的差异。若改变为原生 CLI 的计数形态，计数语义也要重新评估，不能仅机械删除 system。

CLI 证据：`Count tokens request input`、`Count tokens beta allowlist`。

## 6. metadata 没有统一成 2.1.284 的生成方式（明确差异，部分值属于测试数据）

案例 01–03/06 是 JSON 编码的 user_id 字符串；案例 04 仍是旧式 `user_..._account_..._session_...` 字符串。CLI 的 `bA` 统一用 JSON 编码 device_id/account_uuid/session_id，并可能加入 parent_session_id 等条件字段。

当前强制复写只统一 system/tools/环境，没有统一 metadata。案例 04 特意使用了旧式合成 metadata 触发 Claude Code 识别，这个输入并不代表 2.1.284 实际会生成该格式。

普通案例里的 account_uuid 为空也与临时账号构造有关：测试将 UUID 放在 credentials 中，网关这条路径读取的是 extra。CLI 在没有账号 UUID 可用时同样允许回退空字符串，因此不能把空值本身认定为生产代码必错。

CLI 证据：`Metadata JSON serialization`。

## 7. billing block 被简化，现有 cch 注释不可靠（条件性差异）

快照仅保留 `cc_version=...; cc_entrypoint=cli;`。CLI 的 `NFr` 除这两项外，在特定 provider/认证/会话分支还会加入 cch、cc_workload、cc_is_subagent、cc_prev_req、cc_prompt_id、cc_turn_origin、cc_prompt_index/cc_turn_index。

尤其源码仍有 `firstParty && xs()` 条件下加入 `cch=00000;` 的分支。这足以否定网关注释中“新版 CLI 已不再发送 cch”的绝对说法，但**静态分支存在不证明每次请求都会触发，也不证明最终 wire 签名值就是占位符**。需要继续追踪认证条件和最终发送前处理，才能确定真实 OAuth 主会话应如何生成。

不能无条件把所有可选字段塞入每条请求。网关位置：`gateway_billing_block.go`。CLI 证据：`Billing attribution conditional fields`。

## 8. 固定提示词不是完全核验的原文，且工具引用存在缺口（明确内容差异）

fixture README 已披露 `<pasted_content>` 段是额外补充规则，原始样本没有该段，不能将现有 system 称为已验证逐字一致。

另外，当前固定 system 让模型先通过 ToolSearch 加载 EndConversation；23 项 tools 中没有 ToolSearch，也没有 EndConversation。指令与实际工具面存在缺口。原生 CLI 的 deferred tools 和 ToolSearch 依赖运行时能力，单份静态可见工具样本不等于所有可调用工具的完整闭包。

此前用户明确要求构建器无条件列出固定全部内容、复写全部 tools，因此固定清单属于既定要求；应单独决定如何处理指令引用和客户端工具支持，而不是擅自恢复动态构建。

## 9. thinking / stream 需要在相同场景下比较

这六个合成输入没有启用 thinking，Messages 请求均显式要求 stream=false。CLI 典型主会话会按配置启用 adaptive/enabled thinking，并首先用 stream=true；也存在关闭 thinking、side query 和非流式 fallback。

所以这些快照没有完整覆盖“默认 Claude Code 主会话”的形态，但不能仅凭缺少 thinking 或 stream=false 就认定网关请求构造错误。测试中未覆盖的 safeguards、effort、fast mode、工具搜索等条件功能，也无法据此给出全面一致性结论。

## 不应当误报的项目

- example.com、假 token、合成客户端请求 ID、固定环境目录是本地测试/固定 profile 的选择；不是实际发往 Anthropic 的流量证据。
- SDK package version 0.112.1、Accept=application/json、anthropic-version=2023-06-01、timeout=600 可在此版本 SDK/CLI 代码找到对应实现。
- SDK 的 node runtime header 不能仅因 CLI 打包为 Bun 就认定错误；该 SDK 在 Node-compatible process 环境可报告 node。
- 历史 caller_private_tool 保留而新 tools 清单不含该名称，是此前明确要求保留历史工具名的兼容性取舍。
- Fable 的两块 system 是当前网关保留的例外策略，本轮没有真实 Fable 对照流量，不能证明它等同原生 CLI 的 Fable 请求。

建议先处理 session header 同源、强制 profile 下 beta 决策、max_tokens 覆盖，再评估缓存边界、metadata 统一和 billing 条件；count_tokens 与固定工具内容需结合既定产品要求决定。

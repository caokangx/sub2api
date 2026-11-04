# Claude Agent SDK 0.3.310 请求配置

来源为本机 Claude Code 2.1.284 + `@anthropic-ai/claude-agent-sdk` 0.3.310 的 22 个抓包场景。

动态规则：

- `cc_prompt_id`：每次用户提交生成新 UUID；同一用户轮次内的工具往返保持不变。
- `cc_turn_index`：从 1 开始，每次新的用户提交递增；同一轮工具往返保持不变。
- `cc_prev_req`：除会话首个上游请求外，取同一会话上一次上游响应的 `request-id`。抓包核验的 79 个后续请求全部匹配。
- `cch`：先在完整最终 JSON 里保留 `cch=00000`，按 Bun 原生规则排除所有 `model` 值以及 `max_tokens`、`fallbacks`、`fallback_credit_token` 字段，再使用种子 `0x4D659218E32A3268` 计算 xxHash64，取低 20 位并以 5 位小写十六进制覆盖占位符。实现对 160 条 2.1.284 抓包请求逐字节复算，160/160 匹配。
- `<total_tokens>`：每个新用户轮次重置为 15000000；同一轮工具往返时，扣除本轮此前模型响应累计占用的上下文。精确的客户端内部估算不能仅由单次网关请求稳定还原，网关实现使用可观察 usage 的确定性近似；无状态模式下优先由请求历史估算。
- 身份块在真实抓包中带 1h `cache_control`；本方案按产品决定故意不带。

`testdata/` 已将 device/session/message/request/prompt 动态值替换为占位符，未包含 Authorization。

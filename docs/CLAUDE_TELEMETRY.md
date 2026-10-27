# Claude Code 遥测重建

Sub2API 可以根据 Anthropic `/v1/messages` 的最终上游请求与响应，重建 Claude Code
2.1.284 形态的模型生命周期遥测。配置入口位于管理后台「系统设置 → 网关设置 →
Claude Code 遥测」。

## 生效范围

- 默认只处理最终选中的 Anthropic OAuth 账号。
- Setup Token 默认关闭，可在配置页单独开启。
- Anthropic API Key、Bedrock/AWS、Service Account、Upstream、Antigravity 和
  Composite 转换链不会生成、记录或发送重建遥测。
- 客户端不必是 Claude Code；非 Claude Code 请求仍使用最终 OAuth wire body 中的身份。

## 模式

- `off`：关闭重建。
- `record`：只写本地 NDJSON，默认模式。
- `forward`：只向启用的第一方和 Datadog 目标发送。
- `both`：同时记录和发送。

本地记录文件必须位于 `data/telemetry/` 下，默认是
`data/telemetry/telemetry-batches.ndjson`；导出器拒绝符号链接目标。

第一方默认目标是 `/api/event_logging/v2/batch`，使用最终 OAuth 账号的 token、代理和
TLS 路由。OAuth token 只会附加到 HTTPS `api.anthropic.com`，自定义测试接收端不会收到
账号凭据。Datadog 必须显式启用并配置 API key。设置 API 永不回显 Datadog key；空值
表示保留，显式勾选清除才会删除。

## 数据边界

重建事件只包含模型调用可观察或可确定计算的字段，例如：

- 请求/响应的消息、工具、媒体与字符数量；
- system 首块的无盐 SHA-256 与长度；
- token usage、缓存 token、request ID、stop reason；
- 总耗时、首内容耗时和输出块长度；
- 有界会话状态推断出的 query chain、depth 和 prompt index。

不会记录用户提示词、system prompt、模型输出、工具参数或工具结果正文，也不会把
Sub2API 服务器的 CPU、内存或 uptime 冒充 Claude Code 客户端进程指标。客户端 UI、
插件、Skill、MCP 生命周期、GrowthBook、本地文件和更新器事件不重建。

system prompt 哈希仍可对已知候选做匹配；NDJSON 中也可能包含稳定的 account、org、
device 和 session 标识，应按敏感日志保护。账号与最终 wire body 都没有提供的标识直接
省略，不合成替代值。

## 批处理与交付

默认参数与证据基线一致：

- 提交间隔：10 秒
- 单批上限：400
- 队列上限：8192

第一方和 Datadog 独立跟踪交付状态。网络错误、HTTP 429 和 5xx 按配置重试；
第一方 401 只进行一次无认证回退。管理页展示队列、丢弃数、最近 flush 和两路交付状态。

客户端原生 `/api/event_logging/batch` 与 `/api/event_logging/v2/batch` 当前仍由本地
sink 返回成功但不转发。不要同时透明转发原生批次和启用重建 `forward/both`，否则可能
重复上报。

## 一致性说明

目标是“可观察字段精确 + 客户端内部状态受控推断”，不是完整复制 Claude Code：

- 请求、最终 wire body、SSE/JSON 响应能证明的字段保持原生名称、类型和单位。
- query chain、depth、prompt index 等状态使用隔离、带 TTL 的确定性规则推断。
- 无法从模型链路观察的数据保持缺失，不填固定假值。

兼容 profile 固定为 `2.1.284`，不会自动随最低客户端版本变化。

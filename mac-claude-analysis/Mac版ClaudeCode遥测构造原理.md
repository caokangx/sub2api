# Claude Code 2.1.284（macOS ARM64）遥测构造与发送原理

分析日期：2026-10-15。分析对象：本机当前 `claude` 命令实际指向的 macOS ARM64 发布产物。

本文复用 Windows 报告的证据方法：只读分析已安装可执行文件，从 Bun 封装区提取 NUL 分隔的内嵌 JavaScript，再按函数和字符串锚点还原遥测主链路。没有启动 Claude Code、没有读取认证材料、历史对话或当前遥测设置，也没有发起遥测或抓包测试。

## 1. 对象与证据边界

- 命令链接：`/Users/yikangcao/.local/bin/claude`
- 实际文件：`/Users/yikangcao/.local/share/claude/versions/2.1.284`
- 格式：Mach-O 64-bit，ARM64
- 大小：226,563,088 字节
- SHA-256：`50a14c2f50f56668380fdda490167f1d3630d5cc18fb8aed3073c2c7ea7314fe`
- 版本：`2.1.284`
- 构建时间：`2026-09-28T02:26:10Z`
- Git SHA：`2b8ce618c24de26410e4bdfc4e1d592accd61f61`
- 源映射组：`darwin`
- 签名标识：`com.anthropic.claude-code`
- 签名团队：`Q6L2SF6YDW`
- 签名主体：`Developer ID Application: Anthropic PBC (Q6L2SF6YDW)`

Mach-O 的 `__BUN,__bun` section 从文件偏移 `68,206,592` 开始，大小 `157,689,482` 字节。按内嵌版本标记和 NUL 边界识别到 2,167 个源码模块；本次抽取 10 个与遥测主链路直接相关的模块。[M01]

这里分析的是 Mac 上当前安装的 `2.1.284`，而 Windows 报告分析的是 `2.1.299`。两份结果可以比较结构，但任何差异都不能直接归因于操作系统，因为版本并不一致。

## 2. 结论先行

Mac 产物与 Windows 报告具有相同的三条遥测主结构：

1. Anthropic 内建事件：业务动作生成 `tengu_*` 属性，经采样、公共元数据、OTel LogRecord 和 JSON 转换后，默认发往 `/api/event_logging/v2/batch`。
2. Anthropic 内建指标：OTel meter 数据转换为内部 metrics body，发往 `${BASE_API_URL}/api/claude_code/metrics`，并经过资格、信任、凭据 host 和组织策略检查。
3. 用户配置的 OpenTelemetry：由 `CLAUDE_CODE_ENABLE_TELEMETRY` 及各 signal exporter 配置初始化 metrics、logs、traces exporter。

因此，Mac 与 Windows 的主要差别是外层容器和原生运行时：Windows 是 PE/x86_64，当前 Mac 是 Mach-O/ARM64。已定位的遥测业务逻辑仍是内嵌 JavaScript，主流程并没有出现一套 Mac 专用协议。

## 3. 内建事件链路

### 3.1 启用条件

`ZB()` 返回 `!Kg()`；`Kg()` 的条件是：

```js
providerNotAllowed ||
gatewayAuthPresent ||
nonessentialTrafficDisabled ||
customOAuthUrlPresent
```

其中 provider 管理模式有单独分支；gateway 状态由 `po() !== null` 判断；自定义 OAuth URL 存在时也会禁用内建事件。[M02、M03]

统一非必要流量判断的顺序为：

1. `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` 非空 → `essential-traffic`
2. `DISABLE_TELEMETRY` 非空 → `no-telemetry`
3. `DO_NOT_TRACK` 通过真值解析 → `no-telemetry`
4. 其余 → `default`

前两项按字符串是否存在判断，所以 `"0"` 仍会触发。`tengu_frond_boric.firstParty` 还可在记录和发送阶段作为 sink kill switch。[M02、M05]

### 3.2 采样与事件包装

事件包装层读取 `tengu_event_sampling_config[eventName].sample_rate`：

- 缺失、非数字或超出 `[0, 1]`：返回 `null`，这一层不主动丢弃。
- `sample_rate <= 0`：返回 `0`，丢弃事件。
- `0 < sample_rate < 1`：用 `Math.random()` 决定保留。
- `sample_rate >= 1`：返回 `null`，保留。

被抽样保留时，可在属性中添加 `sample_rate`。包装层有同步重入保护；元数据依赖尚未准备好时，会等待初始化 Promise 后再转交内建和可选 Datadog sink。[M04、M05]

### 3.3 公共元数据

事件记录函数先调用公共元数据构造器，再形成 LogRecord attributes：

```js
{
  event_name,
  event_id,
  core_metadata,
  user_metadata,
  event_metadata,
  user_id? 
}
```

公共数据可包含 session、model、betas、入口、交互模式、client type、prompt ID、OS/platform、arch、Node、terminal、shell、包管理器、运行时、CI、远程/容器状态、仓库 head、进程指标、账号/组织 UUID、订阅类型等。字段是否出现取决于运行形态、认证来源、隐私模式和数据可用性。[M06]

业务调用点已确认包含：

- `tengu_api_success`：model、request/client request ID、用户消息 UUID、query source、输入/输出/cache tokens、耗时、重试、stop reason、provider、effort、部分 request 形状统计等。
- `tengu_api_error`：model、错误分类、状态、消息数量和估算 tokens、耗时、重试、provider、请求 ID、fallback/query chain 等。

这是构造能力，不代表每个字段每次都发送。[M12]

### 3.4 HTTP body

`transformLogsToEvents()` 生成两种事件：

- `ClaudeCodeInternalEvent`
- `GrowthbookExperimentEvent`

普通事件的大致形状为：

```json
{
  "events": [
    {
      "event_type": "ClaudeCodeInternalEvent",
      "event_data": {
        "event_name": "tengu_api_success",
        "event_id": "<uuid>",
        "client_timestamp": "<time>",
        "session_id": "<session>",
        "model": "<model>",
        "env": {},
        "auth": {},
        "additional_metadata": "<base64-json>"
      }
    }
  ]
}
```

skill、plugin、marketplace、MCP server/tool、head SHA 等特定字段会先提升为顶层字段。其余业务属性经处理后使用：

```js
Buffer.from(JSON.stringify(metadata)).toString("base64")
```

Base64 不是加密。缺失 `core_metadata` 时会生成带 `transform_error` 的降级事件。[M07]

## 4. 字段过滤与内容开关

在当前 `2.1.284` 内建事件模块中，严格事件白名单的直接判定是 `Ql("hipaa")`。启用后：

- 顶层、env 和 auth 字段按格式及枚举白名单过滤。
- 未知字符串会变为 `hipaa_redacted`，而不是原样保留。
- `additional_metadata` 仅保留 `sample_rate` 和少量事件专属的数值、布尔或枚举字段。
- 磁盘失败队列重放前再次过滤。[M08]

配置式 OTel 的内容控制是独立路径：

- `OTEL_LOG_USER_PROMPTS`：决定已定位的 prompt 字段使用原文还是 `<REDACTED>`。
- `OTEL_LOG_ASSISTANT_RESPONSES`：未设置时回退到 prompt 开关。
- `OTEL_LOG_TOOL_CONTENT`：控制已定位的工具内容输出。
- `OTEL_LOG_TOOL_DETAILS`：控制部分工具类别、名称和细节字段。
- `OTEL_LOG_RAW_API_BODIES`：另有原始 API body 记录路径；代码会对 thinking 文本做特定替换，但不能把它理解为所有字段的全局去敏保证。

这些开关不能推广为内建事件、错误上报、反馈和所有 trace 的统一隐私保证。[M12、M16]

## 5. 缓冲、失败队列与重试

当前版本的已定位默认值：

- 初始化前事件队列：1,024
- LogRecord 批处理定时：10,000 ms，可被配置或 `OTEL_LOGS_EXPORT_INTERVAL` 影响
- 最大批次：200
- LogRecord 队列：8,192
- HTTP timeout：10,000 ms
- 批间等待：100 ms
- 基础重试延迟：500 ms
- 最大重试延迟：30,000 ms
- 最大尝试状态：8

失败文件路径形态：

```text
<Claude config dir>/telemetry/
  1p_failed_events.<session-id>.<run-id>.json
```

storageV5 路径使用当前 session 的 `telemetry` stream。文件和 stream 访问通过 Promise 锁串行化；重试先认领并删除批次，失败部分再写回。[M09、M10]

退避计算为：

```js
Math.min(
  Math.max(baseDelay, baseDelay * attempts * attempts),
  maxDelay
)
```

这是平方增长，不是 `2 ** attempts`。启动重放只枚举当前 session ID 下的其他 run ID；不会由这段代码扫描所有用户的全部历史队列。[M10]

## 6. 内建事件 HTTP 发送

默认 endpoint：

```js
base = config.baseUrl || (
  ANTHROPIC_BASE_URL === "https://api-staging.anthropic.com"
    ? "https://api-staging.anthropic.com"
    : "https://api.anthropic.com"
)
endpoint = base + (config.path || "/api/event_logging/v2/batch")
```

基础 headers：

```http
Content-Type: application/json
User-Agent: <Claude Code UA>
x-service-name: claude-code
```

凭据附加前会检查信任、第一方 HTTPS host、profile scope、OAuth 状态和工作负载身份目标 host。带认证请求收到 401 时，当前版本有一次去掉认证头的重发分支。可选包装器还可以改写 body 并补充 headers。[M09、M11]

当前 Mac `2.1.284` 有一个明确的版本差异：`logSuccess(e,n,r){}` 是空实现，整个二进制中也没有 `accepted_count` 字符串。Windows `2.1.299` 报告中的 `accepted_count` 处理因此不能套用到这个版本；这里的成功判断以 POST 成功返回为主。[M18]

## 7. 内建指标

内部 metrics reader 的资格函数先检查统一 opt-out，再结合 Claude AI 认证和 enterprise/team 等条件。符合条件时，默认 endpoint 为：

```text
${BASE_API_URL}/api/claude_code/metrics
```

发送前会：

1. 检查 workspace trust。
2. 检查 WIF dispatch host 和凭据目标 host。
3. 在适用条件下查询组织 `metrics_enabled`。
4. 把 resource attributes、scope metrics 和 data points 转成内部 JSON。
5. 在受限模式下对白名单外字段替换或删除。
6. 通过可选包装器发送；OAuth 401/特定 403 可刷新 token 后重试。

许多失败分支向 OTel callback 返回 SUCCESS，使遥测故障不阻断主流程；这不代表 HTTP 成功。[M13、M14]

## 8. 用户配置的 OpenTelemetry

`Dt()` 对 `CLAUDE_CODE_ENABLE_TELEMETRY` 做真值解析。启用后，metrics、logs 和 traces 分别读取 exporter 配置；逗号分隔并过滤 `none`。

已定位支持：

- metrics：`console`、`otlp`、`prometheus`
- logs：`console`、`otlp`
- traces：`console`、`otlp`
- OTLP protocol：`grpc`、`http/json`、`http/protobuf`

配置式日志添加公共属性、`event.name`、时间、递增 `event.sequence`、可选 `prompt.id` 和 trace context，LogRecord body 为 `claude_code.<event>`。[M15、M16]

因此，未设置 `CLAUDE_CODE_ENABLE_TELEMETRY` 不等于内建第一方事件一定关闭；三条链路必须分别判断。

## 9. 插件遥测接口

插件侧遥测模块有自己的批处理和禁用判断，默认 ingest 仍是 event logging v2。它限制：

- 最多 16 个属性。
- key 必须是 snake_case token。
- 布尔值和有限数字可直接使用。
- 自由字符串被拒绝；字符串必须用 `Choice { value, of }`，选项最多 32 个。
- 普通事件名可加 `tengu_plugin_` 前缀。

该限制只证明插件接口拒绝自由文本，不能推广到所有 CLI 内部事件。[M17]

## 10. 与 Windows 2.1.299 报告的关系

已确认相同：

- 三条遥测主链路。
- 事件的 LogRecord → `events` JSON 转换。
- 默认事件和 metrics endpoint。
- 统一 opt-out 的核心环境变量及优先级。
- 采样、sink kill switch、预初始化队列。
- Base64 `additional_metadata`。
- 文件/storageV5 失败队列、平方退避和当前 session 重放。
- 自定义 OTel signal/exporter/protocol 结构。
- 插件属性限制。

已确认不同或不能直接等同：

- 文件容器与架构：PE/x86_64 对 Mach-O/ARM64。
- 版本、构建时间、Git SHA 和压缩函数名。
- Mac `2.1.284` 没有 `accepted_count` 处理；Windows 报告的 `2.1.299` 有。
- 当前版本内建事件严格过滤的直接判定是 HIPAA；Windows `2.1.299` 报告还记录了远程/托管组合条件。

若要判断最后两项究竟是平台差异还是版本演进，需要取得同版本产物做交叉比较，例如 macOS `2.1.299` 对 Windows `2.1.299`。

## 11. 未决范围

- 没有检查当前环境变量、用户/组织设置或运行时 exporter 实例。
- 没有抓取本机真实网络请求，所以不能据此声明“当前会话实际上传了哪些字段”。
- 没有执行原生反编译、完整跨模块 reachability 或服务端保留策略审计。
- 没有完整审计 Datadog、反馈上传、更新检查和所有第三方依赖网络路径。
- 源码中存在的字段和 endpoint 代表构造能力，不等于当前配置下必然可达。

## 12. 配套证据

- [代码证据摘录](代码证据摘录.md)
- [证据索引](evidence-index.json)
- [产物及模块哈希](evidence-manifest.json)
- `evidence/modules/`：按原始文件偏移命名的内嵌源码
- `extract_evidence.py`：可重复执行的只读提取器

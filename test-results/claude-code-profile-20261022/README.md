# Claude Code profile 本地接口测试结果

测试日期：2026-10-22（日志时区 Asia/Shanghai）。本目录保存已执行测试的证据，包含真实本地 HTTP 请求内容和网关最后构建的出站 HTTP 请求内容。

**测试边界：** 入站地址为 `http://127.0.0.1:8080`。使用临时 Anthropic OAuth 测试账号、假 token 和开启 `force_claude_code_profile` 的临时分组。该账号的中继地址设置为 `https://example.com`，因此没有调用真实 Anthropic；`upstream.request.json` 是完成 Anthropic 协议转换后、交给 HTTP 客户端发送前的最终请求快照，不代表远端已经收到或接受。HTTP 502 / 超时不影响对本地转换结果的校验。

临时资源：分组 ID 63，账号 ID 2224，API Key ID 1631。原分组 ID 40 的开关已恢复为 false。鉴权头已脱敏。

| 案例 | 输入场景 | HTTP 结果 | 最终 system 块 | 最终 tools 数量 | 请求内容校验 |
| --- | --- | --- | --- | --- | --- |
| 01-string-system | 字符串 system 与自定义 tools | 502 | 4 | 23 | 通过 |
| 02-block-system-history | 数组 system、历史 tool_use / tool_result | 502 | 4 | 23 | 通过 |
| 03-no-system-no-tools | 不提供 system / tools | 502 | 4 | 23 | 通过 |
| 04-claude-client | Claude Code UA 与 metadata | 502 | 4 | 23 | 通过 |
| 05-count-tokens | count_tokens 路径 | 客户端等待超时（15s） | 4 | 23 | 通过 |
| 06-fable | Fable 例外路径 | 502 | 2 | 23 | 通过 |

每个案例子目录包含：

- `client.request.json`：测试客户端调用本地接口的 URL、方法、显式请求头、完整 JSON body。
- `client.body.json`：可直接传给 curl `--data-binary @...` 的原始请求 JSON。
- `gateway.received.json`：网关原始请求快照，包含实际接收到的请求头与 body。
- `upstream.request.json`：最终出站 URL、方法、请求头和完整 JSON body。
- `upstream.body.json`：单独保存的完整出站 body，便于对比。
- `response.txt`：客户端实际收到的响应或超时说明。
- `verification.json`：此案例的逐项断言结果。

核对结果：调用方 system 已完全替换；全部 23 个 tools 的名称、描述、schema 与版本化 fixture 一致（比较时忽略顶层 cache_control）；tool_choice 已删除；用户消息及历史工具调用/结果保持不变；固定 UA 为 2.1.284；cache_control 总数不超过 4。普通路径的完整主提示词匹配 fixture，Fable 只保留 billing + identity 两块。count_tokens 还去除了 max_tokens、stream 等生成参数。

`gateway_debug.log` 仅包含这六组测试的入站和出站快照。`verification.json` 和 `http-results.json` 汇总所有案例。`verify.py` 可再次核验快照：

```sh
python3 test-results/claude-code-profile-20261022/verify.py
```

再次调用接口需要自行指定一个归属于启用复写分组的有效本地 Key（本次临时 Key 会被停用）：

```sh
curl http://127.0.0.1:8080/v1/messages \
  -H "x-api-key: ${PROFILE_TEST_API_KEY}" \
  -H 'content-type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  --data-binary @test-results/claude-code-profile-20261022/02-block-system-history/client.body.json
```

日志开关：`SUB2API_DEBUG_GATEWAY_BODY=/app/data/gateway_debug.log`。本次补齐了 count_tokens 的 `CLIENT_ORIGINAL_COUNT_TOKENS` 与 `UPSTREAM_FORWARD_COUNT_TOKENS` 快照；普通 Messages 使用 `CLIENT_ORIGINAL` / `UPSTREAM_FORWARD`。仅设置环境变量时写完整请求。

测试收尾：临时 API Key、账号、分组已停用并软删除；原分组 40 的开关为 false。应用容器恢复为原开发镜像配置，完整请求调试日志已关闭。新增 count_tokens 日志代码保留在工作区，后续镜像构建时生效。

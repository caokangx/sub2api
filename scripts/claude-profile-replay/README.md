# Claude OAuth 本地抓包回放

工具通过 `http://127.0.0.1:8080/v1/messages` 的现有 OAuth、强制复写、身份、请求头构造和净化链路，捕获最终发往 `http://claude-profile-mock:8090` 的请求。原始录制仅提供请求体，工具不声称恢复了原始 HTTP 请求头。

本地测试通道长期保留。初始化资源 ID 在 `test-results/claude-profile-replay/channel.json` 中；重复初始化、运行或重启复用它们。每轮先检查独占分组、强制复写、唯一 mock OAuth 账号、空 fallback、假凭证、无代理、零费率和管理员 ID 1 的专用 Key。配置漂移会终止回放，不会自动改写现有通道。

## 准备与启动

从仓库根目录执行，要求 Docker、Go、Python 3.9+ 和已有本地开发部署。独立 Compose 覆盖文件保持 mock 自动重启，并将捕获日志存到宿主机忽略目录。

```sh
python3 -m venv scripts/claude-profile-replay/.venv
scripts/claude-profile-replay/.venv/bin/pip install -r scripts/claude-profile-replay/requirements.txt
mkdir -p test-results/claude-profile-replay/runtime
```

先构建包含本次隔离规则的网关。按 Docker 主机架构设置 `GOARCH`（当前本机为 `arm64`；Intel 通常为 `amd64`）。覆盖文件会把该二进制只读挂载到本地开发网关。

```sh
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags embed -o ../test-results/claude-profile-replay/runtime/sub2api ./cmd/server
cd ..
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py start
```

`start` 只启动现有开发网关和 mock，不构建网关镜像；它需要上述二进制。mock 使用 `python:3.13-alpine`，仅加入 Docker `internal` 网络，无互联网出口、代理转发或宿主机端口。控制接口仅绑定容器 `127.0.0.1:8091`，控制命令通过 `docker exec` 发出；网关实际请求走独立的 `8090` 端口。网关保留已有网络，增加 mock 内网和 `NO_PROXY`。

## 初始化与重复运行

默认使用 `deploy/.env` 中的管理员登录信息；也可用 `CLAUDE_REPLAY_ADMIN_TOKEN_FILE` 指定已有管理员 ID 1 的 JWT 文件。临时管理会话不会写入运行报告。

如果本地 `.env` 密码已经与数据库不一致，可显式执行开发环境专用命令，读取本地 `sub2api-postgres-dev` 的持久化签名配置，在忽略目录保存两小时、`0600` 的管理会话。它不修改密码或签名配置，不连接远端数据库：

```sh
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py local-session
```

保存的管理会话过期后，重新执行该命令或提供新的 JWT 文件。测试 API Key 长期保留在 `test-results/claude-profile-replay/api-key.secret`，权限固定为 `0600`，终端和报告不输出 Key。

```sh
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py init
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py check
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py select /path/to/ccmax '/path/to/外接（39）'
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py run --limit 3
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py run
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py verify RUN_ID
```

`init` 为管理员 ID 1 增加该独占测试组的授权，保留原有授权。分组和账号费率均为零，mock 返回的四项 token 用量也均为零。OAuth 账号只保存固定假 access token、测试 UUID 和 `2099-01-01T00:00:00Z` 到期时间，无 refresh token 或代理。现有 `synthetic_ui_test=true` 仍支持模拟用量与连接测试，并阻止刷新、遥测及账单探测。普通 OAuth 账号保留原有逻辑。分组 API 支持零费率，并继续拒绝负费率。

选样默认种子 `42`、每组 `40` 条；对模型、会话、system/tools 哈希、图片、工具历史、thinking、大小和 stream 做边际分层覆盖。存在 manifest 时直接复用，不重新抽样。保存源 HJSON SHA-256、附件大小/SHA-256、还原后 body SHA-256；每轮发送前重建并核验这些值。

HJSON 使用标准 `hjson==3.1.0` 解析器。`request` 为主体，仅在缺失时从顶层补 `messages/tools/model`，冲突或重复字段直接拒绝。附件 `[attachment:路径]` 支持声明路径和同目录同名文件；两处文件内容不同、缺失、空文件、超限、声明的大小/哈希不符都记录原因。没有声明的原始哈希时保存实际哈希，后续运行以 manifest 检测漂移。

客户端不会修改消息正文，也不额外补默认 `stream`。现有网关会将省略的 `stream` 补为 `true`；明确的 `false` 仍走 JSON。probe 拦截或请求校验失败可能导致请求未到达 mock，它们会作为未通过案例保留，即使客户端收到 HTTP 200。

强制覆盖路径现在会将调用方提供的 `safeguards` 放回实际出站请求，统一 `dangerous_tool_use.classifier_context` 的 `platform/live_cwd/home_dir/user_identity` 四项环境值。没有提供时不新增，普通未强制路径保留原行为。其余权限、Git、可信目录和历史上下文原样保留，详见 [字段审查](SAFEGUARDS.md)。新的回放校验包含该规则，历史运行继续按当时保存的预期配置验证。

## 结果与停启验收

每轮目录 `test-results/claude-profile-replay/<run_id>/` 保存：

- 固定清单、通道核验、版本化预期配置、二进制/工具哈希、遥测模式与运行计数快照。
- 每例客户端请求/脱敏头、还原说明、响应及完整状态。
- mock 收到的每次 attempt 原始 JSON、脱敏头（含原始头顺序/大小写）、模拟响应及完成标记；重试不会覆盖。
- 字段结构差异、统一文本 diff、各项验证断言、网关日志、`summary.json` 和 `REPORT.md`。

通过断言覆盖版本化 system/tools、身份/环境、tool_choice、metadata、beta/UA、输出预算、缓存数量、消息顺序/控制字段、工具调用名称/ID/参数和图片。历史文本仍可能被现有环境、记忆路径与缓存净化逻辑修改，详见 diff；通过不等于历史文本逐字一致。JSON/SSE 响应均检查结束状态，HTTP 200 本身不是通过依据。只要有未通过案例，`run`/`verify` 返回退出码 `1`，报告仍完整保留。

```sh
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py isolation-check
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py stop
scripts/claude-profile-replay/.venv/bin/python scripts/claude-profile-replay/replay.py start
```

`isolation-check` 停止 mock，通过原通道发请求，检查 HTTP 失败及运维记录中的唯一本地上游/账号，然后重启同一 mock，确认相同资源再次收到 JSON 请求。`stop` 仅停止 mock，保留网关、资源和此前日志。串行控制器使用文件锁，禁止同时运行两轮；若进程被中断导致 mock 仍处于 armed 状态，在确认旧请求已结束后使用 `disarm` 清除。

客户端发生网络错误或超时后，工具会保存失败、终止本轮并保留 armed 案例，避免网关迟到的重试被关联到下一条。校验按 manifest 逐条计数，未执行的记录同样作为失败报告，不会只统计已完成部分而误报整轮通过。

```sh
scripts/claude-profile-replay/.venv/bin/python -m unittest discover -s scripts/claude-profile-replay -v
cd backend
go test -tags unit ./internal/pkg/claude ./internal/service -run 'TestSynthetic|TestClaudeTelemetry|TestClaudeToken|TestOAuthRefresh|TestAdminServiceGroupZeroRate|TestClaudeCodeProfile|TestOutputBudget' -count=1
```

模拟测试证明本地路由和转换结果，不代表 Anthropic 会接受这些历史请求。完整正文仅保存在本机忽略目录。

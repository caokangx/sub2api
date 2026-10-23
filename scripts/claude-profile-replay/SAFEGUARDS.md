# safeguards 出站与字段审查

当前强制覆盖 OAuth 路径会恢复原生预处理中暂存的调用方 `safeguards`，改写现有四项环境映射后写入实际 `/v1/messages` 请求；模型请求与遥测观察同一份最终值。普通未强制路径不变，没有传入时不主动生成该字段。

本地固定 80 条样本中，15 条含该字段，10 条含历史上下文，共 274 份 `prior_turn_context[].context`。以下是代码和样本的兼容性审查，不是 Anthropic 对该内部结构的公开规范，也不保证上游接受或改变账号风险判断。

| 字段（均位于 classifier_context 下） | 当前实现 | 一致性与语义 |
|---|---|---|
| `platform` | 使用已配置的环境值 | 与 system 环境、出站 OS 头同源 |
| `live_cwd` | 使用配置的 working_directory | 与 system 工作目录同源 |
| `home_dir` | 使用配置的 home_directory | 与环境用户目录同源 |
| `user_identity` | 使用配置的 user_identity | 与配置的用户标识同源 |
| `case_insensitive_paths` | 保留 | 文件系统能力，不能仅凭 darwin/linux 字符串可靠推断；修改会影响路径匹配语义 |
| `git_state.cwd/root/branch/default_branch/status/visibility/error` | 保留 | 真实仓库事实；路径迁移需要显式源目录映射，分支和状态需要真实 Git 快照。只改 cwd 而伪造根目录、clean 状态或远端可见性会制造矛盾 |
| `trusted_directories.primary/additional` 的 path/resolved/source | 保留 | 实际信任边界。必须同时核对解析路径和权限来源，不能为外观一致扩大范围 |
| `trusted_directories.network/block_reads_outside_working_directories` | 保留 | 网络/文件读权限，不能修改为宽松值 |
| `rule_roots.*`、`rules.allow/ask/deny` | 保留 | 规则来源和权限策略；不能替换规则、删除拒绝项或伪造来源。路径映射必须保持规则含义 |
| `permission_mode`、`restricted`、`auto_mode.*`、`classify_all_shell` | 保留 | 实际审批与分类行为，不能冒充其他权限状态 |
| `prior_turn_context[].context` | 整体保留 | 描述历史时点的事实，不能无依据覆盖成当前工作目录或当前分支；tool_use_ids 必须继续关联原工具调用 |
| `artifacts.*` | 保留 | 含目录、viewer_origin、frame_env、特性状态；非单纯机器指纹，重写可能破坏产物引用或功能语义 |
| `artifact_consent_holdback`、`is_remote_mode`、`dropped`、`v` | 保留 | 同意、执行方式、裁剪计数及结构版本，不能推测或伪造 |
| 未识别的 safeguard 类型或扩展字段 | 保留 | 不删除未知协议扩展，不虚构替代值 |

如果要进一步做环境迁移，需以实际执行环境为源，明确 cwd/home/附加目录的映射，连同 Git、规则根目录、信任目录和产物路径一起验证；历史上下文应保持当时的真实含义。现有四字段覆盖并不意味着其余上下文已全部一致。

单元与本地 mock 验证可证明字段在本地链路上实际传出，以及权限、历史、未知扩展保持不变。模拟客户端字段不能证明或保证规避服务商风控。部署后新增的回放报告会检查 safeguards 的四字段映射与其他字段的完整保留，旧捕获文件不会被覆盖。

package claude

// EffectiveCLIVersion 返回全局固定的 Claude CLI 版本号。
// 保留该函数是为了让调用点清楚表达“出站生效版本”，但它不再接受运行期覆盖。
func EffectiveCLIVersion() string {
	return CLIVersion()
}

// DefaultUserAgent 返回 Claude Code CLI 伪装 User-Agent。
// 所有出站路径都必须经本函数取 UA，保证 User-Agent 头与请求体 billing
// attribution 块中的 cc_version 始终源自同一个版本号。
func DefaultUserAgent() string {
	return "claude-cli/" + EffectiveCLIVersion() + " (external, cli)"
}

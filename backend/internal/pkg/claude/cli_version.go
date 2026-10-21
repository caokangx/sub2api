package claude

// CLIVersion 返回对外伪装的 Claude Code CLI 版本号（三段 semver）。
// 该值全局固定，不接受环境变量、数据库设置或运行期同步覆盖。
func CLIVersion() string {
	return CLICurrentVersion
}

package claude

import "testing"

// 伪装身份的自洽性：User-Agent 头里的版本号必须与 CLIVersion() 一致。
// 两者由不同代码路径写入同一个请求，不一致会被上游判为非正版客户端。
func TestDefaultHeadersUserAgentMatchesCLIVersion(t *testing.T) {
	want := "claude-cli/" + CLIVersion() + " (external, cli)"
	if got := DefaultHeaders()["User-Agent"]; got != want {
		t.Fatalf("DefaultHeaders[User-Agent] = %q, want %q", got, want)
	}
}

func TestCLIVersionIsGloballyPinned(t *testing.T) {
	if CLICurrentVersion != "2.1.284" {
		t.Fatalf("CLICurrentVersion = %q, want 2.1.284", CLICurrentVersion)
	}
	if got := CLIVersion(); got != CLICurrentVersion {
		t.Fatalf("CLIVersion() = %q, want fixed pin %q", got, CLICurrentVersion)
	}
	if got := EffectiveCLIVersion(); got != CLICurrentVersion {
		t.Fatalf("EffectiveCLIVersion() = %q, want fixed pin %q", got, CLICurrentVersion)
	}
}

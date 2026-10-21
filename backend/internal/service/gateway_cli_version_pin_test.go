package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestBuildClaudeRequestsUseFixedVersionInHeaderAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, endpoint := range []string{"messages", "count_tokens"} {
		t.Run(endpoint, func(t *testing.T) {
			resetGatewayForwardingSettingsCacheForTest(t)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			body := []byte(`{"model":"claude-haiku-4-5","system":[{"type":"text","text":""}],"messages":[{"role":"user","content":"hello world"}]}`)
			body, err := sjson.SetBytes(
				body,
				"system.0.text",
				"x-anthropic-billing-header: cc_version=9.9.9.abc; cc_entrypoint=cli;",
			)
			require.NoError(t, err)

			svc := &GatewayService{cfg: &config.Config{}}
			account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth}

			var req *http.Request
			var wireBody []byte
			if endpoint == "messages" {
				req, wireBody, err = svc.buildUpstreamRequest(
					context.Background(), c, account, body,
					"test-token", "oauth", "claude-haiku-4-5", false, true,
				)
			} else {
				req, wireBody, err = svc.buildCountTokensRequest(
					context.Background(), c, account, body,
					"test-token", "oauth", "claude-haiku-4-5", true,
				)
			}
			require.NoError(t, err)

			require.Equal(t, claude.DefaultUserAgent(), getHeaderRaw(req.Header, "User-Agent"))
			billingText := gjson.GetBytes(wireBody, "system.0.text").String()
			require.Contains(t, billingText, "cc_version="+claude.CLICurrentVersion+".")
			require.NotContains(t, billingText, "cc_version=9.9.9")
			require.Equal(t, claude.CLICurrentVersion, ExtractCLIVersion(req.Header.Get("User-Agent")))
		})
	}
}

func TestPinClaudeCLIUserAgentVersionPinsHigherAndLowerVersions(t *testing.T) {
	for _, original := range []string{
		"claude-cli/1.0.0 (external, cli)",
		"claude-cli/99.0.0 (external, claude-desktop-3p, agent-sdk/0.3.100)",
	} {
		pinned, changed := pinClaudeCLIUserAgentVersion(original)
		require.True(t, changed)
		require.Equal(t, claude.CLICurrentVersion, ExtractCLIVersion(pinned))
	}

	alreadyPinned := "claude-cli/" + claude.CLICurrentVersion + " (external, cli)"
	pinned, changed := pinClaudeCLIUserAgentVersion(alreadyPinned)
	require.False(t, changed)
	require.Equal(t, alreadyPinned, pinned)

	foreign := "custom-client/9.9.9"
	pinned, changed = pinClaudeCLIUserAgentVersion(foreign)
	require.False(t, changed)
	require.Equal(t, foreign, pinned)
}

func TestAnthropicAPIKeyPassthroughPinsClientVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/9.9.9 (external, cli)")
	body := []byte(`{
		"model":"claude-haiku-4-5",
		"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=9.9.9.abc; cc_entrypoint=cli;"}],
		"messages":[{"role":"user","content":"hello"}]
	}`)
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	svc := &GatewayService{cfg: &config.Config{}}

	messageReq, messageBody, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(
		context.Background(), c, account, body, "test-key",
	)
	require.NoError(t, err)
	require.Equal(t, claude.CLICurrentVersion, ExtractCLIVersion(messageReq.Header.Get("User-Agent")))
	require.Contains(t, string(messageBody), "cc_version="+claude.CLICurrentVersion+".")

	countReq, err := svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(
		context.Background(), c, account, body, "test-key",
	)
	require.NoError(t, err)
	require.Equal(t, claude.CLICurrentVersion, ExtractCLIVersion(countReq.Header.Get("User-Agent")))
	countBody, err := io.ReadAll(countReq.Body)
	require.NoError(t, err)
	require.Contains(t, string(countBody), "cc_version="+claude.CLICurrentVersion+".")
}

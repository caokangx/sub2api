package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// readonlyAdminAccountTypes 只读管理员可见的账号类型，平台固定为 Anthropic。
var readonlyAdminAccountTypes = []string{service.AccountTypeOAuth, service.AccountTypeSetupToken}

// scopeReadonlyAdminAccountFilters 把只读管理员的列表筛选收敛到可见范围。
// 返回 ok=false 表示请求的类型超出可见范围，调用方应直接返回空页。
func scopeReadonlyAdminAccountFilters(accountType string) (platform, scopedType string, ok bool) {
	accountType = strings.TrimSpace(accountType)
	if accountType == "" {
		return service.PlatformAnthropic, strings.Join(readonlyAdminAccountTypes, ","), true
	}
	for _, allowed := range readonlyAdminAccountTypes {
		if accountType == allowed {
			return service.PlatformAnthropic, accountType, true
		}
	}
	return service.PlatformAnthropic, "", false
}

func readonlyAdminCanViewAccount(account *service.Account) bool {
	return account != nil && account.IsAnthropicOAuthOrSetupToken()
}

// redactAccountForReadonlyAdmin 只读管理员不需要代理连接信息，仅保留代理 ID 与名称。
func redactAccountForReadonlyAdmin(account *dto.Account) {
	if account == nil || account.Proxy == nil {
		return
	}
	account.Proxy = &dto.Proxy{ID: account.Proxy.ID, Name: account.Proxy.Name}
}

func isReadonlyAdminRequest(c *gin.Context) bool {
	return middleware2.IsReadonlyAdmin(c)
}

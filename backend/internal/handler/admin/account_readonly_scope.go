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

const readonlyAdminMask = "****"

// readonlyAdminEmailKeys extra/credentials 中需要打码的邮箱字段。
var readonlyAdminEmailKeys = []string{"email_address", "email"}

// readonlyAdminHiddenKeys extra/credentials 中对只读管理员完全隐藏的字段：
// 账号身份标识，以及容量配置（与 dto 上的同名顶层字段同源）。
var readonlyAdminHiddenKeys = []string{
	"org_uuid",
	"account_uuid",
	"window_cost_limit",
	"window_cost_sticky_reserve",
	"max_sessions",
	"session_idle_timeout_minutes",
	"base_rpm",
	"rpm_strategy",
	"rpm_sticky_buffer",
	"quota_limit",
	"quota_used",
	"quota_daily_limit",
	"quota_daily_used",
	"quota_weekly_limit",
	"quota_weekly_used",
}

// redactAccountForReadonlyAdmin 只读管理员看到的账号：账号名称/邮箱、代理名称打码，
// 容量配置与实时容量计数不返回，代理仅保留 ID 与打码后的名称。
func redactAccountForReadonlyAdmin(item *AccountWithConcurrency) {
	if item == nil {
		return
	}
	item.CurrentConcurrency = 0
	item.CurrentWindowCost = nil
	item.ActiveSessions = nil
	item.CurrentRPM = nil

	account := item.Account
	if account == nil {
		return
	}
	account.Name = maskReadonlyIdentity(account.Name)
	account.ParentEmail = maskReadonlyIdentity(account.ParentEmail)
	account.Extra = redactReadonlyAdminFields(account.Extra)
	account.Credentials = redactReadonlyAdminFields(account.Credentials)

	account.Concurrency = 0
	account.LoadFactor = nil
	account.WindowCostLimit = nil
	account.WindowCostStickyReserve = nil
	account.MaxSessions = nil
	account.SessionIdleTimeoutMin = nil
	account.BaseRPM = nil
	account.RPMStrategy = nil
	account.RPMStickyBuffer = nil
	account.QuotaLimit = nil
	account.QuotaUsed = nil
	account.QuotaDailyLimit = nil
	account.QuotaDailyUsed = nil
	account.QuotaWeeklyLimit = nil
	account.QuotaWeeklyUsed = nil

	if account.Proxy != nil {
		account.Proxy = &dto.Proxy{ID: account.Proxy.ID, Name: maskReadonlyIdentity(account.Proxy.Name)}
	}
	if account.ProxyFallbackOriginName != nil {
		masked := maskReadonlyIdentity(*account.ProxyFallbackOriginName)
		account.ProxyFallbackOriginName = &masked
	}
}

// redactReadonlyAdminFields 返回打码后的副本，避免改到共享的原始 map。
func redactReadonlyAdminFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	for _, key := range readonlyAdminHiddenKeys {
		delete(out, key)
	}
	for _, key := range readonlyAdminEmailKeys {
		if value, ok := out[key].(string); ok {
			out[key] = maskReadonlyIdentity(value)
		}
	}
	return out
}

// maskReadonlyIdentity 对账号名、邮箱、代理名打码：邮箱分别处理本地部分与域名（保留顶级域），
// 其余文本仅保留首尾少量字符；固定长度的掩码避免泄露原始长度。
func maskReadonlyIdentity(value string) string {
	value = strings.TrimSpace(value)
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return maskReadonlyText(value)
	}
	local, domain := value[:at], value[at+1:]
	if dot := strings.LastIndex(domain, "."); dot > 0 && dot < len(domain)-1 {
		return maskReadonlyText(local) + "@" + maskReadonlyText(domain[:dot]) + domain[dot:]
	}
	return maskReadonlyText(local) + "@" + maskReadonlyText(domain)
}

func maskReadonlyText(value string) string {
	runes := []rune(value)
	switch n := len(runes); {
	case n == 0:
		return ""
	case n <= 4:
		return string(runes[:1]) + readonlyAdminMask
	case n <= 8:
		return string(runes[:1]) + readonlyAdminMask + string(runes[n-1:])
	default:
		return string(runes[:2]) + readonlyAdminMask + string(runes[n-2:])
	}
}

func isReadonlyAdminRequest(c *gin.Context) bool {
	return middleware2.IsReadonlyAdmin(c)
}

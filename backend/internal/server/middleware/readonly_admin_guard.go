package middleware

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// readonlyAdminAllowedRoutes 只读管理员可访问的后台路由（METHOD + gin FullPath）。
// 默认拒绝：新增后台路由不会自动对只读管理员开放。
var readonlyAdminAllowedRoutes = map[string]struct{}{
	http.MethodGet + " /api/v1/admin/accounts":     {},
	http.MethodGet + " /api/v1/admin/accounts/:id": {},
	http.MethodGet + " /api/v1/admin/groups/all":   {},
}

// IsReadonlyAdmin 判断当前请求主体是否为只读管理员。
func IsReadonlyAdmin(c *gin.Context) bool {
	role, ok := GetUserRoleFromContext(c)
	return ok && role == service.RoleReadonlyAdmin
}

// enforceReadonlyAdminScope 在 adminAuth 认证通过后调用：只读管理员访问白名单外的
// 路由时直接 403。由 adminAuth 内部调用，所有挂 adminAuth 的路由组都受约束。
func enforceReadonlyAdminScope(c *gin.Context) bool {
	if !IsReadonlyAdmin(c) {
		return true
	}
	if _, ok := readonlyAdminAllowedRoutes[c.Request.Method+" "+c.FullPath()]; !ok {
		AbortWithError(c, http.StatusForbidden, "READONLY_ADMIN_FORBIDDEN", "Read-only admin cannot access this resource")
		return false
	}
	return true
}

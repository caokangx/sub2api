package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupReadonlyAccountRouter(role string) (*gin.Engine, *stubAdminService) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUserRole), role)
		c.Next()
	})
	adminSvc := newStubAdminService()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts", handler.List)
	router.GET("/api/v1/admin/accounts/:id", handler.GetByID)
	return router, adminSvc
}

func TestAccountHandlerListReadonlyAdminForcesScope(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		wantType string
	}{
		{name: "no_filters", query: "", wantType: "oauth,setup-token"},
		{name: "platform_override_ignored", query: "&platform=openai", wantType: "oauth,setup-token"},
		{name: "oauth_kept", query: "&type=oauth", wantType: "oauth"},
		{name: "setup_token_kept", query: "&type=setup-token", wantType: "setup-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, adminSvc := setupReadonlyAccountRouter(service.RoleReadonlyAdmin)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20"+tc.query, nil))
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, service.PlatformAnthropic, adminSvc.lastListAccounts.platform)
			require.Equal(t, tc.wantType, adminSvc.lastListAccounts.accountType)
		})
	}
}

func TestAccountHandlerListReadonlyAdminOutOfScopeTypeReturnsEmpty(t *testing.T) {
	router, adminSvc := setupReadonlyAccountRouter(service.RoleReadonlyAdmin)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&type=apikey", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, adminSvc.lastListAccounts.calls, "越界类型不应查询数据库")

	var payload struct {
		Data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Empty(t, payload.Data.Items)
	require.Zero(t, payload.Data.Total)
}

func TestAccountHandlerListAdminFiltersUnchanged(t *testing.T) {
	router, adminSvc := setupReadonlyAccountRouter(service.RoleAdmin)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&platform=openai&type=apikey", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, service.PlatformOpenAI, adminSvc.lastListAccounts.platform)
	require.Equal(t, service.AccountTypeAPIKey, adminSvc.lastListAccounts.accountType)
}

func TestAccountHandlerListReadonlyAdminRedactsProxy(t *testing.T) {
	router, adminSvc := setupReadonlyAccountRouter(service.RoleReadonlyAdmin)
	now := time.Now().UTC()
	proxyID := int64(9)
	adminSvc.accounts = []service.Account{{
		ID: 1, Name: "claude", Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, ProxyID: &proxyID,
		Proxy:     &service.Proxy{ID: proxyID, Name: "hk-proxy", Protocol: "socks5", Host: "10.0.0.1", Port: 1080, Username: "secret-user"},
		CreatedAt: now, UpdatedAt: now,
	}}

	for _, query := range []string{"", "&lite=1"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20"+query, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		require.Contains(t, body, "hk-proxy")
		require.NotContains(t, body, "10.0.0.1")
		require.NotContains(t, body, "secret-user")
	}
}

func TestAccountHandlerGetByIDReadonlyAdminScope(t *testing.T) {
	t.Run("out_of_scope_returns_404", func(t *testing.T) {
		router, adminSvc := setupReadonlyAccountRouter(service.RoleReadonlyAdmin)
		adminSvc.getAccountResult = &service.Account{ID: 5, Name: "openai", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/5", nil))
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("anthropic_apikey_returns_404", func(t *testing.T) {
		router, adminSvc := setupReadonlyAccountRouter(service.RoleReadonlyAdmin)
		adminSvc.getAccountResult = &service.Account{ID: 6, Name: "claude-key", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/6", nil))
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("in_scope_returns_redacted_account", func(t *testing.T) {
		router, adminSvc := setupReadonlyAccountRouter(service.RoleReadonlyAdmin)
		proxyID := int64(3)
		adminSvc.getAccountResult = &service.Account{
			ID: 7, Name: "claude-setup", Platform: service.PlatformAnthropic, Type: service.AccountTypeSetupToken,
			ProxyID: &proxyID, Proxy: &service.Proxy{ID: proxyID, Name: "jp-proxy", Host: "192.168.1.9", Port: 8080},
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/7", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "jp-proxy")
		require.NotContains(t, rec.Body.String(), "192.168.1.9")
	})
}

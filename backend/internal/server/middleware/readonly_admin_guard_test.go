//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminAuthReadonlyAdminScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}
	authService := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)

	users := map[int64]*service.User{
		1: {ID: 1, Email: "admin@example.com", Role: service.RoleAdmin, Status: service.StatusActive, Concurrency: 1},
		2: {ID: 2, Email: "readonly@example.com", Role: service.RoleReadonlyAdmin, Status: service.StatusActive, Concurrency: 1},
		3: {ID: 3, Email: "user@example.com", Role: service.RoleUser, Status: service.StatusActive, Concurrency: 1},
	}
	userRepo := &stubUserRepo{
		getByID: func(ctx context.Context, id int64) (*service.User, error) {
			u, ok := users[id]
			if !ok {
				return nil, service.ErrUserNotFound
			}
			clone := *u
			return &clone, nil
		},
	}
	userService := service.NewUserService(userRepo, nil, nil, nil)

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	admin.Use(gin.HandlerFunc(NewAdminAuthMiddleware(authService, userService, nil, nil)))
	ok := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	admin.GET("/accounts", ok)
	admin.GET("/accounts/:id", ok)
	admin.PUT("/accounts/:id", ok)
	admin.DELETE("/accounts/:id", ok)
	admin.POST("/accounts/usage/batch", ok)
	admin.GET("/accounts/:id/usage", ok)
	admin.GET("/groups/all", ok)
	admin.GET("/users", ok)

	tokenFor := func(t *testing.T, id int64) string {
		u := users[id]
		token, err := authService.GenerateToken(context.Background(), &service.User{ID: u.ID, Email: u.Email, Role: u.Role})
		require.NoError(t, err)
		return token
	}
	do := func(t *testing.T, id int64, method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, id))
		router.ServeHTTP(w, req)
		return w
	}

	t.Run("readonly_admin_allowed_routes", func(t *testing.T) {
		for _, path := range []string{"/api/v1/admin/accounts", "/api/v1/admin/accounts/42", "/api/v1/admin/groups/all"} {
			w := do(t, 2, http.MethodGet, path)
			require.Equal(t, http.StatusOK, w.Code, path)
		}
	})

	t.Run("readonly_admin_denied_routes", func(t *testing.T) {
		cases := []struct{ method, path string }{
			{http.MethodPut, "/api/v1/admin/accounts/42"},
			{http.MethodDelete, "/api/v1/admin/accounts/42"},
			{http.MethodPost, "/api/v1/admin/accounts/usage/batch"},
			{http.MethodGet, "/api/v1/admin/accounts/42/usage"},
			{http.MethodGet, "/api/v1/admin/users"},
		}
		for _, tc := range cases {
			w := do(t, 2, tc.method, tc.path)
			require.Equal(t, http.StatusForbidden, w.Code, tc.method+" "+tc.path)
			require.Contains(t, w.Body.String(), "READONLY_ADMIN_FORBIDDEN")
		}
	})

	t.Run("admin_unaffected", func(t *testing.T) {
		require.Equal(t, http.StatusOK, do(t, 1, http.MethodDelete, "/api/v1/admin/accounts/42").Code)
		require.Equal(t, http.StatusOK, do(t, 1, http.MethodGet, "/api/v1/admin/users").Code)
	})

	t.Run("regular_user_still_forbidden", func(t *testing.T) {
		w := do(t, 3, http.MethodGet, "/api/v1/admin/accounts")
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Contains(t, w.Body.String(), "Admin access required")
	})
}

func TestAdminOnlyRejectsReadonlyAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUserRole), service.RoleReadonlyAdmin)
		c.Next()
	})
	router.Use(AdminOnly())
	router.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))
	require.Equal(t, http.StatusForbidden, w.Code)
}

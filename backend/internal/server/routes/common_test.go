package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeTelemetrySinksAcknowledgeWithoutEchoingPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router)

	for _, path := range []string{
		"/api/event_logging/batch",
		"/api/event_logging/v2/batch",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"events":[{"secret":"must-not-echo"}]}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Empty(t, recorder.Body.String())
		})
	}
}

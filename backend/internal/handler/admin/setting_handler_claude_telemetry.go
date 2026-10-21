package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetClaudeTelemetrySettings returns the sanitized DB-backed telemetry config.
// GET /api/v1/admin/settings/claude-telemetry
func (h *SettingHandler) GetClaudeTelemetrySettings(c *gin.Context) {
	settings, err := h.settingService.GetClaudeTelemetrySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateClaudeTelemetrySettings validates and hot-publishes a new snapshot.
// PUT /api/v1/admin/settings/claude-telemetry
func (h *SettingHandler) UpdateClaudeTelemetrySettings(c *gin.Context) {
	var req service.ClaudeTelemetryRuntimeConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings, err := h.settingService.SaveClaudeTelemetrySettings(c.Request.Context(), &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/gin-gonic/gin"
)

const (
	upstreamVerifyMaxRequestBytes = 64 << 10
	upstreamVerifyModelsTimeout   = 2 * time.Minute
	upstreamVerifyProbeTimeout    = 5 * time.Minute
)

// upstreamVerifyModelsRequest / upstreamVerifyProbeRequest are shared by the
// Azure and Bedrock verify endpoints.
type upstreamVerifyModelsRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
}

type upstreamVerifyProbeRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
	Model   string `json:"model" binding:"required"`
}

func bindUpstreamVerifyRequest(c *gin.Context, req any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, upstreamVerifyMaxRequestBytes)
	if err := c.ShouldBindJSON(req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return false
	}
	return true
}

// prepareUpstreamVerify validates the target and returns a context that refuses
// redirects and, unless private hosts are explicitly allowed, any non-public
// resolved address, because every probe carries a third-party credential.
func prepareUpstreamVerify(c *gin.Context, cfg *config.Config, rawBaseURL, rawAPIKey string, timeout time.Duration) (context.Context, string, string, context.CancelFunc, bool) {
	apiKey := strings.TrimSpace(rawAPIKey)
	if apiKey == "" {
		response.BadRequest(c, "api_key is required")
		return nil, "", "", nil, false
	}
	policy := cfg.Security.URLAllowlist
	baseURL, err := urlvalidator.ValidateHTTPURL(rawBaseURL, policy.AllowInsecureHTTP, urlvalidator.ValidationOptions{
		AllowPrivate: policy.AllowPrivateHosts,
	})
	if err != nil {
		response.BadRequest(c, "Invalid base_url: "+err.Error())
		return nil, "", "", nil, false
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	ctx = service.WithHTTPUpstreamRedirectsDisabled(ctx)
	if !policy.AllowPrivateHosts {
		ctx = service.WithHTTPUpstreamPublicHostsOnly(ctx)
	}
	return ctx, baseURL, apiKey, cancel, true
}

func upstreamVerifyModel(c *gin.Context, raw string) (string, bool) {
	model := strings.TrimSpace(raw)
	if model == "" {
		response.BadRequest(c, "model is required")
		return "", false
	}
	return model, true
}

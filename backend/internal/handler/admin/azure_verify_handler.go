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
	azureVerifyMaxRequestBytes = 64 << 10
	azureVerifyModelsTimeout   = 2 * time.Minute
	azureVerifyProbeTimeout    = 5 * time.Minute
)

// AzureVerifyHandler checks whether an admin-supplied OpenAI-compatible relay is
// really backed by Azure OpenAI by probing it from the server side.
type AzureVerifyHandler struct {
	cfg     *config.Config
	service *service.AzureVerifyService
}

func NewAzureVerifyHandler(cfg *config.Config, upstream service.HTTPUpstream) *AzureVerifyHandler {
	return &AzureVerifyHandler{cfg: cfg, service: service.NewAzureVerifyService(upstream)}
}

type AzureVerifyModelsRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
}

type AzureVerifyProbeRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
	Model   string `json:"model" binding:"required"`
}

// Models POST /api/v1/admin/azure-verify/models
func (h *AzureVerifyHandler) Models(c *gin.Context) {
	var req AzureVerifyModelsRequest
	if !h.bind(c, &req) {
		return
	}
	ctx, baseURL, apiKey, cancel, ok := h.prepare(c, req.BaseURL, req.APIKey, azureVerifyModelsTimeout)
	if !ok {
		return
	}
	defer cancel()
	response.Success(c, h.service.ListModels(ctx, baseURL, apiKey))
}

// Probe POST /api/v1/admin/azure-verify/probe
func (h *AzureVerifyHandler) Probe(c *gin.Context) {
	var req AzureVerifyProbeRequest
	if !h.bind(c, &req) {
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		response.BadRequest(c, "model is required")
		return
	}
	ctx, baseURL, apiKey, cancel, ok := h.prepare(c, req.BaseURL, req.APIKey, azureVerifyProbeTimeout)
	if !ok {
		return
	}
	defer cancel()
	response.Success(c, h.service.ProbeModel(ctx, baseURL, apiKey, model))
}

func (h *AzureVerifyHandler) bind(c *gin.Context, req any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, azureVerifyMaxRequestBytes)
	if err := c.ShouldBindJSON(req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return false
	}
	return true
}

// prepare validates the target and returns a context that refuses redirects and,
// unless private hosts are explicitly allowed, any non-public resolved address,
// because every probe carries a third-party credential.
func (h *AzureVerifyHandler) prepare(c *gin.Context, rawBaseURL, rawAPIKey string, timeout time.Duration) (context.Context, string, string, context.CancelFunc, bool) {
	apiKey := strings.TrimSpace(rawAPIKey)
	if apiKey == "" {
		response.BadRequest(c, "api_key is required")
		return nil, "", "", nil, false
	}
	policy := h.cfg.Security.URLAllowlist
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

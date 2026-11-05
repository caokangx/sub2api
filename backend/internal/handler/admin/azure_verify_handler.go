package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
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

type AzureVerifyModelsRequest = upstreamVerifyModelsRequest

type AzureVerifyProbeRequest = upstreamVerifyProbeRequest

// Models POST /api/v1/admin/azure-verify/models
func (h *AzureVerifyHandler) Models(c *gin.Context) {
	var req AzureVerifyModelsRequest
	if !bindUpstreamVerifyRequest(c, &req) {
		return
	}
	ctx, baseURL, apiKey, cancel, ok := prepareUpstreamVerify(c, h.cfg, req.BaseURL, req.APIKey, upstreamVerifyModelsTimeout)
	if !ok {
		return
	}
	defer cancel()
	response.Success(c, h.service.ListModels(ctx, baseURL, apiKey))
}

// Probe POST /api/v1/admin/azure-verify/probe
func (h *AzureVerifyHandler) Probe(c *gin.Context) {
	var req AzureVerifyProbeRequest
	if !bindUpstreamVerifyRequest(c, &req) {
		return
	}
	model, ok := upstreamVerifyModel(c, req.Model)
	if !ok {
		return
	}
	ctx, baseURL, apiKey, cancel, ok := prepareUpstreamVerify(c, h.cfg, req.BaseURL, req.APIKey, upstreamVerifyProbeTimeout)
	if !ok {
		return
	}
	defer cancel()
	response.Success(c, h.service.ProbeModel(ctx, baseURL, apiKey, model))
}

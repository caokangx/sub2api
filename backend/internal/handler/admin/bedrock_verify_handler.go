package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BedrockVerifyHandler checks whether an admin-supplied Anthropic-compatible relay
// is really backed by AWS Bedrock by probing it from the server side.
type BedrockVerifyHandler struct {
	cfg     *config.Config
	service *service.BedrockVerifyService
}

func NewBedrockVerifyHandler(cfg *config.Config, upstream service.HTTPUpstream) *BedrockVerifyHandler {
	return &BedrockVerifyHandler{cfg: cfg, service: service.NewBedrockVerifyService(upstream)}
}

// Models POST /api/v1/admin/bedrock-verify/models
func (h *BedrockVerifyHandler) Models(c *gin.Context) {
	var req upstreamVerifyModelsRequest
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

// Probe POST /api/v1/admin/bedrock-verify/probe
func (h *BedrockVerifyHandler) Probe(c *gin.Context) {
	var req upstreamVerifyProbeRequest
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

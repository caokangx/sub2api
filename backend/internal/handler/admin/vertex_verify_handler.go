package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// VertexVerifyHandler checks whether an admin-supplied Gemini-compatible relay is
// really backed by Google Vertex AI by probing it from the server side.
type VertexVerifyHandler struct {
	cfg     *config.Config
	service *service.VertexVerifyService
}

func NewVertexVerifyHandler(cfg *config.Config, upstream service.HTTPUpstream) *VertexVerifyHandler {
	return &VertexVerifyHandler{cfg: cfg, service: service.NewVertexVerifyService(upstream)}
}

// Models POST /api/v1/admin/vertex-verify/models
func (h *VertexVerifyHandler) Models(c *gin.Context) {
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

// Probe POST /api/v1/admin/vertex-verify/probe
func (h *VertexVerifyHandler) Probe(c *gin.Context) {
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

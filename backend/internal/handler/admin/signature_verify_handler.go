package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	// Signatures grow with the amount of hidden thinking (tens of KB for long traces).
	signatureVerifyMaxRequestBytes  = 4 << 20
	signatureVerifyMaxResponseBytes = 16 << 20
	// A high-effort harvest can think for several minutes before responding.
	signatureVerifyTimeout          = 15 * time.Minute
	signatureVerifyAnthropicVersion = "2023-06-01"
)

// SignatureVerifyHandler forwards the signature genuineness check (harvest/replay)
// from the admin UI to an admin-supplied Anthropic-compatible gateway, so the
// check also works against gateways that do not allow browser CORS.
type SignatureVerifyHandler struct {
	cfg      *config.Config
	upstream service.HTTPUpstream
}

func NewSignatureVerifyHandler(cfg *config.Config, upstream service.HTTPUpstream) *SignatureVerifyHandler {
	return &SignatureVerifyHandler{cfg: cfg, upstream: upstream}
}

type SignatureVerifyMessagesRequest struct {
	BaseURL string          `json:"base_url" binding:"required"`
	APIKey  string          `json:"api_key" binding:"required"`
	Body    json.RawMessage `json:"body" binding:"required"`
}

// SignatureVerifyMessagesResponse wraps the upstream reply. The upstream status is
// reported in the payload instead of the HTTP status so that, e.g., an upstream 401
// for a bad gateway key is not mistaken for an expired admin session.
type SignatureVerifyMessagesResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
	Text   string          `json:"text,omitempty"`
}

// Messages POST /api/v1/admin/signature-verify/messages
func (h *SignatureVerifyHandler) Messages(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, signatureVerifyMaxRequestBytes)
	var req SignatureVerifyMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil || body == nil {
		response.BadRequest(c, "body must be a JSON object")
		return
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		response.BadRequest(c, "api_key is required")
		return
	}

	policy := h.cfg.Security.URLAllowlist
	baseURL, err := urlvalidator.ValidateHTTPURL(req.BaseURL, policy.AllowInsecureHTTP, urlvalidator.ValidationOptions{
		AllowPrivate: policy.AllowPrivateHosts,
	})
	if err != nil {
		response.BadRequest(c, "Invalid base_url: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), signatureVerifyTimeout)
	defer cancel()
	// The request carries a third-party credential: never follow redirects, and unless
	// private hosts are explicitly allowed, require every resolved address to be public.
	ctx = service.WithHTTPUpstreamRedirectsDisabled(ctx)
	if !policy.AllowPrivateHosts {
		ctx = service.WithHTTPUpstreamPublicHostsOnly(ctx)
	}

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(req.Body))
	if err != nil {
		response.BadRequest(c, "Invalid base_url: "+err.Error())
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("x-api-key", apiKey)
	upstreamReq.Header.Set("anthropic-version", signatureVerifyAnthropicVersion)

	resp, err := h.upstream.Do(upstreamReq, "", 0, 1)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			response.Error(c, http.StatusGatewayTimeout, "upstream request timed out")
			return
		}
		response.Error(c, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, signatureVerifyMaxResponseBytes+1))
	if err != nil {
		response.Error(c, http.StatusBadGateway, "failed to read upstream response: "+err.Error())
		return
	}
	if len(raw) > signatureVerifyMaxResponseBytes {
		response.Error(c, http.StatusBadGateway, "upstream response too large")
		return
	}

	out := SignatureVerifyMessagesResponse{Status: resp.StatusCode}
	if json.Valid(raw) {
		out.Body = raw
	} else {
		text := string(raw)
		if len(text) > 2000 {
			text = text[:2000]
		}
		out.Text = text
	}
	response.Success(c, out)
}

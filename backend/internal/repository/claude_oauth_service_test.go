package repository

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ClaudeOAuthServiceSuite struct {
	suite.Suite
	client *claudeOAuthService
}

// requestCapture holds captured request data for assertions in the main goroutine.
type requestCapture struct {
	path        string
	method      string
	cookies     []*http.Cookie
	body        []byte
	bodyJSON    map[string]any
	contentType string
	userAgent   string
}

func newTestReqClient(rt http.RoundTripper) *req.Client {
	c := req.C()
	c.GetClient().Transport = rt
	return c
}

func (s *ClaudeOAuthServiceSuite) TestGetOrganizationUUID() {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantErr    bool
		errContain string
		wantUUID   string
		validate   func(captured requestCapture)
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"uuid":"org-1"}]`))
			},
			wantUUID: "org-1",
			validate: func(captured requestCapture) {
				require.Equal(s.T(), "/api/organizations", captured.path, "unexpected path")
				require.Len(s.T(), captured.cookies, 1, "expected 1 cookie")
				require.Equal(s.T(), "sessionKey", captured.cookies[0].Name)
				require.Equal(s.T(), "sess", captured.cookies[0].Value)
			},
		},
		{
			name: "non_200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("unauthorized"))
			},
			wantErr:    true,
			errContain: "401",
		},
		{
			name: "invalid_json_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("not-json"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.path = r.URL.Path
				captured.cookies = r.Cookies()
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.baseURL = "http://in-process"
			s.client.clientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			got, err := s.client.GetOrganizationUUID(context.Background(), "sess", "")

			if tt.wantErr {
				require.Error(s.T(), err)
				if tt.errContain != "" {
					require.ErrorContains(s.T(), err, tt.errContain)
				}
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantUUID, got)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func (s *ClaudeOAuthServiceSuite) TestGetAuthorizationCode() {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		wantErr  bool
		wantCode string
		validate func(captured requestCapture)
	}{
		{
			name: "parses_redirect_uri",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"redirect_uri": oauth.RedirectURI + "?code=AUTH&state=STATE",
				})
			},
			wantCode: "AUTH#STATE",
			validate: func(captured requestCapture) {
				require.True(s.T(), strings.HasPrefix(captured.path, "/v1/oauth/") && strings.HasSuffix(captured.path, "/authorize"), "unexpected path: %s", captured.path)
				require.Equal(s.T(), http.MethodPost, captured.method, "expected POST")
				require.Len(s.T(), captured.cookies, 1, "expected 1 cookie")
				require.Equal(s.T(), "sess", captured.cookies[0].Value)
				require.Equal(s.T(), "org-1", captured.bodyJSON["organization_uuid"])
				require.Equal(s.T(), oauth.ClientID, captured.bodyJSON["client_id"])
				require.Equal(s.T(), oauth.RedirectURI, captured.bodyJSON["redirect_uri"])
				require.Equal(s.T(), "st", captured.bodyJSON["state"])
			},
		},
		{
			name: "missing_code_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"redirect_uri": oauth.RedirectURI + "?state=STATE", // no code
				})
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.path = r.URL.Path
				captured.method = r.Method
				captured.cookies = r.Cookies()
				captured.body, _ = io.ReadAll(r.Body)
				_ = json.Unmarshal(captured.body, &captured.bodyJSON)
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.baseURL = "http://in-process"
			s.client.clientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			code, err := s.client.GetAuthorizationCode(context.Background(), "sess", "org-1", oauth.ScopeInference, "cc", "st", "")

			if tt.wantErr {
				require.Error(s.T(), err)
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantCode, code)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func (s *ClaudeOAuthServiceSuite) TestExchangeCodeForToken() {
	tests := []struct {
		name         string
		handler      http.HandlerFunc
		code         string
		state        string
		isSetupToken bool
		wantErr      bool
		wantResp     *oauth.TokenResponse
		validate     func(captured requestCapture)
	}{
		{
			name: "sends_state_when_embedded",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken:  "at",
					TokenType:    "bearer",
					ExpiresIn:    3600,
					RefreshToken: "rt",
					Scope:        "s",
				})
			},
			code:         "AUTH#STATE2",
			isSetupToken: false,
			wantResp: &oauth.TokenResponse{
				AccessToken:  "at",
				RefreshToken: "rt",
			},
			validate: func(captured requestCapture) {
				require.Equal(s.T(), http.MethodPost, captured.method, "expected POST")
				require.True(s.T(), strings.HasPrefix(captured.contentType, "application/json"), "unexpected content-type")
				require.Equal(s.T(), "AUTH", captured.bodyJSON["code"])
				require.Equal(s.T(), "STATE2", captured.bodyJSON["state"])
				require.Equal(s.T(), oauth.ClientID, captured.bodyJSON["client_id"])
				require.Equal(s.T(), oauth.RedirectURI, captured.bodyJSON["redirect_uri"])
				require.Equal(s.T(), "ver", captured.bodyJSON["code_verifier"])
				require.Equal(s.T(), oauth.TokenRequestUserAgent, captured.userAgent)
				// Regular OAuth should not include expires_in
				require.Nil(s.T(), captured.bodyJSON["expires_in"], "regular OAuth should not include expires_in")
			},
		},
		{
			name: "setup_token_requests_one_year_expiry",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken: "at",
					TokenType:   "bearer",
					ExpiresIn:   31536000,
				})
			},
			code:         "AUTH#STATE3",
			isSetupToken: true,
			wantResp: &oauth.TokenResponse{
				AccessToken: "at",
			},
			validate: func(captured requestCapture) {
				require.Equal(s.T(), float64(oauth.SetupTokenExpiresIn), captured.bodyJSON["expires_in"])
				require.Equal(s.T(),
					`{"grant_type":"authorization_code","code":"AUTH","redirect_uri":"`+oauth.RedirectURI+`","client_id":"`+oauth.ClientID+`","code_verifier":"ver","state":"STATE3","expires_in":31536000}`,
					string(captured.body), "body must match Claude Code's field order")
			},
		},
		{
			name: "falls_back_to_session_state",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{AccessToken: "at"})
			},
			code:         "AUTH",
			state:        "SESSION_STATE",
			isSetupToken: false,
			wantResp:     &oauth.TokenResponse{AccessToken: "at"},
			validate: func(captured requestCapture) {
				require.Equal(s.T(), "SESSION_STATE", captured.bodyJSON["state"])
			},
		},
		{
			name: "non_200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("bad request"))
			},
			code:         "AUTH",
			isSetupToken: false,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.method = r.Method
				captured.contentType = r.Header.Get("Content-Type")
				captured.userAgent = r.Header.Get("User-Agent")
				captured.body, _ = io.ReadAll(r.Body)
				_ = json.Unmarshal(captured.body, &captured.bodyJSON)
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.tokenURL = "http://in-process/token"
			s.client.tokenClientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			resp, err := s.client.ExchangeCodeForToken(context.Background(), tt.code, "ver", tt.state, "", tt.isSetupToken)

			if tt.wantErr {
				require.Error(s.T(), err)
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantResp.AccessToken, resp.AccessToken)
			require.Equal(s.T(), tt.wantResp.RefreshToken, resp.RefreshToken)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func (s *ClaudeOAuthServiceSuite) TestRefreshToken() {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		wantErr  bool
		wantResp *oauth.TokenResponse
		validate func(captured requestCapture)
	}{
		{
			name: "sends_json_format",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken:  "new_access_token",
					TokenType:    "bearer",
					ExpiresIn:    28800,
					RefreshToken: "new_refresh_token",
					Scope:        "user:profile user:inference",
				})
			},
			wantResp: &oauth.TokenResponse{
				AccessToken:  "new_access_token",
				RefreshToken: "new_refresh_token",
			},
			validate: func(captured requestCapture) {
				require.Equal(s.T(), http.MethodPost, captured.method, "expected POST")
				// 验证使用 JSON 格式（不是 form 格式）
				require.True(s.T(), strings.HasPrefix(captured.contentType, "application/json"),
					"expected JSON content-type, got: %s", captured.contentType)
				// 验证 JSON body 内容
				require.Equal(s.T(), "refresh_token", captured.bodyJSON["grant_type"])
				require.Equal(s.T(), "rt", captured.bodyJSON["refresh_token"])
				require.Equal(s.T(), oauth.ClientID, captured.bodyJSON["client_id"])
			},
		},
		{
			name: "returns_new_refresh_token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken:  "at",
					TokenType:    "bearer",
					ExpiresIn:    28800,
					RefreshToken: "rotated_rt", // Anthropic rotates refresh tokens
				})
			},
			wantResp: &oauth.TokenResponse{
				AccessToken:  "at",
				RefreshToken: "rotated_rt",
			},
		},
		{
			name: "non_200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.method = r.Method
				captured.contentType = r.Header.Get("Content-Type")
				captured.body, _ = io.ReadAll(r.Body)
				_ = json.Unmarshal(captured.body, &captured.bodyJSON)
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.tokenURL = "http://in-process/token"
			s.client.tokenClientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			resp, err := s.client.RefreshToken(context.Background(), "rt", "")

			if tt.wantErr {
				require.Error(s.T(), err)
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantResp.AccessToken, resp.AccessToken)
			require.Equal(s.T(), tt.wantResp.RefreshToken, resp.RefreshToken)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func (s *ClaudeOAuthServiceSuite) TestFetchOAuthProfile() {
	var captured *http.Request
	rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"permission_error","details":{"error_code":"oauth_scope_insufficient"}}}`))
	}), nil)

	client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
	require.True(s.T(), ok, "type assertion failed")
	require.Equal(s.T(), oauth.ProfileURL, client.profileURL)
	client.tokenClientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

	status, err := client.FetchOAuthProfile(context.Background(), "sk-ant-oat01-test", "")
	require.NoError(s.T(), err, "a 403 is the expected answer for setup-token scope, not a failure")
	require.Equal(s.T(), http.StatusForbidden, status)
	require.Equal(s.T(), http.MethodGet, captured.Method)
	require.Equal(s.T(), "/api/oauth/profile", captured.URL.Path)
	require.Equal(s.T(), "Bearer sk-ant-oat01-test", captured.Header.Get("Authorization"))
	require.Equal(s.T(), "application/json", captured.Header.Get("Content-Type"))
	require.Equal(s.T(), "no-cache", captured.Header.Get("Cache-Control"))
	require.Equal(s.T(), oauth.TokenRequestUserAgent, captured.Header.Get("User-Agent"))
}

// The token endpoint is reached by axios in Claude Code, so the real client must
// speak HTTP/1.1 with axios headers and none of the Chrome impersonation headers.
func TestCreateTokenReqClientSendsAxiosRequest(t *testing.T) {
	var proto int
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto = r.ProtoMajor
		headers = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_ = json.NewEncoder(gz).Encode(oauth.TokenResponse{AccessToken: "at", ExpiresIn: 31536000})
		_ = gz.Close()
	}))
	defer server.Close()

	client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
	require.True(t, ok)
	client.tokenURL = server.URL

	resp, err := client.ExchangeCodeForToken(context.Background(), "AUTH#STATE", "ver", "", "", true)
	require.NoError(t, err)
	require.Equal(t, "at", resp.AccessToken, "gzip response must still decode with an explicit Accept-Encoding")
	require.Equal(t, 1, proto)
	require.Equal(t, oauth.TokenRequestUserAgent, headers.Get("User-Agent"))
	require.Equal(t, "application/json, text/plain, */*", headers.Get("Accept"))
	require.Equal(t, oauth.TokenRequestAcceptEncoding, headers.Get("Accept-Encoding"))
	for name := range headers {
		lower := strings.ToLower(name)
		require.False(t, strings.HasPrefix(lower, "sec-"), "unexpected browser header %s", name)
		require.NotContains(t, []string{"pragma", "upgrade-insecure-requests", "accept-language", "cache-control"}, lower)
	}
}

func TestClaudeOAuthServiceSuite(t *testing.T) {
	suite.Run(t, new(ClaudeOAuthServiceSuite))
}

//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamVerifyEndpoint(t *testing.T) {
	require.Equal(t, "https://a.example.com/v1/models", upstreamVerifyEndpoint("https://a.example.com", "/v1/models"))
	require.Equal(t, "https://a.example.com/v1/models", upstreamVerifyEndpoint("https://a.example.com/", "/v1/models"))
	require.Equal(t, "https://a.example.com/v1/models", upstreamVerifyEndpoint("https://a.example.com/v1/", "/v1/models"))
	require.Equal(t, "https://a.example.com/api/v1/models", upstreamVerifyEndpoint("https://a.example.com/api", "/v1/models"))
}

func TestUpstreamVerifyEvidenceListCapsValues(t *testing.T) {
	var l upstreamVerifyEvidenceList
	l.add(UpstreamVerifyEvidenceInfo, "short", "abc")
	l.add(UpstreamVerifyEvidenceInfo, "long", strings.Repeat("x", upstreamVerifyMaxEvidenceChars+50))
	require.Equal(t, "abc", l[0].Value)
	require.Equal(t, upstreamVerifyMaxEvidenceChars+1, len([]rune(l[1].Value)))
}

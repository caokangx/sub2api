package service

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeCCHKnownVector(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5-5","max_tokens":128000,"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.284.abc; cc_entrypoint=sdk-ts; cch=00000;"}],"messages":[{"role":"user","content":"hello"}]}`)
	got, err := attestClaudeCCH(body)
	require.NoError(t, err)
	require.Contains(t, string(got), "cch=b1163")
	require.NotContains(t, string(got), "cch=00000")
}

func TestClaudeCCHSelfConsistencyAndExcludedFields(t *testing.T) {
	base := []byte(`{"model":"model-a","max_tokens":100,"fallbacks":["x"],"fallback_credit_token":"secret","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.284.abc; cc_entrypoint=sdk-ts; cch=00000;"}],"messages":[{"role":"user","content":"hello"}]}`)
	changed := []byte(`{"model":"model-b","max_tokens":999,"fallbacks":["y","z"],"fallback_credit_token":"other","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.284.abc; cc_entrypoint=sdk-ts; cch=00000;"}],"messages":[{"role":"user","content":"hello"}]}`)
	first, err := attestClaudeCCH(base)
	require.NoError(t, err)
	second, err := attestClaudeCCH(changed)
	require.NoError(t, err)
	pattern := regexp.MustCompile(`cch=([0-9a-f]{5})`)
	require.Equal(t, pattern.FindSubmatch(first)[1], pattern.FindSubmatch(second)[1],
		"native routing fields and model values are excluded from cch")
}

func TestClaudeCCHRejectsBillingWithoutPlaceholder(t *testing.T) {
	_, err := attestClaudeCCH([]byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.284.abc; cc_entrypoint=sdk-ts; cch=abcde;"}]}`))
	require.Error(t, err)
}

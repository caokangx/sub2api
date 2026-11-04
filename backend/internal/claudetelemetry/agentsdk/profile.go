// Package agentsdk contains the telemetry identity used by the TypeScript
// Claude Agent SDK. Event generation remains in the shared telemetry core so
// OAuth CLI and setup-token SDK traffic use the same batching and delivery
// guarantees, while their wire identities and SDK-only events stay distinct.
package agentsdk

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/claudetelemetry"
)

const (
	Version    = "0.3.310"
	Entrypoint = "sdk-ts"
	ClientType = "sdk-typescript"
)

func Apply(context claudetelemetry.EventContext) claudetelemetry.EventContext {
	context.UserType = "external"
	context.Entrypoint = Entrypoint
	context.ClientType = ClientType
	context.AgentSDKVersion = Version
	context.OmitAuth = true
	interactive := false
	context.Interactive = &interactive
	flag := false
	context.Environment.Terminal = "cursor"
	context.Environment.PackageManagers = "npm"
	context.Environment.IsCI = &flag
	context.Environment.IsClaubbit = &flag
	context.Environment.IsGitHubAction = &flag
	context.Environment.IsClaudeCodeAction = &flag
	context.Environment.IsClaudeCodeRemote = &flag
	context.Environment.IsLocalAgentMode = &flag
	context.Environment.IsConductor = &flag
	context.Environment.IsClaudeAIAuth = &flag
	if context.Environment.DeploymentEnvironment == "" && context.Environment.Platform != "" {
		context.Environment.DeploymentEnvironment = "unknown-" + context.Environment.Platform
	}
	return context
}

// Integration owns an SDK-specific session runtime while sharing the exporter
// with the OAuth CLI integration. This keeps request ordering, prompt indexes,
// and startup events isolated between the two wire profiles.
type Integration struct {
	core *claudetelemetry.Integration
}

func NewIntegration(config claudetelemetry.Config, exporter *claudetelemetry.TelemetryExporter) (*Integration, error) {
	core, err := claudetelemetry.NewIntegration(config, exporter)
	if err != nil {
		return nil, err
	}
	return &Integration{core: core}, nil
}

func (i *Integration) Begin(ctx context.Context, input claudetelemetry.BeginInput) (*claudetelemetry.Request, error) {
	input.EventContext = Apply(input.EventContext)
	return i.core.Begin(ctx, input)
}

func (i *Integration) Core() *claudetelemetry.Integration {
	return i.core
}

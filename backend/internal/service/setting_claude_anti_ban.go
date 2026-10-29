package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var windowsAbsoluteEnvironmentPath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

const (
	ClaudeEnvironmentModeOff     = "off"
	ClaudeEnvironmentModeObserve = "observe"
	ClaudeEnvironmentModeRewrite = "rewrite"

	ClaudeEnvironmentUnmatchedPass   = "pass"
	ClaudeEnvironmentUnmatchedReject = "reject"
)

type ClaudeEnvironmentProfile struct {
	WorkingDirectory             string   `json:"working_directory"`
	GitRepository                bool     `json:"git_repository"`
	Platform                     string   `json:"platform"`
	Shell                        string   `json:"shell"`
	OSVersion                    string   `json:"os_version"`
	AdditionalWorkingDirectories []string `json:"additional_working_directories"`
	MemoryDirectory              string   `json:"memory_directory"`
	GitUser                      string   `json:"git_user"`
	CurrentBranch                string   `json:"current_branch"`
	MainBranch                   string   `json:"main_branch"`
	GitStatus                    string   `json:"git_status"`
	RecentCommits                []string `json:"recent_commits"`
	HomeDirectory                string   `json:"home_directory,omitempty"`
	UserIdentity                 string   `json:"user_identity"`
	Arch                         string   `json:"arch"`
	Runtime                      string   `json:"runtime"`
	RuntimeVersion               string   `json:"runtime_version"`
	PackageVersion               string   `json:"package_version"`
}

type ClaudeEnvironmentRewriteSettings struct {
	Mode        string                   `json:"mode"`
	OnUnmatched string                   `json:"on_unmatched"`
	Profile     ClaudeEnvironmentProfile `json:"profile"`
}

// ClaudeThinkingStripSettings 控制转发前删除历史 thinking 块。
// ForceStripPercent 是按会话抽签的比例：即使签名全部合法，
// 也有该比例的会话被整体删除 thinking。
type ClaudeThinkingStripSettings struct {
	Enabled           bool `json:"enabled"`
	ForceStripPercent int  `json:"force_strip_percent"`
}

type ClaudeAntiBanSettings struct {
	ProbeInterceptionEnabled bool                             `json:"probe_interception_enabled"`
	Environment              ClaudeEnvironmentRewriteSettings `json:"environment"`
	ThinkingStrip            ClaudeThinkingStripSettings      `json:"thinking_strip"`
}

func DefaultClaudeAntiBanSettings() *ClaudeAntiBanSettings {
	return &ClaudeAntiBanSettings{
		ProbeInterceptionEnabled: true,
		Environment: ClaudeEnvironmentRewriteSettings{
			Mode:        ClaudeEnvironmentModeRewrite,
			OnUnmatched: ClaudeEnvironmentUnmatchedPass,
			Profile: ClaudeEnvironmentProfile{
				WorkingDirectory:             "/Users/developer/project",
				GitRepository:                true,
				Platform:                     "darwin",
				Shell:                        "zsh",
				OSVersion:                    "Darwin 24.5.0",
				AdditionalWorkingDirectories: []string{},
				MemoryDirectory:              "/Users/developer/.claude/memory/",
				GitUser:                      "developer",
				CurrentBranch:                "main",
				MainBranch:                   "main",
				GitStatus:                    "clean",
				RecentCommits:                []string{},
				HomeDirectory:                "/Users/developer",
				UserIdentity:                 "developer",
				Arch:                         "arm64",
				Runtime:                      "node",
				RuntimeVersion:               "v26.3.0",
				PackageVersion:               "0.112.1",
			},
		},
	}
}

func cloneClaudeAntiBanSettings(in *ClaudeAntiBanSettings) *ClaudeAntiBanSettings {
	if in == nil {
		return DefaultClaudeAntiBanSettings()
	}
	out := *in
	out.Environment.Profile.AdditionalWorkingDirectories = append(
		[]string{}, in.Environment.Profile.AdditionalWorkingDirectories...,
	)
	out.Environment.Profile.RecentCommits = append([]string{}, in.Environment.Profile.RecentCommits...)
	return &out
}

func normalizeClaudeAntiBanSettings(settings *ClaudeAntiBanSettings) {
	settings.Environment.Mode = strings.ToLower(strings.TrimSpace(settings.Environment.Mode))
	settings.Environment.OnUnmatched = strings.ToLower(strings.TrimSpace(settings.Environment.OnUnmatched))
	profile := &settings.Environment.Profile
	profile.WorkingDirectory = strings.TrimSpace(profile.WorkingDirectory)
	profile.Platform = strings.ToLower(strings.TrimSpace(profile.Platform))
	profile.Shell = strings.ToLower(strings.TrimSpace(profile.Shell))
	profile.OSVersion = strings.TrimSpace(profile.OSVersion)
	profile.MemoryDirectory = strings.TrimSpace(profile.MemoryDirectory)
	profile.GitUser = strings.TrimSpace(profile.GitUser)
	profile.CurrentBranch = strings.TrimSpace(profile.CurrentBranch)
	profile.MainBranch = strings.TrimSpace(profile.MainBranch)
	profile.GitStatus = strings.ToLower(strings.TrimSpace(profile.GitStatus))
	profile.HomeDirectory = strings.TrimSpace(profile.HomeDirectory)
	profile.UserIdentity = strings.TrimSpace(profile.UserIdentity)
	profile.Arch = strings.TrimSpace(profile.Arch)
	profile.Runtime = strings.TrimSpace(profile.Runtime)
	profile.RuntimeVersion = strings.TrimSpace(profile.RuntimeVersion)
	profile.PackageVersion = strings.TrimSpace(profile.PackageVersion)
	if profile.AdditionalWorkingDirectories == nil {
		profile.AdditionalWorkingDirectories = []string{}
	}
	if profile.RecentCommits == nil {
		profile.RecentCommits = []string{}
	}
}

func validateClaudeAntiBanSettings(settings *ClaudeAntiBanSettings) error {
	if settings == nil {
		return errors.New("settings cannot be nil")
	}
	switch settings.Environment.Mode {
	case ClaudeEnvironmentModeOff, ClaudeEnvironmentModeObserve, ClaudeEnvironmentModeRewrite:
	default:
		return errors.New("environment.mode must be one of off, observe, rewrite")
	}
	switch settings.Environment.OnUnmatched {
	case ClaudeEnvironmentUnmatchedPass, ClaudeEnvironmentUnmatchedReject:
	default:
		return errors.New("environment.on_unmatched must be one of pass, reject")
	}
	profile := settings.Environment.Profile
	for name, value := range map[string]string{
		"working_directory": profile.WorkingDirectory,
		"platform":          profile.Platform,
		"shell":             profile.Shell,
		"os_version":        profile.OSVersion,
		"memory_directory":  profile.MemoryDirectory,
		"git_user":          profile.GitUser,
		"current_branch":    profile.CurrentBranch,
		"main_branch":       profile.MainBranch,
		"user_identity":     profile.UserIdentity,
		"arch":              profile.Arch,
		"runtime":           profile.Runtime,
		"runtime_version":   profile.RuntimeVersion,
		"package_version":   profile.PackageVersion,
	} {
		if value == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("environment.profile.%s is invalid", name)
		}
	}
	if !isAbsoluteEnvironmentPath(profile.WorkingDirectory) || !isAbsoluteEnvironmentPath(profile.MemoryDirectory) {
		return errors.New("environment profile directories must be absolute")
	}
	if profile.HomeDirectory != "" && !isAbsoluteEnvironmentPath(profile.HomeDirectory) {
		return errors.New("environment.profile.home_directory must be absolute")
	}
	switch profile.Platform {
	case "darwin", "linux", "win32":
	default:
		return errors.New("environment.profile.platform must be darwin, linux, or win32")
	}
	switch profile.Shell {
	case "bash", "zsh", "fish", "powershell", "cmd":
	default:
		return errors.New("environment.profile.shell is invalid")
	}
	switch profile.GitStatus {
	case "clean", "unavailable":
	default:
		return errors.New("environment.profile.git_status must be clean or unavailable")
	}
	if settings.ThinkingStrip.ForceStripPercent < 0 || settings.ThinkingStrip.ForceStripPercent > 100 {
		return errors.New("thinking_strip.force_strip_percent must be between 0 and 100")
	}
	if len(profile.AdditionalWorkingDirectories) > 20 || len(profile.RecentCommits) > 20 {
		return errors.New("environment profile lists support at most 20 entries")
	}
	for _, directory := range profile.AdditionalWorkingDirectories {
		if !isAbsoluteEnvironmentPath(directory) || len(directory) > 512 || strings.ContainsAny(directory, "\x00\r\n") {
			return errors.New("environment.profile.additional_working_directories contains an invalid path")
		}
	}
	for _, commit := range profile.RecentCommits {
		if len(commit) > 512 || strings.ContainsAny(commit, "\x00\r\n") {
			return errors.New("environment.profile.recent_commits contains an invalid entry")
		}
	}
	return nil
}

func isAbsoluteEnvironmentPath(value string) bool {
	return filepath.IsAbs(value) || windowsAbsoluteEnvironmentPath.MatchString(value)
}

func (s *SettingService) loadClaudeAntiBanSettings(ctx context.Context) (*ClaudeAntiBanSettings, error) {
	settings := DefaultClaudeAntiBanSettings()
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyClaudeAntiBanSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return settings, nil
		}
		return nil, fmt.Errorf("get Claude anti-ban settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return settings, nil
	}
	if err := json.Unmarshal([]byte(raw), settings); err != nil {
		slog.Warn("invalid Claude anti-ban settings JSON; using defaults", "error", err)
		return DefaultClaudeAntiBanSettings(), nil
	}
	normalizeClaudeAntiBanSettings(settings)
	if err := validateClaudeAntiBanSettings(settings); err != nil {
		slog.Warn("invalid persisted Claude anti-ban settings; using defaults", "error", err)
		return DefaultClaudeAntiBanSettings(), nil
	}
	return settings, nil
}

func (s *SettingService) GetClaudeAntiBanSettings(ctx context.Context) (*ClaudeAntiBanSettings, error) {
	if s == nil {
		return DefaultClaudeAntiBanSettings(), nil
	}
	if current := s.claudeAntiBanRuntime.Load(); current != nil {
		return cloneClaudeAntiBanSettings(current), nil
	}
	settings, err := s.loadClaudeAntiBanSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.claudeAntiBanRuntime.Store(cloneClaudeAntiBanSettings(settings))
	return cloneClaudeAntiBanSettings(settings), nil
}

// ClaudeThinkingStripRuntime 返回删除 thinking 块的当前配置，供网关热路径读取。
func (s *SettingService) ClaudeThinkingStripRuntime(ctx context.Context) ClaudeThinkingStripSettings {
	if s == nil {
		return ClaudeThinkingStripSettings{}
	}
	if current := s.claudeAntiBanRuntime.Load(); current != nil {
		return current.ThinkingStrip
	}
	settings, err := s.GetClaudeAntiBanSettings(ctx)
	if err != nil || settings == nil {
		return ClaudeThinkingStripSettings{}
	}
	return settings.ThinkingStrip
}

func (s *SettingService) SaveClaudeAntiBanSettings(ctx context.Context, incoming *ClaudeAntiBanSettings) (*ClaudeAntiBanSettings, error) {
	next := cloneClaudeAntiBanSettings(incoming)
	normalizeClaudeAntiBanSettings(next)
	if err := validateClaudeAntiBanSettings(next); err != nil {
		return nil, infraerrors.BadRequest("INVALID_CLAUDE_ANTI_BAN_CONFIG", err.Error())
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("marshal Claude anti-ban settings: %w", err)
	}
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository is unavailable")
	}
	if err := s.settingRepo.Set(ctx, SettingKeyClaudeAntiBanSettings, string(encoded)); err != nil {
		return nil, fmt.Errorf("save Claude anti-ban settings: %w", err)
	}
	s.claudeAntiBanRuntime.Store(cloneClaudeAntiBanSettings(next))
	return cloneClaudeAntiBanSettings(next), nil
}

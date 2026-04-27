package config

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
	xssh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Server    ServerConfig    `mapstructure:"server"`
	Discord   DiscordConfig   `mapstructure:"discord"`
	Docker    DockerConfig    `mapstructure:"docker"`
	Git       GitConfig       `mapstructure:"git"`
	Workspace WorkspaceConfig `mapstructure:"workspace"`
	Work      WorkConfig      `mapstructure:"work"`
	Schedule  ScheduleConfig  `mapstructure:"schedule"`
	RSS       RSSConfig       `mapstructure:"rss"`
	Reddit    RedditConfig    `mapstructure:"reddit"`
	Slack     SlackConfig     `mapstructure:"slack"`
	Agent     AgentConfig     `mapstructure:"agent"`
	WhatsApp  WhatsAppConfig  `mapstructure:"whatsapp"`
}

type WhatsAppConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	Workspace          string `mapstructure:"workspace"`
	SessionDBPath      string `mapstructure:"session_db_path"`
	AgentPrefix        string `mapstructure:"agent_prefix"`
	AgentRequirePrefix bool   `mapstructure:"agent_require_prefix"`
	MediaDir           string `mapstructure:"media_dir"`
	ServeAddr          string `mapstructure:"serve_addr"`
}

type SlackConfig struct {
	UserToken string `mapstructure:"user_token"`
}

type AppConfig struct {
	Env string `mapstructure:"env"`
}

type ServerConfig struct {
	Addr           string   `mapstructure:"addr"`
	AllowedOrigins []string `mapstructure:"allowed_origins"`
}

type DiscordConfig struct {
	Enabled        bool              `mapstructure:"enabled"`
	Token          string            `mapstructure:"token"`
	ApplicationID  string            `mapstructure:"application_id"`
	PublicURL      string            `mapstructure:"public_url"`
	GuildAllowlist []string          `mapstructure:"guild_allowlist"`
	StatusMessage  string            `mapstructure:"status_message"`
	Live           DiscordLiveConfig `mapstructure:"live"`
}

type DiscordLiveConfig struct {
	Enabled                         bool     `mapstructure:"enabled"`
	ProviderName                    string   `mapstructure:"provider_name"`
	AutoJoinVoiceChannelIDs         []string `mapstructure:"auto_join_voice_channel_ids"`
	SystemPrompt                    string   `mapstructure:"system_prompt"`
	VoiceName                       string   `mapstructure:"voice_name"`
	SessionResumption               bool     `mapstructure:"session_resumption"`
	MaxSessions                     int      `mapstructure:"max_sessions"`
	TranscriptThreadParentChannelID string   `mapstructure:"transcript_thread_parent_channel_id"`
}

type DockerConfig struct {
	Host         string `mapstructure:"host"`
	Binary       string `mapstructure:"binary"`
	DefaultImage string `mapstructure:"default_image"`
}

type GitConfig struct {
	RepoRoot      string                       `mapstructure:"repo_root"`
	SSH           GitSSHConfig                 `mapstructure:"ssh"`
	Binaries      GitBinariesConfig            `mapstructure:"binaries"`
	DefaultClient GitDefaultClientConfig       `mapstructure:"default_client"`
	RepoDefaults  GitRepoDefaultsConfig        `mapstructure:"repo_defaults"`
	Users         map[string]GitUserConfig     `mapstructure:"users"`
	Repos         map[string]GitRepoAuthConfig `mapstructure:"repos"`
}

type GitSSHConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Addr        string `mapstructure:"addr"`
	HostKeyPath string `mapstructure:"host_key_path"`
}

type GitBinariesConfig struct {
	Git         string `mapstructure:"git"`
	UploadPack  string `mapstructure:"upload_pack"`
	ReceivePack string `mapstructure:"receive_pack"`
}

type GitDefaultClientConfig struct {
	User           string `mapstructure:"user"`
	PrivateKeyPath string `mapstructure:"private_key_path"`
	Host           string `mapstructure:"host"`
	AuthorName     string `mapstructure:"author_name"`
	AuthorEmail    string `mapstructure:"author_email"`
}

type GitRepoDefaultsConfig struct {
	PublicRead     bool     `mapstructure:"public_read"`
	DefaultWriters []string `mapstructure:"default_writers"`
}

type GitUserConfig struct {
	PublicKeys []string `mapstructure:"public_keys"`
}

type GitRepoAuthConfig struct {
	PublicRead *bool    `mapstructure:"public_read"`
	Readers    []string `mapstructure:"readers"`
	Writers    []string `mapstructure:"writers"`
}

type WorkspaceConfig struct {
	DBRoot    string `mapstructure:"db_root"`
	FilesRoot string `mapstructure:"files_root"`
	Current   string `mapstructure:"current"`
}

type WorkConfig struct {
	HelloWorkers     int           `mapstructure:"hello_workers"`
	SchedulerWorkers int           `mapstructure:"scheduler_workers"`
	AgentWorkers     int           `mapstructure:"agent_workers"`
	AgentJobTimeout  time.Duration `mapstructure:"agent_job_timeout"`
}

type ScheduleConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	HelloCron string `mapstructure:"hello_cron"`
}

type RSSConfig struct {
	Feeds map[string][]RSSFeedConfig `mapstructure:"feeds"`
}

type RSSFeedConfig struct {
	Name string `mapstructure:"name"`
	URL  string `mapstructure:"url"`
}

type RedditConfig struct {
	BaseURL    string              `mapstructure:"base_url"`
	Subreddits map[string][]string `mapstructure:"subreddits"`
}

type AgentConfig struct {
	Tools       AgentToolsConfig           `mapstructure:"tools"`
	Subagents   AgentSubagentsConfig       `mapstructure:"subagents"`
	ToolOutput  AgentToolOutputConfig      `mapstructure:"tool_output"`
	Compaction  AgentCompactionConfig      `mapstructure:"compaction"`
	MCPServers  map[string]MCPServerConfig `mapstructure:"mcp_servers"`
	CustomTools AgentCustomToolsConfig     `mapstructure:"custom_tools"`
	Shell       AgentShellConfig           `mapstructure:"shell"`
	Skills      AgentSkillsConfig          `mapstructure:"skills"`
}

// AgentShellConfig configures the agent's restricted shell tool.
type AgentShellConfig struct {
	DockerFallback AgentShellDockerFallbackConfig `mapstructure:"docker_fallback"`
}

// AgentShellDockerFallbackConfig configures the transparent docker container
// fallback used by the shell tool when a command is neither a builtin nor a
// visible agent tool. The fallback runs the command inside a temporary
// container with the workspace snapshot mounted at /workspace and a scratch
// tmpfs at /tmp.
type AgentShellDockerFallbackConfig struct {
	// Enabled toggles the docker fallback. Defaults to true when unset.
	Enabled bool `mapstructure:"enabled"`
	// Image overrides cfg.Docker.DefaultImage for the fallback container.
	Image string `mapstructure:"image"`
	// Network controls the container's network: "on" (default) or "off".
	Network string `mapstructure:"network"`
}

// AgentSkillsConfig controls bundled and on-disk skill discovery.
type AgentSkillsConfig struct {
	// Enabled toggles skill discovery and activation. Defaults to true.
	Enabled bool `mapstructure:"enabled"`
	// UserDir is the per-user skills directory. Defaults to ~/.nalvin/skills.
	UserDir string `mapstructure:"user_dir"`
	// WorkspaceDir overrides the workspace-local skills directory. Defaults to <workspace>/.nalvin/skills.
	WorkspaceDir string `mapstructure:"workspace_dir"`
	// DefaultActive lists skill names to activate at run start regardless of activation: system flag.
	DefaultActive []string `mapstructure:"default_active"`
}

type AgentSubagentsConfig struct {
	ProviderName string `mapstructure:"provider_name"`

	// Per-skill provider overrides for skill-profile resolution. Keys are
	// skill names; values are provider names from providers.<name>.
	SkillProfileProviders         map[string]string   `mapstructure:"skill_profile_providers"`
	SkillProfileFallbackProviders map[string][]string `mapstructure:"skill_profile_fallback_providers"`
}

type AgentCustomToolsConfig struct {
	// Dir is the directory to load custom tool .go files from.
	// Defaults to ~/.nalvin/custom-tools if empty.
	Dir string `mapstructure:"dir"`
	// Enabled controls whether custom tools are loaded. Defaults to true.
	Enabled bool `mapstructure:"enabled"`
	// TimeoutSeconds is the per-invocation timeout for custom tools. Defaults to 30.
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
}

type AgentCompactionConfig struct {
	Enabled                      bool `mapstructure:"enabled"`
	TriggerPct                   int  `mapstructure:"trigger_pct"`
	ReservedTokens               int  `mapstructure:"reserved_tokens"`
	KeepRecentMessages           int  `mapstructure:"keep_recent_messages"`
	AggressiveKeepRecentMessages int  `mapstructure:"aggressive_keep_recent_messages"`
	MaxSummaryTokens             int  `mapstructure:"max_summary_tokens"`
	MaxCompactionsPerRun         int  `mapstructure:"max_compactions_per_run"`
}

type AgentToolsConfig struct {
	DefaultEnabled  []string                      `mapstructure:"default_enabled"`
	DefaultDisabled []string                      `mapstructure:"default_disabled"`
	DefaultPinned   []string                      `mapstructure:"default_pinned"`
	Metadata        map[string]ToolMetadataConfig `mapstructure:"metadata"`
}

type ToolMetadataConfig struct {
	Keywords []string `mapstructure:"keywords"`
}

type AgentToolOutputConfig struct {
	MaxStoredBytes   int `mapstructure:"max_stored_bytes"`
	DefaultPageLines int `mapstructure:"default_page_lines"`
	GrepDefaultLimit int `mapstructure:"grep_default_limit"`
}

type MCPServerConfig struct {
	Transport        string            `mapstructure:"transport"`
	Command          string            `mapstructure:"command"`
	Args             []string          `mapstructure:"args"`
	Env              map[string]string `mapstructure:"env"`
	CWD              string            `mapstructure:"cwd"`
	URL              string            `mapstructure:"url"`
	Headers          map[string]string `mapstructure:"headers"`
	TimeoutMs        int               `mapstructure:"timeout_ms"`
	EnabledByDefault bool              `mapstructure:"enabled_by_default"`
	OAuth            *MCPOAuthConfig   `mapstructure:"oauth"`
}

type MCPOAuthConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	CallbackPort int    `mapstructure:"callback_port"`
}

type ProviderConfig struct {
	Type                 string `mapstructure:"type" yaml:"type,omitempty"`
	BaseURL              string `mapstructure:"base_url" yaml:"base_url,omitempty"`
	APIKey               string `mapstructure:"api_key" yaml:"api_key,omitempty"`
	Model                string `mapstructure:"model" yaml:"model,omitempty"`
	Project              string `mapstructure:"project" yaml:"project,omitempty"`
	Location             string `mapstructure:"location" yaml:"location,omitempty"`
	UserAgentOverride    string `mapstructure:"user_agent_override" yaml:"user_agent_override,omitempty"`
	ReasoningEffort      string `mapstructure:"reasoning_effort" yaml:"reasoning_effort,omitempty"`
	ToolOutputTokenLimit int    `mapstructure:"tool_output_token_limit" yaml:"tool_output_token_limit,omitempty"`
	ContextWindowTokens  int    `mapstructure:"context_window_tokens" yaml:"context_window_tokens,omitempty"`
}

type contextKey string

const configContextKey contextKey = "app-config"

var defaultConfigReader func() io.Reader

var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const (
	ProviderTypeOpenAI       = "openai"
	ProviderTypeOpenAICompat = "openai_compat"
	ProviderTypeAnthropic    = "anthropic"
	ProviderTypeVertex       = "vertex"
	ProviderTypeMistral      = "mistral"
)

var providerReasoningEfforts = map[string][]string{
	ProviderTypeOpenAI: {
		"none",
		"minimal",
		"low",
		"medium",
		"high",
		"xhigh",
	},
	ProviderTypeOpenAICompat: {
		"none",
		"minimal",
		"low",
		"medium",
		"high",
		"xhigh",
	},
	ProviderTypeAnthropic: {
		"low",
		"medium",
		"high",
		"max",
	},
	ProviderTypeVertex: {
		"none",
		"minimal",
		"low",
		"medium",
		"high",
	},
}

const (
	OpenCodeGoDefaultBaseURL          = "https://opencode.ai/zen/go/v1"
	OpenCodeGoAnthropicDefaultBaseURL = "https://opencode.ai/zen/go"
	OpenCodeGoModelPrefix             = "opencode-go/"
	MistralDefaultBaseURL             = "https://api.mistral.ai/v1"
	MistralDefaultModel               = "mistral-small-latest"
)

// OpenCodeGoModelRoute describes how an OpenCode Go model identifier is
// rewritten into a concrete provider type and base URL.
type OpenCodeGoModelRoute struct {
	Model   string
	Type    string
	BaseURL string
}

var openCodeGoModelBackends = map[string]string{
	"deepseek-v4-pro":   ProviderTypeOpenAICompat,
	"deepseek-v4-flash": ProviderTypeOpenAICompat,
	"glm-5.1":           ProviderTypeOpenAICompat,
	"kimi-k2.6":         ProviderTypeOpenAICompat,
	"mimo-v2.5-pro":     ProviderTypeOpenAICompat,
	"minimax-m2.7":      ProviderTypeAnthropic,
	"qwen3.6-plus":      ProviderTypeAnthropic,
}

const defaultAgentJobTimeout = 30 * time.Minute

func SetDefaultConfig(r io.Reader) {
	buf, _ := io.ReadAll(r)
	defaultConfigReader = func() io.Reader {
		return bytes.NewReader(buf)
	}
}

func Init(configFile string) error {
	v := viper.GetViper()
	v.SetConfigType("yaml")

	if defaultConfigReader != nil {
		if err := v.ReadConfig(defaultConfigReader()); err != nil {
			return fmt.Errorf("load default config: %w", err)
		}
	}

	if err := applyDotEnvDefaults(v); err != nil {
		return err
	}

	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.AddConfigPath(configDir())
		v.SetConfigName("config")
		v.SetConfigType("yaml")
	}

	if err := v.MergeInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("merge config file: %w", err)
		}
	}

	v.SetEnvPrefix("NALVIN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	bindEnv(v)
	v.AutomaticEnv()

	return nil
}

func Load() (Config, error) {
	v := viper.GetViper()
	agentCfg := loadAgentConfig(v)
	agentJobTimeout, err := loadDurationSetting(v, "work.agent_job_timeout", defaultAgentJobTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		App: AppConfig{
			Env: strings.TrimSpace(v.GetString("app.env")),
		},
		Server: ServerConfig{
			Addr:           strings.TrimSpace(v.GetString("server.addr")),
			AllowedOrigins: normalizeOrigins(v.Get("server.allowed_origins")),
		},
		Discord: DiscordConfig{
			Enabled:        v.GetBool("discord.enabled"),
			Token:          strings.TrimSpace(os.ExpandEnv(v.GetString("discord.token"))),
			ApplicationID:  strings.TrimSpace(os.ExpandEnv(v.GetString("discord.application_id"))),
			PublicURL:      strings.TrimSpace(v.GetString("discord.public_url")),
			GuildAllowlist: normalizeOrigins(v.Get("discord.guild_allowlist")),
			StatusMessage:  strings.TrimSpace(v.GetString("discord.status_message")),
			Live: DiscordLiveConfig{
				Enabled:                         v.GetBool("discord.live.enabled"),
				ProviderName:                    strings.TrimSpace(v.GetString("discord.live.provider_name")),
				AutoJoinVoiceChannelIDs:         normalizeOrigins(v.Get("discord.live.auto_join_voice_channel_ids")),
				SystemPrompt:                    strings.TrimSpace(v.GetString("discord.live.system_prompt")),
				VoiceName:                       strings.TrimSpace(v.GetString("discord.live.voice_name")),
				SessionResumption:               v.GetBool("discord.live.session_resumption"),
				MaxSessions:                     v.GetInt("discord.live.max_sessions"),
				TranscriptThreadParentChannelID: strings.TrimSpace(v.GetString("discord.live.transcript_thread_parent_channel_id")),
			},
		},
		Workspace: WorkspaceConfig{
			DBRoot:    expandPath(strings.TrimSpace(v.GetString("workspace.db_root"))),
			FilesRoot: expandPath(strings.TrimSpace(v.GetString("workspace.files_root"))),
			Current:   strings.TrimSpace(v.GetString("workspace.current")),
		},
		Work: WorkConfig{
			HelloWorkers:     v.GetInt("work.hello_workers"),
			SchedulerWorkers: v.GetInt("work.scheduler_workers"),
			AgentWorkers:     v.GetInt("work.agent_workers"),
			AgentJobTimeout:  agentJobTimeout,
		},
		Schedule: ScheduleConfig{
			Enabled:   v.GetBool("schedule.enabled"),
			HelloCron: strings.TrimSpace(v.GetString("schedule.hello_cron")),
		},
		Docker: loadDockerConfig(v),
		RSS:    loadRSSConfig(v),
		Reddit: loadRedditConfig(v),
		Slack: SlackConfig{
			UserToken: strings.TrimSpace(v.GetString("slack.user_token")),
		},
		Agent:    agentCfg,
		WhatsApp: loadWhatsAppConfig(v),
	}
	cfg.Git, err = loadGitConfig(v)
	if err != nil {
		return Config{}, err
	}

	if cfg.App.Env == "" {
		cfg.App.Env = "development"
	}

	if cfg.Server.Addr == "" {
		cfg.Server.Addr = "0.0.0.0:4210"
	}

	if len(cfg.Server.AllowedOrigins) == 0 {
		cfg.Server.AllowedOrigins = []string{
			"http://localhost:4211",
			"http://127.0.0.1:4211",
		}
	}

	if cfg.Workspace.DBRoot == "" {
		cfg.Workspace.DBRoot = filepath.Join(configDir(), "workspace-db")
	}

	if cfg.Workspace.FilesRoot == "" {
		cfg.Workspace.FilesRoot = filepath.Join(configDir(), "workspaces")
	}

	if cfg.Workspace.Current == "" {
		cfg.Workspace.Current = "default"
	}

	if cfg.Work.HelloWorkers <= 0 {
		cfg.Work.HelloWorkers = 2
	}

	if cfg.Work.SchedulerWorkers <= 0 {
		cfg.Work.SchedulerWorkers = 1
	}

	if cfg.Schedule.HelloCron == "" {
		cfg.Schedule.HelloCron = "*/5 * * * *"
	}

	return cfg, nil
}

func WithContext(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, configContextKey, cfg)
}

func FromContext(ctx context.Context) (Config, bool) {
	cfg, ok := ctx.Value(configContextKey).(Config)
	return cfg, ok
}

func ConfigFilePath() string {
	return filepath.Join(configDir(), "config.yaml")
}

func ListProviders() map[string]ProviderConfig {
	rawProviders, ok := viper.Get("providers").(map[string]any)
	if !ok || len(rawProviders) == 0 {
		return map[string]ProviderConfig{}
	}

	names := make([]string, 0, len(rawProviders))
	for name := range rawProviders {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	out := make(map[string]ProviderConfig, len(names))
	for _, name := range names {
		out[name] = loadProviderConfig(viper.GetViper(), name)
	}
	return out
}

func configDir() string {
	return filepath.Join(homeDir(), ".nalvin")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func bindEnv(v *viper.Viper) {
	_ = v.BindEnv("app.env")
	_ = v.BindEnv("server.addr")
	_ = v.BindEnv("server.allowed_origins")
	_ = v.BindEnv("discord.enabled")
	_ = v.BindEnv("discord.token")
	_ = v.BindEnv("discord.application_id")
	_ = v.BindEnv("discord.public_url")
	_ = v.BindEnv("discord.guild_allowlist")
	_ = v.BindEnv("discord.status_message")
	_ = v.BindEnv("docker.host")
	_ = v.BindEnv("docker.binary")
	_ = v.BindEnv("docker.default_image")
	_ = v.BindEnv("git.repo_root")
	_ = v.BindEnv("git.ssh.enabled")
	_ = v.BindEnv("git.ssh.addr")
	_ = v.BindEnv("git.ssh.host_key_path")
	_ = v.BindEnv("git.binaries.git")
	_ = v.BindEnv("git.binaries.upload_pack")
	_ = v.BindEnv("git.binaries.receive_pack")
	_ = v.BindEnv("git.default_client.user")
	_ = v.BindEnv("git.default_client.private_key_path")
	_ = v.BindEnv("git.default_client.host")
	_ = v.BindEnv("git.default_client.author_name")
	_ = v.BindEnv("git.default_client.author_email")
	_ = v.BindEnv("workspace.db_root")
	_ = v.BindEnv("workspace.files_root")
	_ = v.BindEnv("workspace.current")
	_ = v.BindEnv("work.hello_workers")
	_ = v.BindEnv("work.scheduler_workers")
	_ = v.BindEnv("work.agent_workers")
	_ = v.BindEnv("work.agent_job_timeout")
	_ = v.BindEnv("schedule.enabled")
	_ = v.BindEnv("schedule.hello_cron")
	_ = v.BindEnv("agent.tools.default_enabled")
	_ = v.BindEnv("agent.tools.default_disabled")
	_ = v.BindEnv("agent.tools.default_pinned")
	_ = v.BindEnv("agent.subagents.provider_name")
	_ = v.BindEnv("whatsapp.enabled")
	_ = v.BindEnv("whatsapp.workspace")
	_ = v.BindEnv("whatsapp.session_db_path")
	_ = v.BindEnv("whatsapp.agent_prefix")
	_ = v.BindEnv("whatsapp.agent_require_prefix")
	_ = v.BindEnv("whatsapp.media_dir")
	_ = v.BindEnv("whatsapp.serve_addr")
}

func applyDotEnvDefaults(v *viper.Viper) error {
	values, err := godotenv.Read()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read .env: %w", err)
	}

	for key, value := range values {
		switch key {
		case "APP_ENV", "NALVIN_APP_ENV":
			v.SetDefault("app.env", value)
		case "SERVER_ADDR", "NALVIN_SERVER_ADDR":
			v.SetDefault("server.addr", value)
		case "SERVER_ALLOWED_ORIGINS", "NALVIN_SERVER_ALLOWED_ORIGINS":
			v.SetDefault("server.allowed_origins", splitAndTrim(value))
		case "DISCORD_ENABLED", "NALVIN_DISCORD_ENABLED":
			v.SetDefault("discord.enabled", value)
		case "DISCORD_TOKEN", "NALVIN_DISCORD_TOKEN":
			v.SetDefault("discord.token", value)
		case "DISCORD_APPLICATION_ID", "NALVIN_DISCORD_APPLICATION_ID":
			v.SetDefault("discord.application_id", value)
		case "DISCORD_PUBLIC_URL", "NALVIN_DISCORD_PUBLIC_URL":
			v.SetDefault("discord.public_url", value)
		case "DISCORD_GUILD_ALLOWLIST", "NALVIN_DISCORD_GUILD_ALLOWLIST":
			v.SetDefault("discord.guild_allowlist", splitAndTrim(value))
		case "DISCORD_STATUS_MESSAGE", "NALVIN_DISCORD_STATUS_MESSAGE":
			v.SetDefault("discord.status_message", value)
		case "DOCKER_HOST", "NALVIN_DOCKER_HOST":
			v.SetDefault("docker.host", value)
		case "DOCKER_BINARY", "NALVIN_DOCKER_BINARY":
			v.SetDefault("docker.binary", value)
		case "DOCKER_DEFAULT_IMAGE", "NALVIN_DOCKER_DEFAULT_IMAGE":
			v.SetDefault("docker.default_image", value)
		case "GIT_REPO_ROOT", "NALVIN_GIT_REPO_ROOT":
			v.SetDefault("git.repo_root", value)
		case "GIT_SSH_ENABLED", "NALVIN_GIT_SSH_ENABLED":
			v.SetDefault("git.ssh.enabled", value)
		case "GIT_SSH_ADDR", "NALVIN_GIT_SSH_ADDR":
			v.SetDefault("git.ssh.addr", value)
		case "GIT_SSH_HOST_KEY_PATH", "NALVIN_GIT_SSH_HOST_KEY_PATH":
			v.SetDefault("git.ssh.host_key_path", value)
		case "GIT_BINARIES_GIT", "NALVIN_GIT_BINARIES_GIT":
			v.SetDefault("git.binaries.git", value)
		case "GIT_BINARIES_UPLOAD_PACK", "NALVIN_GIT_BINARIES_UPLOAD_PACK":
			v.SetDefault("git.binaries.upload_pack", value)
		case "GIT_BINARIES_RECEIVE_PACK", "NALVIN_GIT_BINARIES_RECEIVE_PACK":
			v.SetDefault("git.binaries.receive_pack", value)
		case "GIT_DEFAULT_CLIENT_USER", "NALVIN_GIT_DEFAULT_CLIENT_USER":
			v.SetDefault("git.default_client.user", value)
		case "GIT_DEFAULT_CLIENT_PRIVATE_KEY_PATH", "NALVIN_GIT_DEFAULT_CLIENT_PRIVATE_KEY_PATH":
			v.SetDefault("git.default_client.private_key_path", value)
		case "GIT_DEFAULT_CLIENT_HOST", "NALVIN_GIT_DEFAULT_CLIENT_HOST":
			v.SetDefault("git.default_client.host", value)
		case "GIT_DEFAULT_CLIENT_AUTHOR_NAME", "NALVIN_GIT_DEFAULT_CLIENT_AUTHOR_NAME":
			v.SetDefault("git.default_client.author_name", value)
		case "GIT_DEFAULT_CLIENT_AUTHOR_EMAIL", "NALVIN_GIT_DEFAULT_CLIENT_AUTHOR_EMAIL":
			v.SetDefault("git.default_client.author_email", value)
		case "WORKSPACE_DB_ROOT", "NALVIN_WORKSPACE_DB_ROOT":
			v.SetDefault("workspace.db_root", value)
		case "WORKSPACE_FILES_ROOT", "NALVIN_WORKSPACE_FILES_ROOT":
			v.SetDefault("workspace.files_root", value)
		case "WORKSPACE_CURRENT", "NALVIN_WORKSPACE_CURRENT":
			v.SetDefault("workspace.current", value)
		case "WORK_HELLO_WORKERS", "NALVIN_WORK_HELLO_WORKERS":
			v.SetDefault("work.hello_workers", value)
		case "WORK_SCHEDULER_WORKERS", "NALVIN_WORK_SCHEDULER_WORKERS":
			v.SetDefault("work.scheduler_workers", value)
		case "WORK_AGENT_WORKERS", "NALVIN_WORK_AGENT_WORKERS":
			v.SetDefault("work.agent_workers", value)
		case "WORK_AGENT_JOB_TIMEOUT", "NALVIN_WORK_AGENT_JOB_TIMEOUT":
			v.SetDefault("work.agent_job_timeout", value)
		case "SCHEDULE_ENABLED", "NALVIN_SCHEDULE_ENABLED":
			v.SetDefault("schedule.enabled", value)
		case "SCHEDULE_HELLO_CRON", "NALVIN_SCHEDULE_HELLO_CRON":
			v.SetDefault("schedule.hello_cron", value)
		case "AGENT_TOOLS_DEFAULT_ENABLED", "NALVIN_AGENT_TOOLS_DEFAULT_ENABLED":
			v.SetDefault("agent.tools.default_enabled", splitAndTrim(value))
		case "AGENT_TOOLS_DEFAULT_DISABLED", "NALVIN_AGENT_TOOLS_DEFAULT_DISABLED":
			v.SetDefault("agent.tools.default_disabled", splitAndTrim(value))
		case "AGENT_TOOLS_DEFAULT_PINNED", "NALVIN_AGENT_TOOLS_DEFAULT_PINNED":
			v.SetDefault("agent.tools.default_pinned", splitAndTrim(value))
		case "AGENT_SUBAGENTS_PROVIDER_NAME", "NALVIN_AGENT_SUBAGENTS_PROVIDER_NAME":
			v.SetDefault("agent.subagents.provider_name", value)
		}
	}

	return nil
}

func loadDurationSetting(v *viper.Viper, key string, fallback time.Duration) (time.Duration, error) {
	raw := v.Get(key)
	if raw == nil {
		return fallback, nil
	}

	switch value := raw.(type) {
	case time.Duration:
		return value, nil
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return fallback, nil
		}
		if value == "-1" {
			return -1, nil
		}
		duration, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", key, err)
		}
		return duration, nil
	default:
		return 0, fmt.Errorf("parse %s: unsupported duration value %T", key, raw)
	}
}

func loadAgentConfig(v *viper.Viper) AgentConfig {
	var cfg AgentConfig
	_ = v.UnmarshalKey("agent", &cfg)

	cfg.Tools.DefaultEnabled = trimValues(cfg.Tools.DefaultEnabled)
	cfg.Tools.DefaultDisabled = trimValues(cfg.Tools.DefaultDisabled)
	cfg.Tools.DefaultPinned = trimValues(cfg.Tools.DefaultPinned)
	cfg.Tools.Metadata = normalizeToolMetadata(cfg.Tools.Metadata)
	cfg.Subagents.ProviderName = strings.TrimSpace(cfg.Subagents.ProviderName)
	if cfg.ToolOutput.MaxStoredBytes <= 0 {
		cfg.ToolOutput.MaxStoredBytes = 4 * 1024 * 1024
	}
	if cfg.ToolOutput.DefaultPageLines <= 0 {
		cfg.ToolOutput.DefaultPageLines = 200
	}
	if cfg.ToolOutput.GrepDefaultLimit <= 0 {
		cfg.ToolOutput.GrepDefaultLimit = 100
	}
	cfg.MCPServers = normalizeMCPServers(cfg.MCPServers)

	// Custom tools defaults.
	if cfg.CustomTools.Dir == "" {
		cfg.CustomTools.Dir = expandPath("~/.nalvin/custom-tools")
	} else {
		cfg.CustomTools.Dir = expandPath(strings.TrimSpace(cfg.CustomTools.Dir))
	}
	if !cfg.CustomTools.Enabled && !v.IsSet("agent.custom_tools.enabled") {
		cfg.CustomTools.Enabled = true
	}
	if cfg.CustomTools.TimeoutSeconds <= 0 {
		cfg.CustomTools.TimeoutSeconds = 30
	}

	// Skills defaults.
	if !cfg.Skills.Enabled && !v.IsSet("agent.skills.enabled") {
		cfg.Skills.Enabled = true
	}
	cfg.Skills.UserDir = strings.TrimSpace(cfg.Skills.UserDir)
	cfg.Skills.WorkspaceDir = strings.TrimSpace(cfg.Skills.WorkspaceDir)
	cfg.Skills.DefaultActive = trimValues(cfg.Skills.DefaultActive)

	// Compaction defaults.
	if !cfg.Compaction.Enabled && !v.IsSet("agent.compaction.enabled") {
		cfg.Compaction.Enabled = true
	}
	if cfg.Compaction.TriggerPct <= 0 {
		cfg.Compaction.TriggerPct = 85
	}
	if cfg.Compaction.ReservedTokens <= 0 {
		cfg.Compaction.ReservedTokens = 12000
	}
	if cfg.Compaction.KeepRecentMessages <= 0 {
		cfg.Compaction.KeepRecentMessages = 8
	}
	if cfg.Compaction.AggressiveKeepRecentMessages <= 0 {
		cfg.Compaction.AggressiveKeepRecentMessages = 4
	}
	if cfg.Compaction.MaxSummaryTokens <= 0 {
		cfg.Compaction.MaxSummaryTokens = 1200
	}
	if cfg.Compaction.MaxCompactionsPerRun <= 0 {
		cfg.Compaction.MaxCompactionsPerRun = 8
	}

	// Shell defaults: docker fallback enabled by default unless explicitly opted out.
	if !cfg.Shell.DockerFallback.Enabled && !v.IsSet("agent.shell.docker_fallback.enabled") {
		cfg.Shell.DockerFallback.Enabled = true
	}
	cfg.Shell.DockerFallback.Image = strings.TrimSpace(cfg.Shell.DockerFallback.Image)
	cfg.Shell.DockerFallback.Network = strings.ToLower(strings.TrimSpace(cfg.Shell.DockerFallback.Network))
	switch cfg.Shell.DockerFallback.Network {
	case "", "on", "off":
	default:
		cfg.Shell.DockerFallback.Network = ""
	}

	return cfg
}

func loadWhatsAppConfig(v *viper.Viper) WhatsAppConfig {
	cfg := WhatsAppConfig{
		Enabled:            v.GetBool("whatsapp.enabled"),
		Workspace:          strings.TrimSpace(v.GetString("whatsapp.workspace")),
		SessionDBPath:      expandPath(strings.TrimSpace(v.GetString("whatsapp.session_db_path"))),
		AgentPrefix:        strings.TrimSpace(v.GetString("whatsapp.agent_prefix")),
		AgentRequirePrefix: v.GetBool("whatsapp.agent_require_prefix"),
		MediaDir:           expandPath(strings.TrimSpace(v.GetString("whatsapp.media_dir"))),
		ServeAddr:          strings.TrimSpace(v.GetString("whatsapp.serve_addr")),
	}
	if cfg.Workspace == "" {
		cfg.Workspace = "whatsapp"
	}
	if cfg.SessionDBPath == "" {
		cfg.SessionDBPath = filepath.Join(configDir(), "whatsapp", "session.sqlite")
	}
	if cfg.AgentPrefix == "" {
		cfg.AgentPrefix = "!ai"
	}
	if cfg.MediaDir == "" {
		cfg.MediaDir = filepath.Join(configDir(), "whatsapp", "media")
	}
	if cfg.ServeAddr == "" {
		cfg.ServeAddr = "127.0.0.1:8766"
	}
	return cfg
}

func loadDockerConfig(v *viper.Viper) DockerConfig {
	cfg := DockerConfig{
		Host:         strings.TrimSpace(v.GetString("docker.host")),
		Binary:       expandPath(strings.TrimSpace(v.GetString("docker.binary"))),
		DefaultImage: strings.TrimSpace(v.GetString("docker.default_image")),
	}
	if cfg.DefaultImage == "" {
		cfg.DefaultImage = "nalvin/dev"
	}

	return cfg
}

func loadGitConfig(v *viper.Viper) (GitConfig, error) {
	var cfg GitConfig
	_ = v.UnmarshalKey("git", &cfg)

	cfg.RepoRoot = expandPath(strings.TrimSpace(cfg.RepoRoot))
	if cfg.RepoRoot == "" {
		cfg.RepoRoot = filepath.Join(configDir(), "git-repos")
	}

	cfg.SSH.Addr = strings.TrimSpace(cfg.SSH.Addr)
	if !cfg.SSH.Enabled && !v.IsSet("git.ssh.enabled") {
		cfg.SSH.Enabled = true
	}
	if cfg.SSH.Addr == "" {
		cfg.SSH.Addr = "127.0.0.1:4222"
	}
	cfg.SSH.HostKeyPath = expandPath(strings.TrimSpace(cfg.SSH.HostKeyPath))
	if cfg.SSH.HostKeyPath == "" {
		cfg.SSH.HostKeyPath = filepath.Join(configDir(), "git", "ssh_host_ed25519")
	}

	cfg.Binaries.Git = expandPath(strings.TrimSpace(cfg.Binaries.Git))
	cfg.Binaries.UploadPack = expandPath(strings.TrimSpace(cfg.Binaries.UploadPack))
	cfg.Binaries.ReceivePack = expandPath(strings.TrimSpace(cfg.Binaries.ReceivePack))

	cfg.DefaultClient.User = strings.TrimSpace(cfg.DefaultClient.User)
	if cfg.DefaultClient.User == "" && !v.IsSet("git.default_client.user") {
		cfg.DefaultClient.User = "nalvin"
	}
	cfg.DefaultClient.PrivateKeyPath = expandPath(strings.TrimSpace(cfg.DefaultClient.PrivateKeyPath))
	if cfg.DefaultClient.PrivateKeyPath == "" && !v.IsSet("git.default_client.private_key_path") {
		cfg.DefaultClient.PrivateKeyPath = filepath.Join(configDir(), "git", "id_nalvin")
	}
	cfg.DefaultClient.Host = strings.TrimSpace(cfg.DefaultClient.Host)
	if cfg.DefaultClient.Host == "" {
		cfg.DefaultClient.Host = "127.0.0.1"
	}
	cfg.DefaultClient.AuthorName = strings.TrimSpace(cfg.DefaultClient.AuthorName)
	if cfg.DefaultClient.AuthorName == "" && cfg.DefaultClient.User != "" {
		cfg.DefaultClient.AuthorName = cfg.DefaultClient.User
	}
	cfg.DefaultClient.AuthorEmail = strings.TrimSpace(cfg.DefaultClient.AuthorEmail)
	if cfg.DefaultClient.AuthorEmail == "" && cfg.DefaultClient.User != "" {
		cfg.DefaultClient.AuthorEmail = cfg.DefaultClient.User + "@nalvin.local"
	}

	if !cfg.RepoDefaults.PublicRead && !v.IsSet("git.repo_defaults.public_read") {
		cfg.RepoDefaults.PublicRead = true
	}
	cfg.RepoDefaults.DefaultWriters = trimValues(cfg.RepoDefaults.DefaultWriters)
	if len(cfg.RepoDefaults.DefaultWriters) == 0 && cfg.DefaultClient.User != "" {
		cfg.RepoDefaults.DefaultWriters = []string{cfg.DefaultClient.User}
	}

	cfg.Users = normalizeGitUsers(cfg.Users)
	cfg.Repos = normalizeGitRepoAuth(cfg.Repos)

	if err := ensureDefaultGitClientIdentity(&cfg); err != nil {
		return GitConfig{}, err
	}

	return cfg, nil
}

func ensureDefaultGitClientIdentity(cfg *GitConfig) error {
	if cfg == nil {
		return nil
	}

	user := strings.TrimSpace(cfg.DefaultClient.User)
	keyPath := strings.TrimSpace(cfg.DefaultClient.PrivateKeyPath)
	if user == "" || keyPath == "" {
		return nil
	}

	publicKey, err := ensureGitClientKeyPair(keyPath)
	if err != nil {
		return err
	}

	userCfg := cfg.Users[user]
	for _, existing := range userCfg.PublicKeys {
		if strings.TrimSpace(existing) == publicKey {
			cfg.Users[user] = userCfg
			return nil
		}
	}
	userCfg.PublicKeys = append(userCfg.PublicKeys, publicKey)
	userCfg.PublicKeys = trimValues(userCfg.PublicKeys)
	cfg.Users[user] = userCfg
	return nil
}

func ensureGitClientKeyPair(path string) (string, error) {
	path = expandPath(strings.TrimSpace(path))
	if path == "" {
		return "", fmt.Errorf("git default client private key path is required")
	}

	if privateKey, err := os.ReadFile(path); err == nil {
		signer, err := xssh.ParsePrivateKey(privateKey)
		if err != nil {
			return "", fmt.Errorf("parse git default client private key: %w", err)
		}
		return strings.TrimSpace(string(xssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read git default client private key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create git default client key directory: %w", err)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate git default client key: %w", err)
	}

	block, err := xssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return "", fmt.Errorf("marshal git default client private key: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", fmt.Errorf("write git default client private key: %w", err)
	}

	publicKey, err := xssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return "", fmt.Errorf("build git default client public key: %w", err)
	}
	authorized := strings.TrimSpace(string(xssh.MarshalAuthorizedKey(publicKey)))
	if err := os.WriteFile(path+".pub", []byte(authorized+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write git default client public key: %w", err)
	}
	return authorized, nil
}

func loadRSSConfig(v *viper.Viper) RSSConfig {
	var cfg RSSConfig
	_ = v.UnmarshalKey("rss", &cfg)
	cfg.Feeds = normalizeRSSFeeds(cfg.Feeds)
	return cfg
}

func loadRedditConfig(v *viper.Viper) RedditConfig {
	var cfg RedditConfig
	_ = v.UnmarshalKey("reddit", &cfg)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://www.reddit.com"
	}
	cfg.Subreddits = normalizeStringSliceMap(cfg.Subreddits)
	return cfg
}

func normalizeToolMetadata(values map[string]ToolMetadataConfig) map[string]ToolMetadataConfig {
	if len(values) == 0 {
		return map[string]ToolMetadataConfig{}
	}

	out := make(map[string]ToolMetadataConfig, len(values))
	for toolID, metadata := range values {
		toolID = strings.TrimSpace(toolID)
		if toolID == "" {
			continue
		}
		metadata.Keywords = trimValues(metadata.Keywords)
		out[toolID] = metadata
	}
	return out
}

func normalizeRSSFeeds(values map[string][]RSSFeedConfig) map[string][]RSSFeedConfig {
	if len(values) == 0 {
		return map[string][]RSSFeedConfig{}
	}

	out := make(map[string][]RSSFeedConfig, len(values))
	for category, feeds := range values {
		category = strings.TrimSpace(category)
		if category == "" {
			continue
		}
		normalizedFeeds := make([]RSSFeedConfig, 0, len(feeds))
		for _, feed := range feeds {
			feed.Name = strings.TrimSpace(feed.Name)
			feed.URL = strings.TrimSpace(feed.URL)
			if feed.Name == "" || feed.URL == "" {
				continue
			}
			normalizedFeeds = append(normalizedFeeds, feed)
		}
		if len(normalizedFeeds) > 0 {
			out[category] = normalizedFeeds
		}
	}
	return out
}

func normalizeMCPServers(servers map[string]MCPServerConfig) map[string]MCPServerConfig {
	if len(servers) == 0 {
		return map[string]MCPServerConfig{}
	}

	out := make(map[string]MCPServerConfig, len(servers))
	for name, server := range servers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		server.Transport = strings.TrimSpace(server.Transport)
		server.Command = strings.TrimSpace(server.Command)
		server.Args = trimValues(server.Args)
		server.CWD = expandPath(strings.TrimSpace(server.CWD))
		server.URL = strings.TrimSpace(server.URL)
		server.Headers = normalizeStringMap(server.Headers)
		server.Env = normalizeStringMap(server.Env)
		if server.OAuth != nil {
			server.OAuth.ClientID = strings.TrimSpace(server.OAuth.ClientID)
			server.OAuth.ClientSecret = strings.TrimSpace(server.OAuth.ClientSecret)
			if server.OAuth.CallbackPort <= 0 {
				server.OAuth.CallbackPort = 9876
			}
		}
		out[name] = server
	}
	return out
}

func normalizeGitUsers(users map[string]GitUserConfig) map[string]GitUserConfig {
	if len(users) == 0 {
		return map[string]GitUserConfig{}
	}

	out := make(map[string]GitUserConfig, len(users))
	for name, user := range users {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		user.PublicKeys = trimValues(user.PublicKeys)
		out[name] = user
	}
	return out
}

func normalizeGitRepoAuth(repos map[string]GitRepoAuthConfig) map[string]GitRepoAuthConfig {
	if len(repos) == 0 {
		return map[string]GitRepoAuthConfig{}
	}

	out := make(map[string]GitRepoAuthConfig, len(repos))
	for name, repo := range repos {
		name = normalizeGitRepoIdentifier(name)
		if name == "" {
			continue
		}
		repo.Readers = trimValues(repo.Readers)
		repo.Writers = trimValues(repo.Writers)
		out[name] = repo
	}
	return out
}

func normalizeGitRepoIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, `\`, "/")
	value = strings.TrimPrefix(value, "/")
	if strings.HasSuffix(value, ".git") {
		value = strings.TrimSuffix(value, ".git")
	}
	value = path.Clean(value)
	switch {
	case value == "", value == ".", value == "..":
		return ""
	case strings.HasPrefix(value, "../"), strings.Contains(value, "/../"):
		return ""
	}
	return value
}

func normalizeStringSliceMap(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return map[string][]string{}
	}

	out := make(map[string][]string, len(values))
	for key, slice := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		normalized := trimValues(slice)
		if len(normalized) > 0 {
			out[key] = normalized
		}
	}
	return out
}

func normalizeOrigins(raw any) []string {
	switch value := raw.(type) {
	case []string:
		return trimValues(value)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return trimValues(out)
	case string:
		return splitAndTrim(value)
	default:
		return nil
	}
}

func trimValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}

	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func splitAndTrim(value string) []string {
	return trimValues(strings.Split(value, ","))
}

func SaveWorkspaceCurrent(configFile, current string) error {
	path := expandPath(strings.TrimSpace(configFile))
	if path == "" {
		path = ConfigFilePath()
	}

	doc, err := loadConfigDocument(path)
	if err != nil {
		return err
	}

	workspaceSection, ok := doc["workspace"].(map[string]any)
	if !ok {
		workspaceSection = map[string]any{}
	}
	workspaceSection["current"] = current
	doc["workspace"] = workspaceSection

	if err := writeConfigDocument(path, doc); err != nil {
		return err
	}

	viper.GetViper().Set("workspace.current", current)
	return nil
}

func SaveSlackUserToken(configFile, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("slack user token is required")
	}

	path := expandPath(strings.TrimSpace(configFile))
	if path == "" {
		path = ConfigFilePath()
	}

	doc, err := loadConfigDocument(path)
	if err != nil {
		return err
	}

	slackSection, ok := doc["slack"].(map[string]any)
	if !ok {
		slackSection = map[string]any{}
	}
	slackSection["user_token"] = token
	doc["slack"] = slackSection

	if err := writeConfigDocument(path, doc); err != nil {
		return err
	}

	viper.GetViper().Set("slack.user_token", token)
	return nil
}

func SaveProvider(configFile, name string, provider ProviderConfig, overwrite bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is required")
	}
	if !providerNamePattern.MatchString(name) {
		return fmt.Errorf("provider name %q is invalid: use letters, numbers, hyphens, or underscores", name)
	}

	provider = normalizeProviderConfig(provider)
	if !isSupportedProviderType(provider.Type) {
		return fmt.Errorf("provider %q: unsupported type %q", name, provider.Type)
	}
	if provider.Model == "" {
		return fmt.Errorf("provider %q: model is required", name)
	}
	if reasoningErr := validateProviderReasoningEffort(provider.Type, provider.ReasoningEffort); reasoningErr != nil {
		return fmt.Errorf("provider %q: %w", name, reasoningErr)
	}
	switch provider.Type {
	case ProviderTypeVertex:
		if provider.Project == "" {
			return fmt.Errorf("provider %q: project is required for type %q", name, provider.Type)
		}
		if provider.Location == "" {
			return fmt.Errorf("provider %q: location is required for type %q", name, provider.Type)
		}
	case ProviderTypeOpenAICompat:
		if provider.BaseURL == "" {
			return fmt.Errorf("provider %q: base_url is required for type %q", name, provider.Type)
		}
		if provider.APIKey == "" {
			return fmt.Errorf("provider %q: api_key is required", name)
		}
	default:
		if provider.APIKey == "" {
			return fmt.Errorf("provider %q: api_key is required", name)
		}
	}

	path := expandPath(strings.TrimSpace(configFile))
	if path == "" {
		path = ConfigFilePath()
	}

	doc, err := loadConfigDocument(path)
	if err != nil {
		return err
	}

	providersSection, ok := doc["providers"].(map[string]any)
	if !ok {
		providersSection = map[string]any{}
	}
	if _, exists := providersSection[name]; exists && !overwrite {
		return fmt.Errorf("provider %q already exists (pass --overwrite to replace it)", name)
	}

	entry := map[string]any{
		"type":  provider.Type,
		"model": provider.Model,
	}
	if provider.APIKey != "" {
		entry["api_key"] = provider.APIKey
	}
	if provider.BaseURL != "" {
		entry["base_url"] = provider.BaseURL
	}
	if provider.Project != "" {
		entry["project"] = provider.Project
	}
	if provider.Location != "" {
		entry["location"] = provider.Location
	}
	if provider.UserAgentOverride != "" {
		entry["user_agent_override"] = provider.UserAgentOverride
	}
	if provider.ReasoningEffort != "" {
		entry["reasoning_effort"] = provider.ReasoningEffort
	}
	if provider.ToolOutputTokenLimit > 0 {
		entry["tool_output_token_limit"] = provider.ToolOutputTokenLimit
	}
	if provider.ContextWindowTokens > 0 {
		entry["context_window_tokens"] = provider.ContextWindowTokens
	}

	providersSection[name] = entry
	doc["providers"] = providersSection

	if err := writeConfigDocument(path, doc); err != nil {
		return err
	}

	prefix := "providers." + name
	v := viper.GetViper()
	v.Set(prefix+".type", provider.Type)
	v.Set(prefix+".base_url", provider.BaseURL)
	v.Set(prefix+".api_key", provider.APIKey)
	v.Set(prefix+".model", provider.Model)
	v.Set(prefix+".project", provider.Project)
	v.Set(prefix+".location", provider.Location)
	v.Set(prefix+".user_agent_override", provider.UserAgentOverride)
	v.Set(prefix+".reasoning_effort", provider.ReasoningEffort)
	v.Set(prefix+".tool_output_token_limit", provider.ToolOutputTokenLimit)
	v.Set(prefix+".context_window_tokens", provider.ContextWindowTokens)

	return nil
}

var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func SaveMCPServer(configFile, name string, server MCPServerConfig, overwrite bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	if !mcpServerNamePattern.MatchString(name) {
		return fmt.Errorf("mcp server name %q is invalid: use letters, numbers, hyphens, or underscores", name)
	}

	transport := strings.TrimSpace(server.Transport)
	switch transport {
	case "streamable_http":
		if strings.TrimSpace(server.URL) == "" {
			return fmt.Errorf("mcp server %q: url is required for streamable_http transport", name)
		}
	case "stdio":
		if strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("mcp server %q: command is required for stdio transport", name)
		}
	case "":
		return fmt.Errorf("mcp server %q: transport is required", name)
	default:
		return fmt.Errorf("mcp server %q: unsupported transport %q", name, transport)
	}

	path := expandPath(strings.TrimSpace(configFile))
	if path == "" {
		path = ConfigFilePath()
	}

	doc, err := loadConfigDocument(path)
	if err != nil {
		return err
	}

	agentSection, ok := doc["agent"].(map[string]any)
	if !ok {
		agentSection = map[string]any{}
	}
	mcpSection, ok := agentSection["mcp_servers"].(map[string]any)
	if !ok {
		mcpSection = map[string]any{}
	}
	if _, exists := mcpSection[name]; exists && !overwrite {
		return fmt.Errorf("mcp server %q already exists (pass --overwrite to replace it)", name)
	}

	entry := map[string]any{
		"transport":          server.Transport,
		"enabled_by_default": server.EnabledByDefault,
	}
	if server.URL != "" {
		entry["url"] = server.URL
	}
	if server.Command != "" {
		entry["command"] = server.Command
	}
	if len(server.Args) > 0 {
		entry["args"] = server.Args
	}
	if len(server.Env) > 0 {
		entry["env"] = server.Env
	}
	if len(server.Headers) > 0 {
		entry["headers"] = server.Headers
	}
	if server.CWD != "" {
		entry["cwd"] = server.CWD
	}
	if server.TimeoutMs > 0 {
		entry["timeout_ms"] = server.TimeoutMs
	}
	if server.OAuth != nil {
		oauth := map[string]any{}
		if server.OAuth.ClientID != "" {
			oauth["client_id"] = server.OAuth.ClientID
		}
		if server.OAuth.ClientSecret != "" {
			oauth["client_secret"] = server.OAuth.ClientSecret
		}
		if server.OAuth.CallbackPort > 0 {
			oauth["callback_port"] = server.OAuth.CallbackPort
		}
		if len(oauth) > 0 {
			entry["oauth"] = oauth
		}
	}

	mcpSection[name] = entry
	agentSection["mcp_servers"] = mcpSection
	doc["agent"] = agentSection

	if err := writeConfigDocument(path, doc); err != nil {
		return err
	}

	// Sync the full entry into viper so in-process reads see the new state.
	// Set the entire key as a map to replace any merged defaults cleanly.
	prefix := "agent.mcp_servers." + name
	v := viper.GetViper()
	v.Set(prefix, entry)

	return nil
}

func DeleteMCPServer(configFile, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}

	path := expandPath(strings.TrimSpace(configFile))
	if path == "" {
		path = ConfigFilePath()
	}

	doc, err := loadConfigDocument(path)
	if err != nil {
		return err
	}

	agentSection, ok := doc["agent"].(map[string]any)
	if !ok {
		return fmt.Errorf("mcp server %q not found", name)
	}
	mcpSection, ok := agentSection["mcp_servers"].(map[string]any)
	if !ok {
		return fmt.Errorf("mcp server %q not found", name)
	}
	if _, exists := mcpSection[name]; !exists {
		return fmt.Errorf("mcp server %q not found", name)
	}

	delete(mcpSection, name)
	agentSection["mcp_servers"] = mcpSection
	doc["agent"] = agentSection

	return writeConfigDocument(path, doc)
}

func EnableMCPServer(configFile, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}

	path := expandPath(strings.TrimSpace(configFile))
	if path == "" {
		path = ConfigFilePath()
	}

	doc, err := loadConfigDocument(path)
	if err != nil {
		return err
	}

	agentSection, ok := doc["agent"].(map[string]any)
	if !ok {
		agentSection = map[string]any{}
	}
	mcpSection, ok := agentSection["mcp_servers"].(map[string]any)
	if !ok {
		mcpSection = map[string]any{}
	}

	entry, ok := mcpSection[name].(map[string]any)
	if !ok {
		// Entry only exists in embedded defaults, not user config.
		// Materialize it from the live config so we can persist the override.
		cfg, loadErr := Load()
		if loadErr != nil {
			return loadErr
		}
		serverCfg, exists := cfg.Agent.MCPServers[name]
		if !exists {
			return fmt.Errorf("mcp server %q not found", name)
		}
		entry = map[string]any{
			"transport":          serverCfg.Transport,
			"enabled_by_default": true,
		}
		if serverCfg.URL != "" {
			entry["url"] = serverCfg.URL
		}
		if serverCfg.Command != "" {
			entry["command"] = serverCfg.Command
		}
		if len(serverCfg.Args) > 0 {
			entry["args"] = serverCfg.Args
		}
		if serverCfg.OAuth != nil {
			oauth := map[string]any{}
			if serverCfg.OAuth.ClientID != "" {
				oauth["client_id"] = serverCfg.OAuth.ClientID
			}
			if serverCfg.OAuth.CallbackPort > 0 {
				oauth["callback_port"] = serverCfg.OAuth.CallbackPort
			}
			if len(oauth) > 0 {
				entry["oauth"] = oauth
			}
		}
	} else {
		entry["enabled_by_default"] = true
	}

	mcpSection[name] = entry
	agentSection["mcp_servers"] = mcpSection
	doc["agent"] = agentSection

	if err := writeConfigDocument(path, doc); err != nil {
		return err
	}

	viper.GetViper().Set("agent.mcp_servers."+name+".enabled_by_default", true)
	return nil
}

func ListMCPServers() map[string]MCPServerConfig {
	cfg, err := Load()
	if err != nil {
		return map[string]MCPServerConfig{}
	}
	return cfg.Agent.MCPServers
}

func normalizeProviderConfig(provider ProviderConfig) ProviderConfig {
	rawType := strings.TrimSpace(strings.ToLower(provider.Type))
	provider.Type = normalizeProviderType(provider.Type)
	provider.BaseURL = strings.TrimSpace(provider.BaseURL)
	provider.APIKey = strings.TrimSpace(provider.APIKey)
	provider.Model = strings.TrimSpace(provider.Model)
	provider.Project = strings.TrimSpace(provider.Project)
	provider.Location = strings.TrimSpace(provider.Location)
	provider.UserAgentOverride = strings.TrimSpace(provider.UserAgentOverride)
	provider.ReasoningEffort = NormalizeProviderReasoningEffort(provider.ReasoningEffort)
	if rawType == ProviderTypeMistral {
		if provider.BaseURL == "" {
			provider.BaseURL = MistralDefaultBaseURL
		}
		if provider.Model == "" {
			provider.Model = MistralDefaultModel
		}
	}
	return provider
}

func loadProviderConfig(v *viper.Viper, name string) ProviderConfig {
	prefix := "providers." + name
	return normalizeProviderConfig(ProviderConfig{
		Type:                 v.GetString(prefix + ".type"),
		BaseURL:              v.GetString(prefix + ".base_url"),
		APIKey:               v.GetString(prefix + ".api_key"),
		Model:                v.GetString(prefix + ".model"),
		Project:              v.GetString(prefix + ".project"),
		Location:             v.GetString(prefix + ".location"),
		UserAgentOverride:    v.GetString(prefix + ".user_agent_override"),
		ReasoningEffort:      v.GetString(prefix + ".reasoning_effort"),
		ToolOutputTokenLimit: v.GetInt(prefix + ".tool_output_token_limit"),
		ContextWindowTokens:  v.GetInt(prefix + ".context_window_tokens"),
	})
}

func NormalizeProviderType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ProviderTypeOpenAICompat
	}
	if value == ProviderTypeMistral {
		return ProviderTypeOpenAICompat
	}
	return value
}

func normalizeProviderType(value string) string {
	return NormalizeProviderType(value)
}

func IsSupportedProviderType(value string) bool {
	switch NormalizeProviderType(value) {
	case ProviderTypeOpenAI, ProviderTypeOpenAICompat, ProviderTypeAnthropic, ProviderTypeVertex:
		return true
	default:
		return false
	}
}

// NormalizeOpenCodeGoModelID strips the OpenCode Go prefix from a model id.
func NormalizeOpenCodeGoModelID(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, OpenCodeGoModelPrefix)
	return strings.TrimSpace(model)
}

// IsOpenCodeGoModelID reports whether model uses the opencode-go/<id> prefix.
func IsOpenCodeGoModelID(model string) bool {
	return strings.HasPrefix(strings.TrimSpace(model), OpenCodeGoModelPrefix)
}

// OpenCodeGoRouteForModel resolves an opencode-go/<id> identifier to its
// concrete provider type, base URL, and stripped model name.
func OpenCodeGoRouteForModel(model string) (OpenCodeGoModelRoute, error) {
	model = NormalizeOpenCodeGoModelID(model)
	if model == "" {
		return OpenCodeGoModelRoute{}, fmt.Errorf("OpenCode Go model is required")
	}
	backend, ok := openCodeGoModelBackends[model]
	if !ok {
		return OpenCodeGoModelRoute{}, fmt.Errorf("unsupported OpenCode Go model %q (supported: %s)", model, strings.Join(OpenCodeGoModelIDs(), ", "))
	}
	route := OpenCodeGoModelRoute{
		Model: model,
		Type:  backend,
	}
	if backend == ProviderTypeAnthropic {
		route.BaseURL = OpenCodeGoAnthropicDefaultBaseURL
	} else {
		route.BaseURL = OpenCodeGoDefaultBaseURL
	}
	return route, nil
}

// OpenCodeGoModelIDs returns the supported opencode-go model ids in lexical order.
func OpenCodeGoModelIDs() []string {
	out := make([]string, 0, len(openCodeGoModelBackends))
	for model := range openCodeGoModelBackends {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func isSupportedProviderType(value string) bool {
	return IsSupportedProviderType(value)
}

func NormalizeProviderReasoningEffort(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func AllowedProviderReasoningEfforts(providerType string) []string {
	values := providerReasoningEfforts[NormalizeProviderType(providerType)]
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func ValidateProviderReasoningEffort(providerType, effort string) error {
	return validateProviderReasoningEffort(providerType, effort)
}

func validateProviderReasoningEffort(providerType, effort string) error {
	providerType = NormalizeProviderType(providerType)
	effort = NormalizeProviderReasoningEffort(effort)
	if effort == "" {
		return nil
	}
	allowed := providerReasoningEfforts[providerType]
	for _, candidate := range allowed {
		if effort == candidate {
			return nil
		}
	}
	if len(allowed) == 0 {
		return fmt.Errorf("reasoning_effort is not supported for type %q", providerType)
	}
	return fmt.Errorf("reasoning_effort %q is invalid for type %q (allowed: %s)", effort, providerType, strings.Join(allowed, ", "))
}

func loadConfigDocument(path string) (map[string]any, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}

	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, fmt.Errorf("parse config file: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	return doc, nil
}

func writeConfigDocument(path string, doc map[string]any) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal config file: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func expandPath(path string) string {
	if path == "" || path == "~" {
		return path
	}
	if path == "~/" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), strings.TrimPrefix(path, "~/"))
	}
	return path
}

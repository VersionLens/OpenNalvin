package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func TestLoadPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NALVIN_APP_ENV", "from-env")
	t.Setenv("NALVIN_SERVER_ALLOWED_ORIGINS", "https://env-one.test, https://env-two.test")

	workdir := t.TempDir()
	configDir := filepath.Join(home, ".nalvin")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configFile := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configFile, []byte("app:\n  env: from-config\nserver:\n  addr: :4100\nworkspace:\n  current: from-config\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte("APP_ENV=from-dotenv\nSERVER_ADDR=:3300\nWORKSPACE_CURRENT=from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("app:\n  env: development\nserver:\n  addr: 0.0.0.0:4210\n  allowed_origins:\n    - http://localhost:4211\nworkspace:\n  current: default\n"))

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("addr", "", "")
	if err := flags.Set("addr", ":5200"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	if err := viper.BindPFlag("server.addr", flags.Lookup("addr")); err != nil {
		t.Fatalf("bind flag: %v", err)
	}

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.App.Env != "from-env" {
		t.Fatalf("expected env override, got %q", cfg.App.Env)
	}
	if cfg.Server.Addr != ":5200" {
		t.Fatalf("expected flag override, got %q", cfg.Server.Addr)
	}
	if len(cfg.Server.AllowedOrigins) != 2 || cfg.Server.AllowedOrigins[0] != "https://env-one.test" {
		t.Fatalf("expected env origin override, got %#v", cfg.Server.AllowedOrigins)
	}
	if cfg.Workspace.Current != "from-config" {
		t.Fatalf("expected config file workspace current, got %q", cfg.Workspace.Current)
	}
	if cfg.Workspace.DBRoot != filepath.Join(home, ".nalvin", "workspace-db") {
		t.Fatalf("expected default workspace db root, got %q", cfg.Workspace.DBRoot)
	}
	if cfg.Workspace.FilesRoot != filepath.Join(home, ".nalvin", "workspaces") {
		t.Fatalf("expected default workspace files root, got %q", cfg.Workspace.FilesRoot)
	}
	if cfg.Work.HelloWorkers != 2 {
		t.Fatalf("expected default hello workers, got %d", cfg.Work.HelloWorkers)
	}
	if cfg.Work.AgentJobTimeout != 30*time.Minute {
		t.Fatalf("expected default agent job timeout, got %s", cfg.Work.AgentJobTimeout)
	}
	if cfg.Schedule.HelloCron != "*/5 * * * *" {
		t.Fatalf("expected default hello cron, got %q", cfg.Schedule.HelloCron)
	}
}

func TestConfigFileOverridesDotEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := t.TempDir()
	configDir := filepath.Join(home, ".nalvin")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte("SERVER_ADDR=:3300\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("server:\n  addr: :4100\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("server:\n  addr: 0.0.0.0:4210\n"))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.Addr != ":4100" {
		t.Fatalf("expected config file to override .env, got %q", cfg.Server.Addr)
	}
}

func TestWorkspaceAndScheduleOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NALVIN_WORKSPACE_DB_ROOT", "/tmp/db-root")
	t.Setenv("NALVIN_WORKSPACE_FILES_ROOT", "/tmp/files-root")
	t.Setenv("NALVIN_WORKSPACE_CURRENT", "alpha")
	t.Setenv("NALVIN_WORK_HELLO_WORKERS", "7")
	t.Setenv("NALVIN_WORK_AGENT_JOB_TIMEOUT", "45m")
	t.Setenv("NALVIN_SCHEDULE_HELLO_CRON", "0 * * * *")

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("workspace:\n  db_root: ~/.nalvin/workspace-db\n  files_root: ~/.nalvin/workspaces\n  current: default\nwork:\n  hello_workers: 2\nschedule:\n  hello_cron: '*/5 * * * *'\n"))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Workspace.DBRoot != "/tmp/db-root" {
		t.Fatalf("expected env workspace db root override, got %q", cfg.Workspace.DBRoot)
	}
	if cfg.Workspace.FilesRoot != "/tmp/files-root" {
		t.Fatalf("expected env workspace files root override, got %q", cfg.Workspace.FilesRoot)
	}
	if cfg.Workspace.Current != "alpha" {
		t.Fatalf("expected env workspace current override, got %q", cfg.Workspace.Current)
	}
	if cfg.Work.HelloWorkers != 7 {
		t.Fatalf("expected env hello worker override, got %d", cfg.Work.HelloWorkers)
	}
	if cfg.Work.AgentJobTimeout != 45*time.Minute {
		t.Fatalf("expected env agent job timeout override, got %s", cfg.Work.AgentJobTimeout)
	}
	if cfg.Schedule.HelloCron != "0 * * * *" {
		t.Fatalf("expected env hello cron override, got %q", cfg.Schedule.HelloCron)
	}
}

func TestExplicitZeroAgentWorkersIsPreserved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NALVIN_WORK_AGENT_WORKERS", "0")

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("work:\n  agent_workers: 1\n"))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Work.AgentWorkers != 0 {
		t.Fatalf("expected explicit zero agent workers to be preserved, got %d", cfg.Work.AgentWorkers)
	}
}

func TestExplicitZeroAgentJobTimeoutIsPreserved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NALVIN_WORK_AGENT_JOB_TIMEOUT", "0")

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("work:\n  agent_job_timeout: 30m\n"))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Work.AgentJobTimeout != 0 {
		t.Fatalf("expected explicit zero agent job timeout to be preserved, got %s", cfg.Work.AgentJobTimeout)
	}
}

func TestInvalidAgentJobTimeoutReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NALVIN_WORK_AGENT_JOB_TIMEOUT", "later")

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("work:\n  agent_job_timeout: 30m\n"))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid agent job timeout to fail load")
	}
}

func TestLoadAgentToolMetadataKeywords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString(`
agent:
  tools:
    metadata:
      web_fetch_get:
        keywords:
          - http request
          - "  download url  "
`))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	metadata, ok := cfg.Agent.Tools.Metadata["web_fetch_get"]
	if !ok {
		t.Fatalf("expected web_fetch_get metadata, got %#v", cfg.Agent.Tools.Metadata)
	}
	if got, want := metadata.Keywords, []string{"http request", "download url"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected normalized keywords: got %#v want %#v", got, want)
	}
}

func TestLoadAgentSubagentsProviderName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString(`
agent:
  subagents:
    provider_name: "  kimi  "
`))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Agent.Subagents.ProviderName != "kimi" {
		t.Fatalf("unexpected subagent provider name: %q", cfg.Agent.Subagents.ProviderName)
	}
}

func TestBundledDefaultConfigIncludesBundledMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	viper.Reset()
	t.Cleanup(viper.Reset)

	data, err := os.ReadFile(filepath.Join("..", "..", "cmd", "config.default.yaml"))
	if err != nil {
		t.Fatalf("read bundled default config: %v", err)
	}
	SetDefaultConfig(bytes.NewReader(data))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := cfg.Agent.Tools.DefaultPinned; len(got) != 2 || got[0] != "mcp__exa__web_search_exa" || got[1] != "search_tools" {
		t.Fatalf("unexpected default pinned tools: got %#v", got)
	}

	exa, ok := cfg.Agent.MCPServers["exa"]
	if !ok {
		t.Fatalf("expected exa mcp server in bundled defaults, got %#v", cfg.Agent.MCPServers)
	}
	if exa.Transport != "streamable_http" {
		t.Fatalf("unexpected exa transport: %q", exa.Transport)
	}
	if exa.URL != "https://mcp.exa.ai/mcp" {
		t.Fatalf("unexpected exa url: %q", exa.URL)
	}
	if !exa.EnabledByDefault {
		t.Fatalf("expected exa to be enabled by default")
	}

	deepwiki, ok := cfg.Agent.MCPServers["deepwiki"]
	if !ok {
		t.Fatalf("expected deepwiki mcp server in bundled defaults, got %#v", cfg.Agent.MCPServers)
	}
	if deepwiki.Transport != "streamable_http" {
		t.Fatalf("unexpected deepwiki transport: %q", deepwiki.Transport)
	}
	if deepwiki.URL != "https://mcp.deepwiki.com/mcp" {
		t.Fatalf("unexpected deepwiki url: %q", deepwiki.URL)
	}
	if !deepwiki.EnabledByDefault {
		t.Fatalf("expected deepwiki to be enabled by default")
	}

	metadata, ok := cfg.Agent.Tools.Metadata["mcp__exa__web_search_exa"]
	if !ok {
		t.Fatalf("expected bundled metadata for mcp__exa__web_search_exa, got %#v", cfg.Agent.Tools.Metadata)
	}
	if got, want := metadata.Keywords, []string{"web search", "search web", "internet search"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("unexpected bundled exa keywords: got %#v want %#v", got, want)
	}
	for _, id := range cfg.Agent.Tools.DefaultPinned {
		if strings.Contains(id, "deepwiki") {
			t.Fatalf("did not expect any deepwiki tool to be pinned by default: %#v", cfg.Agent.Tools.DefaultPinned)
		}
	}

	// Verify OAuth-enabled servers are present and disabled by default.
	for _, name := range []string{"linear", "sentry"} {
		srv, ok := cfg.Agent.MCPServers[name]
		if !ok {
			t.Fatalf("expected %s mcp server in bundled defaults", name)
		}
		if srv.Transport != "streamable_http" {
			t.Fatalf("unexpected %s transport: %q", name, srv.Transport)
		}
		if srv.EnabledByDefault {
			t.Fatalf("expected %s to be disabled by default", name)
		}
		if srv.OAuth == nil {
			t.Fatalf("expected %s to have OAuth config", name)
		}
		if srv.OAuth.CallbackPort <= 0 {
			t.Fatalf("expected %s to have a callback port", name)
		}
	}

	// GitHub uses PAT headers, not OAuth.
	github, ok := cfg.Agent.MCPServers["github"]
	if !ok {
		t.Fatalf("expected github mcp server in bundled defaults")
	}
	if github.OAuth != nil {
		t.Fatalf("expected github to not have OAuth config (uses PAT headers)")
	}
	if github.EnabledByDefault {
		t.Fatalf("expected github to be disabled by default")
	}
}

func TestLoadDiscordConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NALVIN_DISCORD_ENABLED", "true")
	t.Setenv("NALVIN_DISCORD_TOKEN", "${DISCORD_TOKEN}")
	t.Setenv("DISCORD_TOKEN", "secret-token")
	t.Setenv("NALVIN_DISCORD_APPLICATION_ID", "app_123")
	t.Setenv("NALVIN_DISCORD_GUILD_ALLOWLIST", "guild-a, guild-b")
	t.Setenv("NALVIN_DISCORD_STATUS_MESSAGE", "Working")

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("discord:\n  enabled: false\n"))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if !cfg.Discord.Enabled {
		t.Fatalf("expected discord to be enabled")
	}
	if cfg.Discord.Token != "secret-token" {
		t.Fatalf("unexpected discord token: %q", cfg.Discord.Token)
	}
	if cfg.Discord.ApplicationID != "app_123" {
		t.Fatalf("unexpected application id: %q", cfg.Discord.ApplicationID)
	}
	if got, want := cfg.Discord.GuildAllowlist, []string{"guild-a", "guild-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected guild allowlist: got %#v want %#v", got, want)
	}
	if cfg.Discord.StatusMessage != "Working" {
		t.Fatalf("unexpected discord status message: %q", cfg.Discord.StatusMessage)
	}
}

func TestConfigFilePathUsesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := ConfigFilePath(), filepath.Join(home, ".nalvin", "config.yaml"); got != want {
		t.Fatalf("unexpected config path: got %q want %q", got, want)
	}
}

func TestSaveWorkspaceCurrentCreatesAndUpdatesConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".nalvin", "config.yaml")

	if err := SaveWorkspaceCurrent("", "alpha"); err != nil {
		t.Fatalf("save current workspace: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	workspaceSection, ok := doc["workspace"].(map[string]any)
	if !ok {
		t.Fatalf("expected workspace section, got %#v", doc["workspace"])
	}
	if workspaceSection["current"] != "alpha" {
		t.Fatalf("expected workspace current alpha, got %#v", workspaceSection["current"])
	}

	if err := SaveWorkspaceCurrent(configPath, "beta"); err != nil {
		t.Fatalf("update current workspace: %v", err)
	}

	cfgData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read updated config file: %v", err)
	}
	doc = map[string]any{}
	if err := yaml.Unmarshal(cfgData, &doc); err != nil {
		t.Fatalf("unmarshal updated config file: %v", err)
	}
	workspaceSection, ok = doc["workspace"].(map[string]any)
	if !ok {
		t.Fatalf("expected workspace section after update, got %#v", doc["workspace"])
	}
	if workspaceSection["current"] != "beta" {
		t.Fatalf("expected workspace current beta, got %#v", workspaceSection["current"])
	}
}

func TestSaveWorkspaceCurrentPreservesProvidersBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".nalvin")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	input := []byte("providers:\n  default:\n    base_url: https://example.test/v1\n    api_key: ${OPENAI_API_KEY}\n    model: gpt-test\nworkspace:\n  current: alpha\n")
	if err := os.WriteFile(configPath, input, 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	if err := SaveWorkspaceCurrent(configPath, "beta"); err != nil {
		t.Fatalf("update current workspace: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}

	providers, ok := doc["providers"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers section, got %#v", doc["providers"])
	}
	providerDefault, ok := providers["default"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers.default section, got %#v", providers["default"])
	}
	if providerDefault["base_url"] != "https://example.test/v1" {
		t.Fatalf("unexpected providers.default.base_url: %#v", providerDefault["base_url"])
	}
	if providerDefault["api_key"] != "${OPENAI_API_KEY}" {
		t.Fatalf("unexpected providers.default.api_key: %#v", providerDefault["api_key"])
	}
	if providerDefault["model"] != "gpt-test" {
		t.Fatalf("unexpected providers.default.model: %#v", providerDefault["model"])
	}
}

func TestSaveProviderCreatesAndUpdatesConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	if err := SaveProvider("", "lmstudio", ProviderConfig{
		Type:              ProviderTypeOpenAICompat,
		BaseURL:           "http://localhost:1234/v1",
		APIKey:            "secret-key",
		Model:             "gpt-test",
		UserAgentOverride: "nalvin-test/1.0",
		ReasoningEffort:   "HIGH",
	}, false); err != nil {
		t.Fatalf("save provider: %v", err)
	}

	configPath := filepath.Join(home, ".nalvin", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}

	providers, ok := doc["providers"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers section, got %#v", doc["providers"])
	}
	lmstudio, ok := providers["lmstudio"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers.lmstudio section, got %#v", providers["lmstudio"])
	}
	if lmstudio["base_url"] != "http://localhost:1234/v1" {
		t.Fatalf("unexpected providers.lmstudio.base_url: %#v", lmstudio["base_url"])
	}
	if lmstudio["type"] != ProviderTypeOpenAICompat {
		t.Fatalf("unexpected providers.lmstudio.type: %#v", lmstudio["type"])
	}
	if lmstudio["api_key"] != "secret-key" {
		t.Fatalf("unexpected providers.lmstudio.api_key: %#v", lmstudio["api_key"])
	}
	if lmstudio["model"] != "gpt-test" {
		t.Fatalf("unexpected providers.lmstudio.model: %#v", lmstudio["model"])
	}
	if lmstudio["user_agent_override"] != "nalvin-test/1.0" {
		t.Fatalf("unexpected providers.lmstudio.user_agent_override: %#v", lmstudio["user_agent_override"])
	}
	if lmstudio["reasoning_effort"] != "high" {
		t.Fatalf("unexpected providers.lmstudio.reasoning_effort: %#v", lmstudio["reasoning_effort"])
	}

	if got := viper.GetString("providers.lmstudio.base_url"); got != "http://localhost:1234/v1" {
		t.Fatalf("expected provider in viper, got %q", got)
	}
	if got := viper.GetString("providers.lmstudio.user_agent_override"); got != "nalvin-test/1.0" {
		t.Fatalf("expected user agent override in viper, got %q", got)
	}
	if got := viper.GetString("providers.lmstudio.reasoning_effort"); got != "high" {
		t.Fatalf("expected reasoning effort in viper, got %q", got)
	}
	if got := viper.GetString("providers.lmstudio.type"); got != ProviderTypeOpenAICompat {
		t.Fatalf("unexpected provider type in viper: %q", got)
	}
}

func TestSaveProviderPreservesWorkspaceSection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	configDir := filepath.Join(home, ".nalvin")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	input := []byte("workspace:\n  current: alpha\n")
	if err := os.WriteFile(configPath, input, 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	if err := SaveProvider(configPath, "lmstudio", ProviderConfig{
		Type:    ProviderTypeOpenAICompat,
		BaseURL: "http://localhost:1234/v1",
		APIKey:  "secret-key",
		Model:   "gpt-test",
	}, false); err != nil {
		t.Fatalf("save provider: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}

	workspaceSection, ok := doc["workspace"].(map[string]any)
	if !ok {
		t.Fatalf("expected workspace section, got %#v", doc["workspace"])
	}
	if workspaceSection["current"] != "alpha" {
		t.Fatalf("unexpected workspace current: %#v", workspaceSection["current"])
	}
}

func TestSaveProviderRejectsDuplicateWithoutOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	if err := SaveProvider("", "lmstudio", ProviderConfig{
		Type:    ProviderTypeOpenAICompat,
		BaseURL: "http://localhost:1234/v1",
		APIKey:  "secret-key",
		Model:   "gpt-test",
	}, false); err != nil {
		t.Fatalf("save provider: %v", err)
	}

	err := SaveProvider("", "lmstudio", ProviderConfig{
		Type:    ProviderTypeOpenAICompat,
		BaseURL: "http://localhost:5555/v1",
		APIKey:  "other-key",
		Model:   "gpt-other",
	}, false)
	if err == nil || err.Error() != `provider "lmstudio" already exists (pass --overwrite to replace it)` {
		t.Fatalf("unexpected duplicate error: %v", err)
	}
}

func TestSaveAnthropicProviderOmitsBaseURLAndPersistsType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	if err := SaveProvider("", "anthropic", ProviderConfig{
		Type:              ProviderTypeAnthropic,
		APIKey:            "secret-key",
		Model:             "claude-sonnet-4-5",
		UserAgentOverride: "nalvin-anthropic/1.0",
		ReasoningEffort:   "MAX",
	}, false); err != nil {
		t.Fatalf("save anthropic provider: %v", err)
	}

	configPath := filepath.Join(home, ".nalvin", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}

	providers, ok := doc["providers"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers section, got %#v", doc["providers"])
	}
	anthropicProvider, ok := providers["anthropic"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers.anthropic section, got %#v", providers["anthropic"])
	}
	if anthropicProvider["type"] != ProviderTypeAnthropic {
		t.Fatalf("unexpected providers.anthropic.type: %#v", anthropicProvider["type"])
	}
	if _, ok := anthropicProvider["base_url"]; ok {
		t.Fatalf("expected no base_url for anthropic provider, got %#v", anthropicProvider["base_url"])
	}
	if anthropicProvider["api_key"] != "secret-key" {
		t.Fatalf("unexpected providers.anthropic.api_key: %#v", anthropicProvider["api_key"])
	}
	if anthropicProvider["model"] != "claude-sonnet-4-5" {
		t.Fatalf("unexpected providers.anthropic.model: %#v", anthropicProvider["model"])
	}
	if anthropicProvider["user_agent_override"] != "nalvin-anthropic/1.0" {
		t.Fatalf("unexpected providers.anthropic.user_agent_override: %#v", anthropicProvider["user_agent_override"])
	}
	if anthropicProvider["reasoning_effort"] != "max" {
		t.Fatalf("unexpected providers.anthropic.reasoning_effort: %#v", anthropicProvider["reasoning_effort"])
	}
	if got := viper.GetString("providers.anthropic.type"); got != ProviderTypeAnthropic {
		t.Fatalf("expected anthropic type in viper, got %q", got)
	}
	if got := viper.GetString("providers.anthropic.reasoning_effort"); got != "max" {
		t.Fatalf("expected anthropic reasoning effort in viper, got %q", got)
	}
}

func TestSaveProviderRejectsInvalidReasoningEffort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	err := SaveProvider("", "openai", ProviderConfig{
		Type:            ProviderTypeOpenAI,
		BaseURL:         "https://api.openai.com/v1",
		APIKey:          "secret-key",
		Model:           "gpt-5.4",
		ReasoningEffort: "max",
	}, false)
	if err == nil || err.Error() != `provider "openai": reasoning_effort "max" is invalid for type "openai" (allowed: none, minimal, low, medium, high, xhigh)` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveOpenAIProviderAllowsEmptyBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	if err := SaveProvider("", "openai", ProviderConfig{
		Type:            ProviderTypeOpenAI,
		APIKey:          "secret-key",
		Model:           "gpt-5.4",
		ReasoningEffort: "high",
	}, false); err != nil {
		t.Fatalf("save openai provider: %v", err)
	}

	configPath := filepath.Join(home, ".nalvin", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}

	providers := doc["providers"].(map[string]any)
	openAIProvider := providers["openai"].(map[string]any)
	if openAIProvider["type"] != ProviderTypeOpenAI {
		t.Fatalf("unexpected providers.openai.type: %#v", openAIProvider["type"])
	}
	if _, ok := openAIProvider["base_url"]; ok {
		t.Fatalf("expected no base_url for openai provider, got %#v", openAIProvider["base_url"])
	}
}

func TestSaveOpenAICompatProviderRequiresBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	err := SaveProvider("", "compat", ProviderConfig{
		Type:   ProviderTypeOpenAICompat,
		APIKey: "secret-key",
		Model:  "local-model",
	}, false)
	if err == nil || err.Error() != `provider "compat": base_url is required for type "openai_compat"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveMCPServerCreatesAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	server := MCPServerConfig{
		Transport:        "streamable_http",
		URL:              "https://mcp.example.com/mcp",
		EnabledByDefault: true,
		OAuth: &MCPOAuthConfig{
			ClientID:     "test-client",
			CallbackPort: 9876,
		},
	}

	if err := SaveMCPServer("", "example", server, false); err != nil {
		t.Fatalf("save mcp server: %v", err)
	}

	configPath := filepath.Join(home, ".nalvin", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}

	agentSection, ok := doc["agent"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent section, got %#v", doc["agent"])
	}
	mcpSection, ok := agentSection["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent.mcp_servers section, got %#v", agentSection["mcp_servers"])
	}
	example, ok := mcpSection["example"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent.mcp_servers.example, got %#v", mcpSection["example"])
	}
	if example["url"] != "https://mcp.example.com/mcp" {
		t.Fatalf("unexpected url: %#v", example["url"])
	}
	if example["transport"] != "streamable_http" {
		t.Fatalf("unexpected transport: %#v", example["transport"])
	}
	oauth, ok := example["oauth"].(map[string]any)
	if !ok {
		t.Fatalf("expected oauth section, got %#v", example["oauth"])
	}
	if oauth["client_id"] != "test-client" {
		t.Fatalf("unexpected oauth client_id: %#v", oauth["client_id"])
	}
}

func TestSaveMCPServerRejectsDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	server := MCPServerConfig{
		Transport: "streamable_http",
		URL:       "https://mcp.example.com/mcp",
	}

	if err := SaveMCPServer("", "dup", server, false); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := SaveMCPServer("", "dup", server, false); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := SaveMCPServer("", "dup", server, true); err != nil {
		t.Fatalf("overwrite should succeed: %v", err)
	}
}

func TestDeleteMCPServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	server := MCPServerConfig{
		Transport: "streamable_http",
		URL:       "https://mcp.example.com/mcp",
	}

	if err := SaveMCPServer("", "removeme", server, false); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := DeleteMCPServer("", "removeme"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := DeleteMCPServer("", "removeme"); err == nil {
		t.Fatal("expected not found error on second delete")
	}
}

func TestSaveMCPServerValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	t.Cleanup(viper.Reset)

	tests := []struct {
		name    string
		server  MCPServerConfig
		wantErr string
	}{
		{
			name:    "empty transport",
			server:  MCPServerConfig{URL: "https://example.com"},
			wantErr: "transport is required",
		},
		{
			name:    "streamable_http without url",
			server:  MCPServerConfig{Transport: "streamable_http"},
			wantErr: "url is required",
		},
		{
			name:    "stdio without command",
			server:  MCPServerConfig{Transport: "stdio"},
			wantErr: "command is required",
		},
		{
			name:    "unsupported transport",
			server:  MCPServerConfig{Transport: "websocket", URL: "ws://example.com"},
			wantErr: "unsupported transport",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SaveMCPServer("", "test", tt.server, true)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestNormalizeMCPOAuthDefaults(t *testing.T) {
	servers := map[string]MCPServerConfig{
		"test": {
			Transport: "streamable_http",
			URL:       "https://example.com/mcp",
			OAuth: &MCPOAuthConfig{
				ClientID: "  my-client  ",
			},
		},
	}
	result := normalizeMCPServers(servers)
	got := result["test"]
	if got.OAuth == nil {
		t.Fatal("expected OAuth config")
	}
	if got.OAuth.ClientID != "my-client" {
		t.Fatalf("expected trimmed client id, got %q", got.OAuth.ClientID)
	}
	if got.OAuth.CallbackPort != 9876 {
		t.Fatalf("expected default callback port 9876, got %d", got.OAuth.CallbackPort)
	}
}

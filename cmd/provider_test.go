package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderListOmitsBlankBundledDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRoot(t, "provider", "list")
	if err != nil {
		t.Fatalf("provider list: %v", err)
	}
	if out != "No providers configured.\n" {
		t.Fatalf("unexpected provider list output: %q", out)
	}
}

func TestProviderListShowsConfiguredProvidersFromLiveConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".nalvin")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	configData := []byte(`
providers:
  default:
    base_url: https://api.openai.com/v1
    api_key: ${OPENAI_API_KEY}
    model: gpt-5.4-mini
    user_agent_override: nalvin-default/1.0
    reasoning_effort: high
  lmstudio:
    type: openai_compat
    base_url: http://localhost:1234/v1
    model: local-model
`)
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	out, err := executeRoot(t, "--json", "provider", "list")
	if err != nil {
		t.Fatalf("provider list json: %v", err)
	}

	var items []providerListItem
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("unmarshal provider list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 providers, got %#v", items)
	}
	if items[0].Name != "default" || items[0].Type != "openai_compat" || items[0].UserAgentOverride != "nalvin-default/1.0" || items[0].ReasoningEffort != "high" || !items[0].Default || !items[0].Complete || !items[0].APIKeyConfigured {
		t.Fatalf("unexpected default provider payload: %#v", items[0])
	}
	if items[1].Name != "lmstudio" || items[1].Type != "openai_compat" || items[1].Default || items[1].Complete || items[1].APIKeyConfigured {
		t.Fatalf("unexpected lmstudio provider payload: %#v", items[1])
	}
}

func TestProviderAddPromptsForAPIKeyAndSavesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "lmstudio", "--base-url", "http://localhost:1234/v1", "--model", "local-model", "--reasoning-effort", "high")
	if err != nil {
		t.Fatalf("provider add: %v", err)
	}
	if !strings.Contains(out, "Saved provider lmstudio") {
		t.Fatalf("unexpected provider add output: %q", out)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(configData)
	if !strings.Contains(text, "lmstudio:") {
		t.Fatalf("expected provider name in config file, got %q", text)
	}
	if !strings.Contains(text, "base_url: http://localhost:1234/v1") {
		t.Fatalf("expected base_url in config file, got %q", text)
	}
	if !strings.Contains(text, "type: openai_compat") {
		t.Fatalf("expected type in config file, got %q", text)
	}
	if !strings.Contains(text, "api_key: secret-key") {
		t.Fatalf("expected api_key in config file, got %q", text)
	}
	if !strings.Contains(text, "model: local-model") {
		t.Fatalf("expected model in config file, got %q", text)
	}
	if !strings.Contains(text, "reasoning_effort: high") {
		t.Fatalf("expected reasoning_effort in config file, got %q", text)
	}

	listOut, err := executeRoot(t, "--json", "provider", "list")
	if err != nil {
		t.Fatalf("provider list after add: %v", err)
	}
	var items []providerListItem
	if err := json.Unmarshal([]byte(listOut), &items); err != nil {
		t.Fatalf("unmarshal provider list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "lmstudio" || !items[0].Complete {
		t.Fatalf("unexpected provider list payload after add: %#v", items)
	}
	if items[0].Type != "openai_compat" || items[0].ReasoningEffort != "high" {
		t.Fatalf("unexpected provider type after add: %#v", items[0])
	}
}

func TestProviderAddRejectsDuplicateWithoutOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "lmstudio", "--base-url", "http://localhost:1234/v1", "--model", "local-model"); err != nil {
		t.Fatalf("initial provider add: %v", err)
	}

	_, err := executeRootWithInput(t, strings.NewReader("other-key\n"), "provider", "add", "lmstudio", "--base-url", "http://localhost:5555/v1", "--model", "other-model")
	if err == nil || err.Error() != `provider "lmstudio" already exists (pass --overwrite to replace it)` {
		t.Fatalf("unexpected duplicate error: %v", err)
	}
}

func TestProviderAddAnthropicPromptsForAPIKeyAndSavesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "anthropic", "--type", "anthropic", "--model", "claude-sonnet-4-5", "--reasoning-effort", "max")
	if err != nil {
		t.Fatalf("provider add anthropic: %v", err)
	}
	if !strings.Contains(out, "Saved provider anthropic") {
		t.Fatalf("unexpected provider add output: %q", out)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(configData)
	if !strings.Contains(text, "anthropic:") {
		t.Fatalf("expected provider name in config file, got %q", text)
	}
	if !strings.Contains(text, "type: anthropic") {
		t.Fatalf("expected provider type in config file, got %q", text)
	}
	if strings.Contains(text, "base_url:") {
		t.Fatalf("did not expect base_url in anthropic config file, got %q", text)
	}
	if !strings.Contains(text, "api_key: secret-key") {
		t.Fatalf("expected api_key in config file, got %q", text)
	}
	if !strings.Contains(text, "model: claude-sonnet-4-5") {
		t.Fatalf("expected model in config file, got %q", text)
	}
	if !strings.Contains(text, "reasoning_effort: max") {
		t.Fatalf("expected reasoning_effort in config file, got %q", text)
	}

	listOut, err := executeRoot(t, "--json", "provider", "list")
	if err != nil {
		t.Fatalf("provider list after anthropic add: %v", err)
	}
	var items []providerListItem
	if err := json.Unmarshal([]byte(listOut), &items); err != nil {
		t.Fatalf("unmarshal provider list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "anthropic" || items[0].Type != "anthropic" || items[0].ReasoningEffort != "max" || !items[0].Complete {
		t.Fatalf("unexpected provider list payload after anthropic add: %#v", items)
	}
}

func TestProviderAddOpenAIDoesNotRequireBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "openai", "--type", "openai", "--model", "gpt-5.4", "--reasoning-effort", "high")
	if err != nil {
		t.Fatalf("provider add openai: %v", err)
	}
	if !strings.Contains(out, "Saved provider openai") {
		t.Fatalf("unexpected provider add output: %q", out)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(configData)
	if !strings.Contains(text, "type: openai") {
		t.Fatalf("expected provider type in config file, got %q", text)
	}
	if strings.Contains(text, "base_url:") {
		t.Fatalf("did not expect base_url in openai config file, got %q", text)
	}
	if !strings.Contains(text, "reasoning_effort: high") {
		t.Fatalf("expected reasoning_effort in config file, got %q", text)
	}
}

func TestProviderAddRejectsInvalidReasoningEffortForType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "anthropic", "--type", "anthropic", "--model", "claude-sonnet-4-5", "--reasoning-effort", "minimal")
	if err == nil || err.Error() != `reasoning_effort "minimal" is invalid for type "anthropic" (allowed: low, medium, high, max)` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderAddMistralNormalizesToOpenAICompat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "mistral", "--type", "mistral", "--model", "mistral-small-latest")
	if err != nil {
		t.Fatalf("provider add mistral: %v", err)
	}
	if !strings.Contains(out, "Saved provider mistral") {
		t.Fatalf("unexpected provider add output: %q", out)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(configData)
	if !strings.Contains(text, "mistral:") {
		t.Fatalf("expected provider name in config file, got %q", text)
	}
	if !strings.Contains(text, "type: openai_compat") {
		t.Fatalf("expected normalized provider type in config file, got %q", text)
	}
	if !strings.Contains(text, "base_url: https://api.mistral.ai/v1") {
		t.Fatalf("expected Mistral base_url in config file, got %q", text)
	}
	if !strings.Contains(text, "api_key: secret-key") {
		t.Fatalf("expected api_key in config file, got %q", text)
	}
	if !strings.Contains(text, "model: mistral-small-latest") {
		t.Fatalf("expected model in config file, got %q", text)
	}

	listOut, err := executeRoot(t, "--json", "provider", "list")
	if err != nil {
		t.Fatalf("provider list after mistral add: %v", err)
	}
	var items []providerListItem
	if err := json.Unmarshal([]byte(listOut), &items); err != nil {
		t.Fatalf("unmarshal provider list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "mistral" || items[0].Type != "openai_compat" || items[0].BaseURL != "https://api.mistral.ai/v1" || !items[0].Complete {
		t.Fatalf("unexpected provider list payload after mistral add: %#v", items)
	}
}

func TestProviderAddMistralDefaultsModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "mistral", "--type", "mistral"); err != nil {
		t.Fatalf("provider add mistral: %v", err)
	}

	listOut, err := executeRoot(t, "--json", "provider", "list")
	if err != nil {
		t.Fatalf("provider list after mistral add: %v", err)
	}
	var items []providerListItem
	if err := json.Unmarshal([]byte(listOut), &items); err != nil {
		t.Fatalf("unmarshal provider list: %v", err)
	}
	if len(items) != 1 || items[0].Model != "mistral-small-latest" || items[0].BaseURL != "https://api.mistral.ai/v1" {
		t.Fatalf("expected default Mistral model, got %#v", items)
	}
}

func TestProviderAddVertexUsesProjectAndLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRoot(t, "provider", "add", "gemini", "--type", "vertex", "--project", "my-gcp-project", "--location", "us-central1", "--model", "gemini-3-pro-preview", "--reasoning-effort", "medium")
	if err != nil {
		t.Fatalf("provider add vertex: %v", err)
	}
	if !strings.Contains(out, "Saved provider gemini") {
		t.Fatalf("unexpected provider add output: %q", out)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(configData)
	if !strings.Contains(text, "gemini:") {
		t.Fatalf("expected provider name in config file, got %q", text)
	}
	if !strings.Contains(text, "type: vertex") {
		t.Fatalf("expected type in config file, got %q", text)
	}
	if !strings.Contains(text, "project: my-gcp-project") {
		t.Fatalf("expected project in config file, got %q", text)
	}
	if !strings.Contains(text, "location: us-central1") {
		t.Fatalf("expected location in config file, got %q", text)
	}
	if !strings.Contains(text, "model: gemini-3-pro-preview") {
		t.Fatalf("expected model in config file, got %q", text)
	}
	if strings.Contains(text, "api_key:") {
		t.Fatalf("did not expect api_key in vertex config file, got %q", text)
	}
	if !strings.Contains(text, "reasoning_effort: medium") {
		t.Fatalf("expected reasoning_effort in config file, got %q", text)
	}

	listOut, err := executeRoot(t, "--json", "provider", "list")
	if err != nil {
		t.Fatalf("provider list after vertex add: %v", err)
	}
	var items []providerListItem
	if err := json.Unmarshal([]byte(listOut), &items); err != nil {
		t.Fatalf("unmarshal provider list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "gemini" || items[0].Type != "vertex" || items[0].Project != "my-gcp-project" || items[0].Location != "us-central1" || !items[0].Complete || items[0].APIKeyConfigured {
		t.Fatalf("unexpected provider list payload after vertex add: %#v", items)
	}
}

func TestProviderAddVertexRequiresProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := executeRoot(t, "provider", "add", "gemini", "--type", "vertex", "--location", "us-central1", "--model", "gemini-3-pro-preview")
	if err == nil || err.Error() != "provide a GCP project with --project" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderAddVertexRequiresLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := executeRoot(t, "provider", "add", "gemini", "--type", "vertex", "--project", "my-project", "--model", "gemini-3-pro-preview")
	if err == nil || err.Error() != "provide a GCP location with --location" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderAddOpenCodeGoAnthropicRouteSavesProviderConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "opencode-go", "--model", "opencode-go/minimax-m2.7", "--reasoning-effort", "max")
	if err != nil {
		t.Fatalf("provider add opencode go anthropic route: %v", err)
	}
	if !strings.Contains(out, "Saved provider opencode-go") {
		t.Fatalf("unexpected provider add output: %q", out)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(configData)
	if !strings.Contains(text, "type: anthropic") {
		t.Fatalf("expected anthropic type in config file, got %q", text)
	}
	if !strings.Contains(text, "base_url: https://opencode.ai/zen/go") {
		t.Fatalf("expected opencode-go anthropic base_url, got %q", text)
	}
	if !strings.Contains(text, "model: minimax-m2.7") {
		t.Fatalf("expected stripped model id, got %q", text)
	}

	listOut, err := executeRoot(t, "--json", "provider", "list")
	if err != nil {
		t.Fatalf("provider list after opencode go add: %v", err)
	}
	var items []providerListItem
	if err := json.Unmarshal([]byte(listOut), &items); err != nil {
		t.Fatalf("unmarshal provider list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "opencode-go" || items[0].Type != "anthropic" || items[0].BaseURL != "https://opencode.ai/zen/go" || items[0].Model != "minimax-m2.7" || !items[0].Complete {
		t.Fatalf("unexpected provider list payload: %#v", items)
	}
}

func TestProviderAddOpenCodeGoChatRouteSavesProviderConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "opencode-go-kimi", "--model", "opencode-go/kimi-k2.6", "--reasoning-effort", "high")
	if err != nil {
		t.Fatalf("provider add opencode go chat route: %v", err)
	}
	if !strings.Contains(out, "Saved provider opencode-go-kimi") {
		t.Fatalf("unexpected provider add output: %q", out)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(configData)
	if !strings.Contains(text, "type: openai_compat") {
		t.Fatalf("expected openai_compat type in config file, got %q", text)
	}
	if !strings.Contains(text, "base_url: https://opencode.ai/zen/go/v1") {
		t.Fatalf("expected opencode-go chat base_url, got %q", text)
	}
}

func TestProviderAddOpenCodeGoRejectsInvalidModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := executeRootWithInput(t, strings.NewReader("secret-key\n"), "provider", "add", "opencode-go", "--model", "opencode-go/not-a-model")
	if err == nil || !strings.Contains(err.Error(), `unsupported OpenCode Go model "not-a-model"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

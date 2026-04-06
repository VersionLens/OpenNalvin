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

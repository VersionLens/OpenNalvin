package agent

import (
	"testing"

	"github.com/spf13/viper"
)

func TestLoadDefaultProviderConfig(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	t.Setenv("OPENAI_API_KEY", "secret-key")
	viper.Set("providers.default.base_url", "https://example.test/v1")
	viper.Set("providers.default.api_key", "${OPENAI_API_KEY}")
	viper.Set("providers.default.model", "gpt-test")
	viper.Set("providers.default.user_agent_override", "nalvin-test/1.0")
	viper.Set("providers.default.reasoning_effort", "HIGH")
	viper.Set("providers.default.tool_output_token_limit", 1234)

	cfg, err := LoadDefaultProviderConfig()
	if err != nil {
		t.Fatalf("load provider config: %v", err)
	}

	if cfg.BaseURL != "https://example.test/v1" {
		t.Fatalf("unexpected base_url: %q", cfg.BaseURL)
	}
	if cfg.APIKey != "secret-key" {
		t.Fatalf("unexpected api_key: %q", cfg.APIKey)
	}
	if cfg.Model != "gpt-test" {
		t.Fatalf("unexpected model: %q", cfg.Model)
	}
	if cfg.UserAgentOverride != "nalvin-test/1.0" {
		t.Fatalf("unexpected user_agent_override: %q", cfg.UserAgentOverride)
	}
	if cfg.ReasoningEffort != "high" {
		t.Fatalf("unexpected reasoning_effort: %q", cfg.ReasoningEffort)
	}
	if cfg.ToolOutputTokenLimit != 1234 {
		t.Fatalf("unexpected tool_output_token_limit: %d", cfg.ToolOutputTokenLimit)
	}
	if cfg.Type != ProviderTypeOpenAICompat {
		t.Fatalf("unexpected provider type: %q", cfg.Type)
	}
}

func TestLoadDefaultProviderConfigUsesOpenAICompatWhenTypeOmitted(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.default.api_key", "secret-key")
	viper.Set("providers.default.model", "gpt-test")

	_, err := LoadDefaultProviderConfig()
	if err == nil || err.Error() != `provider "default": base_url not configured for type "openai_compat"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadDefaultProviderConfigRequiresAPIKey(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.default.base_url", "https://example.test/v1")
	viper.Set("providers.default.model", "gpt-test")

	_, err := LoadDefaultProviderConfig()
	if err == nil || err.Error() != `provider "default": api_key not configured` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadDefaultProviderConfigRequiresModel(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.default.base_url", "https://example.test/v1")
	viper.Set("providers.default.api_key", "secret-key")

	_, err := LoadDefaultProviderConfig()
	if err == nil || err.Error() != `provider "default": model not configured` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeProviderNameDefaultsToDefault(t *testing.T) {
	t.Parallel()

	if got := normalizeProviderName(""); got != "default" {
		t.Fatalf("expected default provider, got %q", got)
	}
	if got := normalizeProviderName("  lmstudio  "); got != "lmstudio" {
		t.Fatalf("expected trimmed provider name, got %q", got)
	}
}

func TestLoadAnthropicProviderConfigAllowsEmptyBaseURL(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.anthropic.type", "anthropic")
	viper.Set("providers.anthropic.api_key", "secret-key")
	viper.Set("providers.anthropic.model", "claude-sonnet-4-5")
	viper.Set("providers.anthropic.user_agent_override", "nalvin-anthropic/1.0")
	viper.Set("providers.anthropic.reasoning_effort", "MAX")

	cfg, err := LoadProviderConfig("anthropic")
	if err != nil {
		t.Fatalf("load anthropic provider config: %v", err)
	}
	if cfg.Type != ProviderTypeAnthropic {
		t.Fatalf("unexpected provider type: %q", cfg.Type)
	}
	if cfg.BaseURL != "" {
		t.Fatalf("expected empty base_url, got %q", cfg.BaseURL)
	}
	if cfg.UserAgentOverride != "nalvin-anthropic/1.0" {
		t.Fatalf("unexpected user_agent_override: %q", cfg.UserAgentOverride)
	}
	if cfg.ReasoningEffort != "max" {
		t.Fatalf("unexpected reasoning_effort: %q", cfg.ReasoningEffort)
	}
}

func TestLoadProviderConfigRejectsUnsupportedType(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.bad.type", "mystery")
	viper.Set("providers.bad.api_key", "secret-key")
	viper.Set("providers.bad.model", "test-model")

	_, err := LoadProviderConfig("bad")
	if err == nil || err.Error() != `provider "bad": unsupported type "mystery"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadProviderConfigRequiresBaseURLForOpenAICompat(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.compat.type", "openai_compat")
	viper.Set("providers.compat.api_key", "secret-key")
	viper.Set("providers.compat.model", "local-model")

	_, err := LoadProviderConfig("compat")
	if err == nil || err.Error() != `provider "compat": base_url not configured for type "openai_compat"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadProviderConfigRejectsInvalidOpenAIReasoningEffort(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.openai.type", "openai")
	viper.Set("providers.openai.base_url", "https://api.openai.com/v1")
	viper.Set("providers.openai.api_key", "secret-key")
	viper.Set("providers.openai.model", "gpt-5.4")
	viper.Set("providers.openai.reasoning_effort", "max")

	_, err := LoadProviderConfig("openai")
	if err == nil || err.Error() != `provider "openai": reasoning_effort "max" is invalid for type "openai" (allowed: none, minimal, low, medium, high, xhigh)` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadProviderConfigRejectsInvalidAnthropicReasoningEffort(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("providers.anthropic.type", "anthropic")
	viper.Set("providers.anthropic.api_key", "secret-key")
	viper.Set("providers.anthropic.model", "claude-sonnet-4-5")
	viper.Set("providers.anthropic.reasoning_effort", "minimal")

	_, err := LoadProviderConfig("anthropic")
	if err == nil || err.Error() != `provider "anthropic": reasoning_effort "minimal" is invalid for type "anthropic" (allowed: low, medium, high, max)` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveProviderNameForModel(t *testing.T) {
	t.Parallel()

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.default.type", "openai")
	viper.Set("providers.default.api_key", "configured")
	viper.Set("providers.default.model", "gpt-5.4")
	viper.Set("providers.openai.type", "openai")
	viper.Set("providers.openai.api_key", "configured")
	viper.Set("providers.openai.model", "gpt-5.4")
	viper.Set("providers.kimi.type", "openai_compat")
	viper.Set("providers.kimi.base_url", "https://api.kimi.test/v1")
	viper.Set("providers.kimi.api_key", "configured")
	viper.Set("providers.kimi.model", "kimi-for-coding")

	got, err := ResolveProviderNameForModel("gpt-5.4")
	if err != nil {
		t.Fatalf("ResolveProviderNameForModel: %v", err)
	}
	if got != "default" {
		t.Fatalf("expected default provider for duplicate model, got %q", got)
	}

	got, err = ResolveProviderNameForModel("kimi-for-coding")
	if err != nil {
		t.Fatalf("ResolveProviderNameForModel kimi: %v", err)
	}
	if got != "kimi" {
		t.Fatalf("expected kimi provider, got %q", got)
	}
}

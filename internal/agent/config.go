package agent

import (
	"fmt"
	"os"
	"sort"
	"strings"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/spf13/viper"
)

const defaultProviderName = "default"

const (
	ProviderTypeOpenAI       = "openai"
	ProviderTypeOpenAICompat = "openai_compat"
	ProviderTypeAnthropic    = "anthropic"
	ProviderTypeVertex       = "vertex"
)

func normalizeProviderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultProviderName
	}
	return name
}

// ProviderConfig holds connection details for a configured model provider.
type ProviderConfig struct {
	Type                 string
	BaseURL              string
	APIKey               string
	Model                string
	Project              string
	Location             string
	UserAgentOverride    string
	ReasoningEffort      string
	ToolOutputTokenLimit int
	ContextWindowTokens  int
}

// LoadDefaultProviderConfig reads providers.default from Viper.
func LoadDefaultProviderConfig() (ProviderConfig, error) {
	return LoadProviderConfig(defaultProviderName)
}

// LoadProviderConfig reads providers.<name> from Viper.
func LoadProviderConfig(name string) (ProviderConfig, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProviderConfig{}, fmt.Errorf("provider name is required")
	}

	prefix := "providers." + name
	cfg := ProviderConfig{
		Type:                 normalizeProviderType(viper.GetString(prefix + ".type")),
		BaseURL:              strings.TrimSpace(viper.GetString(prefix + ".base_url")),
		APIKey:               strings.TrimSpace(os.ExpandEnv(viper.GetString(prefix + ".api_key"))),
		Model:                strings.TrimSpace(viper.GetString(prefix + ".model")),
		Project:              strings.TrimSpace(os.ExpandEnv(viper.GetString(prefix + ".project"))),
		Location:             strings.TrimSpace(os.ExpandEnv(viper.GetString(prefix + ".location"))),
		UserAgentOverride:    strings.TrimSpace(viper.GetString(prefix + ".user_agent_override")),
		ReasoningEffort:      configpkg.NormalizeProviderReasoningEffort(viper.GetString(prefix + ".reasoning_effort")),
		ToolOutputTokenLimit: viper.GetInt(prefix + ".tool_output_token_limit"),
		ContextWindowTokens:  viper.GetInt(prefix + ".context_window_tokens"),
	}
	if cfg.ToolOutputTokenLimit <= 0 {
		cfg.ToolOutputTokenLimit = 4000
	}
	reasoningErr := configpkg.ValidateProviderReasoningEffort(cfg.Type, cfg.ReasoningEffort)

	switch {
	case !isSupportedProviderType(cfg.Type):
		return ProviderConfig{}, fmt.Errorf("provider %q: unsupported type %q", name, cfg.Type)
	case cfg.Type == ProviderTypeOpenAICompat && cfg.BaseURL == "":
		return ProviderConfig{}, fmt.Errorf("provider %q: base_url not configured for type %q", name, cfg.Type)
	case reasoningErr != nil:
		return ProviderConfig{}, fmt.Errorf("provider %q: %w", name, reasoningErr)
	case cfg.Type == ProviderTypeVertex:
		if cfg.Project == "" {
			return ProviderConfig{}, fmt.Errorf("provider %q: project not configured for type %q", name, cfg.Type)
		}
		if cfg.Location == "" {
			return ProviderConfig{}, fmt.Errorf("provider %q: location not configured for type %q", name, cfg.Type)
		}
		if cfg.Model == "" {
			return ProviderConfig{}, fmt.Errorf("provider %q: model not configured", name)
		}
		return cfg, nil
	case cfg.APIKey == "":
		return ProviderConfig{}, fmt.Errorf("provider %q: api_key not configured", name)
	case cfg.Model == "":
		return ProviderConfig{}, fmt.Errorf("provider %q: model not configured", name)
	default:
		return cfg, nil
	}
}

func normalizeProviderType(value string) string {
	return configpkg.NormalizeProviderType(value)
}

func isSupportedProviderType(value string) bool {
	return configpkg.IsSupportedProviderType(value)
}

// ResolveProviderNameForModel finds the configured provider name for a model ID.
// If multiple providers point at the same model, the default provider wins,
// then the remaining names are considered in lexical order.
func ResolveProviderNameForModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model is required")
	}

	providers := configpkg.ListProviders()
	names := make([]string, 0, len(providers))
	for name, provider := range providers {
		if strings.TrimSpace(provider.Model) != model {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no configured provider found for model %q", model)
	}

	sort.Slice(names, func(i, j int) bool {
		leftDefault := names[i] == defaultProviderName
		rightDefault := names[j] == defaultProviderName
		if leftDefault != rightDefault {
			return leftDefault
		}
		return names[i] < names[j]
	})
	return names[0], nil
}

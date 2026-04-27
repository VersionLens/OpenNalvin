package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/output"
)

var (
	providerAddBaseURL         string
	providerAddModel           string
	providerAddType            string
	providerAddReasoningEffort string
	providerAddProject         string
	providerAddLocation        string
	providerAddOverwrite       bool
)

type providerListItem struct {
	Name                 string `json:"name"`
	Type                 string `json:"type"`
	BaseURL              string `json:"base_url,omitempty"`
	Model                string `json:"model,omitempty"`
	Project              string `json:"project,omitempty"`
	Location             string `json:"location,omitempty"`
	UserAgentOverride    string `json:"user_agent_override,omitempty"`
	ReasoningEffort      string `json:"reasoning_effort,omitempty"`
	APIKeyConfigured     bool   `json:"api_key_configured"`
	ToolOutputTokenLimit int    `json:"tool_output_token_limit,omitempty"`
	ContextWindowTokens  int    `json:"context_window_tokens,omitempty"`
	Default              bool   `json:"default"`
	Complete             bool   `json:"complete"`
}

type providerAddPayload struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	BaseURL          string `json:"base_url"`
	Model            string `json:"model"`
	Project          string `json:"project,omitempty"`
	Location         string `json:"location,omitempty"`
	ReasoningEffort  string `json:"reasoning_effort,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	ConfigPath       string `json:"config_path"`
	Overwritten      bool   `json:"overwritten"`
}

var providerCmd = &cobra.Command{
	Use:     "provider",
	Aliases: []string{"providers"},
	Short:   "List and manage configured model providers",
}

var providerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured providers from the live config",
	RunE: func(cmd *cobra.Command, args []string) error {
		items := listConfiguredProviders()

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(items)
		}
		if len(items) == 0 {
			w.Line("No providers configured.")
			return nil
		}

		for _, item := range items {
			status := "ready"
			if !item.Complete {
				status = "incomplete"
			}
			label := item.Name
			if item.Default {
				label += " (default)"
			}

			w.Line("%s [%s]", label, status)
			w.Line("  type: %s", item.Type)
			if item.BaseURL != "" {
				w.Line("  base_url: %s", item.BaseURL)
			}
			if item.Model != "" {
				w.Line("  model: %s", item.Model)
			}
			if item.Project != "" {
				w.Line("  project: %s", item.Project)
			}
			if item.Location != "" {
				w.Line("  location: %s", item.Location)
			}
			if item.UserAgentOverride != "" {
				w.Line("  user_agent_override: %s", item.UserAgentOverride)
			}
			if item.ReasoningEffort != "" {
				w.Line("  reasoning_effort: %s", item.ReasoningEffort)
			}
			if item.ToolOutputTokenLimit > 0 {
				w.Line("  tool_output_token_limit: %d", item.ToolOutputTokenLimit)
			}
			if item.ContextWindowTokens > 0 {
				w.Line("  context_window_tokens: %d", item.ContextWindowTokens)
			}
			if item.Type == config.ProviderTypeVertex {
				w.Line("  auth: application default credentials")
			} else if item.APIKeyConfigured {
				w.Line("  api_key: configured")
			} else {
				w.Line("  api_key: missing")
			}
		}
		return nil
	},
}

var providerAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a model provider to ~/.nalvin/config.yaml",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rawProviderType := strings.TrimSpace(strings.ToLower(providerAddType))
		providerType := config.NormalizeProviderType(rawProviderType)
		baseURL := strings.TrimSpace(providerAddBaseURL)
		model := strings.TrimSpace(providerAddModel)
		project := strings.TrimSpace(providerAddProject)
		location := strings.TrimSpace(providerAddLocation)
		reasoningEffort := config.NormalizeProviderReasoningEffort(providerAddReasoningEffort)
		if rawProviderType == config.ProviderTypeMistral {
			if baseURL == "" {
				baseURL = config.MistralDefaultBaseURL
			}
			if model == "" {
				model = config.MistralDefaultModel
			}
		}
		if config.IsOpenCodeGoModelID(model) {
			route, err := config.OpenCodeGoRouteForModel(model)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("type") && providerType != route.Type {
				return fmt.Errorf("OpenCode Go model %q requires provider type %q, got %q", model, route.Type, providerType)
			}
			if baseURL != "" && strings.TrimRight(baseURL, "/") != strings.TrimRight(route.BaseURL, "/") {
				return fmt.Errorf("OpenCode Go model %q requires base_url %q, got %q", model, route.BaseURL, baseURL)
			}
			providerType = route.Type
			baseURL = route.BaseURL
			model = route.Model
		}
		if !config.IsSupportedProviderType(providerType) {
			return fmt.Errorf("unsupported provider type %q", providerType)
		}
		if providerType == config.ProviderTypeOpenAICompat && baseURL == "" {
			return fmt.Errorf("provide a base URL with --base-url")
		}
		if providerType == config.ProviderTypeVertex {
			if project == "" {
				return fmt.Errorf("provide a GCP project with --project")
			}
			if location == "" {
				return fmt.Errorf("provide a GCP location with --location")
			}
		}
		if model == "" {
			return fmt.Errorf("provide a model with --model")
		}
		if err := config.ValidateProviderReasoningEffort(providerType, reasoningEffort); err != nil {
			return err
		}

		var apiKey string
		if providerType != config.ProviderTypeVertex {
			var err error
			apiKey, err = promptForSecret(cmd.InOrStdin(), cmd.ErrOrStderr(), "API key: ")
			if err != nil {
				return fmt.Errorf("read api key: %w", err)
			}
			if apiKey == "" {
				return fmt.Errorf("api key is required")
			}
		}

		if err := config.SaveProvider(cfgFile, args[0], config.ProviderConfig{
			Type:            providerType,
			BaseURL:         baseURL,
			APIKey:          apiKey,
			Model:           model,
			Project:         project,
			Location:        location,
			ReasoningEffort: reasoningEffort,
		}, providerAddOverwrite); err != nil {
			return err
		}

		payload := providerAddPayload{
			Name:             strings.TrimSpace(args[0]),
			Type:             providerType,
			BaseURL:          baseURL,
			Model:            model,
			Project:          project,
			Location:         location,
			ReasoningEffort:  reasoningEffort,
			APIKeyConfigured: apiKey != "",
			ConfigPath:       resolvedConfigPath(),
			Overwritten:      providerAddOverwrite,
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}

		w.Line("Saved provider %s to %s", payload.Name, payload.ConfigPath)
		w.Line("Use it with: nalvin agent run --provider %s ...", payload.Name)
		return nil
	},
}

func init() {
	providerAddCmd.Flags().StringVar(&providerAddBaseURL, "base-url", "", "provider base URL override (required for type openai_compat unless --type mistral or --model uses opencode-go/)")
	providerAddCmd.Flags().StringVar(&providerAddType, "type", config.ProviderTypeOpenAICompat, "provider type: openai, openai_compat, anthropic, vertex, or mistral")
	providerAddCmd.Flags().StringVar(&providerAddModel, "model", "", "default model to use for the provider")
	providerAddCmd.Flags().StringVar(&providerAddProject, "project", "", "GCP project ID (required for type vertex)")
	providerAddCmd.Flags().StringVar(&providerAddLocation, "location", "", "GCP location, e.g. us-central1 (required for type vertex)")
	providerAddCmd.Flags().StringVar(&providerAddReasoningEffort, "reasoning-effort", "", "provider reasoning effort override")
	providerAddCmd.Flags().BoolVar(&providerAddOverwrite, "overwrite", false, "replace an existing provider entry with the same name")

	providerCmd.AddCommand(providerListCmd)
	providerCmd.AddCommand(providerAddCmd)
	rootCmd.AddCommand(providerCmd)
}

func listConfiguredProviders() []providerListItem {
	providers := config.ListProviders()
	items := make([]providerListItem, 0, len(providers))
	for name, provider := range providers {
		if !providerHasUserConfiguration(provider) {
			continue
		}
		items = append(items, providerListItem{
			Name:                 name,
			Type:                 provider.Type,
			BaseURL:              provider.BaseURL,
			Model:                provider.Model,
			Project:              provider.Project,
			Location:             provider.Location,
			UserAgentOverride:    provider.UserAgentOverride,
			ReasoningEffort:      provider.ReasoningEffort,
			APIKeyConfigured:     provider.APIKey != "",
			ToolOutputTokenLimit: provider.ToolOutputTokenLimit,
			ContextWindowTokens:  provider.ContextWindowTokens,
			Default:              name == "default",
			Complete:             providerIsComplete(provider),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Default != items[j].Default {
			return items[i].Default
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func providerHasUserConfiguration(provider config.ProviderConfig) bool {
	return provider.BaseURL != "" || provider.APIKey != "" || provider.Model != "" || provider.Project != "" || provider.Location != ""
}

func providerIsComplete(provider config.ProviderConfig) bool {
	if provider.Model == "" {
		return false
	}
	if provider.Type == config.ProviderTypeVertex {
		return provider.Project != "" && provider.Location != ""
	}
	if provider.APIKey == "" {
		return false
	}
	if provider.Type == config.ProviderTypeAnthropic || provider.Type == config.ProviderTypeOpenAI {
		return true
	}
	return provider.BaseURL != ""
}

func resolvedConfigPath() string {
	if strings.TrimSpace(cfgFile) == "" {
		return config.ConfigFilePath()
	}
	return expandHomePath(cfgFile)
}

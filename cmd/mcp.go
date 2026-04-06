package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/output"
)

var (
	mcpAddTransport        string
	mcpAddURL              string
	mcpAddCommand          string
	mcpAddArgs             []string
	mcpAddEnv              []string
	mcpAddHeaders          []string
	mcpAddTimeoutMs        int
	mcpAddEnabledByDefault bool
	mcpAddOAuthClientID    string
	mcpAddOAuthSecret      string
	mcpAddOAuthCallbackPort int
	mcpAddOverwrite        bool
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage MCP servers",
}

type mcpListItem struct {
	Name             string `json:"name"`
	Transport        string `json:"transport"`
	URL              string `json:"url,omitempty"`
	Command          string `json:"command,omitempty"`
	EnabledByDefault bool   `json:"enabled_by_default"`
	OAuth            bool   `json:"oauth"`
	TokenCached      bool   `json:"token_cached"`
}

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured MCP servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.FromContext(cmd.Context())
		servers := cfg.Agent.MCPServers

		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)

		items := make([]mcpListItem, 0, len(names))
		for _, name := range names {
			srv := servers[name]
			item := mcpListItem{
				Name:             name,
				Transport:        srv.Transport,
				URL:              srv.URL,
				Command:          srv.Command,
				EnabledByDefault: srv.EnabledByDefault,
				OAuth:            srv.OAuth != nil,
				TokenCached:      hasCachedToken(name),
			}
			items = append(items, item)
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(items)
		}

		if len(items) == 0 {
			w.Line("No MCP servers configured.")
			return nil
		}

		for _, item := range items {
			status := "enabled"
			if !item.EnabledByDefault {
				status = "disabled"
			}

			authInfo := ""
			if item.OAuth {
				if item.TokenCached {
					authInfo = " [oauth: authenticated]"
				} else {
					authInfo = " [oauth: not authenticated]"
				}
			}

			if item.URL != "" {
				w.Line("%s [%s] %s%s", item.Name, status, item.URL, authInfo)
			} else {
				w.Line("%s [%s] %s %s%s", item.Name, status, item.Command, strings.Join(servers[item.Name].Args, " "), authInfo)
			}
		}
		return nil
	},
}

var mcpAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add an MCP server to ~/.nalvin/config.yaml",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])

		env := parseKeyValuePairs(mcpAddEnv)
		headers := parseKeyValuePairs(mcpAddHeaders)

		var oauth *config.MCPOAuthConfig
		oauthExplicit := cmd.Flags().Changed("oauth-client-id") || cmd.Flags().Changed("oauth-client-secret") || cmd.Flags().Changed("oauth-callback-port")
		if oauthExplicit {
			oauth = &config.MCPOAuthConfig{
				ClientID:     mcpAddOAuthClientID,
				ClientSecret: mcpAddOAuthSecret,
				CallbackPort: mcpAddOAuthCallbackPort,
			}
		}

		server := config.MCPServerConfig{
			Transport:        mcpAddTransport,
			URL:              mcpAddURL,
			Command:          mcpAddCommand,
			Args:             mcpAddArgs,
			Env:              env,
			Headers:          headers,
			TimeoutMs:        mcpAddTimeoutMs,
			EnabledByDefault: mcpAddEnabledByDefault,
			OAuth:            oauth,
		}

		if err := config.SaveMCPServer(cfgFile, name, server, mcpAddOverwrite); err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{
				"name":        name,
				"transport":   server.Transport,
				"config_path": resolvedConfigPath(),
			})
		}
		w.Line("Saved MCP server %s to %s", name, resolvedConfigPath())
		if oauth != nil {
			w.Line("Run: nalvin mcp auth %s", name)
		}
		return nil
	},
}

var mcpRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an MCP server from ~/.nalvin/config.yaml",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if err := config.DeleteMCPServer(cfgFile, name); err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{"name": name, "removed": true})
		}
		w.Line("Removed MCP server %s from %s", name, resolvedConfigPath())
		return nil
	},
}

var mcpAuthCmd = &cobra.Command{
	Use:   "auth <name>",
	Short: "Run interactive OAuth flow for an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		cfg, _ := config.FromContext(cmd.Context())

		serverCfg, ok := cfg.Agent.MCPServers[name]
		if !ok {
			return fmt.Errorf("mcp server %q not found in config", name)
		}
		if serverCfg.OAuth == nil {
			return fmt.Errorf("mcp server %q does not have OAuth configured", name)
		}

		result, err := agent.RunOAuthFlow(cmd.Context(), cmd.OutOrStdout(), name, serverCfg)
		if err != nil {
			return err
		}

		// Enable the server now that auth succeeded.
		if !serverCfg.EnabledByDefault {
			if err := config.EnableMCPServer(cfgFile, name); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Enabled %s by default.\n", name)
			}
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}
		return nil
	},
}

var mcpTestCmd = &cobra.Command{
	Use:   "test <name>",
	Short: "Connect to an MCP server and list its tools",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		cfg, _ := config.FromContext(cmd.Context())

		serverCfg, ok := cfg.Agent.MCPServers[name]
		if !ok {
			return fmt.Errorf("mcp server %q not found in config", name)
		}

		result, err := agent.TestMCPServer(cmd.Context(), cmd.OutOrStdout(), name, serverCfg)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}
		return nil
	},
}

func init() {
	mcpAddCmd.Flags().StringVar(&mcpAddTransport, "transport", "streamable_http", "transport type: streamable_http or stdio")
	mcpAddCmd.Flags().StringVar(&mcpAddURL, "url", "", "server URL (for streamable_http)")
	mcpAddCmd.Flags().StringVar(&mcpAddCommand, "command", "", "command to run (for stdio)")
	mcpAddCmd.Flags().StringSliceVar(&mcpAddArgs, "args", nil, "command arguments (for stdio, repeatable)")
	mcpAddCmd.Flags().StringSliceVar(&mcpAddEnv, "env", nil, "environment variables as KEY=VALUE (repeatable)")
	mcpAddCmd.Flags().StringSliceVar(&mcpAddHeaders, "header", nil, "HTTP headers as KEY=VALUE (repeatable)")
	mcpAddCmd.Flags().IntVar(&mcpAddTimeoutMs, "timeout-ms", 0, "request timeout in milliseconds")
	mcpAddCmd.Flags().BoolVar(&mcpAddEnabledByDefault, "enabled-by-default", true, "enable server by default for agent runs")
	mcpAddCmd.Flags().StringVar(&mcpAddOAuthClientID, "oauth-client-id", "", "OAuth client ID (enables OAuth for this server)")
	mcpAddCmd.Flags().StringVar(&mcpAddOAuthSecret, "oauth-client-secret", "", "OAuth client secret")
	mcpAddCmd.Flags().IntVar(&mcpAddOAuthCallbackPort, "oauth-callback-port", 9876, "local port for OAuth callback")
	mcpAddCmd.Flags().BoolVar(&mcpAddOverwrite, "overwrite", false, "replace an existing MCP server with the same name")

	mcpCmd.AddCommand(mcpListCmd)
	mcpCmd.AddCommand(mcpAddCmd)
	mcpCmd.AddCommand(mcpRemoveCmd)
	mcpCmd.AddCommand(mcpAuthCmd)
	mcpCmd.AddCommand(mcpTestCmd)
	rootCmd.AddCommand(mcpCmd)
}

func hasCachedToken(serverName string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	info, err := os.Stat(fmt.Sprintf("%s/.nalvin/oauth-tokens/%s.json", home, serverName))
	return err == nil && info.Size() > 0
}

func parseKeyValuePairs(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	result := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			result[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

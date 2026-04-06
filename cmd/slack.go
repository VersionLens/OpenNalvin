package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/slack-go/slack"
	"github.com/spf13/cobra"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/output"
)

var (
	slackSearchLimit        int
	slackSearchChannelTypes []string
	slackSearchContentTypes []string
	slackSearchIncludeBots  bool
)

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Manage Slack integration",
}

var slackAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Set Slack user token (xoxp-) for RTS search",
	RunE: func(cmd *cobra.Command, args []string) error {
		token, err := promptForSecret(cmd.InOrStdin(), cmd.ErrOrStderr(), "Slack user token (xoxp-): ")
		if err != nil {
			return fmt.Errorf("read token: %w", err)
		}
		if token == "" {
			return fmt.Errorf("token is required")
		}

		if err := config.SaveSlackUserToken(cfgFile, token); err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{
				"configured":  true,
				"config_path": resolvedConfigPath(),
			})
		}
		w.Line("Slack user token saved to %s", resolvedConfigPath())
		return nil
	},
}

var slackSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search Slack via RTS (assistant.search.context)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.FromContext(cmd.Context())
		token := cfg.Slack.UserToken
		if token == "" {
			return fmt.Errorf("slack user token not configured — run: nalvin slack auth")
		}

		query := strings.Join(args, " ")
		limit := slackSearchLimit
		if limit <= 0 {
			limit = 10
		}
		if limit > 20 {
			limit = 20
		}

		params := slack.AssistantSearchContextParameters{
			Query:       query,
			Limit:       limit,
			IncludeBots: slackSearchIncludeBots,
		}
		if len(slackSearchChannelTypes) > 0 {
			params.ChannelTypes = slackSearchChannelTypes
		}
		if len(slackSearchContentTypes) > 0 {
			params.ContentTypes = slackSearchContentTypes
		}

		client := slack.New(token)
		resp, err := client.SearchAssistantContextContext(context.Background(), params)
		if err != nil {
			return fmt.Errorf("slack RTS search: %w", err)
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(resp)
		}

		messages := resp.Results.Messages
		if len(messages) == 0 {
			w.Line("No results for %q", query)
			return nil
		}

		w.Line("Found %d result(s) for %q:\n", len(messages), query)
		for i, msg := range messages {
			content := msg.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			w.Line("[%d] @%s in #%s", i+1, msg.AuthorUserID, msg.ChannelID)
			w.Line("    %s", content)
			if msg.Permalink != "" {
				w.Line("    %s", msg.Permalink)
			}
			w.Line("")
		}

		if resp.ResponseMetadata.NextCursor != "" {
			w.Line("(more results available)")
		}
		return nil
	},
}

func init() {
	slackSearchCmd.Flags().IntVar(&slackSearchLimit, "limit", 10, "max results (1-20)")
	slackSearchCmd.Flags().StringSliceVar(&slackSearchChannelTypes, "channel-types", nil, "channel types: public_channel, private_channel, mpim, im")
	slackSearchCmd.Flags().StringSliceVar(&slackSearchContentTypes, "content-types", nil, "content types: messages, files, channels, users")
	slackSearchCmd.Flags().BoolVar(&slackSearchIncludeBots, "include-bots", false, "include bot messages")

	slackCmd.AddCommand(slackAuthCmd)
	slackCmd.AddCommand(slackSearchCmd)
	rootCmd.AddCommand(slackCmd)
}

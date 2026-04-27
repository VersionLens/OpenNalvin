package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/discordbot"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/cobra"
)

var discordCmd = &cobra.Command{
	Use:   "discord",
	Short: "Run the Discord bot",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		cfg, ok := config.FromContext(ctx)
		if !ok {
			return fmt.Errorf("config not found in command context")
		}
		cfg.Workspace.Current = discordbot.WorkspaceName

		db, _, err := workspacepkg.OpenDB(ctx, cfg, discordbot.WorkspaceName)
		if err != nil {
			return err
		}
		defer db.Close()

		if !cfg.Discord.Enabled {
			return fmt.Errorf("discord bot is disabled; set discord.enabled=true in config")
		}
		if cfg.Discord.Token == "" {
			return fmt.Errorf("discord token not configured")
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		svc, err := discordbot.New(cfg, knowledge.New(db), logger)
		if err != nil {
			return err
		}
		return svc.Run(config.WithContext(ctx, cfg))
	},
}

func init() {
	rootCmd.AddCommand(discordCmd)
}

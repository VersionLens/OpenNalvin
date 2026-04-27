package cmd

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	whatsapppkg "github.com/versionlens/OpenNalvin/internal/whatsapp"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/cobra"
)

var whatsAppServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the WhatsApp service as a long-running process",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		cfg, ok := config.FromContext(ctx)
		if !ok {
			return fmt.Errorf("config not found in command context")
		}
		if !cfg.WhatsApp.Enabled {
			return fmt.Errorf("whatsapp is disabled; set whatsapp.enabled=true in config")
		}

		cfg.Workspace.Current = cfg.WhatsApp.Workspace
		db, _, err := workspacepkg.OpenDB(ctx, cfg, cfg.WhatsApp.Workspace)
		if err != nil {
			return err
		}
		defer db.Close()

		level := slog.LevelInfo
		if strings.EqualFold(strings.TrimSpace(cfg.App.Env), "development") {
			level = slog.LevelDebug
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
		store := knowledge.New(db)
		service, err := whatsapppkg.NewService(cfg, store, logger, nil)
		if err != nil {
			return err
		}
		if err := service.Start(ctx); err != nil {
			return err
		}
		defer service.Close()

		addr := cfg.WhatsApp.ServeAddr
		logger.Info("whatsapp serve listening", "addr", addr)
		srv := &http.Server{Addr: addr, Handler: service.Handler()}
		go func() {
			<-ctx.Done()
			srv.Close()
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	},
}

func init() {
	whatsAppCmd.AddCommand(whatsAppServeCmd)
}

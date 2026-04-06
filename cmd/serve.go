package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/versionlens/OpenNalvin/internal/buildinfo"
	"github.com/versionlens/OpenNalvin/internal/server"
	webui "github.com/versionlens/OpenNalvin/web"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		assets := webui.Source()
		if !assets.Embedded {
			logger.Info("frontend assets not embedded; run `bun run build:web` and rebuild, or use `bun run dev:web` while developing")
		}

		appServer := server.New(cfg, buildinfo.Current(), assets, logger)
		defer func() {
			if err := appServer.Close(); err != nil {
				logger.Error("server workspace runtime close failed", "error", err)
			}
		}()
		if err := appServer.Start(ctx); err != nil {
			return err
		}

		srv := &http.Server{
			Addr:              cfg.Server.Addr,
			Handler:           appServer.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		}

		go func() {
			<-ctx.Done()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := srv.Shutdown(shutdownCtx); err != nil {
				logger.Error("server shutdown failed", "error", err)
			}
		}()

		logger.Info("server listening", "addr", cfg.Server.Addr, "workspace", paths.Name)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen and serve: %w", err)
		}

		return nil
	},
}

func init() {
	serveCmd.Flags().String("addr", "", "HTTP listen address")
	_ = viper.BindPFlag("server.addr", serveCmd.Flags().Lookup("addr"))
	rootCmd.AddCommand(serveCmd)
}

package cmd

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/output"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//go:embed config.default.yaml
var defaultConfig []byte

var cfgFile string
var outputJSON bool
var workspaceName string

var rootCmd = &cobra.Command{
	Use:           "nalvin",
	Short:         "Composable local AI tooling and services",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		ensureExecutableSearchPath()

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		paths, err := workspacepkg.ResolvePaths(cfg, workspaceName)
		if err != nil {
			return err
		}

		ctx := config.WithContext(cmd.Context(), cfg)
		ctx = workspacepkg.WithPaths(ctx, paths)
		ctx = output.WithContext(ctx, output.New(cmd.OutOrStdout(), outputJSON))
		cmd.SetContext(ctx)
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(func() {
		config.SetDefaultConfig(bytes.NewReader(defaultConfig))
		if err := config.Init(cfgFile); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	})

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.nalvin/config.yaml)")
	rootCmd.PersistentFlags().String("env", "", "application environment")
	rootCmd.PersistentFlags().StringVarP(&workspaceName, "workspace", "w", "", "workspace override for this command")
	rootCmd.PersistentFlags().BoolVar(&outputJSON, "json", false, "render command output as JSON when supported")

	_ = viper.BindPFlag("app.env", rootCmd.PersistentFlags().Lookup("env"))
}

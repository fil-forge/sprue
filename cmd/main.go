package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"github.com/fil-forge/sprue/cmd/client"
	"github.com/fil-forge/sprue/cmd/identity"
	"github.com/fil-forge/sprue/internal/config"
	appfx "github.com/fil-forge/sprue/internal/fx"
)

var cfgFile string

func main() {
	rootCmd := &cobra.Command{
		Use:   "sprue",
		Short: "Sprue upload service for Storacha local development",
		Long: `Sprue is the upload coordination service for Storacha local development.
Routes blob allocations to Piri nodes and tracks upload state in DynamoDB.`,
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the sprue service",
		RunE:  runServe,
	}

	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(client.Cmd)
	rootCmd.AddCommand(identity.Cmd)

	// Global flags
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: looks for config.yaml in current dir)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgFile)
	cobra.CheckErr(err)

	app := fx.New(
		appfx.AppModule(cfg),
		// Suppress fx's default logging and use our own zap logger
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),
	)
	app.Run()

	return nil
}

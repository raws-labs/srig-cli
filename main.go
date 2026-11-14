package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/raws-labs/srig-cli/client"
	"github.com/raws-labs/srig-cli/cmd"
	"github.com/raws-labs/srig-cli/output"
	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	flagAPIKey  string
	flagBaseURL string
	flagJSON    bool
	c           *client.Client
)

var rootCmd = &cobra.Command{
	Use:   "srig",
	Short: "siliconrig — remote access to real embedded hardware",
	Long:  "CLI for the siliconrig Hardware-as-a-Service platform.\nFlash firmware, open serial terminals, and run CI/CD tests on real embedded devices.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if flagAPIKey == "" {
			flagAPIKey = os.Getenv("SRIG_API_KEY")
		}
		// Public commands — skip auth requirement.
		if cmd.Name() == "status" || cmd.Name() == "version" {
			c = client.New(flagBaseURL, flagAPIKey)
			return nil
		}
		if flagAPIKey == "" {
			return fmt.Errorf("no API key — set SRIG_API_KEY or use --api-key")
		}
		c = client.New(flagBaseURL, flagAPIKey)
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key (env: SRIG_API_KEY)")
	rootCmd.PersistentFlags().StringVar(&flagBaseURL, "base-url", envOr("SRIG_BASE_URL", "https://api.srig.io"), "API base URL (env: SRIG_BASE_URL)")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	rootCmd.AddCommand(cmd.NewStatusCmd(&c, &flagJSON))
	rootCmd.AddCommand(cmd.NewSessionCmd(&c, &flagJSON, &flagBaseURL))
	rootCmd.AddCommand(cmd.NewFlashCmd(&c, &flagJSON))
	rootCmd.AddCommand(cmd.NewRunCmd(&c, &flagJSON))
	rootCmd.AddCommand(cmd.NewRecordCmd(&c, &flagJSON))
	rootCmd.AddCommand(cmd.NewSerialCmd(&c, &flagJSON))
	rootCmd.AddCommand(cmd.NewPowerCycleCmd(&c, &flagJSON))
	rootCmd.AddCommand(cmd.NewWhoamiCmd(&c, &flagJSON))
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("srig " + Version)
		},
	})

	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		var ee *cmd.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.Code) // run already printed its own output
		}
		output.Error(err.Error())
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

package cmd

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var (
	verbose bool
	quiet   bool
	Version = "dev"
)

var rootCmd = &cobra.Command{
	Use:           "hccdn-cli",
	Short:         "Upload and manage files on the Hack Club CDN",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	// A missing .env file is normal; environment variables still work.
	_ = godotenv.Load()
	rootCmd.Version = Version
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed activity")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Only print errors or requested JSON")
	rootCmd.MarkFlagsMutuallyExclusive("verbose", "quiet")
}

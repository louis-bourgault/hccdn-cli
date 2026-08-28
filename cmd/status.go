package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/louis-bourgault/hccdn-cli/cdn"
	"github.com/louis-bourgault/hccdn-cli/db"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local database and CDN account status",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		database, err := db.GetDB()
		if err != nil {
			return err
		}
		stats, err := database.Stats()
		if err != nil {
			return err
		}
		result := struct {
			Database string       `json:"database"`
			Local    db.Stats     `json:"local"`
			Account  *cdn.Account `json:"account,omitempty"`
		}{Database: database.Path(), Local: stats}
		apiKey := os.Getenv("HCCDN_API_KEY")
		if apiKey != "" {
			client := cdn.NewClient(apiKey, os.Getenv("HCCDN_API_URL"), 30*time.Second, 1)
			result.Account, err = client.Me(ctx)
			if err != nil {
				return fmt.Errorf("get CDN account: %w", err)
			}
		}
		asJSON, err := cmd.Flags().GetBool("json")
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Database: %s\nSessions: %d\nActive uploads: %d\nActive references: %d\n",
			result.Database, stats.Sessions, stats.ActiveUploads, stats.ActiveReferences)
		if result.Account == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "CDN account: unavailable (HCCDN_API_KEY is not set)")
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "CDN account: %s (%s)\nQuota: %s / %s (%s)\n", result.Account.Name,
				result.Account.Email, humanBytes(result.Account.StorageUsed), humanBytes(result.Account.StorageLimit), result.Account.QuotaTier)
		}
		return nil
	},
}

func humanBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := int64(unit), 0
	for quotient := value / unit; quotient >= unit; quotient /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent])
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().Bool("json", false, "Print JSON")
}

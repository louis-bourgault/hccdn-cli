package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/louis-bourgault/hccdn-cli/db"
	"github.com/spf13/cobra"
)

var historyCmd = &cobra.Command{
	Use:   "history [session-id]",
	Short: "Show recent upload sessions or inspect one session",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, err := db.GetDB()
		if err != nil {
			return err
		}
		asJSON, err := cmd.Flags().GetBool("json")
		if err != nil {
			return err
		}
		if len(args) == 1 {
			session, found, err := database.Session(args[0])
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("session %q was not found", args[0])
			}
			uploads, err := database.SessionUploads(session.ID)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd, struct {
					Session any `json:"session"`
					Uploads any `json:"uploads"`
				}{session, uploads})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Session %s (%s)\nCreated: %s\nFrom: %s\nCommand: %s\nUploaded: %d  Reused: %d\n\n",
				session.ID, session.Status, session.CreatedAt, session.FromDir, session.CommandText, session.Uploaded, session.Reused)
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "STATE\tVARIANT\tSIZE\tSOURCE\tURL")
			for _, upload := range uploads {
				state := "uploaded"
				if upload.Reused {
					state = "reused"
				}
				if upload.Removed {
					state = "removed/" + state
				}
				fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n", state, displayVariant(upload.VariantKey), upload.Size, upload.FileLoc, upload.URL)
			}
			return writer.Flush()
		}

		limit, err := cmd.Flags().GetInt("limit")
		if err != nil {
			return err
		}
		if limit < 1 {
			return fmt.Errorf("limit must be positive")
		}
		sessions, err := database.History(limit)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(cmd, sessions)
		}
		if len(sessions) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No upload sessions yet.")
			return nil
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "SESSION\tCREATED\tSTATUS\tUPLOADED\tREUSED\tFROM")
		for _, session := range sessions {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%d\t%s\n", session.ID, session.CreatedAt, session.Status,
				session.Uploaded, session.Reused, session.FromDir)
		}
		return writer.Flush()
	},
}

func displayVariant(key string) string {
	if key == "" {
		return "legacy"
	}
	if strings.HasPrefix(key, "original:") {
		return "none"
	}
	if strings.HasSuffix(key, ":full") {
		return "full"
	}
	if index := strings.LastIndex(key, "max="); index >= 0 {
		return key[index+4:]
	}
	return key
}

func writeJSON(cmd *cobra.Command, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return nil
}

func init() {
	rootCmd.AddCommand(historyCmd)
	historyCmd.Flags().IntP("limit", "n", 20, "Number of recent sessions")
	historyCmd.Flags().Bool("json", false, "Print JSON")
}

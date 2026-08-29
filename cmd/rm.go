package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/louis-bourgault/hccdn-cli/cdn"
	"github.com/louis-bourgault/hccdn-cli/db"
	"github.com/spf13/cobra"
)

type removalKind int

const (
	removeAll removalKind = iota
	removeSession
	removePath
)

var rmCmd = &cobra.Command{
	Use:   "rm <all|session-id|file-or-directory>",
	Short: "Delete uploads from the CDN",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemove,
}

func runRemove(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	database, err := db.GetDB()
	if err != nil {
		return err
	}
	target := args[0]
	kind := removePath
	sessionID := ""
	path := ""
	directory := false
	var candidates []db.RemovalCandidate

	if target == "all" {
		kind = removeAll
		candidates, err = database.RemovalCandidatesAll()
	} else if _, found, sessionErr := database.Session(target); sessionErr != nil {
		return sessionErr
	} else if found {
		kind = removeSession
		sessionID = target
		candidates, err = database.RemovalCandidatesForSession(target)
	} else {
		path, err = filepath.Abs(filepath.Clean(target))
		if err != nil {
			return err
		}
		if info, statErr := os.Stat(path); statErr == nil {
			directory = info.IsDir()
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		candidates, err = database.RemovalCandidatesForPath(path, directory)
		if err == nil && len(candidates) == 0 && !directory {
			// A deleted local directory can still be selected from database paths.
			directory = true
			candidates, err = database.RemovalCandidatesForPath(path, true)
		}
	}
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		if kind == removeSession {
			if err := database.MarkSessionRemoved(sessionID); err != nil {
				return err
			}
		}
		if !quiet {
			fmt.Fprintln(cmd.OutOrStdout(), "Nothing to remove.")
		}
		return nil
	}

	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return err
	}
	if kind == removeAll && !dryRun && !yes {
		confirmed, err := confirmAll(cmd, len(candidates))
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New("removal cancelled")
		}
	}
	if dryRun {
		for _, candidate := range candidates {
			action := "delete from CDN"
			if candidate.OtherReferences > 0 {
				action = fmt.Sprintf("keep on CDN (%d other references)", candidate.OtherReferences)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", action, candidate.Upload.ID, candidate.Upload.URL)
		}
		return nil
	}

	apiKey := os.Getenv("HCCDN_API_KEY")
	if apiKey == "" {
		return errors.New("HCCDN_API_KEY is not set")
	}
	retries, err := cmd.Flags().GetInt("retries")
	if err != nil {
		return err
	}
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return err
	}
	if retries < 0 {
		return errors.New("retries cannot be negative")
	}
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	client := cdn.NewClient(apiKey, os.Getenv("HCCDN_API_URL"), timeout, retries)
	remoteDeleted, preserved := 0, 0
	var failures []error
	for _, candidate := range candidates {
		deleted := candidate.OtherReferences == 0
		if deleted {
			err = client.Delete(ctx, candidate.Upload.ID)
			var statusErr *cdn.StatusError
			if err != nil && !(errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound) {
				failures = append(failures, fmt.Errorf("delete %s: %w", candidate.Upload.ID, err))
				continue
			}
			remoteDeleted++
		} else {
			preserved++
		}

		switch kind {
		case removeAll:
			err = database.RemoveAllReferences(candidate.Upload.ID, deleted)
		case removeSession:
			err = database.RemoveSessionReference(sessionID, candidate.Upload.ID, deleted)
		case removePath:
			err = database.RemovePathReferences(path, directory, candidate.Upload.ID, deleted)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("update local record %s: %w", candidate.Upload.ID, err))
			continue
		}
		if verbose {
			action := "deleted"
			if !deleted {
				action = "unlinked (still referenced)"
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", action, candidate.Upload.URL)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d removal(s) failed: %w", len(failures), errors.Join(failures...))
	}
	if kind == removeSession {
		if err := database.MarkSessionRemoved(sessionID); err != nil {
			return err
		}
	} else if kind == removeAll {
		if err := database.MarkAllSessionsRemoved(); err != nil {
			return err
		}
	}
	if !quiet {
		fmt.Fprintf(cmd.OutOrStdout(), "%d deleted from CDN, %d preserved because they are still referenced.\n", remoteDeleted, preserved)
	}
	return nil
}

func confirmAll(cmd *cobra.Command, count int) (bool, error) {
	if !isTerminal(cmd.InOrStdin()) {
		return false, errors.New("rm all requires --yes when input is not interactive")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Delete %d uploads from the CDN? [y/N] ", count)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func init() {
	rootCmd.AddCommand(rmCmd)
	rmCmd.Flags().Bool("dry-run", false, "Show what would be removed")
	rmCmd.Flags().BoolP("yes", "y", false, "Confirm rm all without prompting")
	rmCmd.Flags().Int("retries", 2, "Retries for transient CDN failures")
	rmCmd.Flags().Duration("timeout", 30*time.Second, "Timeout for each CDN request")
}

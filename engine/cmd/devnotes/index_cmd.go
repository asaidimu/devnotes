package main

import (
	"fmt"

	"github.com/asaidimu/devnotes/engine/internal/index"
	"github.com/spf13/cobra"
)

func newIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Manage the workspace note index (.devnotes/index.json)",
	}
	cmd.AddCommand(newIndexInitCmd())
	cmd.AddCommand(newIndexUpdateCmd())
	cmd.AddCommand(newIndexStatusCmd())
	return cmd
}

func newIndexInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scan the workspace and write a fresh index",
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, diff, err := index.Build(flags.root)
			if err != nil {
				return err
			}
			if err := index.Save(idx, flags.indexPath); err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(diff)
			}
			fmt.Printf("indexed %d file(s), %d note(s) -> %s\n", len(diff.ScannedFiles), len(idx.Notes), flags.indexPath)
			for _, w := range diff.Warnings {
				warn("%s", w)
			}
			return nil
		},
	}
}

func newIndexUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [paths...]",
		Short: "Re-sync the index from source for changed files (or the whole workspace)",
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := index.Load(flags.indexPath)
			if err != nil {
				return fmt.Errorf("loading index (run `devnotes index init` first): %w", err)
			}
			diff, err := index.Update(idx, args)
			if err != nil {
				return err
			}
			if err := index.Save(idx, flags.indexPath); err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(diff)
			}
			fmt.Printf("scanned %d file(s): %d added, %d changed, %d removed\n",
				len(diff.ScannedFiles), len(diff.AddedNotes), len(diff.ChangedNotes), len(diff.RemovedNotes))
			for _, id := range diff.AddedNotes {
				fmt.Println("  + " + id)
			}
			for _, id := range diff.ChangedNotes {
				fmt.Println("  ~ " + id)
			}
			for _, id := range diff.RemovedNotes {
				fmt.Println("  - " + id)
			}
			for _, w := range diff.Warnings {
				warn("%s", w)
			}
			return nil
		},
	}
}

func newIndexStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the index is stale relative to the working tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := index.Load(flags.indexPath)
			if err != nil {
				return fmt.Errorf("loading index (run `devnotes index init` first): %w", err)
			}
			stale, missing, untracked := index.Status(idx)
			if flags.jsonOut {
				return printJSON(map[string]any{
					"stale":     stale,
					"missing":   missing,
					"untracked": untracked,
				})
			}
			if len(stale) == 0 && len(missing) == 0 && len(untracked) == 0 {
				fmt.Println("index is up to date")
				return nil
			}
			for _, f := range stale {
				fmt.Println("stale:     " + f)
			}
			for _, f := range missing {
				fmt.Println("missing:   " + f + " (indexed but no longer on disk)")
			}
			for _, f := range untracked {
				fmt.Println("untracked: " + f + " (on disk but not indexed)")
			}
			return nil
		},
	}
}

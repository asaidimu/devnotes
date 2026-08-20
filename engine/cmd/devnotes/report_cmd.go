package main

import (
	"fmt"
	"sort"

	"github.com/asaidimu/devnotes/engine/internal/index"
	"github.com/spf13/cobra"
)

func newReportCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Summarize the note backlog (counts by category/status/priority/assignee)",
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := index.Load(flags.indexPath)
			if err != nil {
				return fmt.Errorf("loading index (run `devnotes index init` first): %w", err)
			}
			switch by {
			case "category", "status", "priority", "assignee":
			default:
				return fail("--by must be category, status, priority, or assignee")
			}
			counts := map[string]int{}
			for _, e := range idx.Notes {
				k := reportKey(e, by)
				counts[k]++
			}
			if flags.jsonOut {
				return printJSON(map[string]any{
					"total":  len(idx.Notes),
					"by":     by,
					"counts": counts,
				})
			}
			var keys []string
			for k := range counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Printf("%d note(s) total, by %s:\n", len(idx.Notes), by)
			for _, k := range keys {
				fmt.Printf("  %-20s %d\n", k, counts[k])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "category", "group counts by category|status|priority|assignee")
	return cmd
}

func reportKey(e index.Entry, by string) string {
	switch by {
	case "category":
		return e.Category
	case "status":
		return e.Status
	case "priority":
		if e.Priority == "" {
			return "(none)"
		}
		return e.Priority
	case "assignee":
		if e.Assignee == "" {
			return "(unassigned)"
		}
		return e.Assignee
	}
	return "(all)"
}

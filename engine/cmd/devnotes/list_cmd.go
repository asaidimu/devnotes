package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/index"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var category, status, priority, tag, author, assignee, groupBy string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List indexed notes, filtered by category/status/priority/tag/author/assignee",
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := index.Load(flags.indexPath)
			if err != nil {
				return fmt.Errorf("loading index (run `devnotes index init` first): %w", err)
			}
			entries := filterEntries(idx, filters{
				category: category, status: status, priority: priority,
				tag: tag, author: author, assignee: assignee,
			})
			sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

			if flags.jsonOut {
				return printJSON(entries)
			}
			if len(entries) == 0 {
				fmt.Println("no notes match")
				return nil
			}
			if groupBy != "" {
				printGrouped(entries, groupBy)
				return nil
			}
			for _, e := range entries {
				printEntryLine(e)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "filter by category")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&priority, "priority", "", "filter by priority")
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag (without #)")
	cmd.Flags().StringVar(&author, "author", "", "filter by author")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee")
	cmd.Flags().StringVar(&groupBy, "group-by", "", "group output by category|status|priority|assignee")
	return cmd
}

type filters struct {
	category, status, priority, tag, author, assignee string
}

func filterEntries(idx *index.Index, f filters) []index.Entry {
	var out []index.Entry
	for _, e := range idx.Notes {
		if f.category != "" && !strings.EqualFold(e.Category, f.category) {
			continue
		}
		if f.status != "" && !strings.EqualFold(e.Status, f.status) {
			continue
		}
		if f.priority != "" && !strings.EqualFold(e.Priority, f.priority) {
			continue
		}
		if f.tag != "" && !hasTag(e.Tags, f.tag) {
			continue
		}
		if f.author != "" && !hasStr(e.Authors, f.author) {
			continue
		}
		if f.assignee != "" && !strings.EqualFold(e.Assignee, f.assignee) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func hasTag(tags []string, want string) bool {
	want = strings.TrimPrefix(want, "#")
	for _, t := range tags {
		if strings.EqualFold(strings.TrimPrefix(t, "#"), want) {
			return true
		}
	}
	return false
}

func hasStr(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func printEntryLine(e index.Entry) {
	meta := e.Category
	if e.Priority != "" {
		meta += " " + e.Priority
	}
	meta += " " + e.Status
	if e.Assignee != "" {
		meta += " @" + e.Assignee
	}
	fmt.Printf("#%-28s [%s] %s\n", e.ID, meta, e.Title)
	fmt.Printf("%-30s  %s:%d\n", "", e.Location.File, e.Location.StartLine+1)
}

func printGrouped(entries []index.Entry, groupBy string) {
	groups := map[string][]index.Entry{}
	keyFn := func(e index.Entry) string {
		switch groupBy {
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
		default:
			return "(all)"
		}
	}
	var keys []string
	for _, e := range entries {
		k := keyFn(e)
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], e)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("== %s (%d) ==\n", k, len(groups[k]))
		for _, e := range groups[k] {
			printEntryLine(e)
		}
		fmt.Println()
	}
}

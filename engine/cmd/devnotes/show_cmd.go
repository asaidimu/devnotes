package main

import (
	"fmt"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/index"
	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show full detail for one note from the index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := index.Load(flags.indexPath)
			if err != nil {
				return fmt.Errorf("loading index (run `devnotes index init` first): %w", err)
			}
			id := strings.TrimPrefix(args[0], "#")
			e, ok := idx.Notes[id]
			if !ok {
				return fail("note %q not found in index", id)
			}
			if flags.jsonOut {
				return printJSON(e)
			}
			printShow(e)
			return nil
		},
	}
	return cmd
}

func printShow(e index.Entry) {
	fmt.Printf("#%s\n", e.ID)
	fmt.Printf("  category:  %s\n", e.Category)
	fmt.Printf("  status:    %s\n", e.Status)
	if e.Priority != "" {
		fmt.Printf("  priority:  %s\n", e.Priority)
	}
	if e.Timestamp != "" {
		fmt.Printf("  timestamp: %s\n", e.Timestamp)
	}
	if len(e.Tags) > 0 {
		fmt.Printf("  tags:      %s\n", strings.Join(e.Tags, ", "))
	}
	if len(e.Authors) > 0 {
		fmt.Printf("  authors:   %s\n", strings.Join(e.Authors, ", "))
	}
	if e.Assignee != "" {
		fmt.Printf("  assignee:  %s\n", e.Assignee)
	}
	fmt.Printf("  location:  %s:%d\n", e.Location.File, e.Location.StartLine+1)
	fmt.Printf("  title:     %s\n", e.Title)
	if e.Body != "" {
		fmt.Println("  body:")
		for _, line := range strings.Split(e.Body, "\n") {
			fmt.Println("    " + line)
		}
	}
	if len(e.References) > 0 {
		fmt.Printf("  see:       %s\n", strings.Join(e.References, ", "))
	}
	if len(e.Extensions) > 0 {
		fmt.Println("  extensions:")
		for k, v := range e.Extensions {
			fmt.Printf("    @%s %s\n", k, v)
		}
	}
}

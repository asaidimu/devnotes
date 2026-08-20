package main

import (
	"fmt"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/index"
	"github.com/asaidimu/devnotes/engine/internal/noteedit"
	"github.com/spf13/cobra"
)

func newNoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note",
		Short: "Capture and update DevNotes directly in source (source is the record of truth)",
	}
	cmd.AddCommand(newNoteAddCmd())
	cmd.AddCommand(newNoteClaimCmd())
	cmd.AddCommand(newNoteResolveCmd())
	cmd.AddCommand(newNoteStatusCmd())
	cmd.AddCommand(newNotePriorityCmd())
	return cmd
}

func newNoteAddCmd() *cobra.Command {
	var (
		line     int
		id       string
		category string
		status   string
		priority string
		ts       string
		tags     string
		title    string
		body     string
		author   string
		see      string
	)
	cmd := &cobra.Command{
		Use:   "add <file>",
		Short: "Add a new note as a comment in <file>",
		Long: "Add a new note as a comment in <file>, right before --line (or at the\n" +
			"end of the file if --line is omitted). This is the low-friction capture\n" +
			"command: a reviewer flagging a bug, an experimenter jotting a todo, or\n" +
			"CI/QA flagging a problem programmatically all use this same command.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := args[0]
			if category == "" || title == "" {
				return fail("--category and --title are required")
			}
			opts := noteedit.AddOptions{
				ID:        id,
				Category:  category,
				Status:    status,
				Priority:  priority,
				Timestamp: ts,
				Tags:      splitCSV(tags),
				Title:     title,
				Body:      body,
				Author:    author,
				See:       splitCSV(see),
			}
			newID, err := noteedit.Add(file, line, opts)
			if err != nil {
				return err
			}
			resyncIndexForFile(file)
			if flags.jsonOut {
				return printJSON(map[string]string{"id": newID, "file": file})
			}
			fmt.Printf("added #%s to %s\n", newID, file)
			return nil
		},
	}
	cmd.Flags().IntVar(&line, "line", 0, "1-based line to insert before (default: end of file)")
	cmd.Flags().StringVar(&id, "id", "", "explicit note ID (default: generated from --title)")
	cmd.Flags().StringVar(&category, "category", "", "note category: observation|todo|issue|context|lesson|prompt|<custom> (required)")
	cmd.Flags().StringVar(&status, "status", "", "status: open|resolved|wontfix|deprecated (default: open)")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: P0|P1|P2|P3")
	cmd.Flags().StringVar(&ts, "timestamp", "", "ISO 8601 timestamp")
	cmd.Flags().StringVar(&tags, "tags", "", "comma-separated tags, with or without leading #")
	cmd.Flags().StringVar(&title, "title", "", "note title (required)")
	cmd.Flags().StringVar(&body, "body", "", "note body text (may be multi-line)")
	cmd.Flags().StringVar(&author, "author", "", "author to record via @author")
	cmd.Flags().StringVar(&see, "see", "", "comma-separated @see targets (#id, url, or other)")
	return cmd
}

func newNoteClaimCmd() *cobra.Command {
	var file, assignee string
	cmd := &cobra.Command{
		Use:   "claim <id>",
		Short: "Assign a note to someone (writes an @assignee directive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if assignee == "" {
				return fail("--assignee is required")
			}
			id := args[0]
			f, err := resolveFile(id, file)
			if err != nil {
				return err
			}
			if err := noteedit.SetExtension(f, id, "assignee", assignee); err != nil {
				return err
			}
			resyncIndexForFile(f)
			if flags.jsonOut {
				return printJSON(map[string]string{"id": id, "file": f, "assignee": assignee})
			}
			fmt.Printf("#%s assigned to %s\n", id, assignee)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file the note lives in (skip index lookup)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "who to assign the note to (required)")
	return cmd
}

func newNoteResolveCmd() *cobra.Command {
	var file, body string
	cmd := &cobra.Command{
		Use:   "resolve <id>",
		Short: "Mark a note resolved, optionally appending a closing note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			f, err := resolveFile(id, file)
			if err != nil {
				return err
			}
			if err := noteedit.SetStatus(f, id, "resolved"); err != nil {
				return err
			}
			if body != "" {
				if err := noteedit.AppendBody(f, id, body); err != nil {
					return err
				}
			}
			resyncIndexForFile(f)
			if flags.jsonOut {
				return printJSON(map[string]string{"id": id, "file": f, "status": "resolved"})
			}
			fmt.Printf("#%s marked resolved\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file the note lives in (skip index lookup)")
	cmd.Flags().StringVar(&body, "body", "", "closing note appended to the note's body")
	return cmd
}

func newNoteStatusCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "status <id> <open|resolved|wontfix|deprecated>",
		Short: "Set a note's status directly (general-purpose lifecycle transition)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, status := args[0], args[1]
			switch status {
			case "open", "resolved", "wontfix", "deprecated":
			default:
				return fail("invalid status %q: must be open, resolved, wontfix, or deprecated", status)
			}
			f, err := resolveFile(id, file)
			if err != nil {
				return err
			}
			if err := noteedit.SetStatus(f, id, status); err != nil {
				return err
			}
			resyncIndexForFile(f)
			if flags.jsonOut {
				return printJSON(map[string]string{"id": id, "file": f, "status": status})
			}
			fmt.Printf("#%s status -> %s\n", id, status)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file the note lives in (skip index lookup)")
	return cmd
}

func newNotePriorityCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "priority <id> <P0|P1|P2|P3>",
		Short: "Reprioritize a note",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, pri := args[0], args[1]
			f, err := resolveFile(id, file)
			if err != nil {
				return err
			}
			if err := noteedit.SetPriority(f, id, pri); err != nil {
				return err
			}
			resyncIndexForFile(f)
			if flags.jsonOut {
				return printJSON(map[string]string{"id": id, "file": f, "priority": pri})
			}
			fmt.Printf("#%s priority -> %s\n", id, pri)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file the note lives in (skip index lookup)")
	return cmd
}

// resolveFile finds the file a note ID lives in: explicit --file wins;
// otherwise the index is consulted; otherwise the whole workspace is
// scanned as a last resort (slower, but works without an index).
func resolveFile(id, explicitFile string) (string, error) {
	if explicitFile != "" {
		return explicitFile, nil
	}
	if idx, err := index.Load(flags.indexPath); err == nil {
		if e, ok := idx.Notes[strings.TrimPrefix(id, "#")]; ok {
			return e.Location.File, nil
		}
	}
	found, err := noteedit.Find(index.CollectFiles([]string{flags.root}), id)
	if err != nil {
		return "", fail("could not locate note %q: pass --file explicitly, or run `devnotes index init`", id)
	}
	return found.File, nil
}

// resyncIndexForFile updates the index for a single file after a mutation,
// if an index exists. Missing index is not an error here -- add/claim/
// resolve work fine without one; the user just won't see it reflected
// until they run `devnotes index init`.
func resyncIndexForFile(file string) {
	idx, err := index.Load(flags.indexPath)
	if err != nil {
		return
	}
	if _, err := index.Update(idx, []string{file}); err != nil {
		warn("index update after edit: %v", err)
		return
	}
	if err := index.Save(idx, flags.indexPath); err != nil {
		warn("saving index after edit: %v", err)
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

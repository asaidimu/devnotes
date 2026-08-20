package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/index"
	"github.com/spf13/cobra"
)

type traceNode struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Category  string `json:"category"`
	Status    string `json:"status"`
	Direction string `json:"direction"` // "out" or "in" relative to the root
	Depth     int    `json:"depth"`
	Dangling  bool   `json:"dangling"` // referenced but not present in the index
}

func newTraceCmd() *cobra.Command {
	var depth int
	var direction string
	cmd := &cobra.Command{
		Use:   "trace <id>",
		Short: "Walk the @see reference graph out from (or into) a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := index.Load(flags.indexPath)
			if err != nil {
				return fmt.Errorf("loading index (run `devnotes index init` first): %w", err)
			}
			id := strings.TrimPrefix(args[0], "#")
			if _, ok := idx.Notes[id]; !ok {
				return fail("note %q not found in index", id)
			}
			switch direction {
			case "out", "in", "both":
			default:
				return fail("--direction must be out, in, or both")
			}

			outAdj, inAdj := buildAdjacency(idx)
			var nodes []traceNode
			if direction == "out" || direction == "both" {
				nodes = append(nodes, walk(idx, outAdj, id, depth, "out")...)
			}
			if direction == "in" || direction == "both" {
				nodes = append(nodes, walk(idx, inAdj, id, depth, "in")...)
			}

			if flags.jsonOut {
				return printJSON(nodes)
			}
			if len(nodes) == 0 {
				fmt.Printf("#%s has no %s references\n", id, direction)
				return nil
			}
			for _, n := range nodes {
				arrow := "->"
				if n.Direction == "in" {
					arrow = "<-"
				}
				indent := strings.Repeat("  ", n.Depth)
				if n.Dangling {
					fmt.Printf("%s%s #%s (unresolved reference)\n", indent, arrow, n.ID)
					continue
				}
				fmt.Printf("%s%s #%s [%s/%s] %s\n", indent, arrow, n.ID, n.Category, n.Status, n.Title)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&depth, "depth", 2, "how many hops to follow")
	cmd.Flags().StringVar(&direction, "direction", "out", "out (this note references), in (notes referencing this one), or both")
	return cmd
}

// buildAdjacency returns outward (references this note makes) and inward
// (references made to this note) adjacency maps, keyed by bare note ID.
// References that don't point at a "#id" (e.g. bare URLs) are ignored for
// graph-walking purposes.
func buildAdjacency(idx *index.Index) (out, in map[string][]string) {
	out = map[string][]string{}
	in = map[string][]string{}
	for id, e := range idx.Notes {
		for _, ref := range e.References {
			if !strings.HasPrefix(ref, "#") {
				continue
			}
			target := strings.TrimPrefix(ref, "#")
			out[id] = append(out[id], target)
			in[target] = append(in[target], id)
		}
	}
	return
}

func walk(idx *index.Index, adj map[string][]string, root string, maxDepth int, direction string) []traceNode {
	var out []traceNode
	visited := map[string]bool{root: true}
	frontier := []string{root}
	for d := 1; d <= maxDepth && len(frontier) > 0; d++ {
		var next []string
		var ids []string
		for _, cur := range frontier {
			ids = append(ids, adj[cur]...)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if visited[id] {
				continue
			}
			visited[id] = true
			e, ok := idx.Notes[id]
			if !ok {
				out = append(out, traceNode{ID: id, Direction: direction, Depth: d, Dangling: true})
				continue
			}
			out = append(out, traceNode{
				ID: id, Title: e.Title, Category: e.Category, Status: e.Status,
				Direction: direction, Depth: d,
			})
			next = append(next, id)
		}
		frontier = next
	}
	return out
}

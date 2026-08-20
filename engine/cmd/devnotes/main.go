// Command devnotes validates DevNotes in host source files and standalone
// .dn files, maintains a workspace-wide JSON index of notes, and provides
// commands to query, trace, capture, and mutate notes in place so that
// project-management workflows (triage, assignment, resolution) can live
// directly alongside the code they describe.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const version = "0.2.0"

// globalFlags holds flag values shared across subcommands.
type globalFlags struct {
	indexPath string
	root      string
	jsonOut   bool
}

var flags globalFlags

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "devnotes",
		Short: "Validate, index, and work with DevNotes embedded in source code",
		Long: "devnotes validates DevNotes comments in source files, maintains a\n" +
			"workspace-wide index (.devnotes/index.json), and provides commands to\n" +
			"capture, find, trace, and update notes -- so project-management state\n" +
			"(todos, issues, decisions, assignments) can live next to the code it\n" +
			"describes instead of in a separate tracker.",
		SilenceUsage: true,
		Version:      version,
	}
	root.PersistentFlags().StringVar(&flags.indexPath, "index", ".devnotes/index.json", "path to the index file")
	root.PersistentFlags().StringVar(&flags.root, "root", ".", "workspace root to scan")
	root.PersistentFlags().BoolVar(&flags.jsonOut, "json", false, "emit machine-readable JSON instead of human-readable text")

	root.AddCommand(newCheckCmd())
	root.AddCommand(newIndexCmd())
	root.AddCommand(newNoteCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newShowCmd())
	root.AddCommand(newTraceCmd())
	root.AddCommand(newReportCmd())
	return root
}

func fail(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

func warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "devnotes: "+format+"\n", a...)
}

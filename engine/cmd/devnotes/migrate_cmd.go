package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/asaidimu/devnotes/engine/internal/migrate"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate [paths...]",
		Short: "Rewrite legacy positional note headers to metadata directives",
		Long: "migrate rewrites headers such as\n" +
			"  @note #id issue P1 #tag : Title\n" +
			"into the directive form\n" +
			"  @note #id issue : Title\n" +
			"  @priority high\n" +
			"  @tags #tag\n" +
			"New-format notes are left untouched.",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args
			if len(paths) == 0 {
				paths = []string{flags.root}
			}
			var changed []string
			for _, path := range paths {
				if err := migratePath(path, dryRun, &changed); err != nil {
					return err
				}
			}
			if flags.jsonOut {
				data, err := json.MarshalIndent(map[string]any{"dryRun": dryRun, "files": changed}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}
			for _, f := range changed {
				if dryRun {
					fmt.Printf("would migrate %s\n", f)
				} else {
					fmt.Printf("migrated %s\n", f)
				}
			}
			if len(changed) == 0 {
				fmt.Println("no legacy headers found")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report files without writing changes")
	return cmd
}

func migratePath(path string, dryRun bool, changed *[]string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return migrateFile(path, dryRun, changed)
	}
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != path && (name == ".git" || name == ".devnotes" || name == "node_modules" ||
				name == "vendor" || name == "dist" || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		return migrateFile(p, dryRun, changed)
	})
}

func migrateFile(path string, dryRun bool, changed *[]string) error {
	if pipeline.Lang(filepath.Ext(path)) == "" {
		return nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, ok := migrate.Content(src)
	if !ok {
		return nil
	}
	*changed = append(*changed, path)
	if dryRun {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, info.Mode())
}

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/index"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
	"github.com/asaidimu/devnotes/engine/internal/validate"
	"github.com/spf13/cobra"
)

func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check [paths...]",
		Short: "Validate DevNotes in files or directories (SPEC 31 diagnostics)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{flags.root}
			}
			code := runCheck(args)
			if code != 0 {
				cmd.SilenceErrors = true
				return fmt.Errorf("devnotes check failed")
			}
			return nil
		},
	}
	return cmd
}

func runCheck(paths []string) int {
	files := index.CollectFiles(paths)

	results := map[string]*pipeline.FileResult{}
	var noteLocs []validate.NoteLoc
	for _, f := range files {
		ext := extOf(f)
		src, err := readFile(f)
		if err != nil {
			warn("%v", err)
			continue
		}
		res, err := pipeline.CheckFile(f, ext, src)
		if err != nil {
			warn("%s: %v", f, err)
			continue
		}
		results[f] = &res
		noteLocs = append(noteLocs, res.NoteLocs...)
	}

	all := []pipeline.Diag{}
	for _, res := range results {
		all = append(all, res.Diags...)
	}
	for _, d := range validate.CheckWorkspace(noteLocs) {
		all = append(all, pipeline.Diag{
			File: d.File, Severity: d.Severity, Code: d.Code, Message: d.Message,
			StartLine: d.Range.StartLine, StartCol: d.Range.StartCol,
			EndLine: d.Range.EndLine, EndCol: d.Range.EndCol,
		})
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.StartCol < b.StartCol
	})

	errCount := 0
	for _, d := range all {
		res := results[d.File]
		if res != nil {
			fmt.Println(res.Render(d))
		} else {
			fmt.Println(renderStandalone(d))
		}
		if d.Severity == validate.Error {
			errCount++
		}
	}
	if len(all) > 0 {
		warn("%d diagnostic(s) (%d error(s))", len(all), errCount)
	}
	if errCount > 0 {
		return 1
	}
	return 0
}

func renderStandalone(d pipeline.Diag) string {
	return d.File + ":" + itoa(d.StartLine+1) + ":" + itoa(d.StartCol+1) + ": " +
		d.Severity.String() + " " + d.Code + ": " + strings.TrimSpace(d.Message)
}

func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

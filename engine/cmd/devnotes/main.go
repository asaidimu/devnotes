// Command devnotes validates DevNotes in host source files and standalone
// .dn files, reporting SPEC 31 diagnostics.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/pipeline"
	"github.com/asaidimu/devnotes/engine/internal/validate"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "check":
		return cmdCheck(args[1:])
	case "version", "--version", "-v":
		fmt.Println("devnotes", version)
		return 0
	case "help", "--help", "-h":
		usage()
		return 0
	default:
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: devnotes <command> [paths...]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  check     validate DevNotes in files or directories")
	fmt.Fprintln(os.Stderr, "  version   print the devnotes version")
}

func cmdCheck(paths []string) int {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	files := collectFiles(paths)

	results := map[string]*pipeline.FileResult{}
	var noteLocs []validate.NoteLoc
	for _, f := range files {
		ext := filepath.Ext(f)
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "devnotes: %v\n", err)
			continue
		}
		res, err := pipeline.CheckFile(f, ext, src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "devnotes: %s: %v\n", f, err)
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
		fmt.Fprintf(os.Stderr, "devnotes: %d diagnostic(s) (%d error(s))\n", len(all), errCount)
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

// collectFiles gathers every file under the given paths that has a
// DevNotes-supporting extension.
func collectFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "devnotes: %v\n", err)
			continue
		}
		if !info.IsDir() {
			if pipeline.Lang(filepath.Ext(p)) != "" {
				out = append(out, p)
			}
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != p && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if pipeline.Lang(filepath.Ext(path)) != "" {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "devnotes: %s: %v\n", p, err)
		}
	}
	sort.Strings(out)
	return out
}

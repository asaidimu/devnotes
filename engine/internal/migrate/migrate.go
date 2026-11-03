// Package migrate rewrites legacy positional note headers into the
// directive-based format:
//
//	@note #id issue P1 #tag : Title
//
// becomes
//
//	@note #id issue : Title
//	@priority high
//	@tags #tag
//
// The migrator is intentionally text-based: it must read notes written for
// the old grammar, which the current parser no longer accepts. It also
// normalizes "@status=open"-style directives to "@status open".
package migrate

import (
	"os"
	"regexp"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/model"
)

var (
	titleSepRe   = regexp.MustCompile(`[ \t]+:[ \t]+`)
	timestampRe  = regexp.MustCompile(`^[0-9]{4}[-/][0-9]{1,2}`)
	legacyPrioRe = regexp.MustCompile(`^[Pp][0-9]$`)
	// Some models emit "=" instead of whitespace between a directive name and
	// its value (e.g. "@status=open"). The grammar accepts it for compatibility;
	// migration rewrites it to the canonical space-separated form.
	directiveEqRe = regexp.MustCompile(`^@(status|priority|tags|created|timestamp|author|see|assign|deadline)[ \t]*=[ \t]*(.*)$`)
)

var statuses = map[string]bool{
	"open": true, "resolved": true, "wontfix": true, "deprecated": true,
}

func statusesHas(s string) bool { return statuses[strings.ToLower(s)] }

// Content rewrites every legacy header in src. It reports whether anything
// changed. New-format headers are left untouched, so it is idempotent.
func Content(src []byte) ([]byte, bool) {
	crlf := strings.Contains(string(src), "\r\n")
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if rewritten, ok := normalizeEquals(line); ok {
			line = rewritten
			changed = true
		}
		next, ok := migrateLine(line)
		if !ok {
			out = append(out, line)
			continue
		}
		changed = true
		out = append(out, next...)
	}
	if !changed {
		return src, false
	}
	result := strings.Join(out, "\n")
	if crlf {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return []byte(result), true
}

// normalizeEquals rewrites "@status=open" (or "@status = open") to
// "@status open", preserving any comment prefix before the '@'.
func normalizeEquals(line string) (string, bool) {
	idx := strings.Index(line, "@")
	if idx < 0 {
		return line, false
	}
	rest := line[idx:]
	m := directiveEqRe.FindStringSubmatch(rest)
	if m == nil {
		return line, false
	}
	value := strings.TrimSpace(m[2])
	if m[1] == "priority" {
		if word, ok := model.NormalizePriority(value); ok {
			value = word
		}
	}
	if m[1] == "status" {
		value = strings.ToLower(value)
	}
	if m[1] == "tags" {
		var tags []string
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		}) {
			part = strings.TrimPrefix(part, "#")
			if part != "" {
				tags = append(tags, "#"+part)
			}
		}
		value = strings.Join(tags, " ")
	}
	if value == "" {
		return line[:idx] + "@" + m[1], true
	}
	return line[:idx] + "@" + m[1] + " " + value, true
}

func migrateLine(line string) ([]string, bool) {
	idx := strings.Index(line, "@note")
	if idx < 0 {
		return nil, false
	}
	if idx > 0 && line[idx-1] == '\\' {
		return nil, false
	}
	prefix, rest := line[:idx], line[idx:]

	loc := titleSepRe.FindStringIndex(rest)
	if loc == nil {
		return nil, false
	}
	head, title := rest[:loc[0]], rest[loc[1]:]
	parts := strings.Fields(head)
	if len(parts) < 3 || parts[0] != "@note" {
		return nil, false
	}
	id, category, fields := parts[1], parts[2], parts[3:]
	if len(fields) == 0 {
		return nil, false
	}
	directives := fieldDirectives(fields)
	if len(directives) == 0 {
		return nil, false
	}

	out := make([]string, 0, 1+len(directives))
	out = append(out, prefix+"@note "+id+" "+category+" : "+title)
	for _, d := range directives {
		out = append(out, prefix+d)
	}
	return out, true
}

func fieldDirectives(fields []string) []string {
	var statuses, priorities, tags, created, extras []string
	for _, tok := range fields {
		switch {
		case statusesHas(tok):
			statuses = append(statuses, "@status "+strings.ToLower(tok))
		case legacyPrioRe.MatchString(tok):
			priorities = append(priorities, "@priority "+model.PriorityLabel(tok))
		case isPriorityWord(tok):
			priorities = append(priorities, "@priority "+strings.ToLower(tok))
		case timestampRe.MatchString(tok):
			created = append(created, "@created "+tok)
		case strings.HasPrefix(tok, "#"):
			tags = append(tags, splitTags(tok)...)
		default:
			extras = append(extras, "@"+tok)
		}
	}
	out := make([]string, 0, len(statuses)+len(priorities)+len(tags)+len(created)+len(extras))
	out = append(out, statuses...)
	out = append(out, priorities...)
	if len(tags) > 0 {
		out = append(out, "@tags "+strings.Join(tags, " "))
	}
	out = append(out, created...)
	out = append(out, extras...)
	return out
}

func isPriorityWord(s string) bool {
	_, ok := model.NormalizePriority(s)
	return ok && !legacyPrioRe.MatchString(s)
}

func splitTags(tok string) []string {
	parts := strings.Split(tok, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimPrefix(strings.TrimSpace(p), "#")
		if p != "" {
			out = append(out, "#"+p)
		}
	}
	return out
}

// File rewrites one file in place. It returns whether the file changed.
func File(path string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, changed := Content(src)
	if !changed {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, out, info.Mode())
}

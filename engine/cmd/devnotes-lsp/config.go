// Configuration handling: workspace/didChangeConfiguration plus the
// editor-agnostic devnotes.toml file (minimal TOML subset).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/validate"
)

type config struct {
	DefaultAuthor          string
	KnownAuthors           []string
	CustomCategories       []string
	SnippetsEnableAllFiles bool
	HoverBodyExcerptChars  int
	ListDefaultLimit       int
}

func defaultConfig() config {
	return config{
		SnippetsEnableAllFiles: false,
		HoverBodyExcerptChars:  2000,
		ListDefaultLimit:       200,
	}
}

// apply merges a settings object of the shape
// {"devnotes": {...}} (nested keys allowed) into cfg.
func (c *config) apply(settings map[string]interface{}) error {
	dev, _ := settings["devnotes"].(map[string]interface{})
	if dev == nil {
		// Accept flat payloads too (keys directly on the object).
		dev = settings
	}
	if v, ok := stringVal(dev["defaultAuthor"]); ok {
		c.DefaultAuthor = v
	}
	if v, ok := stringSliceVal(dev["knownAuthors"]); ok {
		c.KnownAuthors = v
	}
	if v, ok := stringSliceVal(dev["categories"]); ok {
		c.CustomCategories = v
	}
	if v, ok := boolVal(dev["snippets.enableAllFiles"]); ok {
		c.SnippetsEnableAllFiles = v
	}
	if sub, ok := dev["snippets"].(map[string]interface{}); ok {
		if v, ok := boolVal(sub["enableAllFiles"]); ok {
			c.SnippetsEnableAllFiles = v
		}
	}
	if v, ok := intVal(dev["hover.bodyExcerptChars"]); ok {
		c.HoverBodyExcerptChars = v
	}
	if sub, ok := dev["hover"].(map[string]interface{}); ok {
		if v, ok := intVal(sub["bodyExcerptChars"]); ok {
			c.HoverBodyExcerptChars = v
		}
	}
	if v, ok := intVal(dev["list.defaultLimit"]); ok {
		c.ListDefaultLimit = v
	}
	if sub, ok := dev["list"].(map[string]interface{}); ok {
		if v, ok := intVal(sub["defaultLimit"]); ok {
			c.ListDefaultLimit = v
		}
	}
	// security.fetchUrls=true is rejected in v1.0: the server never fetches.
	if sub, ok := dev["security"].(map[string]interface{}); ok {
		if v, ok := boolVal(sub["fetchUrls"]); ok && v {
			return fmt.Errorf("devnotes.security.fetchUrls=true is not supported in v1.0; the server never fetches URLs")
		}
	}
	c.normalize()
	return nil
}

func (c *config) normalize() {
	if c.HoverBodyExcerptChars <= 0 {
		c.HoverBodyExcerptChars = 2000
	}
	if c.ListDefaultLimit <= 0 {
		c.ListDefaultLimit = 200
	}
}

// customCategorySet returns the configured custom categories as a set.
func (c *config) customCategorySet() map[string]bool {
	out := map[string]bool{}
	for _, cat := range c.CustomCategories {
		out[strings.TrimSpace(cat)] = true
	}
	return out
}

// syncCustomCategories pushes the configured categories into the validate
// package so UNKNOWN_CATEGORY stays quiet about intentional customs.
func (c *config) syncCustomCategories() {
	validate.CustomCategories = c.customCategorySet()
}

// loadConfigFromRoot reads devnotes.toml from the workspace root if present.
// Missing files and unparsable lines are ignored silently: configuration is
// best-effort and the server must start regardless.
func loadConfigFromRoot(root string) config {
	cfg := defaultConfig()
	if root == "" {
		return cfg
	}
	data, err := os.ReadFile(filepath.Join(root, "devnotes.toml"))
	if err != nil {
		return cfg
	}
	if m := parseTOMLSubset(string(data)); m != nil {
		_ = cfg.apply(m)
	}
	return cfg
}

// parseTOMLSubset handles the small TOML surface devnotes.toml uses:
// [section] headers, key = value with string / bool / int / [string, ...]
// values, and # comments. Nested section keys flatten to "a.b" paths.
func parseTOMLSubset(src string) map[string]interface{} {
	out := map[string]interface{}{}
	section := ""
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if i := strings.Index(val, " #"); i >= 0 && !strings.HasPrefix(val, "\"") {
			val = strings.TrimSpace(val[:i])
		}
		if key == "" || val == "" {
			continue
		}
		full := key
		if section != "" {
			full = section + "." + key
		}
		out[full] = tomlValue(val)
	}
	if len(out) == 0 {
		return nil
	}
	return map[string]interface{}{"devnotes": flattenTOML(out)}
}

// tomlValue parses a scalar or string-array literal.
func tomlValue(v string) interface{} {
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
		var items []string
		for _, part := range strings.Split(inner, ",") {
			part = strings.TrimSpace(part)
			part = strings.Trim(part, "\"")
			if part != "" {
				items = append(items, part)
			}
		}
		return items
	}
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return strings.Trim(v, "\"")
}

// flattenTOML turns "a.b" dotted keys into nested maps.
func flattenTOML(flat map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range flat {
		parts := strings.Split(k, ".")
		cur := out
		for i, p := range parts {
			if i == len(parts)-1 {
				cur[p] = v
				break
			}
			next, ok := cur[p].(map[string]interface{})
			if !ok {
				next = map[string]interface{}{}
				cur[p] = next
			}
			cur = next
		}
	}
	return out
}

func stringVal(v interface{}) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func boolVal(v interface{}) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func intVal(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

func stringSliceVal(v interface{}) ([]string, bool) {
	switch arr := v.(type) {
	case []string:
		return arr, true
	case []interface{}:
		var out []string
		for _, it := range arr {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

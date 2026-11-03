package model

import "strings"

// PriorityWords is the canonical, ordered priority vocabulary.
var PriorityWords = []string{"critical", "high", "medium", "low"}

// NormalizePriority maps either a canonical word or a legacy P0-P3 value to
// the canonical word. The second result is false for unknown values.
func NormalizePriority(p string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "p0", "critical":
		return "critical", true
	case "p1", "high":
		return "high", true
	case "p2", "medium":
		return "medium", true
	case "p3", "low":
		return "low", true
	default:
		return "", false
	}
}

// PriorityLabel returns the canonical word for a valid priority, or the raw
// value when it cannot be normalized (so invalid input remains visible).
func PriorityLabel(p string) string {
	if p == "" {
		return ""
	}
	if word, ok := NormalizePriority(p); ok {
		return word
	}
	return p
}

// PriorityRank orders priorities from most to least urgent. Unknown and
// unset priorities sort after all known values.
func PriorityRank(p string) int {
	word, _ := NormalizePriority(p)
	switch word {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 9
	}
}

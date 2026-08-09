package processenv

import "strings"

// Without returns env without the named variables. It preserves all unrelated
// entries and never mutates the caller's slice.
func Without(env []string, names ...string) []string {
	prefixes := make([]string, len(names))
	for i, name := range names {
		prefixes[i] = name + "="
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		blocked := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

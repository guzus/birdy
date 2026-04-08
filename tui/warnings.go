package tui

import "strings"

func joinWarnings(parts ...string) string {
	var kept []string
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		kept = append(kept, part)
	}
	return strings.Join(kept, " | ")
}

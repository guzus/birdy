// Package envcheck lets tests inspect a process environment by whole variable
// name. Test failures must report names only: a real child environment holds
// the developer's own credentials, and a substring match such as "AUTH_TOKEN="
// also hits GH_AUTH_TOKEN and similar unrelated variables.
package envcheck

import (
	"sort"
	"strings"
)

// Lines splits an environment captured as text (one NAME=value per line).
func Lines(text string) []string {
	return strings.Split(strings.TrimRight(text, "\n"), "\n")
}

// Value returns the value of the variable called name.
func Value(env []string, name string) (string, bool) {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):], true
		}
	}
	return "", false
}

// Has reports whether the variable called name is set.
func Has(env []string, name string) bool {
	_, ok := Value(env, name)
	return ok
}

// Present returns which of names are set, for a failure message.
func Present(env []string, names ...string) []string {
	var found []string
	for _, name := range names {
		if Has(env, name) {
			found = append(found, name)
		}
	}
	return found
}

// Names returns the sorted variable names, never their values.
func Names(env []string) []string {
	names := make([]string, 0, len(env))
	for _, entry := range env {
		if name, _, ok := strings.Cut(entry, "="); ok && name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

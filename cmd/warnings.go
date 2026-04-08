package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

func printStoreWarning(st *store.Store) {
	if st == nil {
		return
	}
	printWarning(st.Warning)
}

func printStateWarning(st *state.State) {
	if st == nil {
		return
	}
	printWarning(st.Warning)
}

func printWarning(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "[birdy] warning: %s\n", msg)
}

func mergeWarnings(existing string, warnings ...string) string {
	lines := make([]string, 0, len(warnings)+1)
	for _, warning := range warnings {
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		lines = append(lines, "[birdy] warning: "+warning)
	}
	existing = strings.TrimSpace(existing)
	if existing != "" {
		lines = append(lines, existing)
	}
	return strings.Join(lines, "\n")
}

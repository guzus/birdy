package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

func printStoreWarning(w io.Writer, st *store.Store) {
	if st == nil {
		return
	}
	printWarning(w, st.Warning)
}

func printStateWarning(w io.Writer, st *state.State) {
	if st == nil {
		return
	}
	printWarning(w, st.Warning)
}

func printWarning(w io.Writer, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintf(w, "[birdy] warning: %s\n", msg)
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

package cmd

import (
	"fmt"

	"github.com/guzus/birdy/internal/store"
)

var readOnlyBlockedBirdCommands = map[string]struct{}{
	"tweet":      {},
	"reply":      {},
	"follow":     {},
	"unfollow":   {},
	"unbookmark": {},
}

func isMutatingBirdCommand(args []string) (bool, string) {
	cmd := firstBirdCommand(args)
	if cmd == "" {
		return false, ""
	}
	_, blocked := readOnlyBlockedBirdCommands[cmd]
	return blocked, cmd
}

func filterWritableAccounts(accounts []store.Account) []store.Account {
	writable := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		if !account.ReadOnly {
			writable = append(writable, account)
		}
	}
	return writable
}

func ensureBirdCommandAllowed(account *store.Account, args []string) error {
	blocked, name := isMutatingBirdCommand(args)
	if !blocked {
		return nil
	}
	if readOnlyModeEnabled() {
		return fmt.Errorf("%q is disabled in read-only mode (BIRDY_READ_ONLY)", name)
	}
	if account != nil && account.ReadOnly {
		return fmt.Errorf("%q is disabled for read-only account %q", name, account.Name)
	}
	return nil
}

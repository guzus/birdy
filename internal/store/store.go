package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Account holds credentials for a single bird CLI account.
type Account struct {
	Name      string    `json:"name"`
	AuthToken string    `json:"auth_token"`
	CT0       string    `json:"ct0"`
	AddedAt   time.Time `json:"added_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
	UseCount  int64     `json:"use_count"`
}

// Store manages multiple accounts persisted to disk.
type Store struct {
	mu        sync.Mutex
	path      string
	ephemeral bool      // true when accounts come purely from env (no file existed)
	Accounts  []Account `json:"accounts"`
}

func defaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "birdy", "accounts.json"), nil
}

// loadFromEnv parses the BIRDY_ACCOUNTS env var into a slice of Account.
// Returns nil (not an error) when the env var is unset or empty.
func loadFromEnv() ([]Account, error) {
	raw := os.Getenv("BIRDY_ACCOUNTS")
	if raw == "" {
		return nil, nil
	}

	var accounts []Account
	if err := json.Unmarshal([]byte(raw), &accounts); err != nil {
		return nil, fmt.Errorf("parsing BIRDY_ACCOUNTS: %w", err)
	}
	return accounts, nil
}

// Open loads (or creates) the account store at the default location,
// then merges any accounts from the BIRDY_ACCOUNTS env var.
func Open() (*Store, error) {
	p, err := defaultPath()
	if err != nil {
		return nil, err
	}
	return OpenPath(p)
}

// OpenPath loads (or creates) the account store at a custom path,
// then merges any accounts from the BIRDY_ACCOUNTS env var.
func OpenPath(path string) (*Store, error) {
	s := &Store{path: path}

	data, err := os.ReadFile(path)
	fileExists := !os.IsNotExist(err)
	if err != nil && fileExists {
		return nil, fmt.Errorf("reading store: %w", err)
	}

	if fileExists {
		if err := json.Unmarshal(data, &s.Accounts); err != nil {
			return nil, fmt.Errorf("parsing store: %w", err)
		}
	} else {
		s.Accounts = []Account{}
	}

	envAccounts, err := loadFromEnv()
	if err != nil {
		return nil, err
	}

	if len(envAccounts) > 0 {
		// Env accounts override file accounts with the same name.
		for _, ea := range envAccounts {
			found := false
			for i, fa := range s.Accounts {
				if fa.Name == ea.Name {
					s.Accounts[i] = ea
					found = true
					break
				}
			}
			if !found {
				s.Accounts = append(s.Accounts, ea)
			}
		}

		// Mark as ephemeral only when no file existed on disk.
		if !fileExists {
			s.ephemeral = true
		}
	}

	return s, nil
}

// Save persists the store to disk. When the store is ephemeral
// (accounts loaded purely from env with no file on disk), Save is a no-op.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ephemeral {
		return nil
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(s.Accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling store: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("writing store: %w", err)
	}
	return nil
}

// Add creates a new account entry. Returns error if name already exists.
func (s *Store) Add(name, authToken, ct0 string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, a := range s.Accounts {
		if a.Name == name {
			return fmt.Errorf("account %q already exists", name)
		}
	}

	s.Accounts = append(s.Accounts, Account{
		Name:      name,
		AuthToken: authToken,
		CT0:       ct0,
		AddedAt:   time.Now(),
	})
	return nil
}

// Remove deletes an account by name.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, a := range s.Accounts {
		if a.Name == name {
			s.Accounts = append(s.Accounts[:i], s.Accounts[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("account %q not found", name)
}

// Get returns an account by name.
func (s *Store) Get(name string) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Accounts {
		if s.Accounts[i].Name == name {
			return &s.Accounts[i], nil
		}
	}
	return nil, fmt.Errorf("account %q not found", name)
}

// List returns all accounts.
func (s *Store) List() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Account, len(s.Accounts))
	copy(out, s.Accounts)
	return out
}

// RecordUsage updates last-used timestamp and increments use count.
func (s *Store) RecordUsage(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Accounts {
		if s.Accounts[i].Name == name {
			s.Accounts[i].LastUsed = time.Now()
			s.Accounts[i].UseCount++
			return nil
		}
	}
	return fmt.Errorf("account %q not found", name)
}

// Len returns the number of stored accounts.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Accounts)
}

// Update replaces the credentials for an existing account.
func (s *Store) Update(name, authToken, ct0 string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Accounts {
		if s.Accounts[i].Name == name {
			s.Accounts[i].AuthToken = authToken
			s.Accounts[i].CT0 = ct0
			return nil
		}
	}
	return fmt.Errorf("account %q not found", name)
}

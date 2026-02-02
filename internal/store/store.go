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
	mu       sync.Mutex
	path     string
	Accounts []Account `json:"accounts"`
}

func defaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "birdy", "accounts.json"), nil
}

// Open loads (or creates) the account store at the default location.
func Open() (*Store, error) {
	p, err := defaultPath()
	if err != nil {
		return nil, err
	}
	return OpenPath(p)
}

// OpenPath loads (or creates) the account store at a custom path.
func OpenPath(path string) (*Store, error) {
	s := &Store{path: path}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		s.Accounts = []Account{}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading store: %w", err)
	}

	if err := json.Unmarshal(data, &s.Accounts); err != nil {
		return nil, fmt.Errorf("parsing store: %w", err)
	}
	return s, nil
}

// Save persists the store to disk.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Account holds credentials for a single bird CLI account.
type Account struct {
	Name               string    `json:"name"`
	AuthToken          string    `json:"auth_token"`
	CT0                string    `json:"ct0"`
	ReadOnly           bool      `json:"read_only,omitempty"`
	AddedAt            time.Time `json:"added_at"`
	LastUsed           time.Time `json:"last_used,omitempty"`
	UseCount           int64     `json:"use_count"`
	LastRateLimitedAt  time.Time `json:"last_rate_limited_at,omitempty"`
	RateLimitCount     int64     `json:"rate_limit_count,omitempty"`
}

// Store manages multiple accounts persisted to disk.
type Store struct {
	mu        sync.Mutex
	path      string
	ephemeral bool      // true when accounts come purely from env (no file existed)
	Accounts  []Account `json:"accounts"`
	Warning   string    `json:"-"`
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
	for i := range accounts {
		name, authToken, ct0, err := normalizeAccountInput(accounts[i].Name, accounts[i].AuthToken, accounts[i].CT0)
		if err != nil {
			return nil, fmt.Errorf("parsing BIRDY_ACCOUNTS: %w", err)
		}
		accounts[i].Name = name
		accounts[i].AuthToken = authToken
		accounts[i].CT0 = ct0
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
			quarantinedPath, quarantineErr := quarantineCorruptStoreFile(path)
			if quarantineErr != nil {
				return nil, fmt.Errorf("parsing store: %w", err)
			}
			s.Accounts = []Account{}
			s.Warning = fmt.Sprintf("recovered from corrupt account store; moved old file to %s", quarantinedPath)
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

	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp store file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting temp store permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp store file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp store file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replacing store file: %w", err)
	}
	return nil
}

func quarantineCorruptStoreFile(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dst := filepath.Join(dir, fmt.Sprintf("%s.corrupt-%s", base, stamp))
	for i := 0; i < 1000; i++ {
		target := dst
		if i > 0 {
			target = fmt.Sprintf("%s-%02d", dst, i)
		}
		if err := os.Rename(path, target); err == nil {
			return target, nil
		} else if os.IsExist(err) {
			continue
		} else {
			return "", err
		}
	}
	return "", fmt.Errorf("exhausted corrupt store quarantine attempts for %s", path)
}

// Add creates a new account entry. Returns error if name already exists.
func (s *Store) Add(name, authToken, ct0 string) error {
	return s.AddWithReadOnly(name, authToken, ct0, false)
}

// AddWithReadOnly creates a new account entry with an explicit read-only mode.
func (s *Store) AddWithReadOnly(name, authToken, ct0 string, readOnly bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name, authToken, ct0, err := normalizeAccountInput(name, authToken, ct0)
	if err != nil {
		return err
	}

	for _, a := range s.Accounts {
		if a.Name == name {
			return fmt.Errorf("account %q already exists", name)
		}
	}

	s.Accounts = append(s.Accounts, Account{
		Name:      name,
		AuthToken: authToken,
		CT0:       ct0,
		ReadOnly:  readOnly,
		AddedAt:   time.Now(),
	})
	return nil
}

// Remove deletes an account by name.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = normalizeAccountName(name)
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

	name = normalizeAccountName(name)
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

	name = normalizeAccountName(name)
	for i := range s.Accounts {
		if s.Accounts[i].Name == name {
			s.Accounts[i].LastUsed = time.Now()
			s.Accounts[i].UseCount++
			return nil
		}
	}
	return fmt.Errorf("account %q not found", name)
}

// RecordRateLimit stamps the most recent 429 and increments the per-account
// rate-limit counter. Used by the quota-aware rotation strategy to avoid
// repicking an account that just hit a limit, and by `birdy budget` to
// show per-account quota status.
func (s *Store) RecordRateLimit(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = normalizeAccountName(name)
	for i := range s.Accounts {
		if s.Accounts[i].Name == name {
			s.Accounts[i].LastRateLimitedAt = time.Now()
			s.Accounts[i].RateLimitCount++
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

// IsEphemeral reports whether the store exists only in env-backed memory and
// cannot persist structural account changes to disk.
func (s *Store) IsEphemeral() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ephemeral
}

// Update replaces the credentials for an existing account.
func (s *Store) Update(name, authToken, ct0 string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name, authToken, ct0, err := normalizeAccountInput(name, authToken, ct0)
	if err != nil {
		return err
	}

	for i := range s.Accounts {
		if s.Accounts[i].Name == name {
			s.Accounts[i].AuthToken = authToken
			s.Accounts[i].CT0 = ct0
			return nil
		}
	}
	return fmt.Errorf("account %q not found", name)
}

// SetReadOnly updates the read-only mode for an existing account.
func (s *Store) SetReadOnly(name string, readOnly bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = normalizeAccountName(name)
	if name == "" {
		return fmt.Errorf("account name is required")
	}

	for i := range s.Accounts {
		if s.Accounts[i].Name == name {
			s.Accounts[i].ReadOnly = readOnly
			return nil
		}
	}
	return fmt.Errorf("account %q not found", name)
}

func normalizeAccountName(name string) string {
	return strings.TrimSpace(name)
}

func normalizeAccountInput(name, authToken, ct0 string) (string, string, string, error) {
	name = normalizeAccountName(name)
	authToken = strings.TrimSpace(authToken)
	ct0 = strings.TrimSpace(ct0)

	if name == "" {
		return "", "", "", fmt.Errorf("account name is required")
	}
	if authToken == "" || ct0 == "" {
		return "", "", "", fmt.Errorf("both auth_token and ct0 are required")
	}
	return name, authToken, ct0, nil
}

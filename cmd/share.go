package cmd

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	shareIDBytes           = 32
	shareIVBytes           = 12
	shareTTL               = 7 * 24 * time.Hour
	maxShareRequestBytes   = 512 * 1024
	maxShareCipherBytes    = 384 * 1024
	maxStoredShares        = 200
	shareEnvelopeVersion   = 1
	shareEnvelopeAlgorithm = "AES-GCM"
)

var errShareCapacity = errors.New("share storage is full")

type shareEnvelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	IV         string `json:"iv"`
	Ciphertext string `json:"ciphertext"`
}

type createShareRequest struct {
	OwnerRef string        `json:"owner_ref"`
	Envelope shareEnvelope `json:"envelope"`
}

type storedShare struct {
	OwnerRefHash string        `json:"owner_ref_hash"`
	Envelope     shareEnvelope `json:"envelope"`
	CreatedAt    time.Time     `json:"created_at"`
	ExpiresAt    time.Time     `json:"expires_at"`
}

type createShareResponse struct {
	OK        bool      `json:"ok"`
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type getShareResponse struct {
	OK        bool          `json:"ok"`
	Envelope  shareEnvelope `json:"envelope"`
	CreatedAt time.Time     `json:"created_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

var (
	shareStoreMu = sync.Mutex{}
	shareReadSem = make(chan struct{}, 32)
)

func shareStorageDir() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("BIRDY_SHARE_DIR")); configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "birdy", "shares"), nil
}

func generateShareID() (string, error) {
	raw := make([]byte, shareIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating share id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validShareID(id string) bool {
	if len(id) != base64.RawURLEncoding.EncodedLen(shareIDBytes) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil && len(raw) == shareIDBytes
}

func validateShareEnvelope(envelope shareEnvelope) error {
	if envelope.Version != shareEnvelopeVersion || envelope.Algorithm != shareEnvelopeAlgorithm {
		return errors.New("unsupported encrypted snapshot")
	}
	iv, err := base64.RawURLEncoding.DecodeString(envelope.IV)
	if err != nil || len(iv) != shareIVBytes {
		return errors.New("invalid encrypted snapshot")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil || len(ciphertext) < 16 || len(ciphertext) > maxShareCipherBytes {
		return errors.New("invalid encrypted snapshot")
	}
	return nil
}

func hashShareOwnerRef(ownerRef string) (string, error) {
	ownerRef = strings.TrimSpace(ownerRef)
	raw, err := base64.RawURLEncoding.DecodeString(ownerRef)
	if err != nil || len(raw) != shareIDBytes {
		return "", errors.New("invalid share owner reference")
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func createStoredShare(dir string, ownerRef string, envelope shareEnvelope, now time.Time) (string, storedShare, error) {
	shareStoreMu.Lock()
	defer shareStoreMu.Unlock()

	if err := validateShareEnvelope(envelope); err != nil {
		return "", storedShare{}, err
	}
	ownerRefHash, err := hashShareOwnerRef(ownerRef)
	if err != nil {
		return "", storedShare{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", storedShare{}, fmt.Errorf("creating share directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", storedShare{}, fmt.Errorf("securing share directory: %w", err)
	}
	storedCount, previousPaths, err := sweepShareStorage(dir, now, ownerRefHash)
	if err != nil {
		return "", storedShare{}, err
	}
	if storedCount-len(previousPaths) >= maxStoredShares {
		return "", storedShare{}, errShareCapacity
	}

	share := storedShare{
		OwnerRefHash: ownerRefHash,
		Envelope:     envelope,
		CreatedAt:    now.UTC(),
		ExpiresAt:    now.UTC().Add(shareTTL),
	}
	data, err := json.Marshal(share)
	if err != nil {
		return "", storedShare{}, fmt.Errorf("encoding share: %w", err)
	}

	for attempts := 0; attempts < 5; attempts++ {
		id, err := generateShareID()
		if err != nil {
			return "", storedShare{}, err
		}
		path := filepath.Join(dir, id+".json")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", storedShare{}, fmt.Errorf("creating share: %w", err)
		}
		written, err := file.Write(data)
		if err == nil && written != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", storedShare{}, fmt.Errorf("writing share: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", storedShare{}, fmt.Errorf("syncing share: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return "", storedShare{}, fmt.Errorf("closing share: %w", err)
		}
		for _, previousPath := range previousPaths {
			if err := os.Remove(previousPath); err != nil && !os.IsNotExist(err) {
				_ = os.Remove(path)
				return "", storedShare{}, fmt.Errorf("replacing previous share: %w", err)
			}
		}
		return id, share, nil
	}
	return "", storedShare{}, errors.New("could not allocate share id")
}

func sweepShareStorage(dir string, now time.Time, replaceOwnerRefHash string) (int, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil, fmt.Errorf("reading share directory: %w", err)
	}
	active := 0
	previousPaths := make([]string, 0, 1)
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if entry.IsDir() || name == id || !validShareID(id) {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, nil, fmt.Errorf("reading stored share: %w", err)
		}
		var share storedShare
		if len(data) > maxShareRequestBytes || json.Unmarshal(data, &share) != nil || share.OwnerRefHash == "" || validateShareEnvelope(share.Envelope) != nil || !share.ExpiresAt.After(now) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return 0, nil, fmt.Errorf("removing unusable share: %w", err)
			}
			continue
		}
		active++
		if share.OwnerRefHash == replaceOwnerRefHash {
			previousPaths = append(previousPaths, path)
		}
	}
	return active, previousPaths, nil
}

func readStoredShareLocked(dir, id string, now time.Time) (storedShare, error) {
	if !validShareID(id) {
		return storedShare{}, os.ErrNotExist
	}
	path := filepath.Join(dir, id+".json")
	file, err := os.Open(path)
	if err != nil {
		return storedShare{}, err
	}
	defer file.Close()

	var share storedShare
	decoder := json.NewDecoder(io.LimitReader(file, maxShareRequestBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&share); err != nil {
		return storedShare{}, fmt.Errorf("decoding share: %w", err)
	}
	if err := validateShareEnvelope(share.Envelope); err != nil {
		return storedShare{}, err
	}
	if !share.ExpiresAt.After(now) {
		_ = os.Remove(path)
		return storedShare{}, os.ErrNotExist
	}
	return share, nil
}

func readStoredShare(dir, id string, now time.Time) (storedShare, error) {
	shareStoreMu.Lock()
	defer shareStoreMu.Unlock()
	return readStoredShareLocked(dir, id, now)
}

func removeSharesByOwnerLocked(dir, ownerRefHash string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if entry.IsDir() || name == id || !validShareID(id) {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		var share storedShare
		if json.Unmarshal(data, &share) != nil || share.OwnerRefHash != ownerRefHash {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		removed = true
	}
	return removed, nil
}

func setShareResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}

func handleShareCollection(inviteCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setShareResponseHeaders(w)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !apiAuthorized(r, inviteCode) {
			writeJSON(w, http.StatusUnauthorized, apiError{OK: false, Error: "unauthorized"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxShareRequestBytes)
		defer r.Body.Close()
		var req createShareRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid encrypted snapshot"})
			return
		}
		if _, err := hashShareOwnerRef(req.OwnerRef); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: err.Error()})
			return
		}
		if err := validateShareEnvelope(req.Envelope); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: err.Error()})
			return
		}
		dir, err := shareStorageDir()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "share storage unavailable"})
			return
		}
		id, share, err := createStoredShare(dir, req.OwnerRef, req.Envelope, time.Now())
		if err != nil {
			if errors.Is(err, errShareCapacity) {
				writeJSON(w, http.StatusInsufficientStorage, apiError{OK: false, Error: "share storage full; revoke a link or wait for expiry"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "saving share"})
			return
		}
		writeJSON(w, http.StatusCreated, createShareResponse{
			OK: true, ID: id, Path: "/share/" + id,
			CreatedAt: share.CreatedAt, ExpiresAt: share.ExpiresAt,
		})
	}
}

func handleShareItem(inviteCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setShareResponseHeaders(w)
		id := strings.TrimPrefix(r.URL.Path, "/api/shares/")
		if strings.Contains(id, "/") || !validShareID(id) {
			writeJSON(w, http.StatusNotFound, apiError{OK: false, Error: "share not found"})
			return
		}
		dir, err := shareStorageDir()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "share storage unavailable"})
			return
		}

		switch r.Method {
		case http.MethodGet:
			select {
			case shareReadSem <- struct{}{}:
				defer func() { <-shareReadSem }()
			default:
				writeJSON(w, http.StatusServiceUnavailable, apiError{OK: false, Error: "share service busy"})
				return
			}
			share, err := readStoredShare(dir, id, time.Now())
			if err != nil {
				writeJSON(w, http.StatusNotFound, apiError{OK: false, Error: "share not found"})
				return
			}
			writeJSON(w, http.StatusOK, getShareResponse{
				OK: true, Envelope: share.Envelope,
				CreatedAt: share.CreatedAt, ExpiresAt: share.ExpiresAt,
			})
		case http.MethodDelete:
			if !apiAuthorized(r, inviteCode) {
				writeJSON(w, http.StatusUnauthorized, apiError{OK: false, Error: "unauthorized"})
				return
			}
			ownerRef := strings.TrimSpace(r.Header.Get("X-Share-Owner-Ref"))
			ownerRefHash := ""
			if ownerRef != "" {
				ownerRefHash, err = hashShareOwnerRef(ownerRef)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid share owner reference"})
					return
				}
			}
			shareStoreMu.Lock()
			removed := false
			if ownerRefHash != "" {
				removed, err = removeSharesByOwnerLocked(dir, ownerRefHash)
				if err == nil && !removed {
					err = os.Remove(filepath.Join(dir, id+".json"))
					removed = err == nil
				}
			} else {
				err = os.Remove(filepath.Join(dir, id+".json"))
				removed = err == nil
			}
			if err != nil || !removed {
				writeJSON(w, http.StatusNotFound, apiError{OK: false, Error: "share not found"})
				shareStoreMu.Unlock()
				return
			}
			w.WriteHeader(http.StatusNoContent)
			shareStoreMu.Unlock()
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

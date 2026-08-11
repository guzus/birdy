package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testShareEnvelope() shareEnvelope {
	return shareEnvelope{
		Version:    shareEnvelopeVersion,
		Algorithm:  shareEnvelopeAlgorithm,
		IV:         base64.RawURLEncoding.EncodeToString(make([]byte, shareIVBytes)),
		Ciphertext: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	}
}

func testShareOwnerRef(seed byte) string {
	raw := make([]byte, shareIDBytes)
	for i := range raw {
		raw[i] = seed
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func createTestShare(t *testing.T, mux http.Handler, inviteCode, ownerRef string) createShareResponse {
	t.Helper()
	body, err := json.Marshal(createShareRequest{OwnerRef: ownerRef, Envelope: testShareEnvelope()})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(string(body)))
	req.Header.Set("X-Invite-Code", inviteCode)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	var response createShareResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func TestShareCreateReadAndRevoke(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BIRDY_SHARE_DIR", dir)
	mux := buildHostedMux("owner-code", nil, t.TempDir())

	created := createTestShare(t, mux, "owner-code", testShareOwnerRef(1))
	if !created.OK || !validShareID(created.ID) || created.Path != "/share/"+created.ID {
		t.Fatalf("unexpected create response: %+v", created)
	}
	if got := created.ExpiresAt.Sub(created.CreatedAt); got != shareTTL {
		t.Fatalf("expected %s expiry, got %s", shareTTL, got)
	}

	path := filepath.Join(dir, created.ID+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat persisted share: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600 share file, got %o", info.Mode().Perm())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/shares/"+created.ID, nil)
	getRR := httptest.NewRecorder()
	mux.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("anonymous GET expected 200, got %d body=%s", getRR.Code, getRR.Body.String())
	}
	if got := getRR.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("unexpected Cache-Control %q", got)
	}
	if got := getRR.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Fatalf("expected noindex, got %q", got)
	}
	var fetched getShareResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Envelope != testShareEnvelope() {
		t.Fatalf("envelope changed: %+v", fetched.Envelope)
	}

	unauthorizedDelete := httptest.NewRecorder()
	mux.ServeHTTP(unauthorizedDelete, httptest.NewRequest(http.MethodDelete, "/api/shares/"+created.ID, nil))
	if unauthorizedDelete.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized revoke, got %d", unauthorizedDelete.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/shares/"+created.ID, nil)
	deleteReq.Header.Set("X-Invite-Code", "owner-code")
	// A regenerated/lost owner token must still revoke the explicitly saved link ID.
	deleteReq.Header.Set("X-Share-Owner-Ref", testShareOwnerRef(99))
	deleteRR := httptest.NewRecorder()
	mux.ServeHTTP(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("expected 204 revoke, got %d body=%s", deleteRR.Code, deleteRR.Body.String())
	}

	missingRR := httptest.NewRecorder()
	mux.ServeHTTP(missingRR, httptest.NewRequest(http.MethodGet, "/api/shares/"+created.ID, nil))
	if missingRR.Code != http.StatusNotFound {
		t.Fatalf("expected revoked share to be 404, got %d", missingRR.Code)
	}
}

func TestShareCreateRequiresAuthAndStrictEnvelope(t *testing.T) {
	t.Setenv("BIRDY_SHARE_DIR", t.TempDir())
	handler := handleShareCollection("owner-code")

	body, _ := json.Marshal(createShareRequest{OwnerRef: testShareOwnerRef(2), Envelope: testShareEnvelope()})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(string(body))))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}

	badBodies := []string{
		fmt.Sprintf(`{"owner_ref":%q,"envelope":{"version":1,"algorithm":"AES-GCM","iv":"bad","ciphertext":"bad"}}`, testShareOwnerRef(3)),
		fmt.Sprintf(`{"owner_ref":%q,"envelope":{"version":1,"algorithm":"AES-GCM","iv":"AAAAAAAAAAAAAAAA","ciphertext":"AAAAAAAAAAAAAAAAAAAAAA"},"extra":true}`, testShareOwnerRef(3)),
		`{"owner_ref":"","envelope":{"version":1,"algorithm":"AES-GCM","iv":"AAAAAAAAAAAAAAAA","ciphertext":"AAAAAAAAAAAAAAAAAAAAAA"}}`,
		`{"owner_ref":"conv-1234-local-id","envelope":{"version":1,"algorithm":"AES-GCM","iv":"AAAAAAAAAAAAAAAA","ciphertext":"AAAAAAAAAAAAAAAAAAAAAA"}}`,
	}
	for _, badBody := range badBodies {
		req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(badBody))
		req.Header.Set("X-Invite-Code", "owner-code")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected bad envelope to be 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	}
}

func TestShareItemHidesMalformedUnknownAndExpired(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BIRDY_SHARE_DIR", dir)
	handler := handleShareItem("owner-code")

	for _, path := range []string{
		"/api/shares/not-a-token",
		"/api/shares/../../accounts.json",
		"/api/shares/" + strings.Repeat("A", 43),
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected %q to be 404, got %d", path, rr.Code)
		}
	}

	id, share, err := createStoredShare(dir, testShareOwnerRef(4), testShareEnvelope(), time.Now().Add(-shareTTL-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !share.ExpiresAt.Before(time.Now()) {
		t.Fatal("test share should be expired")
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/shares/"+id, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected expired share 404, got %d", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".json")); !os.IsNotExist(err) {
		t.Fatalf("expected expired share cleanup, stat err=%v", err)
	}
}

func TestShareIDUses256BitsAndURLSafeEncoding(t *testing.T) {
	first, err := generateShareID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateShareID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !validShareID(first) || strings.ContainsAny(first, "+/=") {
		t.Fatalf("unexpected generated ids %q %q", first, second)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(raw) != 32 {
		t.Fatalf("expected 256-bit id, got %d bytes err=%v", len(raw), err)
	}
}

func TestCreateStoredShareReplacesSameConversation(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	ownerRef := testShareOwnerRef(5)
	firstID, _, err := createStoredShare(dir, ownerRef, testShareEnvelope(), now)
	if err != nil {
		t.Fatal(err)
	}
	secondID, _, err := createStoredShare(dir, ownerRef, testShareEnvelope(), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatal("replacement must receive a fresh public id")
	}
	if _, err := os.Stat(filepath.Join(dir, firstID+".json")); !os.IsNotExist(err) {
		t.Fatalf("expected previous share revoked, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, secondID+".json")); err != nil {
		t.Fatalf("expected replacement share: %v", err)
	}

	t.Setenv("BIRDY_SHARE_DIR", dir)
	req := httptest.NewRequest(http.MethodDelete, "/api/shares/"+firstID, nil)
	req.Header.Set("X-Invite-Code", "owner-code")
	req.Header.Set("X-Share-Owner-Ref", ownerRef)
	rr := httptest.NewRecorder()
	handleShareItem("owner-code").ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected stale-id owner revoke to remove current share, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, secondID+".json")); !os.IsNotExist(err) {
		t.Fatalf("expected current owner share revoked, stat err=%v", err)
	}
}

func TestCreateStoredShareSweepsExpiryAndEnforcesQuota(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	for i := 0; i < maxStoredShares; i++ {
		id, err := generateShareID()
		if err != nil {
			t.Fatal(err)
		}
		ownerHash, err := hashShareOwnerRef(testShareOwnerRef(byte(i + 1)))
		if err != nil {
			t.Fatal(err)
		}
		expiresAt := now.Add(shareTTL)
		if i == 0 {
			expiresAt = now.Add(-time.Minute)
		}
		data, err := json.Marshal(storedShare{
			OwnerRefHash: ownerHash,
			Envelope:     testShareEnvelope(), CreatedAt: now.Add(-time.Hour), ExpiresAt: expiresAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := createStoredShare(dir, testShareOwnerRef(201), testShareEnvelope(), now); err != nil {
		t.Fatalf("expected expired entry to free capacity: %v", err)
	}
	if _, _, err := createStoredShare(dir, testShareOwnerRef(202), testShareEnvelope(), now); !errors.Is(err, errShareCapacity) {
		t.Fatalf("expected capacity error, got %v", err)
	}
}

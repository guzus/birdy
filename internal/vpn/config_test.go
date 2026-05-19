package vpn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPathMissingReturnsEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vpn.json")
	cfg, err := LoadPath(p)
	if err != nil {
		t.Fatalf("LoadPath returned error on missing file: %v", err)
	}
	if cfg.User != "" || cfg.Password != "" {
		t.Fatalf("expected empty cfg, got %+v", cfg)
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vpn.json")
	in := &Config{User: "u", Password: "p", Pool: []string{"a.com"}, Port: 1080}
	if err := in.SavePath(p); err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 perms, got %o", st.Mode().Perm())
	}
	out, err := LoadPath(p)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if out.User != "u" || out.Password != "p" {
		t.Errorf("creds didn't roundtrip: %+v", out)
	}
	if len(out.Pool) != 1 || out.Pool[0] != "a.com" {
		t.Errorf("pool didn't roundtrip: %+v", out.Pool)
	}
}

func TestPickServerPin(t *testing.T) {
	c := &Config{Pool: []string{"a", "b", "c"}}
	got, err := c.PickServer("pinned.example.com")
	if err != nil {
		t.Fatalf("PickServer: %v", err)
	}
	if got != "pinned.example.com" {
		t.Errorf("pin not honored: got %q", got)
	}
}

func TestPickServerEmptyPoolErrors(t *testing.T) {
	c := &Config{}
	_, err := c.PickServer("")
	if err == nil {
		t.Fatal("expected error on empty pool with no pin")
	}
	if !strings.Contains(err.Error(), "no VPN server pool") {
		t.Errorf("expected friendly error, got %v", err)
	}
}

func TestAddPoolDedup(t *testing.T) {
	c := &Config{}
	if !c.AddPool("foo.com") {
		t.Fatal("AddPool first should succeed")
	}
	if c.AddPool("foo.com") {
		t.Fatal("AddPool duplicate should return false")
	}
	if c.AddPool("FOO.COM") {
		t.Fatal("AddPool case-duplicate should return false")
	}
	if len(c.Pool) != 1 {
		t.Errorf("expected 1 entry, got %v", c.Pool)
	}
}

func TestRemovePool(t *testing.T) {
	c := &Config{Pool: []string{"a", "b", "c"}}
	if !c.RemovePool("b") {
		t.Fatal("RemovePool of present should succeed")
	}
	if len(c.Pool) != 2 || c.Pool[0] != "a" || c.Pool[1] != "c" {
		t.Errorf("unexpected pool after remove: %v", c.Pool)
	}
	if c.RemovePool("nope") {
		t.Fatal("RemovePool of absent should return false")
	}
}

func TestValidate(t *testing.T) {
	if (&Config{}).Validate() == nil {
		t.Error("empty config should fail validation")
	}
	if (&Config{User: "u"}).Validate() == nil {
		t.Error("missing password should fail")
	}
	if (&Config{User: "u", Password: "p"}).Validate() != nil {
		t.Error("user+pass should validate")
	}
}

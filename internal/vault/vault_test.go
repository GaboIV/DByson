package vault

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func newVault(t *testing.T) *Vault {
	t.Helper()
	k := make([]byte, keySize)
	rand.Read(k)
	v, err := New(k)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRoundTrip(t *testing.T) {
	v := newVault(t)
	wrapped, ct, err := v.Seal("conn-1", []byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("s3cret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := v.Open("conn-1", wrapped, ct)
	if err != nil || string(got) != "s3cret" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestBoundToRecordID(t *testing.T) {
	v := newVault(t)
	wrapped, ct, _ := v.Seal("conn-1", []byte("s3cret"))
	if _, err := v.Open("conn-2", wrapped, ct); err == nil {
		t.Fatal("opening under a different id must fail")
	}
}

func TestTamperAndWrongKey(t *testing.T) {
	v := newVault(t)
	wrapped, ct, _ := v.Seal("c", []byte("x"))
	ct[len(ct)-1] ^= 1
	if _, err := v.Open("c", wrapped, ct); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	ct[len(ct)-1] ^= 1
	if _, err := newVault(t).Open("c", wrapped, ct); err == nil {
		t.Fatal("wrong master key must fail")
	}
}

func TestUniqueCiphertexts(t *testing.T) {
	v := newVault(t)
	_, a, _ := v.Seal("c", []byte("same"))
	_, b, _ := v.Seal("c", []byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("encryption must be randomized")
	}
}

func TestLoadKeyGeneratesAndReuses(t *testing.T) {
	t.Setenv("ADMINBASE_MASTER_KEY", "")
	t.Setenv("ADMINBASE_MASTER_KEY_FILE", "")
	dir := t.TempDir()
	k1, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadKey(dir)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("key must persist across loads: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "master.key")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadKeyRejectsBadEnv(t *testing.T) {
	t.Setenv("ADMINBASE_MASTER_KEY", "dG9vc2hvcnQ=")
	if _, err := LoadKey(t.TempDir()); err == nil {
		t.Fatal("short key must be rejected")
	}
}

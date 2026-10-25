package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
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
	t.Setenv("DBYSON_MASTER_KEY", "")
	t.Setenv("DBYSON_MASTER_KEY_FILE", "")
	dir := t.TempDir()
	k1, src1, _, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src1 != SourceGenerated {
		t.Fatalf("fresh data dir must report a generated key, got %q", src1)
	}
	k2, _, _, err := LoadKey(dir)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("key must persist across loads: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "master.key")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadKeyRejectsBadEnv(t *testing.T) {
	t.Setenv("DBYSON_MASTER_KEY", "dG9vc2hvcnQ=")
	if _, _, _, err := LoadKey(t.TempDir()); err == nil {
		t.Fatal("short key must be rejected")
	}
}

func TestLoadKeyReportsSource(t *testing.T) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString(raw)
	t.Setenv("DBYSON_MASTER_KEY", enc)
	t.Setenv("DBYSON_MASTER_KEY_FILE", "")
	if _, src, _, err := LoadKey(t.TempDir()); err != nil || src != SourceEnv {
		t.Fatalf("env key must report env source: %v %q", err, src)
	}
}

// The domain label is bound into every stored ciphertext; renaming it (e.g. to match the product
// name) would make every saved credential undecryptable. This test exists to stop that.
func TestCryptoDomainIsStable(t *testing.T) {
	if cryptoDomain != "adminbase/v1/" {
		t.Fatalf("cryptoDomain changed to %q: existing vaults would no longer decrypt", cryptoDomain)
	}
}

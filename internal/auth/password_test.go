package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected format: %s", h)
	}
	if ok, err := VerifyPassword("correct horse battery", h); err != nil || !ok {
		t.Fatalf("expected match, got %v %v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong password!!", h); ok {
		t.Fatal("wrong password must not verify")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same-password-123")
	b, _ := HashPassword("same-password-123")
	if a == b {
		t.Fatal("hashes must differ per call")
	}
}

func TestRejectsMalformedAndHostileHashes(t *testing.T) {
	for _, h := range []string{
		"",
		"plaintext",
		"$argon2id$v=19$m=65536,t=3,p=2$onlysalt",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=4294967295,t=3,p=2$c2FsdA$aGFzaA", // memory bomb
		"$argon2id$v=19$m=65536,t=999,p=2$c2FsdA$aGFzaA",
	} {
		if ok, err := VerifyPassword("x", h); ok || err == nil {
			t.Errorf("hash %q should be rejected, got ok=%v err=%v", h, ok, err)
		}
	}
}

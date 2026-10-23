package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B test vectors (SHA1, 8 digits) truncated to our 6 digits.
func TestTOTPRFCVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	vectors := map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	}
	for ts, want := range vectors {
		got, err := TOTPCode(secret, time.Unix(ts, 0))
		if err != nil || got != want {
			t.Errorf("t=%d: got %s, want %s (%v)", ts, got, want, err)
		}
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	secret, _ := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	code, _ := TOTPCode(secret, now)

	step, ok := VerifyTOTP(secret, code, now, 0)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("valid code rejected")
	}
	if _, ok := VerifyTOTP(secret, code, now, step); ok {
		t.Fatal("replayed code must be rejected")
	}
	if _, ok := VerifyTOTP(secret, code, now.Add(30*time.Second), 0); !ok {
		t.Fatal("previous step must be accepted (clock drift)")
	}
	if _, ok := VerifyTOTP(secret, code, now.Add(2*time.Minute), 0); ok {
		t.Fatal("old code must be rejected")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("wrong length must be rejected")
	}
	spaced := code[:3] + " " + code[3:]
	if _, ok := VerifyTOTP(secret, spaced, now, 0); !ok {
		t.Fatal("spaces should be tolerated")
	}
}

func TestTOTPURI(t *testing.T) {
	u := TOTPURI("DByson", "ana", "ABC")
	if !strings.HasPrefix(u, "otpauth://totp/DByson:ana?") || !strings.Contains(u, "secret=ABC") || !strings.Contains(u, "issuer=DByson") {
		t.Fatalf("bad uri: %s", u)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes()
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Fatalf("bad code %q", c)
		}
		seen[c] = true
	}
	if HashRecoveryCode(codes[0]) != HashRecoveryCode(" "+strings.ToUpper(strings.Replace(codes[0], "-", "", 1))+" ") {
		t.Fatal("hash must normalize case, dashes and spaces")
	}
	if LooksLikeTOTP(codes[0]) || !LooksLikeTOTP("123 456") {
		t.Fatal("LooksLikeTOTP misclassified")
	}
}

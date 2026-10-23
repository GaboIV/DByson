package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP per RFC 6238 with the parameters every authenticator app supports:
// HMAC-SHA1, 30-second steps, 6 digits.
const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1 // accept one step before/after to absorb clock drift

	RecoveryCodeCount = 10
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret, base32-encoded (RFC 4226 recommends 160 bits).
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// TOTPURI builds the otpauth:// URI that authenticator apps read from the QR code.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func totpAt(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000)
}

// TOTPCode returns the code for time t (used by tests and diagnostics).
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return totpAt(key, t.Unix()/totpPeriod), nil
}

// VerifyTOTP checks code against secret around time t. It returns the matched time step so the
// caller can store it and reject any step <= lastStep: each code is accepted at most once.
func VerifyTOTP(secret, code string, t time.Time, lastStep int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	now := t.Unix() / totpPeriod
	matched := int64(0)
	for step := now - totpSkew; step <= now+totpSkew; step++ {
		// Compare every candidate in constant time and do not stop early.
		if hmac.Equal([]byte(totpAt(key, step)), []byte(code)) && step > lastStep {
			matched = step
		}
	}
	return matched, matched != 0
}

// NewRecoveryCodes returns human-friendly single-use codes (xxxxx-xxxxx, 50 bits each).
func NewRecoveryCodes() ([]string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no look-alikes (0/o, 1/l/i)
	codes := make([]string, RecoveryCodeCount)
	buf := make([]byte, 10)
	for i := range codes {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		var sb strings.Builder
		for j, b := range buf {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(alphabet[int(b)%len(alphabet)])
		}
		codes[i] = sb.String()
	}
	return codes, nil
}

// HashRecoveryCode normalizes and hashes a recovery code. The codes are high-entropy random
// values, so a fast hash is sufficient (unlike passwords).
func HashRecoveryCode(code string) string {
	c := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	// The "adminbase-" label predates the rename and is part of every stored hash: never change it.
	sum := sha256.Sum256([]byte("adminbase-recovery:" + c))
	return hex.EncodeToString(sum[:])
}

// LooksLikeTOTP reports whether input is a 6-digit code rather than a recovery code.
func LooksLikeTOTP(code string) bool {
	c := strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(c) != totpDigits {
		return false
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

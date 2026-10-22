// Package vault encrypts connection secrets with envelope encryption:
// every record gets its own random data key (DEK), and DEKs are wrapped with the master key (KEK).
package vault

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

const keySize = chacha20poly1305.KeySize

type Vault struct {
	kek [keySize]byte
}

func New(masterKey []byte) (*Vault, error) {
	if len(masterKey) != keySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", keySize, len(masterKey))
	}
	v := &Vault{}
	copy(v.kek[:], masterKey)
	return v, nil
}

// LoadKey resolves the master key, in order of precedence:
//  1. ADMINBASE_MASTER_KEY      (base64, 32 bytes)
//  2. ADMINBASE_MASTER_KEY_FILE (file containing the base64 key; works with Docker secrets)
//  3. <dataDir>/master.key      (generated on first run with mode 0600)
//
// For a real deployment prefer 1 or 2 so the key is not stored next to the encrypted data.
func LoadKey(dataDir string) ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv("ADMINBASE_MASTER_KEY")); v != "" {
		return decodeKey(v)
	}
	if f := os.Getenv("ADMINBASE_MASTER_KEY_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read master key file: %w", err)
		}
		return decodeKey(string(b))
	}
	path := filepath.Join(dataDir, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		return decodeKey(string(b))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(enc+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}

func decodeKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("master key is not valid base64: %w", err)
	}
	if len(b) != keySize {
		return nil, fmt.Errorf("master key must decode to %d bytes, got %d", keySize, len(b))
	}
	return b, nil
}

// Seal encrypts plaintext for the record identified by id. The id is bound as associated data,
// so ciphertexts cannot be swapped between records.
func (v *Vault) Seal(id string, plaintext []byte) (wrappedDEK, ciphertext []byte, err error) {
	dek := make([]byte, keySize)
	if _, err = rand.Read(dek); err != nil {
		return nil, nil, err
	}
	defer zero(dek)

	if wrappedDEK, err = seal(v.kek[:], dek, []byte("adminbase/v1/dek/"+id)); err != nil {
		return nil, nil, err
	}
	if ciphertext, err = seal(dek, plaintext, []byte("adminbase/v1/secret/"+id)); err != nil {
		return nil, nil, err
	}
	return wrappedDEK, ciphertext, nil
}

func (v *Vault) Open(id string, wrappedDEK, ciphertext []byte) ([]byte, error) {
	dek, err := open(v.kek[:], wrappedDEK, []byte("adminbase/v1/dek/"+id))
	if err != nil {
		return nil, fmt.Errorf("unwrap data key: %w", err)
	}
	defer zero(dek)
	pt, err := open(dek, ciphertext, []byte("adminbase/v1/secret/"+id))
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}
	return pt, nil
}

// DeriveKey returns a 32-byte subkey of the master key for a single purpose (e.g. audit MACs),
// so the master key itself is never used outside the vault.
func (v *Vault) DeriveKey(label string) []byte {
	mac := hmac.New(sha256.New, v.kek[:])
	mac.Write([]byte("adminbase/v1/derive/" + label))
	return mac.Sum(nil)
}

func seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func open(key, blob, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := blob[:aead.NonceSize()], blob[aead.NonceSize():]
	return aead.Open(nil, nonce, ct, aad)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

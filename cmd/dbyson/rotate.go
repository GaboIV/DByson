package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/config"
	"github.com/gaboiv/dbyson/internal/store"
	"github.com/gaboiv/dbyson/internal/vault"
)

// runRotateKey implements `dbyson rotate-key`: re-encrypts every stored secret
// and re-signs the audit chain under a new master key, in a single transaction:
// either everything moves to the new key or nothing changes. The server must be
// stopped first (it keeps the old key in memory); afterwards point
// DBYSON_MASTER_KEY_FILE at the new key and restart.
//
// The new key is read from --new-key-file, or generated into it when the file
// does not exist. It is never taken from the command line, where it would end up
// in shell history and process listings.
func runRotateKey(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("rotate-key", flag.ContinueOnError)
	dataDir := fs.String("data-dir", config.GetOr("DATA_DIR", "./data"), "directory holding dbyson.db")
	newKeyFile := fs.String("new-key-file", "", "file with the new master key (base64); generated with mode 0600 if missing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *newKeyFile == "" {
		return fmt.Errorf("rotate-key needs --new-key-file")
	}
	if !hasDatabase(*dataDir) {
		return fmt.Errorf("no database in %s: nothing to rotate", *dataDir)
	}

	oldKey, _, oldPath, err := vault.LoadKey(*dataDir)
	if err != nil {
		return fmt.Errorf("load current master key: %w", err)
	}
	newKey, generated, err := loadOrCreateKey(*newKeyFile)
	if err != nil {
		return err
	}
	if vault.Fingerprint(newKey) == vault.Fingerprint(oldKey) {
		return fmt.Errorf("the new key is the current key: nothing to rotate")
	}
	if oldPath != "" && sameFile(oldPath, *newKeyFile) {
		return fmt.Errorf("--new-key-file must not be the file holding the current key")
	}
	oldV, err := vault.New(oldKey)
	if err != nil {
		return err
	}
	newV, err := vault.New(newKey)
	if err != nil {
		return err
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	log.Info("rotating master key", "old", vault.Fingerprint(oldKey), "new", vault.Fingerprint(newKey))

	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The audit chain is verified (and re-verified under the new key) inside
	// RekeyTx; a broken chain aborts before anything is committed. The chain is
	// signed with the audit subkey the server derives, never the master key itself.
	if err := audit.RekeyTx(ctx, tx, oldV.DeriveKey("audit"), newV.DeriveKey("audit")); err != nil {
		return rotationFailed(generated, *newKeyFile, err)
	}
	if err := vault.RewrapSecretsTx(ctx, tx, oldV, newV); err != nil {
		return rotationFailed(generated, *newKeyFile, fmt.Errorf("rewrap secrets: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return rotationFailed(generated, *newKeyFile, err)
	}

	fmt.Printf("master key rotated %s -> %s\n", vault.Fingerprint(oldKey), vault.Fingerprint(newKey))
	fmt.Printf("now set DBYSON_MASTER_KEY_FILE=%s and restart the server\n", *newKeyFile)
	if oldPath != "" && pathInside(*dataDir, oldPath) {
		fmt.Printf("then delete the old key %s: it no longer decrypts anything\n", oldPath)
	}
	return nil
}

func hasDatabase(dir string) bool {
	for _, name := range []string{store.FileName, "adminbase.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// loadOrCreateKey reads a base64 key from path, or generates one there (mode 0600,
// refusing to overwrite) when the file does not exist.
func loadOrCreateKey(path string) (key []byte, generated bool, err error) {
	if b, err := os.ReadFile(path); err == nil {
		key, err := vault.DecodeKey(string(b))
		return key, false, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("read new key file: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("create new key file: %w", err)
	}
	if _, err := f.WriteString(vault.EncodeKey(key) + "\n"); err != nil {
		f.Close()
		return nil, false, err
	}
	return key, true, f.Close()
}

// rotationFailed removes a key file generated for a rotation that did not happen,
// so nobody points the server at a key that decrypts nothing.
func rotationFailed(generated bool, path string, err error) error {
	if generated {
		_ = os.Remove(path)
	}
	return fmt.Errorf("rotation aborted, nothing changed: %w", err)
}

func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

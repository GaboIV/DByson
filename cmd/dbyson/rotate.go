package main

import (
	"context"
	"crypto/rand"
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
// and re-signs the audit chain under a new master key. The server must be
// stopped first; afterwards point DBYSON_MASTER_KEY (or _FILE) at the new key
// and restart.
func runRotateKey(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("rotate-key", flag.ContinueOnError)
	dataDir := fs.String("data-dir", config.GetOr("DATA_DIR", "./data"), "directory holding dbyson.db")
	newKeyB64 := fs.String("new-key", "", "new master key (base64, 32 bytes)")
	newKeyFile := fs.String("new-key-file", "", "generate a new key and write it here (mode 0600)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*newKeyB64 == "") == (*newKeyFile == "") {
		return fmt.Errorf("rotate-key needs exactly one of --new-key or --new-key-file")
	}
	for _, name := range []string{store.FileName, "adminbase.db"} {
		if _, err := os.Stat(filepath.Join(*dataDir, name)); err == nil {
			goto haveDB
		}
	}
	return fmt.Errorf("no database in %s: nothing to rotate", *dataDir)
haveDB:

	oldKey, _, _, err := vault.LoadKey(*dataDir)
	if err != nil {
		return fmt.Errorf("load current master key: %w", err)
	}
	var newKey []byte
	if *newKeyB64 != "" {
		newKey, err = vault.DecodeKey(*newKeyB64)
		if err != nil {
			return err
		}
	} else {
		newKey = make([]byte, 32)
		if _, err := rand.Read(newKey); err != nil {
			return err
		}
		if err := os.WriteFile(*newKeyFile, []byte(vault.EncodeKey(newKey)), 0o600); err != nil {
			return fmt.Errorf("write new key file: %w", err)
		}
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
	log.Info("rotating vault secrets", "old", vault.Fingerprint(oldKey), "new", vault.Fingerprint(newKey))
	if err := vault.RewrapSecrets(ctx, st.DB, oldV, newV); err != nil {
		return fmt.Errorf("rewrap secrets: %w", err)
	}
	if err := audit.Rekey(ctx, st.DB, oldKey, newKey); err != nil {
		return fmt.Errorf("rekey audit chain: %w", err)
	}
	if v, err := audit.New(st.DB, newV.DeriveKey("audit")).Verify(ctx); err != nil || !v.OK {
		return fmt.Errorf("post-rotation audit verification failed (checked %d): %w", v.Checked, err)
	}
	log.Info("rotation complete",
		"old", vault.Fingerprint(oldKey), "new", vault.Fingerprint(newKey))
	fmt.Printf("master key rotated %s -> %s\n", vault.Fingerprint(oldKey), vault.Fingerprint(newKey))
	if *newKeyFile != "" {
		fmt.Printf("new key written to %s; point DBYSON_MASTER_KEY_FILE at it and restart\n", *newKeyFile)
	} else {
		fmt.Println("point DBYSON_MASTER_KEY at the new key and restart")
	}
	return nil
}

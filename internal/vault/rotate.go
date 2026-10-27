package vault

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// RewrapSecrets re-encrypts every stored secret from oldV to newV: connection
// profiles, saved queries and TOTP seeds. The record ids double as
// associated data, so they must match the ids used at seal time:
//
//   - connections: the connection id
//   - saved queries: "query/"+id (see savedQueries in internal/api)
//   - TOTP seeds: "totp:"+userID (see totpVaultID in internal/api)
//
// Run it offline with the server stopped. Key rotation calls RewrapSecretsTx
// together with audit.RekeyTx in one transaction.
func RewrapSecrets(ctx context.Context, db *sql.DB, oldV, newV *Vault) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := RewrapSecretsTx(ctx, tx, oldV, newV); err != nil {
		return err
	}
	return tx.Commit()
}

// RewrapSecretsTx is RewrapSecrets inside an existing transaction; it does not commit.
func RewrapSecretsTx(ctx context.Context, tx *sql.Tx, oldV, newV *Vault) error {

	rewrap := func(table, idCol, idPrefix string) error {
		q := fmt.Sprintf(`SELECT %s, wrapped_dek, secret FROM %s`, idCol, table)
		rows, err := tx.QueryContext(ctx, q)
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		type item struct {
			id            string
			wrapped, seal []byte
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.wrapped, &it.seal); err != nil {
				rows.Close()
				return fmt.Errorf("%s: %w", table, err)
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		for _, it := range items {
			plain, err := oldV.Open(idPrefix+it.id, it.wrapped, it.seal)
			if err != nil {
				return fmt.Errorf("%s %s: %w", table, it.id, err)
			}
			wrapped, sealed, err := newV.Seal(idPrefix+it.id, plain)
			zero(plain)
			if err != nil {
				return fmt.Errorf("%s %s: %w", table, it.id, err)
			}
			up := fmt.Sprintf(`UPDATE %s SET wrapped_dek = ?, secret = ? WHERE %s = ?`, table, idCol)
			if _, err := tx.ExecContext(ctx, up, wrapped, sealed, it.id); err != nil {
				return fmt.Errorf("%s %s: %w", table, it.id, err)
			}
		}
		return nil
	}

	if err := rewrap("connections", "id", ""); err != nil {
		return err
	}
	if err := rewrap("saved_queries", "id", "query/"); err != nil {
		return err
	}
	return rewrapTOTP(ctx, tx, oldV, newV)
}

func rewrapTOTP(ctx context.Context, tx *sql.Tx, oldV, newV *Vault) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, totp_wrapped, totp_secret FROM users WHERE totp_wrapped IS NOT NULL AND totp_secret IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("users totp: %w", err)
	}
	defer rows.Close()
	type item struct {
		id            int64
		wrapped, seal []byte
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.wrapped, &it.seal); err != nil {
			return fmt.Errorf("users totp: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("users totp: %w", err)
	}
	for _, it := range items {
		vaultID := "totp:" + strconv.FormatInt(it.id, 10)
		plain, err := oldV.Open(vaultID, it.wrapped, it.seal)
		if err != nil {
			return fmt.Errorf("users totp %d: %w", it.id, err)
		}
		wrapped, sealed, err := newV.Seal(vaultID, plain)
		zero(plain)
		if err != nil {
			return fmt.Errorf("users totp %d: %w", it.id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET totp_wrapped = ?, totp_secret = ? WHERE id = ?`, wrapped, sealed, it.id); err != nil {
			return fmt.Errorf("users totp %d: %w", it.id, err)
		}
	}
	return nil
}

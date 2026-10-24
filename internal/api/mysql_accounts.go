package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gaboiv/dbyson/internal/drivers/mysql"
)

var accountPart = regexp.MustCompile(`^[a-zA-Z0-9_.%:-]{1,128}$`)
var accountPrivileges = map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "CREATE": true, "ALTER": true, "INDEX": true, "DROP": true}

func (s *Server) mysqlAccounts(w http.ResponseWriter, r *http.Request) {
	if sessionFrom(r).Role != "admin" {
		writeError(w, 403, "administrator required")
		return
	}
	var in struct {
		Action     string   `json:"action"`
		User       string   `json:"user"`
		Host       string   `json:"host"`
		Password   string   `json:"password"`
		Database   string   `json:"database"`
		Table      string   `json:"table"`
		Privileges []string `json:"privileges"`
		confirmationInput
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	db, rec, err := s.openDB(ctx, r, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()
	if in.Action == "list" {
		result, err := mysql.ExecuteSQL(ctx, db, "", "SELECT User, Host, plugin FROM mysql.user ORDER BY User,Host LIMIT 500", 500)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	if !accountPart.MatchString(in.User) || !accountPart.MatchString(in.Host) {
		writeError(w, 400, "invalid MySQL account")
		return
	}
	account := "'" + in.User + "'@'" + in.Host + "'"
	if in.Action == "grants" {
		result, err := mysql.ExecuteSQL(ctx, db, "", "SHOW GRANTS FOR "+account, 500)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	if rec.ReadOnly {
		writeError(w, 403, "connection is read-only")
		return
	}
	var statement string
	var args []any
	switch in.Action {
	case "create":
		if len(in.Password) < 12 || len(in.Password) > 256 {
			writeError(w, 400, "password must be 12-256 bytes")
			return
		}
		statement = "CREATE USER " + account + " IDENTIFIED BY ?"
		args = []any{in.Password}
	case "drop":
		statement = "DROP USER " + account
	case "grant", "revoke":
		if in.Database == "" || len(in.Database) > 64 || strings.ContainsAny(in.Database, "\x00%\\") || len(in.Table) > 64 || strings.ContainsAny(in.Table, "\x00%\\") || len(in.Privileges) == 0 || len(in.Privileges) > 8 {
			writeError(w, 400, "select an exact database/table and privileges (wildcards are not supported)")
			return
		}
		for _, p := range in.Privileges {
			if !accountPrivileges[p] {
				writeError(w, 400, "unsupported privilege")
				return
			}
		}
		// At database scope MySQL treats underscore as a wildcard even inside backticks.
		target := mysql.EscapeIdent(strings.ReplaceAll(in.Database, "_", "\\_")) + ".*"
		if in.Table != "" {
			target = mysql.EscapeIdent(in.Database) + "." + mysql.EscapeIdent(in.Table)
		}
		statement = "GRANT " + strings.Join(in.Privileges, ",") + " ON " + target + " TO " + account
		if in.Action == "revoke" {
			statement = "REVOKE " + strings.Join(in.Privileges, ",") + " ON " + target + " FROM " + account
		}
	default:
		writeError(w, 400, "unsupported account action")
		return
	}
	// The password is bound into the challenge digest, never put in audit/preview/response.
	binding, _ := json.Marshal([]any{in.Action, in.User, in.Host, in.Database, in.Table, in.Privileges, in.Password})
	hash := sha256.Sum256(binding)
	if !s.requireConfirmation(w, r, fmt.Sprintf("MYSQL ACCOUNT\n%x", hash), account, "Modifica cuentas o permisos de MySQL. Los privilegios efectivos los decide el servidor MySQL.", statement, in.confirmationInput) {
		return
	}
	if _, err = db.ExecContext(ctx, statement, args...); err != nil {
		writeError(w, 502, "MySQL rechazó el cambio de cuenta. Revisa los permisos, la política de contraseñas y la compatibilidad del servidor.")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

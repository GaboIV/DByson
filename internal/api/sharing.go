package api

import (
	"database/sql"
	"net/http"
	"time"
)

// grantCandidates exposes only the directory needed to share this connection.
// It never grants account administration or exposes email addresses.
func (s *Server) grantCandidates(w http.ResponseWriter, r *http.Request) {
	rec, err := s.peekConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.canManageConnection(r.Context(), sessionFrom(r), rec) {
		s.fail(w, sql.ErrNoRows)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, username, role FROM users WHERE disabled = 0 AND role <> 'admin' AND id <> ? ORDER BY username COLLATE NOCASE`, rec.OwnerID)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer rows.Close()
	type candidate struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	out := []candidate{}
	for rows.Next() {
		var u candidate
		if err := rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// updateSharing changes visibility without resubmitting a possibly stale profile
// or touching its credentials. Turning sharing off revokes grants atomically.
func (s *Server) updateSharing(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Shared *bool `json:"shared"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Shared == nil {
		writeError(w, http.StatusBadRequest, "shared is required")
		return
	}
	id := r.PathValue("id")
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), `UPDATE connections SET shared = ?, updated_at = ? WHERE id = ?`, *in.Shared, time.Now().Unix(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, err := result.RowsAffected(); err != nil {
		s.fail(w, err)
		return
	} else if n == 0 {
		s.fail(w, sql.ErrNoRows)
		return
	}
	if !*in.Shared {
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM connection_grants WHERE connection_id = ?`, id); err != nil {
			s.fail(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, err)
		return
	}
	auditFrom(r).entry.ConnectionID = id
	if *in.Shared {
		auditFrom(r).entry.Detail = "team sharing enabled"
	} else {
		auditFrom(r).entry.Detail = "team sharing disabled; all grants revoked"
	}
	writeJSON(w, http.StatusOK, map[string]bool{"shared": *in.Shared})
}

package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/auth"
	"github.com/gaboiv/dbyson/internal/tunnel"
)

// Team roles. Admins see and manage everything; members own private connections
// and use shared ones they were granted; viewers are read-only and never open
// prod, even with a grant.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// Grant levels on a shared connection.
const (
	GrantUse    = "use"    // open, query, export
	GrantManage = "manage" // use + edit the profile and its grants
)

func validRole(r string) bool {
	return r == RoleAdmin || r == RoleMember || r == RoleViewer
}

func validGrantLevel(l string) bool { return l == GrantUse || l == GrantManage }

// requireAdmin rejects non-admin sessions. It returns false after writing the response.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if sessionFrom(r).Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "admin role required")
		return false
	}
	return true
}

// denyWritesForViewer rejects state-changing requests from the viewer role.
func denyWritesForViewer(w http.ResponseWriter, r *http.Request) bool {
	if sessionFrom(r).Role == RoleViewer {
		writeError(w, http.StatusForbidden, "viewer role is read-only")
		return true
	}
	return false
}

// grantLevel returns the grant level the user holds on the connection, or "".
func (s *Server) grantLevel(ctx context.Context, connectionID string, userID int64) (string, error) {
	var level string
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT level FROM connection_grants WHERE connection_id = ? AND user_id = ?`,
		connectionID, userID).Scan(&level)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return level, err
}

// loadConnection fetches a connection the session user may use. need is GrantUse
// or GrantManage. Unknown, foreign and ungranted connections all report
// sql.ErrNoRows, so private IDs stay indistinguishable from nonexistent ones.
func (s *Server) loadConnection(ctx context.Context, r *http.Request, need string) (*connRecord, secretBlob, error) {
	sess := sessionFrom(r)
	id := r.PathValue("id")
	rec, err := scanConn(s.Store.DB.QueryRowContext(ctx,
		`SELECT `+connColumns+` FROM connections WHERE id = ?`, id))
	if err != nil {
		return nil, secretBlob{}, err
	}
	if !ownerOrAdmin(sess, rec) {
		if rec.Shared == 0 {
			return nil, secretBlob{}, sql.ErrNoRows
		}
		level, err := s.grantLevel(ctx, rec.ID, sess.UserID)
		if err != nil {
			return nil, secretBlob{}, err
		}
		if level == "" || (need == GrantManage && level != GrantManage) {
			return nil, secretBlob{}, sql.ErrNoRows
		}
	}
	// Environment rule: viewers never open prod, even when granted, and even on a
	// connection they own (e.g. a member demoted to viewer keeps their profiles).
	if sess.Role == RoleViewer && rec.Env == "prod" {
		return nil, secretBlob{}, errViewerProd
	}
	return s.openLoaded(rec)
}

func ownerOrAdmin(sess *auth.Session, rec *connRecord) bool {
	return sess.Role == RoleAdmin || rec.OwnerID == sess.UserID
}

func connectionAccess(sess *auth.Session, rec *connRecord, level string) string {
	if sess.Role == RoleViewer {
		return GrantUse
	}
	if ownerOrAdmin(sess, rec) {
		return "owner"
	}
	return level
}

// tlsStrength orders TLS modes from weakest to strongest.
var tlsStrength = map[string]int{"disabled": 0, "required": 1, "verify_ca": 2, "verify_identity": 3}

func sameHop(a, b tunnel.Hop) bool { return a.Host == b.Host && a.Port == b.Port && a.User == b.User }

// destinationChanged reports whether a draft would send the stored credentials
// somewhere other than where they were saved for: another MySQL host, port or
// user, or another SSH server or bastion.
func destinationChanged(rec *connRecord, stored secretBlob, in connInput) bool {
	if in.Host != rec.Host || in.Port != rec.Port || in.Username != rec.Username {
		return true
	}
	if in.SSH == nil {
		return false
	}
	if in.SSH.Enabled != stored.SSH.Enabled || (in.SSH.Enabled && !sameHop(in.SSH.Hop, stored.SSH.Hop)) {
		return true
	}
	switch {
	case in.SSH.Jump == nil && stored.SSH.Jump == nil:
		return false
	case in.SSH.Jump == nil || stored.SSH.Jump == nil:
		return true
	default:
		return !sameHop(*in.SSH.Jump, *stored.SSH.Jump)
	}
}

// secretsFor returns the stored secrets a non-owner may build on. A manage grant
// lets someone edit a shared profile, not carry its stored passwords and keys to
// a destination of their choosing: there, only the credentials they type count.
func secretsFor(sess *auth.Session, rec *connRecord, stored secretBlob, in connInput) secretBlob {
	if ownerOrAdmin(sess, rec) || !destinationChanged(rec, stored, in) {
		return stored
	}
	return secretBlob{}
}

// loweredProtection names a protection a non-owner tried to weaken on a shared
// profile, or "" when the edit keeps them. Owners and admins decide these.
func loweredProtection(rec *connRecord, in connInput) string {
	switch {
	case in.Env != rec.Env:
		return "the environment"
	case rec.ReadOnly && !in.ReadOnly:
		return "read-only mode"
	case tlsStrength[in.TLSMode] < tlsStrength[rec.TLSMode]:
		return "TLS verification"
	}
	return ""
}

// errViewerProd is a permission error, not a missing row: handlers answer 403.
var errViewerProd = errors.New("viewer role cannot open prod connections")

func (s *Server) openLoaded(rec *connRecord) (*connRecord, secretBlob, error) {
	sec, err := s.openSecret(rec)
	return rec, sec, err
}

func (s *Server) failAccess(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errViewerProd) {
		auditFrom(r).entry.Detail = errViewerProd.Error()
		auditFrom(r).entry.Outcome = audit.OutcomeDenied
		writeError(w, http.StatusForbidden, errViewerProd.Error())
		return
	}
	s.fail(w, err)
}

// peekConnection loads the row without any access check, for handlers that need
// to distinguish "not found" from "forbidden" (grants management).
func (s *Server) peekConnection(ctx context.Context, id string) (*connRecord, error) {
	return scanConn(s.Store.DB.QueryRowContext(ctx,
		`SELECT `+connColumns+` FROM connections WHERE id = ?`, id))
}

// canManageConnection reports whether sess may edit the profile or its grants:
// admins, the owner, and holders of a manage grant.
func (s *Server) canManageConnection(ctx context.Context, sess *auth.Session, rec *connRecord) bool {
	if sess.Role == RoleViewer {
		return false
	}
	if ownerOrAdmin(sess, rec) {
		return true
	}
	if rec.Shared == 0 {
		return false
	}
	level, err := s.grantLevel(ctx, rec.ID, sess.UserID)
	return err == nil && level == GrantManage
}

// ---- grants ----

type grantView struct {
	UserID   int64  `json:"userId"`
	Username string `json:"username"`
	Level    string `json:"level"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

func (s *Server) listGrants(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	rec, err := s.peekConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.canManageConnection(r.Context(), sess, rec) {
		s.fail(w, sql.ErrNoRows)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT g.user_id, u.username, g.level, u.role, u.disabled FROM connection_grants g
		 JOIN users u ON u.id = g.user_id WHERE g.connection_id = ? ORDER BY u.username COLLATE NOCASE`,
		rec.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer rows.Close()
	out := []grantView{}
	for rows.Next() {
		var g grantView
		if err := rows.Scan(&g.UserID, &g.Username, &g.Level, &g.Role, &g.Disabled); err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createGrant(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID int64  `json:"userId"`
		Level  string `json:"level"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Level = strings.TrimSpace(in.Level)
	if !validGrantLevel(in.Level) {
		writeError(w, http.StatusBadRequest, "level must be use or manage")
		return
	}
	sess := sessionFrom(r)
	rec, err := s.peekConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.canManageConnection(r.Context(), sess, rec) {
		s.fail(w, sql.ErrNoRows)
		return
	}
	if rec.Shared == 0 {
		writeError(w, http.StatusBadRequest, "only shared connections accept grants")
		return
	}
	var target struct {
		id       int64
		username string
		role     string
		disabled bool
	}
	err = s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, username, role, disabled FROM users WHERE id = ?`, in.UserID).
		Scan(&target.id, &target.username, &target.role, &target.disabled)
	if err == sql.ErrNoRows || target.disabled {
		writeError(w, http.StatusBadRequest, "unknown user")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if target.id == rec.OwnerID {
		writeError(w, http.StatusBadRequest, "the owner already has full access")
		return
	}
	if target.role == RoleAdmin {
		writeError(w, http.StatusBadRequest, "admins already have full access")
		return
	}
	// Recheck visibility, recipient and manager access in the write itself so a
	// concurrent unshare/revocation cannot leave a dormant grant behind.
	result, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO connection_grants (connection_id, user_id, level)
		 SELECT c.id, u.id, ? FROM connections c JOIN users u ON u.id = ?
		 WHERE c.id = ? AND c.shared <> 0 AND u.disabled = 0 AND u.role <> 'admin' AND u.id <> c.owner_id
		 AND (? = 'admin' OR c.owner_id = ? OR EXISTS
		   (SELECT 1 FROM connection_grants g WHERE g.connection_id = c.id AND g.user_id = ? AND g.level = 'manage'))
		 ON CONFLICT(connection_id, user_id) DO UPDATE SET level = excluded.level`,
		in.Level, target.id, rec.ID, sess.Role, sess.UserID, sess.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, err := result.RowsAffected(); err != nil {
		s.fail(w, err)
		return
	} else if n == 0 {
		writeError(w, http.StatusConflict, "connection or recipient access changed; refresh and retry")
		return
	}
	auditFrom(r).entry.Target = target.username
	auditFrom(r).entry.Detail = "level=" + in.Level
	writeJSON(w, http.StatusCreated, grantView{UserID: target.id, Username: target.username, Level: in.Level})
}

func (s *Server) deleteGrant(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	rec, err := s.peekConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.canManageConnection(r.Context(), sess, rec) {
		s.fail(w, sql.ErrNoRows)
		return
	}
	targetID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`DELETE FROM connection_grants WHERE connection_id = ? AND user_id = ?`, rec.ID, targetID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "grant not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- users (admin) ----

type userView struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role"`
	Disabled  bool   `json:"disabled"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, username, email, role, disabled, created_at FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer rows.Close()
	out := []userView{}
	for rows.Next() {
		var u userView
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Disabled, &u.CreatedAt); err != nil {
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

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Email    string `json:"email"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	in.Role = strings.TrimSpace(in.Role)
	in.Email = strings.TrimSpace(in.Email)
	if !validUsername(in.Username) {
		writeError(w, http.StatusBadRequest, "username must be 3-64 characters without spaces")
		return
	}
	if in.Role == "" {
		in.Role = RoleMember
	}
	if !validRole(in.Role) {
		writeError(w, http.StatusBadRequest, "role must be admin, member or viewer")
		return
	}
	if n := len([]rune(in.Password)); n < auth.MinPasswordLen || n > auth.MaxPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be between 12 and 256 characters")
		return
	}
	if !validEmail(in.Email) {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO users (username, password_hash, role, email, created_at) VALUES (?,?,?,?,?)`,
		in.Username, hash, in.Role, in.Email, time.Now().Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		s.fail(w, err)
		return
	}
	id, _ := res.LastInsertId()
	auditFrom(r).entry.Target = in.Username
	auditFrom(r).entry.Detail = "role=" + in.Role
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "username": in.Username, "role": in.Role})
}

var errLastAdmin = errors.New("the server must keep at least one active admin")

// keepAnAdmin fails inside tx when its writes left no enabled admin. Checking after
// the write, in the same transaction, is what makes it race-free: SQLite serializes
// writers, so a concurrent demotion sees this one committed and is rolled back.
func keepAnAdmin(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return errLastAdmin
	}
	return nil
}

func validEmail(email string) bool {
	return len(email) <= 254 && (email == "" || strings.Contains(email, "@"))
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var in struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Password *string `json:"password"`
		Email    *string `json:"email"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var username string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT username FROM users WHERE id = ?`, id).Scan(&username)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}

	// Validate everything before changing anything.
	sets, args, detail := []string{}, []any{}, []string{}
	revoke := false // every session of the account ends
	if in.Role != nil {
		role := strings.TrimSpace(*in.Role)
		if !validRole(role) {
			writeError(w, http.StatusBadRequest, "role must be admin, member or viewer")
			return
		}
		sets, args, detail = append(sets, "role = ?"), append(args, role), append(detail, "role="+role)
	}
	if in.Disabled != nil {
		if *in.Disabled && id == sessionFrom(r).UserID {
			writeError(w, http.StatusConflict, "cannot disable your own account")
			return
		}
		sets, args = append(sets, "disabled = ?"), append(args, *in.Disabled)
		detail = append(detail, "disabled="+strconv.FormatBool(*in.Disabled))
		revoke = revoke || *in.Disabled
	}
	if in.Password != nil {
		if n := len([]rune(*in.Password)); n < auth.MinPasswordLen || n > auth.MaxPasswordLen {
			writeError(w, http.StatusBadRequest, "password must be between 12 and 256 characters")
			return
		}
		hash, err := auth.HashPassword(*in.Password)
		if err != nil {
			s.fail(w, err)
			return
		}
		sets, args, detail = append(sets, "password_hash = ?"), append(args, hash), append(detail, "password reset")
		revoke = true
	}
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if !validEmail(email) {
			writeError(w, http.StatusBadRequest, "invalid email")
			return
		}
		sets, args, detail = append(sets, "email = ?"), append(args, email), append(detail, "email changed")
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}

	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...); err != nil {
		s.fail(w, err)
		return
	}
	if err := keepAnAdmin(r.Context(), tx); err != nil {
		if errors.Is(err, errLastAdmin) {
			writeError(w, http.StatusConflict, "cannot demote or disable the last active admin")
			return
		}
		s.fail(w, err)
		return
	}
	if revoke {
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			s.fail(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, err)
		return
	}
	if revoke {
		s.queries.revokeSessions(id, func(string) bool { return true })
	}
	auditFrom(r).entry.Target = username
	auditFrom(r).entry.Detail = strings.Join(detail, " ")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// deleteUser removes an account and, through the foreign keys, its private
// connections, sessions, grants and SSO links. Shared connections it owns must be
// handed over first, so nobody else loses access by surprise.
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if id == sessionFrom(r).UserID {
		writeError(w, http.StatusConflict, "cannot delete your own account")
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer tx.Rollback()
	var username string
	if err := tx.QueryRowContext(r.Context(), `SELECT username FROM users WHERE id = ?`, id).Scan(&username); err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	var sharedOwned int
	if err := tx.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM connections WHERE owner_id = ? AND shared <> 0`, id).Scan(&sharedOwned); err != nil {
		s.fail(w, err)
		return
	}
	if sharedOwned > 0 {
		writeError(w, http.StatusConflict, "unshare or reassign their shared connections first")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM users WHERE id = ?`, id); err != nil {
		s.fail(w, err)
		return
	}
	if err := keepAnAdmin(r.Context(), tx); err != nil {
		if errors.Is(err, errLastAdmin) {
			writeError(w, http.StatusConflict, "cannot delete the last active admin")
			return
		}
		s.fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, err)
		return
	}
	s.queries.revokeSessions(id, func(string) bool { return true })
	auditFrom(r).entry.Target = username
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

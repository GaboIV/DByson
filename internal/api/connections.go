package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adminbase/adminbase/internal/drivers/mysql"
	"github.com/adminbase/adminbase/internal/tunnel"
)

var validEnvs = map[string]bool{"local": true, "dev": true, "staging": true, "prod": true}

// connInput is the request body for create/update/test. Secret fields are pointers:
// nil keeps the stored value on update, "" clears it, anything else replaces it.
type connInput struct {
	SSH        *tunnel.Config `json:"ssh,omitempty"`
	Name       string         `json:"name"`
	Env        string         `json:"env"`
	Host       string         `json:"host"`
	Port       int            `json:"port"`
	Username   string         `json:"username"`
	Database   string         `json:"database"`
	TLSMode    string         `json:"tlsMode"`
	ReadOnly   bool           `json:"readOnly"`
	Password   *string        `json:"password"`
	CACert     *string        `json:"caCert"`
	ClientCert *string        `json:"clientCert"`
	ClientKey  *string        `json:"clientKey"`
}

// secretBlob is what gets encrypted in the vault. Nothing in it is ever returned by the API.
type secretBlob struct {
	SSH        tunnel.Config `json:"ssh"`
	Password   string        `json:"password,omitempty"`
	CACert     string        `json:"caCert,omitempty"`
	ClientCert string        `json:"clientCert,omitempty"`
	ClientKey  string        `json:"clientKey,omitempty"`
}

type connRecord struct {
	ID        string
	OwnerID   int64
	Name      string
	Env       string
	Host      string
	Port      int
	Username  string
	Database  string
	TLSMode   string
	ReadOnly  bool
	Wrapped   []byte
	Sealed    []byte
	CreatedAt int64
	UpdatedAt int64
}

type connView struct {
	SSH           tunnel.Config `json:"ssh"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Env           string        `json:"env"`
	Host          string        `json:"host"`
	Port          int           `json:"port"`
	Username      string        `json:"username"`
	Database      string        `json:"database"`
	TLSMode       string        `json:"tlsMode"`
	ReadOnly      bool          `json:"readOnly"`
	HasPassword   bool          `json:"hasPassword"`
	HasCACert     bool          `json:"hasCaCert"`
	HasClientCert bool          `json:"hasClientCert"`
	CreatedAt     int64         `json:"createdAt"`
	UpdatedAt     int64         `json:"updatedAt"`
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// validate normalizes in and returns a user-facing error message, or "".
func (in *connInput) validate() string {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.Database = strings.TrimSpace(in.Database)
	if in.Port == 0 {
		in.Port = 3306
	}
	if in.TLSMode == "" {
		in.TLSMode = mysql.TLSVerifyIdentity // secure by default
	}
	if in.Env == "" {
		in.Env = "dev"
	}
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 100:
		return "name is required (max 100 characters)"
	case !validEnvs[in.Env]:
		return "env must be one of local, dev, staging, prod"
	case in.Host == "" || len(in.Host) > 253 || strings.ContainsAny(in.Host, " /\\@?#"):
		return "host is required and must be a hostname or IP"
	case in.Port < 1 || in.Port > 65535:
		return "port must be between 1 and 65535"
	case in.Username == "" || len(in.Username) > 128:
		return "username is required (max 128 characters)"
	case len(in.Database) > 128:
		return "database name is too long"
	case !mysql.ValidTLSMode(in.TLSMode):
		return "tlsMode must be one of disabled, required, verify_ca, verify_identity"
	}
	return ""
}

func (in *connInput) driverConfig(sec secretBlob) mysql.Config {
	return mysql.Config{
		Host: in.Host, Port: in.Port, User: in.Username, Database: in.Database,
		Password: sec.Password, TLSMode: in.TLSMode,
		ReadOnly: in.ReadOnly,
		CACert:   sec.CACert, ClientCert: sec.ClientCert, ClientKey: sec.ClientKey,
	}
}

// applySecrets merges the input's secret fields over base and checks the TLS material parses.
func (in *connInput) applySecrets(base secretBlob) (secretBlob, string) {
	out := base
	if in.SSH != nil {
		out.SSH = tunnel.Merge(*in.SSH, base.SSH)
	}
	if err := out.SSH.Validate(); err != nil {
		return out, err.Error()
	}
	if in.Password != nil {
		out.Password = *in.Password
	}
	if in.CACert != nil {
		out.CACert = strings.TrimSpace(*in.CACert)
	}
	if in.ClientCert != nil {
		out.ClientCert = strings.TrimSpace(*in.ClientCert)
	}
	if in.ClientKey != nil {
		out.ClientKey = strings.TrimSpace(*in.ClientKey)
	}
	if _, err := mysql.BuildTLS(in.driverConfig(out)); err != nil {
		return out, err.Error()
	}
	return out, ""
}

func view(rec *connRecord, sec secretBlob) connView {
	return connView{
		SSH: sec.SSH.Redacted(), ID: rec.ID, Name: rec.Name, Env: rec.Env, Host: rec.Host, Port: rec.Port,
		Username: rec.Username, Database: rec.Database, TLSMode: rec.TLSMode, ReadOnly: rec.ReadOnly,
		HasPassword: sec.Password != "", HasCACert: sec.CACert != "", HasClientCert: sec.ClientCert != "" && sec.ClientKey != "",
		CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	}
}

const connColumns = `id, owner_id, name, env, host, port, username, dbname, tls_mode, read_only, wrapped_dek, secret, created_at, updated_at`

func scanConn(sc interface{ Scan(...any) error }) (*connRecord, error) {
	var rec connRecord
	var ro int
	err := sc.Scan(&rec.ID, &rec.OwnerID, &rec.Name, &rec.Env, &rec.Host, &rec.Port, &rec.Username,
		&rec.Database, &rec.TLSMode, &ro, &rec.Wrapped, &rec.Sealed, &rec.CreatedAt, &rec.UpdatedAt)
	rec.ReadOnly = ro != 0
	return &rec, err
}

// loadOwned fetches a connection only if it belongs to the session user, so IDs of other
// users' connections are indistinguishable from nonexistent ones.
func (s *Server) loadOwned(ctx context.Context, r *http.Request) (*connRecord, secretBlob, error) {
	sess := sessionFrom(r)
	rec, err := scanConn(s.Store.DB.QueryRowContext(ctx,
		`SELECT `+connColumns+` FROM connections WHERE id = ? AND owner_id = ?`, r.PathValue("id"), sess.UserID))
	if err != nil {
		return nil, secretBlob{}, err
	}
	sec, err := s.openSecret(rec)
	return rec, sec, err
}

func (s *Server) openSecret(rec *connRecord) (secretBlob, error) {
	var sec secretBlob
	plain, err := s.Vault.Open(rec.ID, rec.Wrapped, rec.Sealed)
	if err != nil {
		return sec, err
	}
	return sec, json.Unmarshal(plain, &sec)
}

func (s *Server) sealSecret(id string, sec secretBlob) (wrapped, sealed []byte, err error) {
	plain, err := json.Marshal(sec)
	if err != nil {
		return nil, nil, err
	}
	return s.Vault.Seal(id, plain)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "connection not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT `+connColumns+` FROM connections WHERE owner_id = ? ORDER BY name COLLATE NOCASE`, sessionFrom(r).UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer rows.Close()
	out := []connView{}
	for rows.Next() {
		rec, err := scanConn(rows)
		if err != nil {
			s.fail(w, err)
			return
		}
		sec, err := s.openSecret(rec)
		if err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, view(rec, sec))
	}
	if err := rows.Err(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getConnection(w http.ResponseWriter, r *http.Request) {
	rec, sec, err := s.loadOwned(r.Context(), r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view(rec, sec))
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var in connInput
	if !readJSON(w, r, &in) {
		return
	}
	if msg := in.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	sec, msg := in.applySecrets(secretBlob{})
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	id := newID()
	wrapped, sealed, err := s.sealSecret(id, sec)
	if err != nil {
		s.fail(w, err)
		return
	}
	now := time.Now().Unix()
	ro := 0
	if in.ReadOnly {
		ro = 1
	}
	_, err = s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO connections (`+connColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, sessionFrom(r).UserID, in.Name, in.Env, in.Host, in.Port, in.Username, in.Database,
		in.TLSMode, ro, wrapped, sealed, now, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	rec := &connRecord{ID: id, Name: in.Name, Env: in.Env, Host: in.Host, Port: in.Port, Username: in.Username,
		Database: in.Database, TLSMode: in.TLSMode, ReadOnly: in.ReadOnly, CreatedAt: now, UpdatedAt: now}
	writeJSON(w, http.StatusCreated, view(rec, sec))
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	var in connInput
	if !readJSON(w, r, &in) {
		return
	}
	rec, current, err := s.loadOwned(r.Context(), r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if msg := in.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	sec, msg := in.applySecrets(current)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	wrapped, sealed, err := s.sealSecret(rec.ID, sec) // fresh data key on every write
	if err != nil {
		s.fail(w, err)
		return
	}
	now := time.Now().Unix()
	ro := 0
	if in.ReadOnly {
		ro = 1
	}
	_, err = s.Store.DB.ExecContext(r.Context(),
		`UPDATE connections SET name=?, env=?, host=?, port=?, username=?, dbname=?, tls_mode=?, read_only=?,
		 wrapped_dek=?, secret=?, updated_at=? WHERE id=? AND owner_id=?`,
		in.Name, in.Env, in.Host, in.Port, in.Username, in.Database, in.TLSMode, ro,
		wrapped, sealed, now, rec.ID, rec.OwnerID)
	if err != nil {
		s.fail(w, err)
		return
	}
	rec.Name, rec.Env, rec.Host, rec.Port, rec.Username, rec.Database = in.Name, in.Env, in.Host, in.Port, in.Username, in.Database
	rec.TLSMode, rec.ReadOnly, rec.UpdatedAt = in.TLSMode, in.ReadOnly, now
	writeJSON(w, http.StatusOK, view(rec, sec))
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	res, err := s.Store.DB.ExecContext(r.Context(),
		`DELETE FROM connections WHERE id = ? AND owner_id = ?`, r.PathValue("id"), sessionFrom(r).UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "connection not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type testResponse struct {
	OK     bool              `json:"ok"`
	Error  string            `json:"error,omitempty"`
	Result *mysql.TestResult `json:"result,omitempty"`
}

func (s *Server) runTest(w http.ResponseWriter, r *http.Request, cfg mysql.Config, sec secretBlob) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cfg.ReadOnly = cfg.ReadOnly || r.Header.Get("X-DByson-Read-Only") == "1"
	res, err := mysql.Test(ctx, cfg, s.connectionDialer(sec))
	if err != nil {
		writeJSON(w, http.StatusOK, testResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, testResponse{OK: true, Result: res})
}

func (s *Server) testSavedConnection(w http.ResponseWriter, r *http.Request) {
	rec, sec, err := s.loadOwned(r.Context(), r)
	if err != nil {
		s.fail(w, err)
		return
	}
	in := connInput{Host: rec.Host, Port: rec.Port, Username: rec.Username, Database: rec.Database, TLSMode: rec.TLSMode, ReadOnly: rec.ReadOnly}
	s.runTest(w, r, in.driverConfig(sec), sec)
}

// testDraftConnection tests a profile that has not been saved yet.
func (s *Server) testDraftConnection(w http.ResponseWriter, r *http.Request) {
	var in connInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.Name == "" {
		in.Name = "draft"
	}
	if msg := in.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	base := secretBlob{}
	if r.PathValue("id") != "" {
		_, stored, err := s.loadOwned(r.Context(), r)
		if err != nil {
			s.fail(w, err)
			return
		}
		base = stored
	}
	sec, msg := in.applySecrets(base)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	s.runTest(w, r, in.driverConfig(sec), sec)
}

func (s *Server) connectionDialer(sec secretBlob) mysql.Dialer {
	if sec.SSH.Enabled {
		return tunnel.Dialer(sec.SSH, s.Policy)
	}
	return s.Policy.DirectDialer(10 * time.Second)
}
func (s *Server) sshFingerprint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host string      `json:"host"`
		Port int         `json:"port"`
		User string      `json:"user"`
		Jump *tunnel.Hop `json:"jump,omitempty"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Port == 0 {
		in.Port = 22
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.PathValue("id") != "" {
		_, sec, err := s.loadOwned(ctx, r)
		if err != nil {
			s.fail(w, err)
			return
		}
		merged := tunnel.Merge(tunnel.Config{Jump: in.Jump}, sec.SSH)
		in.Jump = merged.Jump
	}
	observation, err := tunnel.ObserveVia(ctx, tunnel.Hop{Host: in.Host, Port: in.Port, User: in.User}, in.Jump, s.Policy)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, observation)
}

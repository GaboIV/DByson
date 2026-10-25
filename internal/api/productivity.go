package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gaboiv/dbyson/internal/drivers/mysql"
)

type savedQuery struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Database  string `json:"database"`
	Query     string `json:"query"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (s *Server) savedQueries(w http.ResponseWriter, r *http.Request) {
	rec, connectionSecret, err := s.loadConnection(r.Context(), r, GrantUse)
	if err != nil {
		s.failAccess(w, r, err)
		return
	}
	if err := s.mfaGate(r, rec.Env, connectionSecret.SSH.Enabled); err != nil {
		s.fail(w, err)
		return
	}
	owner := sessionFrom(r).UserID
	connection := r.PathValue("id")
	if r.Method == "GET" {
		rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id,name,dbname,wrapped_dek,secret,updated_at FROM saved_queries WHERE owner_id=? AND connection_id=? ORDER BY updated_at DESC LIMIT 200`, owner, connection)
		if err != nil {
			s.fail(w, err)
			return
		}
		defer rows.Close()
		list := []savedQuery{}
		for rows.Next() {
			var q savedQuery
			var wrapped, secret []byte
			if err = rows.Scan(&q.ID, &q.Name, &q.Database, &wrapped, &secret, &q.UpdatedAt); err != nil {
				s.fail(w, err)
				return
			}
			plain, e := s.Vault.Open("query/"+q.ID, wrapped, secret)
			if e != nil {
				s.fail(w, e)
				return
			}
			q.Query = string(plain)
			list = append(list, q)
		}
		if err = rows.Err(); err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, 200, list)
		return
	}
	var in savedQuery
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || len(in.Name) > 120 || len(in.Query) < 1 || len(in.Query) > 100000 || len(in.Database) > 256 {
		writeError(w, 400, "invalid saved query: name 1-120 bytes, SQL 1-100000 bytes")
		return
	}
	// Always create a new revision. Existing SQL is never overwritten silently.
	in.ID = newID()
	in.UpdatedAt = time.Now().Unix()
	wrapped, secret, err := s.Vault.Seal("query/"+in.ID, []byte(in.Query))
	if err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO saved_queries(id,owner_id,connection_id,name,dbname,wrapped_dek,secret,updated_at) SELECT ?,?,?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM saved_queries WHERE owner_id=? AND connection_id=?)<200`, in.ID, owner, connection, in.Name, in.Database, wrapped, secret, in.UpdatedAt, owner, connection)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, 409, "query library limit: remove an entry before saving")
		return
	}
	writeJSON(w, 201, in)
}

func (s *Server) deleteSavedQuery(w http.ResponseWriter, r *http.Request) {
	res, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM saved_queries WHERE id=? AND owner_id=? AND connection_id=?`, r.PathValue("queryID"), sessionFrom(r).UserID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) relations(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	db, _, err := s.openDB(ctx, r, r.PathValue("db"))
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()
	keys, err := mysql.ForeignKeys(ctx, db, r.PathValue("db"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, keys)
}

func (s *Server) importCSV(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Columns []string    `json:"columns"`
		Rows    [][]*string `json:"rows"`
		confirmationInput
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	db, rec, err := s.openDB(ctx, r, r.PathValue("db"))
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()
	if rec.ReadOnly {
		writeError(w, 403, "connection is read-only")
		return
	}
	payload, _ := json.Marshal(struct {
		Columns []string
		Rows    [][]*string
	}{in.Columns, in.Rows})
	digest := sha256.Sum256(payload)
	target := r.PathValue("db") + "." + r.PathValue("table")
	if !s.requireConfirmation(w, r, fmt.Sprintf("IMPORT CSV\n%x", digest), target, "Inserta el archivo completo en una transacción; un error revierte todas las filas.", fmt.Sprintf("%d filas · %s", len(in.Rows), strings.Join(in.Columns, ", ")), in.confirmationInput) {
		return
	}
	count, err := mysql.ImportCSV(ctx, db, r.PathValue("db"), r.PathValue("table"), in.Columns, in.Rows)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "inserted": count})
}

func (s *Server) killProcess(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Thread string `json:"thread"`
		confirmationInput
	}
	if !readJSON(w, r, &in) {
		return
	}
	// Only decimal integers reach the control command. Ownership/PROCESS privileges remain MySQL's decision.
	if len(in.Thread) == 0 || len(in.Thread) > 20 || strings.Trim(in.Thread, "0123456789") != "" {
		writeError(w, 400, "invalid process identifier")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	db, rec, err := s.openDB(ctx, r, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()
	if rec.ReadOnly {
		writeError(w, 403, "connection is read-only")
		return
	}
	sqlText := "KILL QUERY " + in.Thread
	if !s.requireConfirmation(w, r, "KILL QUERY", in.Thread, "Detiene la consulta activa de este proceso. Verifica usuario y SQL antes de confirmar.", sqlText, in.confirmationInput) {
		return
	}
	if _, err = db.ExecContext(ctx, sqlText); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

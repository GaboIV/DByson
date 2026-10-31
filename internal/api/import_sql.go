package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gaboiv/dbyson/internal/audit"
	"github.com/gaboiv/dbyson/internal/drivers/mysql"
)

// Importing a database works like Adminer's "Import": a .sql (or .sql.gz) file runs on the
// server, statement by statement, in the selected database. It always asks for confirmation,
// in two requests so the file travels only once:
//
//  1. POST …/import-sql?sha256=…&name=…&size=… with no body: the server answers with a
//     confirmation challenge bound to the database and that SHA-256.
//  2. The same URL with the file as the body and the challenge in the X-DByson-Confirmation-*
//     headers: the server checks the body hashes to the confirmed digest before running it.
//
// Progress streams back as NDJSON lines ({"type":"progress"} … {"type":"done"}).
const (
	maxImportUploadBytes = 64 << 20 // the file as uploaded (compressed or not)
	maxImportScriptBytes = 1 << 30  // the script once decompressed
	importTimeout        = 30 * time.Minute
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) importSQL(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	digest, name := q.Get("sha256"), truncateRunes(q.Get("name"), 200)
	size, _ := strconv.ParseInt(q.Get("size"), 10, 64)
	dbName := r.PathValue("db")
	stopOnError := q.Get("continueOnError") != "1"
	ar := auditFrom(r)
	ar.entry.Target = dbName
	ar.entry.Detail = fmt.Sprintf("file=%s size=%d", name, size)
	if !sha256Pattern.MatchString(digest) || size <= 0 || size > maxImportUploadBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("El archivo debe ocupar entre 1 byte y %d MiB.", maxImportUploadBytes>>20))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), importTimeout)
	defer cancel()
	rec, open, err := s.authorizeDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	confirm := confirmationInput{
		ConfirmationToken: r.Header.Get("X-DByson-Confirmation-Token"),
		ConfirmationName:  r.Header.Get("X-DByson-Confirmation-Name"),
	}
	action := "IMPORT SQL\n" + dbName + "\n" + digest
	reason := "Ejecuta el script completo en la base de datos, sentencia a sentencia. Puede crear, modificar o eliminar tablas y datos; " +
		"lo que ya se ejecutó no se revierte si una sentencia falla."
	preview := fmt.Sprintf("%s · %s · SHA-256 %s…", name, humanBytes(size), digest[:12])
	if confirm.ConfirmationToken == "" {
		s.requireConfirmation(w, r, action, dbName, reason, preview, confirm)
		return
	}

	// The upload may take longer than the server's default read timeout.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(10 * time.Minute))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportUploadBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != digest || int64(len(body)) != size {
		writeError(w, http.StatusBadRequest, "el archivo no coincide con el que se confirmó; vuelve a elegirlo")
		return
	}
	if !s.requireConfirmation(w, r, action, dbName, reason, preview, confirm) {
		return
	}

	var script io.Reader = bytes.NewReader(body)
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		gz, err := gzip.NewReader(script)
		if err != nil {
			writeError(w, http.StatusBadRequest, "el archivo .gz está dañado")
			return
		}
		defer gz.Close()
		script = gz
	}
	script = io.LimitReader(script, maxImportScriptBytes)

	// Signing out (or revoking this session) stops the import like any running query.
	sess := sessionFrom(r)
	requestID := newID()
	if err := s.queries.add(requestID, &activeQuery{userID: sess.UserID, sessionID: sess.ID, connectionID: r.PathValue("id"), cancel: cancel}); err != nil {
		writeError(w, 429, err.Error())
		return
	}
	defer s.queries.remove(requestID)

	db, err := open()
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer conn.Close()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	send := func(v any) {
		_ = enc.Encode(v)
		_ = rc.Flush()
	}
	last := time.Now()
	summary, runErr := mysql.RunScript(ctx, conn, dbName, script, stopOnError, func(p mysql.ImportSummary, read int64) {
		if time.Since(last) < 400*time.Millisecond {
			return
		}
		last = time.Now()
		send(map[string]any{"type": "progress", "statements": p.Statements, "failed": p.Failed, "bytesRead": read})
	})
	ar.entry.Detail = fmt.Sprintf("file=%s size=%d statements=%d failed=%d rows=%d", name, size, summary.Statements, summary.Failed, summary.AffectedRows)
	done := map[string]any{"type": "done", "summary": summary}
	switch {
	case runErr != nil:
		ar.entry.Outcome = audit.OutcomeError
		msg := runErr.Error()
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			msg = "La importación se detuvo antes de terminar."
		}
		done["error"] = msg
	case summary.Failed > 0:
		ar.entry.Outcome = audit.OutcomeError
	}
	send(done)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adminbase/adminbase/internal/audit"
	"github.com/adminbase/adminbase/internal/drivers/mysql"
	"github.com/adminbase/adminbase/internal/policy"
)

func (s *Server) openDB(ctx context.Context, r *http.Request, targetDB string) (*sql.DB, *connRecord, error) {
	rec, sec, err := s.loadOwned(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	if err := s.mfaGate(r, rec.Env, sec.SSH.Enabled); err != nil {
		return nil, nil, err
	}
	rec.ReadOnly = rec.ReadOnly || r.Header.Get("X-DByson-Read-Only") == "1"
	dbName := rec.Database
	if targetDB != "" {
		dbName = targetDB
	}
	in := connInput{
		Host:     rec.Host,
		Port:     rec.Port,
		Username: rec.Username,
		Database: dbName,
		TLSMode:  rec.TLSMode,
	}
	cfg := in.driverConfig(sec)
	cfg.ReadOnly = rec.ReadOnly
	db, err := mysql.Open(cfg, s.connectionDialer(sec))
	if err != nil {
		return nil, nil, err
	}
	return db, rec, nil
}

type listDatabasesResponse struct {
	Databases       []string             `json:"databases"`
	DatabaseDetails []mysql.DatabaseInfo `json:"databaseDetails"`
	ExecutedSQL     string               `json:"executedSql"`
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	db, _, err := s.openDB(ctx, r, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	details, sqlText, err := mysql.ListDatabasesWithDetails(ctx, db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var names []string
	for _, d := range details {
		names = append(names, d.Name)
	}
	writeJSON(w, http.StatusOK, listDatabasesResponse{
		Databases:       names,
		DatabaseDetails: details,
		ExecutedSQL:     sqlText,
	})
}

type dbActionInput struct {
	Name string `json:"name"`
	confirmationInput
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var in dbActionInput
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "database name is required")
		return
	}

	db, rec, err := s.openDB(ctx, r, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	sqlText, err := mysql.CreateDatabase(ctx, db, in.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

func (s *Server) handleDropDatabase(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var in dbActionInput
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "database name is required")
		return
	}

	db, rec, err := s.openDB(ctx, r, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	if !s.requireConfirmation(w, r, "DROP DATABASE", in.Name, "Elimina la base de datos y todas sus tablas.", "DROP DATABASE "+mysql.EscapeIdent(in.Name), in.confirmationInput) {
		return
	}
	sqlText, err := mysql.DropDatabase(ctx, db, in.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

type listTablesResponse struct {
	Tables      []mysql.TableInfo `json:"tables"`
	ExecutedSQL string            `json:"executedSql"`
}

func (s *Server) handleListTables(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	if dbName == "" {
		writeError(w, http.StatusBadRequest, "database is required")
		return
	}

	db, _, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	tables, sqlText, err := mysql.ListTables(ctx, db, dbName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listTablesResponse{Tables: tables, ExecutedSQL: sqlText})
}

func (s *Server) handleTableSchema(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")
	if dbName == "" || tableName == "" {
		writeError(w, http.StatusBadRequest, "database and table are required")
		return
	}

	db, _, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	schema, err := mysql.GetTableSchema(ctx, db, dbName, tableName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (s *Server) handleTableData(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")
	if dbName == "" || tableName == "" {
		writeError(w, http.StatusBadRequest, "database and table are required")
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	sortCol := r.URL.Query().Get("sortCol")
	sortOrder := r.URL.Query().Get("sortOrder")
	filterCol := r.URL.Query().Get("filterCol")
	filterOp := r.URL.Query().Get("filterOp")
	filterVal := r.URL.Query().Get("filterVal")
	customWhere := r.URL.Query().Get("customWhere")
	columns := r.URL.Query().Get("columns")

	opts := mysql.TableDataOptions{
		Page:        page,
		PageSize:    pageSize,
		SortColumn:  sortCol,
		SortOrder:   sortOrder,
		FilterCol:   filterCol,
		FilterOp:    filterOp,
		FilterValue: filterVal,
		CustomWhere: customWhere,
		Columns:     columns,
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		opts.Offset = &offset
	}

	db, _, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	data, err := mysql.GetTableData(ctx, db, dbName, tableName, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleTableCount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	database, table := r.PathValue("db"), r.PathValue("table")
	db, _, err := s.openDB(ctx, r, database)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()
	opts := mysql.TableDataOptions{FilterCol: r.URL.Query().Get("filterCol"), FilterOp: r.URL.Query().Get("filterOp"), FilterValue: r.URL.Query().Get("filterVal"), CustomWhere: r.URL.Query().Get("customWhere")}
	start := time.Now()
	total, query, err := mysql.CountTableRows(ctx, db, database, table, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"totalRows": total, "executedSql": query, "executionMs": time.Since(start).Milliseconds()})
}

func (s *Server) handleRowDetail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var in struct {
		PK map[string]any `json:"pk"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.PK) == 0 {
		writeError(w, http.StatusBadRequest, "primary key required")
		return
	}
	database, table := r.PathValue("db"), r.PathValue("table")
	db, _, err := s.openDB(ctx, r, database)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()
	result, err := mysql.GetRow(ctx, db, database, table, in.PK)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type rowMutationInput struct {
	PK     map[string]any `json:"pk"`
	Values map[string]any `json:"values"`
	confirmationInput
}

type mutationResponse struct {
	OK          bool   `json:"ok"`
	ExecutedSQL string `json:"executedSql,omitempty"`
}

func (s *Server) handleInsertRow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")

	var in rowMutationInput
	if !readJSON(w, r, &in) {
		return
	}

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	sqlText, err := mysql.InsertRow(ctx, db, dbName, tableName, in.Values)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

func (s *Server) handleUpdateRow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")

	var in rowMutationInput
	if !readJSON(w, r, &in) {
		return
	}

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	sqlText, err := mysql.UpdateRow(ctx, db, dbName, tableName, in.PK, in.Values)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

func (s *Server) handleDeleteRow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")

	var in rowMutationInput
	if !readJSON(w, r, &in) {
		return
	}

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	keys, _ := json.Marshal(in.PK)
	if !s.requireConfirmation(w, r, "DELETE ROW\n"+string(keys), dbName+"."+tableName, "Elimina el registro identificado por su clave primaria.", string(keys), in.confirmationInput) {
		return
	}
	sqlText, err := mysql.DeleteRow(ctx, db, dbName, tableName, in.PK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

type batchDeleteInput struct {
	PKs []map[string]any `json:"pks"`
	confirmationInput
}

func (s *Server) handleBatchDeleteRows(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")

	var in batchDeleteInput
	if !readJSON(w, r, &in) {
		return
	}

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	keys, _ := json.Marshal(in.PKs)
	if !s.requireConfirmation(w, r, "DELETE ROWS\n"+string(keys), dbName+"."+tableName, fmt.Sprintf("Elimina %d registros seleccionados.", len(in.PKs)), string(keys), in.confirmationInput) {
		return
	}
	affected, sqlText, err := mysql.BatchDeleteRows(ctx, db, dbName, tableName, in.PKs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"affectedRows": affected,
		"executedSql":  sqlText,
	})
}

func (s *Server) handleTruncateTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")
	var confirmation confirmationInput
	if !readJSON(w, r, &confirmation) {
		return
	}

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	if !s.requireConfirmation(w, r, "TRUNCATE", dbName+"."+tableName, "Vacía todos los registros de la tabla.", "TRUNCATE TABLE "+mysql.EscapeIdent(dbName)+"."+mysql.EscapeIdent(tableName), confirmation) {
		return
	}
	sqlText, err := mysql.TruncateTable(ctx, db, dbName, tableName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

func (s *Server) handleDropTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")
	var confirmation confirmationInput
	if !readJSON(w, r, &confirmation) {
		return
	}

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	if !s.requireConfirmation(w, r, "DROP TABLE", dbName+"."+tableName, "Elimina la tabla y todos sus registros.", "DROP TABLE "+mysql.EscapeIdent(dbName)+"."+mysql.EscapeIdent(tableName), confirmation) {
		return
	}
	sqlText, err := mysql.DropTable(ctx, db, dbName, tableName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

func (s *Server) handleOptimizeTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	dbName := r.PathValue("db")
	tableName := r.PathValue("table")

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	sqlText, err := mysql.OptimizeTable(ctx, db, dbName, tableName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

type createTableInput struct {
	Name    string               `json:"name"`
	Columns []mysql.NewColumnDef `json:"columns"`
}

func (s *Server) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbName := r.PathValue("db")

	var in createTableInput
	if !readJSON(w, r, &in) {
		return
	}

	db, rec, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly {
		writeError(w, http.StatusForbidden, "connection is read-only")
		return
	}

	sqlText, err := mysql.CreateTable(ctx, db, dbName, in.Name, in.Columns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, mutationResponse{OK: true, ExecutedSQL: sqlText})
}

type queryRequest struct {
	Query              string `json:"query"`
	Database           string `json:"database"`
	ConfirmDestructive bool   `json:"confirmDestructive"`
	confirmationInput
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var req queryRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	ar := auditFrom(r)
	ar.entry.Target, ar.entry.Detail = req.Database, req.Query
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query cannot be empty")
		return
	}
	if err := policy.ValidateSQL(req.Query); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	db, rec, err := s.openDB(ctx, r, req.Database)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	if rec.ReadOnly && policy.IsReadOnlyViolation(req.Query) {
		writeError(w, http.StatusForbidden, "connection is configured as read-only")
		return
	}

	classification := policy.ClassifySQL(req.Query)
	if classification.IsDestructive {
		target := req.Database
		if target == "" {
			target = rec.Database
		}
		if target == "" {
			target = rec.Name
		}
		if !s.requireConfirmation(w, r, classification.Operation+"\n"+req.Database+"\n"+req.Query, target, classification.Reason, req.Query, req.confirmationInput) {
			return
		}
	}

	result, err := mysql.ExecuteSQL(ctx, db, req.Database, req.Query, 1000)
	if err != nil {
		ar.entry.Outcome = audit.OutcomeError
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          false,
			"error":       err.Error(),
			"executedSql": req.Query,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"result":      result,
		"executedSql": req.Query,
	})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	dbName := r.URL.Query().Get("db")
	tableName := r.URL.Query().Get("table")
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "sql"
	}
	if format != "sql" && format != "csv" && format != "json" {
		writeError(w, http.StatusBadRequest, "unsupported export format")
		return
	}
	if format != "sql" && tableName == "" {
		writeError(w, http.StatusBadRequest, "select a table for CSV or JSON export")
		return
	}
	includeStructure := r.URL.Query().Get("structure") != "0"
	includeData := r.URL.Query().Get("data") != "0"
	ar := auditFrom(r)
	ar.force = true // exports are reads, but they move data out: always audited
	ar.entry.Target = strings.Trim(dbName+"."+tableName, ".")
	ar.entry.Detail = fmt.Sprintf("format=%s structure=%t data=%t", format, includeStructure, includeData)
	if format == "sql" && !includeStructure && !includeData {
		writeError(w, http.StatusBadRequest, "select structure or data for SQL export")
		return
	}

	if dbName == "" {
		writeError(w, http.StatusBadRequest, "db query parameter is required")
		return
	}

	db, _, err := s.openDB(ctx, r, dbName)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer db.Close()

	var tables []string
	if tableName != "" {
		tables = []string{tableName}
	} else {
		tblList, _, err := mysql.ListTables(ctx, db, dbName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, t := range tblList {
			if t.Type == "BASE TABLE" {
				tables = append(tables, t.Name)
			}
		}
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s.%s", dbName, timestamp, format)
	if tableName != "" {
		filename = fmt.Sprintf("%s_%s_%s.%s", dbName, tableName, timestamp, format)
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Trailer", "X-DByson-Export-Status")
	output := &exportResponseWriter{ResponseWriter: w}
	var exportErr error
	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		exportErr = mysql.ExportCSV(ctx, db, dbName, tableName, output)
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		exportErr = mysql.ExportJSON(ctx, db, dbName, tableName, output)
	default: // "sql"
		w.Header().Set("Content-Type", "application/sql; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		exportErr = mysql.ExportSQL(ctx, db, dbName, tables, includeData, includeStructure, output)
	}
	if exportErr != nil {
		if !output.started {
			w.Header().Del("Content-Disposition")
			w.Header().Del("Trailer")
			writeError(w, http.StatusInternalServerError, "export failed: "+exportErr.Error())
			return
		}
		// Never make a partial dump look like a successful download. Abort the
		// HTTP transfer; no appended JSON or misleading SQL completion footer.
		w.Header().Set("X-DByson-Export-Status", "error")
		panic(http.ErrAbortHandler)
	}
	w.Header().Set("X-DByson-Export-Status", "complete")
}

type exportResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *exportResponseWriter) Write(data []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(data)
}

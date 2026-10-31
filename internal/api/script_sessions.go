package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gaboiv/dbyson/internal/drivers/mysql"
)

// A console script of several statements runs on one pinned physical connection, so
// USE, session variables and START TRANSACTION … COMMIT behave as in a mysql client.
// Each statement is still its own request (own confirmation, audit entry and cancel
// button). Closing the script, or leaving it idle, closes the connection: MySQL rolls
// back a transaction left open.
const (
	scriptIdleTimeout = 2 * time.Minute
	maxScriptsPerUser = 4
	maxScripts        = 64
)

var (
	errScriptNotFound = errors.New("el script ya no está activo; vuelve a ejecutarlo")
	errScriptBusy     = errors.New("el script ya está ejecutando otra sentencia")
	errTooManyScripts = errors.New("too many open scripts")
)

type scriptSession struct {
	mu                      sync.Mutex // held while a statement runs: one at a time
	userID                  int64
	sessionID, connectionID string
	readOnly                bool
	db                      *sql.DB
	conn                    *sql.Conn
	thread                  uint64
	lastUsed                time.Time
	closed                  bool
}

func (sc *scriptSession) close() {
	if sc.closed {
		return
	}
	sc.closed = true
	_ = sc.conn.Close()
	_ = sc.db.Close()
}

type scriptRegistry struct {
	mu    sync.Mutex
	items map[string]*scriptSession
}

// acquire returns the caller's script id locked for one statement, opening it with
// open when it does not exist yet (created reports that). release must follow.
func (reg *scriptRegistry) acquire(ctx context.Context, id string, owner *scriptSession, open func() (*sql.DB, error)) (sc *scriptSession, created bool, err error) {
	reg.mu.Lock()
	if reg.items == nil {
		reg.items = make(map[string]*scriptSession)
	}
	if existing := reg.items[id]; existing != nil {
		reg.mu.Unlock()
		if existing.userID != owner.userID || existing.sessionID != owner.sessionID || existing.connectionID != owner.connectionID {
			return nil, false, errScriptNotFound
		}
		if !existing.mu.TryLock() {
			return nil, false, errScriptBusy
		}
		if existing.closed {
			existing.mu.Unlock()
			return nil, false, errScriptNotFound
		}
		return existing, false, nil
	}
	n := 0
	for _, item := range reg.items {
		if item.userID == owner.userID {
			n++
		}
	}
	if len(reg.items) >= maxScripts || n >= maxScriptsPerUser {
		reg.mu.Unlock()
		return nil, false, errTooManyScripts
	}
	// Reserve the id while connecting, so two requests cannot both open it.
	owner.mu.Lock()
	reg.items[id] = owner
	reg.mu.Unlock()

	fail := func(err error) (*scriptSession, bool, error) {
		reg.mu.Lock()
		delete(reg.items, id)
		reg.mu.Unlock()
		owner.closed = true
		owner.mu.Unlock()
		return nil, false, err
	}
	db, err := open()
	if err != nil {
		return fail(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return fail(err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&owner.thread); err != nil {
		conn.Close()
		db.Close()
		return fail(err)
	}
	owner.db, owner.conn = db, conn
	reg.watch(id, owner)
	return owner, true, nil
}

func (reg *scriptRegistry) release(sc *scriptSession) {
	sc.lastUsed = time.Now()
	sc.mu.Unlock()
}

// watch closes the script once it has been idle for scriptIdleTimeout.
func (reg *scriptRegistry) watch(id string, sc *scriptSession) {
	var check func()
	check = func() {
		if !sc.mu.TryLock() {
			time.AfterFunc(scriptIdleTimeout, check) // a statement is running
			return
		}
		defer sc.mu.Unlock()
		if sc.closed {
			return
		}
		if idle := time.Since(sc.lastUsed); idle < scriptIdleTimeout {
			time.AfterFunc(scriptIdleTimeout-idle, check)
			return
		}
		reg.remove(id, sc)
	}
	time.AfterFunc(scriptIdleTimeout, check)
}

// remove forgets and closes sc; the caller holds sc.mu.
func (reg *scriptRegistry) remove(id string, sc *scriptSession) {
	reg.mu.Lock()
	if reg.items[id] == sc {
		delete(reg.items, id)
	}
	reg.mu.Unlock()
	sc.close()
}

// closeOwned closes the script if it belongs to the caller, waiting for a running
// statement to finish (it was already asked to cancel, if the user wanted that).
func (reg *scriptRegistry) closeOwned(id string, userID int64, sessionID, connectionID string) bool {
	reg.mu.Lock()
	sc := reg.items[id]
	reg.mu.Unlock()
	if sc == nil || sc.userID != userID || sc.sessionID != sessionID || sc.connectionID != connectionID {
		return false
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	reg.remove(id, sc)
	return true
}

// closeSessions closes the scripts of userID's sessions that match, after their
// running statements were cancelled (see queryRegistry.revokeSessions).
func (reg *scriptRegistry) closeSessions(userID int64, match func(string) bool) {
	reg.mu.Lock()
	pending := map[string]*scriptSession{}
	for id, sc := range reg.items {
		if sc.userID == userID && match(sc.sessionID) {
			pending[id] = sc
		}
	}
	reg.mu.Unlock()
	for id, sc := range pending {
		sc.mu.Lock()
		reg.remove(id, sc)
		sc.mu.Unlock()
	}
}

func (s *Server) closeScript(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	id := r.PathValue("scriptID")
	if !queryIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid script identifier")
		return
	}
	// Every statement of the script was audited on its own; closing it is bookkeeping.
	auditFrom(r).skip = true
	if !s.queries.scripts.closeOwned(id, sess.UserID, sess.ID, r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "script not active in this session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

// newScriptExecution runs one statement of sc; the connection stays pinned afterwards.
func newScriptExecution(ctx context.Context, sc *scriptSession) *mysql.Execution {
	return mysql.NewPinnedExecution(ctx, sc.db, sc.conn, sc.thread)
}

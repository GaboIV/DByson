package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// Execution reserves a physical connection until execution AND any KILL finish.
// The caller never supplies a MySQL thread ID. The control command uses a second
// connection with the same credentials, so it cannot kill other MySQL users.
type Execution struct {
	mu                  sync.Mutex
	db                  *sql.DB
	conn                *sql.Conn
	thread              uint64
	ctx                 context.Context
	cancel              context.CancelFunc
	done                chan struct{}
	finished, cancelled bool
}

func NewExecution(parent context.Context, db *sql.DB) (*Execution, error) {
	conn, err := db.Conn(parent)
	if err != nil {
		return nil, err
	}
	var thread uint64
	if err := conn.QueryRowContext(parent, "SELECT CONNECTION_ID()").Scan(&thread); err != nil {
		conn.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	e := &Execution{db: db, conn: conn, thread: thread, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		select {
		case <-parent.Done():
			killCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = e.Cancel(killCtx)
		case <-e.done:
		}
	}()
	return e, nil
}

func (e *Execution) Run(database, query string, maxRows int) (*QueryResult, error) {
	defer e.Close()
	e.mu.Lock()
	cancelled := e.cancelled || e.finished
	e.mu.Unlock()
	if cancelled {
		return nil, context.Canceled
	}
	result, err := executeSQLConn(e.ctx, e.conn, database, query, maxRows)
	e.mu.Lock()
	cancelled = e.cancelled
	e.mu.Unlock()
	if cancelled {
		return nil, context.Canceled
	}
	return result, err
}

func (e *Execution) Cancel(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished || e.cancelled {
		return nil
	}
	e.cancelled = true
	// Even if KILL fails (network/permissions), close the executing driver socket.
	// Its connection stays reserved until this attempt has finished.
	defer e.cancel()
	_, err := e.db.ExecContext(ctx, fmt.Sprintf("KILL QUERY %d", e.thread))
	return err
}

func (e *Execution) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return
	}
	e.finished = true
	e.cancel()
	close(e.done)
	e.conn.Close()
}

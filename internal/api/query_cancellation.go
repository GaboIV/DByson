package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/gaboiv/dbyson/internal/drivers/mysql"
)

var queryIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

type activeQuery struct {
	mu                      sync.Mutex
	userID                  int64
	sessionID, connectionID string
	cancel                  context.CancelFunc
	execution               *mysql.Execution
	cancelled               bool
}

func (q *activeQuery) stop(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cancelled = true
	defer q.cancel()
	if q.execution != nil {
		return q.execution.Cancel(ctx)
	}
	return nil
}

type queryRegistry struct {
	mu    sync.Mutex
	items map[string]*activeQuery
}

func (reg *queryRegistry) add(id string, q *activeQuery) error {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.items == nil {
		reg.items = make(map[string]*activeQuery)
	}
	if _, exists := reg.items[id]; exists {
		return errors.New("query identifier is already active")
	}
	n := 0
	for _, existing := range reg.items {
		if existing.userID == q.userID {
			n++
		}
	}
	if len(reg.items) >= 64 || n >= 8 {
		return errors.New("too many running queries")
	}
	reg.items[id] = q
	return nil
}

func (reg *queryRegistry) remove(id string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	delete(reg.items, id)
}

func (reg *queryRegistry) revokeSessions(userID int64, match func(string) bool) {
	reg.mu.Lock()
	var pending []*activeQuery
	for _, q := range reg.items {
		if q.userID == userID && match(q.sessionID) {
			pending = append(pending, q)
		}
	}
	reg.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, q := range pending {
		wg.Go(func() { _ = q.stop(ctx) })
	}
	wg.Wait()
}

func (s *Server) cancelQuery(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	s.queries.mu.Lock()
	q := s.queries.items[r.PathValue("queryID")]
	s.queries.mu.Unlock()
	if q == nil || q.userID != sess.UserID || q.sessionID != sess.ID || q.connectionID != r.PathValue("id") {
		writeError(w, 404, "query not active in this session")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	auditFrom(r).entry.Target = r.PathValue("queryID")
	if err := q.stop(ctx); err != nil {
		writeError(w, 502, "could not confirm KILL QUERY; execution socket closed")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "cancellation_requested"})
}

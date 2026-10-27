package audit

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gaboiv/dbyson/internal/store"
)

// recordAt appends n entries with the logger clock fixed at ts.
func recordAt(t *testing.T, l *Logger, ts time.Time, n int, action string) {
	t.Helper()
	l.Now = func() time.Time { return ts }
	for i := 0; i < n; i++ {
		if err := l.Record(context.Background(), Entry{Username: "ana", Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	l.Now = time.Now
}

func hashOf(t *testing.T, l *Logger, id int64) string {
	t.Helper()
	var h string
	if err := l.db.QueryRow(`SELECT hash FROM audit_log WHERE id = ?`, id).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

// A retention purge removes only the oldest part of the trail: what remains must
// still verify, and the anchor must be the hash of the last entry removed.
func TestPartialPurgeKeepsChainVerifiable(t *testing.T) {
	l, _ := newLogger(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	recordAt(t, l, old, 3, "old")        // ids 1..3
	recordAt(t, l, time.Now(), 2, "new") // ids 4..5
	lastOld := hashOf(t, l, 3)

	res, err := l.PurgeOlderThan(context.Background(), time.Now().Add(-30*24*time.Hour).Unix())
	if err != nil || res.Deleted != 3 {
		t.Fatalf("purge = %+v, %v", res, err)
	}
	if res.Anchor != lastOld {
		t.Fatalf("anchor must be the hash of the last purged entry: got %s want %s", res.Anchor, lastOld)
	}
	v, err := l.Verify(context.Background())
	if err != nil || !v.OK || v.Checked != 3 { // 2 kept + the audit.purged entry
		t.Fatalf("chain must verify after a partial purge: %+v %v", v, err)
	}
	if !v.Anchored || v.AnchoredAt != lastOld {
		t.Fatalf("verification must report the purge anchor: %+v", v)
	}
}

// Deleting the oldest entries without going through a purge must still be detected:
// the head of the chain is only accepted when a recorded purge vouches for it.
func TestUnrecordedHeadDeletionIsDetected(t *testing.T) {
	l, dir := newLogger(t)
	for i := 0; i < 4; i++ {
		l.Record(context.Background(), Entry{Username: "ana", Action: "x"})
	}
	raw, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, store.FileName)))
	defer raw.Close()
	raw.Exec(`DROP TRIGGER audit_log_no_delete`)
	raw.Exec(`DELETE FROM audit_log WHERE id <= 2`)
	if v, _ := l.Verify(context.Background()); v.OK {
		t.Fatalf("silent head deletion must break verification: %+v", v)
	}
}

// The purge removes a prefix by id: an entry with an older timestamp recorded after
// newer ones (clock moved back) must not punch a hole in the middle of the chain.
func TestPurgeRemovesOnlyAPrefix(t *testing.T) {
	l, _ := newLogger(t)
	now := time.Now()
	recordAt(t, l, now.Add(-60*24*time.Hour), 1, "old")
	recordAt(t, l, now, 1, "new")
	recordAt(t, l, now.Add(-60*24*time.Hour), 1, "clock-skew") // old timestamp, recorded last
	recordAt(t, l, now, 1, "new")

	res, err := l.PurgeOlderThan(context.Background(), now.Add(-30*24*time.Hour).Unix())
	if err != nil || res.Deleted != 1 {
		t.Fatalf("only the leading old entry may go: %+v %v", res, err)
	}
	if v, _ := l.Verify(context.Background()); !v.OK {
		t.Fatalf("chain broken: %+v", v)
	}
}

// Rotating the key after a purge keeps the anchor and the chain verifiable.
func TestRekeyAfterPurge(t *testing.T) {
	l, _ := newLogger(t)
	recordAt(t, l, time.Now().Add(-90*24*time.Hour), 2, "old")
	recordAt(t, l, time.Now(), 2, "new")
	if _, err := l.PurgeOlderThan(context.Background(), time.Now().Add(-30*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	newKey := []byte(strings.Repeat("k", 32))
	tx, err := l.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := RekeyTx(context.Background(), tx, l.key, newKey); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if v, err := New(l.db, newKey).Verify(context.Background()); err != nil || !v.OK || !v.Anchored {
		t.Fatalf("rekeyed chain must verify and stay anchored: %+v %v", v, err)
	}
}

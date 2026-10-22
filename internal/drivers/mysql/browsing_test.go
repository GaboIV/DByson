package mysql

import (
	"context"
	"strings"
	"testing"
)

func TestBrowsingDoesNotCountAndUsesLookahead(t *testing.T) {
	db, f := fixtureDB(t, 3, "value")
	result, err := GetTableData(context.Background(), db, "db", "t", TableDataOptions{PageSize: 2, SortColumn: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.queries) != 1 || strings.Contains(f.queries[0], "COUNT(") || !strings.Contains(f.queries[0], "LIMIT 3 OFFSET 0") {
		t.Fatalf("unexpected browsing queries: %v", f.queries)
	}
	if len(result.Rows) != 2 || !result.HasMore || result.NextOffset != 2 || result.TotalRowsKnown || result.Truncated {
		t.Fatalf("invalid page: %+v", result)
	}
	db, _ = fixtureDB(t, 2, "value")
	offset := int64(2)
	result, err = GetTableData(context.Background(), db, "db", "t", TableDataOptions{PageSize: 2, Offset: &offset})
	if err != nil || result.HasMore || result.NextOffset != 4 {
		t.Fatalf("last full page: %+v %v", result, err)
	}
}

func TestByteLimitedBrowsingDoesNotSkipUnreadRows(t *testing.T) {
	db, _ := fixtureDB(t, 10, strings.Repeat("x", 3<<20))
	result, err := GetTableData(context.Background(), db, "db", "t", TableDataOptions{PageSize: 50})
	if err != nil || len(result.Rows) != 2 || !result.HasMore || result.NextOffset != 2 || !result.Truncated {
		t.Fatalf("bounded page: rows=%d err=%v", len(result.Rows), err)
	}
	db, _ = fixtureDB(t, 1, strings.Repeat("x", 9<<20))
	result, err = GetTableData(context.Background(), db, "db", "t", TableDataOptions{PageSize: 50})
	if err != nil || !result.Truncated || result.HasMore || result.NextOffset != 0 {
		t.Fatalf("oversized row must not cause a pagination loop: %v", err)
	}
}

func TestCancelledBrowsingAndInvalidOffset(t *testing.T) {
	db, _ := fixtureDB(t, 3, "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GetTableData(ctx, db, "db", "t", TableDataOptions{}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	negative := int64(-1)
	if _, err := GetTableData(context.Background(), db, "db", "t", TableDataOptions{Offset: &negative}); err == nil {
		t.Fatal("negative offset accepted")
	}
}

package mysql

import (
	"context"
	"testing"
)

func TestCreateTableRejectsUnstructuredTypeAndLength(t *testing.T) {
	for _, column := range []NewColumnDef{
		{Name: "id", Type: "INT); DROP TABLE t; --"},
		{Name: "id", Type: "INT", Length: "11) ; DROP TABLE t"},
		{Name: "", Type: "INT"},
		{Name: "id\x00bad", Type: "INT"},
	} {
		// No DB is needed: reject before any query can be issued.
		if _, err := CreateTable(context.Background(), nil, "db", "t", []NewColumnDef{column}); err == nil {
			t.Fatalf("accepted unsafe definition: %+v", column)
		}
	}
}

func TestEscapeIdent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"users", "`users`"},
		{"user`table", "`user``table`"},
		{"test\x00name", "`testname`"},
		{"my table name", "`my table name`"},
	}

	for _, tt := range tests {
		got := EscapeIdent(tt.input)
		if got != tt.want {
			t.Errorf("EscapeIdent(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatSQLValue(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{"hello", "'hello'"},
		{"O'Reilly", "'O''Reilly'"},
		{123, "123"},
		{int64(456), "456"},
		{3.14, "3.14"},
		{true, "1"},
		{false, "0"},
	}

	for _, tt := range tests {
		got := formatSQLValue(tt.input)
		if got != tt.want {
			t.Errorf("formatSQLValue(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsRowReturning(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT * FROM users", true},
		{"SHOW TABLES", true},
		{"DESCRIBE users", true},
		{"DESC users", true},
		{"EXPLAIN SELECT 1", true},
		{"INSERT INTO users VALUES (1)", false},
		{"UPDATE users SET a=1", false},
		{"DELETE FROM users", false},
		{"ALTER TABLE users ADD c INT", false},
	}

	for _, tt := range tests {
		got := isRowReturning(tt.sql)
		if got != tt.want {
			t.Errorf("isRowReturning(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}

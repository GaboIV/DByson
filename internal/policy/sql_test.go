package policy

import (
	"testing"
)

func TestClassifySQL(t *testing.T) {
	tests := []struct {
		name          string
		sql           string
		isDestructive bool
		op            string
	}{
		{"select simple", "SELECT * FROM users", false, "SELECT"},
		{"select with comment", "/* comment */ SELECT id FROM products WHERE active = 1", false, "SELECT"},
		{"delete with where", "DELETE FROM users WHERE id = 10", true, "DELETE"},
		{"delete without where", "DELETE FROM users", true, "DELETE"},
		{"delete multiline without where", "/* audit */\nDELETE FROM accounts", true, "DELETE"},
		{"update with where", "UPDATE users SET active = 1 WHERE id = 5", true, "UPDATE"},
		{"update without where", "UPDATE users SET active = 0", true, "UPDATE"},
		{"drop table", "DROP TABLE temp_logs", true, "DROP"},
		{"drop database", "DROP DATABASE old_db", true, "DROP"},
		{"truncate table", "TRUNCATE TABLE sessions", true, "TRUNCATE"},
		{"truncate without table keyword", "TRUNCATE sessions", true, "TRUNCATE"},
		{"alter table", "ALTER TABLE users ADD COLUMN phone VARCHAR(20)", true, "ALTER"},
		{"insert", "INSERT INTO users (name) VALUES ('Ana')", true, "INSERT"},
		{"use", "USE shop", false, "USE"},
		{"use quoted", "USE `shop-2`;", false, "USE"},
		{"start transaction", "START TRANSACTION", false, "START"},
		{"start transaction read only", "START TRANSACTION READ ONLY;", false, "START"},
		{"begin", "BEGIN", false, "BEGIN"},
		{"begin work", "BEGIN WORK;", false, "BEGIN"},
		{"commit", "COMMIT", false, "COMMIT"},
		{"rollback", "ROLLBACK;", false, "ROLLBACK"},
		{"rollback to savepoint", "ROLLBACK TO SAVEPOINT before_import", false, "ROLLBACK"},
		{"savepoint", "SAVEPOINT before_import", false, "SAVEPOINT"},
		{"release savepoint", "RELEASE SAVEPOINT before_import", false, "RELEASE"},
		{"set names", "SET NAMES utf8mb4", false, "SET"},
		{"set user variable", "SET @total = (SELECT COUNT(*) FROM users)", false, "SET"},
		{"set session variable", "SET SESSION sql_mode = 'STRICT_ALL_TABLES'", false, "SET"},
		{"set autocommit", "SET autocommit = 0", false, "SET"},
		{"set global", "SET GLOBAL max_connections = 10", true, "SET"},
		{"set global sysvar", "SET @@GLOBAL.max_connections = 10", true, "SET"},
		{"set persist", "SET PERSIST max_connections = 10", true, "SET"},
		{"set password", "SET PASSWORD FOR 'app'@'%' = 'x'", true, "SET"},
		{"set default role", "SET DEFAULT ROLE ALL TO 'app'@'%'", true, "SET"},
		{"set with sleep", "SET @x = SLEEP(10)", true, "SET"},
		{"start replica", "START REPLICA", true, "START"},
		{"release lock function", "DO RELEASE_LOCK('x')", true, "DO"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ClassifySQL(tt.sql)
			if res.IsDestructive != tt.isDestructive {
				t.Errorf("ClassifySQL(%q).IsDestructive = %v, want %v", tt.sql, res.IsDestructive, tt.isDestructive)
			}
			if res.Operation != tt.op {
				t.Errorf("ClassifySQL(%q).Operation = %q, want %q", tt.sql, res.Operation, tt.op)
			}
		})
	}
}

func TestIsReadOnlyViolation(t *testing.T) {
	tests := []struct {
		sql       string
		violation bool
	}{
		{"SELECT * FROM users", false},
		{"SHOW TABLES", false},
		{"EXPLAIN SELECT 1", false},
		{"INSERT INTO users VALUES (1)", true},
		{"UPDATE users SET a=1", true},
		{"DELETE FROM users", true},
		{"DROP TABLE x", true},
		{"ALTER TABLE x ADD col INT", true},
		{"CREATE TABLE x (id INT)", true},
	}

	for _, tt := range tests {
		got := IsReadOnlyViolation(tt.sql)
		if got != tt.violation {
			t.Errorf("IsReadOnlyViolation(%q) = %v, want %v", tt.sql, got, tt.violation)
		}
	}
}

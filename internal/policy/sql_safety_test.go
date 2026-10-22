package policy

import (
	"strings"
	"testing"
)

func TestSQLSafetyBoundaries(t *testing.T) {
	for _, query := range []string{
		"CALL mutate()", "SET SESSION TRANSACTION READ WRITE", "FLUSH TABLES", "DO dangerous()",
		"SELECT 1 INTO OUTFILE '/tmp/export'", "SELECT @x := 1", "SELECT * FROM t FOR UPDATE",
		"SELECT GET_LOCK('lock', 10)", "EXPLAIN ANALYZE SELECT 1", "EXPLAIN UPDATE t SET n=1",
		"WITH c AS (SELECT 1) UPDATE t SET n=1", "/*!50000 DELETE FROM t */", "/*M! DELETE FROM t */",
		"SELECT 1; DELETE FROM t", "SELECT 'unterminated", "SELECT 'a\\'; DELETE FROM t; -- '",
		"SELECT 1 # comment\r INTO OUTFILE '/tmp/dump'", "SELECT 1 -- comment\r INTO OUTFILE '/tmp/dump'",
		"SELECT `LOAD_FILE`('/tmp/file')", "SELECT `GET_LOCK`('x',1)",
	} {
		t.Run(query, func(t *testing.T) {
			if !IsReadOnlyViolation(query) {
				t.Fatal("read-only accepted unsafe/unsupported SQL")
			}
		})
	}
	for _, query := range []string{
		"SELECT 'WHERE; /* not a comment */'", "SELECT 'O''Reilly'", "SELECT `WHERE` FROM `t`",
		"/* explain why */ SELECT 1; -- end\n", "WITH c AS (SELECT 1 AS n) SELECT n FROM c",
		"SHOW CREATE TABLE t", "DESCRIBE t", "EXPLAIN SELECT * FROM t",
	} {
		if IsReadOnlyViolation(query) {
			t.Errorf("safe read rejected: %s", query)
		}
	}
}

func TestWhereMustBelongToMainStatement(t *testing.T) {
	for _, query := range []string{
		"UPDATE t SET value='WHERE'", "UPDATE t SET value=(SELECT n FROM x WHERE id=1)",
		"UPDATE t SET `WHERE`=1", "DELETE FROM t /* WHERE id=1 */",
		"WITH c AS (SELECT n FROM x WHERE id=1) DELETE FROM t",
	} {
		result := ClassifySQL(query)
		if !result.IsDestructive || !strings.HasPrefix(result.Reason, "Sin WHERE") {
			t.Errorf("unsafe classification: %s => %+v", query, result)
		}
	}
}

func TestSingleStatementValidation(t *testing.T) {
	for _, query := range []string{"SELECT 1; SELECT 2", "SELECT 1;;", "SELECT /* unclosed", "SELECT (1", "SELECT 1\x00", "/*! SELECT 1 */"} {
		if ValidateSQL(query) == nil {
			t.Errorf("accepted %q", query)
		}
	}
	for _, query := range []string{"SELECT ';' AS value; /* done */", "SELECT 'a--b#c/*d*/'", "SELECT 1;\n# done"} {
		if err := ValidateSQL(query); err != nil {
			t.Errorf("%q: %v", query, err)
		}
	}
}

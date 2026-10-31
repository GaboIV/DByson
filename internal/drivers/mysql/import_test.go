package mysql

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func splitAll(t *testing.T, script string) ([]string, []int) {
	t.Helper()
	s := NewScriptSplitter(strings.NewReader(script))
	var stmts []string
	var lines []int
	for {
		stmt, line, err := s.Next()
		if errors.Is(err, io.EOF) {
			return stmts, lines
		}
		if err != nil {
			t.Fatalf("split %q: %v", script, err)
		}
		stmts = append(stmts, stmt)
		lines = append(lines, line)
	}
}

func TestScriptSplitterMysqldump(t *testing.T) {
	dump := `-- MySQL dump 10.13  Distrib 8.4.0
--
-- Host: localhost    Database: shop
/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!50503 SET NAMES utf8mb4 */;

DROP TABLE IF EXISTS ` + "`customers`" + `;
CREATE TABLE ` + "`customers`" + ` (
  ` + "`id`" + ` int NOT NULL AUTO_INCREMENT, # surrogate key
  ` + "`name`" + ` varchar(80) DEFAULT NULL
) ENGINE=InnoDB;

LOCK TABLES ` + "`customers`" + ` WRITE;
INSERT INTO ` + "`customers`" + ` VALUES (1,'O\'Brien; Ltd'),(2,'Line\nbreak'),(3,'semi;colon -- not a comment'),(4,"dq \" ;");
UNLOCK TABLES;
`
	got, lines := splitAll(t, dump)
	want := []string{
		"/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */",
		"/*!50503 SET NAMES utf8mb4 */",
		"DROP TABLE IF EXISTS `customers`",
		"CREATE TABLE `customers` (\n  `id` int NOT NULL AUTO_INCREMENT, \n  `name` varchar(80) DEFAULT NULL\n) ENGINE=InnoDB",
		"LOCK TABLES `customers` WRITE",
		`INSERT INTO ` + "`customers`" + ` VALUES (1,'O\'Brien; Ltd'),(2,'Line\nbreak'),(3,'semi;colon -- not a comment'),(4,"dq \" ;")`,
		"UNLOCK TABLES",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statements:\n%q\nwant:\n%q", got, want)
	}
	if lines[2] != 7 || lines[3] != 8 {
		t.Fatalf("start lines = %v", lines)
	}
}

func TestScriptSplitterDelimiter(t *testing.T) {
	script := "USE shop;\nDELIMITER $$\nCREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN SET NEW.a = 1; SET NEW.b = 2; END$$\n  DELIMITER ;\nSELECT 1;"
	got, _ := splitAll(t, script)
	want := []string{
		"USE shop",
		"CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN SET NEW.a = 1; SET NEW.b = 2; END",
		"SELECT 1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScriptSplitterCommentsAndTail(t *testing.T) {
	got, _ := splitAll(t, "/* header */\nSELECT /*+ MAX_EXECUTION_TIME(1) */ 1 /* plain */ ;;\n-- trailing\nSELECT 2")
	want := []string{"SELECT /*+ MAX_EXECUTION_TIME(1) */ 1", "SELECT 2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, _ := splitAll(t, "-- only comments\n# here\n/* and here */\n"); len(got) != 0 {
		t.Fatalf("comment-only script produced %q", got)
	}
}

func TestScriptSplitterErrors(t *testing.T) {
	for _, script := range []string{"SELECT 'open", "SELECT 1 /* open", "DELIMITER\nSELECT 1"} {
		s := NewScriptSplitter(strings.NewReader(script))
		var err error
		for err == nil {
			_, _, err = s.Next()
		}
		if errors.Is(err, io.EOF) {
			t.Fatalf("%q: expected a syntax error", script)
		}
	}
}

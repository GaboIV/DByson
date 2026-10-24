package mysql

import (
	"context"
	"testing"
	"time"
)

func TestIntegrationCSVAtomicityAndForeignKeys(t *testing.T) {
	cfg := integrationConfig(t)
	db, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	name := "dbyson_productivity_test"
	if _, err = db.ExecContext(ctx, "CREATE DATABASE "+EscapeIdent(name)); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(context.Background(), "DROP DATABASE "+EscapeIdent(name))
	for _, statement := range []string{
		"CREATE TABLE " + EscapeIdent(name) + ".parent(a INT,b INT,PRIMARY KEY(a,b)) ENGINE=InnoDB",
		"CREATE TABLE " + EscapeIdent(name) + ".child(id INT PRIMARY KEY,a INT,b INT,note TEXT,stamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,computed INT GENERATED ALWAYS AS (id+1) STORED,CONSTRAINT parent_fk FOREIGN KEY(a,b) REFERENCES parent(a,b)) ENGINE=InnoDB",
		"INSERT INTO " + EscapeIdent(name) + ".parent VALUES(1,2)",
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := ForeignKeys(ctx, db, name)
	if err != nil || len(keys) != 1 || len(keys[0].Columns) != 2 || keys[0].Columns[0] != "a" || keys[0].ReferencedColumns[1] != "b" {
		t.Fatalf("compound FK lost: %+v %v", keys, err)
	}
	str := func(v string) *string { return &v }
	count, err := ImportCSV(ctx, db, name, "child", []string{"id", "a", "b", "note", "stamp"}, [][]*string{{str("1"), str("1"), str("2"), str("Muñoz, \"quoted\"\nnew line"), str("2026-01-02 03:04:05")}, {str("2"), str("1"), str("2"), nil, str("2026-02-03 04:05:06")}})
	if err != nil || count != 2 {
		t.Fatal(count, err)
	}
	var note string
	var null bool
	if err = db.QueryRowContext(ctx, "SELECT note FROM "+EscapeIdent(name)+".child WHERE id=1").Scan(&note); err != nil || note != "Muñoz, \"quoted\"\nnew line" {
		t.Fatal(note, err)
	}
	if err = db.QueryRowContext(ctx, "SELECT note IS NULL FROM "+EscapeIdent(name)+".child WHERE id=2").Scan(&null); err != nil || !null {
		t.Fatal("NULL lost", err)
	}
	_, err = ImportCSV(ctx, db, name, "child", []string{"id"}, [][]*string{{str("3")}, {str("1")}})
	if err == nil {
		t.Fatal("duplicate accepted")
	}
	var rows int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+EscapeIdent(name)+".child").Scan(&rows); err != nil || rows != 2 {
		t.Fatal("partial import committed", rows, err)
	}
	for _, cols := range [][]string{{"id", "id"}, {"id); DROP DATABASE x;--"}, {"computed"}} {
		if _, err = ImportCSV(ctx, db, name, "child", cols, [][]*string{{str("4")}}); err == nil {
			t.Fatal("invalid columns accepted")
		}
	}
}

func TestIntegrationAccountParametersInBothSQLModes(t *testing.T) {
	cfg := integrationConfig(t)
	db, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	password := "Param-'quoted'\\Unicode-ñ-2026!; DROP USER root;--"
	for _, mode := range []string{"", "NO_BACKSLASH_ESCAPES"} {
		if _, err = conn.ExecContext(ctx, "SET SESSION sql_mode = ?", mode); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.ExecContext(ctx, "CREATE USER 'qa_parameter_test'@'%' IDENTIFIED BY ?", password); err != nil {
			t.Fatal(mode, err)
		}
		client := cfg
		client.User = "qa_parameter_test"
		client.Password = password
		_, testErr := Test(ctx, client, nil)
		_, dropErr := conn.ExecContext(ctx, "DROP USER 'qa_parameter_test'@'%'")
		if testErr != nil {
			t.Fatal("password parameter changed or injected SQL", mode, testErr)
		}
		if dropErr != nil {
			t.Fatal(dropErr)
		}
	}
}

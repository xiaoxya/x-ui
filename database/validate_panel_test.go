package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestValidatePanelDB(t *testing.T) {
	dir := t.TempDir()
	create := func(name, schema string) string {
		path := filepath.Join(dir, name)
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(schema); err != nil {
			t.Fatal(err)
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := create("valid.db", `CREATE TABLE users(id INTEGER, username TEXT, password TEXT); CREATE TABLE inbounds(id INTEGER, user_id INTEGER, port INTEGER, protocol TEXT, settings TEXT, stream_settings TEXT); CREATE TABLE settings(id INTEGER, key TEXT, value TEXT);`)
	if err := ValidatePanelDB(valid); err != nil {
		t.Fatal(err)
	}
	unrelated := create("unrelated.db", `CREATE TABLE unrelated(id INTEGER);`)
	if err := ValidatePanelDB(unrelated); err == nil {
		t.Fatal("accepted unrelated SQLite database")
	}
	incomplete := create("incomplete.db", `CREATE TABLE users(id INTEGER, username TEXT, password TEXT); CREATE TABLE inbounds(id INTEGER, user_id INTEGER, port INTEGER, protocol TEXT, settings TEXT); CREATE TABLE settings(id INTEGER, key TEXT, value TEXT);`)
	if err := ValidatePanelDB(incomplete); err == nil {
		t.Fatal("accepted incompatible schema")
	}
	corrupt := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePanelDB(corrupt); err == nil {
		t.Fatal("accepted corrupt database")
	}
}

func TestCheckpointRejectsBusyWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	if err := InitDB(path); err != nil {
		t.Fatal(err)
	}
	defer CloseDB()
	sqlDB, err := GetDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	reader, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var n int
	if err = reader.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if _, err = sqlDB.Exec("INSERT INTO users(username,password) VALUES ('pending','value')"); err != nil {
		t.Fatal(err)
	}
	if err = Checkpoint(); err == nil {
		t.Fatal("checkpoint claimed success with a pinned WAL reader")
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = Checkpoint(); err != nil {
		t.Fatalf("checkpoint after release: %v", err)
	}
}

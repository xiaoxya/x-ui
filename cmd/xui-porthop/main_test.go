package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUsesOnlyEnabledAndChecksConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE inbounds(id INTEGER,protocol TEXT,port INTEGER,listen TEXT,stream_settings TEXT,enable INTEGER);
 INSERT INTO inbounds VALUES(1,'hysteria',443,'','{"finalmask":{"quicParams":{"udpHop":{"ports":"10000-10002"}}}}',1);
 INSERT INTO inbounds VALUES(2,'hysteria',444,'','{"finalmask":{"quicParams":{"udpHop":{"ports":"10000"}}}}',0);
 INSERT INTO inbounds VALUES(3,'vless',20000,'','{}',1);`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "udp dport 10000-10002 redirect to :443") {
		t.Fatalf("missing rule: %s", plan)
	}
	if _, err = db.Exec("UPDATE inbounds SET port=10001 WHERE id=3"); err != nil {
		t.Fatal(err)
	}
	if _, err = load(path); err == nil {
		t.Fatal("range overlapping another protocol was accepted")
	}
}

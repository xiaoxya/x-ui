package deployment

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterruptedSnapshotIsNotPublished(t *testing.T) {
	if root := os.Getenv("XUI_PARTIAL_SNAPSHOT_TEST"); root != "" {
		_, _ = snapshotDatabase(filepath.Join(root, "source.db"), filepath.Join(root, "backups"), func() { os.Exit(0) })
		os.Exit(1)
	}
	root := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE records(value TEXT); INSERT INTO records VALUES ('committed')"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInterruptedSnapshotIsNotPublished$")
	cmd.Env = append(os.Environ(), "XUI_PARTIAL_SNAPSHOT_TEST="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
	partial, err := filepath.Glob(filepath.Join(root, "backups", "xui-db-*", ".x-ui.db.partial"))
	if err != nil || len(partial) != 1 {
		t.Fatalf("partial snapshot missing: %v %v", partial, err)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(partial[0]), "x-ui.db")); !os.IsNotExist(err) {
		t.Fatalf("incomplete snapshot was published: %v", err)
	}
}

func TestSnapshotRecoversHotRollbackJournal(t *testing.T) {
	if source := os.Getenv("XUI_HOT_JOURNAL_TEST"); source != "" {
		db, err := sql.Open("sqlite3", source)
		if err != nil {
			panic(err)
		}
		for _, q := range []string{"PRAGMA journal_mode=DELETE", "PRAGMA cache_size=5", "CREATE TABLE records (value TEXT)", "INSERT INTO records VALUES ('committed')", "BEGIN IMMEDIATE", "UPDATE records SET value = 'uncommitted'"} {
			if _, err := db.Exec(q); err != nil {
				panic(err)
			}
		}
		for i := 0; i < 100; i++ {
			if _, err := db.Exec("INSERT INTO records VALUES (?)", strings.Repeat("x", 4096)); err != nil {
				panic(err)
			}
		}
		os.Exit(0) // Leave the transaction and its rollback journal open.
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "panel.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshotRecoversHotRollbackJournal$")
	cmd.Env = append(os.Environ(), "XUI_HOT_JOURNAL_TEST="+source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
	if _, err := os.Stat(source + "-journal"); err != nil {
		t.Fatalf("missing hot journal: %v", err)
	}
	ro, err := sql.Open("sqlite3", "file:"+source+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if err := ro.QueryRow("SELECT count(*) FROM records").Scan(new(int)); err == nil {
		ro.Close()
		t.Fatal("expected hot journal to prevent read-only access")
	}
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := SnapshotDatabase(source, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	var value string
	if err := db.QueryRow("SELECT count(*), min(value) FROM records").Scan(&count, &value); err != nil || count != 1 || value != "committed" {
		t.Fatalf("snapshot contents: count=%d value=%q error=%v", count, value, err)
	}
}

func TestSnapshotIncludesCommittedWAL(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "panel.db")
	db, err := sql.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE records (value TEXT)", "INSERT INTO records VALUES ('committed-in-wal')"} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	wal, err := os.Stat(source + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("expected uncheckpointed WAL: %v", err)
	}
	snapshot, err := SnapshotDatabase(source, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions: %v %v", info, err)
	}
	parent, err := os.Stat(filepath.Dir(snapshot))
	if err != nil || parent.Mode().Perm() != 0700 {
		t.Fatalf("snapshot directory permissions: %v %v", parent, err)
	}
	copyDB, err := sql.Open("sqlite3", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var value string
	if err := copyDB.QueryRow("SELECT value FROM records").Scan(&value); err != nil || value != "committed-in-wal" {
		t.Fatalf("snapshot contents %q: %v", value, err)
	}
}

func TestSnapshotNeverCreatesMissingSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "missing.db")
	if _, err := SnapshotDatabase(source, filepath.Join(dir, "backup")); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source unexpectedly created: %v", err)
	}
}

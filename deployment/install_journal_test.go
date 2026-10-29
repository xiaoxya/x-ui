package deployment

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommittedInstallCleanupCanBeRetried(t *testing.T) {
	o, j := interruptedInstall(t)
	j.Phase = "committed"
	if err := writeInstallJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after the temporary directory was removed.
	if err := os.RemoveAll(j.DBStage); err != nil {
		t.Fatal(err)
	}
	if err := RecoverInstall(o, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(o.JournalPath); !os.IsNotExist(err) {
		t.Fatalf("journal remains: %v", err)
	}
	if data, err := os.ReadFile(o.CredentialsPath); err != nil || string(data) != "secret" {
		t.Fatalf("installed credentials changed: %q, %v", data, err)
	}
}

func TestCommittedInstallCleanupFailureKeepsJournal(t *testing.T) {
	root := t.TempDir()
	journal := filepath.Join(root, "journal")
	if err := os.WriteFile(journal, []byte("committed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupInstallRecoveryData(journal, filepath.Join(root, "missing", "stage")); err == nil {
		t.Fatal("expected directory sync failure")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("journal lost after cleanup failure: %v", err)
	}
}

func TestRolledBackInstallCleanupCanBeRetried(t *testing.T) {
	o, j := interruptedInstall(t)
	j.Phase = "cleaned"
	if err := writeInstallJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(j.DBStage); err != nil {
		t.Fatal(err)
	}
	if err := RecoverInstall(o, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(o.JournalPath); !os.IsNotExist(err) {
		t.Fatalf("journal remains: %v", err)
	}
}

func TestInstallRecoveryRejectsOrphanRollbackJournal(t *testing.T) {
	o, _ := interruptedInstall(t)
	if err := os.Remove(o.DatabasePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.DatabasePath+"-journal", []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	if err := RecoverInstall(o, svc); err == nil {
		t.Fatal("orphan rollback journal accepted")
	}
	if !svc.active {
		t.Fatal("service stopped before rejecting orphan journal")
	}
	if data, err := os.ReadFile(o.DatabasePath + "-journal"); err != nil || string(data) != "preserve" {
		t.Fatalf("journal changed: %q, %v", data, err)
	}
}

func interruptedInstall(t *testing.T) (InstallOptions, installJournal) {
	t.Helper()
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	if err := os.Mkdir(dbDir, 0700); err != nil {
		t.Fatal(err)
	}
	stage, err := os.MkdirTemp(root, ".x-ui-stage-")
	if err != nil {
		t.Fatal(err)
	}
	dbStage, err := os.MkdirTemp(dbDir, ".x-ui-initial-")
	if err != nil {
		t.Fatal(err)
	}
	o := InstallOptions{InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(dbDir, "x-ui.db"), CredentialsPath: filepath.Join(dbDir, "credentials"), JournalPath: filepath.Join(root, "journal")}
	o.BackupDir = filepath.Join(root, "backups")
	j := installJournal{Phase: "installing", Stage: stage, DBStage: dbStage, Marker: strings.Repeat("a", 64)}
	for path, data := range map[string]string{filepath.Join(stage, ".xui-install-marker"): j.Marker, filepath.Join(dbStage, "credentials"): "secret", filepath.Join(dbStage, "service"): "unit", filepath.Join(dbStage, "command"): "command"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite3", filepath.Join(dbStage, "x-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE test(value TEXT); INSERT INTO test VALUES('post-crash')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := os.Link(filepath.Join(dbStage, "x-ui.db"), o.DatabasePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dbStage, "credentials"), o.CredentialsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stage, o.InstallDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.InstallDir, "runtime-data"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.UnitPath, []byte("unit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.CommandPath, []byte("command"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeInstallJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	return o, j
}

func TestRecoverInterruptedFreshInstall(t *testing.T) {
	o, j := interruptedInstall(t)
	svc := &fakeService{active: true, enabled: true}
	var saved string
	o.OnRecoverySnapshot = func(path string) { saved = path }
	var retained string
	o.OnRetainedFiles = func(path string) { retained = path }
	if err := RecoverInstall(o, svc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{o.InstallDir, o.UnitPath, o.CommandPath, o.DatabasePath, o.CredentialsPath, o.JournalPath, j.DBStage} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("recovery left %s: %v", path, err)
		}
	}
	if svc.active || svc.enabled {
		t.Fatal("service not stopped and disabled")
	}
	data, err := os.ReadFile(filepath.Join(retained, "x-ui", "runtime-data"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("runtime data not preserved: %q %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(filepath.Dir(saved), "initial-credentials"))
	if err != nil || string(data) != "secret" {
		t.Fatalf("credentials not preserved: %q %v", data, err)
	}
	db, err := sql.Open("sqlite3", saved)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow("SELECT value FROM test").Scan(&value); err != nil || value != "post-crash" {
		t.Fatalf("preserved database: %q %v", value, err)
	}
}

func TestRecoverFreshInstallRejectsForeignDatabase(t *testing.T) {
	o, _ := interruptedInstall(t)
	if err := os.Remove(o.DatabasePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.DatabasePath, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true, enabled: true}
	if err := RecoverInstall(o, svc); err == nil {
		t.Fatal("foreign database accepted")
	}
	b, err := os.ReadFile(o.DatabasePath)
	if err != nil || string(b) != "unrelated" {
		t.Fatalf("foreign database changed: %q %v", b, err)
	}
	if !svc.active || !svc.enabled {
		t.Fatal("service changed before conflict was detected")
	}
	if _, err := os.Stat(o.JournalPath); err != nil {
		t.Fatalf("journal lost: %v", err)
	}
}

func TestRecoverFreshInstallKeepsDatabaseWhenSnapshotFails(t *testing.T) {
	o, _ := interruptedInstall(t)
	if err := os.WriteFile(o.DatabasePath, []byte("corrupted-current-database"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true, enabled: true}
	if err := RecoverInstall(o, svc); err == nil {
		t.Fatal("invalid current database was discarded")
	}
	data, err := os.ReadFile(o.DatabasePath)
	if err != nil || string(data) != "corrupted-current-database" {
		t.Fatalf("current database changed: %q %v", data, err)
	}
	for _, path := range []string{o.InstallDir, o.UnitPath, o.CommandPath, o.JournalPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("recovery removed %s: %v", path, err)
		}
	}
}

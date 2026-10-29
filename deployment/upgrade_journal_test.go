package deployment

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverySnapshotPathRecordsAreCompleteAndPrivate(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"/backup/first/x-ui.db", "/backup/second/x-ui.db"} {
		if err := recordRecoverySnapshot(dir, path); err != nil {
			t.Fatal(err)
		}
	}
	records, err := filepath.Glob(filepath.Join(dir, "interrupted-snapshot-*.path"))
	if err != nil || len(records) != 2 {
		t.Fatalf("records: %v %v", records, err)
	}
	contents := map[string]bool{}
	for _, path := range records {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("record permissions: %v %v", info, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents[string(data)] = true
	}
	if !contents["/backup/first/x-ui.db\n"] || !contents["/backup/second/x-ui.db\n"] {
		t.Fatalf("record contents: %v", contents)
	}
	partial, err := filepath.Glob(filepath.Join(dir, ".interrupted-snapshot-*.partial"))
	if err != nil || len(partial) != 0 {
		t.Fatalf("temporary records remain: %v %v", partial, err)
	}
}

func setTestUpgradeIdentity(t *testing.T, j *upgradeJournal, oldDir, stageDir string) {
	t.Helper()
	dev, ino, err := directoryIdentity(oldDir)
	if err != nil {
		t.Fatal(err)
	}
	j.OldDevice, j.OldInode, j.Marker = dev, ino, strings.Repeat("b", 64)
	for _, item := range []struct {
		name string
		dest *string
	}{{"x-ui.service", &j.UnitSHA256}, {"x-ui-command", &j.CommandSHA256}} {
		*item.dest, err = snapshotDigest(filepath.Join(j.PreviousDir, item.name))
		if err != nil {
			t.Fatal(err)
		}
	}
	if stageDir != "" {
		if err := os.WriteFile(filepath.Join(stageDir, upgradeMarkerName), []byte(j.Marker), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoverInterruptedUpgrade(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal")}
	if err := os.Mkdir(o.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.InstallDir, "x-ui"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{o.UnitPath, o.CommandPath} {
		if err := os.WriteFile(path, []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE state(value TEXT); INSERT INTO state VALUES ('old')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	snapshot, err := SnapshotDatabase(o.DatabasePath, o.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE state SET value='new'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	previous, err := os.MkdirTemp(root, ".x-ui-previous-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(previous, "x-ui"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(previous, "x-ui", "x-ui"): "old", filepath.Join(previous, "x-ui.service"): "old unit", filepath.Join(previous, "x-ui-command"): "old command"} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	j := upgradeJournal{Phase: "switching", WasActive: true, PreviousDir: previous, Snapshot: snapshot, SnapshotSHA256: digest, UnitMode: 0644, CommandMode: 0755}
	setTestUpgradeIdentity(t, &j, filepath.Join(previous, "x-ui"), o.InstallDir)
	if err = writeOldIdentityMarker(filepath.Join(previous, "x-ui"), j.Marker); err != nil {
		t.Fatal(err)
	}
	// Simulate a filesystem device number changing after reboot.
	j.OldDevice ^= 1
	if err = writeJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.DatabasePath+"-journal", []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	var interruptedSnapshot string
	o.OnRecoverySnapshot = func(path string) { interruptedSnapshot = path }
	if err = RecoverUpgrade(o, svc); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(o.DatabasePath + "-journal"); !os.IsNotExist(err) {
		t.Fatalf("stale rollback journal remains: %v", err)
	}
	for path, want := range map[string]string{filepath.Join(o.InstallDir, "x-ui"): "old", o.UnitPath: "old unit", o.CommandPath: "old command"} {
		b, e := os.ReadFile(path)
		if e != nil || string(b) != want {
			t.Fatalf("%s: %q %v", path, b, e)
		}
	}
	db, err = sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow("SELECT value FROM state").Scan(&value); err != nil || value != "old" {
		t.Fatalf("database: %q %v", value, err)
	}
	newDB, err := sql.Open("sqlite3", interruptedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer newDB.Close()
	if err = newDB.QueryRow("SELECT value FROM state").Scan(&value); err != nil || value != "new" {
		t.Fatalf("post-interruption database: %q %v", value, err)
	}
	if !svc.active {
		t.Fatal("previous service not restarted")
	}
	if _, err = os.Stat(o.JournalPath); !os.IsNotExist(err) {
		t.Fatalf("journal retained: %v", err)
	}
}

func TestRecoverUpgradeRejectsChangedOrUnverifiedSnapshot(t *testing.T) {
	for _, mode := range []string{"changed", "missing-digest"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			o := UpgradeOptions{InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal")}
			previous, err := os.MkdirTemp(root, ".x-ui-previous-")
			if err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(o.BackupDir, "xui-db-test")
			if err = os.MkdirAll(backup, 0700); err != nil {
				t.Fatal(err)
			}
			snapshot := filepath.Join(backup, "x-ui.db")
			if err = os.WriteFile(snapshot, []byte("original snapshot"), 0600); err != nil {
				t.Fatal(err)
			}
			digest, err := snapshotDigest(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed" {
				if err = os.WriteFile(snapshot, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				digest = ""
			}
			if err = os.WriteFile(o.DatabasePath, []byte("current database"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = writeJournal(o.JournalPath, upgradeJournal{Phase: "switching", PreviousDir: previous, Snapshot: snapshot, SnapshotSHA256: digest}); err != nil {
				t.Fatal(err)
			}
			svc := &fakeService{active: true}
			if err = RecoverUpgrade(o, svc); err == nil || !strings.Contains(err.Error(), "checksum") {
				t.Fatalf("unexpected recovery result: %v", err)
			}
			if !svc.active {
				t.Fatal("service stopped before snapshot validation")
			}
			data, err := os.ReadFile(o.DatabasePath)
			if err != nil || string(data) != "current database" {
				t.Fatalf("database changed: %q %v", data, err)
			}
			if _, err = os.Stat(o.JournalPath); err != nil {
				t.Fatalf("journal lost: %v", err)
			}
		})
	}
}

func TestRecoveredStateDoesNotReapplyOldSnapshot(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "journal")
	if err := writeJournal(journal, upgradeJournal{Phase: "restored", WasActive: true}); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	if err := RecoverUpgrade(UpgradeOptions{JournalPath: journal}, svc); err != nil {
		t.Fatal(err)
	}
	if !svc.active {
		t.Fatal("recovery stopped an already restored service")
	}
}

type ineffectiveStopService struct{ fakeService }

func (s *ineffectiveStopService) Stop() error { return nil }

func TestRecoveredInactiveServiceIsStoppedAfterAutostart(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "journal")
	if err := writeJournal(journal, upgradeJournal{Phase: "restored", WasActive: false}); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	if err := RecoverUpgrade(UpgradeOptions{JournalPath: journal}, svc); err != nil {
		t.Fatal(err)
	}
	if svc.active {
		t.Fatal("previously inactive service remained active")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared: %v", err)
	}
}

func TestRecoveryKeepsJournalIfServiceRemainsActive(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "journal")
	if err := writeJournal(journal, upgradeJournal{Phase: "restored", WasActive: false}); err != nil {
		t.Fatal(err)
	}
	svc := &ineffectiveStopService{fakeService{active: true}}
	if err := RecoverUpgrade(UpgradeOptions{JournalPath: journal}, svc); err == nil {
		t.Fatal("active service accepted")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("journal lost: %v", err)
	}
	if err := RecoverUpgrade(UpgradeOptions{JournalPath: journal}, &svc.fakeService); err != nil {
		t.Fatal(err)
	}
	if svc.active {
		t.Fatal("service remained active after retry")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared after retry: %v", err)
	}
}

func TestRecoveryRetriesFailedRestartWithoutLosingJournal(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "journal")
	if err := writeJournal(journal, upgradeJournal{Phase: "restored", WasActive: true}); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{failStart: true}
	o := UpgradeOptions{JournalPath: journal}
	if err := RecoverUpgrade(o, svc); err == nil {
		t.Fatal("failed restart accepted")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("journal lost after failed restart: %v", err)
	}
	if err := RecoverUpgrade(o, svc); err != nil {
		t.Fatalf("restart retry: %v", err)
	}
	if !svc.active {
		t.Fatal("service not restarted")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared after retry: %v", err)
	}
}

func TestRecoveryKeepsJournalUntilRestoredServiceIsHealthy(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "journal")
	if err := writeJournal(journal, upgradeJournal{Phase: "restored", WasActive: true, CheckRestoredHealth: true}); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	o := UpgradeOptions{JournalPath: journal, RecoveryHealthCheck: func() error { return errors.New("unhealthy") }}
	if err := RecoverUpgrade(o, svc); err == nil {
		t.Fatal("unhealthy service accepted")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("journal lost on health failure: %v", err)
	}
	o.RecoveryHealthCheck = func() error { return nil }
	if err := RecoverUpgrade(o, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared: %v", err)
	}
}

func TestRecoveryKeepsCurrentDatabaseWhenBackupFails(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal")}
	if err := os.Mkdir(o.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.InstallDir, "x-ui"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE test(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	snapshot, err := SnapshotDatabase(o.DatabasePath, o.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.DatabasePath, []byte("corrupted-current-database"), 0600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.MkdirTemp(root, ".x-ui-previous-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(previous, "x-ui"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{filepath.Join(previous, "x-ui", "x-ui"): "old", filepath.Join(previous, "x-ui.service"): "unit", filepath.Join(previous, "x-ui-command"): "command"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	j := upgradeJournal{Phase: "switching", PreviousDir: previous, Snapshot: snapshot, SnapshotSHA256: digest, UnitMode: 0644, CommandMode: 0755}
	setTestUpgradeIdentity(t, &j, filepath.Join(previous, "x-ui"), o.InstallDir)
	if err = writeJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	if err = RecoverUpgrade(o, &fakeService{active: true}); err == nil {
		t.Fatal("invalid current database was overwritten")
	}
	data, err := os.ReadFile(o.DatabasePath)
	if err != nil || string(data) != "corrupted-current-database" {
		t.Fatalf("database changed: %q %v", data, err)
	}
	for _, path := range []string{o.InstallDir, o.JournalPath, filepath.Join(previous, "x-ui")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("recovery removed %s: %v", path, err)
		}
	}
}

func TestRecoveryRejectsSnapshotChangedAfterPreflight(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal")}
	if err := os.Mkdir(o.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE state(value TEXT); INSERT INTO state VALUES('old')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	snapshot, err := SnapshotDatabase(o.DatabasePath, o.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE state SET value='current'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	previous, err := os.MkdirTemp(root, ".x-ui-previous-")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(previous, "x-ui.service"): "unit", filepath.Join(previous, "x-ui-command"): "command"} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	j := upgradeJournal{Phase: "switching", PreviousDir: previous, Snapshot: snapshot, SnapshotSHA256: digest}
	setTestUpgradeIdentity(t, &j, o.InstallDir, "")
	if err = writeJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	o.OnRecoverySnapshot = func(string) {
		if e := os.WriteFile(snapshot, []byte("changed during recovery"), 0600); e != nil {
			t.Error(e)
		}
	}
	if err = RecoverUpgrade(o, &fakeService{active: true}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("modified backup accepted: %v", err)
	}
	db, err = sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow("SELECT value FROM state").Scan(&value); err != nil || value != "current" {
		t.Fatalf("current database changed: %q %v", value, err)
	}
	if _, err = os.Stat(o.JournalPath); err != nil {
		t.Fatalf("journal lost: %v", err)
	}
}

func TestRecoveryRejectsReplacedInstallationDirectory(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal")}
	if err := os.Mkdir(o.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(o.InstallDir, "foreign-file")
	if err := os.WriteFile(foreign, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE state(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	snapshot, err := SnapshotDatabase(o.DatabasePath, o.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.MkdirTemp(root, ".x-ui-previous-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(previous, "x-ui"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(previous, "x-ui.service"): "unit", filepath.Join(previous, "x-ui-command"): "command"} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	j := upgradeJournal{Phase: "switching", PreviousDir: previous, Snapshot: snapshot, SnapshotSHA256: digest}
	setTestUpgradeIdentity(t, &j, filepath.Join(previous, "x-ui"), "")
	if err = writeJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	if err = RecoverUpgrade(o, svc); err == nil {
		t.Fatal("foreign installation accepted")
	}
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != "keep" {
		t.Fatalf("foreign file changed: %q %v", data, err)
	}
	if !svc.active {
		t.Fatal("service stopped before directory validation")
	}
	if _, err = os.Stat(o.JournalPath); err != nil {
		t.Fatalf("journal lost: %v", err)
	}
}

func TestRecoveryRejectsModifiedPreviousUnit(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal")}
	if err := os.Mkdir(o.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE test(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	snapshot, err := SnapshotDatabase(o.DatabasePath, o.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.MkdirTemp(root, ".x-ui-previous-")
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(previous, "x-ui")
	if err = os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(previous, "x-ui.service")
	for path, data := range map[string]string{unit: "old unit", filepath.Join(previous, "x-ui-command"): "old command"} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	j := upgradeJournal{Phase: "switching", PreviousDir: previous, Snapshot: snapshot, SnapshotSHA256: digest}
	setTestUpgradeIdentity(t, &j, old, o.InstallDir)
	if err = writeJournal(o.JournalPath, j); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(unit, []byte("modified unit"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	if err = RecoverUpgrade(o, svc); err == nil || !strings.Contains(err.Error(), "previous service unit") {
		t.Fatalf("modified unit accepted: %v", err)
	}
	if !svc.active {
		t.Fatal("service stopped before checking backup files")
	}
	if _, err = os.Stat(o.JournalPath); err != nil {
		t.Fatalf("journal lost: %v", err)
	}
}

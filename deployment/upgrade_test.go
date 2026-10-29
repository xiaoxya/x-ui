package deployment

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeService struct {
	active    bool
	exists    bool
	enabled   bool
	failStart bool
	onStart   func()
	onReload  func() error
}

type changingService struct {
	fakeService
	checks int
}

func (s *changingService) Active() (bool, error) { s.checks++; return s.checks == 1, nil }

func (f *fakeService) Active() (bool, error) { return f.active, nil }
func (f *fakeService) Stop() error           { f.active = false; return nil }
func (f *fakeService) Reload() error {
	if f.onReload != nil {
		return f.onReload()
	}
	return nil
}
func (f *fakeService) Exists() (bool, error) { return f.exists, nil }
func (f *fakeService) Enable() error         { f.enabled = true; return nil }
func (f *fakeService) Disable() error        { f.enabled = false; return nil }
func (f *fakeService) Start() error {
	if f.onStart != nil {
		f.onStart()
	}
	if f.failStart {
		f.failStart = false
		return errors.New("injected start failure")
	}
	f.active = true
	return nil
}

func TestUpgradeSuccessAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "start-failure", "health-failure", "sidecar-failure", "snapshot-failure"} {
		t.Run(scenario, func(t *testing.T) {
			fail := scenario != "success"
			root := t.TempDir()
			o := UpgradeOptions{PackageDir: filepath.Join(root, "package"), InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "x-ui.service"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db.sqlite"), BackupDir: filepath.Join(root, "backups")}
			o.JournalPath = filepath.Join(root, "journal")
			var preserved string
			recoveryHealthChecks := 0
			o.RecoveryHealthCheck = func() error { recoveryHealthChecks++; return nil }
			o.OnRecoverySnapshot = func(path string) { preserved = path }
			for _, dir := range []string{o.PackageDir, filepath.Join(o.PackageDir, "bin"), o.InstallDir, filepath.Join(o.InstallDir, "bin"), filepath.Join(o.InstallDir, "cert")} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{filepath.Join(o.PackageDir, "x-ui"): "new binary", filepath.Join(o.PackageDir, "x-ui.sh"): "new command", filepath.Join(o.PackageDir, "x-ui.service"): "new unit", filepath.Join(o.PackageDir, "bin/geoip.dat"): "geoip", filepath.Join(o.PackageDir, "bin/geosite.dat"): "geosite", filepath.Join(o.InstallDir, "x-ui"): "old binary", filepath.Join(o.InstallDir, "bin/geoip.dat"): "custom-geo", filepath.Join(o.InstallDir, "cert/custom.pem"): "custom-cert", o.UnitPath: "old unit", o.CommandPath: "old command"}
			for path, content := range files {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			db, err := sql.Open("sqlite3", o.DatabasePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE state (value TEXT); INSERT INTO state VALUES ('old')"); err != nil {
				t.Fatal(err)
			}
			db.Close()
			beforeDB, err := os.ReadFile(o.DatabasePath)
			if err != nil {
				t.Fatal(err)
			}
			svc := &fakeService{active: true, failStart: scenario == "start-failure" || scenario == "snapshot-failure"}
			if scenario == "sidecar-failure" {
				svc.onReload = func() error {
					if err := os.Mkdir(o.DatabasePath+"-wal", 0700); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(o.DatabasePath+"-wal", "hold"), []byte("hold"), 0600); err != nil {
						return err
					}
					return errors.New("injected reload failure")
				}
			}
			if scenario == "health-failure" {
				o.HealthCheck = func() error { return errors.New("injected health failure") }
			}
			if fail && scenario != "sidecar-failure" {
				svc.onStart = func() {
					svc.onStart = nil
					if e := os.WriteFile(filepath.Join(o.InstallDir, "new-runtime-file"), []byte("new user data"), 0600); e != nil {
						t.Error(e)
					}
					changed, e := sql.Open("sqlite3", o.DatabasePath)
					if e != nil {
						t.Error(e)
						return
					}
					defer changed.Close()
					if _, e = changed.Exec("UPDATE state SET value='new'"); e != nil {
						t.Error(e)
					}
					if scenario == "snapshot-failure" {
						paths, e := filepath.Glob(filepath.Join(o.BackupDir, "xui-db-*", "x-ui.db"))
						if e != nil || len(paths) != 1 {
							t.Errorf("backup path: %v %v", paths, e)
							return
						}
						if e = os.WriteFile(paths[0], []byte("damaged snapshot"), 0600); e != nil {
							t.Error(e)
						}
					}
				}
			}
			result, err := Upgrade(o, svc)
			if scenario == "snapshot-failure" {
				if err == nil || !strings.Contains(err.Error(), "checksum") {
					t.Fatalf("corrupted snapshot accepted: %v", err)
				}
				if _, e := os.Stat(o.JournalPath); e != nil {
					t.Fatalf("recovery journal lost: %v", e)
				}
				if _, e := os.Stat(filepath.Join(result.PreviousDir, "x-ui", "x-ui")); e != nil {
					t.Fatalf("old installation lost: %v", e)
				}
				db, e := sql.Open("sqlite3", o.DatabasePath)
				if e != nil {
					t.Fatal(e)
				}
				defer db.Close()
				var value string
				if e = db.QueryRow("SELECT value FROM state").Scan(&value); e != nil || value != "new" {
					t.Fatalf("current database changed: %q %v", value, e)
				}
				return
			}
			if scenario == "sidecar-failure" {
				if err == nil {
					t.Fatal("expected sidecar failure")
				}
				if _, e := os.Stat(o.JournalPath); e != nil {
					t.Fatalf("recovery journal lost: %v", e)
				}
				if _, e := os.Stat(filepath.Join(result.PreviousDir, "x-ui", "x-ui")); e != nil {
					t.Fatalf("previous installation lost: %v", e)
				}
				data, e := os.ReadFile(filepath.Join(result.RetainedDir, "x-ui", "x-ui"))
				if e != nil || string(data) != "new binary" {
					t.Fatalf("unexpected installation change: %q %v", data, e)
				}
				afterDB, e := os.ReadFile(o.DatabasePath)
				if e != nil || !bytes.Equal(beforeDB, afterDB) {
					t.Fatalf("database changed after sidecar failure: %v", e)
				}
				return
			}
			if _, journalErr := os.Stat(o.JournalPath); !os.IsNotExist(journalErr) {
				t.Fatalf("transaction journal remained: %v", journalErr)
			}
			if (err != nil) != fail {
				t.Fatalf("error=%v", err)
			}
			if fail && recoveryHealthChecks != 1 {
				t.Fatalf("expected restored service health check, got %d", recoveryHealthChecks)
			}
			if !fail && recoveryHealthChecks != 0 {
				t.Fatalf("unexpected restored service health check: %d", recoveryHealthChecks)
			}
			want := "new binary"
			if fail {
				data, e := os.ReadFile(filepath.Join(result.RetainedDir, "x-ui", "new-runtime-file"))
				if e != nil || string(data) != "new user data" {
					t.Fatalf("new installation files not retained: %q %v", data, e)
				}
				want = "old binary"
			}
			data, err := os.ReadFile(filepath.Join(o.InstallDir, "x-ui"))
			if err != nil || string(data) != want {
				t.Fatalf("installation %q: %v", data, err)
			}
			for path, expected := range map[string]string{"bin/geoip.dat": "custom-geo", "cert/custom.pem": "custom-cert"} {
				data, err := os.ReadFile(filepath.Join(o.InstallDir, path))
				if err != nil || string(data) != expected {
					t.Fatalf("existing file %s: %q %v", path, data, err)
				}
			}
			if !svc.active {
				t.Fatal("previously active service was not running")
			}
			if _, err := os.Stat(result.DatabaseSnapshot); err != nil {
				t.Fatalf("missing database snapshot: %v", err)
			}
			if fail {
				newDB, e := sql.Open("sqlite3", preserved)
				if e != nil {
					t.Fatal(e)
				}
				defer newDB.Close()
				var newValue string
				if e = newDB.QueryRow("SELECT value FROM state").Scan(&newValue); e != nil || newValue != "new" {
					t.Fatalf("preserved database %q: %v", newValue, e)
				}
				for path, expected := range map[string]string{o.UnitPath: "old unit", o.CommandPath: "old command"} {
					b, e := os.ReadFile(path)
					if e != nil || string(b) != expected {
						t.Fatalf("restore %s: %q %v", path, b, e)
					}
				}
				check, e := sql.Open("sqlite3", o.DatabasePath)
				if e != nil {
					t.Fatal(e)
				}
				defer check.Close()
				var value string
				if e = check.QueryRow("SELECT value FROM state").Scan(&value); e != nil || value != "old" {
					t.Fatalf("database rollback %q: %v", value, e)
				}
			} else if _, err := os.Stat(filepath.Join(result.PreviousDir, "x-ui", "x-ui")); err != nil {
				t.Fatalf("old installation not retained: %v", err)
			}
		})
	}
}

func TestUpgradeRejectsUnsafePackageBeforeStopping(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "installed")
	if err := os.Mkdir(installDir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "package-link")
	if err := os.Symlink(installDir, link); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	o := UpgradeOptions{PackageDir: link, InstallDir: installDir, UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backup")}
	if _, err := Upgrade(o, svc); err == nil {
		t.Fatal("symlink package accepted")
	}
	if !svc.active {
		t.Fatal("service stopped on preflight failure")
	}
}

func TestStagedPackageValidationOccursBeforeServiceStop(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{PackageDir: filepath.Join(root, "package"), InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), ValidatePackage: func(string) error { return errors.New("injected validation failure") }}
	for _, dir := range []string{o.PackageDir, filepath.Join(o.PackageDir, "bin"), o.InstallDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"x-ui", "x-ui.sh", "x-ui.service", "bin/geoip.dat", "bin/geosite.dat"} {
		if err := os.WriteFile(filepath.Join(o.PackageDir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(o.DatabasePath, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	if _, err := Upgrade(o, svc); err == nil {
		t.Fatal("invalid package accepted")
	}
	if !svc.active {
		t.Fatal("service stopped during preflight")
	}
}

func TestExistingUnitValidationOccursBeforeServiceStop(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{PackageDir: filepath.Join(root, "package"), InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal"), ValidateExisting: func([]byte) error { return errors.New("custom unit") }}
	for _, dir := range []string{o.PackageDir, filepath.Join(o.PackageDir, "bin"), o.InstallDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"x-ui", "x-ui.sh", "x-ui.service", "bin/geoip.dat", "bin/geosite.dat"} {
		if err := os.WriteFile(filepath.Join(o.PackageDir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{o.DatabasePath, o.UnitPath} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc := &fakeService{active: true}
	if _, err := Upgrade(o, svc); err == nil {
		t.Fatal("custom unit accepted")
	}
	if !svc.active {
		t.Fatal("service stopped on validation error")
	}
	if _, err := os.Stat(o.JournalPath); !os.IsNotExist(err) {
		t.Fatalf("journal created before validation: %v", err)
	}
}

func TestUpgradeRejectsChangingServiceState(t *testing.T) {
	root := t.TempDir()
	o := UpgradeOptions{PackageDir: filepath.Join(root, "package"), InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "journal")}
	for _, dir := range []string{o.PackageDir, filepath.Join(o.PackageDir, "bin"), o.InstallDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"x-ui", "x-ui.sh", "x-ui.service", "bin/geoip.dat", "bin/geosite.dat"} {
		if err := os.WriteFile(filepath.Join(o.PackageDir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{o.DatabasePath, o.UnitPath, o.CommandPath} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc := &changingService{fakeService: fakeService{active: true}}
	if _, err := Upgrade(o, svc); err == nil || !strings.Contains(err.Error(), "state changed") {
		t.Fatalf("changing service state accepted: %v", err)
	}
	if !svc.active {
		t.Fatal("service stopped despite changing state")
	}
	if _, err := os.Stat(o.JournalPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected journal: %v", err)
	}
}

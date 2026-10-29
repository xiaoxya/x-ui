//go:build linux

package deployment

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type killService struct {
	fakeService
	killAt string
	mode   string
	dbPath string
}

func (s *killService) Stop() error {
	if s.killAt == "stop" {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	s.active = false
	return nil
}
func (s *killService) Start() error {
	if s.killAt == "start" || s.killAt == "health" {
		db, err := sql.Open("sqlite3", s.dbPath)
		if err != nil {
			os.Exit(41)
		}
		query := "INSERT INTO test VALUES ('new-write')"
		if s.mode == "upgrade" {
			query = "UPDATE state SET value='new-write'"
			if s.killAt == "health" {
				if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
					os.Exit(44)
				}
			}
		}
		if _, err = db.Exec(query); err != nil {
			os.Exit(42)
		}
		if s.mode != "upgrade" || s.killAt != "health" {
			if err = db.Close(); err != nil {
				os.Exit(43)
			}
		}
		if s.killAt == "start" {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		}
	}
	s.active = true
	return nil
}

func (s *killService) Reload() error {
	if s.killAt == "reload" {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	return nil
}
func (s *killService) Enable() error {
	if s.killAt == "enable" {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	return nil
}

func crashOptions(root string) UpgradeOptions {
	return UpgradeOptions{PackageDir: filepath.Join(root, "package"), InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db", "x-ui.db"), BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "upgrade-journal")}
}
func crashInstallOptions(root string) InstallOptions {
	u := crashOptions(root)
	return InstallOptions{PackageDir: u.PackageDir, InstallDir: u.InstallDir, UnitPath: u.UnitPath, CommandPath: u.CommandPath, DatabasePath: u.DatabasePath, CredentialsPath: filepath.Join(root, "db", "credentials"), JournalPath: filepath.Join(root, "install-journal"), ValidatePackage: func(string) error { return nil }, PrepareDatabase: func(path string) ([]byte, error) {
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		_, err = db.Exec("CREATE TABLE test(value TEXT)")
		return []byte("credentials"), err
	}, BackupDir: filepath.Join(root, "backups")}
}
func TestCrashSubprocess(t *testing.T) {
	root := os.Getenv("XUI_CRASH_TEST_ROOT")
	mode := os.Getenv("XUI_CRASH_TEST_MODE")
	stage := os.Getenv("XUI_CRASH_TEST_STAGE")
	if root == "" {
		t.Skip("helper process")
	}
	if mode == "recover-upgrade" {
		o := crashOptions(root)
		if stage == "sidecars" {
			o.afterRecoverySidecars = func() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL) }
		} else {
			o.OnRecoverySnapshot = func(string) { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL) }
		}
		_ = RecoverUpgrade(o, &fakeService{active: true})
		t.Fatal("expected recovery to be killed")
	}
	if mode == "recover-install" {
		o := crashInstallOptions(root)
		o.OnRecoverySnapshot = func(string) { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL) }
		_ = RecoverInstall(o, &fakeService{active: true, enabled: true})
		t.Fatal("expected recovery to be killed")
	}
	if mode == "upgrade" {
		o := crashOptions(root)
		if stage == "health" {
			o.HealthCheck = func() error { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); return nil }
		}
		_, _ = Upgrade(o, &killService{fakeService: fakeService{active: true}, killAt: stage, mode: mode, dbPath: o.DatabasePath})
	} else {
		o := crashInstallOptions(root)
		if stage == "health" {
			o.HealthCheck = func() error { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); return nil }
		}
		_ = Install(o, &killService{killAt: stage, mode: mode, dbPath: o.DatabasePath})
	}
	t.Fatal("expected SIGKILL before returning")
}

func TestRecoveryAfterRealProcessKill(t *testing.T) {
	for _, tc := range []struct{ mode, stage string }{{"install", "reload"}, {"install", "enable"}, {"install", "start"}, {"install", "health"}, {"upgrade", "stop"}, {"upgrade", "reload"}, {"upgrade", "start"}, {"upgrade", "health"}} {
		mode, stage := tc.mode, tc.stage
		t.Run(mode+"/"+stage, func(t *testing.T) {
			root := t.TempDir()
			o := crashOptions(root)
			if err := os.MkdirAll(filepath.Join(o.PackageDir, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string]string{"x-ui": "new", "x-ui.sh": "new command", "x-ui.service": "new unit", "bin/geoip.dat": "geoip", "bin/geosite.dat": "geosite"} {
				if err := os.WriteFile(filepath.Join(o.PackageDir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "upgrade" {
				if err := os.Mkdir(o.InstallDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Dir(o.DatabasePath), 0700); err != nil {
					t.Fatal(err)
				}
				for path, data := range map[string]string{filepath.Join(o.InstallDir, "x-ui"): "old", o.UnitPath: "old unit", o.CommandPath: "old command"} {
					if err := os.WriteFile(path, []byte(data), 0600); err != nil {
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
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashSubprocess$")
			cmd.Env = append(os.Environ(), "XUI_CRASH_TEST_ROOT="+root, "XUI_CRASH_TEST_MODE="+mode, "XUI_CRASH_TEST_STAGE="+stage)
			err := cmd.Run()
			if exit, ok := err.(*exec.ExitError); !ok || !exit.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
				t.Fatalf("child did not terminate by signal: %v", err)
			}
			if stage == "health" {
				// Kill a first recovery after its durable snapshot, then retry it.
				if mode == "upgrade" {
					if info, e := os.Stat(o.DatabasePath + "-wal"); e != nil || info.Size() == 0 {
						t.Fatalf("expected uncheckpointed WAL: %v", e)
					}
					if e := os.WriteFile(o.DatabasePath+"-journal", []byte("stale"), 0600); e != nil {
						t.Fatal(e)
					}
				}
				cmd = exec.Command(os.Args[0], "-test.run=^TestCrashSubprocess$")
				cmd.Env = append(os.Environ(), "XUI_CRASH_TEST_ROOT="+root, "XUI_CRASH_TEST_MODE=recover-"+mode, "XUI_CRASH_TEST_STAGE=sidecars")
				err = cmd.Run()
				if exit, ok := err.(*exec.ExitError); !ok || !exit.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
					t.Fatalf("recovery child did not terminate by signal: %v", err)
				}
				if mode == "upgrade" {
					if _, e := os.Stat(o.DatabasePath + "-journal"); !os.IsNotExist(e) {
						t.Fatalf("rollback journal not removed before kill: %v", e)
					}
				}
				snapshots, e := filepath.Glob(filepath.Join(o.BackupDir, "xui-db-*", "x-ui.db"))
				if e != nil || len(snapshots) == 0 {
					t.Fatalf("interrupted recovery snapshot missing: %v", e)
				}
				found := false
				for _, path := range snapshots {
					db, e := sql.Open("sqlite3", path)
					if e != nil {
						t.Fatal(e)
					}
					query := "SELECT value FROM test LIMIT 1"
					if mode == "upgrade" {
						query = "SELECT value FROM state LIMIT 1"
					}
					var value string
					if db.QueryRow(query).Scan(&value) == nil && value == "new-write" {
						found = true
					}
					_ = db.Close()
				}
				if !found {
					t.Fatal("first recovery snapshot did not preserve the new data")
				}
				var records []string
				if mode == "upgrade" {
					data, e := os.ReadFile(o.JournalPath)
					if e != nil {
						t.Fatal(e)
					}
					var journal upgradeJournal
					if e = json.Unmarshal(data, &journal); e != nil {
						t.Fatal(e)
					}
					records, e = filepath.Glob(filepath.Join(journal.PreviousDir, "interrupted-snapshot-*.path"))
					if e != nil {
						t.Fatal(e)
					}
				} else {
					records, e = filepath.Glob(filepath.Join(o.BackupDir, "xui-db-*", "interrupted-snapshot-*.path"))
					if e != nil {
						t.Fatal(e)
					}
				}
				if len(records) == 0 {
					t.Fatal("interrupted recovery snapshot path record missing")
				}
				data, e := os.ReadFile(records[0])
				if e != nil {
					t.Fatal(e)
				}
				if _, e = os.Stat(strings.TrimSpace(string(data))); e != nil {
					t.Fatalf("recorded snapshot unavailable: %v", e)
				}
			}
			svc := &fakeService{active: true, enabled: true}
			var preserved string
			if mode == "upgrade" {
				o.OnRecoverySnapshot = func(path string) { preserved = path }
				if err := RecoverUpgrade(o, svc); err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(o.InstallDir, "x-ui"))
				if err != nil || string(b) != "old" {
					t.Fatalf("upgrade recovery %q: %v", b, err)
				}
			} else {
				install := crashInstallOptions(root)
				install.OnRecoverySnapshot = func(path string) { preserved = path }
				if err := RecoverInstall(install, svc); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(install.InstallDir); !os.IsNotExist(err) {
					t.Fatalf("installation remains: %v", err)
				}
			}
			if stage == "start" || stage == "health" {
				db, e := sql.Open("sqlite3", preserved)
				if e != nil {
					t.Fatal(e)
				}
				defer db.Close()
				query := "SELECT value FROM test LIMIT 1"
				if mode == "upgrade" {
					query = "SELECT value FROM state LIMIT 1"
				}
				var value string
				if e = db.QueryRow(query).Scan(&value); e != nil || value != "new-write" {
					t.Fatalf("new data not preserved: %q %v", value, e)
				}
			}
		})
	}
}

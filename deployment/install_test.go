package deployment

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFreshInstallAndFailureRollback(t *testing.T) {
	for _, scenario := range []string{"success", "start-failure", "health-failure"} {
		t.Run(scenario, func(t *testing.T) {
			fail := scenario != "success"
			root := t.TempDir()
			pkg := filepath.Join(root, "package")
			if err := os.Mkdir(pkg, 0700); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{"x-ui": "panel", "x-ui.sh": "command", "x-ui.service": "unit"} {
				if err := os.WriteFile(filepath.Join(pkg, name), []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}
			o := InstallOptions{PackageDir: pkg, InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db", "x-ui.db"), CredentialsPath: filepath.Join(root, "db", "credentials"), ValidatePackage: func(string) error { return nil }, PrepareDatabase: func(path string) ([]byte, error) {
				db, err := sql.Open("sqlite3", path)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				_, err = db.Exec("CREATE TABLE records(value TEXT)")
				return []byte("generated secret"), err
			}}
			o.JournalPath = filepath.Join(root, "journal")
			o.BackupDir = filepath.Join(root, "backups")
			var retained, snapshot string
			o.OnRetainedFiles = func(path string) { retained = path }
			o.OnRecoverySnapshot = func(path string) { snapshot = path }
			if scenario == "health-failure" {
				o.HealthCheck = func() error { return errors.New("injected health failure") }
			}
			svc := &fakeService{failStart: scenario == "start-failure"}
			if fail {
				svc.onStart = func() {
					if err := os.WriteFile(filepath.Join(o.InstallDir, "runtime-data"), []byte("keep"), 0600); err != nil {
						t.Error(err)
					}
				}
			}
			err := Install(o, svc)
			if _, e := os.Stat(o.JournalPath); !os.IsNotExist(e) {
				t.Fatalf("journal not cleared: %v", e)
			}
			if (err != nil) != fail {
				t.Fatalf("error: %v", err)
			}
			for _, path := range []string{o.InstallDir, o.UnitPath, o.CommandPath, o.DatabasePath, o.CredentialsPath} {
				_, err := os.Stat(path)
				if fail && !os.IsNotExist(err) {
					t.Fatalf("failed installation left %s: %v", path, err)
				}
				if !fail && err != nil {
					t.Fatalf("missing %s: %v", path, err)
				}
			}
			if svc.active == fail || svc.enabled == fail {
				t.Fatalf("incorrect service state: %+v", svc)
			}
			if !fail {
				info, err := os.Stat(o.CredentialsPath)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("credentials permissions: %v %v", info, err)
				}
			} else {
				data, err := os.ReadFile(filepath.Join(retained, "x-ui", "runtime-data"))
				if err != nil || string(data) != "keep" {
					t.Fatalf("runtime data not retained: %q %v", data, err)
				}
				data, err = os.ReadFile(filepath.Join(filepath.Dir(snapshot), "initial-credentials"))
				if err != nil || string(data) != "generated secret" {
					t.Fatalf("credentials not retained: %q %v", data, err)
				}
			}
		})
	}
}

func TestFreshInstallNeverOverwritesFiles(t *testing.T) {
	root := t.TempDir()
	unit := filepath.Join(root, "unit")
	if err := os.WriteFile(unit, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "package")
	if err := os.Mkdir(pkg, 0700); err != nil {
		t.Fatal(err)
	}
	o := InstallOptions{PackageDir: pkg, InstallDir: filepath.Join(root, "installed"), UnitPath: unit, CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db"), CredentialsPath: filepath.Join(root, "credentials"), ValidatePackage: func(string) error { return errors.New("must not run") }, PrepareDatabase: func(string) ([]byte, error) { return nil, errors.New("must not run") }}
	if err := Install(o, &fakeService{}); err == nil {
		t.Fatal("existing unit accepted")
	}
	b, err := os.ReadFile(unit)
	if err != nil || string(b) != "existing" {
		t.Fatalf("existing unit changed: %q %v", b, err)
	}
}

func TestFreshInstallRejectsDatabaseSidecars(t *testing.T) {
	for _, suffix := range sqliteSidecarSuffixes {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			db := filepath.Join(root, "x-ui.db")
			sidecar := db + suffix
			if err := os.WriteFile(sidecar, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			o := InstallOptions{PackageDir: root, InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: db, CredentialsPath: filepath.Join(root, "credentials"), ValidatePackage: func(string) error { return nil }, PrepareDatabase: func(string) ([]byte, error) { return nil, nil }}
			if err := Install(o, &fakeService{}); err == nil {
				t.Fatal("existing sidecar accepted")
			}
			data, err := os.ReadFile(sidecar)
			if err != nil || string(data) != "preserve" {
				t.Fatalf("sidecar changed: %q, %v", data, err)
			}
		})
	}
}

//go:build linux

package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alireza0/x-ui/deployment"
	_ "github.com/mattn/go-sqlite3"
)

type testService struct{ active, enabled bool }

func (s *testService) Exists() (bool, error) { return false, nil }
func (s *testService) Active() (bool, error) { return s.active, nil }
func (s *testService) Stop() error           { s.active = false; return nil }
func (s *testService) Start() error          { s.active = true; return nil }
func (s *testService) Reload() error         { return nil }
func (s *testService) Enable() error         { s.enabled = true; return nil }
func (s *testService) Disable() error        { s.enabled = false; return nil }

func TestValidateBuiltPackage(t *testing.T) {
	path := os.Getenv("XUI_PACKAGE_TEST_DIR")
	if path == "" {
		t.Skip("set XUI_PACKAGE_TEST_DIR")
	}
	if err := validatePackage(path); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareDatabaseUsesGeneratedCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	credentials, err := prepareDatabase(path, 23456)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var username, password string
	if err := db.QueryRow("SELECT username, password FROM users LIMIT 1").Scan(&username, &password); err != nil {
		t.Fatal(err)
	}
	if username == "admin" || password == "admin" || len(password) < 24 || !strings.Contains(string(credentials), username) || !strings.Contains(string(credentials), password) {
		t.Fatal("generated credentials are not applied")
	}
	var port, base string
	if err := db.QueryRow("SELECT value FROM settings WHERE key = 'webPort'").Scan(&port); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT value FROM settings WHERE key = 'webBasePath'").Scan(&base); err != nil {
		t.Fatal(err)
	}
	if port != "23456" || len(base) < 12 || !strings.Contains(string(credentials), base) {
		t.Fatal("generated port or base path not stored")
	}
}

func TestInstallRealPackageWithIsolatedSystemd(t *testing.T) {
	pkg := os.Getenv("XUI_PACKAGE_TEST_DIR")
	if pkg == "" {
		t.Skip("set XUI_PACKAGE_TEST_DIR")
	}
	pkg, err := filepath.Abs(pkg)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	o := deployment.InstallOptions{PackageDir: pkg, InstallDir: filepath.Join(root, "installed"), UnitPath: filepath.Join(root, "unit"), CommandPath: filepath.Join(root, "command"), DatabasePath: filepath.Join(root, "db", "x-ui.db"), CredentialsPath: filepath.Join(root, "db", "credentials"), ValidatePackage: validatePackage, PrepareDatabase: func(path string) ([]byte, error) { return prepareDatabase(path, 23456) }}
	o.JournalPath = filepath.Join(root, "journal")
	sys := &testService{}
	if err := deployment.Install(o, sys); err != nil {
		t.Fatal(err)
	}
	if !sys.active || !sys.enabled {
		t.Fatal("service was not enabled and started")
	}
	info, err := os.Stat(o.CredentialsPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credentials: %v %v", info, err)
	}
	db, err := sql.Open("sqlite3", o.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var username, password string
	if err := db.QueryRow("SELECT username, password FROM users LIMIT 1").Scan(&username, &password); err != nil {
		t.Fatal(err)
	}
	if username == "admin" || password == "admin" {
		t.Fatal("default credentials retained")
	}
}

//go:build linux

package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alireza0/x-ui/deployment"
)

// Exercises real systemd with a uniquely named user-level service and paths
// confined to a temporary directory. The system x-ui service is never touched.
func TestUserSystemdInstallUpgradeAndRollback(t *testing.T) {
	if os.Getenv("XUI_SYSTEMD_INTEGRATION") != "1" || os.Getenv("XUI_PACKAGE_TEST_DIR") == "" {
		t.Skip("requires XUI_SYSTEMD_INTEGRATION=1 and XUI_PACKAGE_TEST_DIR")
	}
	packageSource, err := filepath.Abs(os.Getenv("XUI_PACKAGE_TEST_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var id [8]byte
	if _, err = rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	name := "codex-xui-lifecycle-" + hex.EncodeToString(id[:]) + ".service"
	unitPath := filepath.Join(root, name)
	run := func(args ...string) error {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl: %w: %s", err, out)
		}
		return nil
	}
	t.Cleanup(func() { _ = run("stop", name); _ = run("disable", "--runtime", name); _ = run("daemon-reload") })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	installDir := filepath.Join(root, "installed")
	dbDir := filepath.Join(root, "db")
	unit := fmt.Sprintf("[Unit]\nDescription=Isolated XUI lifecycle test\n[Service]\nType=simple\nWorkingDirectory=%s\nExecStart=%s/x-ui run\nEnvironment=XUI_DB_FOLDER=%s\nEnvironment=XUI_BIN_FOLDER=%s/bin\nEnvironment=XUI_PORTHOP_AUTO=0\nRestart=no\n[Install]\nWantedBy=default.target\n", installDir, installDir, dbDir, installDir)
	makePackage := func(name, version string) string {
		t.Helper()
		dest := filepath.Join(root, name)
		if out, err := exec.Command("cp", "-a", packageSource, dest).CombinedOutput(); err != nil {
			t.Fatalf("copy package: %v %s", err, out)
		}
		if err := os.WriteFile(filepath.Join(dest, "x-ui.service"), []byte(unit), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dest, "test-version"), []byte(version), 0600); err != nil {
			t.Fatal(err)
		}
		return dest
	}
	pkg1, pkg2, pkg3 := makePackage("pkg1", "one"), makePackage("pkg2", "two"), makePackage("pkg3", "three")
	shim := filepath.Join(root, "systemctl-shim")
	marker := filepath.Join(root, "linked")
	// The shim redirects the fixed production unit name to the isolated user
	// unit, preserving the adapter's actual systemctl calls and state checks.
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
 show) exec /usr/bin/systemctl --user show "$2" "$3" '%s';;
 start|stop) exec /usr/bin/systemctl --user "$1" '%s';;
 enable|disable) exec /usr/bin/systemctl --user "$1" --runtime '%s';;
 daemon-reload)
   if [ -f '%s' ] && [ ! -f '%s' ]; then /usr/bin/systemctl --user link --runtime '%s' || exit; touch '%s'; fi
   exec /usr/bin/systemctl --user daemon-reload;;
 *) exit 2;;
esac
`, name, name, name, unitPath, marker, unitPath, marker)
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	sys := deployment.Systemd{Path: shim}
	dbPath := filepath.Join(dbDir, "x-ui.db")
	health := func() error { return deployment.CheckPanelHealth(dbPath, 12*time.Second) }
	prepare := func(path string) ([]byte, error) {
		credentials, err := prepareDatabase(path, port)
		if err != nil {
			return nil, err
		}
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		_, err = db.Exec("INSERT INTO settings (key,value) VALUES ('webListen','127.0.0.1')")
		return credentials, err
	}
	install := deployment.InstallOptions{PackageDir: pkg1, InstallDir: installDir, UnitPath: unitPath, CommandPath: filepath.Join(root, "command"), DatabasePath: dbPath, CredentialsPath: filepath.Join(dbDir, "credentials"), JournalPath: filepath.Join(root, "install-journal"), ValidatePackage: validatePackageComponents, PrepareDatabase: prepare, HealthCheck: health}
	if err := deployment.Install(install, sys); err != nil {
		t.Fatalf("real user-systemd install: %v", err)
	}
	if err := os.Mkdir(filepath.Join(installDir, "cert"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"cert/custom.pem": "custom-certificate", "bin/custom.dat": "custom-geodata"} {
		if err := os.WriteFile(filepath.Join(installDir, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	upgrade := deployment.UpgradeOptions{PackageDir: pkg2, InstallDir: installDir, UnitPath: unitPath, CommandPath: install.CommandPath, DatabasePath: dbPath, BackupDir: filepath.Join(root, "backups"), JournalPath: filepath.Join(root, "upgrade-journal"), ValidatePackage: validatePackageComponents, HealthCheck: health}
	result, err := deployment.Upgrade(upgrade, sys)
	if err != nil {
		t.Fatalf("real user-systemd upgrade: %v", err)
	}
	if _, err := os.Stat(result.DatabaseSnapshot); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(installDir, "test-version"))
	if err != nil || string(b) != "two" {
		t.Fatalf("upgrade version %q: %v", b, err)
	}
	for path, want := range map[string]string{"cert/custom.pem": "custom-certificate", "bin/custom.dat": "custom-geodata"} {
		b, e := os.ReadFile(filepath.Join(installDir, path))
		if e != nil || string(b) != want {
			t.Fatalf("preserved %s: %q %v", path, b, e)
		}
	}
	upgrade.PackageDir = pkg3
	upgrade.HealthCheck = func() error { return errors.New("injected health failure") }
	if _, err = deployment.Upgrade(upgrade, sys); err == nil || !strings.Contains(err.Error(), "injected health failure") {
		t.Fatalf("expected rollback: %v", err)
	}
	b, err = os.ReadFile(filepath.Join(installDir, "test-version"))
	if err != nil || string(b) != "two" {
		t.Fatalf("rollback version %q: %v", b, err)
	}
	for path, want := range map[string]string{"cert/custom.pem": "custom-certificate", "bin/custom.dat": "custom-geodata"} {
		b, e := os.ReadFile(filepath.Join(installDir, path))
		if e != nil || string(b) != want {
			t.Fatalf("rollback preserved %s: %q %v", path, b, e)
		}
	}
	if err = health(); err != nil {
		t.Fatalf("service after rollback: %v", err)
	}
}

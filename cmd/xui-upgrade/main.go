//go:build linux

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/deployment"
	"github.com/alireza0/x-ui/web/service"
)

func validatePackage(dir string) error {
	if err := verifyPackageManifest(dir); err != nil {
		return fmt.Errorf("package integrity: %w", err)
	}
	unit, err := os.ReadFile(filepath.Join(dir, "x-ui.service"))
	if err != nil {
		return err
	}
	if err = deployment.ValidateStandardUnit(unit); err != nil {
		return fmt.Errorf("package unit: %w", err)
	}
	if err = validatePackageComponents(dir); err != nil {
		return err
	}
	// Version checks execute staged binaries; verify their contents again in
	// case a component modified the package while it was being checked.
	if err = verifyPackageManifest(dir); err != nil {
		return fmt.Errorf("package integrity after version checks: %w", err)
	}
	return nil
}

func validatePackageComponents(dir string) error {
	for _, name := range []string{"x-ui.service", "bin/geoip.dat", "bin/geosite.dat"} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("missing package file %s", name)
		}
	}
	for _, name := range []string{"x-ui", "x-ui.sh", "xui-porthop", "xui-db-snapshot", "xui-upgrade", "xui-manage-local", filepath.Join("bin", "xray-linux-"+runtime.GOARCH)} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("missing executable %s", name)
		}
	}
	for _, item := range []struct{ file, arg, prefix string }{{"x-ui", "-v", ""}, {filepath.Join("bin", "xray-linux-"+runtime.GOARCH), "version", "Xray "}} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		output, err := exec.CommandContext(ctx, filepath.Join(dir, item.file), item.arg).CombinedOutput()
		cancel()
		if err != nil || len(strings.TrimSpace(string(output))) == 0 || !strings.HasPrefix(string(output), item.prefix) {
			return fmt.Errorf("cannot run %s version check", item.file)
		}
	}
	return nil
}

func randomToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func prepareDatabase(path string, port int) ([]byte, error) {
	usernameToken, err := randomToken(12)
	if err != nil {
		return nil, err
	}
	password, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	base, err := randomToken(12)
	if err != nil {
		return nil, err
	}
	username := "xui-" + usernameToken
	if err = database.InitDB(path); err != nil {
		return nil, err
	}
	defer database.CloseDB()
	if err = (&service.UserService{}).UpdateFirstUser(username, password); err != nil {
		return nil, err
	}
	settings := &service.SettingService{}
	if err = settings.SetPort(port); err != nil {
		return nil, err
	}
	if err = settings.SetBasePath("/" + base + "/"); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("username: %s\npassword: %s\nport: %d\nwebBasePath: /%s/\n", username, password, port, base)), nil
}

func run() error {
	packageDir := flag.String("package", "", "local package directory created by deployment/build-local.sh")
	install := flag.Bool("install", false, "create a new systemd installation")
	recoverUpgrade := flag.Bool("recover", false, "recover an interrupted installation or upgrade")
	port := flag.Int("port", 2053, "panel port for a new installation")
	flag.Parse()
	if flag.NArg() != 0 || (*recoverUpgrade && (*install || *packageDir != "")) || (!*recoverUpgrade && *packageDir == "") {
		return errors.New("usage: xui-upgrade -package /path/to/package [-install] or xui-upgrade -recover")
	}
	if os.Geteuid() != 0 {
		return errors.New("run as root")
	}
	lock, err := deployment.Lock("/run/lock/x-ui-upgrade.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	system := deployment.Systemd{Path: "/usr/bin/systemctl"}
	journalPath := "/var/lib/x-ui/upgrade-transaction.json"
	installJournal := "/var/lib/x-ui/install-transaction.json"
	options := deployment.UpgradeOptions{InstallDir: "/usr/local/x-ui", UnitPath: "/etc/systemd/system/x-ui.service", CommandPath: "/usr/bin/x-ui", DatabasePath: "/etc/x-ui/x-ui.db", BackupDir: "/var/backups/x-ui", JournalPath: journalPath}
	options.RecoveryHealthCheck = func() error { return deployment.CheckPanelHealth("/etc/x-ui/x-ui.db", 20*time.Second) }
	options.OnRecoverySnapshot = func(path string) { fmt.Fprintln(os.Stderr, "Database preserved before recovery:", path) }
	options.OnRetainedFiles = func(path string) { fmt.Fprintln(os.Stderr, "New installation files retained:", path) }
	installOptions := deployment.InstallOptions{InstallDir: "/usr/local/x-ui", UnitPath: "/etc/systemd/system/x-ui.service", CommandPath: "/usr/bin/x-ui", DatabasePath: "/etc/x-ui/x-ui.db", CredentialsPath: "/etc/x-ui/initial-credentials", JournalPath: installJournal, BackupDir: "/var/backups/x-ui", OnRecoverySnapshot: options.OnRecoverySnapshot, OnRetainedFiles: options.OnRetainedFiles}
	pending := func(p string) (bool, error) {
		_, e := os.Lstat(p)
		if e == nil {
			return true, nil
		}
		if os.IsNotExist(e) {
			return false, nil
		}
		return false, e
	}
	upgradePending, err := pending(journalPath)
	if err != nil {
		return err
	}
	installPending, err := pending(installJournal)
	if err != nil {
		return err
	}
	if upgradePending && installPending {
		return errors.New("multiple pending transactions require manual review")
	}
	if *recoverUpgrade {
		if upgradePending {
			return deployment.RecoverUpgrade(options, system)
		}
		if installPending {
			return deployment.RecoverInstall(installOptions, system)
		}
		return errors.New("no pending installation or upgrade transaction")
	}
	if upgradePending || installPending {
		return errors.New("pending installation or upgrade transaction; run xui-upgrade -recover first")
	}
	path, err := filepath.Abs(*packageDir)
	if err != nil {
		return err
	}
	if *install {
		if *port < 1 || *port > 65535 {
			return errors.New("invalid panel port")
		}
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
		if err != nil {
			return fmt.Errorf("panel port unavailable: %w", err)
		}
		_ = listener.Close()
		credentialsPath := "/etc/x-ui/initial-credentials"
		installOptions.PackageDir = path
		installOptions.ValidatePackage = validatePackage
		installOptions.PrepareDatabase = func(db string) ([]byte, error) { return prepareDatabase(db, *port) }
		installOptions.HealthCheck = func() error { return deployment.CheckPanelHealth("/etc/x-ui/x-ui.db", 20*time.Second) }
		err = deployment.Install(installOptions, system)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Initial credentials:", credentialsPath)
		return nil
	}
	options.PackageDir = path
	options.ValidatePackage = validatePackage
	options.ValidateExisting = func(unit []byte) error {
		if err := deployment.ValidateStandardUnit(unit); err != nil {
			return err
		}
		return system.ValidateStandardUpgrade()
	}
	options.HealthCheck = func() error { return deployment.CheckPanelHealth("/etc/x-ui/x-ui.db", 20*time.Second) }
	result, err := deployment.Upgrade(options, system)
	if result.PreviousDir != "" {
		fmt.Fprintln(os.Stderr, "Previous installation:", result.PreviousDir)
	}
	if result.DatabaseSnapshot != "" {
		fmt.Fprintln(os.Stderr, "Database snapshot:", result.DatabaseSnapshot)
	}
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Operation failed:", err)
		os.Exit(1)
	}
	fmt.Println("Operation completed")
}

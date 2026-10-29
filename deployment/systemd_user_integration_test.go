//go:build linux

package deployment

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Uses a uniquely named runtime-only user service; it never touches the
// system x-ui unit. Enable explicitly on hosts with a running user manager.
func TestRealUserSystemdAdapter(t *testing.T) {
	if os.Getenv("XUI_SYSTEMD_INTEGRATION") != "1" {
		t.Skip("set XUI_SYSTEMD_INTEGRATION=1")
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "codex-xui-test-" + hex.EncodeToString(suffix[:]) + ".service"
	dir := t.TempDir()
	unit := filepath.Join(dir, name)
	if err := os.WriteFile(unit, []byte("[Unit]\nDescription=Isolated XUI adapter test\n[Service]\nType=simple\nExecStart=/usr/bin/sleep 90\n[Install]\nWantedBy=default.target\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl: %w: %s", err, out)
		}
		return nil
	}
	if err := run("link", "--runtime", unit); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run("stop", name); _ = run("disable", "--runtime", name); _ = run("daemon-reload") })
	shim := filepath.Join(dir, "systemctl-shim")
	// The adapter uses a fixed unit name; substitute it without evaluating shell input.
	script := "#!/bin/sh\ncase \"$1\" in show) exec /usr/bin/systemctl --user show --property=ActiveState --value '" + name + "';; stop|start) exec /usr/bin/systemctl --user \"$1\" '" + name + "';; daemon-reload) exec /usr/bin/systemctl --user daemon-reload;; *) exit 2;; esac\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	svc := Systemd{Path: shim}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if active, err := svc.Active(); err != nil || !active {
		t.Fatalf("service not active: %v", err)
	}
	if err := svc.Stop(); err != nil {
		t.Fatal(err)
	}
	if active, err := svc.Active(); err != nil || active {
		t.Fatalf("service not stopped: %v", err)
	}
}

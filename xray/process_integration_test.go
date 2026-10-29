package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// XRAY_TEST_BINARY selects an official Xray executable for this integration test.
func TestCoreValidationAndLifecycle(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("XRAY_TEST_BINARY not set")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", dir)
	if err = os.Symlink(binary, filepath.Join(dir, GetBinaryName())); err != nil {
		t.Fatal(err)
	}
	var config Config
	raw := `{"api":{"tag":"api","services":[]},"inbounds":[],"outbounds":[{"protocol":"freedom","settings":{},"tag":"direct"}]}`
	if err = json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if err = ValidateConfig(&config); err != nil {
		t.Fatal(err)
	}
	invalid := config
	invalid.OutboundConfigs = append([]OutboundConfig(nil), config.OutboundConfigs...)
	invalid.OutboundConfigs[0].Protocol = "nonexistent"
	if err = ValidateConfig(&invalid); err == nil {
		t.Fatal("invalid core configuration accepted")
	}
	p := NewProcess(&config)
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	if !p.IsRunning() {
		t.Fatal("not running immediately after start")
	}
	if err = p.Stop(); err != nil {
		t.Fatal(err)
	}
	if p.IsRunning() {
		t.Fatal("still running after stop")
	}
	if err = p.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err = p.Stop(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("config permissions: %o", info.Mode().Perm())
		}
	}
}

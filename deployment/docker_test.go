package deployment

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDockerInitializationStopsOnUnsupportedArchitectureAndDownloadFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	script, err := filepath.Abs("../DockerInitFiles.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "wget"), []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "unzip"), []byte("#!/bin/sh\necho unexpected-unzip >&2\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ arch, expected string }{{"unsupported", "Unsupported target architecture"}, {"amd64", ""}} {
		t.Run(tc.arch, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", script, tc.arch)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
			output, err := cmd.CombinedOutput()
			if err == nil || (tc.expected != "" && !strings.Contains(string(output), tc.expected)) || strings.Contains(string(output), "unexpected-unzip") {
				t.Fatalf("output %q error %v", output, err)
			}
		})
	}
}

func TestLocalPackagingAndUpstreamInstallerFailSafely(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	marker := filepath.Join(output, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "deployment/build-local.sh"), output, "/missing/xray", "/missing/assets")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "Output already exists") {
		t.Fatalf("build should refuse overwrite: %q %v", out, err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("output damaged: %q %v", got, err)
	}
	cmd = exec.Command("bash", filepath.Join(root, "install.sh"))
	cmd.Env = append(os.Environ(), "XUI_USE_UPSTREAM_INSTALLER=0")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "downloads the upstream panel") {
		t.Fatalf("upstream installer guard: %q %v", out, err)
	}
}

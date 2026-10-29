//go:build linux

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackageManifestRejectsChangedMissingAndExtraFiles(t *testing.T) {
	for _, mode := range []string{"valid", "installer-valid", "installer-changed", "changed", "missing", "extra", "duplicate", "traversal"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			files := []string{"x-ui", "x-ui.sh", "x-ui.service", "xui-porthop", "xui-db-snapshot", "xui-upgrade", "xui-manage-local", "LICENSE", "CLOUDFLARE_TUNNEL.md", "PORTHOP.md", "XRAY_VERSION.txt", "bin/xray-linux-" + runtime.GOARCH, "bin/geoip.dat", "bin/geosite.dat"}
			if mode == "installer-valid" || mode == "installer-changed" {
				files = append(files, "install.sh")
			}
			var manifest strings.Builder
			for _, name := range files {
				data := []byte("test:" + name)
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(data)
				fmt.Fprintf(&manifest, "%x  %s\n", sum, name)
			}
			switch mode {
			case "installer-changed":
				if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("modified"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed":
				if err := os.WriteFile(filepath.Join(dir, "x-ui"), []byte("replaced"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(filepath.Join(dir, "bin/geoip.dat")); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(filepath.Join(dir, "unexpected"), []byte("extra"), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				sum := sha256.Sum256([]byte("test:x-ui"))
				fmt.Fprintf(&manifest, "%x  x-ui\n", sum)
			case "traversal":
				sum := sha256.Sum256([]byte("bad"))
				fmt.Fprintf(&manifest, "%x  ../outside\n", sum)
			}
			if err := os.WriteFile(filepath.Join(dir, "MANIFEST.sha256"), []byte(manifest.String()), 0600); err != nil {
				t.Fatal(err)
			}
			err := verifyPackageManifest(dir)
			if (err != nil) != (mode != "valid" && mode != "installer-valid") {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestPackageValidationRejectsChangesDuringVersionCheck(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	files := []string{"x-ui", "x-ui.sh", "x-ui.service", "xui-porthop", "xui-db-snapshot", "xui-upgrade", "xui-manage-local", "LICENSE", "CLOUDFLARE_TUNNEL.md", "PORTHOP.md", "XRAY_VERSION.txt", "bin/xray-linux-" + runtime.GOARCH, "bin/geoip.dat", "bin/geosite.dat"}
	unit, err := os.ReadFile("../../x-ui.service")
	if err != nil {
		t.Fatal(err)
	}
	var manifest strings.Builder
	for _, name := range files {
		data := []byte("test:" + name)
		switch name {
		case "x-ui":
			data = []byte("#!/bin/sh\nprintf changed >> \"$(dirname \"$0\")/PORTHOP.md\"\nprintf 'panel version\\n'\n")
		case "x-ui.service":
			data = unit
		case "bin/xray-linux-" + runtime.GOARCH:
			data = []byte("#!/bin/sh\nprintf 'Xray test\\n'\n")
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0755); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&manifest, "%x  %s\n", sum, name)
	}
	if err := os.WriteFile(filepath.Join(dir, "MANIFEST.sha256"), []byte(manifest.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePackage(dir); err == nil || !strings.Contains(err.Error(), "after version checks") {
		t.Fatalf("package mutation was not rejected: %v", err)
	}
}

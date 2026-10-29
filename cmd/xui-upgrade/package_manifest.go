//go:build linux

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The manifest detects incomplete or accidentally modified local packages. It
// is not a signature and does not authenticate an untrusted package source.
func verifyPackageManifest(dir string) error {
	required := []string{"x-ui", "x-ui.sh", "x-ui.service", "xui-porthop", "xui-db-snapshot", "xui-upgrade", "xui-manage-local", "LICENSE", "CLOUDFLARE_TUNNEL.md", "PORTHOP.md", "XRAY_VERSION.txt", "bin/xray-linux-" + runtime.GOARCH, "bin/geoip.dat", "bin/geosite.dat"}
	allowed := map[string]bool{}
	for _, name := range required {
		allowed[name] = true
	}
	allowed["bin/geoip_IR.dat"] = true
	allowed["bin/geosite_IR.dat"] = true
	manifest := filepath.Join(dir, "MANIFEST.sha256")
	info, err := os.Lstat(manifest)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return errors.New("invalid package manifest")
	}
	f, err := os.Open(manifest)
	if err != nil {
		return err
	}
	defer f.Close()
	listed := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 67 || line[64:66] != "  " {
			return errors.New("invalid manifest entry")
		}
		digest, name := line[:64], line[66:]
		decoded, e := hex.DecodeString(digest)
		if e != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) {
			return errors.New("invalid manifest checksum")
		}
		if !allowed[name] || listed[name] != "" {
			return fmt.Errorf("unexpected or duplicate manifest entry %q", name)
		}
		listed[name] = digest
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	for _, name := range required {
		if listed[name] == "" {
			return fmt.Errorf("missing manifest entry %q", name)
		}
	}
	for name, expected := range listed {
		path := filepath.Join(dir, filepath.FromSlash(name))
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("missing or invalid package file %q", name)
		}
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		opened, e := file.Stat()
		if e != nil || !os.SameFile(info, opened) {
			file.Close()
			return fmt.Errorf("package file changed: %q", name)
		}
		hash := sha256.New()
		_, e = io.Copy(hash, file)
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected {
			return fmt.Errorf("checksum mismatch for %q", name)
		}
	}
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if rel == "MANIFEST.sha256" {
			return nil
		}
		if listed[rel] == "" {
			return fmt.Errorf("unlisted package file %q", rel)
		}
		return nil
	})
}

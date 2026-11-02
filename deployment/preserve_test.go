package deployment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreserveExistingFilesRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	stage := filepath.Join(root, "stage")
	for _, dir := range []string{old, stage} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(old, "certificate")); err != nil {
		t.Fatal(err)
	}
	if err := validateExistingTree(old); err == nil {
		t.Fatal("symlink accepted in preflight")
	}
	if err := preserveExistingFiles(old, stage); err == nil {
		t.Fatal("symlink accepted during staging")
	}
}

func TestPreservedFilesAreIndependent(t *testing.T) {
	root := t.TempDir()
	old, stage := filepath.Join(root, "old"), filepath.Join(root, "stage")
	for _, dir := range []string{filepath.Join(old, "bin"), filepath.Join(stage, "bin")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(old, "bin", "geoip.dat"), filepath.Join(old, "certificate.pem"), filepath.Join(stage, "bin", "geoip.dat")} {
		if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := preserveExistingFiles(old, stage); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bin/geoip.dat", "certificate.pem"} {
		if err := os.WriteFile(filepath.Join(stage, name), []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(old, name))
		if err != nil || string(data) != "original" {
			t.Fatalf("backup file %s changed: %q %v", name, data, err)
		}
	}
}

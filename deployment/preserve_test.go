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

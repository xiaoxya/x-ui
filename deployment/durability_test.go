package deployment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDurableDirectoriesRejectSymlinkAndFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "one", "two")
	if err := mkdirDurable(path, 0700); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
	if err := mkdirDurable(path, 0700); err != nil {
		t.Fatalf("existing directory: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := mkdirDurable(link, 0700); err == nil {
		t.Fatal("symlink accepted as transaction directory")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mkdirDurable(file, 0700); err == nil {
		t.Fatal("file accepted as directory")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
}

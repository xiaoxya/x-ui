//go:build linux

package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOldIdentityAcrossDeviceChange(t *testing.T) {
	dir := t.TempDir()
	marker := strings.Repeat("a", 64)
	dev, ino, err := directoryIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeOldIdentityMarker(dir, marker); err != nil {
		t.Fatal(err)
	}
	if match, err := matchesOldDirectory(dir, dev^1, ino, marker); err != nil || !match {
		t.Fatalf("device change: %v %v", match, err)
	}
	if match, err := matchesOldDirectory(dir, dev^1, ino+1, marker); err != nil || match {
		t.Fatalf("inode change accepted: %v %v", match, err)
	}
	path, err := oldIdentityMarkerPath(dir, marker)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(dir, "elsewhere"), path); err != nil {
		t.Fatal(err)
	}
	if match, err := matchesOldDirectory(dir, dev^1, ino, marker); err == nil || match {
		t.Fatalf("invalid marker accepted: %v %v", match, err)
	}
	// Existing transactions without the added marker remain recoverable while
	// their original device and inode still match.
	if match, err := matchesOldDirectory(dir, dev, ino, marker); err != nil || !match {
		t.Fatalf("matching device: %v %v", match, err)
	}
}

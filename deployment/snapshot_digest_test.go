package deployment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageVerifiedSnapshotDoesNotOverwriteDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "snapshot")
	destination := filepath.Join(dir, "database")
	if err := os.WriteFile(source, []byte("original backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("live data"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := snapshotDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := stageVerifiedSnapshot(source, destination, digest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "live data" {
		t.Fatalf("destination changed before commit: %q %v", data, err)
	}
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("changed backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := stageVerifiedSnapshot(source, destination, digest); err == nil {
		t.Fatal("changed backup accepted")
	}
	data, err = os.ReadFile(destination)
	if err != nil || string(data) != "live data" {
		t.Fatalf("destination changed on validation failure: %q %v", data, err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".x-ui-restore-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary file retained: %v %v", matches, err)
	}
}

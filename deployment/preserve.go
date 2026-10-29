package deployment

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Reject objects whose target or behavior could change while staging a package.
func validateExistingTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, path)
			return fmt.Errorf("unsupported file %s", rel)
		}
		return nil
	})
}

// Keep local files absent from the new package and panel-managed Xray data
// files. Staging is on the same filesystem as the old installation, so links
// preserve their mode, owner and contents without copying mutable state.
func preserveExistingFiles(source, stage string) error {
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file %s", rel)
		}
		target := filepath.Join(stage, rel)
		existing, e := os.Lstat(target)
		if entry.IsDir() {
			if os.IsNotExist(e) {
				return os.Mkdir(target, info.Mode().Perm())
			}
			if e != nil {
				return e
			}
			if !existing.IsDir() {
				return fmt.Errorf("file type conflict: %s", rel)
			}
			return nil
		}
		if e == nil {
			if !existing.Mode().IsRegular() {
				return fmt.Errorf("file type conflict: %s", rel)
			}
			// Geodata is managed separately by the panel and may be customized.
			if filepath.Dir(rel) != "bin" || !strings.HasSuffix(strings.ToLower(rel), ".dat") {
				return nil
			}
			if err = os.Remove(target); err != nil {
				return err
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		return os.Link(path, target)
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(stage, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return syncDir(path)
		}
		return nil
	})
}

package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func snapshotDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("snapshot is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", errors.New("snapshot changed during inspection")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// stageVerifiedSnapshot hashes the exact bytes copied to a private temporary
// file. It leaves the destination and its SQLite sidecars untouched on error.
func stageVerifiedSnapshot(source, destination, expected string) (string, error) {
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("snapshot checksum is missing or invalid; manual review required")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("snapshot is not a regular file")
	}
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", errors.New("snapshot changed during inspection")
	}
	out, err := os.CreateTemp(filepath.Dir(destination), ".x-ui-restore-")
	if err != nil {
		return "", err
	}
	path := out.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(path)
		}
	}()
	if err = out.Chmod(0600); err != nil {
		_ = out.Close()
		return "", err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, hash), in)
	if err == nil && hex.EncodeToString(hash.Sum(nil)) != expected {
		err = errors.New("snapshot checksum mismatch; manual review required")
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = syncDir(filepath.Dir(destination)); err != nil {
		return "", err
	}
	success = true
	return path, nil
}

func commitVerifiedSnapshot(staged, destination string) error {
	if err := os.Rename(staged, destination); err != nil {
		return err
	}
	return syncDir(filepath.Dir(destination))
}

func verifySnapshot(path, expected string) error {
	if len(expected) != sha256.Size*2 {
		return errors.New("snapshot checksum is missing or invalid; manual review required")
	}
	actual, err := snapshotDigest(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("snapshot checksum mismatch; manual review required")
	}
	return nil
}

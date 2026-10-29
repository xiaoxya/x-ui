package deployment

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const upgradeMarkerName = ".xui-upgrade-marker"
const oldIdentityPrefix = ".xui-old-identity-"

func oldIdentityMarkerPath(dir, marker string) (string, error) {
	decoded, err := hex.DecodeString(marker)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid transaction marker")
	}
	return filepath.Join(dir, oldIdentityPrefix+marker), nil
}

func writeOldIdentityMarker(dir, marker string) error {
	path, err := oldIdentityMarkerPath(dir, marker)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(marker)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	return syncDir(dir)
}

// Filesystem device numbers can change on reboot. A persistent random marker
// plus the original inode identifies the renamed directory in that case.
func matchesOldDirectory(path string, device, inode uint64, marker string) (bool, error) {
	actualDevice, actualInode, err := directoryIdentity(path)
	if err != nil {
		return false, err
	}
	if actualInode != inode {
		return false, nil
	}
	if actualDevice == device {
		return true, nil
	}
	markerPath, err := oldIdentityMarkerPath(path, marker)
	if err != nil {
		return false, err
	}
	content, _, err := readRegular(markerPath)
	if err != nil {
		return false, err
	}
	return string(content) == marker, nil
}

func verifyUpgradeStage(path, marker string) (bool, error) {
	return verifyMarkedDirectory(path, upgradeMarkerName, marker)
}

func verifyMarkedDirectory(path, markerName, marker string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || len(marker) != 64 {
		return false, errors.New("unrecognized installation directory")
	}
	data, _, err := readRegular(filepath.Join(path, markerName))
	if err != nil || string(data) != marker {
		return false, errors.New("installation directory no longer belongs to this transaction")
	}
	return true, nil
}

func retainFailedInstall(path, marker string) (string, error) {
	return retainMarkedDirectory(path, upgradeMarkerName, marker)
}

func retainMarkedDirectory(path, markerName, marker string) (string, error) {
	exists, err := verifyMarkedDirectory(path, markerName, marker)
	if err != nil || !exists {
		return "", err
	}
	holder, err := os.MkdirTemp(filepath.Dir(path), ".x-ui-failed-")
	if err != nil {
		return "", err
	}
	if err = renameNoReplace(path, filepath.Join(holder, "x-ui")); err != nil {
		_ = os.Remove(holder)
		return "", err
	}
	if err = syncDir(holder); err != nil {
		return holder, fmt.Errorf("sync retained installation: %w", err)
	}
	if err = syncDir(filepath.Dir(path)); err != nil {
		return holder, fmt.Errorf("sync retained installation parent: %w", err)
	}
	return holder, nil
}

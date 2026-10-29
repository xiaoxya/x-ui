package deployment

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type upgradeJournal struct {
	Phase               string `json:"phase"`
	WasActive           bool   `json:"wasActive"`
	PreviousDir         string `json:"previousDir,omitempty"`
	Snapshot            string `json:"snapshot,omitempty"`
	SnapshotSHA256      string `json:"snapshotSha256,omitempty"`
	UnitMode            uint32 `json:"unitMode,omitempty"`
	CommandMode         uint32 `json:"commandMode,omitempty"`
	UnitSHA256          string `json:"unitSha256,omitempty"`
	CommandSHA256       string `json:"commandSha256,omitempty"`
	Marker              string `json:"marker,omitempty"`
	OldDevice           uint64 `json:"oldDevice,omitempty"`
	OldInode            uint64 `json:"oldInode,omitempty"`
	CheckRestoredHealth bool   `json:"checkRestoredHealth,omitempty"`
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Create each missing directory and persist its entry before creating files
// beneath it. Refuse symlinks for transaction and backup directories.
func mkdirDurable(path string, mode os.FileMode) error {
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("not a real directory: %s", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return err
	}
	if err = mkdirDurable(parent, mode); err != nil {
		return err
	}
	if err = os.Mkdir(path, mode); err != nil {
		if !os.IsExist(err) {
			return err
		}
		info, err = os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("not a real directory: %s", path)
		}
	}
	return syncDir(parent)
}
func writeJournal(path string, j upgradeJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err = atomicWrite(path, data, 0600); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func removeJournal(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func recordRecoverySnapshot(dir, path string) error {
	f, err := os.CreateTemp(dir, ".interrupted-snapshot-*.partial")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(path + "\n")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(temporary), "."), ".partial") + ".path"
	if err = renameNoReplace(temporary, filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDir(dir)
}

// RecoverUpgrade restores the pre-upgrade state using a durable journal. The
// caller must hold the same exclusive lock as Upgrade.
func RecoverUpgrade(o UpgradeOptions, system ServiceManager) error {
	if !filepath.IsAbs(o.JournalPath) {
		return errors.New("absolute journal path required")
	}
	data, _, err := readRegular(o.JournalPath)
	if err != nil {
		return err
	}
	var j upgradeJournal
	if err = json.Unmarshal(data, &j); err != nil {
		return fmt.Errorf("invalid upgrade journal: %w", err)
	}
	if j.Phase == "committed" {
		return removeJournal(o.JournalPath)
	}
	if j.Phase == "restored" {
		active, e := system.Active()
		if e != nil {
			return e
		}
		if j.WasActive {
			if !active {
				if e = system.Start(); e != nil {
					return e
				}
			}
			if j.CheckRestoredHealth && o.RecoveryHealthCheck != nil {
				if e = o.RecoveryHealthCheck(); e != nil {
					return fmt.Errorf("previous service health: %w", e)
				}
			}
		} else if active {
			if e = system.Stop(); e != nil {
				return fmt.Errorf("stop previously inactive service: %w", e)
			}
			active, e = system.Active()
			if e != nil {
				return fmt.Errorf("verify previously inactive service: %w", e)
			}
			if active {
				return errors.New("previously inactive service is still active")
			}
		}
		return removeJournal(o.JournalPath)
	}
	if j.Phase != "preparing" && j.Phase != "switching" {
		return errors.New("unknown upgrade journal phase")
	}
	if j.Phase == "switching" {
		previous := filepath.Clean(j.PreviousDir)
		if filepath.Dir(previous) != filepath.Dir(filepath.Clean(o.InstallDir)) || !strings.HasPrefix(filepath.Base(previous), ".x-ui-previous-") {
			return errors.New("unsafe previous installation path")
		}
		if filepath.Base(j.Snapshot) != "x-ui.db" || filepath.Dir(filepath.Dir(filepath.Clean(j.Snapshot))) != filepath.Clean(o.BackupDir) || !strings.HasPrefix(filepath.Base(filepath.Dir(j.Snapshot)), "xui-db-") {
			return errors.New("unsafe database snapshot path")
		}
		if info, e := os.Lstat(previous); e != nil || !info.IsDir() {
			return errors.New("previous installation directory is unavailable")
		}
		if info, e := os.Lstat(j.Snapshot); e != nil || !info.Mode().IsRegular() {
			return errors.New("database snapshot is unavailable")
		}
		if err = verifySnapshot(j.Snapshot, j.SnapshotSHA256); err != nil {
			return fmt.Errorf("verify upgrade snapshot: %w", err)
		}
		if _, _, err = readRegular(filepath.Join(previous, "x-ui.service")); err != nil {
			return err
		}
		if _, _, err = readRegular(filepath.Join(previous, "x-ui-command")); err != nil {
			return err
		}
		if err = verifySnapshot(filepath.Join(previous, "x-ui.service"), j.UnitSHA256); err != nil {
			return fmt.Errorf("verify previous service unit: %w", err)
		}
		if err = verifySnapshot(filepath.Join(previous, "x-ui-command"), j.CommandSHA256); err != nil {
			return fmt.Errorf("verify previous management command: %w", err)
		}
		if j.OldInode == 0 || len(j.Marker) != 64 {
			return errors.New("missing upgrade directory identity; manual review required")
		}
		old := filepath.Join(previous, "x-ui")
		matches, e := matchesOldDirectory(old, j.OldDevice, j.OldInode, j.Marker)
		if os.IsNotExist(e) {
			matches, e = matchesOldDirectory(o.InstallDir, j.OldDevice, j.OldInode, j.Marker)
			if e != nil || !matches {
				return errors.New("previous installation identity changed")
			}
		} else {
			if e != nil || !matches {
				return errors.New("previous installation identity changed")
			}
			if _, e = verifyUpgradeStage(o.InstallDir, j.Marker); e != nil {
				return e
			}
		}
	}
	if err = system.Stop(); err != nil {
		return fmt.Errorf("stop service for recovery: %w", err)
	}
	if j.Phase == "switching" {
		// The service may have restarted after the interrupted installer died.
		// Preserve any subsequent writes before restoring the old snapshot.
		if _, e := os.Lstat(o.DatabasePath); e == nil {
			current, e := SnapshotDatabase(o.DatabasePath, o.BackupDir)
			if e != nil {
				return fmt.Errorf("preserve interrupted database: %w", e)
			}
			if e = recordRecoverySnapshot(j.PreviousDir, current); e != nil {
				return e
			}
			if o.OnRecoverySnapshot != nil {
				o.OnRecoverySnapshot(current)
			}
		} else if !os.IsNotExist(e) {
			return e
		} else {
			for _, suffix := range sqliteSidecarSuffixes {
				if _, e := os.Lstat(o.DatabasePath + suffix); e == nil {
					return errors.New("database missing but SQLite sidecar remains")
				} else if !os.IsNotExist(e) {
					return e
				}
			}
		}
		staged, e := stageVerifiedSnapshot(j.Snapshot, o.DatabasePath, j.SnapshotSHA256)
		if e != nil {
			return fmt.Errorf("stage recovery snapshot: %w", e)
		}
		defer os.Remove(staged)
		for _, suffix := range sqliteSidecarSuffixes {
			if e := os.Remove(o.DatabasePath + suffix); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		if o.afterRecoverySidecars != nil {
			o.afterRecoverySidecars()
		}
		if err = commitVerifiedSnapshot(staged, o.DatabasePath); err != nil {
			return fmt.Errorf("restore database: %w", err)
		}
		old := filepath.Join(j.PreviousDir, "x-ui")
		if info, e := os.Lstat(old); e == nil {
			if !info.IsDir() {
				return errors.New("previous installation is not a directory")
			}
			retained, e := retainFailedInstall(o.InstallDir, j.Marker)
			if retained != "" && o.OnRetainedFiles != nil {
				o.OnRetainedFiles(retained)
			}
			if e != nil {
				return e
			}
			if err = os.Rename(old, o.InstallDir); err != nil {
				return err
			}
			if err = syncDir(filepath.Dir(o.InstallDir)); err != nil {
				return err
			}
			if err = syncDir(j.PreviousDir); err != nil {
				return err
			}
		} else if !os.IsNotExist(e) {
			return e
		} else if info, e = os.Lstat(o.InstallDir); e != nil || !info.IsDir() {
			return errors.New("previous installation cannot be located")
		}
		unit, _, e := readRegular(filepath.Join(j.PreviousDir, "x-ui.service"))
		if e != nil {
			return e
		}
		command, _, e := readRegular(filepath.Join(j.PreviousDir, "x-ui-command"))
		if e != nil {
			return e
		}
		if err = atomicWrite(o.UnitPath, unit, os.FileMode(j.UnitMode)&0777); err != nil {
			return err
		}
		if err = atomicWrite(o.CommandPath, command, os.FileMode(j.CommandMode)&0777); err != nil {
			return err
		}
		if err = system.Reload(); err != nil {
			return err
		}
	}
	j.CheckRestoredHealth = j.Phase == "switching" && j.WasActive
	j.Phase = "restored"
	if err = writeJournal(o.JournalPath, j); err != nil {
		return err
	}
	if j.WasActive {
		if err = system.Start(); err != nil {
			return fmt.Errorf("restart previous service: %w", err)
		}
		if j.CheckRestoredHealth && o.RecoveryHealthCheck != nil {
			if err = o.RecoveryHealthCheck(); err != nil {
				return fmt.Errorf("previous service health: %w", err)
			}
		}
	}
	return removeJournal(o.JournalPath)
}

package deployment

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ServiceManager abstracts systemd so upgrade and rollback can be tested
// without changing the host's service state.
type ServiceManager interface {
	Active() (bool, error)
	Stop() error
	Start() error
	Reload() error
}

type UpgradeOptions struct {
	PackageDir            string
	InstallDir            string
	UnitPath              string
	CommandPath           string
	DatabasePath          string
	BackupDir             string
	ValidatePackage       func(string) error
	ValidateExisting      func([]byte) error
	HealthCheck           func() error
	RecoveryHealthCheck   func() error
	JournalPath           string
	OnRecoverySnapshot    func(string)
	OnRetainedFiles       func(string)
	afterRecoverySidecars func() // fault injection for recovery tests
}

type UpgradeResult struct {
	PreviousDir      string
	DatabaseSnapshot string
	RetainedDir      string
}

// Upgrade replaces an existing installation. The caller must hold an exclusive
// installation lock and ensure no other process modifies its database.
func Upgrade(o UpgradeOptions, system ServiceManager) (result UpgradeResult, err error) {
	for _, p := range []string{o.PackageDir, o.InstallDir, o.UnitPath, o.CommandPath, o.DatabasePath, o.BackupDir} {
		if !filepath.IsAbs(p) {
			return result, errors.New("upgrade paths must be absolute")
		}
	}
	installDir := filepath.Clean(o.InstallDir)
	packageDir := filepath.Clean(o.PackageDir)
	if packageDir == installDir || strings.HasPrefix(packageDir, installDir+string(os.PathSeparator)) {
		return result, errors.New("package cannot be inside current installation")
	}
	if info, e := os.Lstat(packageDir); e != nil || !info.IsDir() {
		return result, errors.New("package must be a real directory")
	}
	for _, path := range []string{o.DatabasePath, o.BackupDir, o.UnitPath, o.CommandPath} {
		clean := filepath.Clean(path)
		if clean == installDir || strings.HasPrefix(clean, installDir+string(os.PathSeparator)) {
			return result, errors.New("database, backup, unit and command paths must be outside the installation")
		}
	}
	if info, e := os.Lstat(installDir); e != nil || !info.IsDir() {
		return result, errors.New("existing installation directory is required")
	}
	if err = validateExistingTree(installDir); err != nil {
		return result, fmt.Errorf("existing installation: %w", err)
	}
	if info, e := os.Lstat(o.DatabasePath); e != nil || !info.Mode().IsRegular() {
		return result, errors.New("existing regular database file is required")
	}
	for _, name := range []string{"x-ui", "x-ui.sh", "x-ui.service", "bin/geoip.dat", "bin/geosite.dat"} {
		info, e := os.Lstat(filepath.Join(packageDir, name))
		if e != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return result, fmt.Errorf("invalid package file %s", name)
		}
	}
	stage, err := os.MkdirTemp(filepath.Dir(installDir), ".x-ui-stage-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	if err = copyTree(packageDir, stage); err != nil {
		return result, fmt.Errorf("prepare package: %w", err)
	}
	if o.ValidatePackage != nil {
		if err = o.ValidatePackage(stage); err != nil {
			return result, fmt.Errorf("validate staged package: %w", err)
		}
	}
	oldUnit, unitMode, err := readRegular(o.UnitPath)
	if err != nil {
		return result, fmt.Errorf("read previous service unit: %w", err)
	}
	if o.ValidateExisting != nil {
		if err = o.ValidateExisting(oldUnit); err != nil {
			return result, fmt.Errorf("existing service preflight: %w", err)
		}
	}
	oldCommand, commandMode, err := readRegular(o.CommandPath)
	if err != nil {
		return result, fmt.Errorf("read previous management command: %w", err)
	}
	wasActive, err := system.Active()
	if err != nil {
		return result, fmt.Errorf("check service: %w", err)
	}
	if o.ValidateExisting != nil {
		if err = o.ValidateExisting(oldUnit); err != nil {
			return result, fmt.Errorf("recheck existing service: %w", err)
		}
	}
	if active, e := system.Active(); e != nil {
		return result, fmt.Errorf("recheck service state: %w", e)
	} else if active != wasActive {
		return result, errors.New("service state changed during upgrade preflight")
	}
	journal := upgradeJournal{Phase: "preparing", WasActive: wasActive}
	if o.JournalPath != "" {
		if !filepath.IsAbs(o.JournalPath) {
			return result, errors.New("absolute journal path required")
		}
		if err = mkdirDurable(filepath.Dir(o.JournalPath), 0700); err != nil {
			return result, err
		}
		if _, err = os.Lstat(o.JournalPath); err == nil {
			return result, errors.New("pending upgrade transaction; run recovery first")
		} else if !os.IsNotExist(err) {
			return result, err
		}
		if err = writeJournal(o.JournalPath, journal); err != nil {
			return result, err
		}
	}
	if wasActive {
		if err = system.Stop(); err != nil {
			return result, fmt.Errorf("stop service: %w", err)
		}
	}
	stopped := wasActive
	mutated := false
	committed := false
	newServiceAttempted := false
	var originalSnapshotDigest string
	var marker string
	var oldDevice, oldInode uint64
	defer func() {
		if err == nil || committed {
			return
		}
		var rollback []error
		if mutated {
			if e := system.Stop(); e != nil {
				rollback = append(rollback, fmt.Errorf("stop new service: %w", e))
				err = errors.Join(err, errors.Join(rollback...))
				return
			}
			dev, ino, e := directoryIdentity(filepath.Join(result.PreviousDir, "x-ui"))
			if e != nil || dev != oldDevice || ino != oldInode {
				err = errors.Join(err, errors.New("previous installation identity changed; rollback stopped"))
				return
			}
			retained, e := retainFailedInstall(installDir, marker)
			result.RetainedDir = retained
			if retained != "" && o.OnRetainedFiles != nil {
				o.OnRetainedFiles(retained)
			}
			if e != nil {
				err = errors.Join(err, fmt.Errorf("retain new installation: %w", e))
				return
			}
			if result.DatabaseSnapshot != "" {
				if e := verifySnapshot(result.DatabaseSnapshot, originalSnapshotDigest); e != nil {
					err = errors.Join(err, fmt.Errorf("cannot verify rollback snapshot: %w", e))
					return
				}
				if newServiceAttempted {
					current, e := SnapshotDatabase(o.DatabasePath, o.BackupDir)
					if e != nil {
						err = errors.Join(err, fmt.Errorf("cannot preserve failed-upgrade database: %w", e))
						return
					}
					if e = recordRecoverySnapshot(result.PreviousDir, current); e != nil {
						err = errors.Join(err, fmt.Errorf("cannot record failed-upgrade snapshot: %w", e))
						return
					}
					if o.OnRecoverySnapshot != nil {
						o.OnRecoverySnapshot(current)
					}
				}
				staged, e := stageVerifiedSnapshot(result.DatabaseSnapshot, o.DatabasePath, originalSnapshotDigest)
				if e != nil {
					err = errors.Join(err, fmt.Errorf("stage rollback snapshot: %w", e))
					return
				}
				defer os.Remove(staged)
				for _, suffix := range sqliteSidecarSuffixes {
					if e := os.Remove(o.DatabasePath + suffix); e != nil && !os.IsNotExist(e) {
						err = errors.Join(err, fmt.Errorf("cannot remove SQLite sidecar; rollback stopped: %w", e))
						return
					}
				}
				if e := commitVerifiedSnapshot(staged, o.DatabasePath); e != nil {
					err = errors.Join(err, fmt.Errorf("restore database; rollback stopped: %w", e))
					return
				}
			}
			if result.PreviousDir != "" {
				if e := os.Rename(filepath.Join(result.PreviousDir, "x-ui"), installDir); e != nil {
					rollback = append(rollback, fmt.Errorf("restore installation: %w", e))
				} else {
					if e = syncDir(filepath.Dir(installDir)); e != nil {
						rollback = append(rollback, e)
					}
					if e = syncDir(result.PreviousDir); e != nil {
						rollback = append(rollback, e)
					}
				}
			}
			if e := atomicWrite(o.UnitPath, oldUnit, unitMode); e != nil {
				rollback = append(rollback, e)
			}
			if e := atomicWrite(o.CommandPath, oldCommand, commandMode); e != nil {
				rollback = append(rollback, e)
			}
			if e := system.Reload(); e != nil {
				rollback = append(rollback, e)
			}
		}
		if len(rollback) == 0 && o.JournalPath != "" {
			journal.Phase = "restored"
			journal.CheckRestoredHealth = mutated && stopped
			if e := writeJournal(o.JournalPath, journal); e != nil {
				rollback = append(rollback, fmt.Errorf("record restored state: %w", e))
			}
		}
		if stopped && len(rollback) == 0 {
			if e := system.Start(); e != nil {
				rollback = append(rollback, fmt.Errorf("restart previous service: %w", e))
			} else if mutated && o.RecoveryHealthCheck != nil {
				if e := o.RecoveryHealthCheck(); e != nil {
					rollback = append(rollback, fmt.Errorf("previous service health: %w", e))
				}
			}
		}
		if len(rollback) > 0 {
			err = errors.Join(err, fmt.Errorf("rollback incomplete: %w", errors.Join(rollback...)))
		} else if o.JournalPath != "" {
			if e := removeJournal(o.JournalPath); e != nil {
				err = errors.Join(err, fmt.Errorf("remove rollback journal: %w", e))
			}
		}
	}()
	if active, e := system.Active(); e != nil {
		return result, fmt.Errorf("verify service stopped: %w", e)
	} else if active {
		return result, errors.New("service became active before database backup")
	}
	if err = preserveExistingFiles(installDir, stage); err != nil {
		return result, fmt.Errorf("preserve existing files: %w", err)
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		return result, err
	}
	marker = hex.EncodeToString(token[:])
	if err = atomicWrite(filepath.Join(stage, upgradeMarkerName), []byte(marker), 0600); err != nil {
		return result, err
	}
	oldDevice, oldInode, err = directoryIdentity(installDir)
	if err != nil {
		return result, err
	}
	result.DatabaseSnapshot, err = SnapshotDatabase(o.DatabasePath, o.BackupDir)
	if err != nil {
		return result, fmt.Errorf("backup database: %w", err)
	}
	originalSnapshotDigest, err = snapshotDigest(result.DatabaseSnapshot)
	if err != nil {
		return result, fmt.Errorf("hash backup: %w", err)
	}
	previous, e := os.MkdirTemp(filepath.Dir(installDir), ".x-ui-previous-")
	if e != nil {
		return result, e
	}
	if err = syncDir(filepath.Dir(previous)); err != nil {
		_ = os.Remove(previous)
		return result, err
	}
	if err = os.WriteFile(filepath.Join(previous, "x-ui.service"), oldUnit, unitMode); err != nil {
		_ = os.RemoveAll(previous)
		return result, err
	}
	if err = os.WriteFile(filepath.Join(previous, "x-ui-command"), oldCommand, commandMode); err != nil {
		_ = os.RemoveAll(previous)
		return result, err
	}
	if err = os.WriteFile(filepath.Join(previous, "database-snapshot-path"), []byte(result.DatabaseSnapshot+"\n"), 0600); err != nil {
		_ = os.RemoveAll(previous)
		return result, err
	}
	if o.JournalPath != "" {
		for _, path := range []string{filepath.Join(previous, "x-ui.service"), filepath.Join(previous, "x-ui-command"), filepath.Join(previous, "database-snapshot-path")} {
			f, e := os.Open(path)
			if e != nil {
				return result, e
			}
			e = f.Sync()
			_ = f.Close()
			if e != nil {
				return result, e
			}
		}
		if err = syncDir(previous); err != nil {
			return result, err
		}
		if err = writeOldIdentityMarker(installDir, marker); err != nil {
			return result, fmt.Errorf("mark previous installation: %w", err)
		}
		journal.Phase = "switching"
		journal.PreviousDir = previous
		journal.Snapshot = result.DatabaseSnapshot
		journal.SnapshotSHA256 = originalSnapshotDigest
		journal.UnitMode = uint32(unitMode)
		journal.CommandMode = uint32(commandMode)
		journal.UnitSHA256, err = snapshotDigest(filepath.Join(previous, "x-ui.service"))
		if err != nil {
			return result, err
		}
		journal.CommandSHA256, err = snapshotDigest(filepath.Join(previous, "x-ui-command"))
		if err != nil {
			return result, err
		}
		journal.Marker = marker
		journal.OldDevice = oldDevice
		journal.OldInode = oldInode
		if err = writeJournal(o.JournalPath, journal); err != nil {
			return result, err
		}
	}
	if err = os.Rename(installDir, filepath.Join(previous, "x-ui")); err != nil {
		_ = os.RemoveAll(previous)
		return result, err
	}
	result.PreviousDir = previous
	mutated = true
	if err = syncDir(filepath.Dir(installDir)); err != nil {
		return result, err
	}
	if err = syncDir(previous); err != nil {
		return result, err
	}
	if err = os.Rename(stage, installDir); err != nil {
		return result, fmt.Errorf("install package: %w", err)
	}
	if err = syncDir(filepath.Dir(installDir)); err != nil {
		return result, err
	}
	if err = atomicCopy(filepath.Join(installDir, "x-ui.service"), o.UnitPath, 0644); err != nil {
		return result, err
	}
	if err = atomicCopy(filepath.Join(installDir, "x-ui.sh"), o.CommandPath, 0755); err != nil {
		return result, err
	}
	if err = system.Reload(); err != nil {
		return result, err
	}
	if wasActive {
		newServiceAttempted = true
		if err = system.Start(); err != nil {
			return result, fmt.Errorf("start upgraded service: %w", err)
		}
		active, e := system.Active()
		if e != nil || !active {
			return result, fmt.Errorf("upgraded service is not active: %v", e)
		}
		if o.HealthCheck != nil {
			if err = o.HealthCheck(); err != nil {
				return result, fmt.Errorf("upgraded service health: %w", err)
			}
		}
	}
	if o.JournalPath != "" {
		journal.Phase = "committed"
		if err = writeJournal(o.JournalPath, journal); err != nil {
			return result, err
		}
		committed = true
		if err = removeJournal(o.JournalPath); err != nil {
			return result, fmt.Errorf("upgrade committed but journal cleanup failed: %w", err)
		}
	}
	return result, nil
}

func readRegular(path string) ([]byte, fs.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("not a regular file: %s", path)
	}
	b, err := os.ReadFile(path)
	return b, info.Mode().Perm(), err
}

func atomicCopy(source, dest string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.CreateTemp(filepath.Dir(dest), ".x-ui-write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = io.Copy(f, in)
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
	if err = os.Rename(f.Name(), dest); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dest))
}

func atomicWrite(dest string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(dest), ".x-ui-write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
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
	if err = os.Rename(f.Name(), dest); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dest))
}

func copyTree(source, dest string) error {
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
		target := filepath.Join(dest, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Mkdir(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported package entry %s", rel)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		if copyErr == nil {
			copyErr = out.Sync()
		}
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(dest, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return syncDir(path)
		}
		return nil
	})
}

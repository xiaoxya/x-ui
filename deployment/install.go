package deployment

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type FreshServiceManager interface {
	ServiceManager
	Exists() (bool, error)
	Enable() error
	Disable() error
}

type InstallOptions struct {
	PackageDir, InstallDir, UnitPath, CommandPath, DatabasePath, CredentialsPath string
	ValidatePackage                                                              func(string) error
	PrepareDatabase                                                              func(string) ([]byte, error)
	HealthCheck                                                                  func() error
	JournalPath                                                                  string
	BackupDir                                                                    string
	OnRecoverySnapshot                                                           func(string)
	OnRetainedFiles                                                              func(string)
}

// Install creates a fresh installation only. Existing installations, units,
// commands, databases or credentials are never overwritten. The caller must
// hold the same lock used by Upgrade.
func Install(o InstallOptions, system FreshServiceManager) (err error) {
	for _, p := range []string{o.PackageDir, o.InstallDir, o.UnitPath, o.CommandPath, o.DatabasePath, o.CredentialsPath} {
		if !filepath.IsAbs(p) {
			return errors.New("installation paths must be absolute")
		}
	}
	if o.ValidatePackage == nil || o.PrepareDatabase == nil {
		return errors.New("package validation and database initialization are required")
	}
	if o.JournalPath != "" {
		if !filepath.IsAbs(o.JournalPath) {
			return errors.New("absolute journal path required")
		}
		if err = mkdirDurable(filepath.Dir(o.JournalPath), 0700); err != nil {
			return err
		}
		if _, e := os.Lstat(o.JournalPath); e == nil {
			return errors.New("pending installation transaction; recover first")
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	if filepath.Dir(o.DatabasePath) != filepath.Dir(o.CredentialsPath) {
		return errors.New("credentials must be stored with the database")
	}
	if info, e := os.Lstat(o.PackageDir); e != nil || !info.IsDir() {
		return errors.New("package must be a real directory")
	}
	for _, path := range []string{o.InstallDir, o.UnitPath, o.CommandPath, o.DatabasePath, o.DatabasePath + "-wal", o.DatabasePath + "-shm", o.DatabasePath + "-journal", o.CredentialsPath} {
		if _, e := os.Lstat(path); e == nil {
			return fmt.Errorf("refusing to overwrite %s", path)
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	exists, err := system.Exists()
	if err != nil {
		return fmt.Errorf("check existing systemd unit: %w", err)
	}
	if exists {
		return errors.New("x-ui systemd unit already exists")
	}
	stage, err := os.MkdirTemp(filepath.Dir(o.InstallDir), ".x-ui-stage-")
	if err != nil {
		return err
	}
	keepRecoveryData := false
	defer func() {
		if !keepRecoveryData {
			_ = os.RemoveAll(stage)
		}
	}()
	if err = copyTree(o.PackageDir, stage); err != nil {
		return fmt.Errorf("prepare package: %w", err)
	}
	if err = o.ValidatePackage(stage); err != nil {
		return fmt.Errorf("validate package: %w", err)
	}
	dbDir := filepath.Dir(o.DatabasePath)
	if err = mkdirDurable(dbDir, 0700); err != nil {
		return err
	}
	dbStage, err := os.MkdirTemp(dbDir, ".x-ui-initial-")
	if err != nil {
		return err
	}
	defer func() {
		if !keepRecoveryData {
			_ = os.RemoveAll(dbStage)
		}
	}()
	stagedDB := filepath.Join(dbStage, "x-ui.db")
	credentials, err := o.PrepareDatabase(stagedDB)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	if len(credentials) == 0 {
		return errors.New("empty initial credentials")
	}
	if info, e := os.Lstat(stagedDB); e != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("invalid initialized database")
	}
	if err = os.Chmod(stagedDB, 0600); err != nil {
		return err
	}
	journal := installJournal{Phase: "installing", Stage: stage, DBStage: dbStage}
	committed := false
	startAttempted := false
	if o.JournalPath != "" {
		var token [32]byte
		if _, err = rand.Read(token[:]); err != nil {
			return err
		}
		journal.Marker = hex.EncodeToString(token[:])
		if err = atomicWrite(filepath.Join(stage, ".xui-install-marker"), []byte(journal.Marker), 0600); err != nil {
			return err
		}
		if err = atomicWrite(filepath.Join(dbStage, "credentials"), credentials, 0600); err != nil {
			return err
		}
		if err = atomicCopy(filepath.Join(stage, "x-ui.service"), filepath.Join(dbStage, "service"), 0600); err != nil {
			return err
		}
		if err = atomicCopy(filepath.Join(stage, "x-ui.sh"), filepath.Join(dbStage, "command"), 0600); err != nil {
			return err
		}
		f, e := os.Open(stagedDB)
		if e != nil {
			return e
		}
		e = f.Sync()
		_ = f.Close()
		if e != nil {
			return e
		}
		if err = syncDir(dbStage); err != nil {
			return err
		}
		if err = syncDir(filepath.Dir(dbStage)); err != nil {
			return err
		}
		if err = syncDir(filepath.Dir(stage)); err != nil {
			return err
		}
		if err = writeInstallJournal(o.JournalPath, journal); err != nil {
			return err
		}
		keepRecoveryData = true
		defer func() {
			if err == nil || committed {
				return
			}
			if e := recoverInstall(o, system, startAttempted); e != nil {
				err = errors.Join(err, fmt.Errorf("installation recovery incomplete: %w", e))
			} else {
				keepRecoveryData = false
			}
		}()
	}
	installed, dbCreated, credsCreated, unitCreated, commandCreated, enabled := false, false, false, false, false, false
	defer func() {
		if err == nil || o.JournalPath != "" {
			return
		}
		var failures []error
		if startAttempted {
			if e := system.Stop(); e != nil {
				err = errors.Join(err, fmt.Errorf("rollback stopped: cannot stop service: %w", e))
				return
			}
		}
		if enabled {
			if e := system.Disable(); e != nil {
				failures = append(failures, e)
			}
		}
		for _, item := range []struct {
			created bool
			path    string
		}{{commandCreated, o.CommandPath}, {unitCreated, o.UnitPath}, {credsCreated, o.CredentialsPath}, {dbCreated, o.DatabasePath}} {
			if item.created {
				if e := os.Remove(item.path); e != nil && !os.IsNotExist(e) {
					failures = append(failures, e)
				}
			}
		}
		if dbCreated {
			for _, suffix := range sqliteSidecarSuffixes {
				if e := os.Remove(o.DatabasePath + suffix); e != nil && !os.IsNotExist(e) {
					failures = append(failures, e)
				}
			}
		}
		if installed {
			if e := os.RemoveAll(o.InstallDir); e != nil {
				failures = append(failures, e)
			}
		}
		if unitCreated {
			if e := system.Reload(); e != nil {
				failures = append(failures, e)
			}
		}
		if len(failures) > 0 {
			err = errors.Join(err, fmt.Errorf("installation rollback incomplete: %w", errors.Join(failures...)))
		}
	}()
	if err = os.Link(stagedDB, o.DatabasePath); err != nil {
		return err
	}
	dbCreated = true
	if o.JournalPath != "" {
		if err = syncDir(dbDir); err != nil {
			return err
		}
	}
	if o.JournalPath != "" {
		if err = os.Link(filepath.Join(dbStage, "credentials"), o.CredentialsPath); err != nil {
			return err
		}
		credsCreated = true
		if err = syncDir(dbDir); err != nil {
			return err
		}
	} else {
		f, err := os.OpenFile(o.CredentialsPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		credsCreated = true
		_, writeErr := f.Write(credentials)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err = renameNoReplace(stage, o.InstallDir); err != nil {
		return err
	}
	installed = true
	if o.JournalPath != "" {
		if err = syncDir(filepath.Dir(o.InstallDir)); err != nil {
			return err
		}
	}
	if err = copyNew(filepath.Join(o.InstallDir, "x-ui.service"), o.UnitPath, 0644); err != nil {
		return err
	}
	unitCreated = true
	if err = copyNew(filepath.Join(o.InstallDir, "x-ui.sh"), o.CommandPath, 0755); err != nil {
		return err
	}
	commandCreated = true
	if err = system.Reload(); err != nil {
		return err
	}
	enabled = true
	if err = system.Enable(); err != nil {
		return err
	}
	startAttempted = true
	if err = system.Start(); err != nil {
		return err
	}
	active, err := system.Active()
	if err != nil {
		return err
	}
	if !active {
		return errors.New("installed service is not active")
	}
	if o.HealthCheck != nil {
		if err = o.HealthCheck(); err != nil {
			return fmt.Errorf("installed service health: %w", err)
		}
	}
	if o.JournalPath != "" {
		journal.Phase = "committed"
		if err = writeInstallJournal(o.JournalPath, journal); err != nil {
			return err
		}
		committed = true
		if err = cleanupInstallRecoveryData(o.JournalPath, dbStage); err != nil {
			return fmt.Errorf("installation committed but journal cleanup failed: %w", err)
		}
		keepRecoveryData = false
	}
	return nil
}

func copyNew(source, dest string, mode os.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	return syncDir(filepath.Dir(dest))
}

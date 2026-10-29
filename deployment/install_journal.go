package deployment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type installJournal struct {
	Phase   string `json:"phase"`
	Stage   string `json:"stage"`
	DBStage string `json:"dbStage"`
	Marker  string `json:"marker"`
}

func writeInstallJournal(path string, j installJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0600)
}

// RecoverInstall removes only objects demonstrably created by this fresh
// installation. A conflict leaves the journal in place for manual review.
func RecoverInstall(o InstallOptions, system FreshServiceManager) error {
	return recoverInstall(o, system, true)
}

func recoverInstall(o InstallOptions, system FreshServiceManager, preserve bool) error {
	if !filepath.IsAbs(o.JournalPath) {
		return errors.New("absolute journal path required")
	}
	data, _, err := readRegular(o.JournalPath)
	if err != nil {
		return err
	}
	var j installJournal
	if err = json.Unmarshal(data, &j); err != nil {
		return err
	}
	if filepath.Dir(filepath.Clean(j.Stage)) != filepath.Dir(filepath.Clean(o.InstallDir)) || !strings.HasPrefix(filepath.Base(j.Stage), ".x-ui-stage-") {
		return errors.New("unsafe installation stage")
	}
	if filepath.Dir(filepath.Clean(j.DBStage)) != filepath.Dir(filepath.Clean(o.DatabasePath)) || !strings.HasPrefix(filepath.Base(j.DBStage), ".x-ui-initial-") {
		return errors.New("unsafe database stage")
	}
	if j.Phase == "committed" || j.Phase == "cleaned" {
		return cleanupInstallRecoveryData(o.JournalPath, j.DBStage)
	}
	if j.Phase != "installing" {
		return errors.New("unknown install journal phase")
	}
	if len(j.Marker) != 64 {
		return errors.New("invalid installation marker")
	}
	if info, e := os.Lstat(j.DBStage); e != nil || !info.IsDir() {
		return errors.New("installation recovery data unavailable")
	}
	// Verify every existing target before stopping services or deleting files.
	for _, item := range []struct {
		path, anchor string
		sameInode    bool
	}{
		{o.DatabasePath, filepath.Join(j.DBStage, "x-ui.db"), true},
		{o.CredentialsPath, filepath.Join(j.DBStage, "credentials"), true},
		{o.UnitPath, filepath.Join(j.DBStage, "service"), false},
		{o.CommandPath, filepath.Join(j.DBStage, "command"), false},
	} {
		target, e := os.Lstat(item.path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		anchor, e := os.Lstat(item.anchor)
		if e != nil || !target.Mode().IsRegular() || !anchor.Mode().IsRegular() {
			return fmt.Errorf("cannot verify installation file %s", item.path)
		}
		if item.sameInode {
			if !os.SameFile(target, anchor) {
				return fmt.Errorf("installation file changed: %s", item.path)
			}
		} else {
			a, e := os.ReadFile(item.anchor)
			if e != nil {
				return e
			}
			b, e := os.ReadFile(item.path)
			if e != nil {
				return e
			}
			if !bytes.Equal(a, b) {
				return fmt.Errorf("installation file changed: %s", item.path)
			}
		}
	}
	if _, e := os.Lstat(o.DatabasePath); os.IsNotExist(e) {
		for _, suffix := range sqliteSidecarSuffixes {
			if _, e := os.Lstat(o.DatabasePath + suffix); e == nil {
				return errors.New("database sidecar exists without installed database")
			} else if !os.IsNotExist(e) {
				return e
			}
		}
	}
	for _, path := range []string{o.InstallDir, j.Stage} {
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !info.IsDir() {
			return fmt.Errorf("installation directory changed: %s", path)
		}
		marker, _, e := readRegular(filepath.Join(path, ".xui-install-marker"))
		if e != nil || string(marker) != j.Marker {
			return fmt.Errorf("installation directory changed: %s", path)
		}
	}
	_, unitErr := os.Lstat(o.UnitPath)
	if unitErr == nil {
		if err = system.Stop(); err != nil {
			return fmt.Errorf("stop installation service: %w", err)
		}
		if err = system.Disable(); err != nil {
			return fmt.Errorf("disable installation service: %w", err)
		}
	} else if !os.IsNotExist(unitErr) {
		return unitErr
	}
	if preserve {
		if _, e := os.Lstat(o.DatabasePath); e == nil {
			if !filepath.IsAbs(o.BackupDir) {
				return errors.New("absolute recovery backup directory required")
			}
			current, e := SnapshotDatabase(o.DatabasePath, o.BackupDir)
			if e != nil {
				return fmt.Errorf("preserve interrupted database: %w", e)
			}
			if e = recordRecoverySnapshot(filepath.Dir(current), current); e != nil {
				return e
			}
			if _, e = os.Lstat(o.CredentialsPath); e == nil {
				if e = atomicCopy(o.CredentialsPath, filepath.Join(filepath.Dir(current), "initial-credentials"), 0600); e != nil {
					return fmt.Errorf("preserve initial credentials: %w", e)
				}
			} else if !os.IsNotExist(e) {
				return e
			}
			if o.OnRecoverySnapshot != nil {
				o.OnRecoverySnapshot(current)
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	if preserve {
		retained, e := retainMarkedDirectory(o.InstallDir, ".xui-install-marker", j.Marker)
		if retained != "" && o.OnRetainedFiles != nil {
			o.OnRetainedFiles(retained)
		}
		if e != nil {
			return fmt.Errorf("retain interrupted installation files: %w", e)
		}
	}
	for _, path := range []string{o.CommandPath, o.UnitPath, o.CredentialsPath, o.DatabasePath + "-wal", o.DatabasePath + "-shm", o.DatabasePath + "-journal", o.DatabasePath} {
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for _, path := range []string{o.InstallDir, j.Stage} {
		if err = os.RemoveAll(path); err != nil {
			return err
		}
	}
	for _, dir := range []string{filepath.Dir(o.CommandPath), filepath.Dir(o.UnitPath), filepath.Dir(o.DatabasePath), filepath.Dir(o.InstallDir)} {
		if err = syncDir(dir); err != nil {
			return err
		}
	}
	if err = system.Reload(); err != nil {
		return err
	}
	j.Phase = "cleaned"
	if err = writeInstallJournal(o.JournalPath, j); err != nil {
		return err
	}
	return cleanupInstallRecoveryData(o.JournalPath, j.DBStage)
}

// Keep the journal until the temporary credentials and database
// links have been durably removed, so recovery can finish interrupted cleanup.
func cleanupInstallRecoveryData(journalPath, dbStage string) error {
	if err := os.RemoveAll(dbStage); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(dbStage)); err != nil {
		return err
	}
	return removeJournal(journalPath)
}

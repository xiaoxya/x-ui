package deployment

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/mattn/go-sqlite3"
)

var sqliteSidecarSuffixes = []string{"-wal", "-shm", "-journal"}

// SnapshotDatabase creates a consistent SQLite snapshot, including committed
// WAL frames, in a newly created private directory. It never creates or
// migrates the source database. If SQLite detects a hot rollback journal, it
// may recover that journal in the source before taking the snapshot.
func SnapshotDatabase(source, backupRoot string) (snapshot string, err error) {
	return snapshotDatabase(source, backupRoot, nil)
}

func snapshotDatabase(source, backupRoot string, afterVacuum func()) (snapshot string, err error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("inspect source database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("source database is not a regular file")
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	backupRoot, err = filepath.Abs(backupRoot)
	if err != nil {
		return "", err
	}
	if err := mkdirDurable(backupRoot, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(backupRoot, "xui-db-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	partial := filepath.Join(dir, ".x-ui.db.partial")
	snapshot = filepath.Join(dir, "x-ui.db")
	vacuum := func(mode string) error {
		dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=" + mode + "&_busy_timeout=5000"}).String()
		db, e := sql.Open("sqlite3", dsn)
		if e != nil {
			return e
		}
		defer db.Close()
		_, e = db.Exec("VACUUM INTO ?", partial)
		return e
	}
	err = vacuum("ro")
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrReadonly {
		// SQLite must write to roll back a hot journal after an abrupt stop.
		if e := os.Remove(partial); e != nil && !os.IsNotExist(e) {
			return "", e
		}
		err = vacuum("rw")
	}
	if err != nil {
		return "", fmt.Errorf("create SQLite snapshot: %w", err)
	}
	if afterVacuum != nil {
		afterVacuum()
	}
	if err = os.Chmod(partial, 0600); err != nil {
		return "", err
	}
	check, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: partial, RawQuery: "mode=ro"}).String())
	if err != nil {
		return "", err
	}
	defer check.Close()
	var result string
	if err = check.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return "", fmt.Errorf("validate snapshot: %w", err)
	}
	if result != "ok" {
		return "", fmt.Errorf("snapshot integrity check: %s", result)
	}
	if err = check.Close(); err != nil {
		return "", err
	}
	file, err := os.Open(partial)
	if err != nil {
		return "", err
	}
	err = file.Sync()
	_ = file.Close()
	if err != nil {
		return "", err
	}
	if err = os.Rename(partial, snapshot); err != nil {
		return "", err
	}
	if err = syncDir(dir); err != nil {
		return "", err
	}
	if err = syncDir(backupRoot); err != nil {
		return "", err
	}
	return snapshot, nil
}

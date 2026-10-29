package service

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alireza0/x-ui/config"
	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/xray"
	_ "github.com/mattn/go-sqlite3"
)

func TestImportRejectsUnrelatedDatabaseBeforeReplacingLiveDB(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	if err := database.InitDB(config.GetDBPath()); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	path := filepath.Join(dir, "unrelated.db")
	other, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Exec("CREATE TABLE unrelated (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	other.Close()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = (&ServerService{}).ImportDB(file); err == nil {
		t.Fatal("unrelated database accepted")
	}
	var count int64
	if err = database.GetDB().Raw("SELECT COUNT(*) FROM users").Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("live database unavailable or altered: count=%d err=%v", count, err)
	}
	files, err := filepath.Glob(filepath.Join(dir, ".x-ui-import-*.db"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary import left behind: %v %v", files, err)
	}
}

func TestGetDbIncludesCommittedWALWithReader(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	if err := database.InitDB(config.GetDBPath()); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	db, err := database.GetDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	reader, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var count int
	if err = reader.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO users(username,password) VALUES ('wal-user','test')"); err != nil {
		t.Fatal(err)
	}
	data, err := (&ServerService{}).GetDb()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "snapshot.db")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if err = snap.QueryRow("SELECT COUNT(*) FROM users WHERE username='wal-user'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot missing WAL commit: %d %v", count, err)
	}
}

func TestRestartXrayServiceReportsCoreFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	t.Setenv("XUI_BIN_FOLDER", dir)
	if err := database.InitDB(config.GetDBPath()); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	if err := (&ServerService{}).RestartXrayService(); err == nil {
		t.Fatal("restart succeeded without a core binary")
	}
}

func TestImportValidBackupPreservesPreviousDatabase(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("XRAY_TEST_BINARY not set")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_DB_FOLDER", dir)
	t.Setenv("XUI_BIN_FOLDER", bin)
	if err = os.Symlink(binary, filepath.Join(bin, xray.GetBinaryName())); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		path := filepath.Join(filepath.Dir(binary), name)
		if _, err = os.Stat(path); err != nil {
			t.Skipf("missing %s", name)
		}
		if err = os.Symlink(path, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.InitDB(config.GetDBPath()); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	s := &ServerService{}
	if err = database.GetDB().Exec("UPDATE users SET username='snapshot-user'").Error; err != nil {
		t.Fatal(err)
	}
	data, err := s.GetDb()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "upload.db")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = database.GetDB().Exec("UPDATE users SET username='current-user'").Error; err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = s.ImportDB(file); err != nil {
		t.Fatalf("valid import: %v", err)
	}
	defer s.StopXrayService()
	var username string
	if err = database.GetDB().Raw("SELECT username FROM users LIMIT 1").Scan(&username).Error; err != nil || username != "snapshot-user" {
		t.Fatalf("import result: %q %v", username, err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, ".x-ui-backup-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup files: %v %v", backups, err)
	}
	previous, err := sql.Open("sqlite3", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	if err = previous.QueryRow("SELECT username FROM users LIMIT 1").Scan(&username); err != nil || username != "current-user" {
		t.Fatalf("previous data: %q %v", username, err)
	}
	// An imported database with an invalid Xray template must roll back.
	badPath := filepath.Join(dir, "bad-config.db")
	if err = os.WriteFile(badPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	bad, err := sql.Open("sqlite3", badPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bad.Exec("DELETE FROM settings WHERE key='xrayTemplateConfig'; INSERT INTO settings(key,value) VALUES ('xrayTemplateConfig','not-json')"); err != nil {
		t.Fatal(err)
	}
	if err = bad.Close(); err != nil {
		t.Fatal(err)
	}
	badFile, err := os.Open(badPath)
	if err != nil {
		t.Fatal(err)
	}
	defer badFile.Close()
	if err = s.ImportDB(badFile); err == nil {
		t.Fatal("invalid imported core config was accepted")
	}
	if err = database.GetDB().Raw("SELECT username FROM users LIMIT 1").Scan(&username).Error; err != nil || username != "snapshot-user" {
		t.Fatalf("failed core startup did not restore prior db: %q %v", username, err)
	}
	if !s.xrayService.IsXrayRunning() {
		t.Fatal("previous core was not restarted")
	}
}

func TestImportMigrationFailureRestoresLiveDatabase(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("XRAY_TEST_BINARY not set")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_DB_FOLDER", dir)
	t.Setenv("XUI_BIN_FOLDER", bin)
	if err = os.Symlink(binary, filepath.Join(bin, xray.GetBinaryName())); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		path := filepath.Join(filepath.Dir(binary), name)
		if _, err = os.Stat(path); err != nil {
			t.Skipf("missing %s", name)
		}
		if err = os.Symlink(path, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.InitDB(config.GetDBPath()); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	if err = database.GetDB().Exec("UPDATE users SET username='original-user'").Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "broken.db")
	broken, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT, password TEXT); INSERT INTO users VALUES (1,'import-user','x'); CREATE TABLE settings(id INTEGER PRIMARY KEY, key TEXT, value TEXT); CREATE TABLE inbounds(id INTEGER PRIMARY KEY, user_id INTEGER, port INTEGER, protocol TEXT, settings TEXT, stream_settings TEXT, tag TEXT); INSERT INTO inbounds(id,tag) VALUES(1,'duplicate'),(2,'duplicate');`
	if _, err = broken.Exec(schema); err != nil {
		t.Fatal(err)
	}
	broken.Close()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	s := &ServerService{}
	if err = s.ImportDB(file); err == nil {
		t.Fatal("expected migration failure")
	}
	defer s.StopXrayService()
	var username string
	if err = database.GetDB().Raw("SELECT username FROM users LIMIT 1").Scan(&username).Error; err != nil || username != "original-user" {
		t.Fatalf("rollback failed: %q %v", username, err)
	}
}

package database

import (
	"bytes"
	"io"
	"os"
	"path"

	"github.com/alireza0/x-ui/config"
	"github.com/alireza0/x-ui/database/migrations"
	"github.com/alireza0/x-ui/database/model"
	"github.com/alireza0/x-ui/util/common"
	"github.com/alireza0/x-ui/xray"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

func initUser() error {
	err := db.AutoMigrate(&model.User{})
	if err != nil {
		return err
	}
	var count int64
	err = db.Model(&model.User{}).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		user := &model.User{
			Username: "admin",
			Password: "admin",
		}
		return db.Create(user).Error
	}
	return nil
}

func InitDB(dbPath string) error {
	dir := path.Dir(dbPath)
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}

	var gormLogger logger.Interface

	if config.IsDebug() {
		gormLogger = logger.Default
	} else {
		gormLogger = logger.Discard
	}

	c := &gorm.Config{
		Logger: gormLogger,
	}
	db, err = gorm.Open(sqlite.Open(dbPath), c)
	if err != nil {
		return err
	}

	err = initUser()
	if err != nil {
		return err
	}
	err = db.AutoMigrate(
		&model.Inbound{},
		&model.Outbound{},
		&model.RoutingRule{},
		&model.Setting{},
		&xray.ClientTraffic{},
	)
	if err != nil {
		return err
	}

	return migrations.Run(db)
}

func CloseDB() error {
	if db != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

func GetDB() *gorm.DB {
	return db
}

func IsNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}

func IsSQLiteDB(file io.Reader) (bool, error) {
	signature := []byte("SQLite format 3\x00")
	buf := make([]byte, len(signature))
	_, err := file.Read(buf)
	if err != nil {
		return false, err
	}
	return bytes.Equal(buf, signature), nil
}

func Checkpoint() error {
	// SQLite can report a busy checkpoint without returning an SQL error. A
	// database file must not be moved while committed frames remain in its WAL.
	var result struct {
		Busy         int
		Log          int
		Checkpointed int
	}
	if err := db.Raw("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&result).Error; err != nil {
		return err
	}
	if result.Busy != 0 || (result.Log >= 0 && result.Checkpointed < result.Log) {
		return common.NewError("database checkpoint incomplete: active readers or writers")
	}
	return nil
}

func ValidateSQLiteDB(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil { // file must exist
		return err
	}
	gdb, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var res string
	if err := gdb.Raw("PRAGMA integrity_check;").Scan(&res).Error; err != nil {
		return err
	}
	if res != "ok" {
		return common.NewError("sqlite integrity check failed: " + res)
	}
	return nil
}

// ValidatePanelDB rejects unrelated SQLite files before the live database is stopped.
func ValidatePanelDB(dbPath string) error {
	if err := ValidateSQLiteDB(dbPath); err != nil {
		return err
	}
	gdb, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	conn, err := gdb.DB()
	if err != nil {
		return err
	}
	defer conn.Close()
	for table, columns := range map[string][]string{
		"users":    {"id", "username", "password"},
		"inbounds": {"id", "user_id", "port", "protocol", "settings", "stream_settings"},
		"settings": {"id", "key", "value"},
	} {
		if !gdb.Migrator().HasTable(table) {
			return common.NewError("missing required table: ", table)
		}
		for _, column := range columns {
			if !gdb.Migrator().HasColumn(table, column) {
				return common.NewError("missing required column: ", table, ".", column)
			}
		}
	}
	return nil
}

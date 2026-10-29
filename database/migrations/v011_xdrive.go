package migrations

import (
	"encoding/json"

	"github.com/alireza0/x-ui/database/model"
	"github.com/alireza0/x-ui/xray"

	"gorm.io/gorm"
)

// migrateV011XDrive renames the XDRIVE "Google Drive" service, which
// Xray-core v26.10.21 deprecates for "google-drive", in stored inbounds and
// outbounds. The core still reads the old name but warns about it on every
// start, and the form only offers the new one.
func migrateV011XDrive(db *gorm.DB) error {
	tx := db.Begin()
	var err error
	defer func() {
		if err == nil {
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}()

	// upgrade returns the rewritten stream settings, or "" when there is
	// nothing to change.
	upgrade := func(raw string) (string, error) {
		stream := map[string]any{}
		if raw == "" || json.Unmarshal([]byte(raw), &stream) != nil || !xray.UpgradeXDriveService(stream) {
			return "", nil
		}
		modified, err := json.MarshalIndent(stream, "", "  ")
		return string(modified), err
	}

	var inbounds []*model.Inbound
	err = tx.Model(model.Inbound{}).Find(&inbounds).Error
	if err != nil {
		return err
	}
	for _, inbound := range inbounds {
		var stream string
		stream, err = upgrade(inbound.StreamSettings)
		if err != nil {
			return err
		}
		if stream == "" {
			continue
		}
		err = tx.Model(model.Inbound{}).Where("id = ?", inbound.Id).Update("stream_settings", stream).Error
		if err != nil {
			return err
		}
	}

	var outbounds []*model.Outbound
	err = tx.Model(model.Outbound{}).Find(&outbounds).Error
	if err != nil {
		return err
	}
	for _, outbound := range outbounds {
		var stream string
		stream, err = upgrade(outbound.StreamSettings)
		if err != nil {
			return err
		}
		if stream == "" {
			continue
		}
		err = tx.Model(model.Outbound{}).Where("id = ?", outbound.Id).Update("stream_settings", stream).Error
		if err != nil {
			return err
		}
	}

	return nil
}

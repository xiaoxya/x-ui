package migrations

import (
	"testing"

	"github.com/alireza0/x-ui/database/model"
)

func TestV011RenamesGoogleDrive(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Outbound{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	const stream = `{"network":"xdrive","xdriveSettings":{"service":"Google Drive","remoteFolder":"x"}}`
	if err := db.Create(&model.Inbound{
		Id: 1, UserId: 1, Tag: "inbound-xdrive", Protocol: model.VLESS, Port: 4443, Enable: true,
		Settings: `{}`, StreamSettings: stream,
	}).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if err := db.Create(&model.Outbound{Tag: "xdrive", Protocol: "vless", Settings: `{}`, StreamSettings: stream}).Error; err != nil {
		t.Fatalf("seed outbound: %v", err)
	}
	if err := db.Create(&model.Outbound{Tag: "direct", Protocol: "freedom", Settings: `{}`, StreamSettings: `{"network":"tcp"}`}).Error; err != nil {
		t.Fatalf("seed outbound: %v", err)
	}

	if err := migrateV011XDrive(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const want = `{"network":"xdrive","xdriveSettings":{"service":"google-drive","remoteFolder":"x"}}`
	var inbound model.Inbound
	if err := db.First(&inbound, 1).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	assertSameJSON(t, inbound.StreamSettings, want)
	var outbounds []model.Outbound
	if err := db.Order("id").Find(&outbounds).Error; err != nil {
		t.Fatalf("reload outbounds: %v", err)
	}
	assertSameJSON(t, outbounds[0].StreamSettings, want)
	if outbounds[1].StreamSettings != `{"network":"tcp"}` {
		t.Fatalf("untouched outbound was rewritten: %q", outbounds[1].StreamSettings)
	}
}

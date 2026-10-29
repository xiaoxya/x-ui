package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/database/model"
)

func TestOutboundTLSMigrationAcrossPanelPaths(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	service := &OutboundService{}
	outbound := &model.Outbound{Tag: "tls-test", Protocol: "vless", Settings: `{"address":"example.com","port":443,"id":"27848739-7e62-4138-9fd3-098a63964b6b","encryption":"none"}`, StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"allowInsecure":true}}`}
	if _, _, err := service.AddOutbound(outbound); err == nil || !strings.Contains(err.Error(), "pinnedPeerCertSha256") {
		t.Fatalf("unpinned outbound accepted: %v", err)
	}
	outbound.StreamSettings = `{"network":"tcp","security":"tls","tlsSettings":{"allowInsecure":true,"pinnedPeerCertSha256":"` + strings.Repeat("a", 64) + `"}}`
	if _, _, err := service.AddOutbound(outbound); err != nil {
		t.Fatal(err)
	}
	config, err := (&XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatal(err)
	}
	var generated string
	for _, out := range config.OutboundConfigs {
		if out.Tag == "tls-test" {
			generated = string(out.StreamSettings)
		}
	}
	if strings.Contains(generated, "allowInsecure") || !strings.Contains(generated, "pinnedPeerCertSha256") {
		t.Fatalf("bad normalized stream: %s", generated)
	}
	var saved model.Outbound
	if err := database.GetDB().First(&saved, outbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.StreamSettings, "allowInsecure") {
		t.Fatal("persisted input unexpectedly changed")
	}
	outbound.StreamSettings = `{"network":"tcp","security":"tls","tlsSettings":{"allowInsecure":true}}`
	if _, _, err := service.UpdateOutbound(outbound); err == nil {
		t.Fatal("unpinned update accepted")
	}
	if result := service.testSingleOutbound(outbound, "http://example.com"); !strings.Contains(result.Message, "pinnedPeerCertSha256") {
		t.Fatalf("test path did not reject invalid TLS: %+v", result)
	}
	if binary := os.Getenv("XRAY_TEST_BINARY"); binary != "" {
		outbound.StreamSettings = saved.StreamSettings
		generatedOutbound, err := normalizedOutboundConfig(outbound)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(map[string]any{"inbounds": []any{}, "outbounds": []any{generatedOutbound}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, "run", "-test", "-config", path).CombinedOutput(); err != nil {
			t.Fatalf("core rejected normalized config: %v: %s", err, output)
		}
	}
}

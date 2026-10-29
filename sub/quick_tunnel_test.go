package sub

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/database/model"
	"github.com/alireza0/x-ui/web/service"
)

func TestQuickTunnelEligibility(t *testing.T) {
	status := []service.TunnelStatus{{Mode: "quick", Running: true, Port: 8080, Domain: "https://example-test.trycloudflare.com"}}
	base := model.Inbound{Enable: true, Protocol: model.VLESS, Port: 8080, StreamSettings: `{"network":"ws","security":"none","wsSettings":{"path":"/ws"}}`}
	tests := []struct {
		name     string
		edit     func(*model.Inbound)
		eligible bool
	}{
		{"plain ws", func(*model.Inbound) {}, true},
		{"other port", func(i *model.Inbound) { i.Port++ }, false},
		{"remote listener", func(i *model.Inbound) { i.Listen = "192.0.2.1" }, false},
		{"disabled", func(i *model.Inbound) { i.Enable = false }, false},
		{"tls upstream", func(i *model.Inbound) { i.StreamSettings = `{"network":"ws","security":"tls"}` }, false},
		{"tcp", func(i *model.Inbound) { i.StreamSettings = `{"network":"tcp","security":"none"}` }, false},
		{"restricted host", func(i *model.Inbound) {
			i.StreamSettings = `{"network":"ws","wsSettings":{"headers":{"Host":"other.example"}}}`
		}, false},
		{"unsupported protocol", func(i *model.Inbound) { i.Protocol = model.Hysteria }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.edit(&in)
			if got := quickTunnelInbound(&in, status); (got != nil) != tc.eligible {
				t.Fatalf("eligibility: got %v", got != nil)
			}
		})
	}
	status[0].Running = false
	if quickTunnelInbound(&base, status) != nil {
		t.Fatal("stopped tunnel advertised")
	}
}

func TestQuickTunnelAppearsOnlyInMatchingSubscriber(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	binary := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho https://example-test.trycloudflare.com\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_CLOUDFLARED_BINARY", binary)
	if err := database.InitDB(filepath.Join(dir, "panel.db")); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	in := model.Inbound{Enable: true, Protocol: model.VLESS, Port: 18080, Tag: "ws-test", Settings: `{"clients":[{"id":"27848739-7e62-4138-9fd3-098a63964b6b","email":"alice","subId":"alice-token","enable":true},{"id":"27848739-7e62-4138-9fd3-098a63964b6c","email":"bob","subId":"bob-token","enable":true}],"decryption":"none","encryption":"none"}`, StreamSettings: `{"network":"ws","security":"none","wsSettings":{"path":"/ws"}}`}
	if err := database.GetDB().Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.StartTunnel(service.TunnelConfig{Mode: "quick", Port: 18080}); err != nil {
		t.Fatal(err)
	}
	defer service.StopTunnel("quick")
	deadline := time.Now().Add(3 * time.Second)
	for service.TunnelStatuses()[0].Domain == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	svc := NewSubService(false, "-ieo")
	links, _, err := svc.GetSubs("alice-token", "origin.example")
	if err != nil || len(links) != 2 {
		t.Fatalf("links %v error %v", links, err)
	}
	u, err := url.Parse(links[1])
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "example-test.trycloudflare.com:443" || u.Query().Get("security") != "tls" || u.Query().Get("host") != "example-test.trycloudflare.com" || u.Query().Get("path") != "/ws" || u.User.Username() != "27848739-7e62-4138-9fd3-098a63964b6b" {
		t.Fatalf("incorrect tunnel link: %s", links[1])
	}
	jsonSub, _, err := NewSubJsonService("", "", svc).GetJson("alice-token", "origin.example")
	if err != nil {
		t.Fatal(err)
	}
	var configs []map[string]any
	if err := json.Unmarshal([]byte(jsonSub), &configs); err != nil || len(configs) != 2 {
		t.Fatalf("JSON config count %d err %v", len(configs), err)
	}
	if !strings.Contains(jsonSub, "example-test.trycloudflare.com") || strings.Contains(jsonSub, "27848739-7e62-4138-9fd3-098a63964b6c") {
		t.Fatal("tunnel domain absent or another client leaked")
	}
	for _, converter := range []struct {
		name, env string
		convert   func([]string) ([]byte, error)
		args      func(string) []string
	}{
		{"sing-box", "SINGBOX_TEST_BINARY", singboxConfig, func(path string) []string { return []string{"check", "-c", path} }},
		{"mihomo", "MIHOMO_TEST_BINARY", mihomoConfig, func(path string) []string { return []string{"-t", "-f", path} }},
	} {
		data, err := converter.convert(links)
		if err != nil || !strings.Contains(string(data), "example-test.trycloudflare.com") {
			t.Fatalf("%s conversion failed: %v", converter.name, err)
		}
		if binary := os.Getenv(converter.env); binary != "" {
			path := filepath.Join(dir, converter.name+".json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(binary, converter.args(path)...).CombinedOutput(); err != nil {
				t.Fatalf("%s validation: %v: %s", converter.name, err, output)
			}
		}
	}
	if binary := os.Getenv("XRAY_TEST_BINARY"); binary != "" {
		data, err := json.Marshal(configs[1])
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "tunnel-xray.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, "run", "-test", "-config", path).CombinedOutput(); err != nil {
			t.Fatalf("Xray validation: %v: %s", err, output)
		}
	}
	if err := service.StopTunnel("quick"); err != nil {
		t.Fatal(err)
	}
	links, _, err = svc.GetSubs("alice-token", "origin.example")
	if err != nil || len(links) != 1 {
		t.Fatalf("stopped tunnel still advertised: %v %v", links, err)
	}
}

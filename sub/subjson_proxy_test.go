package sub

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/alireza0/x-ui/database/model"
)

func TestNormalizeXrayTLSRejectsUnpinnedInsecure(t *testing.T) {
	for _, tc := range []struct {
		name, tls string
		wantErr   bool
	}{
		{"normal", `{"allowInsecure":false}`, false},
		{"unpinned", `{"allowInsecure":true}`, true},
		{"pinned", `{"allowInsecure":true,"pinnedPeerCertSha256":"0123456789abcdef"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := []byte(`{"outbounds":[{"streamSettings":{"tlsSettings":` + tc.tls + `}}]}`)
			result, err := normalizeXrayTLS(config)
			if errors.Is(err, errXrayInsecureTLS) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErr && strings.Contains(string(result), "allowInsecure") {
				t.Fatalf("obsolete setting retained: %s", result)
			}
		})
	}
}

func TestJSONSubscriptionExternalProxiesAreIndependent(t *testing.T) {
	inbound := inboundFor(model.VLESS, `{"encryption":"none"}`, `{"network":"ws","security":"none","wsSettings":{"path":"/proxy"},"externalProxy":[{"dest":"tls.example","port":443,"forceTls":"tls","sni":"first.example","allowInsecure":true},{"dest":"plain.example","port":8080,"forceTls":"same"}]}`)
	inbound.Listen = "127.0.0.1"
	inbound.Port = 1234
	svc := NewSubJsonService("", "", testService())
	for i := 0; i < 2; i++ {
		configs := svc.getConfig(inbound, model.Client{ID: "27848739-7e62-4138-9fd3-098a63964b6b", Email: "a"}, "origin.example")
		if len(configs) != 2 {
			t.Fatalf("got %d configurations", len(configs))
		}
		for n, config := range configs {
			var result struct {
				Outbounds []struct {
					Settings map[string]any `json:"settings"`
					Stream   map[string]any `json:"streamSettings"`
				} `json:"outbounds"`
			}
			if err := json.Unmarshal(config, &result); err != nil {
				t.Fatal(err)
			}
			out := result.Outbounds[0]
			if n == 0 {
				tls, _ := out.Stream["tlsSettings"].(map[string]any)
				if out.Settings["address"] != "tls.example" || out.Settings["port"] != float64(443) || out.Stream["security"] != "tls" || tls["serverName"] != "first.example" || tls["allowInsecure"] != true {
					t.Fatalf("first proxy: %#v", out)
				}
			} else if out.Settings["address"] != "plain.example" || out.Settings["port"] != float64(8080) || out.Stream["security"] != "none" || out.Stream["tlsSettings"] != nil {
				t.Fatalf("second proxy inherited settings: %#v", out)
			}
		}
		if inbound.Listen != "127.0.0.1" || inbound.Port != 1234 {
			t.Fatalf("inbound was mutated: %+v", inbound)
		}
	}
}

func TestShareLinksExternalProxiesAreIndependent(t *testing.T) {
	stream := `{"network":"ws","security":"none","externalProxy":[{"dest":"first.example","port":443,"forceTls":"tls","sni":"first-sni.example","utls":"chrome","allowInsecure":true},{"dest":"second.example","port":80,"forceTls":"same"}]}`
	for _, tc := range []struct {
		protocol model.Protocol
		settings string
	}{
		{model.VLESS, `{"clients":[{"id":"u","email":"e"}]}`},
		{model.Trojan, `{"clients":[{"password":"p","email":"e"}]}`},
		{model.Shadowsocks, `{"method":"aes-256-gcm","clients":[{"password":"p","email":"e"}]}`},
		{model.Hysteria, `{"clients":[{"auth":"p","email":"e"}]}`},
	} {
		t.Run(string(tc.protocol), func(t *testing.T) {
			links := strings.Split(testService().getLink(inboundFor(tc.protocol, tc.settings, stream), "e"), "\n")
			if len(links) != 2 {
				t.Fatalf("expected two links, got %d", len(links))
			}
			first, err := url.Parse(links[0])
			if err != nil {
				t.Fatal(err)
			}
			second, err := url.Parse(links[1])
			if err != nil {
				t.Fatal(err)
			}
			if first.Query().Get("sni") != "first-sni.example" {
				t.Fatalf("missing first SNI: %s", first.RawQuery)
			}
			for _, key := range []string{"sni", "fp", "allowInsecure"} {
				if second.Query().Get(key) != "" {
					t.Fatalf("second proxy inherited %s: %s", key, second.RawQuery)
				}
			}
		})
	}
}

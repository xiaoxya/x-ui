package xray

import "testing"

const xdriveStream = `{"network":"xdrive","xdriveSettings":{"service":"local"}}`

func TestCheckXDriveInbound(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, settings, stream string
		ok                               bool
	}{
		{"not xdrive", "vmess", `{}`, `{"network":"tcp"}`, true},
		{"vless with decryption", "vless", `{"decryption":"mlkem768x25519plus.native.0rtt.abc","clients":[{"email":"a"}]}`, xdriveStream, true},
		{"not vless", "trojan", `{}`, xdriveStream, false},
		{"no decryption", "vless", `{"decryption":"none","clients":[{"email":"a"}]}`, xdriveStream, false},
		{"client flow", "vless", `{"decryption":"x","clients":[{"email":"a","flow":"xtls-rprx-vision"}]}`, xdriveStream, false},
	} {
		err := CheckXDriveInbound(tc.protocol, tc.settings, tc.stream)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestCheckXDriveOutbound(t *testing.T) {
	const mux = `{"enabled":true,"concurrency":8,"xudpConcurrency":16,"xudpProxyUDP443":"reject"}`
	for _, tc := range []struct {
		name, protocol, settings, mux string
		ok                            bool
	}{
		{"flat vless with mux", "vless", `{"address":"a","port":1,"id":"u","encryption":"mlkem"}`, mux, true},
		{"vnext vless with mux", "vless", `{"vnext":[{"users":[{"id":"u","encryption":"mlkem"}]}]}`, mux, true},
		{"reverse needs no mux", "vless", `{"id":"u","encryption":"mlkem","reverse":{"tag":"r"}}`, "", true},
		{"not vless", "vmess", `{}`, mux, false},
		{"no encryption", "vless", `{"id":"u","encryption":"none"}`, mux, false},
		{"flow", "vless", `{"id":"u","encryption":"mlkem","flow":"xtls-rprx-vision"}`, mux, false},
		{"testpre", "vless", `{"id":"u","encryption":"mlkem","testpre":2}`, mux, false},
		{"mux off", "vless", `{"id":"u","encryption":"mlkem"}`, `{"enabled":false}`, false},
		{"no mux", "vless", `{"id":"u","encryption":"mlkem"}`, "", false},
		{"negative concurrency", "vless", `{"id":"u","encryption":"mlkem"}`, `{"enabled":true,"concurrency":-1}`, false},
		{"skip udp 443", "vless", `{"id":"u","encryption":"mlkem"}`, `{"enabled":true,"xudpProxyUDP443":"skip"}`, false},
	} {
		err := CheckXDriveOutbound(tc.protocol, tc.settings, xdriveStream, tc.mux)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestUpgradeXDriveService(t *testing.T) {
	stream := streamFrom(t, `{"network":"xdrive","xdriveSettings":{"service":"Google Drive","remoteFolder":"x"}}`)
	if !UpgradeXDriveService(stream) {
		t.Fatal("reported no change")
	}
	if stream["xdriveSettings"].(map[string]any)["service"] != "google-drive" {
		t.Fatalf("service not renamed: %v", stream)
	}
	if UpgradeXDriveService(stream) || UpgradeXDriveService(streamFrom(t, `{"network":"tcp"}`)) {
		t.Fatal("rewrote a stream that needs nothing")
	}
}

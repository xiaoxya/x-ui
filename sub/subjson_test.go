package sub

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/x-ui/database/model"
	"github.com/xtls/xray-core/infra/conf"
)

// The JSON subscription hands clients a ready outbound, so it has to be one the
// core accepts: a MASQUE outbound with mux, or without its credentials, is not.
func TestJsonSubMasqueOutboundBuilds(t *testing.T) {
	service := NewSubJsonService(`{"enabled":true,"concurrency":8}`, "", testService())
	inbound := inboundFor(model.Masque,
		`{"clients":[{"email":"alice","pass":"secret"}],"address":["10.14.0.1/24"]}`,
		`{"network":"masque","security":"tls","masqueSettings":{"path":"/custom"},
		  "tlsSettings":{"serverName":"example.com","alpn":["h3","h2"]},
		  "finalmask":{"quicParams":{"congestion":"bbr","udpHop":{"ports":"20000-30000"}}}}`)
	inbound.Listen = "example.com"
	client := model.Client{Email: "alice", Pass: "secret"}

	raw := service.genOutbound(inbound, service.streamData(inbound.StreamSettings), client)

	var outbound map[string]any
	if err := json.Unmarshal(raw, &outbound); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, ok := outbound["mux"]; ok {
		t.Fatalf("mux was set on a MASQUE outbound: %s", raw)
	}
	masque, _ := outbound["streamSettings"].(map[string]any)["masqueSettings"].(map[string]any)
	if masque["user"] != "alice" || masque["pass"] != "secret" || masque["path"] != "/custom" {
		t.Fatalf("masqueSettings = %v", masque)
	}

	var detour conf.OutboundDetourConfig
	if err := json.Unmarshal(raw, &detour); err != nil {
		t.Fatalf("core cannot read the outbound: %v", err)
	}
	if _, err := detour.Build(); err != nil {
		t.Fatalf("core rejects the outbound: %v\n%s", err, raw)
	}
}

// An XDRIVE client needs VLESS encryption and mux on every connection; the
// JSON subscription turns mux on even when none is configured.
func TestJsonSubXDriveOutboundBuilds(t *testing.T) {
	service := NewSubJsonService("", "", testService())
	inbound := inboundFor(model.VLESS,
		`{"clients":[{"id":"27848739-7e62-4138-9fd3-098a63964b6b","email":"alice"}],
		  "decryption":"mlkem768x25519plus.native.600s.EDz4VYDJA5t2ZN-1Fg5ldwBZKNYqFqM67VVOV7fNu3M",
		  "encryption":"mlkem768x25519plus.native.0rtt.zvlPsO6IctGhuOTjYMH7leY3nEGGEcAUFLGyBELn9m8"}`,
		`{"network":"xdrive","security":"none","xdriveSettings":{"service":"local","remoteFolder":"/tmp/xdrive"}}`)
	inbound.Listen = "example.com"
	client := model.Client{ID: "27848739-7e62-4138-9fd3-098a63964b6b", Email: "alice"}

	raw := service.genOutbound(inbound, service.streamData(inbound.StreamSettings), client)

	var detour conf.OutboundDetourConfig
	if err := json.Unmarshal(raw, &detour); err != nil {
		t.Fatalf("core cannot read the outbound: %v", err)
	}
	if _, err := detour.Build(); err != nil {
		t.Fatalf("core rejects the outbound: %v\n%s", err, raw)
	}
}

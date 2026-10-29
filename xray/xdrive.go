package xray

import (
	"encoding/json"

	"github.com/alireza0/x-ui/util/common"
)

// Xray-core v26.10.21 only runs the xdrive transport under VLESS with its
// encryption, without "flow", and on the client with Mux.Cool for every
// connection. It refuses anything else while building the config, which keeps
// all of Xray from starting, not only that inbound or outbound, so the panel
// checks the same rules before saving.

func isXDrive(stream string) bool {
	var parsed struct {
		Network string `json:"network"`
	}
	return json.Unmarshal([]byte(stream), &parsed) == nil && parsed.Network == "xdrive"
}

// CheckXDriveInbound rejects an xdrive inbound the core would refuse.
func CheckXDriveInbound(protocol string, settings string, stream string) error {
	if !isXDrive(stream) {
		return nil
	}
	if protocol != "vless" {
		return common.NewError("the XDRIVE transport only works with VLESS")
	}
	var parsed struct {
		Decryption string `json:"decryption"`
		Clients    []struct {
			Email string `json:"email"`
			Flow  string `json:"flow"`
		} `json:"clients"`
	}
	if json.Unmarshal([]byte(settings), &parsed) != nil {
		return nil
	}
	if parsed.Decryption == "" || parsed.Decryption == "none" {
		return common.NewError("the XDRIVE transport needs VLESS decryption, get new VLESS encryption keys")
	}
	for _, client := range parsed.Clients {
		if client.Flow != "" {
			return common.NewErrorf("the XDRIVE transport doesn't support VLESS flow, remove it from %s", client.Email)
		}
	}
	return nil
}

// CheckXDriveOutbound rejects an xdrive outbound the core would refuse. Its
// VLESS user is either the flat settings the panel writes or a vnext entry.
func CheckXDriveOutbound(protocol string, settings string, stream string, mux string) error {
	if !isXDrive(stream) {
		return nil
	}
	if protocol != "vless" {
		return common.NewError("the XDRIVE transport only works with VLESS")
	}
	type user struct {
		Flow       string          `json:"flow"`
		Encryption string          `json:"encryption"`
		Testpre    int             `json:"testpre"`
		Reverse    json.RawMessage `json:"reverse"`
	}
	var parsed struct {
		user
		Vnext []struct {
			Users []user `json:"users"`
		} `json:"vnext"`
	}
	if json.Unmarshal([]byte(settings), &parsed) != nil {
		return nil
	}
	account := parsed.user
	if len(parsed.Vnext) > 0 && len(parsed.Vnext[0].Users) > 0 {
		account = parsed.Vnext[0].Users[0]
	}
	if account.Flow != "" {
		return common.NewError("the XDRIVE transport doesn't support VLESS flow")
	}
	if account.Encryption == "" || account.Encryption == "none" {
		return common.NewError("the XDRIVE transport needs VLESS encryption")
	}
	if account.Testpre != 0 {
		return common.NewError("the XDRIVE transport doesn't support VLESS testpre")
	}
	// a reverse proxy opens its own mux
	if len(account.Reverse) > 0 && string(account.Reverse) != "null" {
		return nil
	}
	if !XDriveMuxValid(mux) {
		return common.NewError(`the XDRIVE transport needs mux enabled, with no negative concurrency and no "skip" for UDP 443`)
	}
	return nil
}

// XDriveMuxValid reports whether a mux setting is one the core accepts on an
// xdrive client.
func XDriveMuxValid(mux string) bool {
	var parsed struct {
		Enabled         bool   `json:"enabled"`
		Concurrency     int    `json:"concurrency"`
		XudpConcurrency int    `json:"xudpConcurrency"`
		XudpProxyUDP443 string `json:"xudpProxyUDP443"`
	}
	if mux == "" || json.Unmarshal([]byte(mux), &parsed) != nil {
		return false
	}
	return parsed.Enabled && parsed.Concurrency >= 0 && parsed.XudpConcurrency >= 0 && parsed.XudpProxyUDP443 != "skip"
}

// UpgradeXDriveService renames the "Google Drive" service, which Xray-core
// v26.10.21 deprecates for "google-drive", in a stream settings map.
func UpgradeXDriveService(stream map[string]any) bool {
	xdrive, _ := stream["xdriveSettings"].(map[string]any)
	if xdrive["service"] != "Google Drive" {
		return false
	}
	xdrive["service"] = "google-drive"
	return true
}

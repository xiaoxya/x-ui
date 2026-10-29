package xray

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/alireza0/x-ui/util/json_util"
)

// NormalizeOutboundTLS migrates the obsolete skip-verification flag only
// when the operator has already specified an explicit certificate pin.
func NormalizeOutboundTLS(raw json_util.RawMessage) (json_util.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var stream map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stream); err != nil {
		return nil, err
	}
	var tls map[string]json.RawMessage
	if len(stream["tlsSettings"]) == 0 || string(stream["tlsSettings"]) == "null" {
		return raw, nil
	}
	if err := json.Unmarshal(stream["tlsSettings"], &tls); err != nil {
		return nil, err
	}
	flag, exists := tls["allowInsecure"]
	if !exists {
		return raw, nil
	}
	var insecure bool
	if err := json.Unmarshal(flag, &insecure); err != nil {
		return nil, errors.New("invalid TLS allowInsecure value")
	}
	if insecure {
		var pin string
		if err := json.Unmarshal(tls["pinnedPeerCertSha256"], &pin); err != nil || strings.TrimSpace(pin) == "" {
			return nil, errors.New("Xray no longer supports allowInsecure; configure pinnedPeerCertSha256")
		}
	}
	delete(tls, "allowInsecure")
	encoded, err := json.Marshal(tls)
	if err != nil {
		return nil, err
	}
	stream["tlsSettings"] = encoded
	return json.Marshal(stream)
}

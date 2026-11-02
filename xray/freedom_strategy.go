package xray

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeFreedomStrategy mirrors Xray's legacy precedence: outbound
// targetStrategy, freedom targetStrategy/domainStrategy, then sockopt. It only
// changes the generated configuration, leaving stored settings recoverable.
func NormalizeFreedomStrategy(c *OutboundConfig) error {
	if c.Protocol != "freedom" {
		return nil
	}
	settings := map[string]json.RawMessage{}
	if len(c.Settings) > 0 && string(c.Settings) != "null" {
		if err := json.Unmarshal(c.Settings, &settings); err != nil || settings == nil {
			return fmt.Errorf("invalid freedom settings")
		}
	}
	read := func(key string) (string, error) {
		var value string
		if raw, ok := settings[key]; ok {
			if err := json.Unmarshal(raw, &value); err != nil {
				return "", fmt.Errorf("invalid freedom %s", key)
			}
		}
		return value, nil
	}
	legacy, err := read("domainStrategy")
	if err != nil {
		return err
	}
	innerTarget, err := read("targetStrategy")
	if err != nil {
		return err
	}
	if innerTarget != "" {
		legacy = innerTarget
	}
	for _, value := range []string{c.TargetStrategy, legacy} {
		switch strings.ToLower(value) {
		case "", "asis", "useip", "useipv4", "useipv6", "useipv4v6", "useipv6v4", "forceip", "forceipv4", "forceipv6", "forceipv4v6", "forceipv6v4":
		default:
			return fmt.Errorf("unsupported freedom domain strategy %q", value)
		}
	}
	strategy := c.TargetStrategy
	if strategy == "" || strings.EqualFold(strategy, "asis") {
		strategy = legacy
	}
	delete(settings, "domainStrategy")
	delete(settings, "targetStrategy")
	c.Settings, err = json.Marshal(settings)
	if err != nil {
		return err
	}
	c.TargetStrategy = ""
	if strategy == "" || strings.EqualFold(strategy, "asis") {
		return nil
	}
	stream := map[string]json.RawMessage{}
	if len(c.StreamSettings) > 0 && string(c.StreamSettings) != "null" {
		if err := json.Unmarshal(c.StreamSettings, &stream); err != nil || stream == nil {
			return fmt.Errorf("invalid freedom stream settings")
		}
	}
	sockopt := map[string]json.RawMessage{}
	if raw := stream["sockopt"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &sockopt); err != nil || sockopt == nil {
			return fmt.Errorf("invalid freedom socket settings")
		}
	}
	sockopt["domainStrategy"], _ = json.Marshal(strategy)
	stream["sockopt"], err = json.Marshal(sockopt)
	if err != nil {
		return err
	}
	c.StreamSettings, err = json.Marshal(stream)
	return err
}

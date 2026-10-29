package sub

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alireza0/x-ui/database/model"
)

func TestHysteriaPortHopConversion(t *testing.T) {
	in := inboundFor(model.Hysteria, `{"version":2,"clients":[{"auth":"password","email":"alice"}]}`, `{"network":"hysteria","security":"tls","tlsSettings":{"serverName":"example.org"},"hysteriaSettings":{"version":2},"finalmask":{"quicParams":{"udpHop":{"ports":"10000-10100,20000","interval":"15-30"}}}}`)
	in.Port = 443
	link := testService().getLink(in, "alice")
	for _, tc := range []struct {
		name, env string
		generate  func([]string) ([]byte, error)
	}{{"singbox", "SINGBOX_TEST_BINARY", singboxConfig}, {"mihomo", "MIHOMO_TEST_BINARY", mihomoConfig}} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.generate([]string{link})
			if err != nil {
				t.Fatalf("conversion failed: %v", err)
			}
			var obj map[string]any
			if err = json.Unmarshal(data, &obj); err != nil {
				t.Fatal(err)
			}
			var node map[string]any
			if tc.name == "singbox" {
				node = obj["outbounds"].([]any)[2].(map[string]any)
				ranges := node["server_ports"].([]any)
				if len(ranges) != 2 || ranges[0] != "10000:10100" || ranges[1] != "20000:20000" || node["hop_interval"] != "15s" || node["hop_interval_max"] != "30s" || node["server_port"] != nil {
					t.Fatalf("wrong sing-box hop config: %s", data)
				}
			} else {
				node = obj["proxies"].([]any)[0].(map[string]any)
				if node["ports"] != "10000-10100,20000" || node["hop-interval"] != "15-30" {
					t.Fatalf("wrong Mihomo hop config: %s", data)
				}
			}
			bin := os.Getenv(tc.env)
			if bin == "" {
				return
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			if tc.name == "singbox" {
				cmd = exec.Command(bin, "check", "-c", path)
			} else {
				cmd = exec.Command(bin, "-t", "-d", dir, "-f", path)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("client rejected config: %v %s\n%s", err, out, data)
			}
		})
	}
}

func TestPortHopRejectsInvalidSettings(t *testing.T) {
	if _, err := singboxConfig([]string{"hysteria2://pw@example.org:443?sni=example.org&mport=10000-10001&udphopInterval=1"}); err == nil {
		t.Fatal("sing-box accepted interval under 5 seconds")
	}

	for _, value := range []string{"0", "1-65536", "300-200", "1,,2", "abc"} {
		if _, err := parseHopPorts(value); err == nil {
			t.Errorf("accepted invalid port range %q", value)
		}
	}
	for _, value := range []string{"0", "5-2", "abc", "5-6-7"} {
		if _, _, err := parseHopInterval(value); err == nil {
			t.Errorf("accepted invalid interval %q", value)
		}
	}
	_, err := singboxConfig([]string{"hysteria2://pw@example.org:443?sni=example.org&udphopInterval=5"})
	if err == nil || !strings.Contains(err.Error(), "requires port") {
		t.Fatalf("missing ports accepted: %v", err)
	}
}

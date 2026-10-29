package sub

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMihomoConfig(t *testing.T) {
	links := []string{
		"vless://27848739-7e62-4138-9fd3-098a63964b6b@example.org:443?type=ws&security=tls&path=%2Fx&host=cdn.example.org&sni=example.org#node",
		"hysteria2://secret@example.org:8443?sni=example.org#hy2",
		"trojan://password@example.org:443?type=grpc&serviceName=api&security=tls#grpc",
	}
	data, err := mihomoConfig(links)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if got := len(config["proxies"].([]any)); got != 3 {
		t.Fatalf("got %d proxies", got)
	}
	binary := os.Getenv("MIHOMO_TEST_BINARY")
	if binary == "" {
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "-t", "-d", dir, "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("Mihomo rejected config: %v %s\n%s", err, output, data)
	}
}
func TestMihomoRejectsUnsupportedNodes(t *testing.T) {
	_, err := mihomoConfig([]string{"vless://id@example.org:443?type=xhttp&security=tls&extra=%7B%22unsupported%22%3A1%7D"})
	if err == nil || !strings.Contains(err.Error(), "unsupported XHTTP") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestMihomoXHTTPReality(t *testing.T) {
	link := "vless://27848739-7e62-4138-9fd3-098a63964b6b@example.org:443?type=xhttp&security=reality&sni=example.org&pbk=A5aoXJY9SW6FQ97xuwFZrR14ERpLvXHfRqtsttSKjxE&sid=abcd&fp=chrome&spx=%2Frandom&path=%2Fapi&host=cdn.example.org&mode=packet-up"
	data, err := mihomoConfig([]string{link})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Proxies []map[string]any `json:"proxies"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	p := config.Proxies[0]
	opts, ok := p["xhttp-opts"].(map[string]any)
	if !ok || p["network"] != "xhttp" || opts["path"] != "/api" || opts["host"] != "cdn.example.org" || opts["mode"] != "packet-up" || p["reality-opts"] == nil {
		t.Fatalf("bad output: %s", data)
	}
	binary := os.Getenv("MIHOMO_TEST_BINARY")
	if binary == "" {
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(binary, "-t", "-d", dir, "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("invalid config: %s %v", out, err)
	}
}

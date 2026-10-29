package sub

import (
	"encoding/json"
	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/database/model"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingboxConfig(t *testing.T) {
	links := []string{
		"vless://27848739-7e62-4138-9fd3-098a63964b6b@example.org:443?type=ws&security=tls&path=%2Fx&host=cdn.example.org&sni=example.org#node",
		"hysteria2://secret@example.org:8443?sni=example.org#hy2",
		"trojan://password@example.org:443?type=tcp&security=tls&sni=example.org#trojan",
		"vless://27848739-7e62-4138-9fd3-098a63964b6b@example.org:443?type=grpc&serviceName=api&authority=&security=tls#grpc",
		"trojan://password@example.org:443?type=httpupgrade&host=cdn.example.org&path=%2Ftest&security=tls#upgrade",
	}
	data, err := singboxConfig(links)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	outs := config["outbounds"].([]any)
	if len(outs) != 7 {
		t.Fatalf("expected selector, direct and 5 nodes: %s", data)
	}
	binary := os.Getenv("SINGBOX_TEST_BINARY")
	if binary == "" {
		return
	}
	file := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(binary, "check", "-c", file).CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box rejected config: %v %s\n%s", err, out, data)
	}
}
func TestSingboxRejectsUnsupportedRatherThanDroppingNodes(t *testing.T) {
	_, err := singboxConfig([]string{"vless://id@example.org:443?type=xhttp&security=tls"})
	if err == nil || !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("expected explicit unsupported error: %v", err)
	}
}

func TestSingboxRejectsGrpcAuthority(t *testing.T) {
	_, err := singboxConfig([]string{"vless://id@example.org:443?type=grpc&authority=other.example&security=tls"})
	if err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("authority silently discarded: %v", err)
	}
}

func TestSingboxSubscriptionRoute(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	in := model.Inbound{Enable: true, Port: 443, Protocol: model.VLESS, Tag: "inbound-443", Settings: `{"clients":[{"id":"27848739-7e62-4138-9fd3-098a63964b6b","email":"alice","subId":"test-token","enable":true}],"decryption":"none"}`, StreamSettings: `{"network":"ws","security":"tls","wsSettings":{"path":"/ws","host":"example.org"},"tlsSettings":{"serverName":"example.org"}}`}
	if err := database.GetDB().Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewSUBController(r.Group("/"), "/sub/", "/json/", false, false, "-ieo", "10", "", "")
	req := httptest.NewRequest("GET", "http://example.org/sub/test-token/singbox", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing cache policy")
	}
	binary := os.Getenv("SINGBOX_TEST_BINARY")
	if binary != "" {
		file := filepath.Join(t.TempDir(), "out.json")
		if err := os.WriteFile(file, res.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(binary, "check", "-c", file).CombinedOutput()
		if err != nil {
			t.Fatalf("invalid result: %s %v %s", out, err, res.Body.String())
		}
	}
	mihomoResult := httptest.NewRecorder()
	r.ServeHTTP(mihomoResult, httptest.NewRequest("GET", "http://example.org/sub/test-token/mihomo", nil))
	if mihomoResult.Code != 200 {
		t.Fatalf("mihomo route: %d %s", mihomoResult.Code, mihomoResult.Body.String())
	}
	if binary := os.Getenv("MIHOMO_TEST_BINARY"); binary != "" {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, mihomoResult.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(binary, "-t", "-d", dir, "-f", path).CombinedOutput()
		if err != nil {
			t.Fatalf("mihomo rejected route: %s %v", out, err)
		}
	}
	unsupported := model.Inbound{Enable: true, Port: 8443, Protocol: model.Wireguard, Tag: "wireguard-8443", Settings: `{"clients":[{"email":"alice","subId":"test-token","enable":true}]}`, StreamSettings: `{}`}
	if err := database.GetDB().Create(&unsupported).Error; err != nil {
		t.Fatal(err)
	}
	mixed := httptest.NewRecorder()
	r.ServeHTTP(mixed, httptest.NewRequest("GET", "http://example.org/sub/test-token/singbox", nil))
	if mixed.Code != 422 || !strings.Contains(mixed.Body.String(), "unsupported") {
		t.Fatalf("unsupported node silently omitted: %d %s", mixed.Code, mixed.Body.String())
	}
	mixedMihomo := httptest.NewRecorder()
	r.ServeHTTP(mixedMihomo, httptest.NewRequest("GET", "http://example.org/sub/test-token/mihomo", nil))
	if mixedMihomo.Code != 422 {
		t.Fatalf("mihomo silently omitted node: %d", mixedMihomo.Code)
	}

	bad := httptest.NewRecorder()
	r.ServeHTTP(bad, httptest.NewRequest("GET", "http://example.org/sub/incorrect/singbox", nil))
	if bad.Code == 200 {
		t.Fatal("unknown subscription accepted")
	}
}

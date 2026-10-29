package sub

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alireza0/x-ui/database/model"
	"github.com/alireza0/x-ui/web/service"
)

// Exercise the generated outbound through an actual TLS-terminating WebSocket
// reverse proxy and Xray server. This is a local stand-in, not Cloudflare.
func TestQuickTunnelLocalDataPath(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("set XRAY_TEST_BINARY")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "tunnel-data-path-ok") }))
	defer origin.Close()
	u, _ := url.Parse(origin.URL)
	_, originPort, _ := net.SplitHostPort(u.Host)
	freePort := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	for _, tc := range []struct {
		protocol model.Protocol
		settings string
		client   model.Client
	}{
		{model.VLESS, `{"clients":[{"id":"27848739-7e62-4138-9fd3-098a63964b6b","email":"e"}],"decryption":"none","encryption":"none"}`, model.Client{ID: "27848739-7e62-4138-9fd3-098a63964b6b", Email: "e"}},
		{model.VMess, `{"clients":[{"id":"27848739-7e62-4138-9fd3-098a63964b6b","email":"e"}]}`, model.Client{ID: "27848739-7e62-4138-9fd3-098a63964b6b", Email: "e"}},
		{model.Trojan, `{"clients":[{"password":"test-password","email":"e"}]}`, model.Client{Password: "test-password", Email: "e"}},
	} {
		t.Run(string(tc.protocol), func(t *testing.T) {
			dir := t.TempDir()
			corePort, proxyPort := freePort(), freePort()
			target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", corePort))
			edge := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
			defer edge.Close()
			_, edgePortText, _ := net.SplitHostPort(edge.Listener.Addr().String())
			edgePort, _ := strconv.Atoi(edgePortText)
			in := model.Inbound{Enable: true, Protocol: tc.protocol, Listen: "127.0.0.1", Port: corePort, Settings: tc.settings, StreamSettings: `{"network":"ws","security":"none","wsSettings":{"path":"/ws"}}`, Remark: "local"}
			var settings any
			if err := json.Unmarshal([]byte(tc.settings), &settings); err != nil {
				t.Fatal(err)
			}
			serverConfig := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": corePort, "protocol": tc.protocol, "settings": settings, "streamSettings": map[string]any{"network": "ws", "security": "none", "wsSettings": map[string]any{"path": "/ws"}}}}, "outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": originPort}}}}}, "routing": map[string]any{"rules": []any{map[string]any{"type": "field", "network": "tcp", "outboundTag": "direct"}}}}
			tunnelIn := quickTunnelInbound(&in, []service.TunnelStatus{{Mode: "quick", Running: true, Port: corePort, Domain: "https://example-test.trycloudflare.com"}})
			if tunnelIn == nil {
				t.Fatal("tunnel inbound unavailable")
			}
			configs := NewSubJsonService("", "", testService()).getConfig(tunnelIn, tc.client, "unused")
			var clientConfig map[string]any
			if len(configs) != 1 || json.Unmarshal(configs[0], &clientConfig) != nil {
				t.Fatal("invalid generated configuration")
			}
			out := clientConfig["outbounds"].([]any)[0].(map[string]any)
			outSettings := out["settings"].(map[string]any)
			outSettings["address"], outSettings["port"] = "127.0.0.1", edgePort
			tls := out["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
			// Trust only the ephemeral certificate created by httptest.
			hash := sha256.Sum256(edge.Certificate().Raw)
			tls["pinnedPeerCertSha256"] = fmt.Sprintf("%x", hash)
			clientConfig["inbounds"] = []any{map[string]any{"listen": "127.0.0.1", "port": proxyPort, "protocol": "http", "settings": map[string]any{}}}
			write := func(name string, config any) string {
				p := filepath.Join(dir, name)
				data, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, data, 0600); err != nil {
					t.Fatal(err)
				}
				return p
			}
			serverPath, clientPath := write("server.json", serverConfig), write("client.json", clientConfig)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var logs lockedTestBuffer
			for _, path := range []string{serverPath, clientPath} {
				cmd := exec.CommandContext(ctx, binary, "run", "-c", path)
				cmd.Stdout, cmd.Stderr = &logs, &logs
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			}
			proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
			client := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 2 * time.Second}
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				response, err := client.Get(origin.URL)
				if err == nil {
					body, _ := io.ReadAll(response.Body)
					response.Body.Close()
					if string(body) == "tunnel-data-path-ok" {
						return
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			cancel()
			t.Fatalf("local tunnel data path failed: %s", logs.String())
		})
	}
}

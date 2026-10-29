package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTunnelOutputIgnoresAPIAddressAndHandlesChunks(t *testing.T) {
	var output tunnelOutput
	for _, part := range []string{"request https://api.trycloudflare.com\n", "https://my-tunnel.trycloud", "flare.com\n"} {
		_, _ = output.Write([]byte(part))
	}
	if got := output.Domain(); got != "https://my-tunnel.trycloudflare.com" {
		t.Fatalf("unexpected tunnel domain %q", got)
	}
}

func TestTunnelLifecycleAndSecretHandling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a shell script")
	}
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	binary := filepath.Join(dir, "fake-cloudflared")
	script := `#!/bin/sh
case "$*" in *test-secret*) exit 12;; esac
if [ "$3" = "run" ] && [ "$TUNNEL_TOKEN" != "test-secret" ]; then exit 13; fi
if [ "$3" = "--url" ]; then echo 'https://sample-example.trycloudflare.com'; fi
exec sleep 30
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_CLOUDFLARED_BINARY", binary)
	previous := managedTunnels
	managedTunnels = &tunnelManager{workers: make(map[string]*tunnelWorker)}
	defer func() { StopTunnelsForShutdown(); managedTunnels = previous }()
	if err := StartTunnel(TunnelConfig{Mode: "quick", Port: 12345}); err != nil {
		t.Fatal(err)
	}
	if err := StartTunnel(TunnelConfig{Mode: "named", Token: "test-secret"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statuses := TunnelStatuses()
		if statuses[0].Domain != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := TunnelStatuses()
	if !status[0].Running || status[0].Domain != "https://sample-example.trycloudflare.com" || !status[1].Running {
		t.Fatalf("unexpected status: %+v", status)
	}
	jsonStatus, _ := json.Marshal(status)
	if strings.Contains(string(jsonStatus), "test-secret") {
		t.Fatal("secret leaked in status")
	}
	info, err := os.Stat(tunnelPath("named"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("secret file permissions: %o", info.Mode().Perm())
	}
	if err = StartTunnel(TunnelConfig{Mode: "quick", Port: 0}); err == nil {
		t.Fatal("invalid port accepted")
	}
	// An unexpected child exit retains desired state and is restarted by the watchdog.
	managedTunnels.mu.Lock()
	crashed := managedTunnels.workers["quick"]
	managedTunnels.mu.Unlock()
	if err = crashed.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-crashed.done:
	case <-time.After(2 * time.Second):
		t.Fatal("child did not exit")
	}
	if status := TunnelStatuses()[0]; status.Running || status.Domain != "" {
		t.Fatalf("stopped tunnel should not advertise a domain: %+v", status)
	}
	if err = RestoreTunnels(); err != nil {
		t.Fatal(err)
	}
	if !TunnelStatuses()[0].Running {
		t.Fatal("watchdog did not restore tunnel")
	}
	StopTunnelsForShutdown()
	if err = RestoreTunnels(); err != nil {
		t.Fatal(err)
	}
	if statuses := TunnelStatuses(); !statuses[0].Running || !statuses[1].Running {
		t.Fatalf("restore failed: %+v", statuses)
	}
	if err = StopTunnel("named"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(tunnelPath("named")); !os.IsNotExist(err) {
		t.Fatalf("saved token not removed: %v", err)
	}
	if err = StopTunnel("quick"); err != nil {
		t.Fatal(err)
	}
}

// The optional real test creates a temporary public tunnel to a harmless local
// test server and stops it on exit. It requires Cloudflare network access.
func TestCloudflareQuickTunnelIntegration(t *testing.T) {
	binary := os.Getenv("CLOUDFLARED_TEST_BINARY")
	if binary == "" {
		t.Skip("CLOUDFLARED_TEST_BINARY not set")
	}
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	t.Setenv("XUI_CLOUDFLARED_BINARY", binary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "xui-tunnel-test-ok") }))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	manager := &tunnelManager{workers: make(map[string]*tunnelWorker)}
	if err = manager.start(TunnelConfig{Mode: "quick", Port: port}, false); err != nil {
		t.Fatal(err)
	}
	defer manager.stop("quick", false)
	deadline := time.Now().Add(35 * time.Second)
	client := &http.Client{Timeout: 3 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		status := manager.statuses()[0]
		if !status.Running {
			t.Fatalf("cloudflared exited: %s", status.Error)
		}
		if status.Domain != "" {
			resp, e := client.Get(status.Domain)
			if e == nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
				resp.Body.Close()
				if resp.StatusCode == 200 && string(body) == "xui-tunnel-test-ok" {
					return
				}
				lastErr = fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
			} else {
				lastErr = e
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	manager.mu.Lock()
	worker := manager.workers["quick"]
	manager.mu.Unlock()
	worker.output.mu.Lock()
	tail := worker.output.tail
	worker.output.mu.Unlock()
	t.Fatalf("quick tunnel not reachable: %v; cloudflared output: %s", lastErr, tail)
}

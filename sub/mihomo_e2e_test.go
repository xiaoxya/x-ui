package sub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alireza0/x-ui/database/model"
)

type lockedTestBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedTestBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *lockedTestBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }

func TestMihomoXHTTPConnection(t *testing.T) {
	xray := os.Getenv("XRAY_TEST_BINARY")
	mihomo := os.Getenv("MIHOMO_TEST_BINARY")
	if xray == "" || mihomo == "" {
		t.Skip("set XRAY_TEST_BINARY and MIHOMO_TEST_BINARY")
	}
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "mihomo-xhttp-ok") }))
	defer origin.Close()
	originURL, _ := url.Parse(origin.URL)
	_, originPort, _ := net.SplitHostPort(originURL.Host)
	free := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	corePort, proxyPort := free(), free()
	id := "27848739-7e62-4138-9fd3-098a63964b6b"
	core := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": corePort, "protocol": "vless", "settings": map[string]any{"clients": []any{map[string]any{"id": id}}, "decryption": "none"}, "streamSettings": map[string]any{"network": "xhttp", "security": "tls", "tlsSettings": map[string]any{"certificates": []any{map[string]any{"certificateFile": certPath, "keyFile": keyPath}}}, "xhttpSettings": map[string]any{"path": "/api", "host": "localhost", "mode": "packet-up"}}}}, "outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": originPort}}}}}, "routing": map[string]any{"rules": []any{map[string]any{"type": "field", "network": "tcp", "outboundTag": "direct"}}}}
	coreData, _ := json.Marshal(core)
	corePath := filepath.Join(dir, "xray.json")
	if err = os.WriteFile(corePath, coreData, 0600); err != nil {
		t.Fatal(err)
	}
	in := inboundFor(model.VLESS, fmt.Sprintf(`{"clients":[{"id":%q,"email":"e"}],"decryption":"none"}`, id), `{"network":"xhttp","security":"tls","xhttpSettings":{"path":"/api","host":"localhost","mode":"packet-up"},"tlsSettings":{"serverName":"localhost","settings":{"allowInsecure":true}}}`)
	in.Port = corePort
	service := testService()
	service.address = "127.0.0.1"
	link := service.getLink(in, "e")
	generated, err := mihomoConfig([]string{link})
	if err != nil {
		t.Fatalf("generated link %q: %v", link, err)
	}
	var config map[string]any
	if err = json.Unmarshal(generated, &config); err != nil {
		t.Fatal(err)
	}
	config["mixed-port"] = proxyPort
	generated, _ = json.Marshal(config)
	clientPath := filepath.Join(dir, "mihomo.json")
	if err = os.WriteFile(clientPath, generated, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs lockedTestBuffer
	start := func(binary string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdout = &logs
		cmd.Stderr = &logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		})
		return cmd
	}
	start(xray, "run", "-c", corePath)
	start(mihomo, "-d", dir, "-f", clientPath)
	proxyURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(proxyPort))
	client := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(origin.URL)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) == "mihomo-xhttp-ok" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	t.Fatalf("XHTTP connection failed; link=%s logs=%s", link, logs.String())
}

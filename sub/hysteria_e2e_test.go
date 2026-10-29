package sub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alireza0/x-ui/database/model"
)

func TestHysteria2ClientConnections(t *testing.T) {
	xray := os.Getenv("XRAY_TEST_BINARY")
	mihomo := os.Getenv("MIHOMO_TEST_BINARY")
	singbox := os.Getenv("SINGBOX_TEST_BINARY")
	if xray == "" || mihomo == "" || singbox == "" {
		t.Skip("set XRAY_TEST_BINARY, MIHOMO_TEST_BINARY and SINGBOX_TEST_BINARY")
	}
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
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
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hysteria-e2e-ok") }))
	defer origin.Close()
	originURL, _ := url.Parse(origin.URL)
	_, originPort, _ := net.SplitHostPort(originURL.Host)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	corePort := udp.LocalAddr().(*net.UDPAddr).Port
	udp.Close()
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, peer, err := echo.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(buffer[:n], peer)
		}
	}()
	udpTarget := echo.LocalAddr().(*net.UDPAddr).Port
	password := "test-password"
	core := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": corePort, "protocol": "hysteria", "settings": map[string]any{"version": 2, "clients": []any{map[string]any{"auth": password}}}, "streamSettings": map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": map[string]any{"certificates": []any{map[string]any{"certificateFile": certPath, "keyFile": keyPath}}}, "hysteriaSettings": map[string]any{"version": 2}}}}, "outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": originPort}}}}}, "routing": map[string]any{"rules": []any{map[string]any{"type": "field", "network": "tcp", "outboundTag": "direct"}}}}
	direct := core["outbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)
	direct["finalRules"] = append(direct["finalRules"].([]any), map[string]any{"action": "allow", "network": "udp", "ip": []string{"127.0.0.1/32"}, "port": strconv.Itoa(udpTarget)})
	core["routing"].(map[string]any)["rules"] = []any{map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "direct"}}
	coreData, _ := json.Marshal(core)
	corePath := filepath.Join(dir, "xray.json")
	if err = os.WriteFile(corePath, coreData, 0600); err != nil {
		t.Fatal(err)
	}
	in := inboundFor(model.Hysteria, fmt.Sprintf(`{"version":2,"clients":[{"auth":%q,"email":"e"}]}`, password), `{"network":"hysteria","security":"tls","tlsSettings":{"serverName":"localhost","settings":{"allowInsecure":true}},"hysteriaSettings":{"version":2}}`)
	in.Port = corePort
	service := testService()
	service.address = "127.0.0.1"
	link := service.getLink(in, "e")
	relayA := newUDPForwarder(t, corePort)
	relayB := newUDPForwarder(t, corePort)
	var hopStream map[string]any
	if err = json.Unmarshal([]byte(in.StreamSettings), &hopStream); err != nil {
		t.Fatal(err)
	}
	hopStream["finalmask"] = map[string]any{"quicParams": map[string]any{"udpHop": map[string]any{"ports": fmt.Sprintf("%d,%d", relayA.port(), relayB.port()), "interval": "5"}}}
	hopJSON, _ := json.Marshal(hopStream)
	in.StreamSettings = string(hopJSON)
	hopLink := service.getLink(in, "e")
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
	for _, tc := range []struct {
		name, binary string
		generate     func([]string) ([]byte, error)
		hop          bool
	}{{"mihomo", mihomo, mihomoConfig, false}, {"singbox", singbox, singboxConfig, false}, {"mihomo-hop", mihomo, mihomoConfig, true}, {"singbox-hop", singbox, singboxConfig, true}} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			activeLink := link
			if tc.hop {
				activeLink = hopLink
				relayA.count.Store(0)
				relayB.count.Store(0)
			}
			raw, err := tc.generate([]string{activeLink})
			if err != nil {
				t.Fatalf("conversion failed: %v", err)
			}
			var cfg map[string]any
			if err = json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(tc.name, "mihomo") {
				cfg["mixed-port"] = port
			} else {
				cfg["inbounds"].([]any)[0].(map[string]any)["listen_port"] = port
			}
			raw, _ = json.Marshal(cfg)
			path := filepath.Join(dir, tc.name+".json")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			if strings.HasPrefix(tc.name, "mihomo") {
				cmd = start(tc.binary, "-d", dir, "-f", path)
			} else {
				cmd = start(tc.binary, "run", "-c", path)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
			client := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 2 * time.Second}
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				resp, err := client.Get(origin.URL)
				if err == nil {
					body, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					if string(body) == "hysteria-e2e-ok" {
						if err := socksUDPRoundTrip(port, udpTarget); err != nil {
							t.Fatalf("%s UDP failed: %v; logs: %s", tc.name, err, logs.String())
						}
						if tc.hop {
							deadline := time.Now().Add(40 * time.Second)
							for time.Now().Before(deadline) {
								r, e := client.Get(origin.URL)
								if e == nil {
									body, _ := io.ReadAll(r.Body)
									r.Body.Close()
									if string(body) == "hysteria-e2e-ok" && relayA.count.Load() > 0 && relayB.count.Load() > 0 {
										if err := socksUDPRoundTrip(port, udpTarget); err != nil {
											t.Fatalf("UDP after hop failed: %v", err)
										}
										return
									}
								}
								time.Sleep(200 * time.Millisecond)
							}
							t.Fatalf("%s did not use both relay ports: %d/%d; logs: %s", tc.name, relayA.count.Load(), relayB.count.Load(), logs.String())
						}
						return
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Fatalf("%s Hysteria2 connection failed: %s", tc.name, logs.String())
		})
	}
}

func socksUDPRoundTrip(proxyPort, targetPort int) error {
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), 3*time.Second)
	if err != nil {
		return err
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = control.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(control, reply); err != nil {
		return err
	}
	if reply[0] != 5 || reply[1] != 0 {
		return fmt.Errorf("SOCKS auth rejected")
	}
	if _, err = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	header := make([]byte, 4)
	if _, err = io.ReadFull(control, header); err != nil {
		return err
	}
	if header[0] != 5 || header[1] != 0 || header[3] != 1 {
		return fmt.Errorf("SOCKS UDP associate failed: %v", header)
	}
	address := make([]byte, 6)
	if _, err = io.ReadFull(control, address); err != nil {
		return err
	}
	ip := net.IP(address[:4])
	if ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}
	relay := &net.UDPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(address[4:]))}
	conn, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	packet := []byte{0, 0, 0, 1, 127, 0, 0, 1, byte(targetPort >> 8), byte(targetPort)}
	packet = append(packet, []byte("hysteria-udp-ok")...)
	if _, err = conn.Write(packet); err != nil {
		return err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return err
	}
	if n < 10 || string(buffer[10:n]) != "hysteria-udp-ok" {
		return fmt.Errorf("invalid UDP response")
	}
	return nil
}

// udpForwarder models one NAT hop port without modifying host firewall rules.
type udpForwarder struct {
	listen   *net.UDPConn
	count    atomic.Int64
	mu       sync.Mutex
	upstream map[string]*net.UDPConn
	target   *net.UDPAddr
}

func (r *udpForwarder) port() int { return r.listen.LocalAddr().(*net.UDPAddr).Port }
func newUDPForwarder(t *testing.T, targetPort int) *udpForwarder {
	listen, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	r := &udpForwarder{listen: listen, upstream: map[string]*net.UDPConn{}, target: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: targetPort}}
	t.Cleanup(func() {
		listen.Close()
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, conn := range r.upstream {
			conn.Close()
		}
	})
	go func() {
		buf := make([]byte, 65535)
		for {
			n, peer, err := listen.ReadFromUDP(buf)
			if err != nil {
				return
			}
			r.count.Add(1)
			key := peer.String()
			r.mu.Lock()
			conn := r.upstream[key]
			if conn == nil {
				conn, err = net.DialUDP("udp", nil, r.target)
				if err == nil {
					r.upstream[key] = conn
					go func(c *net.UDPConn, client *net.UDPAddr) {
						reply := make([]byte, 65535)
						for {
							n, err := c.Read(reply)
							if err != nil {
								return
							}
							_, _ = listen.WriteToUDP(reply[:n], client)
						}
					}(conn, peer)
				}
			}
			r.mu.Unlock()
			if err == nil {
				_, _ = conn.Write(buf[:n])
			}
		}
	}()
	return r
}

package deployment

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CheckPanelHealth authenticates to the panel using its local database, then
// verifies that its status endpoint reports a running Xray process.
func CheckPanelHealth(dbPath string, timeout time.Duration) error {
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro&_busy_timeout=5000"}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	var username, password string
	if err = db.QueryRow("SELECT username, password FROM users ORDER BY id LIMIT 1").Scan(&username, &password); err != nil {
		return fmt.Errorf("read panel user: %w", err)
	}
	if username == "" || password == "" {
		return errors.New("panel user has empty credentials")
	}
	settings := map[string]string{"webPort": "54321", "webBasePath": "/"}
	rows, err := db.Query("SELECT key, value FROM settings WHERE key IN ('webPort','webListen','webBasePath','webDomain','webCertFile','webKeyFile')")
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		settings[key] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(settings["webPort"])
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid panel port")
	}
	address := settings["webListen"]
	if address == "" || address == "0.0.0.0" {
		address = "127.0.0.1"
	}
	if address == "::" {
		address = "::1"
	}
	if address == "localhost" {
		address = "127.0.0.1"
	}
	address = strings.TrimPrefix(strings.TrimSuffix(address, "]"), "[")
	ip := net.ParseIP(address)
	if ip == nil {
		return errors.New("panel health check requires a local IP listen address")
	}
	if !ip.IsLoopback() {
		addresses, err := net.InterfaceAddrs()
		if err != nil {
			return err
		}
		local := false
		for _, a := range addresses {
			candidate, _, err := net.ParseCIDR(a.String())
			if err == nil && candidate.Equal(ip) {
				local = true
				break
			}
		}
		if !local {
			return errors.New("panel listen address is not local")
		}
	}
	basePath := settings["webBasePath"]
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}
	if strings.ContainsAny(basePath, "?#\r\n") {
		return errors.New("invalid panel base path")
	}
	base := &url.URL{Scheme: "http", Host: net.JoinHostPort(address, strconv.Itoa(port)), Path: basePath}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	if settings["webCertFile"] != "" || settings["webKeyFile"] != "" {
		if settings["webCertFile"] == "" || settings["webKeyFile"] == "" {
			return errors.New("incomplete panel TLS configuration")
		}
		certPath := settings["webCertFile"]
		if !filepath.IsAbs(certPath) {
			certPath = filepath.Join("/usr/local/x-ui", certPath)
		}
		data, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read panel certificate: %w", err)
		}
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("invalid panel certificate")
		}
		expected := sha256.Sum256(block.Bytes)
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("missing server certificate")
			}
			actual := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
				return errors.New("panel certificate mismatch")
			}
			return nil
		}}
		transport.TLSClientConfig.ServerName = settings["webDomain"]
		base.Scheme = "https"
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request := func(method, endpoint string, body io.Reader) (*http.Response, error) {
		u := *base
		u.Path = base.Path + endpoint
		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return nil, err
		}
		if settings["webDomain"] != "" {
			req.Host = net.JoinHostPort(settings["webDomain"], strconv.Itoa(port))
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return client.Do(req)
	}
	var lastErr error
	loggedIn := false
	for ctx.Err() == nil {
		var e error
		if !loggedIn {
			form := url.Values{"username": {username}, "password": {password}}
			var resp *http.Response
			resp, e = request(http.MethodPost, "login", strings.NewReader(form.Encode()))
			if e == nil {
				var result struct {
					Success bool `json:"success"`
				}
				e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
				resp.Body.Close()
				loggedIn = e == nil && resp.StatusCode == http.StatusOK && result.Success
			}
		}
		if loggedIn {
			var resp *http.Response
			resp, e = request(http.MethodGet, "xui/API/server/status", nil)
			if e == nil {
				var status struct {
					Success bool `json:"success"`
					Obj     *struct {
						Xray struct {
							State string `json:"state"`
						} `json:"xray"`
					} `json:"obj"`
				}
				e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status)
				resp.Body.Close()
				if e == nil && resp.StatusCode == http.StatusOK && status.Success && status.Obj != nil && status.Obj.Xray.State == "Running" {
					return nil
				}
			}
		}
		if e != nil {
			lastErr = e
		} else {
			lastErr = errors.New("panel login or Xray status not ready")
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("panel health check failed: %w", lastErr)
}

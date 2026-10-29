package deployment

import (
	"database/sql"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPanelHealthTLSCertificatePinAndXrayStatus(t *testing.T) {
	var state atomic.Value
	state.Store("Running")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test/login":
			if r.FormValue("username") != "alice" || r.FormValue("password") != "secret" {
				http.Error(w, "bad login", 403)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
			fmt.Fprint(w, `{"success":true}`)
		case "/test/xui/API/server/status":
			if c, err := r.Cookie("session"); err != nil || c.Value != "ok" {
				http.Error(w, "unauthorized", 403)
				return
			}
			fmt.Fprintf(w, `{"success":true,"obj":{"xray":{"state":%q}}}`, state.Load().(string))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "db.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE users (id INTEGER, username TEXT, password TEXT)", "CREATE TABLE settings (key TEXT, value TEXT)", "INSERT INTO users VALUES (1,'alice','secret')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{"webPort": port, "webListen": "127.0.0.1", "webBasePath": "/test/", "webCertFile": certPath, "webKeyFile": "present"} {
		if _, err := db.Exec("INSERT INTO settings VALUES (?,?)", k, v); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err := CheckPanelHealth(dbPath, 2*time.Second); err != nil {
		t.Fatalf("healthy TLS panel: %v", err)
	}
	state.Store("Stop")
	if err := CheckPanelHealth(dbPath, 200*time.Millisecond); err == nil {
		t.Fatal("stopped Xray accepted")
	}
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer other.Close()
	// A different pinned certificate must prevent sending panel credentials.
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	// httptest servers use the same certificate, so use a deliberately altered DER.
	bad := append([]byte(nil), other.Certificate().Raw...)
	bad[len(bad)-1] ^= 1
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bad}), 0600); err != nil {
		t.Fatal(err)
	}
	state.Store("Running")
	if err := CheckPanelHealth(dbPath, 200*time.Millisecond); err == nil {
		t.Fatal("mismatched TLS certificate accepted")
	}
}

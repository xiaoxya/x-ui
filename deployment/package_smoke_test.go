package deployment

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestLocalPackageBoot(t *testing.T) {
	packageDir := os.Getenv("XUI_PACKAGE_TEST_DIR")
	if packageDir == "" {
		t.Skip("set XUI_PACKAGE_TEST_DIR to a local build")
	}
	packageDir, err := filepath.Abs(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	// Starting Xray writes bin/config.json. Run from a private copy so the
	// tested package remains byte-for-byte identical to what is released.
	copyDir := filepath.Join(t.TempDir(), "package")
	if output, err := exec.Command("cp", "-a", packageDir, copyDir).CombinedOutput(); err != nil {
		t.Fatalf("copy package for smoke test: %v %s", err, output)
	}
	packageDir = copyDir
	dbDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	exe := filepath.Join(packageDir, "x-ui")
	env := append(os.Environ(), "XUI_DB_FOLDER="+dbDir, "XUI_BIN_FOLDER="+filepath.Join(packageDir, "bin"), "XUI_PORTHOP_AUTO=0")
	setup := exec.Command(exe, "setting", "-port", strconv.Itoa(port), "-username", "smoke-user", "-password", "smoke-pass", "-webBasePath", "/smoke/")
	setup.Dir, setup.Env = packageDir, env
	if output, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("setup: %v %s", err, output)
	}
	db, err := sql.Open("sqlite3", filepath.Join(dbDir, "x-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", "webListen", "127.0.0.1"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	logPath := filepath.Join(dbDir, "panel.log")
	logs, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	process := exec.Command(exe, "run")
	process.Dir, process.Env, process.Stdout, process.Stderr = packageDir, env, logs, logs
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = process.Process.Kill()
			<-done
		}
	}()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(port) + "/smoke/"
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		resp, err := client.Get(base)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		output, _ := os.ReadFile(logPath)
		t.Fatalf("panel did not start: %s", output)
	}
	resp, err := client.PostForm(base+"login", url.Values{"username": {"smoke-user"}, "password": {"smoke-pass"}})
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		Success bool `json:"success"`
	}
	err = json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	if err != nil || !login.Success {
		t.Fatalf("login failed: %v %+v", err, login)
	}
	if err := CheckPanelHealth(filepath.Join(dbDir, "x-ui.db"), 10*time.Second); err != nil {
		t.Fatalf("production health check: %v", err)
	}
	var status struct {
		Obj struct {
			Xray struct{ State, Version, ErrorMsg string } `json:"xray"`
		} `json:"obj"`
	}
	deadline = time.Now().Add(8 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		resp, err = client.Get(base + "xui/API/server/status")
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"success":true`)) {
			t.Fatalf("authenticated status: %d %s", resp.StatusCode, body)
		}
		if err = json.Unmarshal(body, &status); err != nil {
			t.Fatal(err)
		}
		if status.Obj.Xray.State == "Running" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("packaged Xray is not running: %+v, response %s", status.Obj.Xray, body)
}

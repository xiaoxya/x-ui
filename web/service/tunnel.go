package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/x-ui/config"
)

type TunnelConfig struct {
	Mode  string `json:"mode" form:"mode"`
	Port  int    `json:"port" form:"port"`
	Token string `json:"token" form:"token"`
}
type TunnelStatus struct {
	Mode    string `json:"mode"`
	Running bool   `json:"running"`
	Port    int    `json:"port,omitempty"`
	Domain  string `json:"domain,omitempty"`
	Error   string `json:"error,omitempty"`
}
type tunnelWorker struct {
	cmd    *exec.Cmd
	done   chan struct{}
	status TunnelStatus
	output *tunnelOutput
}
type tunnelManager struct {
	mu      sync.Mutex
	workers map[string]*tunnelWorker
}

var managedTunnels = &tunnelManager{workers: make(map[string]*tunnelWorker)}
var quickDomain = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

type tunnelOutput struct {
	mu     sync.Mutex
	tail   string
	domain string
}

func (w *tunnelOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tail += string(p)
	for _, match := range quickDomain.FindAllString(w.tail, -1) {
		if !strings.EqualFold(match, "https://api.trycloudflare.com") {
			w.domain = match
		}
	}
	if len(w.tail) > 4096 {
		w.tail = w.tail[len(w.tail)-4096:]
	}
	return len(p), nil
}
func (w *tunnelOutput) Domain() string { w.mu.Lock(); defer w.mu.Unlock(); return w.domain }
func tunnelPath(mode string) string {
	return filepath.Join(config.GetDBFolderPath(), "cloudflared-"+mode+".json")
}
func validateTunnel(c TunnelConfig) error {
	switch c.Mode {
	case "quick":
		if c.Port < 1 || c.Port > 65535 {
			return errors.New("invalid local port")
		}
		if c.Token != "" {
			return errors.New("quick tunnel does not use a token")
		}
	case "named":
		if c.Port != 0 || strings.TrimSpace(c.Token) == "" || len(c.Token) > 8192 || strings.ContainsAny(c.Token, "\r\n\x00") {
			return errors.New("invalid tunnel token")
		}
	default:
		return errors.New("invalid tunnel mode")
	}
	return nil
}
func saveTunnel(c TunnelConfig) error {
	if err := os.MkdirAll(config.GetDBFolderPath(), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(config.GetDBFolderPath(), ".cloudflared-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	encErr := json.NewEncoder(f).Encode(c)
	if encErr == nil {
		encErr = f.Sync()
	}
	closeErr := f.Close()
	if encErr != nil {
		return encErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), tunnelPath(c.Mode))
}
func (m *tunnelManager) start(c TunnelConfig, persist bool) error {
	if err := validateTunnel(c); err != nil {
		return err
	}
	binary := os.Getenv("XUI_CLOUDFLARED_BINARY")
	if binary == "" {
		binary = "cloudflared"
	}
	args := []string{"tunnel", "--no-autoupdate"}
	if c.Mode == "quick" {
		args = append(args, "--url", "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port)))
	} else {
		args = append(args, "run")
	}
	cmd := exec.Command(binary, args...)
	// Do not inherit stale Cloudflare credentials from the panel environment.
	env := os.Environ()
	filtered := make([]string, 0, len(env)+1)
	for _, value := range env {
		if !strings.HasPrefix(value, "TUNNEL_TOKEN=") && !strings.HasPrefix(value, "TUNNEL_TOKEN_FILE=") {
			filtered = append(filtered, value)
		}
	}
	if c.Mode == "named" {
		filtered = append(filtered, "TUNNEL_TOKEN="+c.Token)
	}
	cmd.Env = filtered

	output := &tunnelOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start cloudflared: %w", err)
	}
	if persist {
		if err := saveTunnel(c); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("cannot save tunnel configuration: %w", err)
		}
	}
	old := m.workers[c.Mode]
	w := &tunnelWorker{cmd: cmd, done: make(chan struct{}), output: output, status: TunnelStatus{Mode: c.Mode, Running: true, Port: c.Port}}
	m.workers[c.Mode] = w
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		w.status.Running = false
		if err != nil {
			w.status.Error = "cloudflared exited"
		}
		close(w.done)
		m.mu.Unlock()
	}()
	if old != nil && old.status.Running {
		_ = old.cmd.Process.Kill()
	}
	return nil
}
func (m *tunnelManager) stop(mode string, remove bool) error {
	if mode != "quick" && mode != "named" {
		return errors.New("invalid tunnel mode")
	}
	m.mu.Lock()
	w := m.workers[mode]
	if remove {
		if err := os.Remove(tunnelPath(mode)); err != nil && !os.IsNotExist(err) {
			m.mu.Unlock()
			return err
		}
	}
	if w == nil || !w.status.Running {
		m.mu.Unlock()
		return nil
	}
	err := w.cmd.Process.Kill()
	done := w.done
	m.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("cloudflared did not exit")
	}
}
func (m *tunnelManager) statuses() []TunnelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TunnelStatus, 0, 2)
	for _, mode := range []string{"quick", "named"} {
		w := m.workers[mode]
		if w == nil {
			out = append(out, TunnelStatus{Mode: mode})
			continue
		}
		s := w.status
		if mode == "quick" && s.Running {
			s.Domain = w.output.Domain()
		}
		out = append(out, s)
	}
	return out
}

var tunnelOps sync.Mutex

func StartTunnel(c TunnelConfig) error {
	tunnelOps.Lock()
	defer tunnelOps.Unlock()
	return managedTunnels.start(c, true)
}
func StopTunnel(mode string) error {
	tunnelOps.Lock()
	defer tunnelOps.Unlock()
	return managedTunnels.stop(mode, true)
}
func TunnelStatuses() []TunnelStatus { return managedTunnels.statuses() }
func setTunnelRestoreError(mode, message string) {
	managedTunnels.mu.Lock()
	defer managedTunnels.mu.Unlock()
	managedTunnels.workers[mode] = &tunnelWorker{output: &tunnelOutput{}, status: TunnelStatus{Mode: mode, Error: message}}
}

// RestoreTunnels starts persisted desired tunnels after panel startup.
func RestoreTunnels() error {
	tunnelOps.Lock()
	defer tunnelOps.Unlock()
	var problems []string
	for _, mode := range []string{"quick", "named"} {
		managedTunnels.mu.Lock()
		worker := managedTunnels.workers[mode]
		running := worker != nil && worker.status.Running
		managedTunnels.mu.Unlock()
		if running {
			continue
		}
		data, err := os.ReadFile(tunnelPath(mode))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			setTunnelRestoreError(mode, "cannot read configuration")
			problems = append(problems, mode+": cannot read configuration")
			continue
		}
		var c TunnelConfig
		if json.Unmarshal(data, &c) != nil || c.Mode != mode {
			setTunnelRestoreError(mode, "invalid configuration")
			problems = append(problems, mode+": invalid configuration")
			continue
		}
		if err = managedTunnels.start(c, false); err != nil {
			setTunnelRestoreError(mode, "cannot start cloudflared")
			problems = append(problems, mode+": cannot start")
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// StopTunnelsForShutdown preserves desired settings for the next startup.
func StopTunnelsForShutdown() {
	tunnelOps.Lock()
	defer tunnelOps.Unlock()
	for _, mode := range []string{"quick", "named"} {
		_ = managedTunnels.stop(mode, false)
	}
}

var _ io.Writer = (*tunnelOutput)(nil)

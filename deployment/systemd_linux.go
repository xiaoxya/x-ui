//go:build linux

package deployment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const standardUnit = `[Unit]
Description=x-ui Service
After=network.target
Wants=network.target
[Service]
Environment="XRAY_VMESS_AEAD_FORCED=false"
Type=simple
WorkingDirectory=/usr/local/x-ui/
ExecStart=/usr/local/x-ui/x-ui
ExecReload=kill -USR1 $MAINPID
Restart=on-failure
[Install]
WantedBy=multi-user.target`

func ValidateStandardUnit(data []byte) error {
	normalize := func(s string) string {
		var lines []string
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ";") {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	if normalize(string(data)) != normalize(standardUnit) {
		return errors.New("custom systemd unit requires manual migration")
	}
	return nil
}

// Systemd operates on the fixed x-ui service. Path can point to a test double.
type Systemd struct{ Path string }

func (s Systemd) command(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Path, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("systemctl %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (s Systemd) Active() (bool, error) {
	state, err := s.command("show", "--property=ActiveState", "--value", "x-ui.service")
	if err != nil {
		return false, err
	}
	switch state {
	case "active":
		return true, nil
	case "inactive", "failed":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected systemd state %q", state)
	}
}
func (s Systemd) Stop() error    { _, err := s.command("stop", "x-ui.service"); return err }
func (s Systemd) Reload() error  { _, err := s.command("daemon-reload"); return err }
func (s Systemd) Enable() error  { _, err := s.command("enable", "x-ui.service"); return err }
func (s Systemd) Disable() error { _, err := s.command("disable", "x-ui.service"); return err }
func (s Systemd) Exists() (bool, error) {
	state, err := s.command("show", "--property=LoadState", "--value", "x-ui.service")
	if err != nil {
		return false, err
	}
	switch state {
	case "not-found":
		return false, nil
	case "loaded", "masked", "error", "bad-setting":
		return true, nil
	default:
		return false, fmt.Errorf("unexpected systemd load state %q", state)
	}
}

// ValidateStandardUpgrade checks the effective unit before touching the
// database or service. Drop-ins and service-specific XUI environment variables
// can redirect data or change behavior and cannot be safely replaced blindly.
func (s Systemd) ValidateStandardUpgrade() error {
	const properties = "LoadState,FragmentPath,DropInPaths,NeedDaemonReload,ExecStart,Environment,EnvironmentFiles,WorkingDirectory,RootDirectory,RootImage,User,Group,DynamicUser,ActiveState,MainPID"
	output, err := s.command("show", "x-ui.service", "--all", "--property="+properties, "--no-pager")
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	for key, want := range map[string]string{"LoadState": "loaded", "FragmentPath": "/etc/systemd/system/x-ui.service", "NeedDaemonReload": "no", "DynamicUser": "no"} {
		if values[key] != want {
			return fmt.Errorf("unsupported systemd %s", key)
		}
	}
	if strings.TrimSuffix(values["WorkingDirectory"], "/") != "/usr/local/x-ui" {
		return fmt.Errorf("unsupported systemd WorkingDirectory")
	}
	for _, key := range []string{"DropInPaths", "EnvironmentFiles", "RootDirectory", "RootImage"} {
		if values[key] != "" {
			return fmt.Errorf("unsupported systemd %s", key)
		}
	}
	for _, key := range []string{"User", "Group"} {
		if values[key] != "" && values[key] != "root" {
			return fmt.Errorf("unsupported systemd %s", key)
		}
	}
	start := values["ExecStart"]
	if strings.Count(start, "{ path=") != 1 || (!strings.HasPrefix(start, "{ path=/usr/local/x-ui/x-ui ; argv[]=/usr/local/x-ui/x-ui ;") && !strings.HasPrefix(start, "{ path=/usr/local/x-ui/x-ui ; argv[]=/usr/local/x-ui/x-ui run ;")) {
		return fmt.Errorf("unsupported systemd ExecStart")
	}
	for _, v := range strings.Fields(values["Environment"]) {
		if v != "XRAY_VMESS_AEAD_FORCED=false" {
			return errors.New("unsupported systemd Environment")
		}
	}
	switch values["ActiveState"] {
	case "active":
		pid, err := strconv.Atoi(values["MainPID"])
		if err != nil || pid <= 0 {
			return errors.New("cannot identify running panel process")
		}
		if err = validateRunningProcess("/proc", pid, "/usr/local/x-ui/x-ui", "/usr/local/x-ui"); err != nil {
			return err
		}
	case "inactive", "failed":
	default:
		return errors.New("unsupported systemd ActiveState")
	}
	return nil
}

// Read only names, never values, from the process environment; errors must not
// reveal credentials possibly present in that environment.
func validateRunningProcess(procRoot string, pid int, executable, workingDir string) error {
	base := filepath.Join(procRoot, strconv.Itoa(pid))
	for _, pair := range [][2]string{{filepath.Join(base, "exe"), executable}, {filepath.Join(base, "cwd"), workingDir}} {
		actual, err := os.Stat(pair[0])
		if err != nil {
			return errors.New("cannot inspect running panel process")
		}
		expected, err := os.Stat(pair[1])
		if err != nil || !os.SameFile(actual, expected) {
			return errors.New("running panel executable or working directory differs from installation")
		}
	}
	environment, err := os.ReadFile(filepath.Join(base, "environ"))
	if err != nil {
		return errors.New("cannot inspect running panel environment")
	}
	if len(environment) == 0 {
		return errors.New("running panel environment is empty or unavailable")
	}
	for _, entry := range strings.Split(string(environment), "\x00") {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(name, "XUI_") || strings.HasPrefix(name, "XRAY_LOCATION_") {
			return errors.New("running panel has unsupported environment overrides")
		}
	}
	return nil
}
func (s Systemd) Start() error {
	if _, err := s.command("start", "x-ui.service"); err != nil {
		return err
	}
	// A simple service can report active and immediately crash. Require it to
	// remain active during a short stabilization window.
	for i := 0; i < 3; i++ {
		time.Sleep(time.Second)
		active, err := s.Active()
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("x-ui service exited during startup")
		}
	}
	return nil
}

//go:build linux

package deployment

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInstallationLockReleasedAfterProcessKill(t *testing.T) {
	if path := os.Getenv("XUI_LOCK_CHILD_PATH"); path != "" {
		f, err := Lock(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Println("lock-held")
		for {
			time.Sleep(time.Hour)
		}
	}
	path := filepath.Join(t.TempDir(), "lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallationLockReleasedAfterProcessKill$")
	cmd.Env = append(os.Environ(), "XUI_LOCK_CHILD_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "lock-held\n" {
		t.Fatalf("child did not acquire lock: %q %v", line, err)
	}
	if f, err := Lock(path); err == nil {
		f.Close()
		t.Fatal("lock acquired while child was running")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("expected child to be killed")
	}
	f, err := Lock(path)
	if err != nil {
		t.Fatalf("lock remained held after process termination: %v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallationLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")
	first, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Lock(path); err == nil {
		second.Close()
		t.Fatal("second installer acquired lock")
	}
	first.Close()
	second, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if f, err := Lock(link); err == nil {
		f.Close()
		t.Fatal("symlink accepted")
	}
}

func TestInstallationLockRejectsUnsafeExistingFiles(t *testing.T) {
	for _, mode := range []string{"group-writable", "world-writable", "hard-link", "directory"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "lock")
			if mode == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "group-writable":
					if err := os.Chmod(path, 0620); err != nil {
						t.Fatal(err)
					}
				case "world-writable":
					if err := os.Chmod(path, 0602); err != nil {
						t.Fatal(err)
					}
				case "hard-link":
					if err := os.Link(path, filepath.Join(dir, "other")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if f, err := Lock(path); err == nil {
				f.Close()
				t.Fatal("unsafe lock accepted")
			}
			if mode != "directory" {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "preserve" {
					t.Fatalf("existing lock changed: %q %v", data, err)
				}
			}
		})
	}
}

func TestStandardSystemdUpgradePreflight(t *testing.T) {
	unit, err := os.ReadFile("../x-ui.service")
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateStandardUnit(unit); err != nil {
		t.Fatal(err)
	}
	if err = ValidateStandardUnit(append(unit, []byte("\nEnvironment=XUI_DB_FOLDER=/other\n")...)); err == nil {
		t.Fatal("custom unit accepted")
	}
	base := `LoadState=loaded
FragmentPath=/etc/systemd/system/x-ui.service
DropInPaths=
NeedDaemonReload=no
ExecStart={ path=/usr/local/x-ui/x-ui ; argv[]=/usr/local/x-ui/x-ui ; ignore_errors=no ; }
Environment=XRAY_VMESS_AEAD_FORCED=false
WorkingDirectory=/usr/local/x-ui/
RootDirectory=
RootImage=
User=
Group=
DynamicUser=no`
	base += "\nActiveState=inactive\nMainPID=0"
	cases := []struct {
		name, output string
		fail         bool
	}{
		{"standard", base, false},
		{"override", strings.Replace(base, "DropInPaths=", "DropInPaths=/etc/systemd/system/x-ui.service.d/custom.conf", 1), true},
		{"database override", strings.Replace(base, "Environment=XRAY_VMESS_AEAD_FORCED=false", "Environment=XUI_DB_FOLDER=/other", 1), true},
		{"nonroot", strings.Replace(base, "User=\n", "User=other\n", 1), true},
		{"different command", strings.Replace(base, "path=/usr/local/x-ui/x-ui", "path=/other/x-ui", 1), true},
		{"pending reload", strings.Replace(base, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "systemctl")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\ncat <<'OUTPUT'\n"+tc.output+"\nOUTPUT\n"), 0700); err != nil {
				t.Fatal(err)
			}
			err := (Systemd{Path: binary}).ValidateStandardUpgrade()
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected preflight error: %v", err)
			}
		})
	}
}

func TestSystemdStateRejectsUnexpectedValues(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "systemctl")
	write := func(state string) {
		t.Helper()
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+state+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		state          string
		active, failed bool
	}{{"active", true, false}, {"inactive", false, false}, {"failed", false, false}, {"activating", false, true}, {"", false, true}} {
		write(tc.state)
		active, err := (Systemd{Path: binary}).Active()
		if active != tc.active || (err != nil) != tc.failed {
			t.Fatalf("state %q: active=%v err=%v", tc.state, active, err)
		}
	}
}

func TestRunningProcessPreflight(t *testing.T) {
	dir := t.TempDir()
	proc := filepath.Join(dir, "proc")
	p := filepath.Join(proc, "123")
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(work, "x-ui")
	if err := os.WriteFile(executable, []byte("executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(p, "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(work, filepath.Join(p, "cwd")); err != nil {
		t.Fatal(err)
	}
	environment := filepath.Join(p, "environ")
	if err := os.WriteFile(environment, []byte("PATH=/usr/bin\x00XRAY_VMESS_AEAD_FORCED=false\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateRunningProcess(proc, 123, executable, work); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environment, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateRunningProcess(proc, 123, executable, work); err == nil {
		t.Fatal("empty environment accepted")
	}
	if err := os.WriteFile(environment, []byte("XUI_DB_FOLDER=/different\x00SECRET=do-not-log\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	err := validateRunningProcess(proc, 123, executable, work)
	if err == nil || strings.Contains(err.Error(), "do-not-log") || strings.Contains(err.Error(), "/different") {
		t.Fatalf("environment handling: %v", err)
	}
	if err := os.WriteFile(environment, []byte("PATH=/usr/bin\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("different"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateRunningProcess(proc, 123, other, work); err == nil {
		t.Fatal("different executable accepted")
	}
	if err := validateRunningProcess(proc, 123, executable, dir); err == nil {
		t.Fatal("different working directory accepted")
	}
}

func TestRunningProcessPreflightAgainstProcFS(t *testing.T) {
	work := t.TempDir()
	for _, tc := range []struct {
		name, environment string
		fail              bool
	}{{"standard", "PATH=/usr/bin", false}, {"redirected-database", "XUI_DB_FOLDER=/other", true}} {
		t.Run(tc.name, func(t *testing.T) {
			process := exec.Command("/usr/bin/sleep", "30")
			process.Dir = work
			process.Env = []string{tc.environment}
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = process.Process.Kill(); _ = process.Wait() }()
			envPath := filepath.Join("/proc", strconv.Itoa(process.Process.Pid), "environ")
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(envPath)
				if err == nil && len(data) > 0 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			err := validateRunningProcess("/proc", process.Process.Pid, "/usr/bin/sleep", work)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected process preflight: %v", err)
			}
		})
	}
}

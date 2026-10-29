package deployment

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocalManagementForwardsArgumentsAndFailures(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "package with spaces")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "xui-upgrade")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$XUI_TEST_ARG_LOG\"\nexit \"${XUI_TEST_EXIT:-0}\"\n"
	if err = os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode            string
		extra, expected []string
	}{
		{"install", []string{"23456"}, []string{"-install", "-package", dir, "-port", "23456"}},
		{"upgrade", nil, []string{"-package", dir}},
		{"recover", nil, []string{"-recover"}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "arguments")
			args := append([]string{filepath.Join(root, "deployment/manage-local.sh"), tc.mode, dir}, tc.extra...)
			cmd := exec.Command("bash", args...)
			cmd.Env = append(os.Environ(), "XUI_TEST_ARG_LOG="+log)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("command: %v: %s", err, out)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("arguments %q, want %q", got, tc.expected)
			}
		})
	}
	cmd := exec.Command("bash", filepath.Join(root, "deployment/manage-local.sh"), "upgrade", dir)
	cmd.Env = append(os.Environ(), "XUI_TEST_ARG_LOG="+filepath.Join(t.TempDir(), "arguments"), "XUI_TEST_EXIT=37")
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 37 {
		t.Fatalf("failure not propagated: %v", err)
	}
	cmd = exec.Command("bash", filepath.Join(root, "deployment/manage-local.sh"), "upgrade", dir, "unexpected")
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 2 {
		t.Fatalf("extra argument accepted: %v", err)
	}
}

func TestInstallerLocalEntry(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	helper, err := os.ReadFile(filepath.Join(root, "deployment/manage-local.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "xui-manage-local"), helper, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "xui-upgrade"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$XUI_TEST_ARG_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "arguments")
	cmd := exec.Command("bash", filepath.Join(root, "install.sh"), "--local", dir, "23456")
	cmd.Env = append(os.Environ(), "XUI_TEST_ARG_LOG="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installer entry: %v: %s", err, out)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-install", "-package", dir, "-port", "23456"}
	if args := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n"); !reflect.DeepEqual(args, want) {
		t.Fatalf("arguments %q, want %q", args, want)
	}
}

func TestPackageInstaller(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "package with spaces")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("install-package.sh")
	if err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(dir, "install.sh")
	if err = os.WriteFile(installer, data, 0755); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$XUI_TEST_ARG_LOG\"\nexit \"${XUI_TEST_EXIT:-0}\"\n"
	if err = os.WriteFile(filepath.Join(dir, "xui-upgrade"), []byte(fake), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		port string
		exit string
		ok   bool
	}{
		{"default", nil, "2053", "0", true}, {"custom", []string{"23456"}, "23456", "0", true},
		{"zero", []string{"0"}, "", "0", false}, {"out of range", []string{"65536"}, "", "0", false},
		{"invalid", []string{"abc"}, "", "0", false}, {"extra", []string{"123", "456"}, "", "0", false},
		{"failure propagated", nil, "2053", "37", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "args")
			cmd := exec.Command("bash", append([]string{installer}, tc.args...)...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "XUI_TEST_ARG_LOG="+log, "XUI_TEST_EXIT="+tc.exit)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected result: %v: %s", err, out)
			}
			if tc.port == "" {
				if _, err := os.Stat(log); !os.IsNotExist(err) {
					t.Fatal("invalid input reached installer")
				}
				return
			}
			got, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"-install", "-package", dir, "-port", tc.port}
			if args := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n"); !reflect.DeepEqual(args, want) {
				t.Fatalf("arguments %q, want %q", args, want)
			}
			if tc.exit == "37" && cmd.ProcessState.ExitCode() != 37 {
				t.Fatalf("exit code %d", cmd.ProcessState.ExitCode())
			}
		})
	}
}

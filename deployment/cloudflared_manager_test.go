package deployment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudflaredPackageManager(t *testing.T) {
	data, err := os.ReadFile("../x-ui.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	start := strings.Index(script, "cloudflared_package() (")
	end := strings.Index(script, "\ncloudflared_menu() {")
	if start < 0 || end < start {
		t.Fatal("cloudflared manager missing")
	}
	for _, tc := range []struct {
		name, action            string
		failDownload, installed bool
		success                 bool
	}{
		{"install", "install", false, false, true},
		{"update missing package", "update", false, false, false},
		{"download fails", "install", true, false, false},
		{"update installed", "update", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0755); err != nil {
				t.Fatal(err)
			}
			commands := map[string]string{
				"dpkg-query": "#!/bin/bash\nif [[ $1 == -W && $FAKE_INSTALLED == 1 ]]; then printf 'install ok installed'; exit 0; fi\nif [[ $1 == -S && $FAKE_INSTALLED == 1 ]]; then echo cloudflared:; exit 0; fi\nexit 1\n",
				"curl":       "#!/bin/bash\n[[ $FAKE_FAIL == 1 ]] && exit 1\nwhile [[ $# -gt 0 ]]; do if [[ $1 == -o ]]; then printf key > \"$2\"; exit; fi; shift; done\nexit 1\n",
				"apt-get":    "#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$FAKE_LOG\"\nif [[ $1 == install ]]; then printf '#!/bin/sh\\necho cloudflared-test\\n' > \"$FAKE_BIN/cloudflared\"; chmod +x \"$FAKE_BIN/cloudflared\"; fi\n",
			}
			if tc.installed {
				commands["cloudflared"] = "#!/bin/sh\necho cloudflared-test\n"
			}
			for name, contents := range commands {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(contents), 0755); err != nil {
					t.Fatal(err)
				}
			}
			body := strings.ReplaceAll(script[start:end], "/usr/share/keyrings", filepath.Join(root, "keyrings"))
			body = strings.ReplaceAll(body, "/etc/apt/sources.list.d", filepath.Join(root, "sources"))
			cmd := exec.Command("bash", "-c", "LOGE() { echo \"$*\"; }; LOGI() { echo \"$*\"; };\n"+body+"\ncloudflared_package "+tc.action)
			cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "FAKE_LOG="+filepath.Join(root, "apt.log"), "FAKE_BIN="+bin)
			if tc.failDownload {
				cmd.Env = append(cmd.Env, "FAKE_FAIL=1")
			}
			if tc.installed {
				cmd.Env = append(cmd.Env, "FAKE_INSTALLED=1")
			}
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("unexpected result: %v: %s", err, out)
			}
			log, _ := os.ReadFile(filepath.Join(root, "apt.log"))
			if !tc.success && len(log) > 0 {
				t.Fatalf("APT ran after preflight failure: %s", log)
			}
			if tc.success && (!strings.Contains(string(log), "update") || !strings.Contains(string(log), "install")) {
				t.Fatalf("missing APT calls: %s", log)
			}
		})
	}
}

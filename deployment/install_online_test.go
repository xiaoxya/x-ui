package deployment

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlineInstaller(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	pkg := filepath.Join(root, "source", "xui-package")
	for _, dir := range []string{bin, pkg} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	script := []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" > \"$XUI_FAKE_INSTALL_LOG\"\n")
	if err := os.WriteFile(filepath.Join(pkg, "install.sh"), script, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "source.tar.gz")
	if out, err := exec.Command("tar", "-C", filepath.Dir(pkg), "-czf", archive, "xui-package").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, out)
	}
	content, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	curl := `#!/bin/bash
url=${@: -1}
while [[ $# -gt 0 ]]; do
  if [[ $1 == -o ]]; then dest=$2; shift 2; else shift; fi
done
name=${url##*/}
if [[ $name == *.sha256 ]]; then
  printf '%s  %s\n' "$XUI_FAKE_DIGEST" "${name%.sha256}" > "$dest"
else
  cp -- "$XUI_FAKE_ARCHIVE" "$dest"
fi
`
	for name, data := range map[string]string{"curl": curl, "sudo": "#!/bin/sh\nexec \"$@\"\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
	installer, err := filepath.Abs("install-online.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name             string
		args             []string
		digest           string
		success, install bool
		port             string
	}{
		{"default install", nil, digest, true, true, "2053"},
		{"custom port", []string{"--port", "9443", "--version", "v0.1.1"}, digest, true, true, "9443"},
		{"download only", []string{"--download-only"}, digest, true, false, ""},
		{"checksum mismatch", nil, strings.Repeat("0", 64), false, false, ""},
		{"invalid port", []string{"--port", "65536"}, digest, false, false, ""},
		{"invalid version", []string{"--version", "../../bad"}, digest, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "install.log")
			download := filepath.Join(t.TempDir(), "downloads")
			cmd := exec.Command("bash", append([]string{installer}, tc.args...)...)
			cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "XUI_INSTALLER_DIR="+download, "XUI_FAKE_ARCHIVE="+archive, "XUI_FAKE_DIGEST="+tc.digest, "XUI_FAKE_INSTALL_LOG="+log)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("unexpected result %v: %s", err, out)
			}
			got, err := os.ReadFile(log)
			if tc.install {
				if err != nil || strings.TrimSpace(string(got)) != tc.port {
					t.Fatalf("installer port %q: %v", got, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("installer unexpectedly ran: %v", err)
			}
		})
	}
}

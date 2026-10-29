package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/database/model"
)

func TestPortHopManagerTracksChangesAndErrors(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "db.sqlite")); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	var applied []string
	fail := false
	manager := &portHopManager{apply: func(plan string) error {
		if fail {
			return errors.New("simulated nft failure")
		}
		applied = append(applied, plan)
		return nil
	}}
	in := model.Inbound{Enable: true, Port: 443, Protocol: model.Hysteria, Tag: "h", StreamSettings: `{"finalmask":{"quicParams":{"udpHop":{"ports":"10000-10002"}}}}`, Settings: `{}`}
	if err := database.GetDB().Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	if err := manager.sync(false); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || !strings.Contains(applied[0], "udp dport 10000-10002 redirect to :443") {
		t.Fatalf("unexpected plan: %v", applied)
	}
	if err := manager.sync(false); err != nil || len(applied) != 1 {
		t.Fatal("unchanged plan re-applied")
	}
	if err := database.GetDB().Model(&in).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := manager.sync(false); err == nil || manager.status.LastError == "" {
		t.Fatal("failed sync not reported")
	}
	fail = false
	if err := manager.sync(false); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || strings.Contains(applied[1], "udp dport") || manager.status.LastError != "" {
		t.Fatalf("failed to reconcile disabled inbound: %+v", manager.status)
	}
	if err := manager.sync(true); err != nil || len(applied) != 3 {
		t.Fatal("forced sync did not apply")
	}
	fail = true
	if err := manager.sync(true); err == nil {
		t.Fatal("forced failure not reported")
	}
	fail = false
	if err := manager.sync(false); err != nil || len(applied) != 4 {
		t.Fatal("failed forced refresh was not retried")
	}

}

func TestPortHopSyncNamespace(t *testing.T) {
	if os.Getenv("XUI_TEST_PORTHOP_NAMESPACE") == "1" {
		if err := database.InitDB(filepath.Join(t.TempDir(), "db.sqlite")); err != nil {
			t.Fatal(err)
		}
		defer database.CloseDB()
		in := model.Inbound{Enable: true, Port: 443, Protocol: model.Hysteria, Tag: "h", StreamSettings: `{"finalmask":{"quicParams":{"udpHop":{"ports":"10000-10002"}}}}`, Settings: `{}`}
		if err := database.GetDB().Create(&in).Error; err != nil {
			t.Fatal(err)
		}
		manager := &portHopManager{apply: applyPortHop}
		if err := manager.sync(false); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("nft", "list", "table", "inet", "xui_porthop").CombinedOutput()
		if err != nil || !strings.Contains(string(output), "10000-10002") {
			t.Fatalf("rule not installed: %s %v", output, err)
		}
		if err = database.GetDB().Model(&in).Update("enable", false).Error; err != nil {
			t.Fatal(err)
		}
		if err = manager.sync(false); err != nil {
			t.Fatal(err)
		}
		output, err = exec.Command("nft", "list", "table", "inet", "xui_porthop").CombinedOutput()
		if err != nil || strings.Contains(string(output), "10000-10002") {
			t.Fatalf("rule not removed: %s %v", output, err)
		}
		return
	}
	for _, name := range []string{"nft", "unshare"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s unavailable", name)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--net", binary, "-test.run=^TestPortHopSyncNamespace$", "-test.v")
	cmd.Env = append(os.Environ(), "XUI_TEST_PORTHOP_NAMESPACE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Operation not permitted") {
			t.Skipf("namespace unavailable: %s", out)
		}
		t.Fatalf("namespace sync: %v %s", err, out)
	}
}

func TestPortHopStatusDetectsNftInPanelPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if GetPortHopStatus().NftAvailable {
		t.Fatal("nft reported available with an empty PATH")
	}
	if err := os.WriteFile(filepath.Join(dir, "nft"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	status := GetPortHopStatus()
	if status.NftAvailable != status.LinuxSupported {
		t.Fatalf("unexpected detection: %+v", status)
	}
}

func TestAutoPortHopSettingPersistsAndOverridesEnvironment(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "db.sqlite")); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	t.Setenv("XUI_PORTHOP_AUTO", "1")
	if !AutoPortHopEnabled() {
		t.Fatal("legacy environment default not applied")
	}
	if err := SetAutoPortHopEnabled(false); err != nil {
		t.Fatal(err)
	}
	if AutoPortHopEnabled() {
		t.Fatal("saved off setting did not override environment")
	}
	if err := SetAutoPortHopEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !AutoPortHopEnabled() {
		t.Fatal("saved on setting not applied")
	}
}

func TestPortHopStatusReportsConfiguredHysteria(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "db.sqlite")); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	if GetPortHopStatus().HasHysteria {
		t.Fatal("unexpected Hysteria configuration")
	}
	in := model.Inbound{Protocol: model.Hysteria, Enable: false, Port: 443, Tag: "hysteria-test", Settings: `{}`}
	if err := database.GetDB().Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	if !GetPortHopStatus().HasHysteria {
		t.Fatal("configured disabled Hysteria should be reported")
	}
	if err := database.GetDB().Delete(&in).Error; err != nil {
		t.Fatal(err)
	}
	if GetPortHopStatus().HasHysteria {
		t.Fatal("deleted Hysteria should not be reported")
	}
}

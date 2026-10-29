package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/database/model"
	"github.com/alireza0/x-ui/porthop"
	"strconv"
)

type PortHopStatus struct {
	Automatic      bool      `json:"automatic"`
	HasHysteria    bool      `json:"hasHysteria"`
	NftAvailable   bool      `json:"nftAvailable"`
	LinuxSupported bool      `json:"linuxSupported"`
	LastSuccess    time.Time `json:"lastSuccess"`
	LastError      string    `json:"lastError"`
}
type portHopManager struct {
	mu       sync.Mutex
	lastPlan string
	status   PortHopStatus
	apply    func(string) error
}

var managedPortHop = &portHopManager{apply: applyPortHop}

func autoPortHopEnabled() bool {
	if database.GetDB() != nil {
		setting, err := (&SettingService{}).getSetting("portHopAuto")
		if err == nil {
			value, err := strconv.ParseBool(setting.Value)
			return err == nil && value
		}
		if !database.IsNotFound(err) {
			return false
		}
	}
	return os.Getenv("XUI_PORTHOP_AUTO") == "1"
}

// SetAutoPortHopEnabled persists the panel choice, which takes precedence over
// the legacy environment variable. The scheduler applies it without restart.
func SetAutoPortHopEnabled(enabled bool) error {
	if err := (&SettingService{}).setBool("portHopAuto", enabled); err != nil {
		return err
	}
	if enabled {
		managedPortHop.mu.Lock()
		managedPortHop.lastPlan = ""
		managedPortHop.mu.Unlock()
	}
	return nil
}
func applyPortHop(plan string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("automatic port hopping requires Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(plan)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nftables sync: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
func (m *portHopManager) sync(force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inbounds, err := (&InboundService{}).GetAllInbounds()
	if err == nil {
		items := make([]porthop.Inbound, 0, len(inbounds))
		for _, in := range inbounds {
			if !in.Enable {
				continue
			}
			stream := in.StreamSettings
			if stream == "" {
				stream = "{}"
			}
			items = append(items, porthop.Inbound{ID: in.Id, Protocol: string(in.Protocol), Port: in.Port, Listen: in.Listen, StreamSettings: stream})
		}
		var plan string
		plan, err = porthop.Build(items)
		if err == nil && (force || plan != m.lastPlan) {
			err = m.apply(plan)
			if err == nil {
				m.lastPlan = plan
				m.status.LastSuccess = time.Now()
			}
		}
	}
	if err != nil {
		// Retry even when a forced refresh failed on an unchanged plan.
		m.lastPlan = ""
		m.status.LastError = err.Error()
	} else {
		m.status.LastError = ""
	}
	return err
}
func SyncPortHop(force bool) error { return managedPortHop.sync(force) }
func GetPortHopStatus() PortHopStatus {
	managedPortHop.mu.Lock()
	defer managedPortHop.mu.Unlock()
	s := managedPortHop.status
	s.Automatic = autoPortHopEnabled()
	if db := database.GetDB(); db != nil {
		var count int64
		if err := db.Model(&model.Inbound{}).Where("protocol = ?", model.Hysteria).Count(&count).Error; err == nil {
			s.HasHysteria = count > 0
		}
	}
	s.LinuxSupported = runtime.GOOS == "linux"
	_, err := exec.LookPath("nft")
	s.NftAvailable = s.LinuxSupported && err == nil
	return s
}
func AutoPortHopEnabled() bool { return autoPortHopEnabled() }

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/x-ui/config"
	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/logger"
	"github.com/alireza0/x-ui/util/common"
	"github.com/alireza0/x-ui/util/sys"
	"github.com/alireza0/x-ui/xray"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type ProcessState string

const (
	Running ProcessState = "Running"
	Stop    ProcessState = "Stop"
	Error   ProcessState = "Error"
)

type Status struct {
	T        time.Time `json:"-"`
	Cpu      float64   `json:"cpu"`
	CpuCount int       `json:"cpuCount"`
	Mem      struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"mem"`
	Swap struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"swap"`
	Disk struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"disk"`
	Xray struct {
		State    ProcessState `json:"state"`
		ErrorMsg string       `json:"errorMsg"`
		Version  string       `json:"version"`
	} `json:"xray"`
	Uptime   uint64    `json:"uptime"`
	Loads    []float64 `json:"loads"`
	TcpCount int       `json:"tcpCount"`
	UdpCount int       `json:"udpCount"`
	NetIO    struct {
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	} `json:"netIO"`
	NetTraffic struct {
		Sent uint64 `json:"sent"`
		Recv uint64 `json:"recv"`
	} `json:"netTraffic"`
	AppStats struct {
		Threads uint32 `json:"threads"`
		Mem     uint64 `json:"mem"`
		Uptime  uint64 `json:"uptime"`
	} `json:"appStats"`
	HostInfo struct {
		HostName string `json:"hostname"`
		Ipv4     string `json:"ipv4"`
		Ipv6     string `json:"ipv6"`
	} `json:"hostInfo"`
}

type Release struct {
	TagName string `json:"tag_name"`
	Id      uint   `json:"id"`
}

type ServerService struct {
	xrayService        XrayService
	inboundService     InboundService
	outboundService    OutboundService
	routingRuleService RoutingRuleService
}

func (s *ServerService) GetStatus(lastStatus *Status) *Status {
	now := time.Now()
	status := &Status{
		T: now,
	}

	percents, err := cpu.Percent(0, false)
	if err != nil {
		logger.Warning("get cpu percent failed:", err)
	} else {
		status.Cpu = percents[0]
	}

	upTime, err := host.Uptime()
	if err != nil {
		logger.Warning("get uptime failed:", err)
	} else {
		status.Uptime = upTime
	}

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		logger.Warning("get virtual memory failed:", err)
	} else {
		status.Mem.Current = memInfo.Used
		status.Mem.Total = memInfo.Total
	}

	swapInfo, err := mem.SwapMemory()
	if err != nil {
		logger.Warning("get swap memory failed:", err)
	} else {
		status.Swap.Current = swapInfo.Used
		status.Swap.Total = swapInfo.Total
	}

	distInfo, err := disk.Usage("/")
	if err != nil {
		logger.Warning("get dist usage failed:", err)
	} else {
		status.Disk.Current = distInfo.Used
		status.Disk.Total = distInfo.Total
	}

	avgState, err := load.Avg()
	if err != nil {
		logger.Warning("get load avg failed:", err)
	} else {
		status.Loads = []float64{avgState.Load1, avgState.Load5, avgState.Load15}
	}

	ioStats, err := net.IOCounters(false)
	if err != nil {
		logger.Warning("get io counters failed:", err)
	} else if len(ioStats) > 0 {
		ioStat := ioStats[0]
		status.NetTraffic.Sent = ioStat.BytesSent
		status.NetTraffic.Recv = ioStat.BytesRecv

		if lastStatus != nil {
			duration := now.Sub(lastStatus.T)
			seconds := float64(duration) / float64(time.Second)
			up := uint64(float64(status.NetTraffic.Sent-lastStatus.NetTraffic.Sent) / seconds)
			down := uint64(float64(status.NetTraffic.Recv-lastStatus.NetTraffic.Recv) / seconds)
			status.NetIO.Up = up
			status.NetIO.Down = down
		}
	} else {
		logger.Warning("can not find io counters")
	}

	status.TcpCount, err = sys.GetTCPCount()
	if err != nil {
		logger.Warning("get tcp connections failed:", err)
	}

	status.UdpCount, err = sys.GetUDPCount()
	if err != nil {
		logger.Warning("get udp connections failed:", err)
	}

	if s.xrayService.IsXrayRunning() {
		status.Xray.State = Running
		status.Xray.ErrorMsg = ""
	} else {
		err := s.xrayService.GetXrayErr()
		if err != nil {
			status.Xray.State = Error
		} else {
			status.Xray.State = Stop
		}
		status.Xray.ErrorMsg = s.xrayService.GetXrayResult()
	}
	status.Xray.Version = s.xrayService.GetXrayVersion()

	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)

	status.AppStats.Mem = rtm.Sys
	status.AppStats.Threads = uint32(runtime.NumGoroutine())
	status.CpuCount = runtime.NumCPU()
	if p != nil && p.IsRunning() {
		status.AppStats.Uptime = p.GetUptime()
	} else {
		status.AppStats.Uptime = 0
	}

	status.HostInfo.HostName, _ = os.Hostname()

	// get ip address
	netInterfaces, _ := net.Interfaces()
	for i := 0; i < len(netInterfaces); i++ {
		if len(netInterfaces[i].Flags) > 2 && netInterfaces[i].Flags[0] == "up" && netInterfaces[i].Flags[1] != "loopback" {
			addrs := netInterfaces[i].Addrs

			for _, address := range addrs {
				if strings.Contains(address.Addr, ".") {
					status.HostInfo.Ipv4 += address.Addr + " "
				} else if address.Addr[0:6] != "fe80::" {
					status.HostInfo.Ipv6 += address.Addr + " "
				}
			}
		}
	}

	return status
}

func (s *ServerService) GetXrayVersions() ([]string, error) {
	url := "https://api.github.com/repos/XTLS/Xray-core/tags?per_page=20"
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Xray versions: HTTP %d", resp.StatusCode)
	}
	buffer := bytes.NewBuffer(make([]byte, 8192))
	buffer.Reset()
	_, err = buffer.ReadFrom(resp.Body)
	if err != nil {
		return nil, err
	}

	tags := make([]struct {
		Name string `json:"name"`
	}, 0)
	err = json.Unmarshal(buffer.Bytes(), &tags)
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, tag := range tags {
		if tag.Name != "" {
			versions = append(versions, tag.Name)
		}
	}
	return versions, nil
}

func (s *ServerService) StopXrayService() (string error) {
	err := s.xrayService.StopXray()
	if err != nil {
		logger.Error("stop xray failed:", err)
		return err
	}

	return nil
}

func (s *ServerService) RestartXrayService() error {
	// RestartXray validates before stopping the existing process and returns
	// startup errors to the caller.
	return s.xrayService.RestartXray(true)
}

var xrayUpdateMu sync.Mutex

func (s *ServerService) downloadXRay(version string) (string, error) {
	osName, arch := runtime.GOOS, runtime.GOARCH
	if osName == "darwin" {
		osName = "macos"
	}
	switch arch {
	case "amd64":
		arch = "64"
	case "arm64":
		arch = "arm64-v8a"
	case "arm":
		arch = "arm32-v7a"
	case "386":
		arch = "32"
	}
	if version == "" || strings.ContainsAny(version, "/\\?#") {
		return "", errors.New("invalid Xray version")
	}
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/Xray-%s-%s.zip", version, osName, arch)
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Xray download: HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(config.GetBinFolderPath(), ".xray-download-*.zip")
	if err != nil {
		return "", err
	}
	path := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(resp.Body, 256<<20+1))
	if err != nil {
		return "", err
	}
	if n > 256<<20 {
		return "", errors.New("Xray download too large")
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func (s *ServerService) UpdateXray(version string) error {
	xrayUpdateMu.Lock()
	defer xrayUpdateMu.Unlock()
	zipPath, err := s.downloadXRay(version)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	entry := "xray"
	if runtime.GOOS == "windows" {
		entry = "xray.exe"
	}
	src, err := reader.Open(entry)
	if err != nil {
		return err
	}
	defer src.Close()
	return s.installXrayBinary(src)
}

// InstallXrayUpload accepts an executable or an official Xray zip archive.
func (s *ServerService) InstallXrayUpload(file multipart.File) error {
	xrayUpdateMu.Lock()
	defer xrayUpdateMu.Unlock()
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if size == 0 || size > 256<<20 {
		return errors.New("invalid upload size")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var magic [4]byte
	n, err := io.ReadFull(file, magic[:])
	if err != nil && err != io.ErrUnexpectedEOF {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if n == 4 && string(magic[:]) == "PK\x03\x04" {
		archive, err := zip.NewReader(file, size)
		if err != nil {
			return fmt.Errorf("invalid zip: %w", err)
		}
		name := "xray"
		if runtime.GOOS == "windows" {
			name = "xray.exe"
		}
		src, err := archive.Open(name)
		if err != nil {
			return fmt.Errorf("archive missing %s: %w", name, err)
		}
		defer src.Close()
		return s.installXrayBinary(src)
	}
	return s.installXrayBinary(file)
}

func (s *ServerService) installXrayBinary(src io.Reader) error {
	target := xray.GetBinaryPath()
	stage, err := os.CreateTemp(filepath.Dir(target), ".xray-new-*")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	n, copyErr := io.Copy(stage, io.LimitReader(src, 256<<20+1))
	if copyErr == nil && n > 256<<20 {
		copyErr = errors.New("Xray binary too large")
	}
	if copyErr == nil {
		copyErr = stage.Chmod(0755)
	}
	if copyErr == nil {
		copyErr = stage.Sync()
	}
	closeErr := stage.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, stagePath, "version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(output), "Xray ") {
		return fmt.Errorf("invalid Xray binary: %s", strings.TrimSpace(string(output)))
	}
	lock.Lock()
	defer lock.Unlock()
	cfg, err := s.xrayService.GetXrayConfig()
	if err != nil {
		return err
	}
	configData, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	configFile, err := os.CreateTemp(filepath.Dir(target), ".xray-update-config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(configFile.Name())
	_, err = configFile.Write(configData)
	if err == nil {
		err = configFile.Close()
	} else {
		configFile.Close()
	}
	if err != nil {
		return err
	}
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer checkCancel()
	output, err = exec.CommandContext(checkCtx, stagePath, "run", "-test", "-config", configFile.Name()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("new Xray configuration validation failed: %w: %s", err, output)
	}
	backup, err := os.CreateTemp(filepath.Dir(target), ".xray-previous-*")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	backup.Close()
	if err = os.Remove(backupPath); err != nil {
		return err
	}
	if err = os.Link(target, backupPath); err != nil {
		return fmt.Errorf("preserve previous Xray: %w", err)
	}
	keepBackup := false
	defer func() {
		if !keepBackup {
			os.Remove(backupPath)
		}
	}()
	wasRunning := s.xrayService.IsXrayRunning()
	if err = os.Rename(stagePath, target); err != nil {
		return err
	}
	err = s.xrayService.restartXrayLocked(true)
	if err == nil {
		time.Sleep(2 * time.Second)
		if !s.xrayService.IsXrayRunning() {
			err = errors.New("new Xray exited during startup")
		}
	}
	if err != nil {
		if restoreErr := os.Rename(backupPath, target); restoreErr != nil {
			keepBackup = true
			return errors.Join(err, fmt.Errorf("restore previous Xray (backup %s): %w", backupPath, restoreErr))
		}
		if wasRunning {
			if restartErr := s.xrayService.restartXrayLocked(true); restartErr != nil {
				return errors.Join(err, fmt.Errorf("restart previous Xray: %w", restartErr))
			}
		}
		return fmt.Errorf("new Xray failed; previous binary restored: %w", err)
	}
	return nil
}

func (s *ServerService) GetLogs(count string, level string, syslog string) []string {
	c, _ := strconv.Atoi(count)
	var lines []string

	if syslog == "true" {
		// Check if running on Windows - journalctl is not available
		if runtime.GOOS == "windows" {
			return []string{"Syslog is not supported on Windows. Please use application logs instead by unchecking the 'Syslog' option."}
		}

		// Validate and sanitize count parameter
		countInt, err := strconv.Atoi(count)
		if err != nil || countInt < 1 || countInt > 10000 {
			return []string{"Invalid count parameter - must be a number between 1 and 10000"}
		}

		// Validate level parameter - only allow valid syslog levels
		validLevels := map[string]bool{
			"0": true, "emerg": true,
			"1": true, "alert": true,
			"2": true, "crit": true,
			"3": true, "err": true,
			"4": true, "warning": true,
			"5": true, "notice": true,
			"6": true, "info": true,
			"7": true, "debug": true,
		}
		if !validLevels[level] {
			return []string{"Invalid level parameter - must be a valid syslog level"}
		}

		// Use hardcoded command with validated parameters
		cmd := exec.Command("journalctl", "-u", "x-ui", "--no-pager", "-n", strconv.Itoa(countInt), "-p", level)
		var out bytes.Buffer
		cmd.Stdout = &out
		err = cmd.Run()
		if err != nil {
			return []string{"Failed to run journalctl command! Make sure systemd is available and x-ui service is registered."}
		}
		lines = strings.Split(out.String(), "\n")
	} else {
		lines = logger.GetLogs(c, level)
	}

	return lines
}

func (s *ServerService) GetConfigJson() (interface{}, error) {
	config, err := s.xrayService.GetXrayConfig()
	if err != nil {
		return nil, err
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}

	var jsonData interface{}
	err = json.Unmarshal(contents, &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

func (s *ServerService) GetDb() ([]byte, error) {
	// VACUUM INTO takes a consistent SQLite snapshot, including committed WAL
	// frames even when concurrent readers prevent a complete checkpoint.
	file, err := os.CreateTemp(filepath.Dir(config.GetDBPath()), ".x-ui-snapshot-*.db")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	defer os.Remove(path)
	if err = file.Close(); err != nil {
		return nil, err
	}
	if err = database.GetDB().Exec("VACUUM INTO ?", path).Error; err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s *ServerService) ImportDB(file multipart.File) error {
	// Check if the file is a SQLite database
	isValidDb, err := database.IsSQLiteDB(file)
	if err != nil {
		return common.NewErrorf("Error checking db file format: %v", err)
	}
	if !isValidDb {
		return common.NewError("Invalid db file format")
	}

	// Reset the file reader to the beginning
	_, err = file.Seek(0, 0)
	if err != nil {
		return common.NewErrorf("Error resetting file reader: %v", err)
	}

	// Use a private, unique temporary file in the same filesystem as the DB.
	tempFile, err := os.CreateTemp(filepath.Dir(config.GetDBPath()), ".x-ui-import-*.db")
	if err != nil {
		return common.NewErrorf("Error creating temporary db file: %v", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	defer tempFile.Close()

	// Save uploaded file to temporary file
	_, err = io.Copy(tempFile, file)
	if err != nil {
		return common.NewErrorf("Error saving db: %v", err)
	}

	// Close temp file before opening via sqlite
	if err = tempFile.Close(); err != nil {
		return common.NewErrorf("Error closing temporary db file: %v", err)
	}

	// Validate integrity (no migrations / side effects)
	if err = database.ValidatePanelDB(tempPath); err != nil {
		return common.NewErrorf("Invalid or corrupt db file: %v", err)
	}

	// Allocate a new fallback name; never delete a prior recovery copy.
	fallback, err := os.CreateTemp(filepath.Dir(config.GetDBPath()), ".x-ui-backup-*.db")
	if err != nil {
		return common.NewErrorf("Error allocating backup: %v", err)
	}
	fallbackPath := fallback.Name()
	if err = fallback.Close(); err != nil {
		os.Remove(fallbackPath)
		return err
	}
	if err = os.Remove(fallbackPath); err != nil {
		return err
	}

	if err = database.Checkpoint(); err != nil {
		return common.NewErrorf("Error checkpointing database: %v", err)
	}
	if s.xrayService.IsXrayRunning() {
		if errStop := s.StopXrayService(); errStop != nil {
			return common.NewErrorf("Error stopping Xray before DB import: %v", errStop)
		}
	}
	if errClose := database.CloseDB(); errClose != nil {
		_ = s.RestartXrayService()
		return common.NewErrorf("Error closing database: %v", errClose)
	}

	// Move the current database to the fallback location
	err = os.Rename(config.GetDBPath(), fallbackPath)
	if err != nil {
		if reopen := database.InitDB(config.GetDBPath()); reopen != nil {
			return common.NewErrorf("Backup failed: %v; database reopen failed: %v", err, reopen)
		}
		_ = s.RestartXrayService()
		return common.NewErrorf("Error backing up database: %v", err)
	}

	// Keep the fallback even when import or core startup fails. It contains the
	// last known database and must not be deleted by an error path.

	// Move temp to DB path
	err = os.Rename(tempPath, config.GetDBPath())
	if err != nil {
		errRename := os.Rename(fallbackPath, config.GetDBPath())
		if errRename != nil {
			return common.NewErrorf("Error moving db file and restoring fallback: %v", errRename)
		}
		if reopen := database.InitDB(config.GetDBPath()); reopen != nil {
			return common.NewErrorf("Import failed: %v; database reopen failed: %v", err, reopen)
		}
		_ = s.RestartXrayService()
		return common.NewErrorf("Error moving db file: %v", err)
	}

	// Restore the previous database if migration or the new core fails. The
	// backup is only moved after the failed database has been closed.
	rollback := func(cause error) error {
		if closeErr := database.CloseDB(); closeErr != nil {
			return common.NewErrorf("Import failed: %v; close failed: %v; backup: %s", cause, closeErr, fallbackPath)
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if removeErr := os.Remove(config.GetDBPath() + suffix); removeErr != nil && !os.IsNotExist(removeErr) {
				return common.NewErrorf("Import failed: %v; cleanup failed: %v; backup: %s", cause, removeErr, fallbackPath)
			}
		}
		if restoreErr := os.Rename(fallbackPath, config.GetDBPath()); restoreErr != nil {
			return common.NewErrorf("Import failed: %v; restore failed: %v; backup: %s", cause, restoreErr, fallbackPath)
		}
		if reopen := database.InitDB(config.GetDBPath()); reopen != nil {
			return common.NewErrorf("Import failed: %v; restored database reopen failed: %v", cause, reopen)
		}
		if restart := s.RestartXrayService(); restart != nil {
			return common.NewErrorf("Import failed: %v; previous database restored but core restart failed: %v", cause, restart)
		}
		return common.NewErrorf("Import failed and previous database restored: %v", cause)
	}
	if err = database.InitDB(config.GetDBPath()); err != nil {
		return rollback(err)
	}
	if err = s.RestartXrayService(); err != nil {
		return rollback(err)
	}

	return nil
}

func (s *ServerService) GetNewX25519Cert() (interface{}, error) {
	// Run the command
	cmd := exec.Command(xray.GetBinaryPath(), "x25519")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")

	privateKeyLine := strings.Split(lines[0], ":")
	publicKeyLine := strings.Split(lines[1], ":")

	privateKey := strings.TrimSpace(privateKeyLine[1])
	publicKey := strings.TrimSpace(publicKeyLine[1])

	keyPair := map[string]interface{}{
		"privateKey": privateKey,
		"publicKey":  publicKey,
	}

	return keyPair, nil
}

func (s *ServerService) GetNewmldsa65() (any, error) {
	// Run the command
	cmd := exec.Command(xray.GetBinaryPath(), "mldsa65")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")

	SeedLine := strings.Split(lines[0], ":")
	VerifyLine := strings.Split(lines[1], ":")

	seed := strings.TrimSpace(SeedLine[1])
	verify := strings.TrimSpace(VerifyLine[1])

	keyPair := map[string]any{
		"seed":   seed,
		"verify": verify,
	}

	return keyPair, nil
}

func (s *ServerService) GetNewEchCert(sni string) (interface{}, error) {
	// Run the command
	cmd := exec.Command(xray.GetBinaryPath(), "tls", "ech", "--serverName", sni)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	if len(lines) < 4 {
		return nil, common.NewError("invalid ech cert")
	}

	configList := lines[1]
	serverKeys := lines[3]

	return map[string]interface{}{
		"echServerKeys": serverKeys,
		"echConfigList": configList,
	}, nil
}

func (s *ServerService) GetNewVlessEnc() (any, error) {
	cmd := exec.Command(xray.GetBinaryPath(), "vlessenc")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	var auths []map[string]string
	var current map[string]string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authentication:") {
			if current != nil {
				auths = append(auths, current)
			}
			current = map[string]string{
				"label": strings.TrimSpace(strings.TrimPrefix(line, "Authentication:")),
			}
		} else if strings.HasPrefix(line, `"decryption"`) || strings.HasPrefix(line, `"encryption"`) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && current != nil {
				key := strings.Trim(parts[0], `" `)
				val := strings.Trim(parts[1], `" `)
				current[key] = val
			}
		}
	}

	if current != nil {
		auths = append(auths, current)
	}

	return map[string]any{
		"auths": auths,
	}, nil
}

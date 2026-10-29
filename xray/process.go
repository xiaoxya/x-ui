package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/alireza0/x-ui/config"
	"github.com/alireza0/x-ui/logger"
	"github.com/alireza0/x-ui/util/common"
)

func GetBinaryName() string {
	return fmt.Sprintf("xray-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func GetBinaryPath() string {
	return config.GetBinFolderPath() + "/" + GetBinaryName()
}

func GetConfigPath() string {
	return config.GetBinFolderPath() + "/config.json"
}

func GetGeositePath() string {
	return config.GetBinFolderPath() + "/geosite.dat"
}

func GetGeoipPath() string {
	return config.GetBinFolderPath() + "/geoip.dat"
}

func stopProcess(p *Process) {
	p.Stop()
}

type Process struct {
	*process
}

func NewProcess(xrayConfig *Config) *Process {
	p := &Process{newProcess(xrayConfig)}
	runtime.SetFinalizer(p, stopProcess)
	return p
}

type process struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	running bool
	done    chan struct{}

	version string
	apiAddr string

	onlineOutbounds []string

	config    *Config
	logWriter *LogWriter
	exitErr   error
	startTime time.Time
}

func newProcess(config *Config) *process {
	return &process{
		version:   "Unknown",
		config:    config,
		logWriter: NewLogWriter(),
		startTime: time.Now(),
	}
}

func (p *process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *process) GetErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

func (p *process) GetResult() string {
	if err := p.GetErr(); len(p.logWriter.lastLine) == 0 && err != nil {
		return err.Error()
	}
	return p.logWriter.lastLine
}

func (p *process) GetVersion() string {
	return p.version
}

func (p *Process) GetAPIAddr() string {
	return p.apiAddr
}

func (p *Process) GetConfig() *Config {
	return p.config
}

func (p *Process) GetOnlineOutbounds() []string {
	return p.onlineOutbounds
}

func (p *Process) SetOnlineOutbounds(tags []string) {
	p.onlineOutbounds = tags
}

func (p *Process) GetUptime() uint64 {
	return uint64(time.Since(p.startTime).Seconds())
}

func (p *process) refreshAPIAddr() {
	p.apiAddr = p.config.API.Listen
}

func (p *process) refreshVersion() {
	cmd := exec.Command(GetBinaryPath(), "version")
	data, err := cmd.Output()
	if err != nil {
		p.version = "Unknown"
	} else {
		datas := bytes.Split(data, []byte(" "))
		if len(datas) <= 1 {
			p.version = "Unknown"
		} else {
			p.version = string(datas[1])
		}
	}
}

// ValidateConfig tests the candidate with the configured core before replacing a running instance.
func ValidateConfig(config *Config) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(configFolder(), ".xray-check-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, GetBinaryPath(), "run", "-test", "-config", f.Name()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Xray config validation failed: %w: %s", err, output)
	}
	return nil
}

func configFolder() string { return config.GetBinFolderPath() }

func (p *process) Start() (err error) {
	if p.IsRunning() {
		return errors.New("xray is already running")
	}

	defer func() {
		if err != nil {
			logger.Error("Failure in running xray-core process: ", err)
			p.mu.Lock()
			p.exitErr = err
			p.mu.Unlock()
		}
	}()

	data, err := json.MarshalIndent(p.config, "", "  ")
	if err != nil {
		return common.NewErrorf("Failed to generate XRAY configuration files: %v", err)
	}
	configPath := GetConfigPath()
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return common.NewErrorf("Write the configuration file failed: %v", err)
	}
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}

	cmd := exec.Command(GetBinaryPath(), "run", "-c", configPath)
	p.cmd = cmd

	cmd.Stdout = p.logWriter
	cmd.Stderr = p.logWriter

	if err := cmd.Start(); err != nil {
		return err
	}
	p.mu.Lock()
	p.running = true
	p.done = make(chan struct{})
	done := p.done
	p.mu.Unlock()
	go func() {
		err := cmd.Wait()
		if err != nil {
			logger.Error("Failure in running xray-core: ", err)
		}
		p.mu.Lock()
		p.exitErr = err
		p.running = false
		close(done)
		p.mu.Unlock()
	}()

	p.refreshVersion()
	p.refreshAPIAddr()

	return nil
}

func (p *process) Stop() error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return errors.New("xray is not running")
	}
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	var err error
	if runtime.GOOS == "windows" {
		err = cmd.Process.Kill()
	} else {
		err = cmd.Process.Signal(syscall.SIGTERM)
	}
	if err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		if err := cmd.Process.Kill(); err != nil {
			return err
		}
		<-done
		return nil
	}
}

func writeCrashReport(m []byte) error {
	crashReportPath := config.GetBinFolderPath() + "/core_crash_" + time.Now().Format("20060102_150405") + ".log"
	return os.WriteFile(crashReportPath, m, os.ModePerm)
}

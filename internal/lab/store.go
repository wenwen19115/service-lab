package lab

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Firmware struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	ImageVersion string    `json:"imageVersion"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
	UploadedAt   time.Time `json:"uploadedAt"`
}
type OTA struct {
	Mode       string `json:"mode"`
	FirmwareID string `json:"firmwareId"`
	PublicBase string `json:"publicBase"`
	Upstream   string `json:"upstream"`
	Forward    bool   `json:"forward"`
	DelayMS    int    `json:"delayMs"`
}
type Mock struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Status      int               `json:"status"`
	ContentType string            `json:"contentType"`
	Body        string            `json:"body"`
	DelayMS     int               `json:"delayMs"`
	Enabled     bool              `json:"enabled"`
	Headers     map[string]string `json:"headers,omitempty"`
}
type Config struct {
	OTA      OTA        `json:"ota"`
	Firmware []Firmware `json:"firmware"`
	Trash    []Firmware `json:"trash"`
	Mocks    []Mock     `json:"mocks"`
}
type TransferStep struct {
	Time    time.Time `json:"time"`
	Bytes   int64     `json:"bytes"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
}

var requestSequence atomic.Uint64

type Event struct {
	ID              uint64            `json:"id"`
	Phase           string            `json:"phase"`
	TotalBytes      int64             `json:"totalBytes"`
	SpeedBPS        int64             `json:"speedBps"`
	FirmwareVersion string            `json:"firmwareVersion"`
	RequestHeaders  map[string]string `json:"requestHeaders,omitempty"`
	ResponseBody    string            `json:"responseBody"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	Steps           []TransferStep    `json:"steps,omitempty"`
	Time            time.Time         `json:"time"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	Remote          string            `json:"remote"`
	DeviceID        string            `json:"deviceId"`
	Version         string            `json:"version"`
	Status          int               `json:"status"`
	Bytes           int64             `json:"bytes"`
	DurationMS      int64             `json:"durationMs"`
	Body            string            `json:"body"`
	Note            string            `json:"note"`
}
type App struct {
	historySignal  chan struct{}
	historyStop    chan struct{}
	historyOnce    sync.Once
	historyWG      sync.WaitGroup
	historyError   string
	historySavedAt time.Time
	startedAt      time.Time
	ListenAddr     string
	mu             sync.RWMutex
	config         Config
	events         []Event
	dir            string
	frontend       fs.FS
	client         *http.Client
}

func New(dir string, frontend fs.FS) (*App, error) {
	if err := os.MkdirAll(filepath.Join(dir, "firmware"), 0755); err != nil {
		return nil, err
	}
	app := &App{dir: dir, frontend: frontend, events: []Event{},
		client: &http.Client{Timeout: 15 * time.Second},
		config: Config{OTA: OTA{Mode: "no-update", Forward: true, Upstream: "http://api.cloudzao.cn/xiaozhi/ota/"},
			Firmware: []Firmware{}, Mocks: []Mock{}}}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err == nil {
		if err = json.Unmarshal(data, &app.config); err != nil {
			return nil, fmt.Errorf("invalid config.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if app.config.Firmware == nil {
		app.config.Firmware = []Firmware{}
	}
	for i := range app.config.Firmware {
		if app.config.Firmware[i].ImageVersion == "" {
			app.config.Firmware[i].ImageVersion = app.config.Firmware[i].Version
		}
	}
	if app.config.Trash == nil {
		app.config.Trash = []Firmware{}
	}
	if app.config.Mocks == nil {
		app.config.Mocks = []Mock{}
	}
	if err := app.startHistory(); err != nil {
		return nil, err
	}
	return app, nil
}

func (a *App) saveLocked() error {
	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(a.dir, "config.json")
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (a *App) record(event Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.signalHistory()
	if event.ID == 0 {
		event.ID = requestSequence.Add(1)
	}
	event.Steps = append([]TransferStep{}, event.Steps...)
	for i := range a.events {
		if a.events[i].ID == event.ID {
			a.events[i] = event
			return
		}
	}
	a.events = append(a.events, event)
	if len(a.events) > 300 {
		a.events = a.events[len(a.events)-300:]
	}
}
func LocalAddresses(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{}
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
	result := []string{}
	interfaces, _ := net.InterfaceAddrs()
	for _, entry := range interfaces {
		ip, _, err := net.ParseCIDR(entry.String())
		if err == nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			result = append(result, "http://"+net.JoinHostPort(ip.String(), port))
		}
	}
	return result
}
func firmwarePath(dir, id string) string { return filepath.Join(dir, "firmware", id+".bin") }
func limitedText(body []byte) string {
	if len(body) > 8192 {
		return string(body[:8192]) + "\n[truncated]"
	}
	return string(body)
}
func deviceVersion(body []byte) string {
	var data struct {
		Application struct {
			Version string `json:"version"`
		} `json:"application"`
	}
	_ = json.Unmarshal(body, &data)
	return strings.TrimSpace(data.Application.Version)
}

package lab

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, err error) {
	respond(w, status, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("request must contain one JSON value")
	}
	return nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		// Browser writes must come from this UI, not another website.
		if r.Method != http.MethodGet {
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || parsed.Host != r.Host {
					fail(w, 403, fmt.Errorf("origin rejected"))
					return
				}
			}
		}
		a.admin(w, r)
		return
	}
	a.mu.RLock()
	otaPath := a.config.OTAPath
	a.mu.RUnlock()
	if strings.TrimRight(r.URL.Path, "/") == otaPath+"/activate" {
		a.activate(w, r)
		return
	}
	if strings.TrimRight(r.URL.Path, "/") == otaPath {
		a.ota(w, r)
		return
	}
	routes := a.transferConfig()
	if r.URL.Path == routes.ListPath || r.URL.Path == routes.ConfirmPath {
		a.resourceProtocol(w, r, routes)
		return
	}
	if strings.HasPrefix(r.URL.Path, routes.DownloadPath+"/") {
		if strings.HasSuffix(r.URL.Path, "/cover.png") {
			a.resourceCover(w, r, routes.DownloadPath)
		} else {
			a.resourceDownload(w, r, routes.DownloadPath)
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/firmware/") {
		a.download(w, r)
		return
	}
	a.mu.RLock()
	var selected *Mock
	for _, item := range a.config.Mocks {
		if item.Enabled && item.Path == r.URL.Path && (item.Method == "*" || item.Method == r.Method) {
			copy := item
			selected = &copy
			break
		}
	}
	a.mu.RUnlock()
	if selected != nil {
		a.mock(w, r, *selected)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." {
		name = "index.html"
	}
	file, err := a.frontend.Open(name)
	if err != nil {
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		file, err = a.frontend.Open("index.html")
	}
	if err != nil {
		http.Error(w, "UI is not built. Run npm ci && npm run build in web/ before go build.", 503)
		return
	}
	file.Close()
	http.FileServer(http.FS(a.frontend)).ServeHTTP(w, r)
}

func (a *App) admin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "PUT" && r.URL.Path == "/api/ota/endpoint":
		a.updateOTAEndpoint(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/service/restart":
		if a.RequestRestart == nil {
			fail(w, 503, fmt.Errorf("当前运行方式不支持重启"))
			return
		}
		if !a.RequestRestart() {
			fail(w, 409, fmt.Errorf("服务正在重启"))
			return
		}
		respond(w, http.StatusAccepted, map[string]bool{"ok": true})
	case r.Method == "PUT" && r.URL.Path == "/api/transfer":
		a.updateTransfer(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/diagnostics/log":
		a.diagnosticLog(w, r)
	case r.URL.Path == "/api/resources" && r.Method == "POST":
		a.uploadResource(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/resources/"):
		a.manageResource(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/health":
		respond(w, 200, map[string]string{"service": "service-lab"})
	case r.Method == "GET" && r.URL.Path == "/api/state":
		a.mu.RLock()
		config := a.config
		// Snapshot slices before releasing the lock.
		config.Resources = append([]Resource{}, config.Resources...)
		config.Firmware = append([]Firmware{}, config.Firmware...)
		config.Mocks = append([]Mock{}, config.Mocks...)
		config.Trash = append([]Firmware{}, config.Trash...)
		events := append([]Event{}, a.events...)
		server := map[string]any{"version": Version, "buildTime": BuildTime, "startedAt": a.startedAt, "uptimeSeconds": int64(time.Since(a.startedAt).Seconds()), "dataDir": a.dir, "listenAddr": a.ListenAddr, "eventLimit": 300, "historyError": a.historyError, "historySavedAt": a.historySavedAt}
		a.mu.RUnlock()
		if r.URL.Query().Get("summary") == "1" {
			for i := range events {
				events[i].Body = ""
				events[i].ResponseBody = ""
				events[i].Steps = nil
				events[i].RequestHeaders = nil
				events[i].ResponseHeaders = nil
			}
		}
		respond(w, 200, map[string]any{"config": config, "events": events, "addresses": LocalAddresses(a.ListenAddr), "now": time.Now(), "server": server})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/events/"):
		a.eventDetail(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/test":
		a.testEndpoint(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/ota":
		var value OTA
		if err := decode(w, r, &value); err != nil {
			fail(w, 400, err)
			return
		}
		if err := validateOTA(value); err != nil {
			fail(w, 400, err)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if value.Mode == "update" || value.Mode == "download-error" || value.Mode == "truncate" {
			found := false
			for _, f := range a.config.Firmware {
				if f.ID == value.FirmwareID {
					found = true
				}
			}
			if !found {
				fail(w, 400, fmt.Errorf("请先选择已上传的固件"))
				return
			}
		}
		old := a.config.OTA
		a.config.OTA = value
		if err := a.saveLocked(); err != nil {
			a.config.OTA = old
			fail(w, 500, err)
			return
		}
		respond(w, 200, value)
	case r.Method == "POST" && r.URL.Path == "/api/firmware":
		a.upload(w, r)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/firmware/") && strings.HasSuffix(r.URL.Path, "/file"):
		a.exportFirmware(w, r)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/firmware/"):
		a.deleteFirmware(w, r)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/firmware/") && strings.HasSuffix(r.URL.Path, "/restore"):
		a.restoreFirmware(w, r)
	case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/firmware/"):
		a.editFirmware(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/mocks":
		var items []Mock
		if err := decode(w, r, &items); err != nil {
			fail(w, 400, err)
			return
		}
		if items == nil {
			items = []Mock{}
		}
		if len(items) > 100 {
			fail(w, 400, fmt.Errorf("最多保存 100 个接口"))
			return
		}
		seen := map[string]bool{}
		ids := map[string]bool{}
		for i := range items {
			if items[i].ID == "" {
				items[i].ID = fmt.Sprintf("mock-%d-%d", time.Now().UnixNano(), i)
			}
			routes := a.transferConfig()
			a.mu.RLock()
			otaPath := a.config.OTAPath
			a.mu.RUnlock()
			if strings.TrimRight(items[i].Path, "/") == otaPath || strings.HasPrefix(items[i].Path, otaPath+"/") {
				fail(w, 400, fmt.Errorf("路径已被OTA服务占用"))
				return
			}
			if items[i].Path == routes.ListPath || items[i].Path == routes.ConfirmPath || items[i].Path == routes.DownloadPath || strings.HasPrefix(items[i].Path, routes.DownloadPath+"/") {
				fail(w, 400, fmt.Errorf("路径已被资源传输占用"))
				return
			}
			if err := validateMock(items[i]); err != nil {
				fail(w, 400, err)
				return
			}
			key := items[i].Method + " " + items[i].Path
			if seen[key] || ids[items[i].ID] {
				fail(w, 400, fmt.Errorf("接口方法、路径或 ID 重复"))
				return
			}
			seen[key] = true
			ids[items[i].ID] = true
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		old := a.config.Mocks
		a.config.Mocks = items
		if err := a.saveLocked(); err != nil {
			a.config.Mocks = old
			fail(w, 500, err)
			return
		}
		respond(w, 200, items)
	case r.Method == "DELETE" && r.URL.Path == "/api/events":
		a.mu.Lock()
		a.events = []Event{}
		a.mu.Unlock()
		a.signalHistory()
		respond(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, fmt.Errorf("unknown management endpoint"))
	}
}

func validateOTA(o OTA) error {
	switch o.Mode {
	case "no-update", "update", "check-error", "download-error", "truncate":
	default:
		return fmt.Errorf("未知 OTA 场景")
	}
	if o.DelayMS < 0 || o.DelayMS > 30000 {
		return fmt.Errorf("延迟必须为 0–30000 毫秒")
	}
	for _, value := range []string{o.PublicBase, o.Upstream} {
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("地址必须是有效的 HTTP/HTTPS URL")
		}
	}
	if o.Forward && o.Upstream == "" {
		return fmt.Errorf("转发模式需要填写上游地址")
	}
	if o.PublicBase != "" {
		u, _ := url.Parse(o.PublicBase)
		if u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("对外地址只填写协议、IP 和端口")
		}
	}
	return nil
}
func validateMock(m Mock) error {
	if !strings.HasPrefix(m.Path, "/") || strings.ContainsAny(m.Path, "?#") || path.Clean(m.Path) != m.Path ||
		m.Path == "/" || strings.HasPrefix(m.Path, "/api") || strings.HasPrefix(m.Path, "/firmware") ||
		strings.HasPrefix(m.Path, "/assets") {
		return fmt.Errorf("接口路径无效或与平台保留路径冲突")
	}
	switch m.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "*":
	default:
		return fmt.Errorf("HTTP 方法无效")
	}
	if m.Status < 200 || m.Status > 599 {
		return fmt.Errorf("状态码需为 200–599")
	}
	if m.DelayMS < 0 || m.DelayMS > 30000 {
		return fmt.Errorf("延迟必须为 0–30000 毫秒")
	}
	if err := validateHeaders(m.Headers); err != nil {
		return err
	}
	if strings.ContainsAny(m.ContentType, "\r\n") {
		return fmt.Errorf("Content-Type 无效")
	}
	return nil
}

func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 17*1024*1024)
	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
		fail(w, 400, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024*1024+1))
	if err != nil {
		fail(w, 500, err)
		return
	}
	if len(data) > 16*1024*1024 {
		fail(w, 400, fmt.Errorf("固件超过 16 MiB"))
		return
	}
	if len(data) < 256 || data[0] != 0xe9 || binary.LittleEndian.Uint32(data[32:36]) != 0xabcd5432 {
		fail(w, 400, fmt.Errorf("固件格式不受支持或缺少应用描述，请上传符合镜像格式要求的 app .bin"))
		return
	}
	version := strings.TrimRight(string(data[48:80]), "\x00")
	if version == "" || strings.ContainsAny(version, "\r\n") {
		fail(w, 400, fmt.Errorf("无法读取固件版本"))
		return
	}
	publishVersion := strings.TrimSpace(r.FormValue("version"))
	if publishVersion != "" {
		if err := validatePublishVersion(publishVersion); err != nil {
			fail(w, 400, err)
			return
		}
	} else {
		publishVersion = version
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	f := Firmware{ID: hash[:24], Name: path.Base(strings.ReplaceAll(header.Filename, "\\", "/")), Version: publishVersion, ImageVersion: version,
		Size: int64(len(data)), SHA256: hash, UploadedAt: time.Now()}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, existing := range a.config.Firmware {
		if existing.ID == f.ID {
			if r.FormValue("version") != "" {
				a.config.Firmware[i].Version = publishVersion
				if err := a.saveLocked(); err != nil {
					a.config.Firmware[i] = existing
					fail(w, 500, err)
					return
				}
			}
			respond(w, 200, a.config.Firmware[i])
			return
		}
	}
	for _, trashed := range a.config.Trash {
		if trashed.ID == f.ID {
			fail(w, 409, fmt.Errorf("该文件已在回收站，请先恢复"))
			return
		}
	}
	destination := firmwarePath(a.dir, f.ID)
	if err = os.WriteFile(destination+".tmp", data, 0600); err == nil {
		err = os.Rename(destination+".tmp", destination)
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	a.config.Firmware = append(a.config.Firmware, f)
	if err = a.saveLocked(); err != nil {
		a.config.Firmware = a.config.Firmware[:len(a.config.Firmware)-1]
		_ = os.Remove(destination)
		fail(w, 500, err)
		return
	}
	respond(w, 201, f)
}

func validatePublishVersion(version string) error {
	if len(version) > 31 || !publishVersionPattern.MatchString(version) {
		return fmt.Errorf("发布版本请使用数字格式，例如 2.0.62，最多 31 个字符")
	}
	return nil
}

var publishVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`)

func (a *App) editFirmware(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/firmware/")
	var payload struct {
		Version string `json:"version"`
	}
	if err := decode(w, r, &payload); err != nil {
		fail(w, 400, err)
		return
	}
	payload.Version = strings.TrimSpace(payload.Version)
	if err := validatePublishVersion(payload.Version); err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, f := range a.config.Firmware {
		if f.ID != id {
			continue
		}
		a.config.Firmware[i].Version = payload.Version
		if err := a.saveLocked(); err != nil {
			a.config.Firmware[i] = f
			fail(w, 500, err)
			return
		}
		respond(w, 200, a.config.Firmware[i])
		return
	}
	fail(w, 404, fmt.Errorf("固件不存在"))
}

func isPublished(config OTA, id string) bool {
	return config.FirmwareID == id && (config.Mode == "update" || config.Mode == "download-error" || config.Mode == "truncate")
}
func (a *App) deleteFirmware(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/firmware/")
	a.mu.Lock()
	defer a.mu.Unlock()
	if isPublished(a.config.OTA, id) {
		fail(w, 409, fmt.Errorf("此固件正在发布，请先停止发布或切换到其他固件"))
		return
	}
	for i, f := range a.config.Firmware {
		if f.ID != id {
			continue
		}
		if err := os.MkdirAll(filepath.Join(a.dir, "trash"), 0755); err != nil {
			fail(w, 500, err)
			return
		}
		source := firmwarePath(a.dir, id)
		destination := filepath.Join(a.dir, "trash", id+".bin")
		if err := os.Rename(source, destination); err != nil {
			fail(w, 500, err)
			return
		}
		oldFirmware := append([]Firmware{}, a.config.Firmware...)
		oldOTA := a.config.OTA
		a.config.Firmware = append(a.config.Firmware[:i], a.config.Firmware[i+1:]...)
		a.config.Trash = append(a.config.Trash, f)
		if a.config.OTA.FirmwareID == id {
			a.config.OTA.FirmwareID = ""
		}
		if err := a.saveLocked(); err != nil {
			a.config.Firmware = oldFirmware
			a.config.OTA = oldOTA
			a.config.Trash = a.config.Trash[:len(a.config.Trash)-1]
			_ = os.Rename(destination, source)
			fail(w, 500, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	fail(w, 404, fmt.Errorf("固件不存在"))
}
func (a *App) restoreFirmware(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/firmware/"), "/restore")
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, f := range a.config.Trash {
		if f.ID != id {
			continue
		}
		source := filepath.Join(a.dir, "trash", id+".bin")
		destination := firmwarePath(a.dir, id)
		if err := os.Rename(source, destination); err != nil {
			fail(w, 500, err)
			return
		}
		oldTrash := append([]Firmware{}, a.config.Trash...)
		a.config.Trash = append(a.config.Trash[:i], a.config.Trash[i+1:]...)
		a.config.Firmware = append(a.config.Firmware, f)
		if err := a.saveLocked(); err != nil {
			a.config.Trash = oldTrash
			a.config.Firmware = a.config.Firmware[:len(a.config.Firmware)-1]
			_ = os.Rename(destination, source)
			fail(w, 500, err)
			return
		}
		respond(w, 200, f)
		return
	}
	fail(w, 404, fmt.Errorf("回收站中没有此固件"))
}

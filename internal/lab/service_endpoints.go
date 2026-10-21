package lab

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// normalizeEndpoint is shared by configurable device-facing services.
func normalizeEndpoint(value string) (string, error) {
	text := strings.TrimSpace(value)
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		u, err := url.Parse(text)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
			return "", fmt.Errorf("请输入不含认证信息或参数的接口路径或URL")
		}
		text = u.Path
	}
	text = strings.TrimRight(text, "/")
	if !strings.HasPrefix(text, "/") || path.Clean(text) != text || text == "/" || strings.ContainsAny(text, "?#%\\ \t\r\n") || strings.HasPrefix(text, "/api") || strings.HasPrefix(text, "/assets") || strings.HasPrefix(text, "/firmware") {
		return "", fmt.Errorf("接口路径无效或与系统保留路径冲突")
	}
	return text, nil
}

func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (a *App) updateOTAEndpoint(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decode(w, r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	text, err := normalizeEndpoint(input.Path)
	if err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	routes := a.config.Transfer
	for _, other := range []string{routes.ListPath, routes.ConfirmPath, routes.DownloadPath} {
		if pathsOverlap(text, other) {
			fail(w, 400, fmt.Errorf("与资源传输路径冲突"))
			return
		}
	}
	for _, mock := range a.config.Mocks {
		if strings.TrimRight(mock.Path, "/") == text || strings.HasPrefix(mock.Path, text+"/") {
			fail(w, 400, fmt.Errorf("路径已被接口模拟占用"))
			return
		}
	}
	old := a.config.OTAPath
	a.config.OTAPath = text
	if err := a.saveLocked(); err != nil {
		a.config.OTAPath = old
		fail(w, 500, err)
		return
	}
	respond(w, 200, map[string]string{"path": text})
}

func (a *App) updateTransfer(w http.ResponseWriter, r *http.Request) {
	var value Transfer
	if err := decode(w, r, &value); err != nil {
		fail(w, 400, err)
		return
	}
	paths := []*string{&value.ListPath, &value.ConfirmPath, &value.DownloadPath}
	for _, field := range paths {
		text, err := normalizeEndpoint(*field)
		if err != nil {
			fail(w, 400, err)
			return
		}
		*field = text
	}
	if value.ListPath == value.ConfirmPath || value.ListPath == value.DownloadPath || value.ConfirmPath == value.DownloadPath || strings.HasPrefix(value.ListPath, value.DownloadPath+"/") || strings.HasPrefix(value.ConfirmPath, value.DownloadPath+"/") {
		fail(w, 400, fmt.Errorf("列表、确认和下载路径不能互相重叠"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, endpoint := range []string{value.ListPath, value.ConfirmPath, value.DownloadPath} {
		if pathsOverlap(endpoint, a.config.OTAPath) {
			fail(w, 400, fmt.Errorf("与OTA接口路径冲突"))
			return
		}
	}
	for _, mock := range a.config.Mocks {
		if mock.Path == value.ListPath || mock.Path == value.ConfirmPath || mock.Path == value.DownloadPath || strings.HasPrefix(mock.Path, value.DownloadPath+"/") {
			fail(w, 400, fmt.Errorf("路径已被接口模拟占用"))
			return
		}
	}
	old := a.config.Transfer
	a.config.Transfer = value
	if err := a.saveLocked(); err != nil {
		a.config.Transfer = old
		fail(w, 500, err)
		return
	}
	respond(w, 200, value)
}

package lab

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const resourceList = "/transfer/resources"
const resourceConfirm = "/transfer/confirm"
const maxResourceBytes = 64 << 20

func (a *App) resourcePath(id string) string { return filepath.Join(a.dir, "resources", id) }
func resourceDevice(r *http.Request) string {
	if id := r.Header.Get("Device-Id"); id != "" {
		return id
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}
func (a *App) uploadResource(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxResourceBytes+(5<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		fail(w, 400, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, h, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, err)
		return
	}
	defer f.Close()
	name := filepath.Base(strings.ReplaceAll(h.Filename, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	kind := "BOOK"
	switch ext {
	case ".ef", ".ttf", ".otf":
		kind = "FONT"
	case ".ebook", ".txt", ".epub", ".pdf":
	default:
		fail(w, 400, fmt.Errorf("支持 ebook/txt/epub/pdf 和 ef/ttf/otf"))
		return
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		fail(w, 500, err)
		return
	}
	item := Resource{ID: hex.EncodeToString(token), Name: name, Type: kind, UploadedAt: time.Now(), HiddenFor: []string{}}
	file, err := os.Create(a.resourcePath(item.ID))
	if err != nil {
		fail(w, 500, err)
		return
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(f, maxResourceBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || n == 0 || n > maxResourceBytes {
		os.Remove(a.resourcePath(item.ID))
		fail(w, 400, fmt.Errorf("文件为空、超过 64 MiB 或写入失败"))
		return
	}
	cover, err := readResourceCover(r)
	if err != nil {
		os.Remove(a.resourcePath(item.ID))
		fail(w, 400, err)
		return
	}
	if cover != nil {
		if err = os.WriteFile(a.resourcePath(item.ID)+".cover.png", cover, 0600); err != nil {
			os.Remove(a.resourcePath(item.ID))
			fail(w, 500, err)
			return
		}
		item.CustomCover = true
	}
	item.Size = n
	item.SHA256 = hex.EncodeToString(hash.Sum(nil))
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.config.Resources
	a.config.Resources = append(append([]Resource{}, old...), item)
	if err = a.saveLocked(); err != nil {
		a.config.Resources = old
		os.Remove(a.resourcePath(item.ID) + ".cover.png")
		os.Remove(a.resourcePath(item.ID))
		fail(w, 500, err)
		return
	}
	respond(w, 200, item)
}
func (a *App) manageResource(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/cover") {
		a.updateResourceCover(w, r)
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/api/resources/")
	reset := strings.HasSuffix(tail, "/reset")
	id := strings.TrimSuffix(tail, "/reset")
	if !(r.Method == "DELETE" && !reset || r.Method == "POST" && reset) {
		fail(w, 405, fmt.Errorf("method not allowed"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.config.Resources
	next := append([]Resource{}, old...)
	index := -1
	for i, v := range next {
		if v.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		fail(w, 404, fmt.Errorf("资源不存在"))
		return
	}
	if reset {
		next[index].HiddenFor = []string{}
	} else {
		next = append(next[:index], next[index+1:]...)
	}
	a.config.Resources = next
	if err := a.saveLocked(); err != nil {
		a.config.Resources = old
		fail(w, 500, err)
		return
	}
	if !reset {
		_ = os.Remove(a.resourcePath(id))
		_ = os.Remove(a.resourcePath(id) + ".cover.png")
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) resourceProtocol(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	event := eventFor(r, body)
	writer := &otaResponseWriter{ResponseWriter: w, event: &event}
	w = writer
	defer func() {
		writer.finish(r)
		event.Bytes = writer.written
		event.DurationMS = time.Since(start).Milliseconds()
		a.record(event)
	}()
	if err != nil {
		fail(w, 413, err)
		return
	}
	device := resourceDevice(r)
	if r.URL.Path == resourceList && r.Method == "GET" {
		a.mu.RLock()
		items := []map[string]any{}
		base := strings.TrimRight(a.config.OTA.PublicBase, "/")
		if base == "" {
			base = "http://" + r.Host
		}
		for _, v := range a.config.Resources {
			if slices.Contains(v.HiddenFor, device) {
				continue
			}
			items = append(items, map[string]any{"id": v.ID, "type": v.Type, "name": v.Name, "downloadUrl": base + "/resources/" + v.ID, "coverUrl": base + "/resources/" + v.ID + "/cover.png", "size": v.Size, "sha256": v.SHA256})
		}
		a.mu.RUnlock()
		event.Note = "资源列表"
		respond(w, 200, map[string]any{"code": 0, "data": items})
		return
	}
	if r.URL.Path == resourceConfirm && r.Method == "POST" {
		var input struct {
			ResourceID string `json:"resourceId"`
		}
		if err := json.Unmarshal(body, &input); err != nil || input.ResourceID == "" {
			fail(w, 400, fmt.Errorf("缺少有效 resourceId"))
			return
		}
		a.mu.Lock()
		old := a.config.Resources
		next := append([]Resource{}, old...)
		for i, v := range next {
			if v.ID == input.ResourceID && !slices.Contains(v.HiddenFor, device) {
				next[i].HiddenFor = append(append([]string{}, v.HiddenFor...), device)
			}
		}
		a.config.Resources = next
		err := a.saveLocked()
		if err != nil {
			a.config.Resources = old
		}
		a.mu.Unlock()
		if err != nil {
			fail(w, 500, err)
			return
		}
		event.Note = "资源接收确认"
		respond(w, 200, map[string]any{"code": 0})
		return
	}
	fail(w, 405, fmt.Errorf("method not allowed"))
}
func (a *App) resourceDownload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	event := eventFor(r, nil)
	event.Phase = "started"
	a.record(event)
	defer func() {
		event.DurationMS = time.Since(start).Milliseconds()
		if event.DurationMS > 0 {
			event.SpeedBPS = event.Bytes * 1000 / event.DurationMS
		}
		if event.Phase == "started" {
			event.Phase = "failed"
		}
		a.record(event)
	}()
	if r.Method != "GET" && r.Method != "HEAD" {
		event.Status = 405
		http.Error(w, "method not allowed", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/resources/")
	a.mu.RLock()
	var item Resource
	for _, v := range a.config.Resources {
		if v.ID == id {
			item = v
			break
		}
	}
	a.mu.RUnlock()
	if item.ID == "" {
		event.Status = 404
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(a.resourcePath(item.ID))
	if err != nil {
		event.Status = 404
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	event.Note = "资源下载：" + item.Name
	event.TotalBytes = item.Size
	event.Status = 200
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(item.Size, 10))
	w.Header().Set("Cache-Control", "no-store")
	event.ResponseHeaders = map[string]string{"Content-Type": "application/octet-stream", "Content-Length": strconv.FormatInt(item.Size, 10)}
	if r.Method == "HEAD" {
		event.Phase = "completed"
		return
	}
	event.Phase = "downloading"
	a.record(event)
	last := time.Now()
	writer := &progressWriter{writer: w, onWrite: func(n int) {
		event.Bytes += int64(n)
		if time.Since(last) >= 500*time.Millisecond {
			event.DurationMS = time.Since(start).Milliseconds()
			if event.DurationMS > 0 {
				event.SpeedBPS = event.Bytes * 1000 / event.DurationMS
			}
			event.Steps = append(event.Steps, TransferStep{Time: time.Now(), Bytes: event.Bytes, Phase: "downloading", Message: "正在发送资源"})
			if len(event.Steps) > 120 {
				event.Steps = event.Steps[len(event.Steps)-120:]
			}
			a.record(event)
			last = time.Now()
		}
	}}
	_, err = io.CopyBuffer(writer, f, make([]byte, 32*1024))
	if err != nil {
		event.Phase = "interrupted"
		event.Note += "；连接中断：" + err.Error()
	} else {
		event.Phase = "completed"
		event.Note += "；文件已发送（不代表设备已保存）"
	}
	event.Steps = append(event.Steps, TransferStep{Time: time.Now(), Bytes: event.Bytes, Phase: event.Phase, Message: event.Note})
}

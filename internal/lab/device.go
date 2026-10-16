package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Capture what the handler actually writes, including error responses.
type otaResponseWriter struct {
	http.ResponseWriter
	event    *Event
	writeErr error
	started  bool
}

func (w *otaResponseWriter) WriteHeader(status int) {
	if w.started {
		return
	}
	w.started = true
	w.event.Status = status
	w.event.ResponseHeaders = map[string]string{}
	for key, values := range w.Header() {
		w.event.ResponseHeaders[key] = strings.Join(values, ", ")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *otaResponseWriter) Write(data []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.event.ResponseBody = limitedText(append([]byte(w.event.ResponseBody), data...))
	if err != nil {
		w.writeErr = err
	}
	return n, err
}

func (w *otaResponseWriter) finish(r *http.Request) {
	// Force Go's buffered response onto the connection so flush failures are visible.
	if w.started {
		if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) && w.writeErr == nil {
			w.writeErr = err
		}
	}
	if w.writeErr != nil {
		w.event.Phase = "failed"
		w.event.Note += "；响应写入失败: " + w.writeErr.Error()
		log.Printf("OTA response write failed: method=%s path=%s remote=%s status=%d error=%v", r.Method, r.URL.Path, r.RemoteAddr, w.event.Status, w.writeErr)
	}
}

func wait(r *http.Request, ms int) bool {
	if ms <= 0 {
		return true
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}
func (a *App) otaSnapshot() (OTA, *Firmware) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	config := a.config.OTA
	for _, f := range a.config.Firmware {
		if f.ID == config.FirmwareID {
			copy := f
			return config, &copy
		}
	}
	return config, nil
}
func eventFor(r *http.Request, body []byte) Event {
	headers := map[string]string{}
	for _, key := range []string{"Content-Type", "Content-Length", "User-Agent", "Device-Id", "Client-Id", "Accept"} {
		if value := r.Header.Get(key); value != "" {
			headers[key] = value
		}
	}
	return Event{RequestHeaders: headers, ID: requestSequence.Add(1), Time: time.Now(), Method: r.Method, Path: r.URL.RequestURI(), Remote: r.RemoteAddr,
		DeviceID: r.Header.Get("Device-Id"), Version: deviceVersion(body), Body: limitedText(body)}
}

func (a *App) upstream(r *http.Request, config OTA, body []byte, activate bool) ([]byte, int, error) {
	target, err := url.Parse(config.Upstream)
	if err != nil {
		return nil, 0, err
	}
	if activate {
		target.Path = strings.TrimRight(target.Path, "/") + "/activate"
	}
	if r.URL.RawQuery != "" {
		query := target.Query()
		for key, values := range r.URL.Query() {
			query[key] = values
		}
		target.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	for _, key := range []string{"Content-Type", "Device-Id", "Client-Id", "Activation-Version", "Serial-Number", "User-Agent", "Accept-Language", "Authorization"} {
		if value := r.Header.Get(key); value != "" {
			request.Header.Set(key, value)
		}
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if len(data) > 1024*1024 {
		return nil, 0, fmt.Errorf("upstream response exceeds 1 MiB")
	}
	return data, response.StatusCode, err
}

func (a *App) ota(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "GET" {
		w.Header().Set("Allow", "POST, GET")
		http.Error(w, "method not allowed", 405)
		return
	}
	start := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	body, err := io.ReadAll(r.Body)
	event := eventFor(r, body)
	writer := &otaResponseWriter{ResponseWriter: w, event: &event}
	w = writer
	defer func() { event.DurationMS = time.Since(start).Milliseconds(); a.record(event) }()
	defer writer.finish(r)
	if err != nil {
		event.Status = 413
		event.Note = "请求体超过限制"
		fail(w, 413, err)
		return
	}
	config, firmware := a.otaSnapshot()
	if !wait(r, config.DelayMS) {
		event.Status = 499
		event.Note = "等待时连接关闭"
		return
	}
	if config.Mode == "check-error" {
		event.Status = 503
		event.Note = "模拟检查失败"
		fail(w, 503, fmt.Errorf("simulated OTA check failure"))
		return
	}
	root := map[string]any{"server_time": map[string]any{"timestamp": time.Now().UnixMilli(), "timezone_offset": 0}}
	if config.Forward {
		data, status, err := a.upstream(r, config, body, false)
		if err != nil {
			event.Status = 502
			event.Note = "上游转发失败"
			fail(w, 502, fmt.Errorf("upstream unavailable: %w", err))
			return
		}
		if status != 200 {
			event.Status = status
			event.Note = "保留上游错误"
			w.WriteHeader(status)
			n, _ := w.Write(data)
			event.Bytes = int64(n)
			return
		}
		if err = json.Unmarshal(data, &root); err != nil || root == nil {
			event.Status = 502
			event.Note = "上游未返回 JSON 对象"
			fail(w, 502, fmt.Errorf("upstream returned invalid JSON object"))
			return
		}
		if code, exists := root["code"]; exists && fmt.Sprint(code) != "0" && fmt.Sprint(code) != "200" {
			event.Note = "上游业务错误，未发布固件"
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadGateway)
			n, _ := w.Write(data)
			event.Bytes = int64(n)
			return
		}
	}
	delete(root, "firmware")
	if config.Mode != "no-update" {
		if firmware == nil {
			event.Status = 409
			event.Note = "没有发布固件"
			fail(w, 409, fmt.Errorf("no published firmware"))
			return
		}
		base := strings.TrimRight(config.PublicBase, "/")
		if base == "" {
			base = "http://" + r.Host
		}
		root["firmware"] = map[string]any{"version": firmware.Version, "url": base + "/firmware/" + firmware.ID + ".bin"}
		event.Note = "发布 " + firmware.Version
	} else {
		event.Note = "无更新"
	}
	encoded, _ := json.Marshal(root)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.Header().Set("Cache-Control", "no-store")
	event.Status = 200
	n, _ := w.Write(encoded)
	event.Bytes = int64(n)
}

func (a *App) activate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	body, err := io.ReadAll(r.Body)
	event := eventFor(r, body)
	start := time.Now()
	writer := &otaResponseWriter{ResponseWriter: w, event: &event}
	w = writer
	defer func() { event.DurationMS = time.Since(start).Milliseconds(); a.record(event) }()
	defer writer.finish(r)
	if err != nil {
		event.Status = 413
		fail(w, 413, err)
		return
	}
	config, _ := a.otaSnapshot()
	if !config.Forward {
		event.Status = 404
		event.Note = "未启用激活转发"
		fail(w, 404, fmt.Errorf("activation forwarding disabled"))
		return
	}
	data, status, err := a.upstream(r, config, body, true)
	if err != nil {
		event.Status = 502
		fail(w, 502, err)
		return
	}
	event.Status = status
	event.Note = "激活转发"
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	n, _ := w.Write(data)
	event.Bytes = int64(n)
}

func (a *App) download(w http.ResponseWriter, r *http.Request) {
	event := eventFor(r, nil)
	start := time.Now()
	event.Phase = "started"
	event.Note = "开始处理固件下载"
	a.mu.RLock()
	if event.DeviceID == "" {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		for i := len(a.events) - 1; i >= 0; i-- {
			old := a.events[i]
			oldHost, _, _ := net.SplitHostPort(old.Remote)
			if oldHost == host && old.DeviceID != "" && strings.HasPrefix(old.Path, "/xiaozhi/ota") && time.Since(old.Time) < 30*time.Minute {
				event.DeviceID = old.DeviceID
				event.Version = old.Version
				break
			}
		}
	}
	a.mu.RUnlock()
	a.record(event)
	defer func() {
		event.DurationMS = time.Since(start).Milliseconds()
		if event.Phase == "started" {
			event.Phase = "failed"
		}
		if event.DurationMS > 0 {
			event.SpeedBPS = event.Bytes * 1000 / event.DurationMS
		}
		event.Steps = append(event.Steps, TransferStep{Time: time.Now(), Bytes: event.Bytes, Phase: event.Phase, Message: event.Note})
		a.record(event)
	}()
	if r.Method != "GET" && r.Method != "HEAD" {
		event.Status = 405
		event.Note = "不支持的方法"
		http.Error(w, "method not allowed", 405)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/firmware/"), ".bin")
	a.mu.RLock()
	var found *Firmware
	for _, f := range a.config.Firmware {
		if f.ID == id {
			copy := f
			found = &copy
		}
	}
	config := a.config.OTA
	a.mu.RUnlock()
	if found == nil {
		event.Status = 404
		event.Note = "固件不存在"
		http.NotFound(w, r)
		return
	}
	event.FirmwareVersion = found.Version
	if id == config.FirmwareID && config.Mode == "download-error" {
		event.Status = 503
		event.Note = "模拟下载失败"
		http.Error(w, "simulated download failure", 503)
		return
	}
	file, err := os.Open(firmwarePath(a.dir, id))
	if err != nil {
		event.Status = 404
		event.Note = "固件文件不可读"
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		event.Status = 500
		event.Note = "无法读取文件信息"
		http.Error(w, "cannot read firmware", 500)
		return
	}
	event.TotalBytes = info.Size()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	event.ResponseHeaders = map[string]string{"Content-Type": "application/octet-stream", "Content-Length": strconv.FormatInt(info.Size(), 10)}
	event.Status = 200
	if r.Method == "HEAD" {
		event.Phase = "completed"
		event.Note = "仅返回文件信息"
		return
	}
	event.Phase = "downloading"
	event.Note = "正在发送固件 " + found.Version
	event.Steps = append(event.Steps, TransferStep{Time: time.Now(), Phase: "downloading", Message: "下载开始，文件长度 " + strconv.FormatInt(info.Size(), 10) + " 字节"})
	a.record(event)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	last := time.Now()
	lastBytes := int64(0)
	writer := &progressWriter{writer: w, onWrite: func(n int) {
		event.Bytes += int64(n)
		now := time.Now()
		elapsed := now.Sub(last)
		if elapsed >= 500*time.Millisecond {
			event.SpeedBPS = int64(float64(event.Bytes-lastBytes) / elapsed.Seconds())
			event.DurationMS = now.Sub(start).Milliseconds()
			event.Steps = append(event.Steps, TransferStep{Time: now, Bytes: event.Bytes, Phase: "downloading", Message: "已发送 " + strconv.FormatInt(event.Bytes, 10) + " 字节"})
			if len(event.Steps) > 120 {
				event.Steps = append(event.Steps[:1], event.Steps[len(event.Steps)-119:]...)
			}
			a.record(event)
			last = now
			lastBytes = event.Bytes
		}
	}}
	var source io.Reader = file
	truncated := id == config.FirmwareID && config.Mode == "truncate"
	if truncated {
		source = io.LimitReader(file, info.Size()/2)
	}
	_, err = io.CopyBuffer(writer, source, make([]byte, 32*1024))
	if err != nil {
		event.Phase = "interrupted"
		event.Note = "下载连接中断：" + err.Error()
	} else if truncated {
		event.Phase = "interrupted"
		event.Note = "模拟中断：只发送一半"
	} else {
		event.Phase = "completed"
		event.Note = "文件发送完成（设备升级结果需另行确认）"
	}
}

type progressWriter struct {
	writer  io.Writer
	onWrite func(int)
}

func (w *progressWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.onWrite(n)
	return n, err
}

func (a *App) mock(w http.ResponseWriter, r *http.Request, m Mock) {
	start := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	body, err := io.ReadAll(r.Body)
	event := eventFor(r, body)
	defer func() { event.DurationMS = time.Since(start).Milliseconds(); a.record(event) }()
	if err != nil {
		event.Status = 413
		fail(w, 413, err)
		return
	}
	if !wait(r, m.DelayMS) {
		event.Status = 499
		return
	}
	contentType := m.ContentType
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	for key, value := range m.Headers {
		w.Header().Set(key, value)
	}
	event.ResponseHeaders = map[string]string{}
	for key, values := range w.Header() {
		event.ResponseHeaders[key] = strings.Join(values, ", ")
	}
	w.WriteHeader(m.Status)
	event.Status = m.Status
	event.Note = "模拟接口：" + m.Name
	if r.Method != "HEAD" && m.Status != 204 && m.Status != 304 {
		event.ResponseBody = limitedText([]byte(m.Body))
		n, _ := io.WriteString(w, m.Body)
		event.Bytes = int64(n)
	}
}

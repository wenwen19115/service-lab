package lab

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type TestRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
}

func (a *App) testEndpoint(w http.ResponseWriter, r *http.Request) {
	var input TestRequest
	if err := decode(w, r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	parsed, err := url.Parse(input.Path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(input.Path, "/") || strings.HasPrefix(parsed.Path, "/api") || parsed.Fragment != "" {
		fail(w, 400, fmt.Errorf("调试路径必须为本地设备接口路径，不能请求管理 API"))
		return
	}
	switch input.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		fail(w, 400, fmt.Errorf("HTTP 方法无效"))
		return
	}
	if err := validateHeaders(input.Headers); err != nil {
		fail(w, 400, err)
		return
	}
	host, port, err := net.SplitHostPort(a.ListenAddr)
	if err != nil {
		fail(w, 503, fmt.Errorf("服务监听地址不可用"))
		return
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	target := "http://" + net.JoinHostPort(host, port) + parsed.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), input.Method, target, bytes.NewBufferString(input.Body))
	if err != nil {
		fail(w, 400, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ServiceLab-API-Tester")
	for key, value := range input.Headers {
		req.Header.Set(key, value)
	}
	started := time.Now()
	client := http.Client{Timeout: 35 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		fail(w, 502, fmt.Errorf("接口调试失败：%w", err))
		return
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		fail(w, 502, err)
		return
	}
	truncated := len(data) > 1024*1024
	if truncated {
		data = data[:1024*1024]
	}
	respond(w, 200, map[string]any{"status": response.StatusCode, "headers": response.Header, "body": string(data), "durationMs": time.Since(started).Milliseconds(), "truncated": truncated})
}

var headerPattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_-]*$")

func validateHeaders(headers map[string]string) error {
	if len(headers) > 30 {
		return fmt.Errorf("最多设置 30 个 HTTP 头")
	}
	for key, value := range headers {
		if !headerPattern.MatchString(key) || strings.ContainsAny(value, "\r\n") || len(value) > 8192 {
			return fmt.Errorf("无效的 HTTP 头：%s", key)
		}
		switch http.CanonicalHeaderKey(key) {
		case "Content-Length", "Transfer-Encoding", "Connection", "Host", "Trailer", "Upgrade":
			return fmt.Errorf("不能自行设置协议控制头：%s", key)
		}
	}
	return nil
}

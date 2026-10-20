package lab

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConnState also sees connections which never deliver a complete HTTP header.
func DiagnosticConnState(c net.Conn, s http.ConnState) {
	if s == http.StateNew || s == http.StateClosed || s == http.StateHijacked {
		log.Printf("[NETDIAG] connection remote=%s local=%s state=%s", c.RemoteAddr(), c.LocalAddr(), s)
	}
}

type diagnosticBody struct {
	io.ReadCloser
	start         time.Time
	remote, trace string
	bytes         int64
	first         bool
}

func (b *diagnosticBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes += int64(n)
	if !b.first || err != nil {
		log.Printf("[NETDIAG] body_read remote=%s trace=%q bytes=%d elapsed_ms=%d err=%v", b.remote, b.trace, b.bytes, time.Since(b.start).Milliseconds(), err)
		b.first = true
	}
	return n, err
}

type diagnosticWriter struct {
	http.ResponseWriter
	status        int
	bytes         int64
	start         time.Time
	remote, trace string
	writeErr      error
}

func (w *diagnosticWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *diagnosticWriter) WriteHeader(s int) {
	if w.status != 0 {
		return
	}
	w.status = s
	log.Printf("[NETDIAG] response_headers remote=%s trace=%q status=%d elapsed_ms=%d", w.remote, w.trace, s, time.Since(w.start).Milliseconds())
	w.ResponseWriter.WriteHeader(s)
}
func (w *diagnosticWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	if err != nil {
		w.writeErr = err
	}
	return n, err
}
func (w *diagnosticWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.writeErr = err
	}
}
func diagnosticTrace(r *http.Request) string {
	trace := r.Header.Get("X-Diagnostic-Id")
	if len(trace) > 80 {
		return trace[:80]
	}
	return trace
}

func (a *App) diagnoseRequest(w http.ResponseWriter, r *http.Request) func() {
	start := time.Now()
	trace := diagnosticTrace(r)
	log.Printf("[NETDIAG] request_begin remote=%s trace=%q method=%s path=%s content_length=%d", r.RemoteAddr, trace, r.Method, r.URL.Path, r.ContentLength)
	body := &diagnosticBody{ReadCloser: r.Body, start: start, remote: r.RemoteAddr, trace: trace}
	r.Body = body
	return func() {
		log.Printf("[NETDIAG] request_end remote=%s trace=%q method=%s path=%s body_bytes=%d elapsed_ms=%d context_err=%v", r.RemoteAddr, trace, r.Method, r.URL.Path, body.bytes, time.Since(start).Milliseconds(), r.Context().Err())
	}
}
func (a *App) DiagnosticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !(strings.HasPrefix(r.URL.Path, "/xiaozhi/") || strings.HasPrefix(r.URL.Path, "/transfer/") || strings.HasPrefix(r.URL.Path, "/resources/") || strings.HasPrefix(r.URL.Path, "/firmware/")) {
			a.ServeHTTP(w, r)
			return
		}
		end := a.diagnoseRequest(w, r)
		defer end()
		trace := diagnosticTrace(r)
		writer := &diagnosticWriter{ResponseWriter: w, start: time.Now(), remote: r.RemoteAddr, trace: trace}
		defer func() {
			log.Printf("[NETDIAG] response_end remote=%s trace=%q status=%d bytes=%d elapsed_ms=%d write_err=%v", r.RemoteAddr, trace, writer.status, writer.bytes, time.Since(writer.start).Milliseconds(), writer.writeErr)
		}()
		a.ServeHTTP(writer, r)
		if writer.status == 0 {
			writer.WriteHeader(http.StatusOK)
		}
	})
}
func (a *App) diagnosticLog(w http.ResponseWriter, r *http.Request) {
	file, err := os.Open(filepath.Join(a.dir, "service-lab.log"))
	if err != nil {
		fail(w, 404, fmt.Errorf("日志暂不可读: %w", err))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fail(w, 500, err)
		return
	}
	start := int64(0)
	if info.Size() > 1<<20 {
		start = info.Size() - (1 << 20)
	}
	if _, err = file.Seek(start, io.SeekStart); err != nil {
		fail(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "attachment; filename=service-lab-diagnostics.log")
	_, _ = io.CopyN(w, file, info.Size()-start)
}

package lab

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOTABusinessErrorDoesNotPublish(t *testing.T) {
	for _, code := range []string{`500`, `"500"`, `null`, `{}`} {
		t.Run(code, func(t *testing.T) {
			body := `{"code":` + code + `,"msg":"upstream failed","data":null}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer upstream.Close()
			app := appForTest(t)
			app.config.OTA = OTA{Mode: "update", Forward: true, Upstream: upstream.URL}
			rec := request(app, "POST", "/xiaozhi/ota/", "{}")
			if rec.Code != 502 || rec.Body.String() != body || strings.Contains(rec.Body.String(), `"firmware"`) {
				t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
			}
			event := app.events[len(app.events)-1]
			if event.Status != 502 || event.ResponseBody != body || event.ResponseHeaders["Content-Type"] != "application/json" {
				t.Fatalf("missing response diagnostics: %+v", event)
			}
		})
	}
}

type failedOTAWriter struct{ header http.Header }

func (w *failedOTAWriter) Header() http.Header       { return w.header }
func (w *failedOTAWriter) WriteHeader(int)           {}
func (w *failedOTAWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOTAWriteFailureRecorded(t *testing.T) {
	app := appForTest(t)
	app.config.OTA = OTA{Mode: "no-update"}
	w := &failedOTAWriter{header: make(http.Header)}
	app.ServeHTTP(w, httptest.NewRequest("POST", "/xiaozhi/ota/", strings.NewReader("{}")))
	event := app.events[len(app.events)-1]
	if event.Phase != "failed" || !strings.Contains(event.Note, io.ErrClosedPipe.Error()) || event.ResponseHeaders["Content-Length"] == "" {
		t.Fatalf("missing write failure diagnostics: %+v", event)
	}
}

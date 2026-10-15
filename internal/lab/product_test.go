package lab

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestHistorySurvivesRestartAndMarksIncompleteTransfers(t *testing.T) {
	app := appForTest(t)
	app.record(Event{ID: requestSequence.Add(1), Time: time.Now(), Path: "/mock/ok", Status: 200, RequestHeaders: map[string]string{"Content-Type": "application/json"}})
	incompleteID := requestSequence.Add(1)
	app.record(Event{ID: incompleteID, Time: time.Now(), Path: "/firmware/demo.bin", Phase: "downloading", Bytes: 512, TotalBytes: 1024})
	app.Close()
	reopened, err := New(app.dir, app.frontend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	if len(reopened.events) != 2 || reopened.events[1].Phase != "interrupted" || reopened.events[1].Bytes != 512 || len(reopened.events[1].Steps) == 0 {
		t.Fatalf("unexpected history: %+v", reopened.events)
	}
	if reopened.events[0].RequestHeaders["Content-Type"] != "application/json" {
		t.Fatal("headers lost")
	}
	reopened.record(Event{ID: requestSequence.Add(1), Time: time.Now(), Path: "/mock/after", Status: 200})
	if reopened.events[2].ID <= incompleteID {
		t.Fatal("request IDs reused")
	}
	rec := request(reopened, "DELETE", "/api/events", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	reopened.Close()
	data, err := os.ReadFile(filepath.Join(app.dir, "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	if err = json.Unmarshal(data, &events); err != nil || len(events) != 0 {
		t.Fatal("clear not persisted", string(data))
	}
}

func TestLocalTesterReturnsResponseAndRecordsActualRequest(t *testing.T) {
	app := appForTest(t)
	app.config.Mocks = []Mock{{ID: "test", Name: "echo fixture", Method: "POST", Path: "/mock/test", Enabled: true, Status: 201, ContentType: "application/json", Body: "{\"created\":true}", Headers: map[string]string{"X-Test": "yes"}}}
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	parsed, _ := url.Parse(server.URL)
	app.ListenAddr = parsed.Host
	rec := request(app, "POST", "/api/test", `{"method":"POST","path":"/mock/test?attempt=1","headers":{"Content-Type":"application/json","Device-Id":"device-fixture","Authorization":"secret"},"body":"{\"id\":1}"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var result struct {
		Status  int
		Body    string
		Headers map[string][]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != 201 || result.Body != app.config.Mocks[0].Body || result.Headers["X-Test"][0] != "yes" {
		t.Fatal(result)
	}
	app.mu.RLock()
	event := app.events[0]
	app.mu.RUnlock()
	if event.DeviceID != "device-fixture" || event.Body != "{\"id\":1}" || event.RequestHeaders["Authorization"] != "" {
		t.Fatal(event)
	}
	for _, body := range []string{`{"method":"GET","path":"https://example.com/"}`, `{"method":"DELETE","path":"/api/events"}`, `{"method":"GET","path":"/%61pi/state"}`, `{"method":"GET","path":"/mock/test","headers":{"Host":"evil"}}`, `{"method":"GET","path":"/mock/test","headers":{"X-Test":"a\r\nb"}}`} {
		bad := request(app, "POST", "/api/test", body)
		if bad.Code != 400 {
			t.Fatal("unsafe tester request accepted", body, bad.Code)
		}
	}
}

func TestMockNoBodyStatusesAndCustomHeaderValidation(t *testing.T) {
	app := appForTest(t)
	for _, status := range []int{204, 304} {
		m := Mock{ID: "empty", Name: "empty", Method: "GET", Path: "/mock/empty", Enabled: true, Status: status, Body: "must not send", Headers: map[string]string{"X-Trace": "fixture"}}
		if err := validateMock(m); err != nil {
			t.Fatal(err)
		}
		app.config.Mocks = []Mock{m}
		rec := request(app, "GET", m.Path, "")
		if rec.Code != status || rec.Body.Len() != 0 || rec.Header().Get("X-Trace") != "fixture" {
			t.Fatal(rec)
		}
		app.mu.RLock()
		event := app.events[len(app.events)-1]
		app.mu.RUnlock()
		if event.Bytes != 0 || event.ResponseBody != "" {
			t.Fatal("record claims unsent body", event)
		}
	}
	if err := validateMock(Mock{Method: "GET", Path: "/mock/bad", Status: 200, Headers: map[string]string{"Content-Length": "1"}}); err == nil {
		t.Fatal("protocol header accepted")
	}
}

func TestManagementFirmwareDownloadIgnoresFailureScenario(t *testing.T) {
	app := appForTest(t)
	rec := uploadFixture(t, app, fixture("2.0.63"))
	if rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	var f Firmware
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	app.config.OTA.Mode = "download-error"
	app.config.OTA.FirmwareID = f.ID
	download := request(app, "GET", "/api/firmware/"+f.ID+"/file", "")
	if download.Code != 200 || download.Body.Len() != 2048 || !strings.Contains(download.Header().Get("Content-Disposition"), "app.bin") {
		t.Fatal(download.Code, download.Body.Len())
	}
	ranged := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/firmware/"+f.ID+"/file", nil)
	req.Header.Set("Range", "bytes=0-79")
	app.ServeHTTP(ranged, req)
	if ranged.Code != 206 || ranged.Body.Len() != 80 {
		t.Fatal("range download failed")
	}
	missing := request(app, "GET", "/api/firmware/../../config.json/file", "")
	if missing.Code != 404 {
		t.Fatal("invalid ID accepted")
	}
	response := request(app, "GET", "/api/state", "")
	data, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(data), "\"server\"") {
		t.Fatal("server metadata missing")
	}
	var metadata struct {
		Server struct {
			Version   string `json:"version"`
			BuildTime string `json:"buildTime"`
		} `json:"server"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Server.Version != Version || metadata.Server.BuildTime != BuildTime {
		t.Fatal("build metadata does not match executable")
	}
}

func TestSummaryAndDetailDoNotDiscardStoredPayload(t *testing.T) {
	app := appForTest(t)
	id := requestSequence.Add(1)
	app.record(Event{ID: id, Time: time.Now(), Path: "/mock/detail", Status: 200, Body: "{\"request\":true}", ResponseBody: "{\"response\":true}", Steps: []TransferStep{{Time: time.Now(), Phase: "completed", Bytes: 123}}, RequestHeaders: map[string]string{"Device-Id": "fixture"}, ResponseHeaders: map[string]string{"X-Test": "fixture"}})
	summary := request(app, "GET", "/api/state?summary=1", "")
	var state struct{ Events []Event }
	if err := json.Unmarshal(summary.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Events) != 1 || state.Events[0].Body != "" || len(state.Events[0].Steps) != 0 {
		t.Fatal("summary contains heavy payload")
	}
	detail := request(app, "GET", fmt.Sprintf("/api/events/%d", id), "")
	var event Event
	if err := json.Unmarshal(detail.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Body == "" || event.ResponseBody == "" || len(event.Steps) != 1 || event.RequestHeaders["Device-Id"] != "fixture" {
		t.Fatal("stored detail was altered", event)
	}
	if request(app, "GET", "/api/events/not-a-number", "").Code != 400 {
		t.Fatal("invalid ID accepted")
	}
	if request(app, "GET", "/api/events/0", "").Code != 404 {
		t.Fatal("missing event not rejected")
	}
}

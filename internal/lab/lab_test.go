package lab

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func appForTest(t *testing.T) *App {
	t.Helper()
	app, err := New(t.TempDir(), fstest.MapFS{"index.html": {Data: []byte("<html>Service Lab</html>")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}
func request(app *App, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Device-Id", "10:20:ba:6d:fe:4c")
	app.ServeHTTP(rec, req)
	return rec
}
func fixture(version string) []byte {
	data := make([]byte, 2048)
	data[0] = 0xe9
	binary.LittleEndian.PutUint32(data[32:36], 0xabcd5432)
	copy(data[48:80], version)
	return data
}
func uploadFixture(t *testing.T, app *App, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "app.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(data)
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/api/firmware", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestUploadVersionDedupAndPersistence(t *testing.T) {
	app := appForTest(t)
	rec := uploadFixture(t, app, fixture("2.0.62"))
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var firmware Firmware
	if err := json.Unmarshal(rec.Body.Bytes(), &firmware); err != nil {
		t.Fatal(err)
	}
	if firmware.Version != "2.0.62" || firmware.Size != 2048 || len(firmware.SHA256) != 64 {
		t.Fatal(firmware)
	}
	if uploadFixture(t, app, fixture("2.0.62")).Code != 200 {
		t.Fatal("duplicate failed")
	}
	reopened, err := New(app.dir, app.frontend)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.config.Firmware) != 1 || reopened.config.Firmware[0].ID != firmware.ID {
		t.Fatal("persistence failed")
	}
	if uploadFixture(t, app, []byte("not a firmware")).Code != 400 {
		t.Fatal("invalid upload accepted")
	}
}

func TestOTAOverridePreservesCloudFieldsAndActivation(t *testing.T) {
	var activationBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("appkey") != "key" {
			t.Error("lost appkey")
		}
		if r.Header.Get("Device-Id") != "10:20:ba:6d:fe:4c" {
			t.Error("lost Device-Id")
		}
		if strings.HasSuffix(r.URL.Path, "activate") {
			body, _ := io.ReadAll(r.Body)
			activationBody = string(body)
			w.WriteHeader(201)
			_, _ = w.Write([]byte("{}"))
			return
		}
		_, _ = w.Write([]byte(`{"websocket":{"url":"wss://cloud/chat","token":"secret"},"activation":{"code":"1234"},"server_time":{"timestamp":123},"firmware":{"version":"99","url":"https://wrong/firmware"}}`))
	}))
	defer upstream.Close()
	app := appForTest(t)
	uploaded := uploadFixture(t, app, fixture("2.0.62"))
	var firmware Firmware
	_ = json.Unmarshal(uploaded.Body.Bytes(), &firmware)
	app.config.OTA = OTA{Mode: "update", FirmwareID: firmware.ID, Forward: true, Upstream: upstream.URL + "/ota?appkey=key", PublicBase: "http://192.168.1.10:8080"}
	rec := request(app, "POST", "/xiaozhi/ota/", `{"application":{"version":"2.0.61"}}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &root)
	if !bytes.Contains(root["websocket"], []byte("wss://cloud/chat")) || !bytes.Contains(root["activation"], []byte("1234")) {
		t.Fatal("lost cloud fields")
	}
	var announced struct{ Version, URL string }
	_ = json.Unmarshal(root["firmware"], &announced)
	if announced.Version != "2.0.62" || !strings.HasPrefix(announced.URL, "http://192.168.1.10:8080/firmware/") {
		t.Fatal(announced)
	}
	if app.events[0].Version != "2.0.61" {
		t.Fatal("device version missing")
	}
	activation := request(app, "POST", "/xiaozhi/ota/activate", `{"challenge":"x"}`)
	if activation.Code != 201 || activationBody != `{"challenge":"x"}` {
		t.Fatal("activation forwarding failed")
	}
	app.config.OTA.Mode = "no-update"
	rec = request(app, "POST", "/xiaozhi/ota/", "{}")
	_ = json.Unmarshal(rec.Body.Bytes(), &root)
	// Unmarshal into a fresh map: map reuse preserves missing keys.
	root = map[string]json.RawMessage{}
	_ = json.Unmarshal(rec.Body.Bytes(), &root)
	if _, exists := root["firmware"]; exists {
		t.Fatal("upstream firmware not removed")
	}
}

func TestOTAErrorsNeverInventCloudConfiguration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not JSON")) }))
	defer upstream.Close()
	app := appForTest(t)
	app.config.OTA.Upstream = upstream.URL
	if rec := request(app, "POST", "/xiaozhi/ota/", "{}"); rec.Code != 502 {
		t.Fatal(rec.Code)
	}
	app.config.OTA.Mode = "check-error"
	if rec := request(app, "POST", "/xiaozhi/ota/", "{}"); rec.Code != 503 {
		t.Fatal(rec.Code)
	}
	app.config.OTA = OTA{Mode: "no-update"}
	rec := request(app, "POST", "/xiaozhi/ota/", "{}")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "server_time") {
		t.Fatal(rec.Code)
	}
}

func TestDownloadFullErrorAndTruncated(t *testing.T) {
	app := appForTest(t)
	data := fixture("2.0.62")
	rec := uploadFixture(t, app, data)
	var f Firmware
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	app.config.OTA = OTA{Mode: "update", FirmwareID: f.ID}
	server := httptest.NewServer(app)
	defer server.Close()
	response, err := http.Get(server.URL + "/firmware/" + f.ID + ".bin")
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(content, data) || response.ContentLength != int64(len(data)) {
		t.Fatal("full download mismatch", err)
	}
	app.mu.Lock()
	app.config.OTA.Mode = "download-error"
	app.mu.Unlock()
	response, err = http.Get(server.URL + "/firmware/" + f.ID + ".bin")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal(response.StatusCode)
	}
	app.mu.Lock()
	app.config.OTA.Mode = "truncate"
	app.mu.Unlock()
	response, err = http.Get(server.URL + "/firmware/" + f.ID + ".bin")
	if err != nil {
		t.Fatal(err)
	}
	content, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != io.ErrUnexpectedEOF || len(content) != len(data)/2 || response.ContentLength != int64(len(data)) {
		t.Fatal("truncation not visible", err, len(content))
	}
}

func TestMocksValidationMatchingAndLogs(t *testing.T) {
	app := appForTest(t)
	mock := Mock{Name: "status", Method: "POST", Path: "/mock/status", Status: 202, ContentType: "application/json", Body: `{"ok":true}`, Enabled: true}
	encoded, _ := json.Marshal([]Mock{mock})
	rec := request(app, "PUT", "/api/mocks", string(encoded))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = request(app, "POST", "/mock/status?device=1", `{"state":1}`)
	if rec.Code != 202 || rec.Body.String() != mock.Body {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if request(app, "GET", "/mock/status", "").Code == 202 {
		t.Fatal("method ignored")
	}
	if len(app.events) != 1 || app.events[0].Body != `{"state":1}` {
		t.Fatal("event missing")
	}
	mock.Path = "/api/state"
	encoded, _ = json.Marshal([]Mock{mock})
	if request(app, "PUT", "/api/mocks", string(encoded)).Code != 400 {
		t.Fatal("reserved route accepted")
	}
	req := httptest.NewRequest("PUT", "/api/mocks", strings.NewReader("[]"))
	req.Header.Set("Origin", "https://elsewhere.example")
	blocked := httptest.NewRecorder()
	app.ServeHTTP(blocked, req)
	if blocked.Code != 403 {
		t.Fatal("cross-origin write accepted")
	}
}

func TestConfigValidationAndRingLimit(t *testing.T) {
	app := appForTest(t)
	if request(app, "PUT", "/api/ota", `{"mode":"update","firmwareId":"missing","publicBase":"","upstream":"","forward":false,"delayMs":0}`).Code != 400 {
		t.Fatal("missing firmware accepted")
	}
	if validateOTA(OTA{Mode: "no-update", PublicBase: "file:///tmp"}) == nil {
		t.Fatal("invalid URL accepted")
	}
	for i := 0; i < 350; i++ {
		app.record(Event{Status: 200})
	}
	if len(app.events) != 300 {
		t.Fatal(len(app.events))
	}
	if request(app, "DELETE", "/api/events", "").Code != 200 || len(app.events) != 0 {
		t.Fatal("clear failed")
	}
}

func TestCancellationAndUI(t *testing.T) {
	app := appForTest(t)
	rec := request(app, "GET", "/", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Service Lab") {
		t.Fatal("UI missing")
	}
	app.config.Mocks = []Mock{{Method: "POST", Path: "/mock/slow", Status: 200, DelayMS: 30000, Enabled: true}}
	req := httptest.NewRequest("POST", "/mock/slow", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	app.ServeHTTP(httptest.NewRecorder(), req)
	if app.events[0].Status != 499 {
		t.Fatal("cancellation not recorded")
	}
}

func TestPublishVersionOverrideLeavesImageUntouched(t *testing.T) {
	app := appForTest(t)
	data := fixture("2.0.61")
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	file, err := writer.CreateFormFile("file", "app.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(data)
	_ = writer.WriteField("version", "2.0.62")
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/api/firmware", &form)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var f Firmware
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	if f.Version != "2.0.62" || f.ImageVersion != "2.0.61" {
		t.Fatal(f)
	}
	stored, err := os.ReadFile(firmwarePath(app.dir, f.ID))
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("image changed", err)
	}
	rec = request(app, "PATCH", "/api/firmware/"+f.ID, `{"version":"2.0.63"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	reopened, err := New(app.dir, app.frontend)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.config.Firmware[0].Version != "2.0.63" || reopened.config.Firmware[0].ImageVersion != "2.0.61" {
		t.Fatal("version persistence failed")
	}
	app.config.OTA = OTA{Mode: "update", FirmwareID: f.ID, PublicBase: "http://192.168.1.2:8080"}
	rec = request(app, "POST", "/xiaozhi/ota/", "{}")
	if !strings.Contains(rec.Body.String(), `"version":"2.0.63"`) {
		t.Fatal(rec.Body.String())
	}
	if request(app, "PATCH", "/api/firmware/"+f.ID, `{"version":"invalid"}`).Code != 400 {
		t.Fatal("invalid version accepted")
	}
	if request(app, "PATCH", "/api/firmware/missing", `{"version":"2.0.64"}`).Code != 404 {
		t.Fatal("missing firmware accepted")
	}
}

func TestOldFirmwareMetadataMigration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"firmware":[{"id":"old","version":"2.0.61"}],"ota":{"mode":"no-update"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := New(dir, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	if app.config.Firmware[0].ImageVersion != "2.0.61" {
		t.Fatal("old version not retained")
	}
}

func TestFirmwareDeleteRestoreAndPublishedProtection(t *testing.T) {
	app := appForTest(t)
	data := fixture("2.0.62")
	rec := uploadFixture(t, app, data)
	var f Firmware
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	app.config.OTA = OTA{Mode: "update", FirmwareID: f.ID}
	rec = request(app, "DELETE", "/api/firmware/"+f.ID, "")
	if rec.Code != 409 {
		t.Fatal("published firmware deleted", rec.Code)
	}
	if len(app.config.Firmware) != 1 || len(app.config.Trash) != 0 {
		t.Fatal("blocked delete changed state")
	}
	app.config.OTA.Mode = "no-update"
	rec = request(app, "DELETE", "/api/firmware/"+f.ID, "")
	if rec.Code != 200 || len(app.config.Firmware) != 0 || len(app.config.Trash) != 1 || app.config.OTA.FirmwareID != "" {
		t.Fatal("delete failed", rec.Code)
	}
	if request(app, "GET", "/firmware/"+f.ID+".bin", "").Code != 404 {
		t.Fatal("trashed file still served")
	}
	if uploadFixture(t, app, data).Code != 409 {
		t.Fatal("trashed duplicate accepted")
	}
	reopened, err := New(app.dir, app.frontend)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.config.Trash) != 1 {
		t.Fatal("trash not persisted")
	}
	rec = request(app, "POST", "/api/firmware/"+f.ID+"/restore", "")
	if rec.Code != 200 || len(app.config.Firmware) != 1 || len(app.config.Trash) != 0 {
		t.Fatal("restore failed", rec.Code)
	}
	restored, err := os.ReadFile(firmwarePath(app.dir, f.ID))
	if err != nil || !bytes.Equal(restored, data) {
		t.Fatal("restored file changed", err)
	}
	if app.config.OTA.Mode != "no-update" || app.config.OTA.FirmwareID != "" {
		t.Fatal("restoration published firmware")
	}
	if request(app, "DELETE", "/api/firmware/missing", "").Code != 404 {
		t.Fatal("missing delete succeeded")
	}
}

type blockedDownloadWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
}

func (w *blockedDownloadWriter) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.ResponseRecorder.Write(data)
}
func TestDownloadVisibleBeforeCompletion(t *testing.T) {
	app := appForTest(t)
	rec := uploadFixture(t, app, fixture("2.0.62"))
	var f Firmware
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	writer := &blockedDownloadWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { app.ServeHTTP(writer, httptest.NewRequest("GET", "/firmware/"+f.ID+".bin", nil)); close(done) }()
	<-writer.entered
	app.mu.RLock()
	event := app.events[0]
	app.mu.RUnlock()
	if event.Phase != "downloading" || event.TotalBytes != 2048 || event.ID == 0 {
		close(writer.release)
		<-done
		t.Fatal("in-flight event not visible", event)
	}
	close(writer.release)
	<-done
	app.mu.RLock()
	defer app.mu.RUnlock()
	if len(app.events) != 1 || app.events[0].Phase != "completed" || app.events[0].Bytes != 2048 || len(app.events[0].Steps) < 2 {
		t.Fatal("download not finalized", app.events)
	}
}

type failedDownloadWriter struct{ *httptest.ResponseRecorder }

func (w *failedDownloadWriter) Write(data []byte) (int, error) {
	return 0, fmt.Errorf("test disconnected client")
}
func TestDownloadWriteFailureRecorded(t *testing.T) {
	app := appForTest(t)
	rec := uploadFixture(t, app, fixture("2.0.62"))
	var f Firmware
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	app.ServeHTTP(&failedDownloadWriter{httptest.NewRecorder()}, httptest.NewRequest("GET", "/firmware/"+f.ID+".bin", nil))
	if len(app.events) != 1 || app.events[0].Phase != "interrupted" || !strings.Contains(app.events[0].Note, "test disconnected") {
		t.Fatal(app.events)
	}
}

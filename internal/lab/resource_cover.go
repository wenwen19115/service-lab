package lab

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Normalize small grayscale covers to a predictable size for the device decoder.
func readResourceCover(r *http.Request) ([]byte, error) {
	f, _, err := r.FormFile("cover")
	if err == http.ErrMissingFile {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("封面不能超过 4 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return nil, fmt.Errorf("请选择 PNG/JPEG 封面，最多 1600 万像素")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("封面解码失败")
	}
	dst := image.NewGray(image.Rect(0, 0, 240, 320))
	for i := range dst.Pix {
		dst.Pix[i] = 255
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := 240, 320
	if sw*320 > sh*240 {
		dh = sh * 240 / sw
	} else {
		dw = sw * 320 / sh
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			c := src.At(src.Bounds().Min.X+x*sw/dw, src.Bounds().Min.Y+y*sh/dh)
			rr, gg, bb, aa := c.RGBA()
			gray := (299*uint64(rr)+587*uint64(gg)+114*uint64(bb))/1000 + uint64(65535-aa)
			dst.SetGray((240-dw)/2+x, (320-dh)/2+y, color.Gray{Y: uint8(gray >> 8)})
		}
	}
	var out bytes.Buffer
	err = png.Encode(&out, dst)
	return out.Bytes(), err
}
func defaultResourceCover(kind string) []byte {
	img := image.NewGray(image.Rect(0, 0, 240, 320))
	for i := range img.Pix {
		img.Pix[i] = 240
	}
	rect := func(x, y, w, h int, c uint8) {
		for j := y; j < y+h; j++ {
			for i := x; i < x+w; i++ {
				img.SetGray(i, j, color.Gray{Y: c})
			}
		}
	}
	rect(36, 34, 168, 252, 40)
	rect(44, 42, 152, 236, 255)
	rect(54, 42, 4, 236, 140)
	if kind == "FONT" {
		rect(87, 92, 80, 10, 40)
		rect(122, 102, 10, 94, 40)
		rect(101, 196, 52, 8, 40)
	} else {
		for y := 100; y < 214; y += 26 {
			rect(76, y, 100, 5, 90)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
func (a *App) resourceCover(w http.ResponseWriter, r *http.Request, prefix string) {
	start := time.Now()
	e := eventFor(r, nil)
	writer := &otaResponseWriter{ResponseWriter: w, event: &e}
	defer func() {
		writer.finish(r)
		e.ResponseBody = ""
		e.Bytes = writer.written
		e.DurationMS = time.Since(start).Milliseconds()
		a.record(e)
	}()
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(writer, "method not allowed", 405)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"/"), "/cover.png")
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
		http.NotFound(writer, r)
		return
	}
	var data []byte
	if item.CustomCover {
		var err error
		data, err = os.ReadFile(a.resourcePath(id) + ".cover.png")
		if err != nil {
			http.Error(writer, "cover unavailable", 500)
			return
		}
	}
	if !item.CustomCover {
		data = defaultResourceCover(item.Type)
	}
	e.Note = "资源封面：" + item.Name
	writer.Header().Set("Content-Type", "image/png")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Length", fmt.Sprint(len(data)))
	writer.WriteHeader(200)
	if r.Method == "GET" {
		_, _ = writer.Write(data)
	}
}
func (a *App) updateResourceCover(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, fmt.Errorf("method not allowed"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		fail(w, 400, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	data, err := readResourceCover(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/resources/"), "/cover")
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, v := range a.config.Resources {
		if v.ID != id {
			continue
		}
		path := a.resourcePath(v.ID) + ".cover.png"
		old, _ := os.ReadFile(path)
		if data != nil {
			if err = os.WriteFile(path+".tmp", data, 0600); err == nil {
				err = os.Rename(path+".tmp", path)
			}
			if err != nil {
				os.Remove(path + ".tmp")
				fail(w, 500, err)
				return
			}
		}
		a.config.Resources[i].CustomCover = data != nil
		if err = a.saveLocked(); err != nil {
			a.config.Resources[i] = v
			if old != nil {
				_ = os.WriteFile(path, old, 0600)
			} else {
				_ = os.Remove(path)
			}
			fail(w, 500, err)
			return
		}
		if data == nil {
			_ = os.Remove(path)
		}
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	fail(w, 404, fmt.Errorf("资源不存在"))
}

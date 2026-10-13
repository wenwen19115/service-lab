package lab

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Management exports always return the original file, independent of OTA test scenarios.
func (a *App) exportFirmware(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/firmware/"), "/file")
	a.mu.RLock()
	var selected *Firmware
	for _, f := range a.config.Firmware {
		if f.ID == id {
			copy := f
			selected = &copy
			break
		}
	}
	// Open under the read lock so a concurrent move to the trash cannot race this lookup.
	var file *os.File
	var err error
	if selected != nil {
		file, err = os.Open(firmwarePath(a.dir, id))
	}
	a.mu.RUnlock()
	if selected == nil {
		fail(w, 404, fmt.Errorf("固件不存在"))
		return
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fail(w, 500, err)
		return
	}
	name := strings.ReplaceAll(strings.ReplaceAll(selected.Name, "\"", ""), "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, selected.Name, info.ModTime(), file)
}

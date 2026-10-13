package lab

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) eventDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/api/events/"), 10, 64)
	if err != nil {
		fail(w, 400, fmt.Errorf("请求 ID 无效"))
		return
	}
	a.mu.RLock()
	var selected *Event
	for _, event := range a.events {
		if event.ID == id {
			copy := event
			selected = &copy
			break
		}
	}
	a.mu.RUnlock()
	if selected == nil {
		fail(w, 404, fmt.Errorf("记录不存在或已超出保留范围"))
		return
	}
	respond(w, 200, selected)
}

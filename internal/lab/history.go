package lab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (a *App) startHistory() error {
	a.historySignal = make(chan struct{}, 1)
	a.historyStop = make(chan struct{})
	a.startedAt = time.Now()
	data, err := os.ReadFile(filepath.Join(a.dir, "events.json"))
	if err == nil {
		if err = json.Unmarshal(data, &a.events); err != nil {
			return fmt.Errorf("invalid events.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if len(a.events) > 300 {
		a.events = a.events[len(a.events)-300:]
	}
	for i := range a.events {
		e := &a.events[i]
		for {
			current := requestSequence.Load()
			if current >= e.ID || requestSequence.CompareAndSwap(current, e.ID) {
				break
			}
		}
		if e.Phase == "started" || e.Phase == "downloading" {
			e.Phase = "interrupted"
			e.Note = "服务重启，未记录到传输结束"
			e.Steps = append(e.Steps, TransferStep{Time: time.Now(), Bytes: e.Bytes, Phase: e.Phase, Message: e.Note})
		}
	}
	a.historyWG.Add(1)
	go a.historyLoop()
	return nil
}
func (a *App) signalHistory() {
	select {
	case a.historySignal <- struct{}{}:
	default:
	}
}
func (a *App) historyLoop() {
	defer a.historyWG.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	dirty := false
	for {
		select {
		case <-a.historySignal:
			dirty = true
		case <-ticker.C:
			if dirty {
				a.flushHistory()
				dirty = false
			}
		case <-a.historyStop:
			a.flushHistory()
			return
		}
	}
}
func (a *App) flushHistory() {
	a.mu.RLock()
	events := append([]Event{}, a.events...)
	a.mu.RUnlock()
	data, err := json.Marshal(events)
	if err == nil {
		file := filepath.Join(a.dir, "events.json")
		err = os.WriteFile(file+".tmp", data, 0600)
		if err == nil {
			err = os.Rename(file+".tmp", file)
		}
	}
	a.mu.Lock()
	if err != nil {
		a.historyError = err.Error()
		a.signalHistory()
	} else {
		a.historyError = ""
		a.historySavedAt = time.Now()
	}
	a.mu.Unlock()
}
func (a *App) Close() { a.historyOnce.Do(func() { close(a.historyStop); a.historyWG.Wait() }) }

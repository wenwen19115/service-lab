package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"service-lab/internal/lab"
)

//go:embed all:web/dist
var assets embed.FS

func main() {
	addr := flag.String("addr", "0.0.0.0:8080", "HTTP listen address")
	data := flag.String("data", defaultDataDir(), "configuration and firmware directory")
	open := flag.Bool("open", true, "open the browser on startup")
	flag.Parse()
	if err := run(*addr, *data, *open); err != nil {
		log.Print(err)
		showStartupError(err.Error())
		os.Exit(1)
	}
}

func defaultDataDir() string {
	executable, err := os.Executable()
	if err != nil {
		return "data"
	}
	dir := filepath.Dir(executable)
	if strings.EqualFold(filepath.Base(dir), "dist") {
		dir = filepath.Dir(dir)
	}
	return filepath.Join(dir, "data")
}

func browserURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid listen address: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func runningInstance(url string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url + "/api/health")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return false
	}
	var health struct {
		Service string `json:"service"`
	}
	return json.NewDecoder(response.Body).Decode(&health) == nil && health.Service == "service-lab"
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

var errRestart = errors.New("service restart requested")

func run(addr, data string, open bool) error {
	for {
		err := runOnce(addr, data, open)
		if !errors.Is(err, errRestart) {
			return err
		}
		open = false
	}
}

func runOnce(addr, data string, open bool) error {
	url, err := browserURL(addr)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if runningInstance(url) {
			if open {
				return openBrowser(url)
			}
			return nil
		}
		return fmt.Errorf("无法启动服务，监听地址 %s 不可用。请检查端口是否被其他程序占用。\n\n%s", addr, err)
	}
	defer listener.Close()
	frontend, err := fs.Sub(assets, "web/dist")
	if err != nil {
		return err
	}
	app, err := lab.New(data, frontend)
	if err != nil {
		return fmt.Errorf("无法打开数据目录 %s：%w", data, err)
	}
	defer app.Close()
	app.ListenAddr = addr
	logFile, err := os.OpenFile(filepath.Join(data, "service-lab.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	server := &http.Server{Addr: addr, Handler: app.DiagnosticHandler(), ConnState: lab.DiagnosticConnState, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	restart := make(chan struct{}, 1)
	var restartRequested atomic.Bool
	app.RequestRestart = func() bool {
		if !restartRequested.CompareAndSwap(false, true) {
			return false
		}
		restart <- struct{}{}
		return true
	}

	shutdownDone := make(chan struct{})
	restarting := false
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
		case <-restart:
			restarting = true
			time.Sleep(200 * time.Millisecond)
		}
		timeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(timeout); err != nil {
			_ = server.Close()
		}
	}()
	log.Printf("Service Lab: %s; Data: %s", url, data)
	fmt.Printf("Service Lab: %s\nData: %s\n", url, data)
	for _, address := range lab.LocalAddresses(addr) {
		fmt.Println("Service:", address)
	}
	// Start accepting requests before opening the browser.
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if open {
		if err := openBrowser(url); err != nil {
			log.Printf("Cannot open browser: %v", err)
			showStartupError("服务已启动，但无法自动打开浏览器。请手动访问：" + url)
		}
	}
	err = <-done
	if err == http.ErrServerClosed {
		<-shutdownDone
		if restarting {
			return errRestart
		}
		return nil
	}
	return err
}

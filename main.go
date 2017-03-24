package main


import (
	//"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	//"os/exec"
	"os/signal"
	"path/filepath"
	//"strconv"
	//"strings"
	//"sync"
	"syscall"
	//"time"
)

func setupSocket(socketPath string) (net.Listener, error) {
	os.RemoveAll(filepath.Dir(socketPath))
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %v", filepath.Dir(socketPath), err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %q: %v", socketPath, err)
	}

	log.Printf("Listening on: unix://%s", socketPath)
	return listener, nil
}

func setupSignals(socketPath string) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupt
		os.RemoveAll(filepath.Dir(socketPath))
		os.Exit(0)
	}()
}


func main() {
	const socketPath = "/var/run/scope/plugins/my-plugin/my-plugin.sock"
	setupSignals(socketPath)

	listener, err := setupSocket(socketPath)

	plugin := &Plugin{}
	http.HandleFunc("/report", plugin.Report)

	defer func() {
		listener.Close()
		os.RemoveAll(filepath.Dir(socketPath))
	}()
}
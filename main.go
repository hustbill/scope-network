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


// TrafficControlStatus keeps track of parameters status
type TrafficControlStatus struct {
	latency    string
	packetLoss string
}

// String is useful to easily create a string of the traffic control plugin internal status.
// Useful for debugging
func (tcs *TrafficControlStatus) String() string {
	return fmt.Sprintf("%s %s", tcs.latency, tcs.packetLoss)
}

// SetLatency sets the latency value
// the convention is that empty latency is represented by '-'
func (tcs *TrafficControlStatus) SetLatency(latency string) {
	if latency == "" {
		tcs.latency = "-"
	}
	tcs.latency = latency
}

// SetPacketLoss sets the packet loss value
// the convention is that empty packet loss is represented by '-'
func (tcs *TrafficControlStatus) SetPacketLoss(packetLoss string) {
	if packetLoss == "" {
		tcs.packetLoss = "-"
	}
	tcs.packetLoss = packetLoss
}


// TrafficControlStatusInit initializes with the convention that empty values are '-'
func TrafficControlStatusInit() *TrafficControlStatus {
	return &TrafficControlStatus{
		latency:    "-",
		packetLoss: "-",
	}
}

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

// TrafficControlStatusCache implements status caching
var trafficControlStatusCache map[string]*TrafficControlStatus

func main() {
	const socketPath = "/var/run/scope/plugins/my-plugin/my-plugin.sock"
	setupSignals(socketPath)

	listener, err := setupSocket(socketPath)
	if err != nil {
		log.Fatalf("Failed to setup socket: %v", err)
	}


	plugin := &Plugin{}
	http.HandleFunc("/report", plugin.Report)

	defer func() {
		listener.Close()
		os.RemoveAll(filepath.Dir(socketPath))
	}()
}
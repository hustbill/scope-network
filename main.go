package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	log "github.com/Sirupsen/logrus"
)

// TODO:
//
// do not try to install the qdics on the network interface ever
// time, skip this step if it is already installed (currently we do
// "replace" instead of "add", but this check may be a way of avoiding
// more one-time installation steps in future).
//
// somehow inform the user about the current traffic control state
// (either add some metadata about latency or maybe add background
// color to buttons jhortcut reports as a part of a response to the
// control request
//
// detect if ip and tc binaries are in $PATH
//
// detect if required sch_netem kernel module is loaded; note that in
// some (rare) cases this might be compiled in the kernel instead of
// being a separate module; probably check if tc works, if it does not
// return something like "not implemented".
//
// add traffic control on ingress traffic too (ifb kernel module will
// be required)
//
// currently we can control latency, add controls for packet loss and
// bandwidth
//
// port to eBPF?

type containerClient interface {
	Start()
}

// Plugin is the internal data structure
type Plugin struct {
	reporter *Reporter

	clients []containerClient
}

// TrafficControlStatus keeps track of parameters status
type TrafficControlStatus struct {
	pod	   string
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
		pod:	"-",
		latency:    "-",
		packetLoss: "-",
	}
}

// TrafficControlStatusCache implements status caching
var trafficControlStatusCache map[string]*TrafficControlStatus

func main() {
	// We put the socket in a sub-directory to have more control on the permissions
	const socketPath = "/var/run/scope/plugins/network-control/network-control.sock"

	// Handle the exit signal
	setupSignals(socketPath)

	listener, err := setupSocket(socketPath)
	if err != nil {
		log.Fatalf("Failed to setup socket: %v", err)
	}

	plugin, err := NewPlugin()
	if err != nil {
		log.Fatalf("Failed to create a plugin: %v", err)
	}

	// Cache
	trafficControlStatusCache = make(map[string]*TrafficControlStatus)

	trafficControlServeMux := http.NewServeMux()

	//// Report request handler
	reportHandler := http.HandlerFunc(plugin.report)
	trafficControlServeMux.Handle("/report", reportHandler)
	//
	//// Control request handler
	controlHandler := http.HandlerFunc(plugin.control)
	trafficControlServeMux.Handle("/control", controlHandler)

	log.Println("Listening...")
	if err = http.Serve(listener, trafficControlServeMux); err != nil {
		fmt.Errorf("failed to serve: %v", err)
		log.Fatalf("failed to serve: %v", err)
	}
}

func setupSignals(socketPath string) {
	log.Debugf("enter setupSignals for socketPath %s", socketPath)  // billzhang 2017-04-04
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupt
		os.RemoveAll(filepath.Dir(socketPath))
		os.Exit(0)
	}()
}

func NewPlugin() (*Plugin, error) {
	store := NewStore()
	dockerClient, err := NewDockerClient(store)
	if err != nil {
		return nil, fmt.Errorf("failed to create a docker client: %v", err)
	}
	reporter := NewReporter(store)
	plugin := &Plugin{
		reporter: reporter,
		clients: []containerClient{
			dockerClient,
		},
	}
	for _, client := range plugin.clients {
		go client.Start()
	}
	return plugin, nil
}

// NewPlugin instantiates a new plugin
func setupSocket(socketPath string) (net.Listener, error) {
	log.Debugf("enter setupSocket for socketPath %s", socketPath)  // billzhang 2017-04-04
	os.RemoveAll(filepath.Dir(socketPath))
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %v", filepath.Dir(socketPath), err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %q: %v", socketPath, err)
	}

	log.Debugf("Listening on: unix://%s", socketPath)
	return listener, nil
}

func (p *Plugin) report(w http.ResponseWriter, r *http.Request) {
	log.Printf("enter report")  // billzhang 2017-04-04

	raw, err := p.reporter.RawReport()
	if err != nil {
		msg := fmt.Sprintf("error: failed to get raw report: %v", err)
		log.Print(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

type request struct {
	NodeID  string
	Control string
}

type response struct {
	Error string `json:"error,omitempty"`
}

func (p *Plugin) control(w http.ResponseWriter, r *http.Request) {
	xreq := request{}
	if err := json.NewDecoder(r.Body).Decode(&xreq); err != nil {
		log.Debugf("Bad request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	handler, err := p.reporter.GetHandler(xreq.NodeID, xreq.Control)
	if err != nil {
		sendResponse(w, fmt.Errorf("failed to get handler: %v", err))
		return
	}
	if err := handler(); err != nil {
		sendResponse(w, fmt.Errorf("handler failed: %v", err))
		return
	}
	sendResponse(w, nil)
}

func sendResponse(w http.ResponseWriter, err error) {
	res := response{}
	if err != nil {
		res.Error = err.Error()
	}
	raw, err := json.Marshal(res)
	if err != nil {
		log.Debugf("Internal server error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

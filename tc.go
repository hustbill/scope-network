package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	log "github.com/Sirupsen/logrus"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/influxdata/influxdb/client"
	"net/http"
	"net/http/httptest"
	"net/url"
)

const db = "ican"
var command = "select * from p2prxbyte"

// applyTrafficControlRules set the network policies
func applyTrafficControlRules(pid int, rules []string) (netNSID string, err error) {
	log.Printf("enter applyTrafficControlRules for pid %d", pid)  // billzhang 2017-04-04
	cmds := [][]string{
		strings.Fields("tc qdisc replace dev eth0 root handle 1: netem"),
	}
	cmd := strings.Fields("tc qdisc change dev eth0 root handle 1: netem")
	cmd = append(cmd, rules...)
	cmds = append(cmds, cmd)

	netNS := fmt.Sprintf("/proc/%d/ns/net", pid)
	log.Printf("/proc/%d/ns/net", pid)  // billzhang 2017-04-04

	err = ns.WithNetNSPath(netNS, func(hostNS ns.NetNS) error {
		for _, cmd := range cmds {
			if output, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
				log.Error(string(output))
				return fmt.Errorf("failed to execute command: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to perform traffic control: %v", err)
	}
	log.Printf("call getNSID for netNS %s", netNS)  // billzhang 2017-04-04
	netNSID, err = getNSID(netNS)

	if err != nil {
		return "", err
	}
	return netNSID, nil
}

// ApplyLatency sets the latency
func ApplyLatency(pid int, latency string) error {
	if latency == "" {
		return nil
	}
	rules := strings.Fields(fmt.Sprintf("delay %s", latency))

	// Get cached packet loss
	packetLoss, err := getPacketLoss(pid)
	if err != nil {
		return err
	}
	if packetLoss != "-" {
		rules = append(rules, strings.Fields(fmt.Sprintf("loss %s", packetLoss))...)
	}

	netNSID, err := applyTrafficControlRules(pid, rules)

	// Update cached values
	if trafficControlStatusCache[netNSID] == nil {
		trafficControlStatusCache[netNSID] = TrafficControlStatusInit()
	}
	trafficControlStatusCache[netNSID].SetLatency(latency)
	trafficControlStatusCache[netNSID].SetPacketLoss(packetLoss)

	return nil
}

// ApplyPacketLoss sets the packet loss
func ApplyPacketLoss(pid int, packetLoss string) error {
	if packetLoss == "" {
		return nil
	}
	rules := strings.Fields(fmt.Sprintf("loss %s", packetLoss))

	// Get cached latency
	latency, err := getLatency(pid)
	if err != nil {
		return err
	}
	if latency != "-" {
		rules = append(rules, strings.Fields(fmt.Sprintf("delay %s", latency))...)
	}

	netNSID, err := applyTrafficControlRules(pid, rules)

	// Update cached values
	if trafficControlStatusCache[netNSID] == nil {
		trafficControlStatusCache[netNSID] = TrafficControlStatusInit()
	}
	trafficControlStatusCache[netNSID].SetLatency(latency)
	trafficControlStatusCache[netNSID].SetPacketLoss(packetLoss)

	return nil
}

// ClearTrafficControlSettings clear all parameters of the qdisc with tc
func ClearTrafficControlSettings(pid int) error {
	log.Printf("enter ClearTrafficControlSettings for pid %d", pid)  // billzhang 2017-04-04
	cmds := [][]string{
		strings.Fields("tc qdisc replace dev eth0 root handle 1: netem"),
	}
	netNS := fmt.Sprintf("/proc/%d/ns/net", pid)
	err := ns.WithNetNSPath(netNS, func(hostNS ns.NetNS) error {
		for _, cmd := range cmds {
			if output, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
				log.Error(string(output))
				return fmt.Errorf("failed to execute command: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to perform traffic control: %v", err)
	}
	// clear cached parameters
	log.Printf("call getNSID for netNS %s", netNS)  // billzhang 2017-04-04
	netNSID, err := getNSID(netNS)
	if err != nil {
		log.Error(netNSID)
		return fmt.Errorf("failed to get network namespace ID: %v", err)
	}
	delete(trafficControlStatusCache, netNSID)
	return nil
}

func getLatency(pid int) (string, error) {
	log.Printf("enter getLatency for PID %d", pid)  // billzhang 2017-04-04
	var status *TrafficControlStatus
	var err error
	if status, err = getStatus(pid); err != nil {
		return "-", err
	} else if status == nil {
		return "-", fmt.Errorf("status for PID %d does not exist", pid)
	}
	return status.latency, nil
}

func getPacketLoss(pid int) (string, error) {
	log.Printf("enter getPacketLoss for PID %d", pid)  // billzhang 2017-04-04
	var status *TrafficControlStatus
	var err error
	if status, err = getStatus(pid); err != nil {
		return "-", err
	} else if status == nil {
		return "-", fmt.Errorf("status for PID %d does not exist", pid)
	}
	return status.packetLoss, nil
}

func queryInfluxDB(db string, command string) {
	log.Info("queryInfluxDB")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data client.Response
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)

	u.Host = "localhost:8086"  // set to InfluxDB default port : 8086

	config := client.Config{URL: *u}
	c, err := client.NewClient(config)
	if err != nil {
		log.Fatalf("unexpected error.  expected %v, actual %v", nil, err)
	}

	query := client.Query{}
	query.Database = db
	query.Command = command


	res, err := c.Query(query)
	log.Info("response: ", res)

	if err != nil {
		log.Fatalf("unexpected error.  expected %v, actual %v", nil, err)
	}
}


func getStatus(pid int) (*TrafficControlStatus, error) {

	netNS := fmt.Sprintf("/proc/%d/ns/net", pid)

	netNSID, err := getNSID(netNS)
	log.Printf("getStatus for PID %d, netNSID %s", pid, netNSID)  // billzhang 2017-04-04
	if err != nil {
		log.Error(netNSID)
		return nil, fmt.Errorf("failed to get network namespace ID: %v", err)
	}
	if status, ok := trafficControlStatusCache[netNSID]; ok {
		return status, nil
	}


	//cmd := strings.Fields("tc qdisc show dev eth0")
	cmd := strings.Fields("tc qdisc show dev enp2s0f1")  // billzhang 2017-04-04
	var output string

	queryInfluxDB(db, command)

	err = ns.WithNetNSPath(netNS, func(hostNS ns.NetNS) error {
		cmdOut, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			log.Error(string(cmdOut))
			output = ""
			//return fmt.Errorf("failed to execute command: tc qdisc show dev eth0: %v", err)
			return fmt.Errorf("failed to execute command: tc qdisc show dev enp2s0f1: %v", err)  // billzhang 2017-04-04
		}
		output = string(cmdOut)
		log.Printf("start exec command, output = %s", output)  // billzhang 2017-04-09
		return nil
	})

	// cache parameters
	trafficControlStatusCache[netNSID] = &TrafficControlStatus{
		latency:    parseLatency(output),
		packetLoss: parsePacketLoss(output),
	}
	status, _ := trafficControlStatusCache[netNSID]

	status.latency = "5000"
	status.packetLoss = "1"
	return status, err
}

func parseLatency(statusString string) string {
	return parseAttribute(statusString, "delay")
}

func parsePacketLoss(statusString string) string {
	return parseAttribute(statusString, "loss")
}
func parseAttribute(statusString string, attribute string) string {
	log.Debugf("enter parseAttribute for statusString %s", statusString)  // billzhang 2017-04-04
	statusStringSplited := strings.Fields(statusString)
	for i, s := range statusStringSplited {
		if s == attribute {
			if i < len(statusStringSplited)-1 {
				return strings.Trim(statusStringSplited[i+1], "\n")
			}
			return "-"
		}
	}
	return "-"
}

func getNSID(nsPath string) (string, error) {
	log.Debugf("enter getNSID for nsPath %s", nsPath)  // billzhang 2017-04-04
	nsID, err := os.Readlink(nsPath)
	if err != nil {
		log.Errorf("failed read \"%s\": %v", nsPath, err)
		return "", fmt.Errorf("failed read \"%s\": %v", nsPath, err)
	}
	log.Debugf("exit getNSID for nsID[5 : len(nsID)-1] %s", nsID[5 : len(nsID)-1])  // billzhang 2017-04-04
	return nsID[5 : len(nsID)-1], nil
}

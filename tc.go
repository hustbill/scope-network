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
	"strconv"
)

const db = "ican"

// resp is a container for the raw InfluxDB responses.
//type resp struct {
//	Results []struct {
//		Series []struct {
//			Columns []string        `json:"columns"`
//			Values  [][]interface{} `json:"values"`
//		} `json:"series"`
//	} `json:"results"`
//}


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

func getPod(pid int) (string, error) {
	log.Printf("enter getPod for PID %d", pid)  // billzhang 2017-04-04
	var status *TrafficControlStatus
	var err error
	if status, err = getStatus(pid); err != nil {
		return "-", err
	} else if status == nil {
		return "-", fmt.Errorf("status for PID %d does not exist", pid)
	}
	return status.pod, nil
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

func queryInfluxDB(db string, command string) (*client.Response, error) {
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

	if err != nil {
		log.Fatalf("unexpected error.  expected %v, actual %v", nil, err)
	}
	return res, nil;
}


func getStatus(pid int) (*TrafficControlStatus, error) {
	var cmd_bandwidth = "select * from p2prxbyte"
	var cmd_packet = "select * from p2prxpkt"

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

	// query bandwidth
	output_bandWidth, err := queryInfluxDB(db, cmd_bandwidth)

	// query packet
	// cmd_packet
	output_packet, err := queryInfluxDB(db, cmd_packet)

	if err != nil {
		log.Error(netNSID)
		return nil, fmt.Errorf("failed to get data from InfluxDB: %v", err)
	}

	// cache parameters
	trafficControlStatusCache[netNSID] = &TrafficControlStatus{
		pod:        parsePod(output_bandWidth),
		latency:    parseBandwidth(output_bandWidth),
		packetLoss: parsePacket(output_packet),
	}
	status, _ := trafficControlStatusCache[netNSID]

	return status, err
}

func parsePod(res *client.Response) string {
	return parseAttribute(res, "pod")
}

func parseBandwidth(res *client.Response) string {
	return parseAttribute(res, "bandwidth")
}

func parsePacket(res *client.Response) string {
	return parseAttribute(res, "packet")
}


func parseAttribute(resp *client.Response, attribute string) (string) {
	log.Debugf("enter parseAttribute for statusString")
	log.Info(resp)  // billzhang 2017-04-04

	//res, err := resp.Results[0].Series[0].Values[0][1].(json.Number).Float64()

	var myData [][]interface{} = make([][]interface{}, len(resp.Results[0].Series[0].Values))
	for i, d := range resp.Results[0].Series[0].Values {
		myData[i] = d
	}

	// row : p2prxpkt,
	// host=10.145.240.148,
	// spod_name=user-468431046-6wr62,
	// spod_namespace=user-468431046-6wr62,
	// dpod_name=weave-net-8sww0,
	// dpod_namespace=kube-system,
	// src=10.32.0.14,
	// dst=10.145.240.148
	// value=41

	log.Printf("Result :")
	fmt.Println("", myData[0]) //first element in slice
	log.Println("---------------------------")
	fmt.Println("time: ", myData[0][0])
	fmt.Println("dpod_name: ", myData[0][1])
	fmt.Println("dpod_namespace: ", myData[0][2])
	fmt.Println("dst: ", myData[0][3])
	fmt.Println("host: ", myData[0][4])
	fmt.Println("spod_name: ", myData[0][5])
	fmt.Println("spod_namespace: ", myData[0][6])
	fmt.Println("src: ", myData[0][7])
	fmt.Println("value: ", myData[0][8])

	if (attribute == "pod") {
		pod, err := resp.Results[0].Series[0].Values[0][1].(string)
		if err == false {
			return "-"
		}
		log.Printf("spod_name : %s ", pod)
		return pod
	}


	val, err := resp.Results[0].Series[0].Values[0][8].(json.Number).Int64()
	str := strconv.FormatInt(val, 10)

	if err != nil {
		log.Errorf("failed read data from InfluxDB: %v", err)
		return "-"
	}

	return str

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

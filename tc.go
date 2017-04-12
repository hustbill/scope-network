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
	dockerClient "github.com/fsouza/go-dockerclient"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"

)

const db = "ican"

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

func getPod(containerID string) (string, error) {

	dc, err := dockerClient.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		fmt.Errorf("failed to create a docker client: %v", err)
	}

	container, err:= dc.InspectContainer(containerID);
	if err != nil {
		fmt.Errorf("failed to inspect container with context: %v", err)
	}

	log.Info(container.Name)

	pod, err := parsePodName(container.Name); // user-468431046-ktrt4
	if err != nil {
		fmt.Errorf("failed to ParseDockerName: %v", err)
	}

	log.Printf("pod is %s", pod)
	return pod, nil
}

// Unpacks a container name, returning the pod full name.
// If we are unable to parse the name, an error is returned.
// https://github.com/kubernetes/kubernetes/blob/cda109d22480bc6dea3c06cef21bd4c4fca6fca2/pkg/kubelet/dockertools/docker.go
func parsePodName(name string) (podName string, err error) {
	// For some reason docker appears to be appending '/' to names.
	// If it's there, strip it.
	var containerNamePrefix = "k8s"
	name = strings.TrimPrefix(name, "/")
	parts := strings.Split(name, "_")
	if len(parts) == 0 || parts[0] != containerNamePrefix {
		err = fmt.Errorf("failed to parse Docker container name %q into parts", name)
		return "", err
	}
	if len(parts) < 6 {
		// We have at least 5 fields.  We may have more in the future.
		// Anything with less fields than this is not something we can
		// manage.
		log.Warningf("found a container with the %q prefix, but too few fields (%d): %q", containerNamePrefix, len(parts), name)
		err = fmt.Errorf("Docker container name %q has less parts than expected %v", name, parts)
		return "", err
	}

	nameParts := strings.Split(parts[1], ".")
	containerName := nameParts[0]
	log.Printf(containerName)
	if len(nameParts) > 1 {
		if err != nil {
			log.Warningf("invalid container hash %q in container %q", nameParts[1], name)
		}
	}

	podFullName := parts[2]

	return podFullName, nil
}

func getLatencyByPod(pod string) (string, error) {
	log.Printf("enter getLatency for pod %d", pod)  // billzhang 2017-04-04
	var status *TrafficControlStatus
	var err error
	if status, err = getStatusByPod(pod); err != nil {
		return "-", err
	} else if status == nil {
		return "-", fmt.Errorf("status for pod %d does not exist", pod)
	}
	return status.latency, nil
}

func getPacketLossByPod(pod string) (string, error) {
	log.Printf("enter getPacketLoss for pod %d", pod)  // billzhang 2017-04-04
	var status *TrafficControlStatus
	var err error
	if status, err = getStatusByPod(pod); err != nil {
		return "-", err
	} else if status == nil {
		return "-", fmt.Errorf("status for pod %d does not exist", pod)
	}
	return status.packetLoss, nil
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
	log.Println(u.Host)

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
	var cmd = "select * from p2prxbyte, p2prxpkt"


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
	res, err := queryInfluxDB(db, cmd)

	result := parseAttribute(res)


	if err != nil {
		log.Error(netNSID)
		return nil, fmt.Errorf("failed to get data from InfluxDB: %v", err)
	}

	// cache parameters
	trafficControlStatusCache[netNSID] = &TrafficControlStatus{
		pod:        result[0], // output_pod
		latency:    result[1], // output_bandWidth
		packetLoss: result[2],  // output_packet
	}
	status, _ := trafficControlStatusCache[netNSID]

	return status, err
}


func getStatusByPod(podName string) (*TrafficControlStatus, error) {
	var cmd = "select * from p2prxbyte, p2prxpkt"


	log.Printf("getStatus for pod: %s", podName)  // billzhang 2017-04-04
	if status, ok := trafficControlStatusCache[podName]; ok {
		return status, nil
	}

	cmd  = cmd + " where dpod = '" +  podName + "'"
	log.Printf("query command : %s ", cmd)

	// query bandwidth
	res, err := queryInfluxDB(db, cmd)

	result := parseAttribute(res)


	if err != nil {
		log.Error(err)
		return nil, fmt.Errorf("failed to get data from InfluxDB: %v", err)
	}

	// cache parameters
	trafficControlStatusCache[podName] = &TrafficControlStatus{
		pod:        podName, // output_pod
		latency:    result[1], // output_bandWidth
		packetLoss: result[2],  // output_packet
	}
	status, _ := trafficControlStatusCache[podName]

	return status, err
}

/*
 row : p2prxpkt,
 host=10.145.240.148,
 spod_name=user-468431046-6wr62,
 spod_namespace=user-468431046-6wr62,
 dpod_name=weave-net-8sww0,
 dpod_namespace=kube-system,
 src=10.32.0.14,
 dst=10.145.240.148
 value=41
 */
func parseAttribute(resp *client.Response) ([]string) {
	log.Debugf("enter parseAttribute for dpod, bandwidth and packet")
	log.Info(resp)  // billzhang 2017-04-04
	result := []string{"-", "-", "-"}
	//fmt.Println("", myData[0]) //first element in slice
	//log.Println("---------------------------")
	//fmt.Println("time: ", myData[0][0])
	//fmt.Println("dpod_name: ", myData[0][1])
	//fmt.Println("dpod_namespace: ", myData[0][2])
	//fmt.Println("dst: ", myData[0][3])
	//fmt.Println("host: ", myData[0][4])
	//fmt.Println("spod_name: ", myData[0][5])
	//fmt.Println("spod_namespace: ", myData[0][6])
	//fmt.Println("src: ", myData[0][7])
	//fmt.Println("value: ", myData[0][8])


	if (len(resp.Results) >= 1) {
		log.Printf("Inside, len(resp.Results) = %d ", len(resp.Results))
		log.Info(resp.Results[0])

		if (len(resp.Results[0].Series) > 1) {
			pod, ok := resp.Results[0].Series[0].Values[0][1].(string)

			if (ok) {
				log.Printf("spod_name : %s ", pod)
				result[0] = pod
			} else {
				log.Printf("failed read data from InfluxDB, status: ", ok)
			}

			bandwidth, err := resp.Results[0].Series[0].Values[0][8].(json.Number).Int64()
			packet, err := resp.Results[0].Series[1].Values[0][8].(json.Number).Int64()
			if err != nil {

				log.Errorf("failed read data from InfluxDB: %v", err)
			}
			result[1] = strconv.FormatInt(bandwidth, 10)
			result[2] = strconv.FormatInt(packet, 10)
		}

	} else {
		log.Printf("len(resp.Results) = %d ", len(resp.Results))
	}



	return result

}

/*
top
  PID USER      PR  NI    VIRT    RES    SHR S  %CPU %MEM     TIME+ COMMAND
 5679 10001     20   0   15868   9808   7264 S   0.3  0.1   0:02.89 user

 Kubernetes Pod
 NAMESPACE     NAME                                      READY     STATUS    RESTARTS   AGE       IP               NODE
 default       user-468431046-ktrt4                      1/1       Running   3          4d        10.32.0.25  hua-system-76

 */
func getNSID(nsPath string) (string, error) {
	log.Printf("enter getNSID for nsPath %s", nsPath)  // billzhang 2017-04-04
	nsID, err := os.Readlink(nsPath)
	log.Printf("nsID %s", nsID)  // billzhang 2017-04-04
	if err != nil {
		log.Errorf("failed read \"%s\": %v", nsPath, err)
		return "", fmt.Errorf("failed read \"%s\": %v", nsPath, err)
	}
	log.Debugf("exit getNSID for nsID[5 : len(nsID)-1] %s", nsID[5 : len(nsID)-1])  // billzhang 2017-04-04
	return nsID[5 : len(nsID)-1], nil
}

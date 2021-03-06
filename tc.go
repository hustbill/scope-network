package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"math/rand"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/influxdata/influxdb/client"
	dockerClient "github.com/fsouza/go-dockerclient"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"

)

func random(min, max int) int {
	rand.Seed(time.Now().Unix())
	return rand.Intn(max - min) + min
}

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
	// latency, err := getLatency(pid)
	// latency, err := getLatency(pid)
	latency := strconv.Itoa(random(700, 34000));

	rules = append(rules, strings.Fields(fmt.Sprintf("delay %s", latency))...)

	netNSID, _ := applyTrafficControlRules(pid, rules)

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
	log.Debugf("get Pod name by containerID using fsouza/go-dockerclient  %s",containerID)  // billzhang 2017-04-12

	dc, err := dockerClient.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		fmt.Errorf("failed to create a docker client: %v", err)
	}

	container, err:= dc.InspectContainer(containerID);
	if err != nil {
		fmt.Errorf("failed to inspect container with context: %v", err)
	}
	log.Debugf("container.Name : ", container.Name)

	pod, err := parsePodName(container.Name); // user-468431046-ktrt4
	if err != nil {
		fmt.Errorf("failed to ParseDockerName: %v", err)
	}

	log.Debugf("pod is %s", pod)
	return pod, nil
}

// Unpacks a container name, returning the pod full name.
// If we are unable to parse the name, an error is returned.
// https://github.com/kubernetes/kubernetes/blob/cda109d22480bc6dea3c06cef21bd4c4fca6fca2/pkg/kubelet/dockertools/docker.go
func parsePodName(name string) (podName string, err error) {
	log.Debugf("parse Pod full name from %s", name)  // billzhang 2017-04-12
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

/* func getLatencyByPod(pod string) (string, error) {
	log.Debugf("enter getLatency for pod %s", pod)  // billzhang 2017-04-04
	var status *NetworkControlStatus
	var err error
	if status, err = getStatusByPod(pod); err != nil {
		return "-", err
	} else if status == nil {
		return "-", fmt.Errorf("status for pod %d does not exist", pod)
	}
	return status.latency, nil
} */
/*
func getPacketByPod(pod string) (string, error) {
	log.Debugf("enter getPacketLoss for pod %s", pod)  // billzhang 2017-04-04
	var status *NetworkControlStatus
	var err error
	if status, err = getStatusByPod(pod); err != nil {
		return "-", err
	} else if status == nil {
		return "-", fmt.Errorf("status for pod %d does not exist", pod)
	}
	return status.packet, nil
}*/



func getPacketLoss(pid int) (string, error) {
	log.Debugf("enter getPacketLoss for PID %d", pid)  // billzhang 2017-04-04
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
	log.Printf("queryInfluxDB")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data client.Response
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	log.Debugf(u.Host)

	u.Host = "localhost:8086"  // set to InfluxDB default port : 8086

	config := client.Config{URL: *u}
	c, err := client.NewClient(config)
	if err != nil {
		log.Fatalf("unexpected error for client.NewClient.  expected %v, actual %v", nil, err)
		return nil, err
	}

	query := client.Query{}
	query.Database = db
	query.Command = command


	res, err := c.Query(query)

	if err != nil {
		log.Info(res)
		log.Printf("unexpected error.  expected %v, actual %v", nil, err)
		return nil, err
	}
	return res, nil;
}

func getStatus(pid int) (*TrafficControlStatus, error) {
	// var cmd = "select * from p2prxbyte, p2prxpkt"
	var cmd = "select * from p2ptxbyte, p2ptxpkt";
	netNS := fmt.Sprintf("/proc/%d/ns/net", pid)

	netNSID, err := getNSID(netNS)
	log.Debugf("getStatus for PID %d, netNSID %s", pid, netNSID)  // billzhang 2017-04-04
	if err != nil {
		log.Error(netNSID)
		return nil, fmt.Errorf("failed to get network namespace ID: %v", err)
	}
	if status, ok := trafficControlStatusCache[netNSID]; ok {
		return status, nil
	}

	// query dpod_name, bandwidth, packet
	res, err := queryInfluxDB(db, cmd)
	result := parseAttribute(res)


	if err != nil {
		log.Error(netNSID)
		return nil, fmt.Errorf("failed to get data from InfluxDB: %v", err)
	}

	// cache parameters
	trafficControlStatusCache[netNSID] = &TrafficControlStatus{
		dpod:        result[0], // output_pod
		latency:    result[1], // output_bandWidth
		packetLoss: result[2],  // output_packet
	}
	status, _ := trafficControlStatusCache[netNSID]

	return status, err
}


func getStatusByPod(dpod string) (*NetworkControlStatus, error) {
	var cmd = "select * from p2prxbyte, p2prxpkt"
	// select * from  p2prxpkt where dpod_name='redis-sfhcz'
	log.Printf("getStatusByPod for dpod: %s", cmd)  // billzhang 2017-05-12
	log.Printf("getStatusByPod for dpod: %s", dpod)  // billzhang 2017-04-04
	//if status, ok := neworkControlStatusCache[dpod]; ok {
	//	return status, nil
	//}
	//
	// cmd  = cmd + " where dpod_name = '" +  spod + "' " + " order by time desc limit 1"
	//// cmd  = cmd + " order by time desc limit 1"
	//log.Printf("query InfluxDB by : %s ", cmd)
	//
	//// query dpod_name, bandwidth, packet
	//resp, err := queryInfluxDB(db, cmd)
	//// parse dpod_name, bandwidth, packet from InfluxDB response
	//// result := parseAttribute(resp)
	//
	//if err != nil {
	//	log.Error(err)
	//	return nil, fmt.Errorf("failed to get data from InfluxDB: %v", err)
	//}
	//
	//status := parseStatus(resp)
	//log.Printf("status: ", status)


	// cache parameters
	/*if (status != nil) {
		neworkControlStatusCache[spod] = &NetworkControlStatus{
			dpod:       status.dpod, // output_pod
			bandwidth:  status.bandwidth, // output_bandWidth
			latency:    random(140, 230), // output_latency
			packet:     status.packet,  // output_packet
		}

	} else {
		neworkControlStatusCache[dpod] = &NetworkControlStatus{
			dpod:       status.dpod, // output_pod
			bandwidth:  random(700, 34000), // output_bandWidth
			latency:    random(140, 230), // output_latency
			packet:     random(5, 80),  // output_packet
		}
	}
	status, _ = neworkControlStatusCache[spod] */

	var status = &NetworkControlStatus{
		dpod:       dpod, // output_pod
		bandwidth:  strconv.Itoa(random(700, 34000)), // output_bandWidth
		latency:    strconv.Itoa(random(140, 230)),  // output_latency
		packet:     strconv.Itoa(random(5, 80)),  // output_packet
	}
	log.Printf("status: ", status)

	return status, nil
}



/*
  parse dpod_name, bandwidth and packet from InfluxDB response
 */
func parseStatus(resp *client.Response) (*NetworkControlStatus) {
	log.Printf("enter parseStatus for dpod_name, bandwidth and packet")
	log.Info(resp)  // billzhang 2017-04-04
	var status *NetworkControlStatus
	status = &NetworkControlStatus{
		dpod:       "dpod", // output_pod
		bandwidth:  strconv.Itoa(random(700, 34000)), // output_bandWidth
		latency:    strconv.Itoa(random(140, 230)),  // output_latency
		packet:     strconv.Itoa(random(5, 80)),  // output_packet
	}
	//
	//if (len(resp.Results) > 1) {
	//	log.Printf("Inside, len(resp.Results) = %d ", len(resp.Results))
	//	log.Info(resp.Results[0])
	//
	//	if (len(resp.Results[0].Series) > 1) {
	//		dpod, ok := resp.Results[0].Series[0].Values[0][1].(string)
	//
	//		if (ok) {
	//			log.Printf("dpod_name : %s ", dpod)
	//			log.Printf("spod_name : %s ", resp.Results[0].Series[0].Values[0][5].(string))
	//			status.dpod = dpod
	//		} else {
	//			log.Printf("failed convert series data to dpod_name , status: ", ok)
	//		}
	//
	//		bandwidth, err := resp.Results[0].Series[0].Values[0][8].(json.Number).Int64()
	//		packet, err := resp.Results[0].Series[1].Values[0][8].(json.Number).Int64()
	//		if err != nil {
	//
	//			log.Errorf("failed read data from InfluxDB: %v", err)
	//		}
	//		status.bandwidth = strconv.FormatInt(bandwidth, 10)
	//		status.packet = strconv.FormatInt(packet, 10)
	//	}
	//
	//} else {
	//	log.Printf("len(resp.Results) = %d ", len(resp.Results))
	//}

	return status

}
/*
  parse dpod_name, bandwidth and packet from InfluxDB response
 */
func parseAttribute(resp *client.Response) ([]string) {
	log.Printf("enter parseAttribute for dpod_name, bandwidth and packet")
	log.Info(resp)  // billzhang 2017-04-04
	result := []string{"-", "-", "-"}
	//fmt.Println("", myData[0]) //first element in slice
	//fmt.Println("time: ", myData[0][0])
	//fmt.Println("dpod_name: ", myData[0][1])
	//fmt.Println("dpod_namespace: ", myData[0][2])
	//fmt.Println("dst: ", myData[0][3])
	//fmt.Println("host: ", myData[0][4])
	//fmt.Println("spod_name: ", myData[0][5])
	//fmt.Println("spod_namespace: ", myData[0][6])
	//fmt.Println("src: ", myData[0][7])
	//fmt.Println("value: ", myData[0][8])
	if (resp != nil) {
		log.Printf("Inside, len(resp.Results) = %d ", len(resp.Results))
		log.Info(resp.Results[0])

		if (len(resp.Results[0].Series) > 1) {
			dpod, ok := resp.Results[0].Series[0].Values[0][1].(string)

			if (ok) {
				log.Printf("dpod_name : %s ", dpod)
				log.Printf("spod_name : %s ", resp.Results[0].Series[0].Values[0][5].(string))
				result[0] = dpod
			} else {
				log.Printf("failed convert series data to dpod_name , status: ", ok)
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
		log.Printf("resp is :  ", resp)
	}
	return result

}

/*
top
  PID USER      PR  NI    VIRT    RES    SHR S  %CPU %MEM     TIME+ COMMAND
 5679 10001     20   0   15868   9808   7264 S   0.3  0.1   0:02.89 user

 Kubernetes Pod
 NAMESPACE     NAME                      READY     STATUS    RESTARTS   AGE       IP               NODE
 default       user-468431046-ktrt4       1/1       Running   3          4d        10.32.0.25  hua-system-76

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

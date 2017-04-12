package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	log "github.com/Sirupsen/logrus"
)

const (
	networkControlTablePrefix = "network-control-table-"
)

type report struct {
	Container topology
	Plugins   []pluginSpec
}

type topology struct {
	Nodes             map[string]node             `json:"nodes"`
	Controls          map[string]control          `json:"controls"`
	MetadataTemplates map[string]metadataTemplate `json:"metadata_templates,omitempty"`
	TableTemplates    map[string]tableTemplate    `json:"table_templates,omitempty"`
}

type tableTemplate struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Prefix string `json:"prefix"`
}

type metadataTemplate struct {
	ID       string  `json:"id"`
	Label    string  `json:"label,omitempty"`    // Human-readable descriptor for this row
	Truncate int     `json:"truncate,omitempty"` // If > 0, truncate the value to this length.
	Datatype string  `json:"dataType,omitempty"`
	Priority float64 `json:"priority,omitempty"`
	From     string  `json:"from,omitempty"`     // Defines how to get the value from a report node
}

type node struct {
	LatestControls map[string]controlEntry `json:"latestControls,omitempty"`
	Latest         map[string]stringEntry  `json:"latest,omitempty"`
}

type controlEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Value     controlData `json:"value"`
}

type controlData struct {
	Dead bool `json:"dead"`
}

type control struct {
	ID    string `json:"id"`
	Human string `json:"human"`
	Icon  string `json:"icon"`
	Rank  int    `json:"rank"`
}

type stringEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Value     string    `json:"value"`
}

type pluginSpec struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Interfaces  []string `json:"interfaces"`
	APIVersion  string   `json:"api_version,omitempty"`
}

// Reporter internal data structure
type Reporter struct {
	store *Store
}

// NewReporter instantiates a new Reporter
func NewReporter(store *Store) *Reporter {
	return &Reporter{
		store: store,
	}
}

// RawReport returns a report
func (r *Reporter) RawReport() ([]byte, error) {
	log.Debugf("enter RawReport")  // billzhang 2017-04-04
	rpt := &report{
		Container: topology{
			Nodes:             r.getContainerNodes(),
			Controls:          getTrafficControls(),
			MetadataTemplates: getMetadataTemplate(),
			TableTemplates:    getTableTemplate(),
		},
		Plugins: []pluginSpec{
			{
				ID:          "network-control",
				Label:       "Network control",
				Description: "Adds Network controls to the running Docker containers",
				Interfaces:  []string{"reporter", "controller"},
				APIVersion:  "1",
			},
		},
	}
	raw, err := json.Marshal(rpt)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal the report: %v", err)
	}
	return raw, nil
}

// GetHandler returns the function performing the action specified by controlID
func (r *Reporter) GetHandler(nodeID, controlID string) (func() error, error) {
	log.Debugf("enter GetHandler for nodeID %d", nodeID)  // billzhang 2017-04-04
	log.Debugf("enter GetHandler for controlID %s", controlID)  // billzhang 2017-04-04
	containerID, err := nodeIDToContainerID(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get container ID from node ID %q: %v", nodeID, err)
	}
	container, found := r.store.Container(containerID)
	if !found {
		return nil, fmt.Errorf("container %s not found", containerID)
	}
	var handler func(pid int) error
	for _, c := range getControls() {
		if c.control.ID == controlID {
			handler = c.handler
			break
		}
	}
	if handler == nil {
		return nil, fmt.Errorf("unknown control ID %q for node ID %q", controlID, nodeID)
	}
	return func() error {
		return handler(container.PID)
	}, nil
}

// states:
// created, destroyed - don't create any node
// running, not running - create node with controls
func (r *Reporter) getContainerNodes() map[string]node {
	log.Debugf("enter getContainerNodes")  // billzhang 2017-04-04
	var status *TrafficControlStatus
	var err error
	nodes := map[string]node{}
	timestamp := time.Now()
	r.store.ForEach(func(containerID string, container Container) {
		dead := false
		switch container.State {
		case Created, Destroyed:
		// do nothing, to prevent adding a stale node
		// to a report
		case Stopped:
			dead = true
			fallthrough
		case Running:
			nodeID := containerIDToNodeID(containerID)
			spod, _ := getPod(containerID)
			log.Debugf("getStatusByPod by Pod: ", spod)
			if status, err = getStatusByPod(spod); err != nil {
				if err != nil {
					log.Fatalf("unexpected error.  expected %v, actual %v", nil, err)
				}
			} else if status == nil {
				fmt.Errorf("status for pod %s does not exist", spod)
			}

			nodes[nodeID] = node{
				LatestControls: getTrafficNodeControls(timestamp, dead),
				Latest: map[string]stringEntry{
					fmt.Sprintf("%s%s", networkControlTablePrefix, "dst-pod"): {
						Timestamp: timestamp,
						Value:     status.dpod,
					},
					fmt.Sprintf("%s%s", networkControlTablePrefix, "bandwidth"): {
						Timestamp: timestamp,
						Value:     status.latency,
					},
					fmt.Sprintf("%s%s", networkControlTablePrefix, "packet"): {
						Timestamp: timestamp,
						Value:     status.packetLoss,
					},
				},
			}
		}
	})
	return nodes
}

func getMetadataTemplate() map[string]metadataTemplate {
	log.Debugf("enter getMetadataTemplate")  // billzhang 2017-04-04
	return map[string]metadataTemplate{
		"network-control-latency": {
			ID:       "network-control-latency",
			Label:    "Latency",
			Truncate: 0,
			Datatype: "",
			Priority: 13.5,
			From:     "latest",
		},
		"network-control-pktloss": {
			ID:       "network-control-pktloss",
			Label:    "Packet Loss",
			Truncate: 0,
			Datatype: "",
			Priority: 13.6,
			From:     "latest",
		},
	}
}

func getTableTemplate() map[string]tableTemplate {
	return map[string]tableTemplate{
		"network-control-table": {
			ID:     "network-control-table",
			Label:  "Network Control",
			Prefix: networkControlTablePrefix,
		},
	}
}

func getTrafficNodeControls(timestamp time.Time, dead bool) map[string]controlEntry {
	log.Debugf("enter getTrafficNodeControls")  // billzhang 2017-04-04
	controls := map[string]controlEntry{}
	entry := controlEntry{
		Timestamp: timestamp,
		Value: controlData{
			Dead: dead,
		},
	}
	for _, c := range getControls() {
		controls[c.control.ID] = entry
	}
	return controls
}

func getTrafficControls() map[string]control {
	controls := map[string]control{}
	for _, c := range getControls() {
		controls[c.control.ID] = c.control
	}
	return controls
}

type extControl struct {
	control control
	handler func(pid int) error
}

func getLatencyControls() []extControl {
	log.Debugf("enter getLatencyControls")  // billzhang 2017-04-04
	return []extControl{
		{
			control: control{
				ID:    fmt.Sprintf("%s%s", networkControlTablePrefix, "slow"),
				Human: "Traffic speed: slow",
				Icon:  "fa-hourglass-1",
				Rank:  20,
			},
			handler: func(pid int) error {
				return ApplyLatency(pid, "2000ms")
			},
		},
		{
			control: control{
				ID:    fmt.Sprintf("%s%s", networkControlTablePrefix, "medium"),
				Human: "Traffic speed: medium",
				Icon:  "fa-hourglass-2",
				Rank:  21,
			},
			handler: func(pid int) error {
				return ApplyLatency(pid, "1000ms")
			},
		},
		{
			control: control{
				ID:    fmt.Sprintf("%s%s", networkControlTablePrefix, "fast"),
				Human: "Traffic speed: fast",
				Icon:  "fa-hourglass-3",
				Rank:  22,
			},
			handler: func(pid int) error {
				return ApplyLatency(pid, "500ms")
			},
		},
	}
}

func getPacketLossControls() []extControl {
	return []extControl{
		{
			control: control{
				ID:    fmt.Sprintf("%s%s", networkControlTablePrefix, "pkt-drop-low"),
				Human: "Packet drop: low",
				Icon:  "fa-cut",
				Rank:  23,
			},
			handler: func(pid int) error {
				return ApplyPacketLoss(pid, "10%")
			},
		},
	}
}

func getGeneralControls() []extControl {
	return []extControl{
		{
			control: control{
				ID:    fmt.Sprintf("%s%s", networkControlTablePrefix, "clear"),
				Human: "Clear traffic control settings",
				Icon:  "fa-times-circle",
				Rank:  24,
			},
			handler: func(pid int) error {
				return ClearTrafficControlSettings(pid)
			},
		},
	}
}

func getControls() []extControl {
	controls := getLatencyControls()
	// TODO alepuccetti why append(controls, getPacketLossControls()) does not work?
	for _, ctrl := range getPacketLossControls() {
		controls = append(controls, ctrl)
	}
	for _, ctrl := range getGeneralControls() {
		controls = append(controls, ctrl)
	}
	return controls
}

const nodeSuffix = ";<container>"

func containerIDToNodeID(containerID string) string {
	return fmt.Sprintf("%s%s", containerID, nodeSuffix)
}

func nodeIDToContainerID(nodeID string) (string, error) {
	if !strings.HasSuffix(nodeID, nodeSuffix) {
		return "", fmt.Errorf("no suffix %q in node ID %q", nodeSuffix, nodeID)
	}
	return strings.TrimSuffix(nodeID, nodeSuffix), nil
}

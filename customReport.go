package main

import "sync"

// Plugin groups the methods a plugin needs
type Plugin struct {
	lock       sync.Mutex
}

type report struct {
	Plugins []pluginSpec
}

func (p *Plugin) makeReport() (*report, error) {
	rpt := &report{
		Plugins: []pluginSpec{
			{
				ID:          "plugin-id",
				Label:       "Plugin Name",
				Description: "Plugin short description",
				Interfaces:  []string{"reporter"},
				APIVersion:  "1",
			},
		},
	}
	return rpt, nil
}

// Report is called by scope when a new report is needed. It is part of the
// "reporter" interface, which all plugins must implement.
func (p *Plugin) Report(w http.ResponseWriter, r *http.Request) {
	p.lock.Lock()
	defer p.lock.Unlock()
	log.Println(r.URL.String())
	rpt, err := p.makeReport()
	if err != nil {
		log.Printf("error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	raw, err := json.Marshal(*rpt)
	if err != nil {
		log.Printf("error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

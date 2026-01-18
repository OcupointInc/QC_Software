package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"github.com/dma/pkg/psu"
)

//go:embed index.html
var templatesFS embed.FS

func main() {
	port := flag.Int("p", 8080, "Port to listen on")
	psuAddr := flag.String("psu", "TCPIP::192.168.1.200::inst0::INSTR", "PSU VISA address")
	flag.Parse()

	log.Printf("Initializing PSU at %s...", *psuAddr)
	if err := psu.InitGlobalPSU(*psuAddr); err != nil {
		log.Printf("Warning: Failed to initialize PSU: %v", err)
	} else {
		log.Println("PSU initialized.")
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := template.ParseFS(templatesFS, "index.html")
		if err != nil {
			http.Error(w, "Template error: "+err.Error(), 500)
			return
		}
		tmpl.Execute(w, nil)
	})

	// API Handlers
	http.HandleFunc("/api/psu/state", handlePSUState)
	http.HandleFunc("/api/psu/output/2/enable", handlePSUEnable)
	http.HandleFunc("/api/psu/voltage", handlePSUVoltage)
	http.HandleFunc("/api/psu/current", handlePSUCurrent)

	log.Printf("PSU Controller listening on :%d", *port)
	log.Printf("Control interface: http://localhost:%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}

func handlePSUState(w http.ResponseWriter, r *http.Request) {
	p := psu.GetGlobalPSU()
	if p == nil {
		http.Error(w, "PSU not initialized", 500)
		return
	}
	state := p.GetState()
	json.NewEncoder(w).Encode(state)
}

func handlePSUEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	p := psu.GetGlobalPSU()
	if p == nil {
		http.Error(w, "PSU not initialized", 500)
		return
	}

	if err := p.SetOutput(req.Enabled); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handlePSUVoltage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		Voltage float64 `json:"voltage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	p := psu.GetGlobalPSU()
	if p == nil {
		http.Error(w, "PSU not initialized", 500)
		return
	}

	if err := p.SetVoltage(req.Voltage); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handlePSUCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		Current float64 `json:"current"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	p := psu.GetGlobalPSU()
	if p == nil {
		http.Error(w, "PSU not initialized", 500)
		return
	}

	if err := p.SetCurrent(req.Current); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

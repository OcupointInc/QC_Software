package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type RecordStartRequest struct {
	Samples int `json:"samples"`
}

func handleRecordStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req RecordStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	if req.Samples <= 0 {
		http.Error(w, "Invalid sample count", 400)
		return
	}

	serverState.mu.Lock()
	defer serverState.mu.Unlock()

	if serverState.Recording {
		http.Error(w, "Already recording", 409)
		return
	}

	// Create data directory if not exists
	dataDir := "data"
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		os.Mkdir(dataDir, 0755)
	}

	// Generate filename: capture_YYYYMMDD_HHMMSS.bin
	filename := fmt.Sprintf("capture_%s.bin", time.Now().Format("20060102_150405"))
	filepath := filepath.Join(dataDir, filename)

	f, err := os.Create(filepath)
	if err != nil {
		http.Error(w, "Failed to create file: "+err.Error(), 500)
		return
	}

	serverState.Recording = true
	serverState.RecordingFile = filename
	serverState.RecordingSamples = req.Samples
	serverState.RecordingCurrent = 0
	serverState.RecordingFileHandle = f

	// Broadcast start
	go broadcastJSON(map[string]interface{}{
		"type":     "recording_status",
		"recording": true,
		"filename": filename,
		"total":    req.Samples,
		"current":  0,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"filename": filename,
	})
}

func handleRecordStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}

	serverState.mu.Lock()
	defer serverState.mu.Unlock()

	if !serverState.Recording {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Not recording"})
		return
	}

	// Close file
	if serverState.RecordingFileHandle != nil {
		serverState.RecordingFileHandle.Close()
		serverState.RecordingFileHandle = nil
	}
	serverState.Recording = false

	// Broadcast stop
	go broadcastJSON(map[string]interface{}{
		"type":     "recording_status",
		"recording": false,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handleRecordStatus(w http.ResponseWriter, r *http.Request) {
	serverState.mu.RLock()
	defer serverState.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"recording": serverState.Recording,
		"filename":  serverState.RecordingFile,
		"total":     serverState.RecordingSamples,
		"current":   serverState.RecordingCurrent,
	})
}

package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const dataFolder = "data"

// API Handlers

func handleRFConfig(w http.ResponseWriter, r *http.Request) {
	serverState.mu.RLock()
	defer serverState.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ddc_freq_mhz": serverState.DDCFreqMHz,
		"ibw_mhz":      serverState.IBWMHZ,
	})
}

func handleDDCFrequency(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		serverState.mu.RLock()
		defer serverState.mu.RUnlock()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ddc_freq_mhz": serverState.DDCFreqMHz,
		})
		return
	}

	if r.Method == "POST" {
		var req struct {
			FreqMHz float64 `json:"freq_mhz"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		serverState.mu.Lock()
		serverState.DDCFreqMHz = req.FreqMHz
		serverState.mu.Unlock()

		// Broadcast to all clients
		broadcastJSON(map[string]interface{}{
			"type":     "ddc_update",
			"freq_mhz": req.FreqMHz,
		})

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"ddc_freq_mhz": req.FreqMHz,
		})
	}
}

func handleSigGenState(w http.ResponseWriter, r *http.Request) {
	serverState.mu.RLock()
	defer serverState.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"freq_mhz":  serverState.SigGenFreqMHz,
		"power_dbm": serverState.SigGenPowerDBm,
		"rf_output": serverState.SigGenRFOutput,
	})
}

func handleSigGenFrequency(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		FreqMHz float64 `json:"freq_mhz"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	serverState.mu.Lock()
	serverState.SigGenFreqMHz = req.FreqMHz
	serverState.mu.Unlock()

	broadcastJSON(map[string]interface{}{
		"type":     "siggen_freq_update",
		"freq_mhz": req.FreqMHz,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"freq_mhz": req.FreqMHz,
	})
}

func handleSigGenPower(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		PowerDBm float64 `json:"power_dbm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	serverState.mu.Lock()
	serverState.SigGenPowerDBm = req.PowerDBm
	serverState.mu.Unlock()

	broadcastJSON(map[string]interface{}{
		"type":      "siggen_power_update",
		"power_dbm": req.PowerDBm,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"power_dbm": req.PowerDBm,
	})
}

func handleSigGenOutput(w http.ResponseWriter, r *http.Request) {
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

	serverState.mu.Lock()
	serverState.SigGenRFOutput = req.Enabled
	serverState.mu.Unlock()

	broadcastJSON(map[string]interface{}{
		"type":    "siggen_output_update",
		"enabled": req.Enabled,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"enabled": req.Enabled,
	})
}

func handleSweepStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var params SweepParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	serverState.mu.Lock()
	serverState.SweepRunning = true
	serverState.SweepParams = &params
	serverState.mu.Unlock()

	// Start sweep in background
	go runSweep(&params)

	broadcastJSON(map[string]interface{}{
		"type":    "sweep_status",
		"running": true,
		"params":  params,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handleSweepStop(w http.ResponseWriter, r *http.Request) {
	serverState.mu.Lock()
	serverState.SweepRunning = false
	serverState.mu.Unlock()

	broadcastJSON(map[string]interface{}{
		"type":    "sweep_status",
		"running": false,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handleSweepState(w http.ResponseWriter, r *http.Request) {
	serverState.mu.RLock()
	defer serverState.mu.RUnlock()

	resp := map[string]interface{}{
		"running": serverState.SweepRunning,
	}
	if serverState.SweepParams != nil {
		resp["params"] = serverState.SweepParams
	}
	json.NewEncoder(w).Encode(resp)
}

func runSweep(params *SweepParams) {
	freq := params.StartMHz
	for {
		serverState.mu.RLock()
		running := serverState.SweepRunning
		serverState.mu.RUnlock()

		if !running {
			return
		}

		serverState.mu.Lock()
		serverState.SigGenFreqMHz = freq
		serverState.mu.Unlock()

		broadcastJSON(map[string]interface{}{
			"type":     "sweep_progress",
			"freq_mhz": freq,
		})

		time.Sleep(time.Duration(params.DwellMS) * time.Millisecond)

		freq += params.StepMHz
		if freq > params.StopMHz {
			freq = params.StartMHz
		}
	}
}

// PSU stub handlers (simulated)
func handlePSUState(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"outputs": []map[string]interface{}{
			{
				"voltage":          5.0,
				"current_limit":    1.0,
				"measured_voltage": 5.0,
				"measured_current": 0.25,
				"enabled":          true,
			},
			{
				"voltage":          12.0,
				"current_limit":    2.0,
				"measured_voltage": 12.0,
				"measured_current": 0.5,
				"enabled":          false,
			},
		},
	})
}

func handlePSUEnable(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handlePSUVoltage(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handlePSUCurrent(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// Replay mode handlers

func ensureDataFolder() error {
	return os.MkdirAll(dataFolder, 0755)
}

func handleReplayUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	if err := ensureDataFolder(); err != nil {
		http.Error(w, "Failed to create data folder: "+err.Error(), 500)
		return
	}

	// Parse multipart form (max 500MB)
	err := r.ParseMultipartForm(500 << 20)
	if err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), 400)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to get file: "+err.Error(), 400)
		return
	}
	defer file.Close()

	// Read file data
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read file: "+err.Error(), 500)
		return
	}

	// Save to data folder (sanitize filename to prevent path traversal)
	safeFilename := filepath.Base(header.Filename)
	filePath := filepath.Join(dataFolder, safeFilename)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		http.Error(w, "Failed to save file: "+err.Error(), 500)
		return
	}

	log.Printf("[REPLAY] Saved %s (%d bytes) to %s", safeFilename, len(data), filePath)

	// Broadcast file list update
	broadcastFileList()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"filename": safeFilename,
		"size":     len(data),
	})
}

func handleReplayFiles(w http.ResponseWriter, r *http.Request) {
	if err := ensureDataFolder(); err != nil {
		http.Error(w, "Failed to access data folder: "+err.Error(), 500)
		return
	}

	entries, err := os.ReadDir(dataFolder)
	if err != nil {
		http.Error(w, "Failed to read data folder: "+err.Error(), 500)
		return
	}

	type FileInfo struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}

	files := []FileInfo{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".bin") {
			info, err := entry.Info()
			if err == nil {
				files = append(files, FileInfo{
					Name: entry.Name(),
					Size: info.Size(),
				})
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"files": files,
	})
}

func handleReplaySelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// Load file from data folder (sanitize filename)
	safeFilename := filepath.Base(req.Filename)
	filePath := filepath.Join(dataFolder, safeFilename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "Failed to load file: "+err.Error(), 404)
		return
	}

	serverState.mu.Lock()
	serverState.ReplayData = data
	serverState.ReplayName = req.Filename
	serverState.ReplayOffset = 0
	serverState.mu.Unlock()

	log.Printf("[REPLAY] Selected %s (%d bytes)", req.Filename, len(data))

	broadcastJSON(map[string]interface{}{
		"type":        "replay_update",
		"has_data":    true,
		"filename":    req.Filename,
		"size":        len(data),
		"replay_mode": serverState.ReplayMode,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"filename": req.Filename,
		"size":     len(data),
	})
}

func handleReplayDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// If this file is currently loaded, clear it
	serverState.mu.Lock()
	if serverState.ReplayName == req.Filename {
		serverState.ReplayMode = false
		serverState.ReplayData = nil
		serverState.ReplayName = ""
		serverState.ReplayOffset = 0
	}
	serverState.mu.Unlock()

	// Delete file (sanitize filename)
	safeFilename := filepath.Base(req.Filename)
	filePath := filepath.Join(dataFolder, safeFilename)
	if err := os.Remove(filePath); err != nil {
		http.Error(w, "Failed to delete file: "+err.Error(), 500)
		return
	}

	log.Printf("[REPLAY] Deleted %s", req.Filename)

	// Broadcast updates
	broadcastFileList()
	broadcastJSON(map[string]interface{}{
		"type":        "replay_update",
		"has_data":    len(serverState.ReplayData) > 0,
		"filename":    serverState.ReplayName,
		"replay_mode": serverState.ReplayMode,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func handleReplayState(w http.ResponseWriter, r *http.Request) {
	serverState.mu.RLock()
	defer serverState.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"replay_mode": serverState.ReplayMode,
		"has_data":    len(serverState.ReplayData) > 0,
		"filename":    serverState.ReplayName,
		"size":        len(serverState.ReplayData),
	})
}

func handleReplayToggle(w http.ResponseWriter, r *http.Request) {
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

	serverState.mu.Lock()
	if req.Enabled && len(serverState.ReplayData) == 0 {
		serverState.mu.Unlock()
		http.Error(w, "No replay data loaded", 400)
		return
	}
	serverState.ReplayMode = req.Enabled
	if req.Enabled {
		serverState.ReplayOffset = 0 // Reset to start
	}
	serverState.mu.Unlock()

	log.Printf("[REPLAY] Mode set to %v", req.Enabled)

	broadcastJSON(map[string]interface{}{
		"type":        "replay_update",
		"replay_mode": req.Enabled,
		"has_data":    len(serverState.ReplayData) > 0,
		"filename":    serverState.ReplayName,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"replay_mode": req.Enabled,
	})
}

func handleReplayClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	serverState.mu.Lock()
	serverState.ReplayMode = false
	serverState.ReplayData = nil
	serverState.ReplayName = ""
	serverState.ReplayOffset = 0
	serverState.mu.Unlock()

	log.Println("[REPLAY] Selection cleared")

	broadcastJSON(map[string]interface{}{
		"type":        "replay_update",
		"replay_mode": false,
		"has_data":    false,
		"filename":    "",
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func broadcastFileList() {
	entries, err := os.ReadDir(dataFolder)
	if err != nil {
		return
	}

	type FileInfo struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}

	files := []FileInfo{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".bin") {
			info, err := entry.Info()
			if err == nil {
				files = append(files, FileInfo{
					Name: entry.Name(),
					Size: info.Size(),
				})
			}
		}
	}

	broadcastJSON(map[string]interface{}{
		"type":  "replay_files",
		"files": files,
	})
}

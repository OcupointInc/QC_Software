package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RecordStartRequest struct {
	Samples  int             `json:"samples"`
	Mode     string          `json:"mode"`  // "samples", "time", "size"
	Value    string          `json:"value"` // Input string
	Filename string          `json:"filename"`
	Config   *HardwareConfig `json:"config"`
}

func parseSize(value string) (int, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	multiplier := 1
	if strings.HasSuffix(value, "GB") {
		multiplier = 1024 * 1024 * 1024
		value = strings.TrimSuffix(value, "GB")
	} else if strings.HasSuffix(value, "MB") {
		multiplier = 1024 * 1024
		value = strings.TrimSuffix(value, "MB")
	} else if strings.HasSuffix(value, "KB") {
		multiplier = 1024
		value = strings.TrimSuffix(value, "KB")
	} else if strings.HasSuffix(value, "B") {
		value = strings.TrimSuffix(value, "B")
	}
	val, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return val * multiplier, nil
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

	// Calculate samples based on Mode
	const sampleRate = 244500000
	const bytesPerSample = 32

	if req.Mode != "" && req.Value != "" {
		switch req.Mode {
		case "samples":
			if s, err := strconv.Atoi(req.Value); err == nil {
				req.Samples = s
			}
		case "time":
			// Try time.ParseDuration (handles 10s, 500ms)
			// If it's just a number, assume seconds
			val := req.Value
			if _, err := strconv.ParseFloat(val, 64); err == nil {
				val += "s"
			}
			if d, err := time.ParseDuration(val); err == nil {
				req.Samples = int(d.Seconds() * float64(sampleRate))
			}
		case "size":
			if bytes, err := parseSize(req.Value); err == nil {
				req.Samples = bytes / bytesPerSample
			}
		}
	}

	if req.Samples <= 0 {
		http.Error(w, "Invalid sample count", 400)
		return
	}

	// Apply hardware configuration if provided
	if req.Config != nil && hwController != nil {
		if err := hwController.ApplyConfig(req.Config); err != nil {
			log.Printf("Error applying hardware config: %v", err)
		}
		
		// Update server state center frequency if DDC0 changes
		if req.Config.DDC0FreqMHz != nil {
			serverState.mu.Lock()
			serverState.DDCFreqMHz = float64(*req.Config.DDC0FreqMHz)
			serverState.mu.Unlock()
		}
	}

	serverState.mu.Lock()
	// Do NOT defer unlock here because we want to unlock before starting goroutine (though logically fine, better explicitly manage if we accessed complex state)
	// But defer is fine for this short block.
	
	if serverState.Recording {
		serverState.mu.Unlock()
		http.Error(w, "Already recording", 409)
		return
	}

	// Create data directory if not exists
	dataDir := "data"
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		os.Mkdir(dataDir, 0755)
	}

	// Determine filename
	var filename string
	if req.Filename != "" {
		filename = req.Filename
		if !strings.HasSuffix(filename, ".bin") {
			filename += ".bin"
		}
		// Sanitize to prevent path traversal
		filename = filepath.Base(filename)
	} else {
		// Generate filename: capture_YYYYMMDD_HHMMSS.bin
		filename = fmt.Sprintf("capture_%s.bin", time.Now().Format("20060102_150405"))
	}

	fullPath := filepath.Join(dataDir, filename)

	f, err := os.Create(fullPath)
	if err != nil {
		serverState.mu.Unlock()
		http.Error(w, "Failed to create file: "+err.Error(), 500)
		return
	}

	// Use currently viewed channels if not explicitly set in the request
	// (Always override RecordingChannels with GUI selection for consistency)
	serverState.RecordingChannels = nil
	
	// Convert serverState.Channels (e.g. ["I1", "Q1", "I3"]) to indices
	channelMap := make(map[int]bool)
	for _, chName := range serverState.Channels {
		if len(chName) >= 2 {
			// Parse channel index from name like "I1" or "Q1"
			// Channels are named I1, Q1, I2, Q2, ..., I8, Q8
			if idx, err := strconv.Atoi(chName[1:]); err == nil {
				channelMap[idx-1] = true
			}
		}
	}

	if len(channelMap) > 0 {
		serverState.RecordingChannels = make([]int, 0, len(channelMap))
		for chIdx := range channelMap {
			serverState.RecordingChannels = append(serverState.RecordingChannels, chIdx)
		}
		sort.Ints(serverState.RecordingChannels)
	} else {
		// Fallback to all channels if nothing selected
		serverState.RecordingChannels = []int{0, 1, 2, 3, 4, 5, 6, 7}
	}

	serverState.Recording = true
	serverState.RecordingFile = filename
	serverState.RecordingSamples = req.Samples
	serverState.RecordingCurrent = 0
	serverState.RecordingFileHandle = f
	serverState.mu.Unlock()

	// Save Metadata
	metaFilename := strings.TrimSuffix(filename, ".bin") + ".json"
	metaPath := filepath.Join(dataDir, metaFilename)

	var currentConfig *HardwareConfig
	if hwController != nil {
		currentConfig = hwController.GetConfig()
	}

	serverState.mu.RLock()
	// Convert internal 0-7 indices to user-facing 1-8
	activeChannels := make([]int, len(serverState.RecordingChannels))
	for i, ch := range serverState.RecordingChannels {
		activeChannels[i] = ch + 1
	}
	serverState.mu.RUnlock()

	metadata := CaptureMetadata{
		Timestamp:  time.Now().Format(time.RFC3339),
		SampleRate: 244400000,
		Channels:   activeChannels,
		Config:     currentConfig,
	}
	
	if metaBytes, err := json.MarshalIndent(metadata, "", "  "); err == nil {
		os.WriteFile(metaPath, metaBytes, 0644)
	}

	// Broadcast start
	go broadcastJSON(map[string]interface{}{
		"type":     "recording_status",
		"recording": true,
		"filename": filename,
		"total":    req.Samples,
		"current":  0,
	})

	// Start the recording loop in background
	go performRecording()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"filename": filename,
	})
}

func cleanupRecording(errorMsg string) {
	serverState.mu.Lock()
	defer serverState.mu.Unlock()

	if serverState.RecordingFileHandle != nil {
		serverState.RecordingFileHandle.Close()
		serverState.RecordingFileHandle = nil
	}
	serverState.Recording = false

	msg := map[string]interface{}{
		"type":      "recording_status",
		"recording": false,
		"finished":  true,
	}
	if errorMsg != "" {
		msg["error"] = errorMsg
		msg["finished"] = false
	}
	go broadcastJSON(msg)
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
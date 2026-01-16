package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type RecordStartRequest struct {
	Samples int             `json:"samples"`
	Config  *HardwareConfig `json:"config"`
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

	// Generate filename: capture_YYYYMMDD_HHMMSS.bin
	filename := fmt.Sprintf("capture_%s.bin", time.Now().Format("20060102_150405"))
	fullPath := filepath.Join(dataDir, filename)

	f, err := os.Create(fullPath)
	if err != nil {
		serverState.mu.Unlock()
		http.Error(w, "Failed to create file: "+err.Error(), 500)
		return
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

	metadata := CaptureMetadata{
		Timestamp:  time.Now().Format(time.RFC3339),
		SampleRate: 250000000,
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

func performRecording() {
	// 1. Wait a bit for the global loop to release device
	time.Sleep(200 * time.Millisecond)

	serverState.mu.RLock()
	devicePath := serverState.DevicePath
	samplesTotal := serverState.RecordingSamples
	// f is accessed via serverState to allow handleRecordStop to close it if needed,
	// but we should probably grab a local ref or check serverState repeatedly.
	// For simplicity, we check serverState in the loop.
	serverState.mu.RUnlock()

	if devicePath == "" {
		log.Println("Error: Device path not set, defaulting to /dev/xdma0_c2h_0")
		devicePath = "/dev/xdma0_c2h_0"
	}

	// 2. Open Device
	log.Printf("Opening device %s for recording...", devicePath)
	fd, err := unix.Open(devicePath, unix.O_RDONLY, 0)
	if err != nil {
		log.Printf("Failed to open device for recording: %v", err)
		cleanupRecording(err.Error())
		return
	}
	defer unix.Close(fd)

	// Optimize pipe
	const maxPipeSize = 1024 * 1024
	_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETPIPE_SZ, maxPipeSize)

	const numChannels = 8
	const bytesPerSample = 4 // 2 byte I + 2 byte Q
	const blockSize = 4 * 1024 * 1024 // 4MB chunks

	buf := make([]byte, blockSize)
	samplesRecorded := 0
	lastBroadcast := 0

	for samplesRecorded < samplesTotal {
		// Check if stopped externally
		serverState.mu.RLock()
		if !serverState.Recording || serverState.RecordingFileHandle == nil {
			serverState.mu.RUnlock()
			break
		}
		f := serverState.RecordingFileHandle
		serverState.mu.RUnlock()

		// Read from device
		n, err := unix.Read(fd, buf)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Printf("Recording read error: %v", err)
			cleanupRecording(err.Error())
			return
		}
		if n == 0 {
			time.Sleep(1 * time.Millisecond)
			continue
		}

		// Write to file
		if _, err := f.Write(buf[:n]); err != nil {
			log.Printf("Recording write error: %v", err)
			cleanupRecording(err.Error())
			return
		}

		// Update stats
		samplesInChunk := n / (numChannels * bytesPerSample)
		samplesRecorded += samplesInChunk

		serverState.mu.Lock()
		serverState.RecordingCurrent = samplesRecorded
		serverState.mu.Unlock()

		// Broadcast progress every 1 million samples or so
		if samplesRecorded - lastBroadcast > 100000 {
			go broadcastJSON(map[string]interface{}{
				"type":    "recording_progress",
				"current": samplesRecorded,
				"total":   samplesTotal,
			})
			lastBroadcast = samplesRecorded
		}
	}

	log.Printf("Recording finished. Total samples: %d", samplesRecorded)
	cleanupRecording("")
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

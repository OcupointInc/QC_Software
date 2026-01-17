package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocket clients
var (
	wsClients         = make(map[*Client]bool)
	wsClientsMu       sync.RWMutex
	streamLoopRunning = false
)

type Client struct {
	conn     *websocket.Conn
	send     chan interface{}
	channels []string
	mu       sync.Mutex
}

// writePump pumps messages from the hub to the websocket connection.
func (c *Client) writePump() {
	defer func() {
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			switch v := msg.(type) {
			case []byte:
				if err := c.conn.WriteMessage(websocket.BinaryMessage, v); err != nil {
					return
				}
			default:
				if err := c.conn.WriteJSON(v); err != nil {
					return
				}
			}
		}
	}
}

// runServer starts the WebSocket server with embedded HTML
func runServer(port int, devicePath string, targetSize int, psuAddress string) {
	// Initialize hardware controller
	commandDevice := "/dev/xdma0_user"
	initHardwareController(commandDevice)

	// Initialize PSU if address provided
	if psuAddress != "" {
		if err := InitGlobalPSU(psuAddress); err != nil {
			log.Printf("Warning: Failed to initialize PSU: %v", err)
		}
	}

	// Load config.json for default recording channels if it exists
	if configData, err := os.ReadFile("config.json"); err == nil {
		var config HardwareConfig
		if err := json.Unmarshal(configData, &config); err == nil {
			// Apply hardware configuration
			if hwController != nil {
				if err := hwController.ApplyConfig(&config); err != nil {
					log.Printf("Warning: Error applying config: %v", err)
				}
				log.Println("Loaded and applied config.json")
			}

			// Store recording channels from config (config uses 1-8, internal uses 0-7)
			if len(config.Channels) > 0 {
				serverState.mu.Lock()
				serverState.RecordingChannels = make([]int, 0, len(config.Channels))
				for _, ch := range config.Channels {
					if ch >= 1 && ch <= 8 {
						serverState.RecordingChannels = append(serverState.RecordingChannels, ch-1)
					}
				}
				serverState.mu.Unlock()
				log.Printf("Recording channels set from config: %v (1-indexed)", config.Channels)
			}
		}
	}

	serverState.mu.Lock()
	serverState.DevicePath = devicePath
	serverState.mu.Unlock()

	upgrader := websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  1024,
		WriteBufferSize: 65536,
	}

	// Serve embedded HTML files
	templatesContent, _ := fs.Sub(templatesFS, "templates")

	// Serve static files
	staticContent, _ := fs.Sub(templatesFS, "static")
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			tmpl, err := template.ParseFS(templatesContent, "*.html")
			if err != nil {
				http.Error(w, "Template error: "+err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			tmpl.ExecuteTemplate(w, "index.html", nil)
			return
		}
		http.NotFound(w, r)
	})

	http.HandleFunc("/siggen", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(templatesContent, "siggen.html")
		if err != nil {
			http.Error(w, "Not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	// API endpoints
	http.HandleFunc("/api/rf/config", handleRFConfig)
	http.HandleFunc("/api/ddc/frequency", handleDDCFrequency)
	http.HandleFunc("/api/siggen/state", handleSigGenState)
	http.HandleFunc("/api/siggen/frequency", handleSigGenFrequency)
	http.HandleFunc("/api/siggen/power", handleSigGenPower)
	http.HandleFunc("/api/siggen/output", handleSigGenOutput)
	http.HandleFunc("/api/sweep/start", handleSweepStart)
	http.HandleFunc("/api/sweep/stop", handleSweepStop)
	http.HandleFunc("/api/sweep/state", handleSweepState)
	http.HandleFunc("/api/psu/state", handlePSUState)
	http.HandleFunc("/api/psu/output/1/enable", handlePSUEnable)
	http.HandleFunc("/api/psu/output/2/enable", handlePSUEnable)
	http.HandleFunc("/api/psu/output/1/voltage", handlePSUVoltage)
	http.HandleFunc("/api/psu/output/2/voltage", handlePSUVoltage)
	http.HandleFunc("/api/psu/output/1/current", handlePSUCurrent)
	http.HandleFunc("/api/psu/output/2/current", handlePSUCurrent)

	// Hardware control endpoints
	http.HandleFunc("/api/hardware/state", handleHardwareState)
	http.HandleFunc("/api/hardware/ddc/freq", handleDDCFreqUpdate)
	http.HandleFunc("/api/hardware/ddc/enable", handleDDCEnable)
	http.HandleFunc("/api/hardware/attenuation", handleAttenuationUpdate)
	http.HandleFunc("/api/hardware/filter", handleFilterSelect)
	http.HandleFunc("/api/hardware/calibration", handleCalibrationMode)
	http.HandleFunc("/api/hardware/system", handleSystemEnable)

	// Replay mode endpoints
	http.HandleFunc("/api/replay/upload", handleReplayUpload)
	http.HandleFunc("/api/replay/files", handleReplayFiles)
	http.HandleFunc("/api/replay/select", handleReplaySelect)
	http.HandleFunc("/api/replay/delete", handleReplayDelete)
	http.HandleFunc("/api/replay/state", handleReplayState)
	http.HandleFunc("/api/replay/toggle", handleReplayToggle)
	http.HandleFunc("/api/replay/clear", handleReplayClear)
	http.HandleFunc("/api/replay/seek", handleReplaySeek)

	// Recording endpoints
	http.HandleFunc("/api/record/start", handleRecordStart)
	http.HandleFunc("/api/record/stop", handleRecordStop)
	http.HandleFunc("/api/record/status", handleRecordStatus)

	// WebSocket streaming endpoint
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serverState.mu.RLock()
		isRecording := serverState.Recording
		serverState.mu.RUnlock()

		if isRecording {
			http.Error(w, "System is recording", http.StatusServiceUnavailable)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Upgrade:", err)
			return
		}

		log.Println("Client connected")

		client := &Client{conn: conn, send: make(chan interface{}, 256)}

		// Register client
		wsClientsMu.Lock()
		wsClients[client] = true
		shouldStart := !streamLoopRunning
		if shouldStart {
			streamLoopRunning = true
		}
		wsClientsMu.Unlock()

		if shouldStart {
			go runGlobalStreamLoop(devicePath)
		}

		// Start write pump
		go client.writePump()

		defer func() {
			wsClientsMu.Lock()
			delete(wsClients, client)
			wsClientsMu.Unlock()
			close(client.send) // This will stop writePump
			log.Println("Client disconnected")
		}()

		// Handle incoming config messages from client (read pump)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var config struct {
				Channels []string `json:"channels"`
				Mode     string   `json:"mode"`
				FPS      int      `json:"fps"`
				FFTSize  int      `json:"fft_size"`
				FFTTypes []string `json:"fft_types"`
				// New control fields
				Type    string `json:"type"`
				Enabled *bool  `json:"enabled"`
			}
			if err := json.Unmarshal(msg, &config); err == nil {
				// Update Client's requested channels
				if len(config.Channels) > 0 {
					client.mu.Lock()
					client.channels = config.Channels
					client.mu.Unlock()
				}

				serverState.mu.Lock()

				// Handle specific control messages
				if config.Type == "stream_control" && config.Enabled != nil {
					wasEnabled := serverState.StreamingEnabled
					serverState.StreamingEnabled = *config.Enabled

					// When stream is first started, initialize DDC0 to 125 MHz
					if *config.Enabled && !wasEnabled {
						serverState.mu.Unlock() // Unlock before hardware call
						initFreqMHz := 125.0
						if hwController != nil {
							actualFreq, err := setDDCFrequency(0, initFreqMHz)
							if err != nil {
								log.Printf("Failed to initialize DDC0 frequency: %v", err)
							} else {
								log.Printf("Initialized DDC0 to %.3f MHz (req %.0f) on stream start", actualFreq, initFreqMHz)
								serverState.mu.Lock()
								serverState.DDCFreqMHz = actualFreq
								serverState.mu.Unlock()
								// Broadcast frequency update to clients
								go broadcastJSON(map[string]interface{}{
									"type":      "ddc_freq_update",
									"ddc_index": 0,
									"freq_mhz":  actualFreq,
								})
							}
						}
						serverState.mu.Lock() // Re-lock for remaining config updates
					}
				}

				// Handle standard config updates
				if config.Mode != "" {
					serverState.StreamMode = config.Mode
				}
				if config.FPS > 0 {
					serverState.StreamFPS = config.FPS
				}
				if config.FFTSize > 0 {
					serverState.FFTSize = config.FFTSize
				}
				if len(config.FFTTypes) > 0 {
					serverState.FFTTypes = config.FFTTypes
				}
				serverState.mu.Unlock()
			}
		}
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("RF Stream Server listening on http://localhost%s", addr)
	log.Printf("Device: %s", devicePath)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func broadcastJSON(msg interface{}) {
	wsClientsMu.RLock()
	defer wsClientsMu.RUnlock()

	for client := range wsClients {
		select {
		case client.send <- msg:
		default:
		}
	}
}
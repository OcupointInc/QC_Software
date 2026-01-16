package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

// WebSocket clients
var (
	wsClients         = make(map[*Client]bool)
	wsClientsMu       sync.RWMutex
	streamLoopRunning = false
)

type Client struct {
	conn *websocket.Conn
	send chan interface{}
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
func runServer(port int, devicePath string, targetSize int) {
	// Initialize hardware controller
	commandDevice := "/dev/xdma0_user"
	initHardwareController(commandDevice)

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
							if err := hwController.UpdateParameter(DDC0_FMIX, int(initFreqMHz)); err != nil {
								log.Printf("Failed to initialize DDC0 frequency: %v", err)
							} else {
								log.Printf("Initialized DDC0 to %.0f MHz on stream start", initFreqMHz)
								serverState.mu.Lock()
								serverState.DDCFreqMHz = initFreqMHz
								serverState.mu.Unlock()
								// Broadcast frequency update to clients
								go broadcastJSON(map[string]interface{}{
									"type":      "ddc_freq_update",
									"ddc_index": 0,
									"freq_mhz":  initFreqMHz,
								})
							}
						}
						serverState.mu.Lock() // Re-lock for remaining config updates
					}
				}

				// Handle standard config updates
				if len(config.Channels) > 0 {
					serverState.Channels = config.Channels
				}
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

// runGlobalStreamLoop continuously reads from device (or replay buffer) and broadcasts to all clients
func runGlobalStreamLoop(devicePath string) {
	defer func() {
		wsClientsMu.Lock()
		streamLoopRunning = false
		wsClientsMu.Unlock()
		log.Println("Global stream loop stopped")
	}()

	var fd int = -1
	var deviceOpen bool = false

	const numChannels = 8
	const bytesPerSample = 4 // 2 bytes I + 2 bytes Q per channel
	const sampleSize = 1024  // samples for time domain display

	frameCounter := 0

	for {
		// Check if we should exit (no clients)
		wsClientsMu.Lock()
		if len(wsClients) == 0 {
			wsClientsMu.Unlock()
			if deviceOpen {
				unix.Close(fd)
			}
			return
		}
		wsClientsMu.Unlock()

		serverState.mu.RLock()
		fps := serverState.StreamFPS
		fftSize := serverState.FFTSize
		mode := serverState.StreamMode
		channels := serverState.Channels
		replayMode := serverState.ReplayMode
		replayData := serverState.ReplayData
		streamingEnabled := serverState.StreamingEnabled
		forceReplayUpdate := serverState.ForceReplayUpdate
		isRecording := serverState.Recording
		serverState.mu.RUnlock()

		if fps <= 0 {
			fps = 30
		}
		frameInterval := time.Second / time.Duration(fps)

		// Check if we should be streaming anything at all
		// If Recording is active, we must yield the device!
		if isRecording {
			if deviceOpen {
				unix.Close(fd)
				deviceOpen = false
				fd = -1
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if !replayMode && !streamingEnabled && !forceReplayUpdate {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Calculate how much data we need
		samplesNeeded := fftSize
		if samplesNeeded < sampleSize {
			samplesNeeded = sampleSize
		}
		bytesNeeded := samplesNeeded * numChannels * bytesPerSample

		buf := make([]byte, bytesNeeded)

		if (replayMode || forceReplayUpdate) && len(replayData) > 0 {
			// Close device if it was open
			if deviceOpen {
				unix.Close(fd)
				deviceOpen = false
				fd = -1
			}

			// Read from replay buffer (loop)
			serverState.mu.Lock()
			// Reset force flag if it was set
			if serverState.ForceReplayUpdate {
				serverState.ForceReplayUpdate = false
			}
			offset := serverState.ReplayOffset

			// Send progress update occasionally
			frameCounter++
			if frameCounter%10 == 0 {
				totalSize := len(replayData)
				progress := float64(offset) / float64(totalSize)
				go broadcastJSON(map[string]interface{}{
					"type":     "replay_progress",
					"progress": progress,
					"offset":   offset,
					"total":    totalSize,
				})
			}

			for i := 0; i < bytesNeeded; i++ {
				buf[i] = replayData[offset]
				offset = (offset + 1) % len(replayData)
			}
			serverState.ReplayOffset = offset
			serverState.mu.Unlock()
		} else {
			// Open device if not already open
			if !deviceOpen {
				var err error
				fd, err = unix.Open(devicePath, unix.O_RDONLY, 0)
				if err != nil {
					log.Printf("Could not open device %s: %v", devicePath, err)
					go broadcastJSON(map[string]string{"error": fmt.Sprintf("Could not open device: %v", err)})
					time.Sleep(1 * time.Second) // Wait before retrying
					continue
				}
				deviceOpen = true

				// Increase pipe buffer for better throughput
				const maxPipeSize = 1024 * 1024
				unix.FcntlInt(uintptr(fd), unix.F_SETPIPE_SZ, maxPipeSize)
			}

			// Read data from device
			totalRead := 0
			for totalRead < bytesNeeded {
				n, err := unix.Read(fd, buf[totalRead:])
				if err != nil {
					if err == unix.EINTR {
						continue
					}
					log.Printf("Read error: %v", err)
					if deviceOpen {
						unix.Close(fd)
						deviceOpen = false
					}
					// Wait a bit or continue
					time.Sleep(10 * time.Millisecond)
					break // Retry outer loop
				}
				if n == 0 {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				totalRead += n
			}
			if totalRead < bytesNeeded {
				continue // Incomplete frame
			}
		}

		// Parse into channel data
		// Data format: for each sample, 8 channels * (I16 + Q16) = 32 bytes
		channelI := make([][]int16, numChannels)
		channelQ := make([][]int16, numChannels)
		for ch := 0; ch < numChannels; ch++ {
			channelI[ch] = make([]int16, samplesNeeded)
			channelQ[ch] = make([]int16, samplesNeeded)
		}

		for s := 0; s < samplesNeeded; s++ {
			baseOffset := s * numChannels * bytesPerSample
			for ch := 0; ch < numChannels; ch++ {
				offset := baseOffset + ch*bytesPerSample
				if offset+4 <= len(buf) {
					channelI[ch][s] = int16(binary.LittleEndian.Uint16(buf[offset:]))
					channelQ[ch][s] = int16(binary.LittleEndian.Uint16(buf[offset+2:]))
				}
			}
		}

		// Build output binary message
		var outBuf []byte

		// Determine active channels
		activeChannels := make(map[int]bool)
		for _, chName := range channels {
			if len(chName) >= 2 {
				chIdx := int(chName[1] - '1')
				if chIdx >= 0 && chIdx < numChannels {
					activeChannels[chIdx] = true
				}
			}
		}

		// Send raw time-domain data (Client will do FFT if needed)
		// We send whatever we read (samplesNeeded), which is based on FFTSize
		if mode == "raw" || mode == "fft" || mode == "both" {
			for ch := 0; ch < numChannels; ch++ {
				if !activeChannels[ch] {
					continue
				}
				// I component (header 0-7 for I0-I7)
				iHeader := byte(ch * 2)
				outBuf = append(outBuf, iHeader)
				for s := 0; s < samplesNeeded && s < len(channelI[ch]); s++ {
					b := make([]byte, 2)
					binary.LittleEndian.PutUint16(b, uint16(channelI[ch][s]))
					outBuf = append(outBuf, b...)
				}

				// Q component (header 1, 3, 5... for Q0-Q7)
				qHeader := byte(ch*2 + 1)
				outBuf = append(outBuf, qHeader)
				for s := 0; s < samplesNeeded && s < len(channelQ[ch]); s++ {
					b := make([]byte, 2)
					binary.LittleEndian.PutUint16(b, uint16(channelQ[ch][s]))
					outBuf = append(outBuf, b...)
				}
			}
		}

		// Broadcast the frame
		if len(outBuf) > 0 {
			wsClientsMu.RLock()
			for client := range wsClients {
				select {
				case client.send <- outBuf:
				default:
					// If channel is full, drop the frame to avoid blocking loop
				}
			}
			wsClientsMu.RUnlock()
		}

		time.Sleep(frameInterval)
	}
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
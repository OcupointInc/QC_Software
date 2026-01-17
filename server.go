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
	"time"

	"github.com/dma/pkg/shm_ring"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

// WebSocket clients
var (
	wsClients          = make(map[*Client]bool)
	wsClientsMu        sync.RWMutex
	streamLoopRunning  = false
	shmProducerRunning = false
)

type Client struct {
	conn     *websocket.Conn
	send     chan interface{}
	channels []string
	mu       sync.Mutex
}

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

func runShmProducerLoop() {
	defer func() {
		shmProducerRunning = false
		log.Println("SHM Producer loop stopped")
	}()

	serverState.mu.RLock()
	devicePath := serverState.DevicePath
	shmName := serverState.SHMName
	serverState.mu.RUnlock()

	log.Printf("Starting Integrated SHM Producer: %s -> %s", devicePath, shmName)

	const sizeBytes = 8 * 1024 * 1024 * 1024
	const inputBlockSize = 32 // 8 channels * 4 bytes
	
	shm_ring.Remove(shmName)
	ring, err := shm_ring.Create(shmName, sizeBytes)
	if err != nil {
		log.Printf("Failed to create SHM ring: %v", err)
		return
	}
	defer ring.Close()

	fd, err := unix.Open(devicePath, unix.O_RDONLY, 0)
	if err != nil {
		log.Printf("Failed to open XDMA: %v", err)
		return
	}
	defer unix.Close(fd)

	// Use a blockSize that is a multiple of 4KB (and thus 32 bytes)
	const blockSize = 4 * 1024 * 1024 
	ringData := ring.Data()
	ringTotal := ring.Total()

	for {
		serverState.mu.RLock()
		isRecording := serverState.Recording
		useSHM := serverState.UseSHM
		serverState.mu.RUnlock()

		if isRecording && !useSHM {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		head := ring.GetHead()
		spaceToEnd := ringTotal - head
		
		readRequest := uint64(blockSize)
		if readRequest > spaceToEnd {
			// Read only what fits exactly at the end of the buffer
			// Since ringTotal and inputBlockSize are multiples of 32, 
			// spaceToEnd is guaranteed to be a multiple of 32.
			readRequest = spaceToEnd
		}

		n, err := unix.Read(fd, ringData[head : head+readRequest])
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Printf("SHM Producer read error: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if n > 0 {
			// XDMA usually returns full 4KB blocks, but may occasionally return
			// non-aligned amounts. We MUST only advance head by a multiple of 32
			// (inputBlockSize) to maintain channel alignment. Any leftover bytes
			// will be overwritten on the next read - this is safe because the
			// ring buffer is much larger than a single read.
			alignedBytes := (uint64(n) / inputBlockSize) * inputBlockSize
			if alignedBytes > 0 {
				ring.AdvanceHead(alignedBytes)
			}
		} else {
			time.Sleep(1 * time.Millisecond)
		}
	}
}

func runServer(port int, devicePath string, targetSize int, psuAddress string) {
	commandDevice := "/dev/xdma0_user"
	initHardwareController(commandDevice)

	if psuAddress != "" {
		if err := InitGlobalPSU(psuAddress); err != nil {
			log.Printf("Warning: Failed to initialize PSU: %v", err)
		}
	}

	if configData, err := os.ReadFile("config.json"); err == nil {
		var config HardwareConfig
		if err := json.Unmarshal(configData, &config); err == nil {
			if hwController != nil {
				hwController.ApplyConfig(&config)
			}
			if len(config.Channels) > 0 {
				serverState.mu.Lock()
				serverState.RecordingChannels = nil
				for _, ch := range config.Channels {
					if ch >= 1 && ch <= 8 {
						serverState.RecordingChannels = append(serverState.RecordingChannels, ch-1)
					}
				}
				serverState.mu.Unlock()
			}
		}
	}

	serverState.mu.Lock()
	serverState.DevicePath = devicePath
	serverState.mu.Unlock()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	templatesContent, _ := fs.Sub(templatesFS, "templates")
	staticContent, _ := fs.Sub(templatesFS, "static")
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl, _ := template.ParseFS(templatesContent, "*.html")
		tmpl.ExecuteTemplate(w, "index.html", nil)
	})

	http.HandleFunc("/api/rf/config", handleRFConfig)
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
	http.HandleFunc("/api/hardware/state", handleHardwareState)
	http.HandleFunc("/api/hardware/ddc/freq", handleDDCFreqUpdate)
	http.HandleFunc("/api/hardware/ddc/enable", handleDDCEnable)
	http.HandleFunc("/api/hardware/attenuation", handleAttenuationUpdate)
	http.HandleFunc("/api/hardware/filter", handleFilterSelect)
	http.HandleFunc("/api/hardware/calibration", handleCalibrationMode)
	http.HandleFunc("/api/hardware/system", handleSystemEnable)
	http.HandleFunc("/api/replay/files", handleReplayFiles)
	http.HandleFunc("/api/replay/select", handleReplaySelect)
	http.HandleFunc("/api/replay/state", handleReplayState)
	http.HandleFunc("/api/replay/toggle", handleReplayToggle)
	http.HandleFunc("/api/replay/upload", handleReplayUpload)
	http.HandleFunc("/api/replay/delete", handleReplayDelete)
	http.HandleFunc("/api/replay/clear", handleReplayClear)
	http.HandleFunc("/api/replay/seek", handleReplaySeek)
	http.HandleFunc("/api/record/start", handleRecordStart)
	http.HandleFunc("/api/record/stop", handleRecordStop)
	http.HandleFunc("/api/record/status", handleRecordStatus)

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil { return }

		client := &Client{conn: conn, send: make(chan interface{}, 256)}
		wsClientsMu.Lock()
		wsClients[client] = true
		shouldStart := !streamLoopRunning
		if shouldStart { streamLoopRunning = true }
		wsClientsMu.Unlock()

		if shouldStart { go runGlobalStreamLoop(devicePath) }

		serverState.mu.RLock()
		if serverState.UseSHM && !shmProducerRunning {
			shmProducerRunning = true
			go runShmProducerLoop()
		}
		serverState.mu.RUnlock()

		go client.writePump()
		defer func() {
			wsClientsMu.Lock()
			delete(wsClients, client)
			wsClientsMu.Unlock()
			close(client.send)
		}()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil { return }
			var config struct {
				Channels []string `json:"channels"`
				Mode     string   `json:"mode"`
				FPS      int      `json:"fps"`
				FFTSize  int      `json:"fft_size"`
				Type     string   `json:"type"`
				Enabled  *bool    `json:"enabled"`
			}
			if err := json.Unmarshal(msg, &config); err == nil {
				if len(config.Channels) > 0 {
					client.mu.Lock()
					client.channels = config.Channels
					client.mu.Unlock()
					serverState.mu.Lock()
					serverState.Channels = config.Channels
					serverState.mu.Unlock()
				}
				serverState.mu.Lock()
				if config.Type == "stream_control" && config.Enabled != nil {
					serverState.StreamingEnabled = *config.Enabled
				}
				if config.Mode != "" { serverState.StreamMode = config.Mode }
				if config.FPS > 0 { serverState.StreamFPS = config.FPS }
				if config.FFTSize > 0 { serverState.FFTSize = config.FFTSize }
				serverState.mu.Unlock()
			}
		}
	})

	log.Printf("Server listening on :%d", port)
	http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
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

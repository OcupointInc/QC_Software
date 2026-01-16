package main

import (
	"os"
	"sync"
)

// Server state
type ServerState struct {
	mu sync.RWMutex

	// RF Configuration
	DDCFreqMHz float64
	IBWMHZ     float64

	// Signal Generator
	SigGenFreqMHz  float64
	SigGenPowerDBm float64
	SigGenRFOutput bool

	// Sweep
	SweepRunning bool
	SweepParams  *SweepParams

	// Stream config from client
	StreamMode       string   // "raw", "fft", "both"
	StreamFPS        int      // frames per second
	FFTSize          int      // 1024, 2048, 4096, 8192
	FFTTypes         []string // "complex", "i", "q"
	Channels         []string // active channels like ["I0", "Q0", "I1", "Q1"]
	StreamingEnabled bool     // Controls if data is actually sent

	// Replay mode
	ReplayMode        bool
	ReplayData        []byte
	ReplayName        string
	ReplayOffset      int
	ForceReplayUpdate bool

	// Recording
	Recording          bool
	RecordingFile      string
		RecordingSamples   int // Total samples to record
		RecordingCurrent   int // Samples recorded so far
		RecordingFileHandle *os.File
	
		// System
			DevicePath string
		}
		
		// CaptureMetadata represents the metadata saved alongside a capture
		type CaptureMetadata struct {
			Timestamp   string          `json:"timestamp"`
			SampleRate  int             `json:"sample_rate"` // Always 250000000
			Config      *HardwareConfig `json:"config"`
		}
		
		type SweepParams struct {
			StartMHz float64 `json:"start_mhz"`
	StopMHz  float64 `json:"stop_mhz"`
	StepMHz  float64 `json:"step_mhz"`
	DwellMS  float64 `json:"dwell_ms"`
}

var serverState = &ServerState{
	DDCFreqMHz:     125.0,
	IBWMHZ:         250.0,
	SigGenFreqMHz:  100.0,
	SigGenPowerDBm: -10.0,
	SigGenRFOutput: false,
	StreamMode:     "fft",
	StreamFPS:      30,
	FFTSize:        1024,
	FFTTypes:       []string{"complex"},
	Channels:       []string{"I1", "Q1"},
}

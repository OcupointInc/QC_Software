package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/* static/*
var templatesFS embed.FS

// sizeFlag custom type to handle units like KB, MB, GB
type sizeFlag int

func (s *sizeFlag) String() string {
	return fmt.Sprintf("%d", *s)
}

func (s *sizeFlag) Set(value string) error {
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
		return fmt.Errorf("invalid size format: %s", value)
	}

	*s = sizeFlag(val * multiplier)
	return nil
}

func main() {
	// Common flags
	device := flag.String("d", "/dev/xdma0_c2h_0", "DMA device path")
	
	// Use custom size flag
	var size sizeFlag = 100 * 1024 * 1024 // Default 100MB
	flag.Var(&size, "s", "Capture size (e.g., 100MB, 1GB, 4096B)")

	// New flags for duration
	samples := flag.Int64("n", 0, "Number of samples to capture (overrides -s)")
	duration := flag.Duration("t", 0, "Duration to capture (e.g., 10s, 500ms) (overrides -n and -s)")

	// CLI-specific flags
	outputFile := flag.String("o", "capture.bin", "Output filename (CLI mode only)")
	configFile := flag.String("c", "", "Hardware configuration JSON file (CLI mode only)")
	channels := flag.String("channels", "1,2,3,4,5,6,7,8", "Comma-separated list of channels (1-8) to capture (CLI mode only)")

	// Server-specific flags
	isServer := flag.Bool("server", false, "Run in WebSocket server mode")
	port := flag.Int("p", 8080, "Port to listen on (Server mode only)")
	psuAddr := flag.String("psu", "TCPIP::192.168.1.200::inst0::INSTR", "PSU VISA address (Server mode only)")
	useSHM := flag.Bool("use-shm", false, "Use shared memory ring buffer for recording/streaming")
	shmName := flag.String("shm-name", "/xdma_ring", "SHM ring buffer name")

	// Simulation flags
	isSim := flag.Bool("sim", false, "Simulate XDMA hardware via named pipe")
	simPath := flag.String("sim-path", "/tmp/xdma_sim", "Path for simulation pipe")

	// PCIe reset flag
	resetPCIe := flag.Bool("r", false, "Reset PCIe device before starting")

	// Custom usage message
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "  CLI Mode:    go run . [options]")
		fmt.Fprintln(os.Stderr, "  Server Mode: go run . --server [options]")
		fmt.Fprintln(os.Stderr, "  Sim Mode:    go run . --sim [options]")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		flag.PrintDefaults()
	}

	flag.Parse()

	// Update global state with flags
	serverState.mu.Lock()
	serverState.DevicePath = *device
	serverState.UseSHM = *useSHM
	serverState.SHMName = *shmName
	serverState.mu.Unlock()

	// Reset PCIe device if requested
	if *resetPCIe {
		log.Println("Resetting PCIe device...")
		cmd := exec.Command("/bin/bash", "reset_pcie.sh")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatal("PCIe reset failed:", err)
		}
		log.Println("PCIe reset complete")
		time.Sleep(1 * time.Second)
	}

	// If simulation mode is on, override device path and start the background generator
	if *isSim {
		*device = *simPath
		go RunSimulator(*device)
		// Give the simulator a moment to initialize the pipe
		time.Sleep(200 * time.Millisecond)
	}

	targetSize := int(size)

	// Calculate target size based on precedence
	const bytesPerSample = 32
	const sampleRate = 244400000

	if *duration > 0 {
		totalSamples := int64((*duration).Seconds() * float64(sampleRate))
		targetSize = int(totalSamples * bytesPerSample)
		fmt.Printf("Duration %v -> %d samples -> %d bytes\n", *duration, totalSamples, targetSize)
	} else if *samples > 0 {
		targetSize = int(*samples * bytesPerSample)
		fmt.Printf("Samples %d -> %d bytes\n", *samples, targetSize)
	}

	if *isServer {
		runServer(*port, *device, targetSize, *psuAddr)
	} else {
		runCLI(*device, targetSize, *outputFile, *configFile, *channels)
	}
}
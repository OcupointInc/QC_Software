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

//go:embed templates/*
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

	// CLI-specific flags
	outputFile := flag.String("o", "capture.bin", "Output filename (CLI mode only)")
	configFile := flag.String("c", "", "Hardware configuration JSON file (CLI mode only)")

	// Server-specific flags
	isServer := flag.Bool("server", false, "Run in WebSocket server mode")
	port := flag.Int("p", 8080, "Port to listen on (Server mode only)")

	// Simulation flags
	isSim := flag.Bool("sim", false, "Simulate XDMA hardware via named pipe")

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
		*device = "/tmp/xdma_c2h0"
		go RunSimulator(*device)
		// Give the simulator a moment to initialize the pipe
		time.Sleep(200 * time.Millisecond)
	}

	targetSize := int(size)

	if *isServer {
		runServer(*port, *device, targetSize)
	} else {
		runCLI(*device, targetSize, *outputFile, *configFile)
	}
}
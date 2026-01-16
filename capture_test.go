package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dma/pkg/dma"
)

func TestCaptureWithSimulator(t *testing.T) {
	// Create a temporary directory for the named pipe
	tmpDir, err := os.MkdirTemp("", "dma_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Define pipe path
	pipePath := filepath.Join(tmpDir, "sim_pipe")

	// Start Simulator in background
	go func() {
		// RunSimulator blocks forever, so we run it in a goroutine
		RunSimulator(pipePath)
	}()

	// Give the simulator a moment to create and open the pipe
	time.Sleep(500 * time.Millisecond)

	// Define capture configuration
	// We want to capture a small amount of data, e.g., 1MB
	targetSize := 1 * 1024 * 1024
	cfg := dma.CaptureConfig{
		DevicePath: pipePath,
		TargetSize: targetSize,
	}

	// Run Capture
	fmt.Printf("Starting capture of %d bytes from %s...\n", targetSize, pipePath)
	result, err := dma.RunCapture(cfg)
	if err != nil {
		t.Fatalf("RunCapture failed: %v", err)
	}

	// Validation
	if result.BytesRead != targetSize {
		t.Errorf("Expected to read %d bytes, got %d", targetSize, result.BytesRead)
	}

	if len(result.Data) != targetSize {
		t.Errorf("Result data length mismatch. Expected %d, got %d", targetSize, len(result.Data))
	}

	if result.Duration <= 0 {
		t.Errorf("Duration should be positive, got %v", result.Duration)
	}

	if result.Throughput <= 0 {
		t.Errorf("Throughput should be positive, got %f MB/s", result.Throughput)
	}

	fmt.Printf("--- Test Results ---\n")
	fmt.Printf("Bytes Read: %d\n", result.BytesRead)
	fmt.Printf("Duration:   %v\n", result.Duration)
	fmt.Printf("Throughput: %.2f MB/s\n", result.Throughput)
	fmt.Printf("Aligned:    %v\n", result.Aligned)
}

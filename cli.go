package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/dma/pkg/dma"
)

// runCLI executes the one-shot capture and file save
func runCLI(devicePath string, targetSize int, outputFilename string, configFile string) {
	fmt.Println("--- DMA Capture Session Start ---")

	// Apply hardware configuration if provided
	if configFile != "" {
		fmt.Printf(">>> Loading config from %s\n", configFile)
		data, err := os.ReadFile(configFile)
		if err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}

		var config HardwareConfig
		if err := json.Unmarshal(data, &config); err != nil {
			log.Fatalf("Failed to parse config file: %v", err)
		}

		// Initialize controller
		commandDevice := "/dev/xdma0_user"
		initHardwareController(commandDevice)

		fmt.Println(">>> Applying Hardware Configuration...")
		if err := hwController.ApplyConfig(&config); err != nil {
			log.Printf("Warning: Error applying config: %v", err)
		} else {
			fmt.Println("    Configuration applied successfully.")
		}
	}

	fmt.Printf("Device: %s | Target: %d bytes\n", devicePath, targetSize)
	fmt.Println(">>> CAPTURING...")

	cfg := dma.CaptureConfig{
		DevicePath: devicePath,
		TargetSize: targetSize,
	}

	result, err := dma.RunCapture(cfg)
	if err != nil {
		log.Fatalf("Capture failed: %v", err)
	}

	if result.Aligned {
		// fmt.Printf(">>> Aligning data: Shifting Ch0-7 up.\n")
		// fmt.Printf("    Original Bytes: %d, New Bytes: %d\n", result.BytesRead, len(result.Data))
	} else {
		log.Printf("Warning: Alignment not needed or insufficient data. Captured %d bytes", result.BytesRead)
	}

	fmt.Println("--- Results ---")
	fmt.Printf("Total Read:     %d bytes\n", result.BytesRead)
	fmt.Printf("Throughput:     %.2f MB/s\n", result.Throughput)
	fmt.Printf("Duration:       %v\n", result.Duration)

	fmt.Printf(">>> SAVING TO FILE: %s ... ", outputFilename)
	saveStart := time.Now()
	
	if err := os.WriteFile(outputFilename, result.Data, 0644); err != nil {
		fmt.Printf("\nError saving file: %v\n", err)
	} else {
		elapsed := time.Since(saveStart)
		mb := float64(result.BytesRead) / (1024 * 1024)
		throughput := mb / elapsed.Seconds()
		fmt.Printf("DONE\n")
		fmt.Printf("Save Duration:   %v\n", elapsed)
		fmt.Printf("Save Throughput: %.2f MB/s\n", throughput)
	}
}

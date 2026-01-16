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

	fmt.Printf(">>> SAVING TO PARQUET FILE: %s ... ", outputFilename)
	saveStart := time.Now()
	
	f, err := os.Create(outputFilename)
	if err != nil {
		fmt.Printf("\nError creating file: %v\n", err)
		return
	}
	defer f.Close()

	// Use the loaded config if available, otherwise nil
	var cfgPtr *HardwareConfig
	if configFile != "" {
		// Re-read or just reuse if we had a struct to pass around. 
		// Since we didn't save the struct in a variable accessible here easily without refactor, re-reading is safest/easiest or parse again.
		// Actually I can just move the parse logic up or capture it.
		// Let's just re-read for simplicity or assume we can pass it if we refactored runCLI signature.
		// Refactoring signature is better but I'll parse again for minimal diff.
		data, _ := os.ReadFile(configFile)
		var c HardwareConfig
		json.Unmarshal(data, &c)
		cfgPtr = &c
	}

	pw := NewParquetWriter(f, cfgPtr)
	if _, err := WriteRawBuffer(pw, result.Data); err != nil {
		fmt.Printf("\nError writing parquet data: %v\n", err)
	}
	
	if err := pw.Close(); err != nil {
		fmt.Printf("\nError closing parquet writer: %v\n", err)
	} else {
		elapsed := time.Since(saveStart)
		mb := float64(result.BytesRead) / (1024 * 1024)
		throughput := mb / elapsed.Seconds()
		
		// Get final file stats
		fi, err := os.Stat(outputFilename)
		var compressedSize int64
		if err == nil {
			compressedSize = fi.Size()
		}

		fmt.Printf("DONE\n")
		fmt.Printf("Save Duration:   %v\n", elapsed)
		fmt.Printf("Save Throughput: %.2f MB/s\n", throughput)
		
		if compressedSize > 0 {
			origSize := float64(result.BytesRead)
			compSize := float64(compressedSize)
			reduction := (1.0 - (compSize / origSize)) * 100.0
			
			fmt.Printf("Final Size:      %.2f MB\n", compSize/(1024*1024))
			fmt.Printf("Compression:     %.2f%%\n", reduction)
		}
	}
}

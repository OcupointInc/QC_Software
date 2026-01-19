package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dma/pkg/dma"
)

// runCLI executes the one-shot capture and file save
func runCLI(devicePath string, targetSize int, outputFilename string, configFile string, channels string, benchMode bool) {
	fmt.Println("--- DMA Capture Session Start ---")

	// Parse channels
	activeChannelIndices := []int{}
	activeMask := [8]bool{}
	parts := strings.Split(channels, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if chIdx, err := strconv.Atoi(p); err == nil && chIdx >= 1 && chIdx <= 8 {
			activeChannelIndices = append(activeChannelIndices, chIdx-1)
			activeMask[chIdx-1] = true
		}
	}
	if len(activeChannelIndices) == 0 {
		log.Fatal("Error: No valid channels selected")
	}

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

	fmt.Printf("Device: %s | Target: %d bytes | Channels: %v\n", devicePath, targetSize, activeChannelIndices)
	if benchMode {
		fmt.Println(">>> BENCHMARK MODE ACTIVE (Looping) <<<")
	}

	for {
		fmt.Println(">>> CAPTURING...")

		cfg := dma.CaptureConfig{
			DevicePath:  devicePath,
			TargetSize:  targetSize,
			ChannelMask: activeMask,
		}

		result, err := dma.RunCapture(cfg)
		if err != nil {
			log.Fatalf("Capture failed: %v", err)
		}

		// Aligned flag is false for filtered captures in dma.RunCapture
		if result.Aligned {
			// ...
		}

		fmt.Println("--- Results ---")
		fmt.Printf("Total Read:     %d bytes\n", result.BytesRead)
		fmt.Printf("Throughput:     %.2f MB/s\n", result.Throughput)
		fmt.Printf("Duration:       %v\n", result.Duration)

		if outputFilename != "" {
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

				// Save Metadata
				metaFilename := strings.TrimSuffix(outputFilename, ".bin") 
				if metaFilename == outputFilename {
					metaFilename += ".json"
				} else {
					metaFilename += ".json"
				}
				
				var currentConfig *HardwareConfig
				if hwController != nil {
					currentConfig = hwController.GetConfig()
				}

				// Convert internal 0-7 indices to user-facing 1-8 for metadata
				outputChannels := make([]int, len(activeChannelIndices))
				for i, ch := range activeChannelIndices {
					outputChannels[i] = ch + 1
				}

				metadata := CaptureMetadata{
					Timestamp:  time.Now().Format(time.RFC3339),
					SampleRate: 244400000,
					Channels:   outputChannels,
					Config:     currentConfig,
				}

				if metaBytes, err := json.MarshalIndent(metadata, "", "  "); err == nil {
					os.WriteFile(metaFilename, metaBytes, 0644)
					fmt.Printf("Metadata saved to: %s\n", metaFilename)
				}
			}
		} else {
			fmt.Println(">>> Skipping save (RAM only)")
		}

		if !benchMode {
			break
		}
		
		// Small pause between runs
		time.Sleep(500 * time.Millisecond)
		fmt.Println("\n--- Restarting Capture (Benchmark) ---")
	}
}

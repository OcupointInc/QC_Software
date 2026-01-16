package main

import (
	"fmt"
	"log"
	"os"

	"github.com/dma/pkg/dma"
)

// runCLI executes the one-shot capture and file save
func runCLI(devicePath string, targetSize int, outputFilename string) {
	fmt.Println("--- DMA Capture Session Start ---")
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
		fmt.Printf(">>> Aligning data: Shifting Ch0-7 up.\n")
		fmt.Printf("    Original Bytes: %d, New Bytes: %d\n", result.BytesRead, len(result.Data))
	} else {
		log.Printf("Warning: Alignment not needed or insufficient data. Captured %d bytes", result.BytesRead)
	}

	fmt.Println("--- Results ---")
	fmt.Printf("Total Read:     %d bytes\n", result.BytesRead)
	fmt.Printf("Throughput:     %.2f MB/s\n", result.Throughput)
	fmt.Printf("Duration:       %v\n", result.Duration)

	fmt.Printf(">>> SAVING TO FILE: %s ... ", outputFilename)
	err = os.WriteFile(outputFilename, result.Data, 0644)
	if err != nil {
		fmt.Printf("\nError saving file: %v\n", err)
	} else {
		fmt.Println("DONE")
	}
}

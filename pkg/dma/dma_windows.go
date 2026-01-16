//go:build windows

package dma

import (
	"fmt"
)

// RunCapture performs the read from the device and handles the specific alignment logic.
func RunCapture(cfg CaptureConfig) (*CaptureResult, error) {
	return nil, fmt.Errorf("DMA capture not supported on Windows")
}

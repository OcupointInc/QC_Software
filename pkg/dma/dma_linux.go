//go:build linux

package dma

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// RunCapture performs the read from the device and handles the specific alignment logic.
func RunCapture(cfg CaptureConfig) (*CaptureResult, error) {
	fd, err := unix.Open(cfg.DevicePath, unix.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("could not open device %s: %v", cfg.DevicePath, err)
	}
	defer unix.Close(fd)

	// Increase pipe buffer size to maximum (1MB on Linux) for better throughput
	const maxPipeSize = 1024 * 1024
	_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETPIPE_SZ, maxPipeSize)

	data := make([]byte, cfg.TargetSize)

	// Pre-fault all pages to avoid page faults during the timed read
	for i := 0; i < len(data); i += 4096 {
		data[i] = 0
	}

	startTime := time.Now()

	totalRead := 0
	// Read in large chunks to minimize syscall overhead
	// Use 4MB or remaining size, whichever is smaller
	const chunkSize = 4 * 1024 * 1024
	for totalRead < len(data) {
		remaining := len(data) - totalRead
		readSize := remaining
		if readSize > chunkSize {
			readSize = chunkSize
		}
		n, err := unix.Read(fd, data[totalRead:totalRead+readSize])
		if n > 0 {
			totalRead += n
		}
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return nil, fmt.Errorf("read failed after %d bytes: %v", totalRead, err)
		}
		if n == 0 {
			break // EOF
		}
	}
	n := totalRead
	elapsed := time.Since(startTime)

	// Truncate to actual read size
	data = data[:n]

	// Post-processing: Alignment
	processedData, aligned := alignData(data)
	
	mbRead := float64(n) / (1024 * 1024)
	mbps := 0.0
	if elapsed.Seconds() > 0 {
		mbps = mbRead / elapsed.Seconds()
	}

	return &CaptureResult{
		Data:       processedData,
		Duration:   elapsed,
		Throughput: mbps,
		BytesRead:  n,
		Aligned:    aligned,
	}, nil
}

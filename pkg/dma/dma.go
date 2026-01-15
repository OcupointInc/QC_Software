package dma

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// CaptureConfig holds configuration for the DMA capture
type CaptureConfig struct {
	DevicePath string
	TargetSize int
}

// CaptureResult holds the data and stats from a capture
type CaptureResult struct {
	Data       []byte
	Duration   time.Duration
	Throughput float64 // MB/s
	BytesRead  int
	Aligned    bool
}

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

// alignData performs the specific channel shifting logic
func alignData(data []byte) ([]byte, bool) {
	n := len(data)
	const shiftSamples = 97992
	const bytesPerFrame = 32
	const bytesHalfFrame = 16 // 8 channels * 2 bytes

	totalSamples := n / bytesPerFrame
	if totalSamples <= shiftSamples {
		return data, false
	}

	newSamples := totalSamples - shiftSamples
	newData := make([]byte, newSamples*bytesPerFrame)

	for i := 0; i < newSamples; i++ {
		dstBase := i * bytesPerFrame
		
		// Ch 0-7 (First 16 bytes) come from later in the buffer (shifted "up" -> earlier in output)
		srcBaseCh0_7 := (i + shiftSamples) * bytesPerFrame
		
		// Ch 8-15 (Next 16 bytes) come from current position
		srcBaseCh8_15 := i * bytesPerFrame

		copy(newData[dstBase:dstBase+bytesHalfFrame], data[srcBaseCh0_7:srcBaseCh0_7+bytesHalfFrame])
		copy(newData[dstBase+bytesHalfFrame:dstBase+bytesPerFrame], data[srcBaseCh8_15+bytesHalfFrame:srcBaseCh8_15+bytesPerFrame])
	}

	return newData, true
}

package dma

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// CaptureConfig holds configuration for the DMA capture
type CaptureConfig struct {
	DevicePath  string
	TargetSize  int
	ChannelMask [8]bool // Active channels (true = record)
}

// CaptureResult holds the data and stats from a capture
type CaptureResult struct {
	Data       []byte
	Duration   time.Duration
	Throughput float64 // MB/s
	BytesRead  int
	Aligned    bool
}

// RunCapture performs the read from the device and filters active channels
// Uses two-phase approach: fast capture into RAM, then filter afterwards
func RunCapture(cfg CaptureConfig) (*CaptureResult, error) {
	fd, err := unix.Open(cfg.DevicePath, unix.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("could not open device %s: %v", cfg.DevicePath, err)
	}
	defer unix.Close(fd)

	// Increase pipe buffer size
	const maxPipeSize = 1024 * 1024
	_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETPIPE_SZ, maxPipeSize)

	// Count active channels
	activeCount := 0
	for _, active := range cfg.ChannelMask {
		if active {
			activeCount++
		}
	}
	if activeCount == 0 {
		return nil, fmt.Errorf("no active channels selected")
	}

	const bytesPerFrame = 32 // 8 channels * 4 bytes each

	// Calculate how many input bytes we need to read
	// TargetSize is the desired output size
	totalSamples := cfg.TargetSize / (activeCount * 4)
	inputReadSize := totalSamples * bytesPerFrame

	// PHASE 1: Fast capture into RAM (all channels, no processing)
	data := make([]byte, inputReadSize)

	// Pre-fault pages to avoid page faults during timed read
	for i := 0; i < len(data); i += 4096 {
		data[i] = 0
	}

	startTime := time.Now()

	totalRead := 0
	const chunkSize = 4 * 1024 * 1024 // 4MB chunks
	for totalRead < inputReadSize {
		remaining := inputReadSize - totalRead
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

	captureElapsed := time.Since(startTime)

	// Truncate to actual read size (aligned to frame boundary)
	totalRead = (totalRead / bytesPerFrame) * bytesPerFrame
	data = data[:totalRead]

	// PHASE 2: Filter channels (post-processing)
	var outputData []byte

	if activeCount == 8 {
		// All channels selected - no filtering needed, use data directly
		outputData = data
	} else {
		// Filter to selected channels only
		totalFrames := totalRead / bytesPerFrame
		outputSize := totalFrames * activeCount * 4
		outputData = make([]byte, outputSize)

		// Pre-calculate copy offsets
		type copyOp struct {
			srcOffset int
		}
		ops := make([]copyOp, 0, activeCount)
		for i := 0; i < 8; i++ {
			if cfg.ChannelMask[i] {
				ops = append(ops, copyOp{srcOffset: i * 4})
			}
		}

		// Fast filtering loop
		wIdx := 0
		for f := 0; f < totalFrames; f++ {
			baseSrc := f * bytesPerFrame
			for _, op := range ops {
				src := baseSrc + op.srcOffset
				outputData[wIdx] = data[src]
				outputData[wIdx+1] = data[src+1]
				outputData[wIdx+2] = data[src+2]
				outputData[wIdx+3] = data[src+3]
				wIdx += 4
			}
		}
	}

	// Calculate throughput based on capture speed (not including filtering)
	mbRead := float64(totalRead) / (1024 * 1024)
	mbps := 0.0
	if captureElapsed.Seconds() > 0 {
		mbps = mbRead / captureElapsed.Seconds()
	}

	return &CaptureResult{
		Data:       outputData,
		Duration:   captureElapsed,
		Throughput: mbps,
		BytesRead:  len(outputData),
		Aligned:    false,
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
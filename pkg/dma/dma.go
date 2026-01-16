package dma

import (
	"time"
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
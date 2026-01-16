//go:build linux

package main

import (
	"log"
	"time"

	"golang.org/x/sys/unix"
)

func performRecording() {
	// 1. Wait for the global loop to release device
	// Increased wait time to ensure exclusive access
	time.Sleep(1 * time.Second)

	serverState.mu.RLock()
	devicePath := serverState.DevicePath
	samplesTotal := serverState.RecordingSamples
	recChannels := serverState.RecordingChannels
	serverState.mu.RUnlock()

	if devicePath == "" {
		log.Println("Error: Device path not set, defaulting to /dev/xdma0_c2h_0")
		devicePath = "/dev/xdma0_c2h_0"
	}

	// 2. Open Device
	log.Printf("Opening device %s for recording...", devicePath)
	fd, err := unix.Open(devicePath, unix.O_RDONLY, 0)
	if err != nil {
		log.Printf("Failed to open device for recording: %v", err)
		cleanupRecording(err.Error())
		return
	}
	defer unix.Close(fd)

	// Optimize pipe
	const maxPipeSize = 1024 * 1024
	_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETPIPE_SZ, maxPipeSize)

	const numChannels = 8
	const bytesPerSample = 4 // 2 byte I + 2 byte Q
	const inputBlockSize = numChannels * bytesPerSample // 32 bytes

	const readChunkSize = 4 * 1024 * 1024 // 4MB chunks

	// Ensure read buffer is multiple of input block size
	bufSize := (readChunkSize / inputBlockSize) * inputBlockSize
	buf := make([]byte, bufSize)

	// Allocate RAM buffer for entire capture (all channels)
	totalBytes := samplesTotal * inputBlockSize
	captureData := make([]byte, 0, totalBytes)

	log.Printf("Capturing %d samples (%d MB) into RAM...", samplesTotal, totalBytes/(1024*1024))

	samplesRecorded := 0
	lastBroadcast := 0
	captureStart := time.Now()

	// PHASE 1: Fast capture into RAM (all channels, no filtering)
	for samplesRecorded < samplesTotal {
		// Check if stopped externally
		serverState.mu.RLock()
		if !serverState.Recording || serverState.RecordingFileHandle == nil {
			serverState.mu.RUnlock()
			break
		}
		serverState.mu.RUnlock()

		// Read from device
		n, err := unix.Read(fd, buf)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Printf("Recording read error: %v", err)
			cleanupRecording(err.Error())
			return
		}
		if n == 0 {
			time.Sleep(1 * time.Millisecond)
			continue
		}

		// Determine valid frames
		frames := n / inputBlockSize
		validBytes := frames * inputBlockSize

		// Append directly to capture buffer (all channels)
		captureData = append(captureData, buf[:validBytes]...)

		// Update stats - we count "time samples", so frames
		samplesRecorded += frames

		serverState.mu.Lock()
		serverState.RecordingCurrent = samplesRecorded
		serverState.mu.Unlock()

		// Broadcast progress every 100k samples
		if samplesRecorded - lastBroadcast > 100000 {
			go broadcastJSON(map[string]interface{}{
				"type":    "recording_progress",
				"current": samplesRecorded,
				"total":   samplesTotal,
			})
			lastBroadcast = samplesRecorded
		}
	}

	captureDuration := time.Since(captureStart)
	log.Printf("Capture complete in %v. Processing and writing to file...", captureDuration)

	// PHASE 2: Filter channels and write to file
	serverState.mu.RLock()
	if !serverState.Recording || serverState.RecordingFileHandle == nil {
		serverState.mu.RUnlock()
		return
	}
	f := serverState.RecordingFileHandle
	serverState.mu.RUnlock()

	// Determine active channels for filtering
	activeMask := [numChannels]bool{}
	activeCount := 0

	for _, idx := range recChannels {
		if idx >= 0 && idx < numChannels {
			if !activeMask[idx] {
				activeMask[idx] = true
				activeCount++
			}
		}
	}

	// If no channels specified, default to all (safety)
	if activeCount == 0 {
		for i := 0; i < numChannels; i++ {
			activeMask[i] = true
		}
		activeCount = numChannels
	}

	log.Printf("Filtering to %d channels and writing to file...", activeCount)

	// Pre-calculate offsets to copy
	type copyOp struct {
		srcOffset int
		dstOffset int
	}
	ops := make([]copyOp, 0, activeCount)
	dstOff := 0
	for i := 0; i < numChannels; i++ {
		if activeMask[i] {
			ops = append(ops, copyOp{srcOffset: i * bytesPerSample, dstOffset: dstOff})
			dstOff += bytesPerSample
		}
	}
	outputBlockSize := activeCount * bytesPerSample

	// If all channels are active, just write directly
	if activeCount == numChannels {
		writeStart := time.Now()
		if _, err := f.Write(captureData); err != nil {
			log.Printf("Recording write error: %v", err)
			cleanupRecording(err.Error())
			return
		}
		writeDuration := time.Since(writeStart)
		log.Printf("Write complete in %v (no filtering needed)", writeDuration)
	} else {
		// Filter and write
		totalFrames := len(captureData) / inputBlockSize
		filteredData := make([]byte, totalFrames * outputBlockSize)

		writeStart := time.Now()

		// Filter the data
		wIdx := 0
		for fIdx := 0; fIdx < totalFrames; fIdx++ {
			baseSrc := fIdx * inputBlockSize
			for _, op := range ops {
				src := baseSrc + op.srcOffset
				filteredData[wIdx] = captureData[src]
				filteredData[wIdx+1] = captureData[src+1]
				filteredData[wIdx+2] = captureData[src+2]
				filteredData[wIdx+3] = captureData[src+3]
				wIdx += 4
			}
		}

		// Write filtered data to file
		if _, err := f.Write(filteredData); err != nil {
			log.Printf("Recording write error: %v", err)
			cleanupRecording(err.Error())
			return
		}

		writeDuration := time.Since(writeStart)
		log.Printf("Filter and write complete in %v", writeDuration)
	}

	log.Printf("Recording finished. Total samples: %d", samplesRecorded)
	cleanupRecording("")
}

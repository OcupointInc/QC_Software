//go:build linux

package main

import (
	"log"
	"time"

	"golang.org/x/sys/unix"
)

func performRecording() {
	// 1. Wait a bit for the global loop to release device
	time.Sleep(200 * time.Millisecond)

	serverState.mu.RLock()
	devicePath := serverState.DevicePath
	samplesTotal := serverState.RecordingSamples
	// f is accessed via serverState to allow handleRecordStop to close it if needed,
	// but we should probably grab a local ref or check serverState repeatedly.
	// For simplicity, we check serverState in the loop.
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
	const blockSize = 4 * 1024 * 1024 // 4MB chunks

	buf := make([]byte, blockSize)
	samplesRecorded := 0
	lastBroadcast := 0

	for samplesRecorded < samplesTotal {
		// Check if stopped externally
		serverState.mu.RLock()
		if !serverState.Recording || serverState.RecordingFileHandle == nil {
			serverState.mu.RUnlock()
			break
		}
		f := serverState.RecordingFileHandle
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

		// Write to file
		if _, err := f.Write(buf[:n]); err != nil {
			log.Printf("Recording write error: %v", err)
			cleanupRecording(err.Error())
			return
		}

		// Update stats
		samplesInChunk := n / (numChannels * bytesPerSample)
		samplesRecorded += samplesInChunk

		serverState.mu.Lock()
		serverState.RecordingCurrent = samplesRecorded
		serverState.mu.Unlock()

		// Broadcast progress every 1 million samples or so
		if samplesRecorded - lastBroadcast > 100000 {
			go broadcastJSON(map[string]interface{}{
				"type":    "recording_progress",
				"current": samplesRecorded,
				"total":   samplesTotal,
			})
			lastBroadcast = samplesRecorded
		}
	}

	log.Printf("Recording finished. Total samples: %d", samplesRecorded)
	cleanupRecording("")
}

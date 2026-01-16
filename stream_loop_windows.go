//go:build windows

package main

import (
	"encoding/binary"
	"log"
	"time"
)

// runGlobalStreamLoop continuously reads from replay buffer (if active) and broadcasts to all clients
func runGlobalStreamLoop(devicePath string) {
	defer func() {
		wsClientsMu.Lock()
		streamLoopRunning = false
		wsClientsMu.Unlock()
		log.Println("Global stream loop stopped")
	}()

	// No physical device support on Windows in this loop
	// We only support Replay Mode here.

	const numChannels = 8
	const bytesPerSample = 4 // 2 bytes I + 2 bytes Q per channel
	const sampleSize = 1024  // samples for time domain display

	frameCounter := 0

	for {
		// Check if we should exit (no clients)
		wsClientsMu.Lock()
		if len(wsClients) == 0 {
			wsClientsMu.Unlock()
			return
		}
		wsClientsMu.Unlock()

		serverState.mu.RLock()
		fps := serverState.StreamFPS
		fftSize := serverState.FFTSize
		mode := serverState.StreamMode
		channels := serverState.Channels
		replayMode := serverState.ReplayMode
		replayData := serverState.ReplayData
		//streamingEnabled := serverState.StreamingEnabled
		forceReplayUpdate := serverState.ForceReplayUpdate
		isRecording := serverState.Recording
		serverState.mu.RUnlock()

		if fps <= 0 {
			fps = 30
		}
		frameInterval := time.Second / time.Duration(fps)

		// If Recording is active, we yield (though recording isn't supported on Windows either, but for consistency)
		if isRecording {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// On Windows, if we are NOT replaying, we can't do anything (no live stream)
		if !replayMode && !forceReplayUpdate {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Calculate how much data we need
		samplesNeeded := fftSize
		if samplesNeeded < sampleSize {
			samplesNeeded = sampleSize
		}
		bytesNeeded := samplesNeeded * numChannels * bytesPerSample

		buf := make([]byte, bytesNeeded)

		// Replay Logic
		if (replayMode || forceReplayUpdate) && len(replayData) > 0 {
			serverState.mu.Lock()
			// Reset force flag if it was set
			if serverState.ForceReplayUpdate {
				serverState.ForceReplayUpdate = false
			}
			offset := serverState.ReplayOffset

			// Send progress update occasionally
			frameCounter++
			if frameCounter%10 == 0 {
				totalSize := len(replayData)
				progress := float64(offset) / float64(totalSize)
				go broadcastJSON(map[string]interface{}{
					"type":     "replay_progress",
					"progress": progress,
					"offset":   offset,
					"total":    totalSize,
				})
			}

			for i := 0; i < bytesNeeded; i++ {
				buf[i] = replayData[offset]
				offset = (offset + 1) % len(replayData)
			}
			serverState.ReplayOffset = offset
			serverState.mu.Unlock()
		} else {
			// Should not happen due to check above
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Parse into channel data
		// Data format: for each sample, 8 channels * (I16 + Q16) = 32 bytes
		channelI := make([][]int16, numChannels)
		channelQ := make([][]int16, numChannels)
		for ch := 0; ch < numChannels; ch++ {
			channelI[ch] = make([]int16, samplesNeeded)
			channelQ[ch] = make([]int16, samplesNeeded)
		}

		for s := 0; s < samplesNeeded; s++ {
			baseOffset := s * numChannels * bytesPerSample
			for ch := 0; ch < numChannels; ch++ {
				offset := baseOffset + ch*bytesPerSample
				if offset+4 <= len(buf) {
					channelI[ch][s] = int16(binary.LittleEndian.Uint16(buf[offset:]))
					channelQ[ch][s] = int16(binary.LittleEndian.Uint16(buf[offset+2:]))
				}
			}
		}

		// Build output binary message
		var outBuf []byte

		// Determine active channels
		activeChannels := make(map[int]bool)
		for _, chName := range channels {
			if len(chName) >= 2 {
				chIdx := int(chName[1] - '1')
				if chIdx >= 0 && chIdx < numChannels {
					activeChannels[chIdx] = true
				}
			}
		}

		// Send raw time-domain data (Client will do FFT if needed)
		// We send whatever we read (samplesNeeded), which is based on FFTSize
		if mode == "raw" || mode == "fft" || mode == "both" {
			for ch := 0; ch < numChannels; ch++ {
				if !activeChannels[ch] {
					continue
				}
				// I component (header 0-7 for I0-I7)
				iHeader := byte(ch * 2)
				outBuf = append(outBuf, iHeader)
				for s := 0; s < samplesNeeded && s < len(channelI[ch]); s++ {
					b := make([]byte, 2)
					binary.LittleEndian.PutUint16(b, uint16(channelI[ch][s]))
					outBuf = append(outBuf, b...)
				}

				// Q component (header 1, 3, 5... for Q0-Q7)
				qHeader := byte(ch*2 + 1)
				outBuf = append(outBuf, qHeader)
				for s := 0; s < samplesNeeded && s < len(channelQ[ch]); s++ {
					b := make([]byte, 2)
					binary.LittleEndian.PutUint16(b, uint16(channelQ[ch][s]))
					outBuf = append(outBuf, b...)
				}
			}
		}

		// Broadcast the frame
		if len(outBuf) > 0 {
			wsClientsMu.RLock()
			for client := range wsClients {
				select {
				case client.send <- outBuf:
				default:
					// If channel is full, drop the frame to avoid blocking loop
				}
			}
			wsClientsMu.RUnlock()
		}

		time.Sleep(frameInterval)
	}
}

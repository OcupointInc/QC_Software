//go:build linux

package main

import (
	"encoding/binary"
	"log"
	"math"
	"math/rand"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func RunSimulator(devicePath string) {
	_ = os.Remove(devicePath)
	if err := syscall.Mkfifo(devicePath, 0666); err != nil {
		log.Fatalf("[SIM] Error: %v", err)
	}

	log.Printf("[SIM] Streaming 12-bit LSB aligned data to: %s", devicePath)

	fd, err := unix.Open(devicePath, unix.O_WRONLY, 0)
	if err != nil {
		log.Fatal(err)
	}
	defer unix.Close(fd)

	// Tune buffer for throughput
	const maxPipeSize = 1024 * 1024
	_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETPIPE_SZ, maxPipeSize)

	const (
		numChannels     = 8
		samplesPerWrite = 8192
		sampleRate      = 2445e5
		targetFreq      = 26e6 
		amplitude       = 2040.0
	)

	writeBuf := make([]byte, samplesPerWrite*numChannels*4)

	// --- OPTIMIZATION: Use Integer Phase Accumulator (DDS) ---
	// This avoids "phase -= 2*Pi" drift and float modulo operations.
	// We map the full circle (2*Pi) to the range of a uint32 [0, 2^32).
	
	var phaseAcc uint32
	// Tuning Word = (Target / SampleRate) * 2^32
	tFreq := float64(targetFreq)
    sRate := float64(sampleRate)
    
    // Tuning Word = (Target / SampleRate) * 2^32
    tuningWord := uint32((tFreq / sRate) * 4294967296.0)

	// Pre-calculate channel phase offsets in integer space
	// 2^32 / 16 represents Pi/8 (since 2^32 is 2*Pi)
	chanOffsets := make([]uint32, numChannels)
	for c := 0; c < numChannels; c++ {
		// Offset = c * (Pi/8)
		// Map Pi/8 to int: (2^32) / 16
		chanOffsets[c] = uint32(c) * (4294967296 / 16)
	}

	// Create a fast local random source for dithering (global rand is slow due to locks)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		for s := 0; s < samplesPerWrite; s++ {
			for c := 0; c < numChannels; c++ {
				
				// 1. Calculate Phase
				// Add the channel offset to the current time accumulator
				currentPhaseInt := phaseAcc + chanOffsets[c]
				
				// Convert back to Radians for math.Cos/Sin
				// angle = (int_phase / 2^32) * 2*Pi
				rads := float64(currentPhaseInt) * (2.0 * math.Pi / 4294967296.0)

				// 2. Generate Signal
				valI := amplitude * math.Cos(rads)
				valQ := amplitude * math.Sin(rads)

				// 3. APPLY DITHER (The Fix)
				// We add triangular dither (+/- 1 LSB) to randomize quantization error.
				// This turns harmonic spurs into a flat noise floor.
				ditherI := rng.Float64() - rng.Float64()
				ditherQ := rng.Float64() - rng.Float64()

				valI += ditherI
				valQ += ditherQ

				// 4. Clamp and Cast
				if valI > 2047 { valI = 2047 }
				if valI < -2048 { valI = -2048 }
				if valQ > 2047 { valQ = 2047 }
				if valQ < -2048 { valQ = -2048 }

				iVal := int16(valI)
				qVal := int16(valQ)

				idx := (s*numChannels + c) * 4
				binary.LittleEndian.PutUint16(writeBuf[idx:], uint16(iVal))
				binary.LittleEndian.PutUint16(writeBuf[idx+2:], uint16(qVal))
			}
			
			// Increment time phase
			phaseAcc += tuningWord
		}

		if _, err := unix.Write(fd, writeBuf); err != nil {
			log.Println("[SIM] Pipe closed, restarting...")
			unix.Close(fd)
			for {
				fd, err = unix.Open(devicePath, unix.O_WRONLY, 0)
				if err == nil { break }
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

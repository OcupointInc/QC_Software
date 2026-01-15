package main

import (
	"encoding/binary"
	"log"
	"math"
	"os"
	"syscall"
	"time"
	"math/rand"
	"golang.org/x/sys/unix"
)

// RunSimulator creates a named pipe at devicePath and streams I/Q data.
// If dataFile is empty, it generates sine wave patterns.
// If dataFile is specified, it reads that .bin file and streams it in a loop.
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
        sampleRate      = 250e6
        targetFreq      = 5.0e6
        
        // CORRECTION: 12-bit Signed Max is 2047.
        // We use slightly less to prevent clipping with dither.
        amplitude       = 2040.0 
    )

    writeBuf := make([]byte, samplesPerWrite*numChannels*4)
    var phase float64
    phaseStep := 2.0 * math.Pi * targetFreq / sampleRate

    for {
        for s := 0; s < samplesPerWrite; s++ {
            for c := 0; c < numChannels; c++ {
                chPhase := phase + (float64(c) * (math.Pi / 8))

                // Dither is still necessary!
                // Even at 12-bit, quantization spurs appear without it.
                // We add +/- 0.5 bit of random noise.
                dither := rand.Float64() - 0.5
                
                // Calculate signal
                val := (amplitude * math.Cos(chPhase)) + dither
                
                // Clamp to 12-bit signed range (-2048 to 2047) to be safe
                if val > 2047 { val = 2047 }
                if val < -2048 { val = -2048 }

                var iVal int16
                var qVal int16

                // LSB Alignment: Simple cast to int16
                // If this were MSB aligned, we would shift left: int16(val) << 4
                iVal = int16(val)
                qVal = int16(val) // Using same value for I/Q example, or calc sine for Q

                // Just calculating Q properly here for completeness
                valQ := (amplitude * math.Sin(chPhase)) + dither
                if valQ > 2047 { valQ = 2047 }
                if valQ < -2048 { valQ = -2048 }
                qVal = int16(valQ)

                idx := (s*numChannels + c) * 4
                binary.LittleEndian.PutUint16(writeBuf[idx:], uint16(iVal))
                binary.LittleEndian.PutUint16(writeBuf[idx+2:], uint16(qVal))
            }
            phase += phaseStep
            if phase > 2.0*math.Pi {
                phase -= 2.0 * math.Pi
            }
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

// generateSineWaveBuffer creates a 4MB buffer of 8-channel sine wave I/Q data
func generateSineWaveBuffer() []byte {
    const (
        numChannels     = 8
        bytesPerSample  = 4 // 2 bytes for I (int16), 2 bytes for Q (int16)
        samplesPerChan  = 131072 // 4MB / (8 channels * 4 bytes)
        bufferSize      = samplesPerChan * numChannels * bytesPerSample
    )

    buf := make([]byte, bufferSize)
    
    // K must be an integer for perfect wrapping
    // Target ~800kHz at 250MSPS
    kCycles := 417.0 
    
    for s := 0; s < samplesPerChan; s++ {
        // Calculate angle based on index to prevent cumulative float error
        angle := (2.0 * math.Pi * kCycles * float64(s)) / float64(samplesPerChan)
        
        for c := 0; c < numChannels; c++ {
            phaseShift := float64(c) * (math.Pi / 4)
            
            // Generate I and Q
            iVal := int16(2047 * math.Cos(angle+phaseShift))
            qVal := int16(2047 * math.Sin(angle+phaseShift))

            // Calculate exact index: (SampleIndex * TotalChannels + ChannelIndex) * BytesPerSample
            idx := (s*numChannels + c) * bytesPerSample
            
            binary.LittleEndian.PutUint16(buf[idx:], uint16(iVal))
            binary.LittleEndian.PutUint16(buf[idx+2:], uint16(qVal))
        }
    }
    return buf
}
package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dma/pkg/shm_ring"
)

func main() {
	devicePath := flag.String("dev", "/dev/xdma0_c2h_0", "XDMA device path")
	shmName := flag.String("shm", "/xdma_ring", "Shared memory name")
	sizeGB := flag.Int("size", 8, "Size of ring buffer in GB")
	blockSize := flag.Int("block", 1024*1024, "Read block size in bytes")
	
	flag.Parse()

	sizeBytes := uint64(*sizeGB) * 1024 * 1024 * 1024
	
	log.Printf("Starting XDMA to SHM Bridge")
	log.Printf("Device: %s", *devicePath)
	log.Printf("SHM: /dev/shm%s (%d GB)", *shmName, *sizeGB)

	// Clean up old SHM if it exists and we want a fresh start
	shm_ring.Remove(*shmName)

	ring, err := shm_ring.Create(*shmName, sizeBytes)
	if err != nil {
		log.Fatalf("Failed to create SHM ring: %v", err)
	}
	defer ring.Close()

	// Open XDMA Device
	f, err := os.OpenFile(*devicePath, os.O_RDONLY, 0)
	if err != nil {
		log.Fatalf("Failed to open XDMA device: %v", err)
	}
	defer f.Close()

	// Handle signals for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("\nShutting down...")
		shm_ring.Remove(*shmName)
		os.Exit(0)
	}()

	buf := make([]byte, *blockSize)
	var totalRead uint64
	lastReport := time.Now()
	var lastRead uint64

	log.Println("Streaming started. Press Ctrl+C to stop.")

	for {
		n, err := f.Read(buf)
		if err != nil {
			if err == io.EOF {
				time.Sleep(1 * time.Millisecond)
				continue
			}
			log.Printf("Read error: %v", err)
			break
		}

		if n > 0 {
			_, err = ring.Write(buf[:n])
			if err != nil {
				log.Printf("SHM Write error: %v", err)
				break
			}
			totalRead += uint64(n)
			
			// Performance reporting every 2 seconds
			if time.Since(lastReport) > 2*time.Second {
				elapsed := time.Since(lastReport).Seconds()
				bits := (totalRead - lastRead) * 8
				gbps := float64(bits) / (1e9 * elapsed)
				
			head, _ := ring.GetPointers()
				log.Printf("Speed: %.2f Gbps | Total: %.2f GB | Head: %d", gbps, float64(totalRead)/(1024*1024*1024), head)
				
				lastReport = time.Now()
				lastRead = totalRead
			}
		}
	}
}
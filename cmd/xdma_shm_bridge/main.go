package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dma/pkg/shm_ring"
	"golang.org/x/sys/unix"
)

func main() {
	devicePath := flag.String("dev", "/dev/xdma0_c2h_0", "XDMA device path")
	shmName := flag.String("shm", "/xdma_ring", "Shared memory name")
	sizeGB := flag.Int("size", 8, "Size of ring buffer in GB")
	blockSize := flag.Int("block", 4*1024*1024, "Read block size in bytes (e.g. 4MB)")
	
	flag.Parse()

	sizeBytes := uint64(*sizeGB) * 1024 * 1024 * 1024
	
	log.Printf("Starting High-Performance XDMA to SHM Bridge")
	log.Printf("Device: %s", *devicePath)
	log.Printf("SHM: /dev/shm%s (%d GB)", *shmName, *sizeGB)
	log.Printf("Block Size: %d KB", *blockSize / 1024)

	// Clean up old SHM
	shm_ring.Remove(*shmName)

	ring, err := shm_ring.Create(*shmName, sizeBytes)
	if err != nil {
		log.Fatalf("Failed to create SHM ring: %v", err)
	}
	defer ring.Close()

	// Open XDMA Device using low-level syscall for maximum control
	xdmaFd, err := unix.Open(*devicePath, unix.O_RDONLY, 0)
	if err != nil {
		log.Fatalf("Failed to open XDMA device: %v", err)
	}
	defer unix.Close(xdmaFd)

	// Handle signals for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("\nShutting down...")
		shm_ring.Remove(*shmName)
		os.Exit(0)
	}()

	var totalRead uint64
	lastReport := time.Now()
	var lastRead uint64

	ringData := ring.Data()
	ringTotal := ring.Total()

	log.Println("Zero-copy streaming started. Press Ctrl+C to stop.")

	for {
		head := ring.GetHead()
		
		// Determine how much we can read before hitting the end of the ring buffer
		spaceToEnd := ringTotal - head
		readRequest := uint64(*blockSize)
		
		if readRequest > spaceToEnd {
			readRequest = spaceToEnd
		}

		// READ DIRECTLY INTO MMAP (Zero-Copy)
		n, err := unix.Read(xdmaFd, ringData[head : head+readRequest])
		
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Printf("Read error: %v", err)
			break
		}

		if n > 0 {
			// Only advance by aligned amount (32 bytes = 8 channels * 4 bytes)
			const inputBlockSize = 32
			alignedBytes := (uint64(n) / inputBlockSize) * inputBlockSize
			if alignedBytes > 0 {
				ring.AdvanceHead(alignedBytes)
			}
			totalRead += uint64(n)
			
			// Performance reporting every 2 seconds
			if time.Since(lastReport) > 2*time.Second {
				elapsed := time.Since(lastReport).Seconds()
				bits := (totalRead - lastRead) * 8
				gbps := float64(bits) / (1e9 * elapsed)
				
				log.Printf("Speed: %.2f Gbps (%.2f GB/s) | Total: %.2f GB | Head: %d", 
					gbps, gbps/8, float64(totalRead)/(1024*1024*1024), ring.GetHead())
				
				lastReport = time.Now()
				lastRead = totalRead
			}
		} else {
			// No data, small sleep to prevent spinning
			time.Sleep(1 * time.Microsecond)
		}
	}
}

package main

import (
	"flag"
	"log"
	"time"

	"github.com/dma/pkg/shm_ring"
)

func main() {
	shmName := flag.String("shm", "/xdma_ring", "Shared memory name")
	flag.Parse()

	log.Printf("Connecting to SHM: /dev/shm%s", *shmName)

	ring, err := shm_ring.Open(*shmName)
	if err != nil {
		log.Fatalf("Failed to open SHM ring: %v", err)
	}
	defer ring.Close()

	data := ring.Data()
	
	log.Println("Reading from SHM. Press Ctrl+C to stop.")

	var lastHead uint64
	for {
		head, _ := ring.GetPointers()
		if head != lastHead {
			// Calculate some stats or just show we're seeing movement
			diff := uint64(0)
			if head > lastHead {
				diff = head - lastHead
			} else {
				diff = (uint64(len(data)) - lastHead) + head
			}

			// Peek at some data (first 8 bytes at current head)
			peekIdx := head
			if peekIdx + 8 > uint64(len(data)) {
				peekIdx = 0
			}
			
			log.Printf("Head: %12d | Moved: %10d bytes | Data: %X", head, diff, data[peekIdx:peekIdx+8])
			lastHead = head
		}
		
		time.Sleep(500 * time.Millisecond)
	}
}

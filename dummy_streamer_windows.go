//go:build windows

package main

import (
	"log"
	"time"
)

func RunSimulator(devicePath string) {
	log.Println("[SIM] Simulation mode via named pipes is not supported on Windows.")
	log.Println("[SIM] Please run on Linux or use a different data source.")
	for {
		time.Sleep(1 * time.Second)
	}
}

//go:build windows

package main

import (
	"log"
)

func performRecording() {
	log.Println("Recording not supported on Windows")
	cleanupRecording("Not supported on Windows")
}

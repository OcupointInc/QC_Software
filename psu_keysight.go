package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// KeysightE3631A controls a Keysight E3631A triple output power supply
type KeysightE3631A struct {
	address string
	conn    net.Conn
	mu      sync.Mutex
	reader  *bufio.Reader

	// Cached state
	stateMu      sync.RWMutex
	lastPoll     time.Time
	outputState  bool
	setVoltage   float64
	setCurrent   float64
	measVoltage  float64
	measCurrent  float64
	identity     string
	connected    bool
}

// PSUState represents the current state of the PSU
type PSUState struct {
	Connected       bool    `json:"connected"`
	Identity        string  `json:"identity,omitempty"`
	OutputEnabled   bool    `json:"output_enabled"`
	SetVoltage      float64 `json:"set_voltage"`
	SetCurrent      float64 `json:"set_current"`
	MeasuredVoltage float64 `json:"measured_voltage"`
	MeasuredCurrent float64 `json:"measured_current"`
	Channel         string  `json:"channel"`
}

const (
	psuTimeout     = 2 * time.Second
	psuPollInterval = 500 * time.Millisecond
	psuChannel     = "P25V" // Channel 2: +25V output
)

var (
	globalPSU   *KeysightE3631A
	globalPSUMu sync.Mutex
)

// NewKeysightE3631A creates a new PSU controller
// visaAddress format: TCPIP::192.168.1.200::inst0::INSTR
func NewKeysightE3631A(visaAddress string) *KeysightE3631A {
	// Parse VISA address to get IP
	// Format: TCPIP::192.168.1.200::inst0::INSTR
	parts := strings.Split(visaAddress, "::")
	ip := "192.168.1.200" // default
	if len(parts) >= 2 {
		ip = parts[1]
	}

	return &KeysightE3631A{
		address: ip + ":5025", // Standard SCPI raw socket port
	}
}

// Connect establishes connection to the PSU
func (p *KeysightE3631A) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil {
		p.conn.Close()
	}

	conn, err := net.DialTimeout("tcp", p.address, psuTimeout)
	if err != nil {
		p.stateMu.Lock()
		p.connected = false
		p.stateMu.Unlock()
		return fmt.Errorf("failed to connect to PSU at %s: %w", p.address, err)
	}

	p.conn = conn
	p.reader = bufio.NewReader(conn)

	// Get identity
	identity, err := p.queryLocked("*IDN?")
	if err != nil {
		p.conn.Close()
		p.conn = nil
		p.stateMu.Lock()
		p.connected = false
		p.stateMu.Unlock()
		return fmt.Errorf("failed to identify PSU: %w", err)
	}

	p.stateMu.Lock()
	p.identity = strings.TrimSpace(identity)
	p.connected = true
	p.stateMu.Unlock()

	log.Printf("PSU connected: %s", p.identity)

	// Select channel 2 (P25V)
	if err := p.writeLocked("INST:SEL " + psuChannel); err != nil {
		return fmt.Errorf("failed to select channel: %w", err)
	}

	return nil
}

// Disconnect closes the connection
func (p *KeysightE3631A) Disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}

	p.stateMu.Lock()
	p.connected = false
	p.stateMu.Unlock()
}

// IsConnected returns whether the PSU is connected
func (p *KeysightE3631A) IsConnected() bool {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()
	return p.connected
}

// writeLocked sends a command (caller must hold p.mu)
func (p *KeysightE3631A) writeLocked(cmd string) error {
	if p.conn == nil {
		return fmt.Errorf("not connected")
	}

	p.conn.SetWriteDeadline(time.Now().Add(psuTimeout))
	_, err := p.conn.Write([]byte(cmd + "\n"))
	return err
}

// queryLocked sends a query and reads response (caller must hold p.mu)
func (p *KeysightE3631A) queryLocked(cmd string) (string, error) {
	if p.conn == nil {
		return "", fmt.Errorf("not connected")
	}

	p.conn.SetWriteDeadline(time.Now().Add(psuTimeout))
	if _, err := p.conn.Write([]byte(cmd + "\n")); err != nil {
		return "", err
	}

	p.conn.SetReadDeadline(time.Now().Add(psuTimeout))
	response, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response), nil
}

// Poll reads current state from the PSU
func (p *KeysightE3631A) Poll() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Ensure we're on the right channel
	if err := p.writeLocked("INST:SEL " + psuChannel); err != nil {
		p.handleDisconnect()
		return err
	}

	// Query output state
	outpStr, err := p.queryLocked("OUTP?")
	if err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to query output state: %w", err)
	}

	// Query set voltage
	voltStr, err := p.queryLocked("VOLT?")
	if err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to query set voltage: %w", err)
	}

	// Query set current
	currStr, err := p.queryLocked("CURR?")
	if err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to query set current: %w", err)
	}

	// Measure actual voltage
	measVoltStr, err := p.queryLocked("MEAS:VOLT?")
	if err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to measure voltage: %w", err)
	}

	// Measure actual current
	measCurrStr, err := p.queryLocked("MEAS:CURR?")
	if err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to measure current: %w", err)
	}

	// Parse responses
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	p.outputState = strings.TrimSpace(outpStr) == "1" || strings.ToUpper(strings.TrimSpace(outpStr)) == "ON"
	p.setVoltage, _ = strconv.ParseFloat(strings.TrimSpace(voltStr), 64)
	p.setCurrent, _ = strconv.ParseFloat(strings.TrimSpace(currStr), 64)
	p.measVoltage, _ = strconv.ParseFloat(strings.TrimSpace(measVoltStr), 64)
	p.measCurrent, _ = strconv.ParseFloat(strings.TrimSpace(measCurrStr), 64)
	p.lastPoll = time.Now()

	return nil
}

// handleDisconnect marks the PSU as disconnected (caller must hold p.mu)
func (p *KeysightE3631A) handleDisconnect() {
	p.stateMu.Lock()
	p.connected = false
	p.stateMu.Unlock()
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
}

// GetState returns the current cached state
func (p *KeysightE3631A) GetState() PSUState {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()

	return PSUState{
		Connected:       p.connected,
		Identity:        p.identity,
		OutputEnabled:   p.outputState,
		SetVoltage:      p.setVoltage,
		SetCurrent:      p.setCurrent,
		MeasuredVoltage: p.measVoltage,
		MeasuredCurrent: p.measCurrent,
		Channel:         psuChannel,
	}
}

// SetOutput enables or disables the output
func (p *KeysightE3631A) SetOutput(enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Ensure we're on the right channel
	if err := p.writeLocked("INST:SEL " + psuChannel); err != nil {
		p.handleDisconnect()
		return err
	}

	cmd := "OUTP OFF"
	if enabled {
		cmd = "OUTP ON"
	}

	if err := p.writeLocked(cmd); err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to set output: %w", err)
	}

	// Update cached state
	p.stateMu.Lock()
	p.outputState = enabled
	p.stateMu.Unlock()

	log.Printf("PSU output %s", cmd)
	return nil
}

// SetVoltage sets the output voltage
func (p *KeysightE3631A) SetVoltage(voltage float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Clamp to safe range (0-12V max for safety)
	if voltage < 0 {
		voltage = 0
	}
	if voltage > 12 {
		voltage = 12
	}

	if err := p.writeLocked("INST:SEL " + psuChannel); err != nil {
		p.handleDisconnect()
		return err
	}

	if err := p.writeLocked(fmt.Sprintf("VOLT %.3f", voltage)); err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to set voltage: %w", err)
	}

	p.stateMu.Lock()
	p.setVoltage = voltage
	p.stateMu.Unlock()

	log.Printf("PSU voltage set to %.3f V", voltage)
	return nil
}

// SetCurrent sets the current limit
func (p *KeysightE3631A) SetCurrent(current float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Clamp to valid range for P25V channel (0-1A)
	if current < 0 {
		current = 0
	}
	if current > 1 {
		current = 1
	}

	if err := p.writeLocked("INST:SEL " + psuChannel); err != nil {
		p.handleDisconnect()
		return err
	}

	if err := p.writeLocked(fmt.Sprintf("CURR %.3f", current)); err != nil {
		p.handleDisconnect()
		return fmt.Errorf("failed to set current: %w", err)
	}

	p.stateMu.Lock()
	p.setCurrent = current
	p.stateMu.Unlock()

	log.Printf("PSU current limit set to %.3f A", current)
	return nil
}

// StartPolling begins background polling of PSU state
func (p *KeysightE3631A) StartPolling(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			if !p.IsConnected() {
				// Try to reconnect
				if err := p.Connect(); err != nil {
					continue
				}
			}

			if err := p.Poll(); err != nil {
				log.Printf("PSU poll error: %v", err)
			}
		}
	}()
}

// InitGlobalPSU initializes the global PSU controller
func InitGlobalPSU(visaAddress string) error {
	globalPSUMu.Lock()
	defer globalPSUMu.Unlock()

	if globalPSU != nil {
		globalPSU.Disconnect()
	}

	globalPSU = NewKeysightE3631A(visaAddress)

	if err := globalPSU.Connect(); err != nil {
		log.Printf("Warning: Could not connect to PSU: %v", err)
		// Start polling anyway - it will retry connection
	}

	globalPSU.StartPolling(psuPollInterval)
	return nil
}

// GetGlobalPSU returns the global PSU controller
func GetGlobalPSU() *KeysightE3631A {
	globalPSUMu.Lock()
	defer globalPSUMu.Unlock()
	return globalPSU
}

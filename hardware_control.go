package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// Hardware parameter IDs
type ParamID int

const (
	// Channel enables
	CH0_EN ParamID = iota
	CH1_EN
	CH2_EN
	CH3_EN
	CH4_EN
	CH5_EN
	CH6_EN
	CH7_EN

	// DDC enables
	DDC0_EN
	DDC1_EN
	DDC2_EN

	// DDC control
	DDC0_FMIX
	DDC0_SFOUT
	DDC1_FMIX
	DDC1_SFOUT
	DDC2_FMIX
	DDC2_SFOUT

	// Filter controls
	LP500MHZ_EN
	LP1GHZ_EN
	LP2GHZ_EN
	BYPASS_EN

	// Attenuation
	ATTENUATION_BVAL

	// System state
	SYSTEM_EN
	CAL_EN
)

// BRAM Schema constants
const (
	START_TOKEN        = 0xDEADBEEF
	START_TOKEN_ADDR   = 0x00
	STATUS_ADDR        = 0x01
	SCHEMA_VERSION     = 0x01
	SCHEMA_VERSION_ADDR = 0x02
	NUM_PARAMS_ADDR    = 0x05
	END_HEADER_TOKEN   = 0xDEADBEEF
	END_HEADER_ADDR    = 0x06
	PARAM_START_TOKEN  = 0xCCCCCCCC
	KEY_VAL_SEP        = 0xBBBBBBBB
	PARAM_END          = 0xEEEEEEEE
	LAST_PARAM         = 0xABABABAB
	END_TOKEN          = 0xEEEEEEEE

	// Status bits
	HOST_PARAM_CHANGE   = 1 << 31
	PARAM_CHANGE_ACK    = 1 << 30
	PARAM_CHANGE_DONE   = 1 << 29
	PARAM_CHANGE_STAT   = 1 << 28
	BRAM_SETUP_REQUEST  = 1 << 27
	HOST_SETUP_DONE     = 1 << 26
	BRAM_SCHEMA_RETURN  = 1 << 25
	BRAM_SCHEMA_VALID   = 1 << 24
	HOST_IND_OP_REQUEST = 1 << 23
	IND_OP_ACK          = 1 << 22
	IND_OP_ONLINE       = 1 << 21
)

// Parameter definition
type Parameter struct {
	ID    ParamID
	Name  string
	Value int
}

// HardwareController manages FPGA parameter control via PCIe
type HardwareController struct {
	commandDevice string
	params        map[ParamID]*Parameter
	mu            sync.RWMutex
}

// Parameter table mapping
var paramTable = []Parameter{
	{CH0_EN, "CH0_EN", 0},
	{CH1_EN, "CH1_EN", 0},
	{CH2_EN, "CH2_EN", 0},
	{CH3_EN, "CH3_EN", 0},
	{CH4_EN, "CH4_EN", 0},
	{CH5_EN, "CH5_EN", 0},
	{CH6_EN, "CH6_EN", 0},
	{CH7_EN, "CH7_EN", 0},
	{DDC0_EN, "DDC0_EN", 0},
	{DDC1_EN, "DDC1_EN", 0},
	{DDC2_EN, "DDC2_EN", 0},
	{DDC0_FMIX, "DDC0_FMIX", 10},
	{DDC0_SFOUT, "DDC0_SFOUT", 1},
	{DDC1_FMIX, "DDC1_FMIX", 1},
	{DDC1_SFOUT, "DDC1_SFOUT", 1},
	{DDC2_FMIX, "DDC2_FMIX", 1},
	{DDC2_SFOUT, "DDC2_SFOUT", 1},
	{LP500MHZ_EN, "LP500MHZ_EN", 1},
	{LP1GHZ_EN, "LP1GHZ_EN", 0},
	{LP2GHZ_EN, "LP2GHZ_EN", 0},
	{BYPASS_EN, "BYPASS_EN", 0},
	{ATTENUATION_BVAL, "ATTENUATION_BVAL", 0},
	{SYSTEM_EN, "SYSTEM_EN", 1},
	{CAL_EN, "CAL_EN", 0},
}

// NewHardwareController creates a new hardware controller
func NewHardwareController(commandDevice string) *HardwareController {
	hc := &HardwareController{
		commandDevice: commandDevice,
		params:        make(map[ParamID]*Parameter),
	}

	// Initialize parameter map
	for i := range paramTable {
		p := paramTable[i]
		hc.params[p.ID] = &Parameter{
			ID:    p.ID,
			Name:  p.Name,
			Value: p.Value,
		}
	}

	return hc
}

// SetupBRAM initializes BRAM if hardware requests it
func (hc *HardwareController) SetupBRAM() error {
	status, err := hc.readPCIeBytes(STATUS_ADDR)
	if err != nil {
		return fmt.Errorf("failed to read status: %w", err)
	}

	if status&BRAM_SETUP_REQUEST != 0 {
		log.Println("Initializing BRAM...")
		if err := hc.programBRAM(); err != nil {
			return fmt.Errorf("failed to program BRAM: %w", err)
		}

		log.Println("Setting BRAM complete flag...")
		status, err = hc.readPCIeBytes(STATUS_ADDR)
		if err != nil {
			return err
		}
		status |= HOST_SETUP_DONE
		if err := hc.writePCIeBytes(status, STATUS_ADDR); err != nil {
			return err
		}

		time.Sleep(50 * time.Millisecond)

		log.Println("Resetting BRAM complete flag...")
		status, err = hc.readPCIeBytes(STATUS_ADDR)
		if err != nil {
			return err
		}
		status &^= HOST_SETUP_DONE
		if err := hc.writePCIeBytes(status, STATUS_ADDR); err != nil {
			return err
		}
	}

	return nil
}

// programBRAM writes parameter table to BRAM
func (hc *HardwareController) programBRAM() error {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	// Write start token
	if err := hc.writePCIeBytes(START_TOKEN, START_TOKEN_ADDR); err != nil {
		return err
	}

	// Write number of params
	if err := hc.writePCIeBytes(uint32(len(hc.params)), NUM_PARAMS_ADDR); err != nil {
		return err
	}

	// Write end header token
	if err := hc.writePCIeBytes(END_HEADER_TOKEN, END_HEADER_ADDR); err != nil {
		return err
	}

	// Write all parameters
	address := END_HEADER_ADDR + 1
	for idx, param := range paramTable {
		p := hc.params[param.ID]

		// Param start token
		if err := hc.writePCIeBytes(PARAM_START_TOKEN, address); err != nil {
			return err
		}
		address++

		// Param ID
		if err := hc.writePCIeBytes(uint32(idx), address); err != nil {
			return err
		}
		address++

		// Param key length
		if err := hc.writePCIeBytes(uint32(len(p.Name)), address); err != nil {
			return err
		}
		address++

		// Param key offset
		if err := hc.writePCIeBytes(3, address); err != nil {
			return err
		}
		address++

		// Param value
		if err := hc.writePCIeBytes(uint32(p.Value), address); err != nil {
			return err
		}
		address++

		// Key-value separator
		if err := hc.writePCIeBytes(KEY_VAL_SEP, address); err != nil {
			return err
		}
		address++

		// Param name (string)
		if err := hc.writePCIeString(p.Name, address); err != nil {
			return err
		}
		address += (len(p.Name) / 4) + 1

		// Param end token
		if err := hc.writePCIeBytes(PARAM_END, address); err != nil {
			return err
		}
		address++
	}

	// Last param marker
	if err := hc.writePCIeBytes(LAST_PARAM, address); err != nil {
		return err
	}
	address++

	// End token
	if err := hc.writePCIeBytes(END_TOKEN, address); err != nil {
		return err
	}

	return nil
}

// UpdateParameter updates a single parameter and notifies hardware
func (hc *HardwareController) UpdateParameter(paramID ParamID, value int) error {
	hc.mu.Lock()
	param, exists := hc.params[paramID]
	if !exists {
		hc.mu.Unlock()
		return fmt.Errorf("parameter ID %d not found", paramID)
	}

	param.Value = value
	paramIndex := -1
	for idx, p := range paramTable {
		if p.ID == paramID {
			paramIndex = idx
			break
		}
	}
	hc.mu.Unlock()

	if paramIndex == -1 {
		return fmt.Errorf("parameter index not found for ID %d", paramID)
	}

	return hc.updateBRAM(paramIndex)
}

// updateBRAM signals hardware that a parameter changed
func (hc *HardwareController) updateBRAM(paramIndex int) error {
	log.Printf("Updating BRAM parameter with index: %d", paramIndex)

	// Set param change request bit
	status, err := hc.readPCIeBytes(STATUS_ADDR)
	if err != nil {
		return err
	}
	status |= HOST_PARAM_CHANGE
	if err := hc.writePCIeBytes(status, STATUS_ADDR); err != nil {
		return err
	}

	// Wait for hardware acknowledgment
	timeout := time.After(1 * time.Second)
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for param change ACK")
		default:
			status, err := hc.readPCIeBytes(STATUS_ADDR)
			if err != nil {
				return err
			}
			if status&PARAM_CHANGE_ACK != 0 {
				goto acknowledged
			}
			time.Sleep(1 * time.Millisecond)
		}
	}

acknowledged:
	// Reset param change request bit
	status, err = hc.readPCIeBytes(STATUS_ADDR)
	if err != nil {
		return err
	}
	status &^= HOST_PARAM_CHANGE
	if err := hc.writePCIeBytes(status, STATUS_ADDR); err != nil {
		return err
	}

	// Reprogram BRAM
	if err := hc.programBRAM(); err != nil {
		return err
	}

	// Write changed param index
	status, err = hc.readPCIeBytes(STATUS_ADDR)
	if err != nil {
		return err
	}
	status = (status & 0xFFFF0000) | uint32(paramIndex&0xFFFF)
	if err := hc.writePCIeBytes(status, STATUS_ADDR); err != nil {
		return err
	}

	// Set param change done bit
	status, err = hc.readPCIeBytes(STATUS_ADDR)
	if err != nil {
		return err
	}
	status |= PARAM_CHANGE_DONE
	if err := hc.writePCIeBytes(status, STATUS_ADDR); err != nil {
		return err
	}

	// Wait for hardware to reset ACK
	timeout = time.After(1 * time.Second)
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for param change done")
		default:
			status, err := hc.readPCIeBytes(STATUS_ADDR)
			if err != nil {
				return err
			}
			if status&PARAM_CHANGE_ACK == 0 {
				goto done
			}
			time.Sleep(1 * time.Millisecond)
		}
	}

done:
	// Reset param change done bit
	status, err = hc.readPCIeBytes(STATUS_ADDR)
	if err != nil {
		return err
	}
	status &^= PARAM_CHANGE_DONE
	return hc.writePCIeBytes(status, STATUS_ADDR)
}

// GetParameter gets current value of a parameter
func (hc *HardwareController) GetParameter(paramID ParamID) (int, error) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	param, exists := hc.params[paramID]
	if !exists {
		return 0, fmt.Errorf("parameter ID %d not found", paramID)
	}

	return param.Value, nil
}

// writePCIeBytes writes a 32-bit value to PCIe device at offset
func (hc *HardwareController) writePCIeBytes(data uint32, offset int) error {
	f, err := os.OpenFile(hc.commandDevice, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open command device: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, data)

	_, err = f.WriteAt(buf, int64(offset*4))
	return err
}

// readPCIeBytes reads a 32-bit value from PCIe device at offset
func (hc *HardwareController) readPCIeBytes(offset int) (uint32, error) {
	f, err := os.OpenFile(hc.commandDevice, os.O_RDONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to open command device: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 4)
	_, err = f.ReadAt(buf, int64(offset*4))
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint32(buf), nil
}

// writePCIeString writes a string as 32-bit chunks
func (hc *HardwareController) writePCIeString(data string, offset int) error {
	// Pad string to multiple of 4 bytes
	padded := data
	for len(padded)%4 != 0 {
		padded += "\x00"
	}

	for i := 0; i < len(padded); i += 4 {
		chunk := padded[i : i+4]
		var val uint32
		for j := 0; j < 4; j++ {
			val |= uint32(chunk[j]) << (j * 8)
		}
		if err := hc.writePCIeBytes(val, offset+i/4); err != nil {
			return err
		}
	}

	return nil
}

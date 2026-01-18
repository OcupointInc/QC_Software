package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
)

const DesignClockMHz = 250.0


var hwController *HardwareController

// Initialize hardware controller
func initHardwareController(commandDevice string) error {
	hwController = NewHardwareController(commandDevice)

	// Open the device file for persistent access
	if err := hwController.Open(); err != nil {
		return fmt.Errorf("failed to open command device: %w", err)
	}

	// Verify connection by reading status
	if _, err := hwController.readPCIeBytes(STATUS_ADDR); err != nil {
		hwController.Close()
		return fmt.Errorf("failed to access hardware: %w", err)
	}

	// Perform a simple memory check instead of hardware handshake
	log.Println("Verifying system memory (1GB check)...")
	const gb = 1024 * 1024 * 1024
	
	// allocate 1GB
	mem := make([]byte, gb)
	if len(mem) != gb {
		hwController.Close()
		return fmt.Errorf("failed to allocate 1GB memory")
	}
	
	// Touch end of buffer to ensure allocation
	mem[gb-1] = 1
	
	// "Deallocate"
	mem = nil
	log.Println("Memory check passed")

	// Try to setup BRAM on startup
	if err := hwController.SetupBRAM(); err != nil {
		log.Printf("Warning: Failed to setup BRAM: %v", err)
	}

	// Sync server state with hardware DDC0 frequency
	if ddc0Freq, err := hwController.GetParameter(DDC0_FMIX); err == nil {
		serverState.mu.Lock()
		// Convert hardware value (design clock domain) to real frequency
		serverState.DDCFreqMHz = float64(ddc0Freq) * (serverState.IBWMHZ / DesignClockMHz)
		serverState.mu.Unlock()
		log.Printf("Initialized center frequency from hardware: %.3f MHz", serverState.DDCFreqMHz)
	}
	
	return nil
}

// setDDCFrequency sets the DDC frequency with clock domain scaling
func setDDCFrequency(ddcIndex int, freqMHz float64) (float64, error) {
	var paramID ParamID
	switch ddcIndex {
	case 0:
		paramID = DDC0_FMIX
	case 1:
		paramID = DDC1_FMIX
	case 2:
		paramID = DDC2_FMIX
	default:
		return 0, fmt.Errorf("invalid DDC index: %d", ddcIndex)
	}

	serverState.mu.RLock()
	actualClock := serverState.IBWMHZ
	serverState.mu.RUnlock()

	hwVal := int(math.Round(freqMHz * (DesignClockMHz / actualClock)))

	// Calculate achieved frequency and round to nearest integer for the UI
	achievedMHz := math.Round(float64(hwVal) * (actualClock / DesignClockMHz))

	return achievedMHz, hwController.UpdateParameter(paramID, hwVal)
}

// DDC Frequency handler
func handleDDCFreqUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverState.mu.RLock()
	if !serverState.HardwareAvailable {
		serverState.mu.RUnlock()
		http.Error(w, "Hardware unavailable", http.StatusServiceUnavailable)
		return
	}
	serverState.mu.RUnlock()

	var req struct {
		DDCIndex int     `json:"ddc_index"` // 0, 1, or 2
		FreqMHz  float64 `json:"freq_mhz"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure we are working with integer requested frequency
	req.FreqMHz = math.Round(req.FreqMHz)

	actualFreq, err := setDDCFrequency(req.DDCIndex, req.FreqMHz)
	if err != nil {
		log.Printf("Failed to update DDC frequency: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Update server state center frequency if DDC0
	if req.DDCIndex == 0 {
		serverState.mu.Lock()
		serverState.DDCFreqMHz = actualFreq
		serverState.mu.Unlock()
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"ddc_index": req.DDCIndex,
		"freq_mhz":  int(actualFreq),
	})

	// Broadcast update to all clients
	go broadcastJSON(map[string]interface{}{
		"type":      "ddc_freq_update",
		"ddc_index": req.DDCIndex,
		"freq_mhz":  int(actualFreq),
	})
}

// DDC Enable handler
func handleDDCEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverState.mu.RLock()
	if !serverState.HardwareAvailable {
		serverState.mu.RUnlock()
		http.Error(w, "Hardware unavailable", http.StatusServiceUnavailable)
		return
	}
	serverState.mu.RUnlock()

	var req struct {
		DDCIndex int  `json:"ddc_index"` // 0, 1, or 2
		Enabled  bool `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var paramID ParamID
	switch req.DDCIndex {
	case 0:
		paramID = DDC0_EN
	case 1:
		paramID = DDC1_EN
	case 2:
		paramID = DDC2_EN
	default:
		http.Error(w, "Invalid DDC index", http.StatusBadRequest)
		return
	}

	value := 0
	if req.Enabled {
		value = 1
	}

	if err := hwController.UpdateParameter(paramID, value); err != nil {
		log.Printf("Failed to update DDC enable: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"ddc_index": req.DDCIndex,
		"enabled":   req.Enabled,
	})

	// Broadcast update
	go broadcastJSON(map[string]interface{}{
		"type":      "ddc_enable_update",
		"ddc_index": req.DDCIndex,
		"enabled":   req.Enabled,
	})
}

// Attenuation handler
func handleAttenuationUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverState.mu.RLock()
	if !serverState.HardwareAvailable {
		serverState.mu.RUnlock()
		http.Error(w, "Hardware unavailable", http.StatusServiceUnavailable)
		return
	}
	serverState.mu.RUnlock()

	var req struct {
		AttenuationDB int `json:"attenuation_db"` // 0-31
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.AttenuationDB < 0 || req.AttenuationDB > 31 {
		http.Error(w, "Attenuation must be between 0 and 31 dB", http.StatusBadRequest)
		return
	}

	if err := hwController.UpdateParameter(ATTENUATION_BVAL, req.AttenuationDB); err != nil {
		log.Printf("Failed to update attenuation: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"attenuation_db": req.AttenuationDB,
	})

	// Broadcast update
	go broadcastJSON(map[string]interface{}{
		"type":           "attenuation_update",
		"attenuation_db": req.AttenuationDB,
	})
}

// Filter selection handler
func handleFilterSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverState.mu.RLock()
	if !serverState.HardwareAvailable {
		serverState.mu.RUnlock()
		http.Error(w, "Hardware unavailable", http.StatusServiceUnavailable)
		return
	}
	serverState.mu.RUnlock()

	var req struct {
		Filter string `json:"filter"` // "500mhz", "1ghz", "2ghz", "bypass"
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Disable all filters first
	hwController.UpdateParameter(LP500MHZ_EN, 0)
	hwController.UpdateParameter(LP1GHZ_EN, 0)
	hwController.UpdateParameter(LP2GHZ_EN, 0)
	hwController.UpdateParameter(BYPASS_EN, 0)

	// Enable selected filter
	var paramID ParamID
	switch req.Filter {
	case "500mhz":
		paramID = LP500MHZ_EN
	case "1ghz":
		paramID = LP1GHZ_EN
	case "2ghz":
		paramID = LP2GHZ_EN
	case "bypass":
		paramID = BYPASS_EN
	default:
		http.Error(w, "Invalid filter selection", http.StatusBadRequest)
		return
	}

	if err := hwController.UpdateParameter(paramID, 1); err != nil {
		log.Printf("Failed to update filter: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"filter":  req.Filter,
	})

	// Broadcast update
	go broadcastJSON(map[string]interface{}{
		"type":   "filter_update",
		"filter": req.Filter,
	})
}

// Calibration mode handler
func handleCalibrationMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverState.mu.RLock()
	if !serverState.HardwareAvailable {
		serverState.mu.RUnlock()
		http.Error(w, "Hardware unavailable", http.StatusServiceUnavailable)
		return
	}
	serverState.mu.RUnlock()

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	value := 0
	if req.Enabled {
		value = 1
	}

	if err := hwController.UpdateParameter(CAL_EN, value); err != nil {
		log.Printf("Failed to update calibration mode: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"enabled": req.Enabled,
	})

	// Broadcast update
	go broadcastJSON(map[string]interface{}{
		"type":    "calibration_update",
		"enabled": req.Enabled,
	})
}

// System enable handler
func handleSystemEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverState.mu.RLock()
	if !serverState.HardwareAvailable {
		serverState.mu.RUnlock()
		http.Error(w, "Hardware unavailable", http.StatusServiceUnavailable)
		return
	}
	serverState.mu.RUnlock()

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	value := 0
	if req.Enabled {
		value = 1
	}

	if err := hwController.UpdateParameter(SYSTEM_EN, value); err != nil {
		log.Printf("Failed to update system enable: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"enabled": req.Enabled,
	})

	// Broadcast update
	go broadcastJSON(map[string]interface{}{
		"type":    "system_enable_update",
		"enabled": req.Enabled,
	})
}

// Get current hardware state
func handleHardwareState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverState.mu.RLock()
	hwAvailable := serverState.HardwareAvailable
	serverState.mu.RUnlock()

	if !hwAvailable {
		// Return last known state or defaults
		serverState.mu.RLock()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ddc0_freq_mhz":  int(serverState.DDCFreqMHz), // Approximate
			"ddc1_freq_mhz":  0,
			"ddc2_freq_mhz":  0,
			"ddc0_enabled":   false,
			"ddc1_enabled":   false,
			"ddc2_enabled":   false,
			"attenuation_db": 0,
			"cal_enabled":    false,
			"system_enabled": false,
			"active_filter":  "unknown",
		})
		serverState.mu.RUnlock()
		return
	}

	ddc0Freq, _ := hwController.GetParameter(DDC0_FMIX)
	ddc1Freq, _ := hwController.GetParameter(DDC1_FMIX)
	ddc2Freq, _ := hwController.GetParameter(DDC2_FMIX)

	ddc0En, _ := hwController.GetParameter(DDC0_EN)
	ddc1En, _ := hwController.GetParameter(DDC1_EN)
	ddc2En, _ := hwController.GetParameter(DDC2_EN)

	atten, _ := hwController.GetParameter(ATTENUATION_BVAL)
	cal, _ := hwController.GetParameter(CAL_EN)
	sysEn, _ := hwController.GetParameter(SYSTEM_EN)

	lp500, _ := hwController.GetParameter(LP500MHZ_EN)
	lp1g, _ := hwController.GetParameter(LP1GHZ_EN)
	lp2g, _ := hwController.GetParameter(LP2GHZ_EN)
	bypass, _ := hwController.GetParameter(BYPASS_EN)

	activeFilter := "none"
	if lp500 == 1 {
		activeFilter = "500mhz"
	} else if lp1g == 1 {
		activeFilter = "1ghz"
	} else if lp2g == 1 {
		activeFilter = "2ghz"
	} else if bypass == 1 {
		activeFilter = "bypass"
	}

	// Reverse scale frequencies for display and round to nearest integer
	serverState.mu.RLock()
	actualClock := serverState.IBWMHZ
	serverState.mu.RUnlock()
	scale := actualClock / DesignClockMHz

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ddc0_freq_mhz":  int(math.Round(float64(ddc0Freq) * scale)),
		"ddc1_freq_mhz":  int(math.Round(float64(ddc1Freq) * scale)),
		"ddc2_freq_mhz":  int(math.Round(float64(ddc2Freq) * scale)),
		"ddc0_enabled":   ddc0En == 1,
		"ddc1_enabled":   ddc1En == 1,
		"ddc2_enabled":   ddc2En == 1,
		"attenuation_db": atten,
		"cal_enabled":    cal == 1,
		"system_enabled": sysEn == 1,
		"active_filter":  activeFilter,
	})
}

package main

import (
	"encoding/json"
	"log"
	"net/http"
)

var hwController *HardwareController

// Initialize hardware controller
func initHardwareController(commandDevice string) {
	hwController = NewHardwareController(commandDevice)

	// Try to setup BRAM on startup
	if err := hwController.SetupBRAM(); err != nil {
		log.Printf("Warning: Failed to setup BRAM: %v", err)
	}

	// Sync server state with hardware DDC0 frequency
	if ddc0Freq, err := hwController.GetParameter(DDC0_FMIX); err == nil {
		serverState.mu.Lock()
		serverState.DDCFreqMHz = float64(ddc0Freq)
		serverState.mu.Unlock()
		log.Printf("Initialized center frequency from hardware: %.3f MHz", float64(ddc0Freq))
	}
}

// DDC Frequency handler
func handleDDCFreqUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DDCIndex int     `json:"ddc_index"` // 0, 1, or 2
		FreqMHz  float64 `json:"freq_mhz"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var paramID ParamID
	switch req.DDCIndex {
	case 0:
		paramID = DDC0_FMIX
	case 1:
		paramID = DDC1_FMIX
	case 2:
		paramID = DDC2_FMIX
	default:
		http.Error(w, "Invalid DDC index", http.StatusBadRequest)
		return
	}

	if err := hwController.UpdateParameter(paramID, int(req.FreqMHz)); err != nil {
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
		serverState.DDCFreqMHz = req.FreqMHz
		serverState.mu.Unlock()
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"ddc_index": req.DDCIndex,
		"freq_mhz":  req.FreqMHz,
	})

	// Broadcast update to all clients
	go broadcastJSON(map[string]interface{}{
		"type":      "ddc_freq_update",
		"ddc_index": req.DDCIndex,
		"freq_mhz":  req.FreqMHz,
	})
}

// DDC Enable handler
func handleDDCEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ddc0_freq_mhz":  ddc0Freq,
		"ddc1_freq_mhz":  ddc1Freq,
		"ddc2_freq_mhz":  ddc2Freq,
		"ddc0_enabled":   ddc0En == 1,
		"ddc1_enabled":   ddc1En == 1,
		"ddc2_enabled":   ddc2En == 1,
		"attenuation_db": atten,
		"cal_enabled":    cal == 1,
		"system_enabled": sysEn == 1,
		"active_filter":  activeFilter,
	})
}

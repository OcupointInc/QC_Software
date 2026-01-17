# Queens Canyon Capture Software

## Project Overview
This project provides a high-speed data capture and control interface for the "Queens Canyon" FPGA-based system. It enables streaming data from FPGA hardware via PCIe (XDMA), controlling hardware parameters through BRAM-mapped registers, and managing external equipment like power supplies.

The system features:
- **High-speed DMA Streaming**: Interfaces with XDMA drivers on both Linux and Windows.
- **Web-based Control Panel**: A real-time dashboard for hardware configuration, status monitoring, and data visualization using WebSockets.
- **Hardware Parameter Control**: Dynamic updating of FPGA parameters (DDC frequencies, filters, attenuation) via a BRAM-based schema.
- **Equipment Integration**: Remote control of Keysight E3631A PSU via SCPI over TCP.
- **Flexible Modes**: Support for both CLI-based batch capture and WebSocket-based live streaming/control.

## Key Technologies
- **Language**: Go (Golang)
- **Frontend**: HTML5, CSS (Bootstrap-like), JavaScript (uPlot for charting)
- **Communication**: WebSockets (gorilla/websocket)
- **Hardware Interface**: PCIe XDMA, SCPI over TCP (for PSU)

## Project Structure
- `main.go`: Application entry point, handles CLI flags and mode selection.
- `server.go`: Core WebSocket server and HTTP handler registration.
- `handlers.go`: API endpoint implementations for hardware and PSU control.
- `hardware_control.go`: FPGA parameter management and BRAM protocol implementation.
- `psu_keysight.go`: SCPI driver for Keysight E3631A power supply.
- `pkg/dma/`: Cross-platform DMA abstraction layer.
- `templates/` & `static/`: Web UI components.
- `recording_loop_*.go`: OS-specific logic for high-speed data recording to disk.
- `stream_loop_*.go`: OS-specific logic for streaming data to connected WebSocket clients.
- `pkg/shm_ring/`: Shared memory ring buffer implementation for inter-process communication.
- `cmd/xdma_shm_bridge/`: Standalone utility to bridge XDMA data into a high-speed SHM ring buffer.

## Building and Running

### Build
To build the main executable and bridge tools:
```bash
go build -o capture_sw .
go build -o xdma_shm_bridge ./cmd/xdma_shm_bridge
go build -o shm_reader_test ./cmd/shm_reader_test
```

### Shared Memory Bridge
To run the high-speed bridge (8GB ring buffer):
```bash
./xdma_shm_bridge -dev /dev/xdma0_c2h_0 -size 8
```
Other processes can then access this data by mapping `/dev/shm/xdma_ring`.

### Run as Server
To start the web server with PSU support:
```bash
./capture_sw -server -p 8080 -psu TCPIP::192.168.1.200::inst0::INSTR
```
Navigate to `http://localhost:8080` to access the control panel.

### Run as CLI
To perform a direct capture to file:
```bash
./capture_sw -o output.bin -channels 1,2,3
```

## Development Conventions
- **Cross-Platform**: Maintain separate `_linux.go` and `_windows.go` files for OS-specific hardware interactions (DMA, file IO).
- **Concurrency**: Use `sync.Mutex` and `sync.RWMutex` for safe access to shared hardware state.
- **Hardware Safety**: All voltage and current limits for the PSU are clamped in `psu_keysight.go` to prevent hardware damage.
- **Parameters**: New FPGA parameters should be added to the `paramTable` in `hardware_control.go` to be automatically included in the BRAM sync process.

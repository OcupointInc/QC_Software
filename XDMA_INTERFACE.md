# XDMA Data and Configuration Interface Documentation

## Streaming Data Interface

### Overview

The streaming data interface provides high-speed data acquisition from the FPGA to the host system using XDMA (Xilinx DMA). Data is read from a character device that represents the Card-to-Host (C2H) DMA channel.

### Device Path

```
/dev/xdma0_c2h_0
```

### Data Format

- **Data Type**: 16-bit signed integers (I and Q samples)
- **Byte Order**: Little-endian
- **Channel Configuration**: 8-channel interleaved IQ data
- **Sample Order**: I1, Q1, I2, Q2, I3, Q3, I4, Q4, I5, Q5, I6, Q6, I7, Q7, I8, Q8 (repeating)
- **Streaming Mode**: Continuous read operation
- **Data Rate**: Fixed at 6.9 GB/s
- **Instantaneous Bandwidth**: 250 MHz

### Data Stream Structure

The data stream consists of interleaved in-phase (I) and quadrature (Q) samples from 8 channels. Each complete cycle through all channels produces 16 samples (8 I/Q pairs):

- Sample 0: Channel 1 In-phase (I1)
- Sample 1: Channel 1 Quadrature (Q1)
- Sample 2: Channel 2 In-phase (I2)
- Sample 3: Channel 2 Quadrature (Q2)
- ...continuing through...
- Sample 14: Channel 8 In-phase (I8)
- Sample 15: Channel 8 Quadrature (Q8)

### Reading Data

Open the device in read-only mode and perform standard POSIX `read()` operations to retrieve streaming data. The device will return 16-bit sample values in little-endian format.

---

## Configuration Interface

### Overview

The configuration interface uses a memory-mapped BRAM (Block RAM) accessible via PCIe. This interface allows bidirectional communication for system configuration and control.

### Device Path

```
/dev/xdma0_user
```

### Access Methods

Configuration data is accessed using 32-bit word-aligned reads and writes at specific byte offsets using `pread()` and `pwrite()` system calls.

**Important**: Offsets are specified in 32-bit words. The actual byte offset is `offset * 4`.

---

## Configuration Protocol

### BRAM Memory Map

| Address (words) | Field | Description |
|-----------------|-------|-------------|
| 0x00 | Start Token | Must be `0xDEADBEEF` |
| 0x01 | Status Register | Control and status flags |
| 0x02 | Schema Version | Protocol version (currently `0x01`) |
| 0x03 | Host Time | Host timestamp |
| 0x04 | Device Time | Device timestamp |
| 0x05 | Number of Parameters | Count of configuration parameters |
| 0x06 | End Header Token | Must be `0xDEADBEEF` |
| 0x07+ | Parameter Data | Variable-length parameter entries |

### Status Register Bit Definitions

| Bit | Flag Name | Direction | Description |
|-----|-----------|-----------|-------------|
| 31 | `HOST_PARAM_CHANGE` | Host → Device | Host requests parameter change |
| 30 | `PARAM_CHANGE_ACK` | Device → Host | Device acknowledges change request |
| 29 | `PARAM_CHANGE_DONE` | Host → Device | Host signals parameter update complete |
| 28 | `PARAM_CHANGE_STAT` | Device → Host | Parameter change status |
| 27 | `BRAM_SETUP_REQUEST` | Device → Host | Device requests BRAM initialization |
| 26 | `HOST_SETUP_DONE` | Host → Device | Host completed BRAM setup |
| 25 | `BRAM_SCHEMA_RETURN` | Device → Host | Schema information available |
| 24 | `BRAM_SCHEMA_VALID` | Device → Host | Schema validation status |
| 23 | `HOST_IND_OP_REQUEST` | Host → Device | Individual operation request |
| 22 | `IND_OP_ACK` | Device → Host | Operation acknowledged |
| 21 | `IND_OP_ONLINE` | Device → Host | Operation online status |
| 15-0 | Parameter Index | Bidirectional | Index of parameter being modified |

### Parameter Entry Format

Each parameter follows this structure in BRAM:

```
[Param Start Token]    0xCCCCCCCC
[Parameter ID]         Unique integer identifier
[Key Length]           Length of parameter name string
[Value Offset]         Offset to value (typically 3)
[Parameter Value]      32-bit parameter value
[Key-Value Separator]  0xBBBBBBBB
[Parameter Key String] ASCII string (4 bytes per word, null-padded)
[Param End Token]      0xEEEEEEEE
```

After all parameters:

```
[Last Param Token]     0xABABABAB
[End Token]            0xEEEEEEEE
```

### Example: DDC0_FMIX Parameter Entry

For the DDC0_FMIX parameter (parameter ID 11) with a value of 100 MHz, the BRAM contents would be:

| Address Offset | Value | Description |
|----------------|-------|-------------|
| +0 | 0xCCCCCCCC | Param start token |
| +1 | 0x0000000B | Parameter ID (11 for DDC0_FMIX) |
| +2 | 0x0000000A | Key length (10 characters: "DDC0_FMIX") |
| +3 | 0x00000003 | Value offset (3 words ahead) |
| +4 | 0x00000064 | Parameter value (100 decimal = 0x64) |
| +5 | 0xBBBBBBBB | Key-value separator |
| +6 | 0x30434444 | "DDC0" encoded as ASCII ('D'=0x44, 'D'=0x44, 'C'=0x43, '0'=0x30) |
| +7 | 0x58494D5F | "_FMI" encoded as ASCII ('_'=0x5F, 'F'=0x46, 'M'=0x4D, 'I'=0x49) |
| +8 | 0x00000058 | "X\0\0\0" encoded as ASCII ('X'=0x58, null-padded) |
| +9 | 0xEEEEEEEE | Param end token |

Note: The string encoding uses little-endian byte order, so "DDC0" becomes 0x30434444 (reading bytes right-to-left as they appear in memory).

### Initial BRAM Setup Protocol

1. **Check for Setup Request**: Read the status register at address `0x01` and check if bit 27 (`BRAM_SETUP_REQUEST`) is set. If set, proceed with BRAM initialization.

2. **Write BRAM Header**:
   - Write start token `0xDEADBEEF` to address `0x00`
   - Write the number of parameters to address `0x05`
   - Write end header token `0xDEADBEEF` to address `0x06`

3. **Write Parameters**:
   - Start at address `0x07`
   - Write each parameter following the format specified above
   - Increment address appropriately for each field

4. **Signal Completion**:
   - Read current status from address `0x01`
   - Set bit 26 (`HOST_SETUP_DONE`) in the status register
   - Write updated status back to address `0x01`
   - Wait 50 milliseconds
   - Read status again and clear bit 26
   - Write updated status back to address `0x01`

### Parameter Update Protocol

1. **Request Parameter Change**:
   - Read current status from address `0x01`
   - Set bit 31 (`HOST_PARAM_CHANGE`) in the status register
   - Write updated status back to address `0x01`

2. **Wait for Acknowledgment**:
   - Poll the status register at address `0x01`
   - Wait until bit 30 (`PARAM_CHANGE_ACK`) is set by the device

3. **Clear Request Flag**:
   - Read status from address `0x01`
   - Clear bit 31 (`HOST_PARAM_CHANGE`)
   - Write updated status back to address `0x01`

4. **Update BRAM**: Re-write the entire parameter table (or specific parameter entries) following the parameter entry format

5. **Signal Completion**:
   - Read status from address `0x01`
   - Store the parameter index in bits 15-0 of the status register
   - Set bit 29 (`PARAM_CHANGE_DONE`)
   - Write updated status back to address `0x01`

6. **Wait for Device Acknowledgment**:
   - Poll the status register at address `0x01`
   - Wait until bit 30 (`PARAM_CHANGE_ACK`) is cleared by the device

7. **Clear Done Flag**:
   - Read status from address `0x01`
   - Clear bit 29 (`PARAM_CHANGE_DONE`)
   - Write updated status back to address `0x01`

### String Encoding

Strings are encoded in 32-bit words with 4 ASCII characters per word using little-endian byte order. Strings shorter than a multiple of 4 characters should be null-padded to word boundaries.

For example, the string "HELLO" would be encoded across two 32-bit words:
- Word 1: 'H', 'E', 'L', 'L' (bytes 0-3)
- Word 2: 'O', '\0', '\0', '\0' (bytes 0-3)

Each character occupies one byte within the word, arranged in little-endian order (least significant byte first).

---

## Configuration Parameters

The system supports up to 27 configurable parameters (indices 0-26). Parameters are organized below by implementation status.

### Currently Implemented Parameters

#### Filter Path Selection (17-20)
- **LP500MHZ_EN**: 500 MHz lowpass filter enable
- **LP1GHZ_EN**: 1 GHz lowpass filter enable
- **LP2GHZ_EN**: 2 GHz lowpass filter enable
- **BYPASS_EN**: Bypass filtering

**Filter Path Control**: Only one filter path should be enabled at a time. Set the desired filter parameter to 1 and all others to 0. For example, to select the 1 GHz lowpass filter, set LP1GHZ_EN=1 and LP500MHZ_EN=0, LP2GHZ_EN=0, BYPASS_EN=0.

#### Attenuation (21)
- **ATTENUATION_BVAL**: Attenuation value in dB

#### DDC Control (Partial - 8-16)
- **DDC0_FMIX**: Mixing frequency in MHz for Digital Down Converter 0

#### System Control (Partial - 22-26)
- **CAL_EN**: Calibration mode enable

### Future Implementation (Planned)

#### Channel Control (0-7)
- CH0_EN through CH7_EN: Enable/disable individual ADC channels
- **Note**: Currently, all 8 channels continuously stream data over PCIe regardless of these settings

#### DDC Control (8-16)
- DDC0_EN, DDC1_EN, DDC2_EN: Enable Digital Down Converters
- DDC0_SFOUT: Output sample rate in Msps for DDC0
- DDC1_FMIX, DDC2_FMIX: Mixing frequency in MHz for DDC1 and DDC2
- DDC1_SFOUT, DDC2_SFOUT: Output sample rate in Msps for DDC1 and DDC2

#### System Control (22-26)
- SYSTEM_EN: System enable/disable
- ACQUIREBYSAMPLES: Acquisition mode (by sample count)
- ACQUIREBYTIME_MS: Acquisition mode (by time)
- ACQUISITIONTIME_MS: Acquisition duration in milliseconds
- NUMSAMPLES_CAPTURE: Number of samples to capture

### Data Streaming Behavior

The hardware continuously streams data from all 8 channels over PCIe at the fixed rate of 6.9 GB/s regardless of channel enable settings. Channel enable/disable functionality and acquisition control features are planned for future firmware releases.

---

## Notes and Best Practices
1. **Device Permissions**: Ensure your application has appropriate permissions to access `/dev/xdma*` devices.

2. **Timing**: The 50ms delay after setting `HOST_SETUP_DONE` is required for proper hardware synchronization.

3. **Endianness**: All multi-byte values use little-endian byte order.

4. **Word Alignment**: All BRAM accesses must be 32-bit word-aligned.
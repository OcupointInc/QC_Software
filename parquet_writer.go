package main

import (
	"encoding/json"
	"io"

	"github.com/segmentio/parquet-go"
)

// CaptureSample represents a single time sample with 8 complex channels (16 columns)
type CaptureSample struct {
	I1 int32 `parquet:"I1"`
	Q1 int32 `parquet:"Q1"`
	I2 int32 `parquet:"I2"`
	Q2 int32 `parquet:"Q2"`
	I3 int32 `parquet:"I3"`
	Q3 int32 `parquet:"Q3"`
	I4 int32 `parquet:"I4"`
	Q4 int32 `parquet:"Q4"`
	I5 int32 `parquet:"I5"`
	Q5 int32 `parquet:"Q5"`
	I6 int32 `parquet:"I6"`
	Q6 int32 `parquet:"Q6"`
	I7 int32 `parquet:"I7"`
	Q7 int32 `parquet:"Q7"`
	I8 int32 `parquet:"I8"`
	Q8 int32 `parquet:"Q8"`
}

// NewParquetWriter creates a generic parquet writer with our schema and metadata
func NewParquetWriter(w io.Writer, config *HardwareConfig) *parquet.GenericWriter[CaptureSample] {
	// Serialize config to JSON string for metadata
	configStr := "{}"
	if config != nil {
		b, _ := json.Marshal(config)
		configStr = string(b)
	}

	return parquet.NewGenericWriter[CaptureSample](w,
		parquet.KeyValueMetadata("config", configStr),
	)
}

// ParquetWriteAdapter adapts a Parquet writer to io.WriteCloser
// It buffers bytes and writes them as Parquet rows when full samples are available
type ParquetWriteAdapter struct {
	file   io.Closer
	writer *parquet.GenericWriter[CaptureSample]
	buffer []byte
}

func NewParquetWriteAdapter(f io.WriteCloser, config *HardwareConfig) *ParquetWriteAdapter {
	return &ParquetWriteAdapter{
		file:   f,
		writer: NewParquetWriter(f, config),
		buffer: make([]byte, 0),
	}
}

func (p *ParquetWriteAdapter) Write(data []byte) (int, error) {
	// Append to buffer
	p.buffer = append(p.buffer, data...)

	const bytesPerSample = 32
	// Calculate how many full samples we have
	fullSamples := len(p.buffer) / bytesPerSample
	if fullSamples == 0 {
		return len(data), nil
	}

	bytesToProcess := fullSamples * bytesPerSample
	
	// Write full samples
	if _, err := WriteRawBuffer(p.writer, p.buffer[:bytesToProcess]); err != nil {
		return 0, err
	}

	// Keep remaining bytes
	remaining := p.buffer[bytesToProcess:]
	newBuf := make([]byte, len(remaining))
	copy(newBuf, remaining)
	p.buffer = newBuf

	return len(data), nil
}

func (p *ParquetWriteAdapter) Close() error {
	if err := p.writer.Close(); err != nil {
		p.file.Close()
		return err
	}
	return p.file.Close()
}

// WriteRawBuffer parses a raw byte buffer (int16 L/E) and writes rows to the parquet writer
func WriteRawBuffer(writer *parquet.GenericWriter[CaptureSample], buf []byte) (int, error) {
	// buf contains sequence of [I0, Q0, I1, Q1 ... I7, Q7] samples (16 * 2 = 32 bytes per sample)
	// We map I0->I1, Q0->Q1 etc. based on user request (1-based indexing in schema)
	
	const bytesPerSample = 32
	numSamples := len(buf) / bytesPerSample
	rows := make([]CaptureSample, numSamples)

	for i := 0; i < numSamples; i++ {
		offset := i * bytesPerSample
		
		// Helper to read int16 le and cast to int32
		read16 := func(off int) int32 {
			return int32(int16(uint16(buf[off]) | uint16(buf[off+1])<<8))
		}

		rows[i] = CaptureSample{
			I1: read16(offset + 0),
			Q1: read16(offset + 2),
			I2: read16(offset + 4),
			Q2: read16(offset + 6),
			I3: read16(offset + 8),
			Q3: read16(offset + 10),
			I4: read16(offset + 12),
			Q4: read16(offset + 14),
			I5: read16(offset + 16),
			Q5: read16(offset + 18),
			I6: read16(offset + 20),
			Q6: read16(offset + 22),
			I7: read16(offset + 24),
			Q7: read16(offset + 26),
			I8: read16(offset + 28),
			Q8: read16(offset + 30),
		}
	}

	_, err := writer.Write(rows)
	return numSamples, err
}

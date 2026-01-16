package main

import (
	"math"
	"math/cmplx"
)

// computeFFT computes power spectrum in dBm from I/Q samples
func computeFFT(iSamples, qSamples []int16, fftSize int) []float64 {
	// Generate Blackman window and compute its sum for normalization
	window := make([]float64, fftSize)
	windowSum := 0.0
	for i := 0; i < fftSize; i++ {
		// Blackman window
		window[i] = 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1)) +
			0.08*math.Cos(4*math.Pi*float64(i)/float64(fftSize-1))
		windowSum += window[i]
	}

	// Build complex input with window applied
	input := make([]complex128, fftSize)
	for i := 0; i < fftSize; i++ {
		input[i] = complex(float64(iSamples[i])*window[i], float64(qSamples[i])*window[i])
	}

	output := fft(input)

	// Compute power in dBm and shift so DC is in center
	result := make([]float64, fftSize)
	halfSize := fftSize / 2

	// For I/Q (complex) FFT, full scale sine appears in ONE bin (no pos/neg split)
	// Reference: full-scale amplitude = 2048, after windowed FFT = 2048 * windowSum
	const fullScaleAmplitude = 32768.0
	const fullScaleDBm = 3.9
	reference := fullScaleAmplitude * windowSum

	for i := 0; i < fftSize; i++ {
		// FFT shift: move DC to center
		srcIdx := (i + halfSize) % fftSize
		mag := cmplx.Abs(output[srcIdx])

		// Convert to dBm
		var powerDBm float64
		if mag > 0 {
			powerDBm = 20*math.Log10(mag/reference) + fullScaleDBm
		} else {
			powerDBm = -150.0
		}

		result[i] = powerDBm
	}

	return result
}

// Simple radix-2 FFT implementation
func fft(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}

	// Bit-reversal permutation
	result := make([]complex128, n)
	bits := 0
	for temp := n; temp > 1; temp >>= 1 {
		bits++
	}
	for i := 0; i < n; i++ {
		j := 0
		for k := 0; k < bits; k++ {
			if i&(1<<k) != 0 {
				j |= 1 << (bits - 1 - k)
			}
		}
		result[j] = x[i]
	}

	// Cooley-Tukey iterative FFT
	for size := 2; size <= n; size *= 2 {
		halfSize := size / 2
		tableStep := n / size
		for i := 0; i < n; i += size {
			k := 0
			for j := i; j < i+halfSize; j++ {
				// Twiddle factor
				angle := -2 * math.Pi * float64(k) / float64(n)
				w := cmplx.Exp(complex(0, angle))

				t := result[j+halfSize] * w
				result[j+halfSize] = result[j] - t
				result[j] = result[j] + t
				k += tableStep
			}
		}
	}

	return result
}

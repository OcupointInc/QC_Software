import numpy as np
import matplotlib.pyplot as plt

# --- Configuration ---
FILE_PATH = 'capture.bin'
NUM_CHANNELS = 8
SAMPLE_RATE = 250e6  # 250 MHz
FFT_SIZE = 1024*2      # Higher resolution for high sample rates
NUM_AVERAGES = 50    # Number of FFT "steps" to average together

def analyze_spectrum(file_path):
    # 1. Load data as memory map (better for large files)
    # This avoids loading the whole file into RAM at once
    raw_data = np.memmap(file_path, dtype=np.int16, mode='r')
    
    # Calculate how many complex samples we have total per channel
    total_complex_samples = len(raw_data) // (2 * NUM_CHANNELS)
    
    # We need enough data for the requested number of averages
    needed_samples = FFT_SIZE * NUM_AVERAGES
    if total_complex_samples < needed_samples:
        print(f"Warning: File only has {total_complex_samples} samples. Reducing averages.")
        actual_averages = total_complex_samples // FFT_SIZE
    else:
        actual_averages = NUM_AVERAGES

    # 2. Setup Plotting
    plt.figure(figsize=(12, 8))
    freqs = np.fft.fftshift(np.fft.fftfreq(FFT_SIZE, 1/SAMPLE_RATE)) / 1e6 # MHz scale
    
    # 3. Process each channel
    for ch in range(NUM_CHANNELS):
        # Accumulator for averaging
        psd_accumulator = np.zeros(FFT_SIZE)
        
        for i in range(actual_averages):
            # Step through the file: find start index for this average
            # Index = (Sample Step * Total Channels * 2 for I/Q) + (Channel Offset)
            start_idx = i * FFT_SIZE * NUM_CHANNELS * 2
            
            # Extract one FFT block for this specific channel
            # We jump by (NUM_CHANNELS * 2) to stay on the same channel's data
            block = raw_data[start_idx : start_idx + (FFT_SIZE * NUM_CHANNELS * 2)]
            ch_data = block[ch*2::NUM_CHANNELS*2] + 1j*block[ch*2+1::NUM_CHANNELS*2]
            
            # Ensure block is correct size (pad with zeros if file ends early)
            if len(ch_data) < FFT_SIZE:
                ch_data = np.pad(ch_data, (0, FFT_SIZE - len(ch_data)))

            # Apply window and FFT
            windowed = ch_data * np.hanning(FFT_SIZE)
            fft_res = np.fft.fft(windowed)
            
            # Add the Power (magnitude squared) to our accumulator
            psd_accumulator += np.abs(np.fft.fftshift(fft_res))**2

        # Final average and convert to dB
        avg_psd = psd_accumulator / actual_averages
        magnitude_db = 10 * np.log10(avg_psd + 1e-12)
        
        plt.plot(freqs, magnitude_db, label=f'Ch {ch}', alpha=0.8)

    plt.title(f"Averaged Spectrum ({actual_averages} segments)")
    plt.xlabel("Frequency (MHz)")
    plt.ylabel("Power Spectral Density (dB)")
    plt.grid(True, which='both', linestyle='--', alpha=0.5)
    plt.legend(loc='upper right', ncol=2)
    plt.tight_layout()
    plt.show()

if __name__ == "__main__":
    analyze_spectrum(FILE_PATH)
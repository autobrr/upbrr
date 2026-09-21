# Audio-analysis processing fidelity

Scope: `internal/services/audioanalysis/`. Follow the repository and `internal/AGENTS.md` rules too. This is a reference for contributors changing waveform or spectrogram processing, not a PNG styling specification.

## Spectrogram signal contract

The reference is SoX `spectrogram -x 3000 -y 513 -z 120 -w Kaiser` on the same decoded PCM. Preserve these properties when optimizing:

- Analyze every selected channel at its decoded sample rate. Do not resample, downmix, skip audio, replace the float PCM stream with a lossy/compressed representation, or shorten the analyzed duration for speed.
- Use a 1024-sample real FFT, retaining bins 0 through 512. The 513 bins span DC through Nyquist; changing FFT size changes frequency resolution and what each row represents.
- Use SoX's default Kaiser adjustment for a 120 dB range: beta `13.740533666337532`. SoX builds the interior window over 1025 coefficients and uses the first 1024 (periodic denominator 1024, not a symmetric 1023-interval window). Its unnormalized window density is approximately `0.334927765937`.
- Derive the integer FFT step and windows per column from the input duration using SoX's rounding rules, including its 5000-pixels/second cap and integer truncation of frames per column. For a 48 kHz, 6426.411-second source with 3000 columns, that is a 343-frame step and 300 windows per column; these are examples, not constants for other durations. Do not use fewer FFT windows, larger steps, or unrelated time buckets to claim a speedup.
- Apply SoX's shortened and adjusted Kaiser windows at the beginning and end of the stream, rather than treating a full window with zero padding as equivalent.
- Accumulate squared real-FFT magnitudes per channel and frequency bin over the exact windows belonging to each native SoX column, then average by that column's window count. Use SoX's `2/window-sum` window scaling for every bin, including DC and Nyquist; dividing those two bins by an extra factor of four changes their levels by 6.02 dB. Convert the averaged power with `10*log10`; the `-120 dB` floor is a display limit, not an excuse to discard quieter input before analysis.

The column count and pixel layout are separate from this signal contract. Rendering may rescale native columns, draw different axes/labels, or change PNG dimensions and palette. It must not substitute different FFT or averaging results to make the PNG look similar. Floating-point and PNG palette rounding need measured tolerances; do not claim bit-for-bit identity with SoX solely from visual similarity.

If the decoded frame count changes the SoX hop or windows-per-column grouping, or the initial buckets compact, that first pass is not SoX-equivalent. Re-decode the affected track using the measured frame count and verify the PCM digest and frame count before publishing; fail the track if verification cannot succeed. This also applies when duration metadata is missing or overstated.

Why these are fixed: FFT length sets frequency resolution; window shape and hop set spectral leakage and time resolution; grouping and power normalization set the energy represented by each time-frequency cell. Changing any of them can produce a plausible-looking PNG that describes different audio.

## Waveform signal contract

Follow Audacity's default linear-amplitude peak envelope: preserve the signed minimum and maximum sample in each channel's time slice. RMS is off; clipping visualization is enabled here by request and marks a slice containing a sample at or beyond full scale. Do not normalize, average peaks, merge channels, or use image resizing to conceal altered extrema.

The current bounded dyadic waveform buckets preserve peak values but can spread a peak into an adjacent output pixel when a bucket crosses the final pixel boundary. Treat exact Audacity per-pixel timing as an outstanding fidelity check, not a proven property of that compaction scheme.

## Change gate

Before accepting a processing speedup, compare the DSP parameters and numerical outputs against SoX/Audacity behavior, not only final PNGs. Keep regression cases for a bin-centered tone, DC, Nyquist, start/end windows, channel isolation, clipping, and durations whose SoX step and column count differ. Benchmark the same complete source and decoded tracks before/after; do not compare a sparse-FFT profile with this dense SoX profile as if quality were unchanged. Parallelism is acceptable when the per-window and per-column results remain equivalent. Do not modify `go-fft` merely to change output quality or hide a caller-side bottleneck.

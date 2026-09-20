---
title: Audio analysis
description: Generate and download local waveform and spectrogram PNGs from prepared audio tracks.
---

# Audio analysis

Audio analysis generates waveform and spectrogram PNGs from the audio tracks already identified during release preparation. It is optional and local: the images are not uploaded to an image host, inserted into descriptions, or submitted to trackers automatically.

## Requirements

- FFmpeg must be available to the upbrr process.
- Input preparation must have identified at least one audio track.
- Each selected track must have no more than eight channels.

The operation uses the exact prepared source and stable track identities. If the source or its stream mapping changes, prepare Input again before retrying.

## Generate from the CLI

Generate both image types for the primary audio track:

```powershell
.\upbrr.exe --audio-analysis "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

Select all prepared audio tracks:

```powershell
.\upbrr.exe --audio-analysis --audio-tracks all "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

Generate only waveforms for the first and third audio tracks:

```powershell
.\upbrr.exe --audio-analysis --audio-tracks 1,3 --audio-images waveform "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

`--audio-tracks` accepts `primary`, `all`, or comma-separated positive ordinals. These are one-based positions among audio tracks, not container-wide FFmpeg stream indexes. Repeated ordinals are ignored, and selected results retain source track order.

`--audio-images` accepts `both`, `waveform`, or `spectrogram`. The default selection is `primary` with `both` image types. The two selection flags require `--audio-analysis`.

Analysis runs after preparation and before tracker submission or torrent-client effects. The CLI prints each successful PNG path and its expiry. A partial or failed result exits nonzero and prevents later effects, even when some PNGs were generated successfully.

## Generate in the Web UI

1. Prepare the release on **Input**.
2. Open **Audio Analysis** after its navigation item becomes available.
3. Choose the primary track, all tracks, or individual tracks.
4. Choose waveform, spectrogram, or both.
5. Click **Generate**.
6. Preview, open, or download each successful PNG.

Opening the page does not start work. Leaving it unopened or disabled does not block upload.

Use **Cancel** to stop active decoder work. **Disable** waits for active work to stop and then hides the current result. Disabling does not immediately delete retained files; they remain under managed retention until their displayed expiry.

## Understand the images

One waveform and one spectrogram can be produced for each selected audio track. Multi-channel tracks use one vertically stacked panel per channel so channels are not mixed together.

FFmpeg decodes the selected track to 32-bit floating-point PCM and streams it directly to the Go analyzer. upbrr does not save a complete decoded-audio file or hold the complete PCM stream in memory. It performs two streaming decode passes, one selected track at a time, to preserve complete-duration geometry with bounded memory.

Samples retain their native sample rate and channel layout. The analysis does not normalize, resample, or downmix the audio.

## Retry partial results

A failed track does not prevent later selected tracks from being attempted. The result can therefore contain successful images and bounded failure details together.

Retry without changing the prepared release, selected tracks, image types, or analysis profile to regenerate only missing or failed variants. A changed generation or incompatible selection starts a new complete analysis instead. The Web UI never presents an older generation's result as current.

## Retention and access

Generated PNGs live in upbrr-managed temporary storage and have an explicit expiry. Treat printed CLI paths and browser download URLs as temporary artifacts rather than stable library locations.

Browser and versioned API downloads remain bound to the workflow owner, analysis ID, artifact ID, and exact result revision. Artifact identifiers and URLs are opaque. See the [API reference](../api/index.md#audio-analysis-routes) for the versioned routes.

For decoder, source, or channel-limit failures, see [Audio analysis fails or is incomplete](../troubleshooting/index.md#audio-analysis-fails-or-is-incomplete).

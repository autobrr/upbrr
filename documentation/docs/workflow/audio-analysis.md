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
5. Adjust **Threads per decoder** if the default does not suit the host.
6. Click **Generate**.
7. Preview, open, or download each successful PNG.

Opening the page does not start work. Leaving it unopened or disabled does not block upload.

Use **Cancel** to stop active decoder work. **Disable** waits for active work to stop and then hides the current result. Disabling does not immediately delete retained files; they remain under managed retention until their displayed expiry.

## Tune resource use

The Web UI and CLI default to **2 threads per decoder**. Spectrograms use a fixed 3,000-column, 513-bin Kaiser-window profile.

- **Threads per decoder** controls FFmpeg's threads for each selected audio decoder. Try `1` on a small or shared server if CPU stays busy during analysis. Higher values can help some codecs, but may not shorten the run.
  API requests can set `resourceLimits.decoderThreads` to a value from 1 to 16; omitting it uses the default of 2.

Up to two audio-analysis requests can run at once. Spectrogram FFT buckets use about 6 MiB per selected channel, plus decoding and image-rendering memory, so plan for the selected track count on a shared host. Changing the decoder thread count starts a fresh analysis rather than reusing completed images from the previous attempt.

## Understand the images

One waveform and one spectrogram can be produced for each selected audio track. Multi-channel tracks use one vertically stacked panel per channel so channels are not mixed together.

FFmpeg streams decoded audio directly to the Go analyzer. Tracks that need the same image types normally share one pass over the source. If a source reports a missing or inaccurate duration, upbrr may decode an affected track again to use the measured frame count for spectrogram timing; it verifies that the decoded audio matches before publishing. Retrying missing images can also require separate passes.

Samples retain their native sample rate and channel layout. The analysis does not normalize, resample, or downmix the audio.

Waveforms show each pixel column's peak range on a linear amplitude scale. Columns containing a full-scale sample are marked in red, like Audacity's waveform view with clipping display enabled.

## Retry partial results

A failed track does not prevent later selected tracks from being attempted. The result can therefore contain successful images and bounded failure details together.

Retry without changing the prepared release, selected tracks, image types, or analysis profile to regenerate only missing or failed variants. A changed generation or incompatible selection starts a new complete analysis instead. The Web UI never presents an older generation's result as current.

## Retention and access

Generated PNGs live in upbrr-managed temporary storage and have an explicit expiry. Treat printed CLI paths and browser download URLs as temporary artifacts rather than stable library locations.

Browser and versioned API downloads remain bound to the workflow owner, analysis ID, artifact ID, and exact result revision. Artifact identifiers and URLs are opaque. See the [API reference](../api/index.md#audio-analysis-routes) for the versioned routes.

For decoder, source, or channel-layout failures, see [Audio analysis fails or is incomplete](../troubleshooting/index.md#audio-analysis-fails-or-is-incomplete).

---
title: HDR analysis
description: Generate HDR10+ and Dolby Vision plots, check scene synchronization and borders, and include audio analysis.
---

# HDR analysis

HDR analysis reads native HDR10+ and Dolby Vision metadata and produces separate plots for each available format. The CLI and Web UI default to **HDR metadata plots and primary audio**: complete native metadata plus waveform, spectrogram, and amplitude statistics for the primary audio track, without decoding video.

Use bounded scene synchronization to inspect pictures near selected metadata boundaries. Choose sampled or full-frame picture analysis when you also need decoded brightness, border, and scene evidence. Metadata plots describe values carried in the stream; decoded picture measurements describe the pictures themselves.

## Supported sources

Prepared upload targets support MKV, MP4, TS, and M2TS files with a unique HEVC video track, and selected primary HEVC playlists from ordinary, unencrypted Blu-ray content roots at angle 0. ISO files, SSIF mappings, and ambiguous video bindings are unsupported. A filename alone does not establish that dynamic metadata is present. Static HDR10 and HLG do not supply HDR10+ or Dolby Vision metadata.

For a non-disc TV pack, preparation reuses the selected first episode’s MediaInfo report for the pack’s HDR facts and target eligibility. Only that episode is offered as a prepared HDR target. Later episodes do not supply the pack’s HDR facts or HDR targets, even if their HDR properties differ; an ineligible first episode does not trigger a search through the pack.

Standalone analysis also accepts raw `.hevc` and `.h265` files. Raw HEVC support does not make those files prepared upload targets. Use metadata-only analysis when decoded checks are unavailable for the source.

Audio and picture checks require FFmpeg. Picture checks also require supported colour, geometry, and timing evidence; a source that supports native metadata extraction may still be unsupported for a decoded check. Read the individual outcome rather than treating the presence of a native plot as proof that every check ran.

## Generate in the Web UI

1. Prepare **Input**. For Blu-ray, enable **Check for HDR metadata in the selected playlists** before confirming playlist selection. Use **Review selected playlists** to revisit a prepared disc. This preparation check collects native metadata and renders plots; choose additional analysis on **HDR Analysis**.
2. Open **HDR Analysis** and select the eligible targets in source order. One eligible target is selected automatically; multiple eligible targets require selection.
3. Choose the native formats and analysis mode below. Leave **HDR metadata plots and primary audio** selected for the default operation.
4. Click **Generate**. Review each format, picture check, and audio-track outcome after processing finishes.
5. Preview or download successful artifacts. Select the picture-check images you want in descriptions, then inspect the generated descriptions before upload.

Information icons beside the target heading, options, actions and results explain scope, units and retained-work behavior. Help supports pointer, keyboard focus and touch, and does not select targets or start work. Required evidence and coverage limitations remain visible inline.

Opening the page does not start analysis. **Cancel** stops active work. **Retry** reuses retained work where compatible; **Resume** appears for a retained pending request.

### Choose native formats

| Choice                   | Behavior                                                                                       |
| ------------------------ | ---------------------------------------------------------------------------------------------- |
| **Automatic discovery**  | Discover HDR10+ and Dolby Vision independently in a shared metadata scan. This is the default. |
| **HDR10+ only**          | Request HDR10+ metadata and its plot.                                                          |
| **Dolby Vision only**    | Request Dolby Vision metadata and its L1 brightness plot.                                      |
| **Require both formats** | Analyze both native formats; a missing format prevents a complete result.                      |

The HDR10+ peak estimator defaults to **Histogram**. Other choices are **Last histogram value**, **Maximum scene component (MaxSCL)**, and **Scene-component luminance (MaxSCL)**. Histogram uses the maximum supplied distribution value; Last histogram value uses the final supplied value without sorting. The MaxSCL choices use the largest supplied component or a luminance estimate weighted from the three RGB components. Changing the estimator can reuse complete retained metadata; it does not change decoded pixels or turn metadata into a picture measurement.

### Choose the work to perform

| Mode                                          | Work and coverage                                                                                                                                                |
| --------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **HDR metadata plots and primary audio**      | Complete native metadata and primary audio, without picture decoding.                                                                                            |
| **Bounded scene synchronization**             | Complete native metadata, then limited picture windows around distributed HDR10+ and Dolby Vision boundaries. Audio is enabled by default.                       |
| **Picture checks and primary audio**          | Native metadata and complete audio, with sampled brightness, border, and scene checks. Select **Measure every video frame (slower)** for full-frame measurement. |
| **Metadata only / individual picture checks** | Native plots with optional **Scene synchronization**, **Active-area borders**, or reference comparison checks. This mode does not request audio.                 |

Audio-enabled modes offer **Analyze all audio tracks (up to 64)**. Each track retains its own outcome, waveform, spectrogram, and statistics. The primary selection uses the prepared primary track. A known absence of audio does not invalidate completed native metadata. More than 64 selected tracks is rejected rather than silently reduced to a subset.

For picture work, **Video decoder threads** accepts `0` for the mode default or `1`–`64` as a requested maximum. CPU availability and shared resource budgets can reduce the actual number. This is separate from the decoder-thread setting on the standalone **Audio Analysis** page.

## Check scene synchronization and borders

Choose **Bounded scene synchronization**, leave **Analyze audio** enabled if needed, and enable **Active-area borders** for border checks alongside scene checks.

The scene check uses luminance measurements to find cuts within bounded windows around selected native metadata boundaries. It then obtains colour thumbnails for selected visual pairs. Each pair shows the frame immediately before and at the metadata boundary; a detected cut offset is reported separately. The images do not move to hide an offset.

Contact sheets show measured candidates, visual pairs, missing coverage, and confidence. They use SDR colour previews for inspection, with grayscale fallback when colour is unavailable. Preview colour is not HDR brightness evidence: use the numeric measurements and metadata plots for that purpose.

Active-area checks compare decoded picture borders with Dolby Vision active-area metadata. In bounded mode, they sample fixed positions rather than checking every frame. Conflicting observations remain visible instead of being averaged into an assumed match.

**Bounded and sampled checks can miss short mismatches or brief peaks.** Completing all selected work does not mean the entire video was measured. An **inconclusive** result means the evidence does not establish a conclusion; it is not a pass or proof of defective media. Native metadata plots remain separate from these picture outcomes.

Bounded mode can combine audio and active-area checks. It cannot combine automatic pictures, full-frame pictures, the separate **Scene synchronization** check, or reference comparison in the same request.

## Compare a reference source

In a mode offering comparison checks, select a server-local reference file as source B. Supply an explicit integer frame offset and a common crop, then choose grade and/or colour checks. The mapping is `B frame = A frame + offset`; zero must be supplied when the sources are already aligned.

The crop is `left,top,width,height` in coded-pixel coordinates, applies to both sources, and must fit both images. Width and height must be positive even values. upbrr does not automatically align or resize the two sources. Confirm the offset and crop yourself before interpreting the plots.

The comparison selection belongs to the current prepared release and does not replace the primary input. Select it again if the generation changes or the selection expires. Unsupported reference evidence can leave the comparison inconclusive while native plots remain available.

## Generate from the CLI

Request the default metadata and primary-audio analysis during upload:

```powershell
.\upbrr.exe --hdr-analysis "D:\releases\Example.Release.2026.2160p-GRP.mkv"
```

For bounded scene synchronization, primary audio, and active-area borders:

```powershell
.\upbrr.exe --hdr-analysis --hdr-boundary-sync --hdr-active-area "D:\releases\Example.Release.2026.2160p-GRP.mkv"
```

Add `--hdr-all-audio-tracks` to analyze all prepared audio tracks, or `--hdr-boundary-audio=false` to omit audio from bounded mode. The latter requires `--hdr-boundary-sync` even when set to false.

Use `--hdr-pictures` for sampled automatic picture checks with native plots and audio, `--hdr-full-frames` for full-frame checks, or `--hdr-metadata-only` for native plots without automatic audio or picture checks. Individual flags such as `--hdr-active-area` select manual checks instead of the default audio mode; use bounded mode as shown above when you want borders together with audio.

Generate local output without configuration or upload:

```powershell
.\upbrr.exe --hdr-analysis-only --hdr-boundary-sync --hdr-active-area --hdr-output-dir "D:\reports\hdr" "D:\releases\Example.Release.2026.2160p-GRP.mkv"
```

For native plots from a Blu-ray content root:

```powershell
.\upbrr.exe --hdr-analysis-only --hdr-metadata-only --hdr-output-dir "D:\reports\hdr" --hdr-playlist 00001 "D:\releases\Example.Disc.2026"
```

Omit `--hdr-playlist` only when metadata identifies exactly one supported, non-looping primary HEVC candidate. upbrr does not choose the largest stream file. Each standalone run creates a fresh child directory under the output parent for its artifacts and JSON summary. The output parent must be outside a disc source, including through directory aliases.

One eligible prepared upload target is selected automatically. For multiple targets, supply their opaque IDs in source order with `--hdr-targets <id,id>`. Every selected target needs usable native metadata or, when audio was requested, usable audio output. A partial result can satisfy this requirement; canceled and interrupted attempts cannot. Standalone and upload analysis modes are mutually exclusive; standalone accepts exactly one source and its HDR/unattended options. See the [CLI reference](../cli/index.md#hdr-analysis-options) for all flags and retained-workflow controls.

## Include results in descriptions

Native plots and successful selected audio images/statistics are included by default. Picture-check images require explicit selection on **HDR Analysis**. Successful assets from partial results remain usable in descriptions and can satisfy requested upload analysis when every selected target has usable native metadata or requested audio output.

Generated descriptions keep **HDR plots** and **Audio Plots** separate, including when audio was generated through HDR analysis. Images use each tracker's selected image host. Analysis images do not consume ordinary screenshot slots. Ordinary screenshots retain their configured screenshot header and applicable tonemapped notice.

Final edited descriptions remain authoritative. After changing analysis selection, inspect the regenerated description or reset an edited group deliberately; do not assume a saved final edit has been rebuilt.

## Progress, retries, and retention

Progress reports phases such as native metadata reading, audio decoding, picture measurement, boundary-window preparation, colour preview rendering, and saving. Percentages describe completed work, not remaining wall-clock time. Audio decoding can finish before rendering and publication; a decode percentage alone is not the final operation result. Read the terminal status and any failure message.

Complete native metadata is retained independently for each format. Retrying rendering or changing the HDR10+ estimator can reuse it without a fresh native scan. Compatible decoded measurements can also be rendered again without measuring the pictures again. Successful artifacts remain inspectable after a partial attempt.

A new decoded measurement pass requires explicit retry of the unsuccessful modes against the current retained attempt. This does not authorize another native extraction. Changed sources require preparing Input again; missing or damaged retained native metadata needs recovery rather than silently using stale evidence.

Compatible saved outputs and hosted links can be restored when reopening a release. Reuse verifies source and artifact identity; changes to the image-host account require a new upload. Managed outputs have no time-based expiry, but workflow deletion, source retirement, replacement, or temporary-directory cleanup can remove them. Standalone outputs remain in the chosen output directory.

See [HDR troubleshooting](../troubleshooting/index.md#hdr-analysis-is-partial-or-inconclusive) and the [API reference](../api/index.md#hdr-analysis-routes).

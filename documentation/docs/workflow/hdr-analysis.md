---
title: HDR analysis
description: Generate HDR10+ brightness plots from MKV files and selected Blu-ray playlists.
---

# HDR analysis

HDR analysis produces an opaque 3000 × 1200 PNG showing dynamic HDR10+ brightness metadata across all presentation frames. Supported sources are MKV files with MediaInfo-confirmed HDR10+ on one HEVC video track and ordinary, unencrypted Blu-ray content roots with a selected primary HEVC playlist at angle 0. ISO files, SSIF mappings, and ambiguous video bindings are unsupported. Static HDR, HLG, or Dolby Vision alone do not provide HDR10+ measurements. A filename containing HDR10+ does not establish eligibility.

## Generate in the Web UI

1. Prepare **Input**. For Blu-ray, check **Check for HDR10+ in the selected playlists** before confirming playlist selection. This scans streams even when text reports are cached and automatically renders plots for confirmed playlists. The plots are ready when you open **HDR Analysis**. Use **Review selected playlists** to check an already prepared selection.
2. Open **HDR Analysis** and select the eligible prepared targets. A single eligible target is selected by default; multiple eligible targets require an explicit selection. Playlists without confirmed HDR10+ remain disabled.
3. Choose a peak estimator and click **Generate**.
4. Preview or download successful plots. Completed analysis is automatically included in generated descriptions.

The default estimator is **Histogram**. Other choices are **Histogram 99th percentile**, **MaxSCL**, and **MaxSCL luminance**. Changing the estimator reuses complete retained metadata. These are metadata estimators, rather than measurements of decoded pixel brightness.

For automatic descriptions, upbrr uploads plots using each tracker's selected image host and adds a separate `[spoiler=source_hdr]` block. HDR plots do not consume screenshot slots. Final edited descriptions remain authoritative.

## Generate from the CLI

Request HDR analysis during upload:

```powershell
.\upbrr.exe --hdr-analysis "D:\releases\Synthetic.HDR.2026.2160p-GRP.mkv"
```

One eligible prepared target is selected automatically, even when other files are ineligible. For several eligible targets, supply their opaque IDs with `--hdr-targets <id,id>`, in source order. The upload workflow proceeds only when every requested target completes.

Generate local output without configuration or upload:

```powershell
.\upbrr.exe --hdr-analysis-only --hdr-output-dir "D:\reports\hdr" "D:\releases\Synthetic.HDR.2026.2160p-GRP.mkv"
```

For a Blu-ray content root, select a numeric playlist basename:

```powershell
.\upbrr.exe --hdr-analysis-only --hdr-output-dir "D:\reports\hdr" --hdr-playlist 00001 "D:\releases\Synthetic.Disc.2026"
```

Omit `--hdr-playlist` only when metadata identifies exactly one supported, non-looping primary HEVC candidate. upbrr does not choose the largest stream file. Standalone analysis creates a new child directory under the output parent, writes a PNG and JSON summary, and never replaces an existing result. The output parent must be outside a disc source, including through directory aliases.

`--hdr-peak-source` accepts `histogram`, `histogram99`, `max-scl`, or `max-scl-luminance`. `--hdr-playlist` and `--hdr-output-dir` require standalone mode; `--hdr-targets` requires upload mode. The two analysis modes are mutually exclusive. Standalone mode accepts only HDR options and unattended options, with exactly one source.

## Retry and retain results

The first MKV analysis reads the original source. Its progress shows structure discovery followed by metadata collection, with the current read position and overall percentage. Progress is based on completed phases and source position, rather than elapsed time. Blu-ray preparation collects metadata and renders plots when its playlist checkbox is checked; later estimator changes reuse that captured metadata. CLI uploads requesting HDR analysis collect it during preparation. One native extraction or rendering session runs at a time across the application; queued requests can be canceled.

Complete metadata and verified HDR10+ absence are retained privately before rendering starts. A render failure or cancellation can therefore be retried without reading the source again. Successful plots from a partial attempt remain available for preview and download, but a partial result cannot be included in descriptions or satisfy requested upload analysis. Choose **Retry** or generate with fewer targets.

Missing or damaged retained metadata requires explicit recovery when a new plot must be rendered. Source changes require preparing Input again. Compatible saved PNGs still present in the temporary directory are restored with their existing hosted links, even when their extraction metadata is unavailable. Reuse verifies the source, plot contents, caption, estimator, and rendering profile; a changed hosting account requires a new upload. Retained metadata is copied when compatible saved work is rebound to another prepared generation; plot captions are regenerated when needed. Managed outputs have no time-based expiry, but workflow deletion, source retirement, or temporary-directory cleanup can remove them. Standalone outputs remain in the chosen output directory.

See the [CLI reference](../cli/index.md#hdr-analysis-options) and [API reference](../api/index.md#hdr-analysis-routes) for the transport surfaces.

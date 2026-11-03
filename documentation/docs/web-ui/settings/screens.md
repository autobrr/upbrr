---
title: Screenshot capture settings
description: Configure screenshot counts, partial image-upload success, tone mapping, overlays, and concurrency.
---

# Screenshot capture settings

Use **Settings → Screenshot capture** for generated screenshots, automatic DVD menu captures, and image-upload concurrency.

| Field                                  | Default | Effect                                                                                                      |
| -------------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------- |
| **Automatic screenshot count**         | `4`     | Number of screenshots requested during automatic generation. Must be greater than zero.                     |
| **Maximum DVD menu images**            | `6`     | Caps automatic distinct DVD menu captures. Accepted range is `0`–`32`; `0` resolves to `6`.                 |
| **Minimum successful images per host** | `3`     | Accepts a partially failed host batch after this many images publish. `0` requires every image.             |
| **Show frame information on captures** | Off     | Adds frame number, frame type, and a tone-mapped HDR marker when applicable.                                |
| **Overlay text size**                  | `18`    | Sets overlay text size relative to a 1080-line frame.                                                       |
| **Tone map HDR screenshots**           | On      | Tone maps HDR captures for display.                                                                         |
| **Use Vulkan/libplacebo tone mapping** | Off     | Tries Vulkan/libplacebo tone mapping for previews and captures when tone mapping is on, including overlays. |

Frame type reports FFmpeg's picture type (I, P, or B), or **Unknown** when that information is unavailable. Enabling the overlay does not change which frame is selected.

When tone mapping is applied, the overlay identifies the source format from inspected media metadata, such as **HLG**, **HDR10**, or **Dolby Vision Profile 5**, separately from the **Tonemapped** treatment. Reported compatibility formats remain labelled **compatible**; they do not identify the output colour space or prove which layer was processed. Missing or name-only evidence shows **HDR type unknown**. Incomplete or conflicting source evidence is qualified, and an unknown Dolby Vision profile is never guessed. No tonemapped marker appears when tone mapping is disabled or falls back to non-tonemapped output. Existing images retain their original overlays; recapture them to get the new source labels.

## Advanced fields

| Field                               | Default  | Effect                                                                                     |
| ----------------------------------- | -------- | ------------------------------------------------------------------------------------------ |
| **Concurrent capture processes**    | `2`      | Caps concurrent FFmpeg screenshot processes when **Limit capture concurrency** is enabled. |
| **Concurrent image uploads**        | `6`      | Caps concurrent image uploads; values at or below zero resolve to one worker.              |
| **Limit capture concurrency**       | Off      | Enables the process limit.                                                                 |
| **PNG compression level**           | `6`      | Sets PNG compression level; accepted range is `0`–`9`.                                     |
| **Software tone-mapping algorithm** | `mobius` | Selects the software FFmpeg tone-map algorithm.                                            |
| **Software highlight desaturation** | `10.0`   | Passes the desaturation value to the software tone-map filter.                             |

**Cutoff screens** remains in the schema but has no current runtime reader.

For Dolby Vision Profile 5 inputs, enable **Tone map HDR screenshots** and **Use Vulkan/libplacebo tone mapping**, including when **Show frame information on captures** is enabled. Profile 5 needs Dolby Vision-aware processing to avoid green/purple screenshots. This requires a compatible FFmpeg build and Vulkan setup; Profile 5 skips software tone mapping because its base layer is incompatible; a last-resort non-tonemapped image is visibly marked as degraded, and its colours may be inaccurate. HDR-compatible Dolby Vision and HDR10 sources can use the configured software path. See the [FFmpeg libplacebo documentation](https://ffmpeg.org/ffmpeg-filters.html#libplacebo).

## Verify the change

Save, open **Screenshots**, generate a small set, and inspect output before uploading. Preview and final capture use the same renderer policy. Libplacebo consumes decoded Dolby Vision metadata. Each renderer receives only the selected decoded frame, so buffering cannot select a later frame. Profile 5 is marked degraded if decoded Dolby Vision metadata is unavailable. A transient device failure permits one retry. Unsupported capabilities skip that retry; rendering can then use compatible software tone mapping and finally a valid non-tonemapped image. Disabling libplacebo never enables it during recovery. Disabling tone mapping skips both tonemappers.

Successful previews and generated images report the actual renderer and whether tone mapping was applied. Ordinary CLI logs report the same successful capture outcomes, including unattended runs. Each recovered image also shows its requested renderer and failed or skipped stage. Non-tonemapped fallback carries a colour warning in the WebUI and CLI. A batch can contain different processing outcomes; the overlay and generated description use each image’s actual tone-mapping evidence.

Renderer fallback preserves the selected frame. Independent black-frame or disc-segment recovery can move it; inspect the resolved capture or preview time before using the image. Invalid or corrupt output is rejected, and cancellation, deadlines, and explicit decoder corruption stop recovery.

Tone mapping changes apply to new captures, not pixels in existing images. Saved screenshots retain content-bound renderer outcomes and processing evidence for later descriptions; replacing an image does not transfer the old image's evidence.

This version adds screenshot-processing fields to the state database. Before testing a development build, stop the service and back up the whole state directory, or use a separate test state directory. Changing back to an older binary or image tag does not undo database changes; restore the matching pre-test state when returning to that release.

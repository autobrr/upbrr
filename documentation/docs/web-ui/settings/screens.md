---
title: Screenshot capture settings
description: Configure screenshot counts, partial image-upload success, tone mapping, overlays, and concurrency.
---

# Screenshot capture settings

Use **Settings → Screenshot capture** for generated screenshots, automatic DVD menu captures, and image-upload concurrency.

| Field                                  | Default | Effect                                                                                          |
| -------------------------------------- | ------- | ----------------------------------------------------------------------------------------------- |
| **Automatic screenshot count**         | `4`     | Number of screenshots requested during automatic generation. Must be greater than zero.         |
| **Maximum DVD menu images**            | `6`     | Caps automatic distinct DVD menu captures. Accepted range is `0`–`32`; `0` resolves to `6`.     |
| **Minimum successful images per host** | `3`     | Accepts a partially failed host batch after this many images publish. `0` requires every image. |
| **Show frame information on captures** | Off     | Adds frame number, frame type, and a tone-mapped HDR marker when applicable.                    |
| **Overlay text size**                  | `18`    | Sets overlay text size relative to a 1080-line frame.                                           |
| **Tone map HDR screenshots**           | On      | Tone maps HDR captures for display.                                                             |
| **Use Vulkan/libplacebo tone mapping** | Off     | Tries Vulkan/libplacebo tone mapping when tone mapping is on and frame overlay is off.          |

Frame type reports FFmpeg's picture type (I, P, or B), or **Unknown** when that information is unavailable. Enabling the overlay does not change which frame is selected.

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

For Dolby Vision Profile 5 inputs, enable **Tone map HDR screenshots** and **Use Vulkan/libplacebo tone mapping**, and disable **Show frame information on captures**. Profile 5 needs Dolby Vision-aware processing to avoid green/purple screenshots. This requires a compatible FFmpeg build and Vulkan setup; capture failures can still fall back to software tone mapping. See the [FFmpeg libplacebo documentation](https://ffmpeg.org/ffmpeg-filters.html#libplacebo).

## Verify the change

Save, open **Screenshots**, generate a small set, and inspect output before uploading. Libplacebo retries and falls back to the software chain when its capture path fails.

Tone mapping changes apply to new captures, not pixels in existing images. Saved screenshots retain their processing evidence for later descriptions; replacing an image does not transfer the old image's evidence.

This version adds screenshot-processing fields to the state database. Before testing a development build, stop the service and back up the whole state directory, or use a separate test state directory. Changing back to an older binary or image tag does not undo database changes; restore the matching pre-test state when returning to that release.

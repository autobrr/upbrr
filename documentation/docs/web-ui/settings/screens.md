---
title: Screens settings
description: Configure screenshot counts, partial image-upload success, tone mapping, overlays, and concurrency.
---

# Screens settings

Use **Settings → Screens** for generated screenshots, automatic DVD menu captures, and image-upload concurrency.

| Field                            | Default | Effect                                                                                          |
| -------------------------------- | ------- | ----------------------------------------------------------------------------------------------- |
| **Screens**                      | `4`     | Number of screenshots requested during automatic generation. Must be greater than zero.         |
| **Maximum DVD menu images**      | `6`     | Caps automatic distinct DVD menu captures. Accepted range is `0`–`32`; `0` resolves to `6`.     |
| **Min successful image uploads** | `3`     | Accepts a partially failed host batch after this many images publish. `0` requires every image. |
| **Frame overlay**                | Off     | Adds frame number, frame type, and a tone-mapped HDR marker when applicable.                    |
| **Overlay text size**            | `18`    | Sets overlay text size relative to a 1080-line frame.                                           |
| **Tone map**                     | On      | Tone maps HDR captures for display.                                                             |
| **Use libplacebo**               | Off     | Tries Vulkan/libplacebo tone mapping when tone mapping is on and frame overlay is off.          |

## Advanced fields

| Field                      | Default  | Effect                                                                        |
| -------------------------- | -------- | ----------------------------------------------------------------------------- |
| **Process limit**          | `2`      | Caps concurrent FFmpeg screenshot processes when **FFmpeg limit** is enabled. |
| **Max concurrent uploads** | `6`      | Caps concurrent image uploads; values at or below zero resolve to one worker. |
| **FFmpeg limit**           | Off      | Enables the process limit.                                                    |
| **FFmpeg compression**     | `6`      | Sets PNG compression level; accepted range is `0`–`9`.                        |
| **Tonemap algorithm**      | `mobius` | Selects the software FFmpeg tone-map algorithm.                               |
| **Desat**                  | `10.0`   | Passes the desaturation value to the software tone-map filter.                |

**Cutoff screens** remains in the schema but has no current runtime reader.

## Verify the change

Save, open **Screenshots**, generate a small set, and inspect output before uploading. Libplacebo retries and falls back to the software chain when its capture path fails.

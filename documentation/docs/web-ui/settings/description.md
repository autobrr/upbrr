---
title: Description settings
description: Configure shared description artwork, screenshot layout, headers, episode text, and signatures.
---

# Description settings

Use **Settings → Description** to control shared description builders. Trackers can apply their own markup and field rules, so confirm each rendered result on **Descriptions**.

## Layout and artwork

| Field                                   | Default | Effect                                                                                      |
| --------------------------------------- | ------- | ------------------------------------------------------------------------------------------- |
| **Include title logo**                  | Off     | Adds a TMDB logo when one is available. Requires TMDB enrichment.                           |
| **Logo width (pixels)**                 | `300`   | Sets the rendered logo width; non-positive values resolve to `300`.                         |
| **Preferred logo languages**            | Empty   | Supplies preferred TMDB logo languages.                                                     |
| **Screenshot thumbnail width (pixels)** | `350`   | Sets shared screenshot and disc-menu thumbnail width; non-positive values resolve to `350`. |
| **Screenshots per description row**     | Empty   | Sets images per shared description row. Empty, invalid, or non-positive values use `2`.     |
| **Include Blu-ray.com cover images**    | Off     | Adds selected blu-ray.com cover images when available.                                      |
| **Disc cover width (pixels)**           | `250`   | Sets those cover-image widths; non-positive values resolve to `250`.                        |

## Text blocks

| Field                                | Default         | Effect                                                                                                                                                                              |
| ------------------------------------ | --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Include episode overview**         | Off             | Adds episode overview text to the generated description where supported when available.                                                                                             |
| **Tone-mapped screenshot notice**    | Built-in notice | Adds the configured notice only when every included screenshot is known to have been tone mapped. Mixed selections show counts of tone-mapped, not-tone-mapped, and unknown images. |
| **Description introduction**         | Empty           | Prepends custom markup in tracker builders that support the shared header.                                                                                                          |
| **Screenshot section heading**       | Empty           | Adds markup immediately before the screenshot block where supported.                                                                                                                |
| **DVD menu section heading**         | Empty           | Adds markup immediately before the DVD menu-image block where supported.                                                                                                            |
| **Description signature**            | Empty           | Appends custom markup in builders that support the shared signature.                                                                                                                |
| **Include Blu-ray.com release link** | Off             | Adds the selected blu-ray.com release URL when available.                                                                                                                           |

Screenshot processing evidence belongs to the image and its saved hosted URL, not the current Tone map setting. Reused images keep their recorded processing across restarts. Legacy and externally supplied images remain unknown unless recorded image evidence establishes their processing.

Header and signature values are tracker markup, commonly BBCode. Preview them before submission; malformed or unsupported markup is not made portable automatically.

Generated analysis uses separate **Audio Plots** and **HDR plots** sections. Audio generated through HDR analysis stays in **Audio Plots**. Analysis images do not consume ordinary screenshot slots or replace the screenshot block: the configured **Screenshot section heading** and applicable **Tone-mapped screenshot notice** still belong with ordinary screenshots. Picture-check thumbnails are selected on **HDR Analysis**; their tone mapping is for inspection and does not change the ordinary screenshot selection.

Imported comparison blocks remain in the description rather than becoming ordinary screenshots. Review the destination-specific formatting on **Descriptions**. **Save group** retains the complete edited output without appending generated sections again; use **Reset group** to rebuild the group from its generated description.

:::note Compatibility fields

**Multi screens**, **Pack thumb size**, **Char limit**, **File limit**, and Description's **Process limit** remain in the schema but have no current runtime reader.

:::

## Verify the change

Save, prepare `Example.Release.2026.1080p-GRP`, generate its descriptions, and inspect every tracker tab. Artwork options need matching provider data; an empty result does not necessarily mean the setting failed.

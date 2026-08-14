---
title: Description settings
description: Configure shared description artwork, screenshot layout, headers, episode text, and signatures.
---

# Description settings

Use **Settings → Description** to control shared description builders. Trackers can apply their own markup and field rules, so confirm each rendered result on **Descriptions**.

## Layout and artwork

| Field                 | Default | Effect                                                                                      |
| --------------------- | ------- | ------------------------------------------------------------------------------------------- |
| **Add logo**          | Off     | Adds a TMDB logo when one is available. Requires TMDB enrichment.                           |
| **Logo size**         | `300`   | Sets the rendered logo width; non-positive values resolve to `300`.                         |
| **Logo language**     | Empty   | Supplies preferred TMDB logo languages.                                                     |
| **Thumbnail size**    | `350`   | Sets shared screenshot and disc-menu thumbnail width; non-positive values resolve to `350`. |
| **Screens per row**   | Empty   | Sets images per shared description row. Empty, invalid, or non-positive values use `2`.     |
| **Use Bluray images** | Off     | Adds selected blu-ray.com cover images when available.                                      |
| **Bluray image size** | `250`   | Sets those cover-image widths; non-positive values resolve to `250`.                        |

## Text blocks

| Field                         | Default         | Effect                                                                      |
| ----------------------------- | --------------- | --------------------------------------------------------------------------- |
| **Episode overview**          | Off             | Adds episode overview text to the shared Unit3D description when available. |
| **Tonemapped header**         | Built-in notice | Adds the configured notice when screenshots were tone mapped.               |
| **Custom description header** | Empty           | Prepends custom markup in tracker builders that support the shared header.  |
| **Screenshot header**         | Empty           | Adds markup immediately before the screenshot block where supported.        |
| **Disc menu header**          | Empty           | Adds markup immediately before the DVD menu-image block where supported.    |
| **Custom signature**          | Empty           | Appends custom markup in builders that support the shared signature.        |
| **Add Bluray link**           | Off             | Adds the selected blu-ray.com release URL when available.                   |

Header and signature values are tracker markup, commonly BBCode. Preview them before submission; malformed or unsupported markup is not made portable automatically.

:::note Compatibility fields

**Multi screens**, **Pack thumb size**, **Char limit**, **File limit**, and Description's **Process limit** remain in the schema but have no current runtime reader.

:::

## Verify the change

Save, prepare `Example.Release.2026.1080p-GRP`, generate its descriptions, and inspect every tracker tab. Artwork options need matching provider data; an empty result does not necessarily mean the setting failed.

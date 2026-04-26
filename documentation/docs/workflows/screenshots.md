---
sidebar_position: 3
title: Screenshots
---

# Screenshots

Screenshot handling is split into planning, generation, final selection, image upload, and tracker link management.

## Planning

The screenshot plan uses media duration, frame rate, configuration, and release metadata to propose selections.

If duration or frame rate is unavailable, manual frame selections may be required.

## Generation

Generate screenshots only after the source path and metadata are correct. Review final selections before uploading to an image host.

## Image hosting

Configured image hosts determine where screenshots are uploaded. Keep host credentials in config and avoid logging credentials or full secret-bearing payloads.

## Tracker links

Tracker upload payloads consume final image links. Delete or regenerate links when a screenshot selection changes.

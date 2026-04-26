---
sidebar_position: 1
title: Upload Workflow
---

# Upload workflow

The shared core prepares a release in stages. CLI, GUI, and web mode should preserve parity where the same behavior exists.

## 1. Select input

Choose a file, folder, disc structure, or queue root. For BDMV sources, playlist discovery can persist the selected playlist.

## 2. Resolve metadata

Metadata lookup can use detected release details, external IDs, tracker requirements, and manual overrides. Review warnings before upload.

## 3. Check dupes and rules

Dupe checks and tracker rules can block or skip uploads. Override behavior should be explicit and visible before upload.

## 4. Prepare screenshots and images

Generate screenshots, choose final selections, upload to configured image hosts, and keep tracker image links aligned with the upload target.

## 5. Build descriptions

Description groups render tracker-specific BBCode from metadata, mediainfo, screenshots, and configured formatting.

## 6. Review tracker upload

Run a dry-run preview before a real upload when changing configuration, tracker settings, or automation behavior.

## 7. Upload and post-process

After upload, `upbrr` can create torrents and hand completed work to configured seeding or watch-folder destinations.

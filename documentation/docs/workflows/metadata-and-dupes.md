---
sidebar_position: 2
title: Metadata and Dupes
---

# Metadata and dupes

Metadata resolution combines detected release information, tracker expectations, external IDs, and explicit user overrides.

## External IDs

The GUI exposes editable TMDB, IMDb, TVDB, and TVmaze IDs. CLI flags can provide manual IDs when detection is not enough.

## Naming overrides

Release-name overrides cover attributes such as category, type, source, resolution, tag, service, edition, season, episode, year, and daily date.

Use overrides to correct the current release. Keep persistent tracker and metadata defaults in config.

## Dupe checks

Dupe checks run per tracker where supported. Some trackers require manual checks or return notes instead of API-backed results.

When automation cannot safely decide, prefer a clear skip, dry-run, or site-check outcome over attempting an uncertain upload.

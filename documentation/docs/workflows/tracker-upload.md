---
sidebar_position: 5
title: Tracker Uploads
---

# Tracker uploads

Tracker uploads use shared request types under `pkg/api` and tracker implementations under `internal/trackers`.

## Implementation groups

Tracker-specific upload behavior lives under `internal/trackers/impl`. Current implementation groups include:

`AITHER`, `ANT`, `AR`, `ASC`, `AZFAMILY`, `BHD`, `BHDTV`, `BJS`, `BT`, `BTN`, `COMMONHTTP`, `DC`, `FF`, `FL`, `GPW`, `HDB`, `HDS`, `HDT`, `IS`, `MTV`, `NBL`, `PTP`, `PTS`, `RTF`, `SPD`, `THR`, `TL`, `TVC`, and `UNIT3D`.

Some groups are shared foundations rather than a single upload target. For example, `COMMONHTTP` contains reusable HTTP helpers, and `UNIT3D` covers many Unit3D-based sites.

## Dry run first

Run a dry run when changing tracker config, overrides, description groups, screenshot links, or unattended behavior:

```bash
go run ./cmd/upbrr --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
```

## Rule and dupe overrides

Rule failures and dupes should be skipped or overridden explicitly. If one surface supports a skip or override, keep parity in the other surfaces when the behavior is shared.

## Retry behavior

Retry failed uploads only after reviewing the failed tracker state and correcting the relevant config, payload, image links, or tracker-specific questionnaire answers.

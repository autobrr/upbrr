---
sidebar_position: 5
title: Tracker Uploads
---

# Tracker uploads

Tracker uploads use shared request types under `pkg/api` and tracker implementations under `internal/trackers`.

## Dry run first

Run a dry run when changing tracker config, overrides, description groups, screenshot links, or unattended behavior:

```bash
go run ./cmd/upbrr --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
```

## Rule and dupe overrides

Rule failures and dupes should be skipped or overridden explicitly. If one surface supports a skip or override, keep parity in the other surfaces when the behavior is shared.

## Retry behavior

Retry failed uploads only after reviewing the failed tracker state and correcting the relevant config, payload, image links, or tracker-specific questionnaire answers.

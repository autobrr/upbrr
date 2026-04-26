---
sidebar_position: 6
title: Troubleshooting
---

# Troubleshooting

## TMDB metadata fails

Confirm `main_settings.tmdb_api` is set and valid.

## GUI or web mode cannot browse paths

Remote web sessions may not support native browsing. Enter the server-side path manually or adjust the browse policy for local trusted use.

## Config import fails

Validate the source file format and extension. YAML, JSON, and legacy Upload Assistant `config.py` imports are supported.

## Screenshots require manual frames

If duration or frame rate cannot be detected, enter manual frames before capture.

## Upload should not proceed

Use `--dry-run` or `--site-check` while investigating. For unattended paths, prefer safe skip or clear failure over continuing with uncertain state.

---
title: Trackers
description: Configure tracker credentials, authentication, default selection, duplicate checks, and tracker-specific review.
---

# Trackers

upbrr's tracker catalog is built from registered tracker implementations. The Web UI renders each tracker's supported settings and capabilities from that catalog.

## Configure a tracker

1. Open **Settings**.
2. Open the tracker section.
3. Enter only the requested credentials and options.
4. save settings;
5. import cookies, sign in, or test authentication when those actions are available;
6. add the tracker to default selection only after it reports ready.

Auth requirements vary. A tracker can require an API key, passkey, cookie session, username/password login, 2FA, or a supported combination. upbrr stores managed tracker cookies encrypted in SQLite.

Never paste tracker credentials, cookies, announce URLs, OTP secrets, or raw auth failures into issues or chat.

## Defaults and per-run selection

- **Default trackers** seed the initial tracker set.
- **Preferred tracker** influences workflows that need one preferred source of tracker data.
- CLI `--trackers` selects a comma-separated set for one run.
- CLI `--trackers-remove` removes trackers from that run.
- The Web UI lets you select trackers from the Input page.

The final upload authority is the exact tracker subset approved after duplicate review. Later stages must not silently add disabled, blocked, or unapproved trackers.

## Duplicate checks and rules

Tracker adapters normalize duplicate results into a common review surface. A tracker can return:

- a completed search with zero or more matches;
- a not-run result with a reason, such as missing auth or metadata;
- an attempted search failure.

Rule and validation outcomes can block a live upload while still allowing debug preparation so you can inspect later stages. Subjective or incomplete tracker rules remain manual decisions.

`--skip-dupe-check` and similar bypasses remove safeguards. Use them only when you have manually completed the equivalent tracker checks.

## Names and payloads

upbrr resolves tracker-specific upload and search names before duplicate checking. Review the projected name for every tracker. The eventual payload uses that reviewed name rather than deriving a new name at submission time.

Tracker-specific categories, source/type mappings, descriptions, media selection, questionnaires, and auth flows remain owned by the tracker adapter. A successful mapping does not prove the upload complies with every current site rule.

## Image hosts and clients

Trackers can restrict usable image hosts or select tracker-specific image/client overrides. Configure a compatible host before media preparation. Confirm the final hosted links and client injection settings per tracker.

## Add or update tracker support

Tracker implementation work requires registry, auth, naming, duplicate-search, validation, payload, and test contracts. Read [ADDING_TRACKERS.md](https://github.com/autobrr/upbrr/blob/main/ADDING_TRACKERS.md) before changing code.

For a tracker defect, report the smallest sanitized reproduction. Omit private rules, credentials, response bodies, and private URLs unless tracker staff have authorized publication.

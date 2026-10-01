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

Tracker settings can restrict duplicate competition by incoming release group. When a tag matches that tracker's **Duplicate bypass groups** or **Internal groups**, confirmed candidates from other groups coexist and are omitted from slot-capacity decisions. Exact duplicates, same-group candidates, conflicting evidence, and unknown groups keep their normal duplicate result. See [tracker group policy lists](../web-ui/settings/trackers.md#group-policy-lists).

`--skip-dupe-check` and similar bypasses remove safeguards. Use them only when you have manually completed the equivalent tracker checks.

### Approve tracker warnings

Some tracker warnings permit an explicit override. On **Dupe Check**, read the warnings on the affected tracker's card. Choose **Upload anyway** only after deciding that the release is appropriate for that tracker.

Approval applies only to that tracker and its current warning set. Changed warnings require renewed approval. Strict failures remain blocked and cannot be overridden, including in debug mode.

The interactive CLI and `--unattended_confirm` prompt for approval. Strict `--unattended` declines without prompting and skips that tracker; other eligible trackers can continue. Debug mode bypasses warnings that permit an override.

## Names and payloads

upbrr resolves tracker-specific upload and search names before duplicate checking. Review the projected name for every tracker. The eventual payload uses that reviewed name rather than deriving a new name at submission time.

Tracker-specific categories, source/type mappings, descriptions, media selection, questionnaires, and auth flows remain owned by the tracker adapter. A successful mapping does not prove the upload complies with every current site rule.

### Disc region and distributor IDs

For Unit3D disc uploads, upbrr translates known country codes and publisher names to the standard UNIT3D numeric IDs. Matching is case-insensitive; explicit positive numeric IDs remain usable for site-specific entries. Tracker taxonomy implementations can add or override names without changing the shared defaults. Disc playback zones such as A/B/C are not country codes.

Unknown optional values are omitted with a debug diagnostic rather than assigned a guessed ID. ACM and SHRI retain their stricter validation, and SHRI still requires a valid region for DVD and HD DVD uploads. Site-specific catalogs and live upload acceptance still need verification when a site changes its list.

### ACM diagnostics

ACM duplicate searches gather the full TMDB work within the movie or TV category. They do not narrow by release name, season, type, or resolution; the duplicate evaluator compares the returned content scopes and variants. This preserves work matching on ACM's legacy API.

For an ACM report, enable `--log-level trace` for the affected run. Search diagnostics include the work query and pagination decisions. Payload diagnostics include numeric classification IDs and evidence byte counts, without copying descriptions or MediaInfo into these messages. Review all logs before sharing and remove credentials, private URLs, and identifying release details. A successful local preparation does not confirm that ACM accepted an upload.

## Image hosts and clients

Trackers can restrict usable image hosts or select tracker-specific image/client overrides. Configure a compatible host before media preparation. Confirm the final hosted links and client injection settings per tracker.

## Add or update tracker support

Tracker implementation work requires registry, auth, naming, duplicate-search, validation, payload, and test contracts. Read [ADDING_TRACKERS.md](https://github.com/autobrr/upbrr/blob/main/ADDING_TRACKERS.md) before changing code.

For a tracker defect, report the smallest sanitized reproduction. Omit private rules, credentials, response bodies, and private URLs unless tracker staff have authorized publication.

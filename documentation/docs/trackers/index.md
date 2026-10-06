---
title: Trackers
description: Configure tracker credentials, authentication, default selection, duplicate checks, and tracker-specific review.
---

# Trackers

upbrr's tracker catalog is built from registered tracker implementations. The Web UI renders each tracker's supported settings and capabilities from that catalog.

THR support, including its tracker-owned image host, has been removed because the site changed its underlying codebase. On upgrade or config import, THR settings, default/preferred selections, and obsolete Pronfo credentials are discarded before secret decryption. Historical upload records and unrelated settings are preserved. Startup writes repaired database configuration atomically; if that write fails, the original configuration remains available for retry. A replacement integration is not included.

## Configure a tracker

1. Open **Settings**.
2. Open the tracker section.
3. Enter only the requested credentials and options.
4. Save settings.
5. [Export and import browser cookies](../web-ui/settings/tracker-auth.md#export-and-import-cookies), sign in, or test authentication when those actions are available.
6. Add the tracker to default selection only after it reports ready.

Auth requirements vary. A tracker can require an API key, passkey, cookie session, username/password login, 2FA, or a supported combination. upbrr stores managed tracker cookies encrypted in SQLite.

Never paste tracker credentials, cookies, announce URLs, OTP secrets, or raw auth failures into issues or chat.

## Defaults and per-run selection

- **Default trackers** seed the initial tracker set.
- **Preferred tracker** influences workflows that need one preferred source of tracker data.
- CLI `--trackers` selects a comma-separated set for one run.
- CLI `--trackers-remove` removes trackers from that run.
- The Web UI lets you select trackers from the Input page.

CLI workflows that require post-duplicate approval use the exact tracker subset you approve. The Web UI uses its per-stage tracker selections. Later stages do not add disabled, blocked, or unselected trackers.

## Duplicate checks and rules

Tracker adapters normalize duplicate results into a common review surface. A tracker can return:

- a completed search with zero or more matches;
- a not-run result with a reason, such as missing auth or metadata;
- an attempted search failure.

Warnings that permit an override can block a live upload while allowing debug preparation. Strict constructibility failures block every mode. Non-submitting debug execution preserves the explicit language-eligibility bypass and displays the bypassed assessment. Subjective or incomplete tracker rules remain visible for review.

Unit3D duplicate searches cover the complete movie or TV category family, including site-specific categories such as anime or season packs. They do not filter by upload type, resolution, or individual episode number; the evaluator compares the returned release variants and overlapping content. A TV season may still narrow the search, except where the site needs a broader query, as described for [ACM](#acm-diagnostics).

Tracker settings can restrict duplicate competition by incoming release group. When a tag matches that tracker's **Duplicate bypass groups** or **Internal groups**, confirmed candidates from other groups coexist and are omitted from slot-capacity decisions. Exact duplicates, same-group candidates, conflicting evidence, and unknown groups keep their normal duplicate result. See [tracker group policy lists](../web-ui/settings/trackers.md#group-policy-lists).

`--skip-dupe-check` and similar bypasses remove safeguards. Use them only when you have manually completed the equivalent tracker checks.

### Approve tracker warnings

Some tracker warnings permit an explicit override. On **Dupe Check**, read the warnings on the affected tracker's card. Turn on **Acknowledge tracker warnings** only after deciding that the release is appropriate for that tracker. The warning details remain visible while acknowledged.

Approval applies only to that tracker and its current warning set. After the initial duplicate check, changing its warning toggle rechecks only that tracker when eligible. Other trackers keep their fresh, unchanged duplicate evidence and decisions. Turning the toggle off withdraws approval and skips that tracker's search until it is eligible again. Changed inputs, configuration, or expired evidence still require fresh checks. Changed warnings or a new prepared generation require renewed approval. Strict failures cannot be waived for a live upload. Debug may bypass the explicitly identified language-eligibility assessments, but never authorizes tracker submission.

The interactive CLI and `--unattended_confirm` prompt for approval. Strict `--unattended` declines without prompting and skips that tracker; other eligible trackers can continue. Debug mode bypasses warnings that permit an override and explicitly marked language-eligibility assessments; bypass details remain visible.

### Language eligibility

BHD, AITHER, HHD, ULCX, LST and LUME assess finalized language facts for non-disc uploads. Review the affected tracker's **Prohibited**, **Staff approval required**, **Trumpable release**, or **Unresolved** assessment. The details identify the original language, programme languages and relevant defect. Commentary, compatibility and other identified secondary tracks are assessed separately from programme dubs. Correct language evidence on Input; a naming override does not change factual API tags or upload eligibility.

A **Trumpable release** acknowledgement accepts that a compliant replacement may supersede the upload. It applies only to the affected tracker and prepared generation. It cannot clear an independent strict finding, grant staff permission, or prove that a remote site tag was applied. Missing or conflicting evidence must be resolved before a normal upload proceeds.

LST's missing-English-subtitle exception requires a successful complete title-wide search. Zero other torrents allows a labelled trumpable acknowledgement. Any other torrent blocks that exception, including other resolutions or formats. A failed or incomplete search cannot establish an empty title. Changed or expired search evidence requires reassessment.

Complete DVD, Blu-ray and UHD Blu-ray uploads retain their existing behavior and are excluded from these language assessments. Remuxes are non-disc uploads and remain subject to the rules. upbrr does not remove or remux tracks automatically.

## Tracker questions

On **Dupe Checking**, open **Tracker questions** for a selected tracker, review any required fields, and choose **Apply tracker answers**. Loading the questions uses existing prepared facts without contacting the tracker. Applying answers refreshes the assessment; it does not upload or start another duplicate search. Unanswered required fields block only their tracker, and unapplied edits must be applied before dry runs or uploads. See [the questionnaire workflow](../workflow/index.md#tracker-questions) for retained duplicate evidence and questions discovered during preparation.

PTP can ask you to review subtitle and trumpable tags when neither English subtitles nor a first English audio track are established, or hardcoded languages are unknown. Known hardcoded-language corrections, including **English (Full)** and **English (Forced)**, supply that evidence directly. Review every applicable choice; contradictory English and no-English claims block PTP. Choosing **English Softsubs Exist (Mislabeled)** does not correct a media track’s language. Use [Input language corrections](../workflow/index.md#subtitle-review) for that, or follow the [CLI subtitle-review guide](../cli/index.md#ptp-subtitle-review).

## Names and payloads

upbrr resolves tracker-specific upload and search names before duplicate checking. Review the projected name for every tracker. The eventual payload uses that reviewed name rather than deriving a new name at submission time. BHD factual tags are independent: original foreign programme audio plus English can set both `DualAudio` and `EnglishDub`, while the automatic name uses only the applicable exclusive marker. An additional prohibited dub still blocks the upload.

Generated DVDRip release names include the known resolution, such as `480p` or `576p`, after the movie year or the rendered TV season/episode segment. Missing or unknown resolution stays absent. Complete manual names and tracker policies that use exact source names or separate display titles retain their existing behavior.

Tracker-specific categories, source/type mappings, descriptions, media selection, questionnaires, and auth flows remain owned by the tracker adapter. A successful mapping does not prove the upload complies with every current site rule.

### Disc region and distributor IDs

For Unit3D disc uploads, upbrr translates known country codes and publisher names to the standard UNIT3D numeric IDs. Matching is case-insensitive; explicit positive numeric IDs remain usable for site-specific entries. Tracker taxonomy implementations can add or override names without changing the shared defaults. Disc playback zones such as A/B/C are not country codes.

Unknown optional values are omitted with a debug diagnostic rather than assigned a guessed ID. ACM and SHRI retain their stricter validation, and SHRI still requires a valid region for DVD and HD DVD uploads. ULCX requires a resolvable country region for Blu-ray discs (including UHD); a missing or unsupported region strictly blocks upload, even in debug mode or with rule authorization. Set the Region correction to a recognized country code or a positive tracker region ID, then refresh metadata before retrying. Playback zones A/B/C do not satisfy this country field.

Site-specific catalogs and live upload acceptance still need verification when a site changes its list.

### ACM diagnostics

ACM duplicate searches gather the full TMDB work within the movie or TV category. They do not narrow by release name, season, type, or resolution; the duplicate evaluator compares the returned content scopes and variants. This preserves work matching on ACM's legacy API.

For an ACM report, enable `--log-level trace` for the affected run. Search diagnostics include the work query and pagination decisions. Payload diagnostics include numeric classification IDs and evidence byte counts, without copying descriptions or MediaInfo into these messages. Review all logs before sharing and remove credentials, private URLs, and identifying release details. A successful local preparation does not confirm that ACM accepted an upload.

## Image hosts and clients

Trackers can restrict usable image hosts or select tracker-specific image/client overrides. Configure a compatible host before media preparation. Confirm the final hosted links and client injection settings per tracker.

## Add or update tracker support

Tracker implementation work requires registry, auth, naming, duplicate-search, validation, payload, and test contracts. Read [ADDING_TRACKERS.md](https://github.com/autobrr/upbrr/blob/main/ADDING_TRACKERS.md) before changing code.

For a tracker defect, report the smallest sanitized reproduction. Omit private rules, credentials, response bodies, and private URLs unless tracker staff have authorized publication.

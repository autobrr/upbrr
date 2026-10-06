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

Tracker settings can restrict duplicate competition by incoming release group. When a tag matches that tracker's **Duplicate bypass groups** or **Internal groups**, confirmed candidates from other groups coexist and are omitted from slot-capacity decisions. Exact duplicates, same-group candidates, conflicting evidence, and unknown groups keep their normal duplicate result. See [tracker group policy lists](../web-ui/settings/trackers.md#group-policy-lists).

`--skip-dupe-check` and similar bypasses remove safeguards. Use them only when you have manually completed the equivalent tracker checks.

### Approve tracker warnings

Some tracker warnings permit an explicit override. On **Dupe Check**, read the warnings on the affected tracker's card. Turn on **Acknowledge tracker warnings** only after deciding that the release is appropriate for that tracker. The warning details remain visible while acknowledged.

Approval applies only to that tracker and its current warning set. After the initial duplicate check, changing its warning toggle rechecks only that tracker when eligible. Other trackers keep their fresh, unchanged duplicate evidence and decisions. Turning the toggle off withdraws approval and skips that tracker's search until it is eligible again. Changed inputs, configuration, or expired evidence still require fresh checks. Changed warnings or a new prepared generation require renewed approval. Strict failures cannot be waived for a live upload. Debug may bypass the explicitly identified language-eligibility assessments, but never authorizes tracker submission.

The interactive CLI and `--unattended_confirm` prompt for approval. Strict `--unattended` declines without prompting and skips that tracker; other eligible trackers can continue. Debug mode bypasses warnings that permit an override and explicitly marked language-eligibility assessments; bypass details remain visible.

### Blocking reasons and warnings

Review the assessment displayed for each selected tracker:

- **Prohibited**: a rule prevents the release from proceeding in a normal upload.
- **Staff approval required**: the release needs permission from the tracker's staff; answering a question or acknowledging a warning does not grant that permission.
- **Trumpable release**: a compliant replacement may supersede the upload. Proceed only when the displayed finding permits an explicit acknowledgement.
- **Unresolved**: required evidence is missing or conflicting. Correct the facts or answer the requested questions before continuing.

Check the tracker's current rules to understand what each finding means for your release. upbrr's assessment is not a substitute for those rules.

Correct language and other factual evidence on **Input**. Naming overrides and warning acknowledgements do not change media facts. An acknowledgement applies only to its tracker and current prepared generation; it cannot clear an independent strict finding. Changed evidence requires renewed review. upbrr does not remove or remux tracks automatically.

### Media analysis

Preparation may inspect every selected media file when the current requirements need per-file evidence. The first analysis can therefore take longer for larger inputs; unchanged evidence can reuse cached reports. Media inspection cannot establish every source fact. Answer source questions only after checking the release, and resolve missing or conflicting evidence before proceeding.

## Tracker questions

On **Dupe Checking**, open **Tracker questions** for a selected tracker, review any required fields, and choose **Apply tracker answers**. Loading the questions uses existing prepared facts without contacting the tracker. Applying answers refreshes the assessment; it does not upload or start another duplicate search. Unanswered required fields block only their tracker, and unapplied edits must be applied before dry runs or uploads. See [the questionnaire workflow](../workflow/index.md#tracker-questions) for retained duplicate evidence and questions discovered during preparation.

Answer only after checking the relevant source. Unknown mandatory facts remain unresolved. Correct an unknown **Source** on Input where requested; changing source, type, tracks or prepared generation requires source answers to be reviewed again. A source answer cannot waive an independent prohibited or staff-only finding. Existing debug-mode policy bypasses also leave eligibility-only questions optional; evidence needed to construct names, payloads or required descriptions remains necessary.

## Names and payloads

upbrr resolves upload and search names before duplicate checking. Review the projected name for every selected tracker. The eventual payload uses that reviewed name rather than deriving a new name at submission time. Manual naming controls are presentation choices and do not grant upload eligibility or replace required factual evidence.

Generated DVDRip release names include the known resolution, such as `480p` or `576p`, after the movie year or TV season/episode segment. Missing or unknown resolution stays absent, and complete manual names remain unchanged.

Categories, source/type mappings, descriptions, media selection, questionnaires and authentication requirements can vary. Review the displayed requirements and consult the tracker's current rules. A successful local preparation does not prove that a remote upload will be accepted.

### Editions and commentary

A solitary Theatrical cut or edition is ignored. Compound editions retain their constituent cuts and multi-edition marker. Presentation and technical-feature annotations do not establish another edition. Actual additional editions and multi-cut sets remain intact; opaque manual Edition compounds retain their existing wording.

Review detected labels and inspected tracks before approval. Missing track languages cannot be borrowed from another report without a reliable identity match; correct missing languages explicitly when needed. Consult the tracker's current rules when interpreting any eligibility finding.

To override commentary detection, set **Commentary** to **Yes** or **No** and refresh metadata, or use `--commentary=true` / `--commentary=false` (`--mc` is an alias). Explicit No survives re-preparation and removes `With Commentary`. No extra commentary prompt is introduced, including in unattended mode. Older prepared generations must be refreshed to use the new evidence and classification.

### Disc region and distributor IDs

Region and distributor fields accept supported suggestions or explicit numeric IDs where applicable. Disc playback zones such as A/B/C are not country codes. If a required value cannot be resolved, correct it on **Input** and refresh metadata before retrying. Check the destination's current requirements rather than guessing an ID.

### Diagnostics

For a tracker defect, follow [troubleshooting guidance](../troubleshooting/index.md) and collect only the relevant sanitized excerpt. upbrr is intended to redact credentials automatically, but inspect every log and remove any remaining credentials, cookies, tokens, private URLs and unrelated personal information before sharing.

## Image hosts and clients

Trackers can restrict usable image hosts or select tracker-specific image/client overrides. Configure a compatible host before media preparation. Confirm the final hosted links and client injection settings per tracker.

## Add or update tracker support

Tracker implementation work requires registry, auth, naming, duplicate-search, validation, payload, and test contracts. Read [ADDING_TRACKERS.md](https://github.com/autobrr/upbrr/blob/main/ADDING_TRACKERS.md) before changing code.

For a tracker defect, report the smallest sanitized reproduction. Omit private rules, credentials, response bodies, and private URLs unless tracker staff have authorized publication.

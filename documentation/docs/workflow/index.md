---
title: Upload workflow
description: How upbrr turns one source into reviewed tracker operations and registered torrent artifacts.
---

# Upload workflow

upbrr separates source preparation, tracker decisions, media work, payload review, submission, and post-upload effects. Each stage consumes the exact prepared state approved by earlier stages.

## 1. Select the source

Provide a release folder or file. upbrr resolves the source layout, finds reusable torrent-client data when enabled, and creates a prepared release generation.

Folder handling matters. `--keep-folder` preserves a supplied folder instead of processing only a selected video file.

## 2. Review canonical metadata

Metadata providers and local media inspection produce shared release facts. Review at least:

- title and year;
- movie, TV, anime, or other category;
- release type, source, and resolution;
- season, episode, edition, service, distributor, region, and group;
- external IDs;
- generated release name.

Overrides change the prepared generation. Later operations must use that exact generation rather than silently rebuilding it.

## 3. Resolve tracker names and eligibility

Each tracker can project its own upload and duplicate-search names from the reviewed source facts. upbrr resolves those names before duplicate checks so the search evidence and eventual payload refer to the same reviewed identity.

Tracker rules and constructibility checks can mark a lane ready, blocked, skipped, or requiring manual review. A tracker-specific block need not stop other eligible trackers.

## 4. Review duplicate evidence

Duplicate search results are evidence, not an automatic upload decision. Review candidate names, metadata, and tracker warnings. Where approval is required, select an explicit non-empty tracker subset after the duplicate stage.

Never infer trumping, coexistence, or slot capacity from a release name alone.

## 5. Prepare media and descriptions

Depending on the source and trackers, upbrr can:

- inspect MediaInfo, BDInfo, DVD, or other prepared technical data;
- select Blu-ray playlists;
- generate screenshots at chosen frames;
- capture compatible DVD menus or import disc-menu images;
- upload selected images to allowed hosts;
- build tracker-specific BBCode descriptions.

Inspect image ordering, host URLs, technical blocks, headers, and rendered BBCode.

## 6. Preview immutable tracker operations

Tracker preparation captures an immutable operation. Payload preview and live submission use that captured state rather than regenerating names, rereading mutable prepared input, or uploading images again.

- **Description preview** prepares only the description.
- **Dry run** and upload review can prepare a preview but cannot submit it.
- **Upload** consumes the approved operation once.

Short-lived remote tokens can still be acquired at submission time when required by a tracker.

## 7. Submit and retain registered torrents

After confirmed tracker success, upbrr records the tracker result and attempts to retain the tracker-registered torrent. Client injection consumes that registered artifact, not the pre-upload torrent.

A failure to download or persist the registered torrent does not turn a confirmed remote upload into a failed upload. Review the warning and recover the torrent manually when needed.

## 8. Inject into clients

Client injection is enabled by default when configured. Disable it explicitly with CLI `--no-seed` or the corresponding Web UI upload option.

Check save path, category, tags, automatic management, staging mode, and source-file access. Hardlink, reflink, and symlink staging have filesystem-specific requirements.

## Debug and unattended modes

| Mode                   | Submission                                | Prompts                  | Client injection           |
| ---------------------- | ----------------------------------------- | ------------------------ | -------------------------- |
| Normal                 | allowed after review                      | allowed                  | enabled when configured    |
| `--debug`              | suppressed                                | allowed                  | enabled unless `--no-seed` |
| `--unattended`         | allowed when all required decisions exist | never                    | enabled when configured    |
| `--unattended_confirm` | allowed when confirmed                    | required prompts allowed | enabled when configured    |

Debug mode is not a non-mutating dry run. It can perform screenshots, image uploads, remote searches, tracker preparation, and later workflow effects. Add `--no-seed` when testing without client injection.

## Final review checklist

Before submission, verify:

- release name matches current tracker rules;
- category and type are correct for every tracker;
- movie, TV, disc, remux, encode, WEB, HDTV, pack, season, and episode handling are correct;
- source, resolution, edition, service, distributor, region, language, tag, and group are correct;
- screenshots are valid, ordered, and hosted on allowed hosts;
- description BBCode renders correctly;
- torrent contents, piece settings, and announce behavior are expected;
- client category, tags, save path, and injection target are correct;
- duplicate results, rule warnings, and manual prerequisites have been read.

When any item is uncertain, stop before upload and resolve it manually.

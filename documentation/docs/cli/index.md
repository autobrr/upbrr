---
title: CLI reference
description: Commands, interaction modes, aliases, and upload preparation flags supported by upbrr.
---

# CLI reference

```text
upbrr [options] <input path>...
upbrr serve [options]
upbrr api-token <list|revoke> [options]
upbrr auth <password|browse-roots> [options]
upbrr recover [options]
upbrr live-test <init|cleanup> [options]
```

On Windows, examples use `upbrr.exe`. Put options before input paths for consistent behavior across commands. Use `--` to end option parsing when a path begins with a hyphen or could be mistaken for an option value.

Upload, `serve`, `recover`, `auth`, and `api-token` options accept either one or two leading hyphens, including the aliases below. The exception is `--console-log-level`, which requires two hyphens for its full name; `-cll` and `--cll` also work. Command and option names are case-sensitive, including `--onlyID` and underscores in `--skip_auto_torrent`, `--manual_frames`, `--comparison_index`, and `--unattended_confirm`. Aliases also accept two hyphens, for example `--tk`. Boolean options accept `true`, `false`, `1`, `0`, `t`, `f`, `T`, `F`, `TRUE`, `FALSE`, `True`, or `False` after `=`; a bare boolean option means true. Use the equals sign instead of a separate value. The `-hc` language shorthand has additional forms described under [Subtitle review](#subtitle-review).

Omitted correction flags preserve saved values, and omitted configuration overrides use the active configuration. Workflow switches such as `--debug`, `--no-seed`, and `--unattended` are off unless enabled. `--hdr-boundary-audio` defaults to true within bounded HDR mode. Non-correction boolean overrides use the configured value when omitted. Numeric and audio-selection defaults are noted below. Paths and free text have no fixed choice list; quote spaces and use the spelling shown for constrained values. Integer options accept Go integer syntax unless a decimal-only rule is stated; use ordinary decimal digits to avoid base-prefix interpretation.

Use `--help` (also `-h`, `--h`, or `-help` outside `live-test`) as the exact option-name reference for your installed version:

```powershell
.\upbrr.exe --help
.\upbrr.exe serve --help
.\upbrr.exe api-token list --help
.\upbrr.exe auth password --help
```

Upload accepts one or more local file or folder paths; named queues require exactly one root. Standalone audio/HDR analysis requires exactly one source. `serve`, `recover`, `auth password`, `api-token list`, and both `live-test` subcommands accept no positional arguments. `api-token revoke` requires exactly one token ID; `auth browse-roots` requires existing directory paths or `--allow-unrestricted`, never both. Command groups without a subcommand show help. There are no command-name aliases or shell-completion command.

## Common operations

Prepare one release:

```powershell
.\upbrr.exe "D:\releases\Example.Release.2026.1080p-GRP"
```

Prepare without tracker submission or client injection:

```powershell
.\upbrr.exe --debug --no-seed "D:\releases\Example.Release.2026.1080p-GRP"
```

Run duplicate and site checks without tracker submission:

```powershell
.\upbrr.exe --site-check "D:\releases\Example.Release.2026.1080p-GRP"
```

Both `--debug` and `--site-check` can prepare media, write local artifacts, upload images and inject torrents into configured clients. Add `--no-seed` (`-ns`) to prevent client injection. These modes do not provide a non-mutating dry run.

Create or resume a named queue, processing at most five pending entries:

```powershell
.\upbrr.exe --queue uploads --limit-queue 5 "D:\upload-queue"
```

Supply a non-empty queue name and exactly one queue-root path. The first invocation saves the sorted first-level media files and release folders. Reusing that name with the same root loads its saved membership and progress. `--limit-queue` applies after filtering progress; `0` means unlimited, so repeated limited runs advance through the queue. An item failure does not stop later entries, but the command returns a nonzero summary result if any item failed. Cancellation stops the queue.

The CLI prints the saved file's location on creation and resume. Files live in `queues/<name-hash>.json` beside the active `db.sqlite` (including a custom database location). The hash is the SHA-256 of the exact, trimmed queue name; names have independent progress even when their filesystem-safe spellings would collide. The private JSON contains the name, root, member paths, attempt counts, workflow IDs and per-item progress. It contains no tracker credentials or submission payloads. Protect these local paths when sharing the file. An OS lock excludes concurrent writers and releases on process exit; keep its adjacent `.lock` file in place.

Each item is saved before processing and after its outcome. The checkpoint's temporary file is flushed before replacement. Unix also flushes the queue directory and its ancestors before queue work, then the containing directory after each replacement. Windows flushes the writable destination after replacement; Microsoft documents directory-structure persistence for this operation on NTFS, without an equivalent guarantee for other Windows filesystems. These barriers depend on the filesystem and storage honoring flush requests; they do not establish unconditional power-loss or exactly-once tracker submission guarantees.

A checkpoint write or sync failure stops the queue and reports the item as failed. If replacement succeeded before a sync error, the saved file may already contain the new progress. The command reports durability as unconfirmed, retains the file and does not automatically repeat the write or work. Reopen the named queue to load and flush its retained state before deciding whether to retry an interrupted item. Do not assume that an error means no checkpoint was written.

States are `pending`, `running`, `completed`, `failed`, `interrupted` and `skipped`. The TUI reports skipped items without replacing more specific recovery messages. Failed or partial debug/site-check reports remain retryable failures. A leftover `running` checkpoint becomes `interrupted` when you explicitly resume the queue. Normal resume selects pending items only. To include failed, interrupted and skipped items, inspect their outcomes and run:

```powershell
.\upbrr.exe --queue uploads --retry-queue --limit-queue 5 "D:\upload-queue"
```

Retry selection does not bypass input ownership, confirmed-upload exclusions or uncertain-submission reconciliation. A tracker submission with an unknown outcome still requires the existing recovery process; no remote submission is retried automatically. Confirmed tracker uploads are recorded separately from item progress, including partial failures. `completed` means the requested processing finished: Input-only and debug/site-check runs can complete without uploading. Skips and declined actions do not prove an upload. Use a different queue name when starting a different task, such as switching a completed Input-only queue to uploads.

An unknown tracker outcome leaves the affected queue item interrupted while independent pending items continue. Legacy attempts without durable submission protection still block new inputs until reconciled. The affected item's original workflow and submission protection remain available after restart. Reopen that source interactively or with `--unattended_confirm` only after checking the remote outcome; declining reconciliation leaves its outcome unresolved. If a retained workflow's source cannot be identified reliably, manual recovery is still required.

Membership stays unchanged until explicitly refreshed:

```powershell
.\upbrr.exe --queue uploads --refresh-queue "D:\upload-queue"
```

Refresh adds newly discovered members and preserves existing progress, including missing sources and failed items. It does not reset completed items, remove old members, or change the saved root. Missing members fail when processed while other entries continue. A queue with no remaining work reports completion; retained failed, interrupted or skipped items remain inspectable and require `--retry-queue`. Queue processing never starts automatically when the application or Web UI starts. Saved queues cannot be combined with `--cleanup` or `--delete-tmp`; perform stored-release maintenance separately, preserving any unresolved remote effects.

### Multi-disc folders

Pass the collection parent, not each disc separately:

```powershell
.\upbrr.exe "D:\releases\Example BDMV Collection"
```

For BDMV, interactive preparation groups playlist choices by disc and requires at least one selection from every disc. `--unattended` stops if no complete stored or configured selection can be resolved; `--unattended_confirm` can show the required prompt.

`--manual_frames 240,480` requests both frames from every prepared disc. The selected parent remains one collection-root torrent containing all disc folders.

### Blu-ray.com release inspection

When Blu-ray.com lookup is enabled, the CLI and TUI first show the release listing, including each edition’s release ID. Enter a release number to select its available country, publisher and release URL; enabled cover handling inspects only that edition. Enter `a` to fetch and score all eligible editions, then choose a release or `n` to skip. Unattended runs do not prompt or implicitly fetch all details. Country filtering and automatic unique-country selection still apply.

Use `--bluray-release "https://www.blu-ray.com/movies/Example-Release-Blu-ray/123/"` or `--bluray-release 123` for an exact release. Numeric IDs use the source's Blu-ray or DVD catalogue; a URL identifies its own catalogue. This fetches only that edition when its details are needed to resolve it. `--bluray-release=""` explicitly skips selection; `--bluray-release auto` restores automatic matching (`auto` ignores case and surrounding whitespace). Compatible listings, details and manual choices survive ordinary corrections. The separate source-torrent ID flags remain independent of this edition selection.

### Decrypted 3D Blu-ray ISO input

Select the original `.iso` file in the WebUI file browser, or pass it directly to the CLI:

```powershell
.\upbrr.exe "D:\releases\Example.Movie.2026.3D.iso"
```

Choose a playlist through the normal Blu-ray selector. A remembered choice is reused only for the same source fingerprint; a changed image requires a fresh selection. Preparation uses BDInfo and requires MVC evidence in every selected playlist. A filename or manual 3D correction cannot make an unsupported image eligible. MediaInfo is not required for this ISO workflow. If the filename has no resolution, naming uses the primary selected report's video resolution; explicit resolution corrections still apply.

Screenshots use FFmpeg's `bluray:` input and the explicitly selected playlist. Times refer to the complete playlist, including multiple clips. The images are ordinary single-view AVC base-view frames, not stereoscopic reconstructions; the base view is not necessarily the left eye. FFmpeg must provide the libbluray input protocol. The reported FFmpeg information includes `libbluray: available`, `unavailable`, or `unknown`; availability alone does not prove that a particular image can be decoded.

The torrent contains only the original ISO, with its physical size and source path preserved. No mounting or extraction is required. DVD ISOs, 2D Blu-ray ISOs, encrypted images, and generic archive browsing are not supported by this workflow. Existing extracted-disc preparation remains available.

## Interaction and safety

The CLI shares one active-input slot with every process using its database. Queue entries run serially. If another session owns the input, the command returns a busy error; unattended mode never prompts or takes over a live input. After its terminal result, the CLI closes its owned input. Cancellation uses bounded cleanup; a reported cleanup failure requires recovery before further work.

After an interrupted legacy workflow, check the tracker or torrent client before answering the recovery confirmation. Confirm that the operation did not complete only when you have verified its outcome. Interactive mode and `--unattended_confirm` can ask this question; `--unattended` exits without prompting or starting a new submission. Recovery does not submit anything by itself.

If a configuration change blocks startup because an external outcome is unresolved, stop other processes using that database and run `upbrr recover`. Pass the same `--config` option and environment as the original command so recovery selects the existing database. Check each tracker, image host or torrent client before confirming that its action did not complete; answer No for completed or uncertain outcomes.

This maintenance command does not activate configuration or start external work. It opens the retained local artifacts and saves each confirmation together with its workflow checkpoint in one database transaction. After recovery succeeds, rerun the original command to activate configuration and continue through the normal preparation and review checks. Keep the database if recovery reports an error and follow its diagnostic before retrying.

Language review identifies the inspected source, resource and track, shows detected and effective languages, and gives the correction flag for that track. Playlist and metadata choices show the current candidates. Tracker questions show the reviewed upload name, current value and accepted answers; a displayed value may be a default, so review it before submitting your answer.

Plain output and the TUI show the same prepared evidence and answer consequences. Duplicate review explains search completeness and any required comparison. Yes records the explicit duplicate decision; tracker approval separately selects the trackers allowed to continue. A clear, completed search defaults to Yes at tracker approval; incomplete or manual checks default to No. `--unattended` does not read answers, and `--uac` allows required prompts. Use the correction shown by the prompt and rerun when the source facts are wrong.

After you answer tracker questions, the CLI rebuilds the affected tracker stages and refreshes duplicate evidence before asking for duplicate review. Follow-up questions can depend on earlier answers. Accepted answers, reviewed names, and rule acknowledgements remain valid only while their underlying inputs still match. Previous duplicate review decisions are requested again after evidence changes; explicit duplicate-policy options still apply. Answering tracker questions does not approve duplicates or authorize upload.

Trackers with a confirmed upload of the same verified submitted content are excluded before duplicate checks. If every selected tracker is already uploaded, the command succeeds without another approval prompt or submission. See [the upload workflow](../workflow/index.md#4-review-duplicate-evidence).

Final upload results report tracker submission and client injection separately, including failed and skipped submissions. An accepted upload remains accepted when client injection or local receipt retention fails. Inspect the reported failure and the torrent client before retrying; do not resubmit an accepted upload to repair client delivery. Printed debug/site-check reports also include retained preparation failures and the actual client injection outcome. The TUI includes these explanations in its tracker details and final summary; long queue summaries retain bounded excerpts.

If release preparation stops before an upload report is available, plain output and the TUI release panel show the retained failure details. That input does not advance to tracker upload. The TUI queue summary also keeps the safe error explanation when the next input starts. Correct the reported problem before retrying; unattended mode does not ask for additional answers.

Terminal errors explain the next recovery step in plain language. A confirmation for an uncertain external outcome requires checking the remote service first; opting into prompts does not authorize a retry. Client-delivery recovery does not repeat an accepted tracker upload.

During image hosting, the TUI shows each host's latest message and separate uploaded, reused and failed image counts. Fallback hosts are identified. These image counts do not replace overall workflow progress; a processed image may have failed. Overall progress stays above the host details. Focus **Application flow** and use Page Up/Page Down to inspect earlier hosts; new attempts start with the latest details visible.

Normal logs identify image uploads and returned-link counts, explain reused images and requested upload skips, and show when another host is tried. A partial-upload warning explains when enough images remain to meet the configured minimum. Skipping new uploads preserves reusable links; it does not make missing images available or establish tracker readiness. Timing and concurrency details remain available at DEBUG.

| Option                     | Behavior                                                                                                                                      |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `--debug`                  | Runs preparation and payload preview without tracker submission. Image uploads and client injection remain enabled unless explicitly skipped. |
| `--site-check`             | Runs site checks and preparation without tracker submission, with the same image-upload and client-injection side effects as `--debug`.       |
| `--log-level debug`        | Changes application logging verbosity for this run. It does not enable debug/non-submitting behavior.                                         |
| `--console-log-level info` | Changes terminal log verbosity for this run without changing file or retained application logs.                                               |
| `--unattended`             | Never prompts. Unsafe global ambiguity returns an error; tracker-specific manual prerequisites can block only that tracker.                   |
| `--unattended_confirm`     | Uses unattended defaults but permits required confirmation or manual-input prompts.                                                           |
| `--no-seed`                | Disables torrent-client injection.                                                                                                            |

Debug runs the normal authentication, banned-group, claim, rule and duplicate checks using the configured cache and refresh rules. Blocked or failed outcomes remain visible. When the required inputs are available, diagnostic preparation continues to a redacted final payload even after an upload-related approval is declined. Missing inputs and construction failures still stop the affected preparation; metadata corrections and explicit cancellation retain their normal meaning. No approval, continuation or retry can submit a tracker upload in debug mode.

Interactive tracker questions with exactly `yes`/`no` choices also accept `y`/`n`, regardless of letter case. Other choices still require their listed values. Strict `--unattended` remains prompt-free.

With `--ui=auto`, a terminal-dashboard failure before work starts falls back to plain output. Explicit `--ui=tui` startup failures stop the command. User cancellation or interruption never starts a fallback run.

When Ctrl+C interrupts an accepted background workflow operation, the CLI requests cancellation and allows up to five seconds for the worker to stop and finish its bookkeeping before attempting input cleanup. Cancellation acknowledgement alone does not mean hashing or an OS read has stopped. Busy input stays protected; a `cleanup_failed` warning identifies the cleanup stage, a safe reason and a recovery action. Wait for shutdown before retrying. Retained HDR analysis remains available for resumption, and unresolved external outcomes still require reconciliation. An interrupt message alone does not establish that a tracker submission happened.

The terminal log view bounds each line to 4,096 bytes. Oversized raw diagnostic lines are replaced with `[oversized diagnostic omitted]` rather than showing a partial structured value that cannot be safely redacted. Already-sanitized application log messages retain a bounded prefix with `[truncated]`. File and retained application logs are not shortened by this display limit. Inspect logs and remove any remaining credentials, cookies, tokens, private URLs and unrelated personal information before sharing them.

:::danger Destructive maintenance

`--cleanup` deletes all stored release content from the active database. `--delete-tmp` deletes stored database content for each supplied input before processing it. Back up state and verify the active config/database before using either option.

:::

## Config and application

| Option                      | Aliases                    | Purpose                                                                         |
| --------------------------- | -------------------------- | ------------------------------------------------------------------------------- |
| `--config <path>`           | `-config`                  | Use a config file path.                                                         |
| `--export-config <path>`    | `-export-config`           | Export SQLite config, or built-in defaults when unconfigured, to YAML and exit. |
| `--export-config-plaintext` | `-export-config-plaintext` | Include plaintext secrets; requires `--export-config`.                          |
| `--import-config <path>`    | `-import-config`           | Import `.py`, `.yaml`, `.yml`, or `.json` config and exit.                      |
| `--create-auth`             | `-create-auth`             | Create `web-auth.json` beside the active database and exit.                     |
| `--version`                 | `-version`                 | Print version and exit.                                                         |
| `--cleanup`                 | `-cleanup`                 | Delete all stored release content and exit.                                     |

`--create-auth`, `--export-config`, and `--import-config` are mutually exclusive. Config exports omit plaintext secrets unless `--export-config-plaintext` is supplied; protect plaintext exports as credentials. See [configuration import and export](../configuration/index.md#import-configuration) for config selection and encryption requirements.

## Execution

| Option                        | Aliases                       | Purpose                                                                                                                                 |
| ----------------------------- | ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `--ui <mode>`                 | `-ui`                         | Select `plain` (default), `auto`, or `tui`; exact lowercase, with empty following auto detection. Static commands keep ordinary output. |
| `--ui-keep-open`              | `-ui-keep-open`               | Keep a TUI dashboard open after completion; default false, ignored in plain mode.                                                       |
| `--queue <name>`              | `-queue`                      | Create or resume a saved queue; supply its original root as the single input path.                                                      |
| `--limit-queue <count>`       | `-limit-queue`, `-lq`         | Limit remaining eligible items; defaults to `0` (unlimited). Negative values are rejected.                                              |
| `--refresh-queue`             | `-refresh-queue`              | Add new members without resetting progress; requires `--queue`.                                                                         |
| `--retry-queue`               | `-retry-queue`                | Include failed, interrupted and skipped items; requires `--queue`.                                                                      |
| `--site-check`                | `-site-check`, `-sc`          | Check sites and prepare upload previews without tracker submission.                                                                     |
| `--site-upload <tracker>`     | `-site-upload`, `-su`         | Process one tracker upload flow.                                                                                                        |
| `--debug`                     | `-debug`                      | Prepare and preview without tracker submission; image uploads and client injection remain enabled.                                      |
| `--log-level <level>`         | `-log-level`                  | Set application logging to `error`, `warn`, `info`, `debug`, or `trace` for this run.                                                   |
| `--console-log-level <level>` | `-cll`                        | Set console logging to the same levels without changing application logs.                                                               |
| `--upload-only`               | `-upload-only`                | Upload using prepared metadata cache only.                                                                                              |
| `--input-only`                | `-input-only`                 | Stop after local Input preparation and readiness evaluation.                                                                            |
| `--delete-tmp`                | `-delete-tmp`, `-dtmp`        | Delete stored content for each input before processing.                                                                                 |
| `--unattended`                | `-unattended`, `-ua`          | Run without prompts.                                                                                                                    |
| `--unattended_confirm`        | `-unattended_confirm`, `-uac` | Run unattended defaults with prompts allowed.                                                                                           |

Log levels ignore case and surrounding whitespace; `warning` aliases `warn`. An explicit empty log-level value selects `info`; omission inherits the applicable application/console threshold.

`--live-test` and `--live-test-max-images` are development options described under [`live-test`](#live-test). There is no `--dry-run` option; use `--debug --no-seed` for preparation without tracker submission or client injection.

## Tracker selection and IDs

| Option                     | Aliases                    | Purpose                            |
| -------------------------- | -------------------------- | ---------------------------------- |
| `--trackers <list>`        | `-trackers`, `-tk`         | Use comma-separated trackers.      |
| `--trackers-remove <list>` | `-trackers-remove`, `-rtk` | Remove comma-separated trackers.   |
| `--ptp <id-or-url>`        | `-ptp`                     | Supply a source torrent ID or URL. |
| `--blu <id-or-url>`        | `-blu`                     | Supply a source torrent ID or URL. |
| `--aither <id-or-url>`     | `-aither`                  | Supply a source torrent ID or URL. |
| `--lst <id-or-url>`        | `-lst`                     | Supply a source torrent ID or URL. |
| `--oe <id-or-url>`         | `-oe`                      | Supply a source torrent ID or URL. |
| `--hdb <id-or-url>`        | `-hdb`                     | Supply a source torrent ID or URL. |
| `--btn <id-or-url>`        | `-btn`                     | Supply a source torrent ID or URL. |
| `--bhd <id-or-url>`        | `-bhd`                     | Supply a source torrent ID or URL. |
| `--ulcx <id-or-url>`       | `-ulcx`                    | Supply a source torrent ID or URL. |

Tracker lists trim comma-separated entries and normalize IDs to uppercase; use IDs advertised by the running catalog rather than public display names. `--site-upload` takes one nonempty ID. Omitted lists inherit scope/configuration; there is no global `all` choice.

Omitting `--trackers` uses the retained workflow scope when available, otherwise the configured default trackers. `--site-upload` replaces the requested tracker list with its one tracker; `--trackers-remove` still excludes named trackers. Removing every selected tracker stops the upload. Tracker ID flags supply lookup identities and do not select upload destinations.

## Release overrides

Explicit corrections take precedence over saved history and provider metadata. Omitting a correction flag preserves the saved value. Resetting a field removes its manual value and restores automatic detection. A boolean value such as `--commentary=false` is an explicit correction, not a reset.

Metadata review always prints the effective `Commentary: true` or `Commentary: false` before confirmation. Answer No and enter `--commentary=false` to disable automatic commentary detection, then review the refreshed value. These displays add no prompts to `--unattended`. See [editions and commentary](../trackers/index.md#editions-and-commentary) for manual corrections.

### Review Input without advancing the workflow

```powershell
.\upbrr.exe --input-only --skip_auto_torrent --trackers "<tracker-id>,<other-tracker-id>" "E:\Media\Example.Release.2026.1080p-GRP.mkv"
```

This loads release facts and evaluates selected tracker input requirements. It stops before tracker assessment, duplicate searches, screenshots, descriptions, torrent preparation, or uploads. Metadata provider requests and media inspection can still run. `--skip_auto_torrent` also disables torrent-client discovery.

The output retains its `Input facts:` JSON, `Track` lines, and `Input <key>: <status>` lines, then explains local readiness. Missing or invalid required evidence identifies the affected trackers and inspected track when available. Advisory evidence appears separately and does not block local readiness. Supported correction flags are shown where known; otherwise review the source and metadata selection before rerunning `--input-only`.

Exit code `0` means local Input is ready, including when only advisory evidence needs review. Exit code `2` means required input prevents the requested readiness. Local readiness does not establish tracker authentication, duplicate-check results, upload acceptance, or torrent-client injection. Strict `--unattended` never prompts.

### Correct descriptive fields and languages

Title and original title are read-only provider values. Use `--alternate-title` to correct the alternate title.

| Option                                            | Purpose                                                                                                                             |
| ------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `--alternate-title <text>`                        | Override the alternate title.                                                                                                       |
| `--genres "Drama, Comedy"`                        | Supply one or more genres.                                                                                                          |
| `--audio-languages "English, Spanish"`            | Replace the release-level audio language list.                                                                                      |
| `--subtitle-languages "French"`                   | Replace the regular subtitle language list.                                                                                         |
| `--hardcoded-subs` / `-hc`                        | Enable hardcoded subtitles; `-hc Spanish` supplies a language; `--hardcoded-subs=false` disables them.                              |
| `--hardcoded-subtitle-languages "English"`        | Set hardcoded languages and enable them; an empty list disables them.                                                               |
| `--track-audio-origin "<track-id>=original"`      | Classify one programme or alternate-mix audio track as `original`, `dubbed`, or `unresolved`; repeat for distinct track IDs.        |
| `--track-languages "<track-id>=English, Spanish"` | Correct one previously inspected track. Repeat for distinct track IDs.                                                              |
| `--bluray-release "<release-url-or-id>"`          | Select one Blu-ray.com edition without scanning unrelated release pages. Empty skips selection; `auto` restores automatic matching. |
| `--source-lookup "<tracker-url>"`                 | Look up source metadata using a tracker URL.                                                                                        |
| `--reset-input <field>`                           | Remove one saved correction. Repeat for distinct fields.                                                                            |
| `--confirm-input <field>`                         | Confirm one stale content correction against the current required action.                                                           |
| `--tracker-input "<tracker-id>:<field>=<answer>"` | Answer a field shown by the current tracker questionnaire.                                                                          |

Language entries accept one or more comma-separated values. Blank segments are ignored, duplicate languages are removed, and multiword names remain intact. Use `--audio-languages=` for an explicit empty list. Original production language remains separate from track languages. Regional/script tags and Full/Forced coverage are retained; collectives and unknown values do not establish a specific language. See [language corrections](../workflow/index.md#2-review-canonical-metadata) for identity and clear/Auto behavior.

Run `--input-only` first to obtain track IDs. Track corrections require the retained scan manifest and one source. `--track-audio-origin` trims whitespace and ignores case in its three values; `unresolved` retains an explicit unknown answer. Reset `metadata.track_audio_origins:<track-id>` to restore Auto. It does not change the track language; commentary and other secondary roles are ineligible. If the source, playlist, or track order changes, inspect the source again and select a current track. Release-level lists do not assign languages to individual streams.

Reset examples:

```powershell
.\upbrr.exe --input-only --reset-input metadata.audio_languages --reset-input release_name.tag "E:\Media\Example.Release.2026.1080p-GRP.mkv"
.\upbrr.exe --input-only --reset-input metadata.hardcoded_subs "E:\Media\Example.Release.2026.1080p-GRP.mkv"
```

Apply fact corrections before tracker answers in separate commands. Combining them is rejected. Changes to source identity can require confirmation of saved content fields. Track corrections always require current scan evidence.

Generated descriptions include manually supplied audio, subtitle, and hardcoded language lines. User-supplied descriptions pass through each tracker's normal formatting. Selected screenshots still appear in the tracker's usual location, including separate screenshot fields where applicable.

Saved corrections use a newer database format. Older binaries do not support writing this correction state.

### Correction targets and repetition

`--reset-input` and `--confirm-input` take exact, case-sensitive field IDs. Use a full prefix from this list; they are not the CLI flag names.

| Prefix          | Supported suffixes                                                                                                                                                                                                                                                                                                                                                      |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `identity.`     | `tmdb`, `imdb`, `tvdb`, `tvmaze`, `mal`, `keep_tmdb`, `keep_imdb`, `keep_tvdb`, `keep_tvmaze`, `keep_mal`                                                                                                                                                                                                                                                               |
| `release_name.` | `category`, `type`, `source`, `resolution`, `tag`, `service`, `edition`, `presentation`, `release_features`, `repack`, `three_d`, `season`, `episode`, `episode_title`, `manual_year`, `manual_date`, `use_season_episode`, `no_season`, `no_year`, `no_aka`, `no_tag`, `no_episode_title`, `no_distributor`, `no_edition`, `no_dub`, `no_dual`, `dual_audio`, `region` |
| `metadata.`     | `distributor`, `original_language`, `personal_release`, `commentary`, `web_dv`, `stream_optimized`, `anime`, `alternate_title`, `genres`, `audio_languages`, `subtitle_languages`, `hardcoded_subs`, `hardcoded_subtitle_languages`, `title`, `original_title`, `track_languages`, `track_audio_origins`                                                                |

The two track fields require `:<track-id>` for reset and cannot be confirmed; other fields reject that suffix. Provider-owned `metadata.title` and `metadata.original_title` cannot be manually corrected. Confirmation supports only `metadata.title`, `metadata.alternate_title`, `metadata.original_title`, `metadata.genres`, `metadata.original_language`, `release_name.manual_year`, `release_name.manual_date`, `release_name.season`, `release_name.episode`, and `release_name.episode_title`. It is only for a currently requested stale content correction and requires matching retained revision/context; it is not a general approval switch.

Repeat track, reset, confirm, and tracker-input options for different targets; duplicate targets and overlapping value/reset/confirm requests are rejected. Supply each other correction flag once, including its aliases. `--release-features` takes one comma-separated list. A repeated ordinary execution/configuration option uses its last value.

`--tracker-input` uses a tracker ID and exact questionnaire field key from the current review. Choices come from that questionnaire, rather than a global list. `yes`, `no`, and `auto` ignore case; `auto` removes that answer. Other values retain their spelling. Interactive `y`/`n` shorthand is not a general command-line answer alias.

### Subtitle review

Supply known hardcoded languages directly: `-hc English Forced`, `-hc "English Full"`, or `-hc Spanish`. Coverage is retained as `English (Forced)` or `English (Full)`. Explicit hardcoded languages enable hardcoded handling and supply known evidence to the workflow. `--hardcoded-subtitle-languages "English (Forced), Spanish"` also accepts a language list.

Custom names are allowed: use the attached form `-hc="Custom Dialect"` (quote multiword names). Unknown space-separated values remain source paths, so bare `-hc` never consumes a custom-named source folder. If a source name could be confused with a language, end the options explicitly, for example `-hc -- "Spanish"`. Put other options before `--`. For unlisted custom names, a trailing bare `Full` or `Forced` stays part of the name; use `(Full)`/`(Forced)` or `- Full`/`- Forced` to specify coverage.

Bare `-hc` or `-hc=""` explicitly marks unknown hardcoded languages, clearing saved language choices and leaving that evidence unresolved. Legacy boolean forms such as `-hc=true`, `-hc=false`, and `--hardcoded-subs=false` remain supported; explicit false disables hardcoded handling.

The review is tracker-specific. Strict `--unattended` never prompts and skips a tracker when its required review is unanswered; other eligible trackers remain available. `--unattended_confirm` permits the required questions. The workflow still records the approved tracker subset before downstream work.

### Naming fields

Manual season and episode inputs accept plain numbers: `--season 5 --episode 5` produces `S05E05` in generated naming. For daily-dated TV releases, also set `--use-season-episode` to prefer these coordinates over the date. Padded and prefixed forms such as `05`, `S05`, and `E05` remain supported. Seasons accept up to four digits, including year-numbered seasons such as `--season 2026` or `--season S2026`; episodes accept up to three digits. Supply one value per field, without ranges or combined tokens.

Automatic filename detection still requires exactly two or four season digits. Source names can use `S01E03` or `S2026E03` for an episode and `S01` or `S2026` for a season pack. One- and three-digit seasons and x-separated forms such as `1x05` are not recognized in filenames; use `S01E05` instead.

Specials can use `S00E01` in a source name or `--season S00 --episode 01`. An explicit season zero is distinct from an unknown season; episode zero is invalid. Use `--season=` to clear the season or `--reset-input release_name.season` to restore automatic detection. Tracker eligibility still determines whether a special or a pack can be uploaded. Changing the episode coordinates does not remove known multi-episode source membership.

Existing prepared releases are rebuilt after this update. Older binaries do not support saved season-zero corrections.

| Option                        | Aliases                                           | Purpose                                                                                               |
| ----------------------------- | ------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `--category <value>`          | `-category`, `-c`                                 | Select a [category value](#accepted-naming-values).                                                   |
| `--type <value>`              | `-type`, `-t`                                     | Set a [release type](#accepted-naming-values).                                                        |
| `--source <value>`            | `-source`                                         | Set a [source value](#accepted-naming-values).                                                        |
| `--resolution <value>`        | `-resolution`, `-res`                             | Set a [resolution value](#accepted-naming-values).                                                    |
| `--tag <value>`               | `-tag`, `-g`                                      | Override group tag.                                                                                   |
| `--service <value>`           | `-service`, `-serv`                               | Override streaming service.                                                                           |
| `--distributor <value>`       | `-distributor`, `-dist`                           | Override distributor.                                                                                 |
| `--original-language <value>` | `-original-language`, `-ol`                       | Override original language.                                                                           |
| `--edition <value>`           | `-edition`, `-repack`                             | Override cut/edition text and multi-edition set labels; independent presentation and features remain. |
| `--presentation <value>`      | `-presentation`                                   | Set `IMAX`, `Open Matte`, or `IMAX Open Matte`; `--presentation=` clears it.                          |
| `--release-features <list>`   | `-release-features`                               | Replace release features using comma-separated IDs; `--release-features=` clears them.                |
| `--season <value>`            | `-season`                                         | Override one season number, such as `5`, `05`, or `S05`.                                              |
| `--episode <value>`           | `-episode`                                        | Override one episode token, such as `5` or `E05`.                                                     |
| `--episode-title <value>`     | `-episode-title`, `-manual-episode-title`, `-met` | Override episode title.                                                                               |
| `--manual-year <year>`        | `-manual-year`, `-year`                           | Override movie year or TV year without usable TVDB data; `0` clears it.                               |
| `--daily <YYYY-MM-DD>`        | `-daily`                                          | Set daily episode air date.                                                                           |
| `--use-season-episode`        | `-use-season-episode`                             | Prefer explicit or TMDB-matched season/episode naming over a daily date.                              |
| `--region <value>`            | `-region`, `-reg`                                 | Override release country code, such as USA or GER.                                                    |
| `--no-season`                 | `-no-season`                                      | Remove season and episode from name.                                                                  |
| `--no-year`                   | `-no-year`                                        | Omit movie year or TV year without usable TVDB data.                                                  |
| `--no-aka`                    | `-no-aka`                                         | Remove AKA from name.                                                                                 |
| `--no-tag`                    | `-no-tag`                                         | Remove group tag from name.                                                                           |
| `--no-episode-title`          | `-no-episode-title`, `-net`                       | Remove episode title from name.                                                                       |
| `--no-distributor`            | `-no-distributor`, `-ndist`                       | Remove distributor.                                                                                   |
| `--no-edition`                | `-no-edition`, `-ne`                              | Remove cut, edition, presentation, and multi-edition set labels from the name.                        |
| `--no-dub`                    | `-no-dub`                                         | Remove dubbed tag from audio name.                                                                    |
| `--no-dual`                   | `-no-dual`                                        | Remove dual-audio tag from audio name.                                                                |
| `--dual-audio`                | `-dual-audio`                                     | Add dual-audio tag to audio name.                                                                     |

Filename cut, edition, and presentation labels take priority over provider runtime labels. If selected playlists identify two distinct editions, automatic naming uses only `2in1`, without listing the individual editions. Manual `--edition` and `--no-edition` choices take precedence over these automatic labels.

Presentation and release features are independent of Edition. For example, `--edition "Director's Cut" --presentation "Open Matte" --release-features hybrid,4k_restoration` retains all three meanings. Supported feature IDs are `hybrid`, `restoration`, `remaster`, `two_disc_set`, `4k_restoration`, `4k_remaster`, `extras`, `2d_3d_edition`, `3d_anaglyph`, `3d_full_sbs`, `3d_half_ou`, and `3d_half_sbs`. These are release annotations; selecting a 3D annotation does not change measured 3D media facts.

The independent `--3d` control sets effective 3D; `--3d=false` clears it. Auto and explicit `--3d` preserve detected or manually selected stereoscopic subtype annotations, including with `--no-edition`. Auto does not turn an annotation into a measured 3D fact. An explicit release-features selection, including an empty value, still takes precedence. Use `--reset-input release_name.three_d` to restore detected evidence. This does not edit Edition or release-version corrections. Clear also suppresses effective stereoscopic subtype annotations, while their saved feature selections remain available when 3D returns to Auto. Recognized subtype annotations contribute only one literal `3D` marker immediately before Source, after Resolution and any Country. Generated names never include Full/Half SBS or OU wording; the underlying subtype facts remain available for tracker payloads. Other release features retain their existing placement.

Omit a flag to retain a saved correction. Use `--reset-input release_name.presentation` or `--reset-input release_name.release_features` to return that control to automatic detection. An explicit empty value clears only that control. Legacy `--no-edition` suppresses automatic edition-related labels, but explicit presentation/features selections take precedence.

`-repack` remains an alias for `--edition`. It does not set the independent **Release version** correction. Set that correction in the Web UI; use `--reset-input release_name.repack` to restore automatic version detection from the CLI. The legacy `--no-edition` option also suppresses an automatic release version, but a saved explicit **Release version** selection takes precedence. See [Input corrections](../web-ui/index.md#correct-input-facts).

If `--use-season-episode` has neither an explicit season/episode nor a matching TMDB episode, an existing daily date is retained with a warning. `--use-season-episode=false` selects daily-date naming when a date is available.

### Accepted naming values

Category is validated against a fixed vocabulary. Type, Source, and Resolution are text corrections: the CLI trims them but does not reject every unknown value at parsing time. Their canonical values below are supported choices, not a promise that arbitrary text works with every tracker. Use these exact source/resolution spellings and inspect the prepared facts and projected names. An empty correction differs from omitting the flag; use reset to restore detection. The literal `auto` is not a reset value for these flags.

| Option         | Values and handling                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--category`   | `movie` or `tv`. Case-insensitive aliases: `film` → movie; `television`, `series`, `episode` → TV. Empty or `unknown` selects unknown; preparation can still derive a category from source/provider evidence and finally fall back to Movie. Reset `release_name.category` for Auto.                                                                                                                                                                       |
| `--type`       | `DISC`, `REMUX`, `ENCODE`, `WEBDL`, `WEBRIP`, `HDTV`, `DVDRIP`. Naming uppercases and removes spaces/hyphens; `WEB-DL`, `WEB_DL`, `WEB-Rip`, and `WEB_RIP` normalize to the corresponding WEB type. Saved fact text is not a validated enum. Omitted values derive from media/source evidence; Use a release type here rather than a Movie/TV category label.                                                                                              |
| `--source`     | `Blu-ray` (disc), `BluRay` (encode/remux), `Blu-ray 3D`, `DVD`, `PAL DVD`, `NTSC DVD`, `HD DVD` (disc), `HDDVD` (encode/remux), `Web`, `HDTV`, `UHDTV`. These are the canonical source strings; do not enter display text such as `Blu-ray (disc)`. The naming source recognizer also accepts `BLU RAY`, `BD`, and `BDMV` case-insensitively, but does not globally normalize the saved correction for every destination.                                  |
| `--resolution` | `360p`, `480i`, `480p`, `540i`, `540p`, `576i`, `576p`, `720p`, `900p`, `1080i`, `1080p`, `1440p`, `1800p`, `2160p`, `2880p`, `3240p`, `4320p`, `8640p`, `DCI2K`, `DCI4K`. No global case/alias normalization is applied to the saved correction; use lowercase `p`/`i` and the shown DCI spelling. `OTHER` (case-insensitive) suppresses the resolution in generic naming; it does not establish a measured tier. Omission uses measured/source evidence. |

For example, `--category movie --type WEBDL --source Web --resolution 1080p` supplies canonical corrections for a synthetic WEB source. Setting `DISC` does not turn an ordinary file into a disc or establish its technical evidence.

`--service` accepts a recognized service acronym/name or custom text; recognized matches resolve to their service code. `--region` and `--distributor` accept suggestions or custom text, with numeric destination IDs where supported. They have no CLI-wide allowlist. `--tag`, `--edition`, `--episode-title`, and `--alternate-title` are free text; `--genres` is a comma-separated text list. Language options accept the names/codes described above, rather than a fixed language enum.

Presentation ignores case and collapses whitespace between words, but accepts only the three complete labels listed above or empty. Release-feature IDs are case-sensitive lowercase IDs; display labels such as `4K Restoration` are not accepted IDs. Season/episode prefixes ignore case and surrounding whitespace. `--daily` accepts a real `YYYY-MM-DD` calendar date or empty to clear the manual date override; `--manual-year` is an integer, with `0` clearing it.

### Required release facts

Category, Type, Source, and Resolution must be present in the saved prepared metadata before upload. The release preview labels the filesystem location **Path**, then shows Type, Resolution, and Source above Upload name. A recognizable filename or provider summary does not replace a missing saved fact.

If any are missing, **Missing fields** replaces the metadata confirmation and lists only the correction arguments you need: `--category <value>`, `--type <value>`, `--source <value>`, or `--resolution <value>`. Enter the listed corrections to prepare the input again. The normal **Metadata correct?** confirmation returns when all four saved facts are present; answering yes or entering `continue` cannot bypass missing facts.

Strict `--unattended` reports the missing facts and exits without prompting. Interactive mode and `--unattended_confirm` (`--uac`) allow corrections. `--input-only` still prepares incomplete input for inspection and reports its readiness without uploading.

## Metadata IDs

| Option          | Aliases   | Purpose             |
| --------------- | --------- | ------------------- |
| `--tmdb <id>`   | `-tmdb`   | Override TMDB ID.   |
| `--imdb <id>`   | `-imdb`   | Override IMDb ID.   |
| `--mal <id>`    | `-mal`    | Override MAL ID.    |
| `--tvdb <id>`   | `-tvdb`   | Override TVDB ID.   |
| `--tvmaze <id>` | `-tvmaze` | Override TVmaze ID. |

Multiple provider flags select each provider independently, even when their returned cross-links disagree. Each successful lookup remains available for preparation. See [manual metadata authority](../workflow/index.md#2-review-canonical-metadata).

TMDB also accepts `movie/<id>`, `tv/<id>`, or a URL ending in the ID; a movie/TV hint sets the category when no explicit `--category` is supplied. A category chosen during provider selection takes precedence over a retained hint. IMDb accepts numeric IDs or the `tt` prefix. TVDB, TVmaze, and MAL take nonnegative integer IDs, not URLs. Their parser accepts Go base prefixes, so use decimal IDs without leading zeroes to avoid octal interpretation. TMDB and IMDb numeric parts are decimal; their prefixes ignore case.

Use `--keep-existing-ids` to keep existing provider matches and their data when correcting another provider. You can pass it on the command line or in the correction arguments after **Metadata correct?**. For a release you have already prepared, this updates IMDb while keeping the other matches:

```powershell
.\upbrr.exe --keep-existing-ids --imdb tt1234567 --input-only "E:\Media\Example.Series.S01E01.1080p-GRP.mkv"
```

The Keep choices are saved for this release and also appear in the Web UI. A provider's explicit ID change or clear takes precedence. Use `--keep-existing-ids=false` to turn off these choices without removing manual ID corrections. Omitting the flag preserves saved choices. Changing Movie/TV can still clear incompatible automatic matches; a forced fresh preparation reloads data for kept IDs.

During the **Metadata correct?** loop, correcting the same active input reuses provider lookups for unchanged inputs, including lookups that returned no result. A later run can retry those empty results. Completed fetch failures remain retained until the input is removed from **History**. Changing the source, provider ID, or lookup query requests its own result.

### Select a provider title

When the identity is resolved, answer **No** at **Metadata correct?**, then enter `search` at the correction prompt. When the identity is unresolved, the correction prompt appears first; enter `search` there. Choose `movie` or `tv`, then enter a title and optional year, such as `Example Series 2026`. An empty title answer searches the title shown in brackets. TMDB and IMDb support movie and TV searches; TVDB searches TV series only.

Matches are grouped by provider and show their available title, year, ID, source link, overview, credits and runtime. Missing details do not prevent selection. The plain terminal accepts the listed match number; the TUI supports arrow-key selection and scrolling through the full list. Search uses the same bounded provider calls, caching and cancellation as the Web UI.

For example, with a synthetic TV source:

```text
Metadata correct? [Y/n]: n
Input args that need correction, 'search' for provider matches, or 'continue': search
Search category (movie/tv), back, or cancel: tv
Search title and optional year [Example Series 2026], back, or cancel:
...
1. Example Series (2026) | TMDB 123456 | tv | https://www.themoviedb.org/tv/123456
Match number, back (decline), or cancel: 1
```

Selection applies only that provider's ID through normal metadata preparation, then shows the resulting release details and IDs before continuation. Independently supplied manual IDs and explicit clears for other providers remain authoritative. Search again to select another provider, use correction flags such as `--imdb=tt1234567` or `--tmdb=0`, or use `--reset-input identity.tmdb` to restore Auto. A later selection replaces an earlier Auto request for that provider.

Enter `back` to decline matches without changing your choices, or `cancel` to stop the item. No matches, unavailable providers and failed searches have separate messages. Matches retained from earlier resolution have their own heading and remain selectable when a fresh search finds nothing. Retry `search` with another title/year, check metadata settings, or enter an exact provider ID. Searching does not retry a retained preparation failure; the **History** recovery boundary above still applies.

When the identity remains unresolved, candidates for providers you have not cleared require an explicit choice before continuation. Prepared generations retain this match evidence across reruns and restarts. Strict `--unattended` never searches interactively or prompts; supply an exact ID or use `--unattended_confirm` / `--uac` to permit selection. Valid automatic resolution continues unchanged. `--input-only` remains an inspection command without this interactive review.

### Clear a metadata provider

Pass an empty value or `0` to stop using a provider for this release. This works with `--tmdb`, `--imdb`, `--tvdb`, `--tvmaze`, and `--mal`.

For example, clear TMDB and use a known IMDb ID:

```powershell
.\upbrr.exe --tmdb= --imdb tt1234567 "E:\Media\Example.Release.2026.1080p-GRP.mkv"
```

Use the equals sign in `--tmdb=` to pass an empty value reliably in PowerShell. `--tmdb=0` has the same effect. You can clear several providers in one command.

Clearing removes that provider's ID and metadata from the prepared release. It also prevents automatic rediscovery through title searches, tracker data, or other providers. Other providers can still supply metadata.

The clear is saved for that source path. Omitting the flag on a later run preserves the clear. Supply a positive ID to use that provider again, such as `--tmdb 123456`. Provider settings for other releases stay unchanged.

Trackers that require the cleared provider can remain blocked. Continue with trackers whose metadata requirements are satisfied, or supply a correct ID before retrying. See [metadata troubleshooting](../troubleshooting/index.md#a-metadata-provider-fails-or-selects-the-wrong-title).

## Tracker overrides

| Option                | Aliases                      | Purpose                                                                 |
| --------------------- | ---------------------------- | ----------------------------------------------------------------------- |
| `--skip-dupe-check`   | `-skip-dupe-check`, `-sdc`   | Skip remote tracker duplicate searches.                                 |
| `--skip-dupe-asking`  | `-skip-dupe-asking`, `-sda`  | Skip trackers with duplicate evidence without asking.                   |
| `--double-dupe-check` | `-double-dupe-check`, `-ddc` | Request two duplicate checks instead of one.                            |
| `--commentary`        | `-commentary`, `-mc`         | Mark release as containing commentary.                                  |
| `--personalrelease`   | `-personalrelease`, `-pr`    | Explicitly set personal-release handling.                               |
| `--stream`            | `-stream`, `-st`             | Mark release as stream optimized.                                       |
| `--webdv`             | `-webdv`                     | Mark release as WEB-DV.                                                 |
| `--anime`             | `-anime`                     | Explicitly set anime handling; `false` clears the anime classification. |
| `--anon`              | `-anon`, `-a`                | Request anonymous attribution where the tracker adapter supports it.    |
| `--draft`             | `-draft`, `-dr`              | Send to drafts where supported.                                         |
| `--modq`              | `-modq`, `-mq`               | Opt into mod queue where supported.                                     |

`--personalrelease=true` and `--personalrelease=false` are both explicit choices and override tracker group defaults. Omitting the option preserves any saved correction. Use `--reset-input metadata.personal_release` to return **Personal Release** to **Auto**, where each tracker's configured personal-release groups can supply the default.

After duplicate checks, trackers with completed clear results default to **Yes** (`[Y/n]`). An explicit `--skip-dupe-check` replaces stored remote duplicate results with a waiver and grants this default when the tracker's preflight is ready; matching torrents already in your client still block that tracker. Strict `--unattended` applies this default without prompting and records only that exact eligible subset for preparation and upload. Duplicate results and separately overridden duplicate evidence default to **No**; other viable trackers can continue. Missing prerequisites, other incomplete or failed checks, rule waivers and staff-token requirements never become automatic Yes decisions.

`--skip-dupe-asking` skips trackers with duplicate evidence; it does not authorize duplicate uploads. Separate explicit per-tracker duplicate overrides take precedence over this skip policy, but still require tracker authorization and any staff-token confirmation. Duplicate options never bypass authoritative client blocks. Use `--anime=false` to explicitly classify a release as non-anime. The obsolete `--not-anime` flag has been removed and is rejected as an unknown option.

`--foreign`, `--opera`, and `--asian` set destination-specific classification booleans where supported; they default to false. `--channel <value>` (alias `-ch`) supplies a destination channel ID or tag, resolved by the applicable questionnaire rather than a global choice list. These flags do not select a tracker or waive its requirements.

`--disctype` accepts `BD100`, `BD66`, `BD50`, `BD25`, `NTSC DVD9`, `NTSC DVD5`, `PAL DVD9`, `PAL DVD5`, `CUSTOM`, or `3D`. Values ignore case and surrounding whitespace. Quote values containing spaces.

## Screenshots, images, and descriptions

Without `--screens`, the CLI uses `screenshot_handling.screens` when selected trackers require screenshots. Tracker-specific image counts and limits still apply. An override below a tracker's screenshot requirements can block that upload.

When required release images are missing, the CLI stops with the media failure and recovery guidance, or the existing prompt to capture, select, or host images. Resolve that media prerequisite before retrying. This block does not open a tracker questionnaire or prompt for media recovery, including with `--unattended_confirm`; required images continue to block upload in every interaction mode.

| Option                       | Aliases                             | Purpose                                                                                            |
| ---------------------------- | ----------------------------------- | -------------------------------------------------------------------------------------------------- |
| `--screens <count>`          | `-screens`, `-s`                    | Set a nonnegative screenshot count; `0` requests none. Omission uses configuration (built-in `4`). |
| `--manual_frames <list>`     | `-manual_frames`, `-mf`             | Use comma-separated positive frame numbers.                                                        |
| `--comparison <paths>`       | `-comparison`, `-comps`             | Set one comparison folder or comma-separated folders.                                              |
| `--comparison_index <index>` | `-comparison_index`, `-comps_index` | Select a one-based primary comparison path; omission keeps the supplied order.                     |
| `--menu-images <path>`       | `-menu-images`                      | Import manually captured disc-menu screenshots.                                                    |
| `--get-dvd-menus`            | `-get-dvd-menus`                    | Capture distinct menus from extracted DVD `VIDEO_TS`.                                              |
| `--imghost <name>`           | `-imghost`, `-ih`                   | Override a supported, non-tracker-owned image host.                                                |
| `--skip-imagehost-upload`    | `-skip-imagehost-upload`, `-siu`    | Skip automatic image-host uploads.                                                                 |
| `--descfile <path>`          | `-descfile`, `-df`                  | Use a custom description file.                                                                     |
| `--desclink <url>`           | `-desclink`, `-pb`                  | Use a custom description link.                                                                     |

`--imghost` accepts `dalexni`, `imgbb`, `imgbox`, `lensdump`, `onlyimage`, `passtheimage`, `pixhost`, `ptscreens`, `seedpool_cdn`, `sharex`, `utppm`, or `zipline`. Values ignore case and surrounding whitespace. Tracker-owned hosts are not valid global overrides. Omission uses configured host policy; empty is invalid.

`--manual_frames` ignores blank comma-separated segments but requires at least one positive decimal frame number. `--comparison` takes nonempty comma-separated local paths; `--comparison_index` must be positive when supplied and within the available paths. `--descfile`, `--desclink`, and `--menu-images` are paths/URLs rather than choice labels.

### HDR analysis options

| Option                         | Purpose                                                                                                                  |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| `--hdr-analysis`               | Generate native HDR plots and primary-audio analysis before upload.                                                      |
| `--hdr-analysis-only`          | Analyze one supported HDR file or Blu-ray content root without configuration or upload.                                  |
| `--hdr-targets <id,id>`        | Select prepared opaque target IDs in source order for upload.                                                            |
| `--hdr-playlist <number>`      | Select a numeric MPLS basename in standalone disc mode.                                                                  |
| `--hdr-peak-source <value>`    | Choose `histogram` (default), `histogram99`, `max-scl`, or `max-scl-luminance`.                                          |
| `--hdr-output-dir <path>`      | Required output parent for standalone mode; each run creates a fresh child directory.                                    |
| `--hdr-formats <value>`        | Select `auto` (default), `hdr10plus`, `dolby-vision`, or `both`.                                                         |
| `--hdr-metadata-only`          | Generate native plots without automatic audio or picture measurement.                                                    |
| `--hdr-audio-only`             | Explicitly request native plots and primary audio without pictures, the default mode.                                    |
| `--hdr-pictures`               | Add sampled automatic picture checks to native metadata and audio.                                                       |
| `--hdr-full-frames`            | Select full-frame automatic picture checks instead of sampling; slower.                                                  |
| `--hdr-boundary-sync`          | Decode bounded windows at selected HDR10+ and Dolby Vision scene boundaries.                                             |
| `--hdr-boundary-audio=false`   | Disable audio in bounded mode; audio is otherwise enabled there by default.                                              |
| `--hdr-all-audio-tracks`       | Analyze all prepared audio tracks instead of the primary track; maximum 64. Requires an audio-enabled mode.              |
| `--hdr-decoder-threads <n>`    | Video threads: `0` uses the mode default; `1`–`64` requests a maximum subject to resource limits. Requires picture work. |
| `--hdr-scene-sync`             | Select the individual decoded scene check against Dolby Vision markers.                                                  |
| `--hdr-active-area`            | Check decoded picture borders against Dolby Vision active areas.                                                         |
| `--hdr-compare-source <path>`  | Explicit local reference source B for grade or colour checks.                                                            |
| `--hdr-compare-offset <n>`     | Confirm the frame mapping `B = A + n`; explicit zero is required for aligned sources.                                    |
| `--hdr-compare-crop <l,t,w,h>` | Common coded-pixel crop for both sources; nonnegative left/top and positive even width/height must fit both.             |
| `--hdr-grade`                  | Compare brightness and PQ values against the selected reference.                                                         |
| `--hdr-colour`                 | Compare colour distributions against the selected reference.                                                             |

`--hdr-peak-source` and `--hdr-formats` require exact lowercase spellings without added whitespace; an empty value selects their default. `--hdr-playlist` accepts decimal `0`–`99999`, with an optional case-insensitive `.MPLS` suffix, and normalizes to five digits. `--hdr-targets` takes at most 16 distinct nonempty IDs; IDs are case-sensitive and must belong to the prepared release.

For requested upload analysis, every selected target needs usable native metadata or, when audio was requested, usable audio output. Partial results can qualify; canceled and interrupted attempts cannot. A comparison requires source B, an explicit decimal frame offset from `-500000` through `500000`, a crop, and at least one of `--hdr-grade` or `--hdr-colour`. Bounded mode accepts audio and active-area checks, but cannot combine metadata-only, audio-only, automatic pictures, full-frame pictures, the individual scene check, or comparison options. `--hdr-boundary-audio` requires bounded mode even when explicitly false.

Crop coordinates are decimal integers: left/top are nonnegative, width/height are positive even values, and the crop must fit both sources within an `8192×4320` bound. `--hdr-compare-offset` does not trim whitespace; supply the signed number directly.

Individual picture flags select manual work rather than the default audio operation. For scene checks, borders, and audio together, use `--hdr-boundary-sync --hdr-active-area`. See [HDR analysis](../workflow/hdr-analysis.md) for examples and the limits of sampled and bounded coverage.

#### Retained HDR workflows

| Option                       | Purpose                                                                                                          |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `--hdr-resume-workflow <id>` | Resume the exact retained active CLI workflow without replacing its request.                                     |
| `--hdr-retry-attempt <id>`   | Identify the retained analysis attempt for a new decoded measurement pass. Requires retry modes.                 |
| `--hdr-retry-modes <list>`   | Explicit unsuccessful decoded modes to measure again, from the current retained request. Requires an attempt ID. |
| `--hdr-image-ids <id,id>`    | Select exact current HDR artifact IDs for descriptions on continuation.                                          |

`--hdr-retry-modes` takes comma-separated, case-sensitive IDs: `automatic`, `boundary_sync`, `audio`, `scene_sync`, `active_area`, `grade`, `colour`. Only unsuccessful modes requested by that retained attempt can be retried; a list is not authorization to add work. Whitespace around each ID is trimmed. Artifact and workflow IDs must identify the exact retained objects.

Resume takes no input paths. Retry and image-selection flags require `--hdr-resume-workflow`; do not combine resume with either new-analysis flag. Retry measurements before selecting their new artifact IDs, in separate invocations. Resume accepts configuration, logging, and unattended options but cannot change the retained analysis request. Use IDs from the retained workflow; they are opaque. A decoded retry does not authorize a native metadata rescan.

The retained HDR report identifies each target and reports native-format and decoded-analysis outcomes, including saved failure and cancellation reasons. Successful artifact IDs remain listed even when other requested outputs are incomplete. Review those outcomes before choosing retry modes; printing the report does not retry analysis or select images.

### Audio analysis

Use `--audio-analysis` to analyze prepared audio tracks during the upload workflow, or `--audio-analysis-only` to analyze one file without configuration or upload.

| Option                   | Purpose                                                                         |
| ------------------------ | ------------------------------------------------------------------------------- |
| `--audio-analysis`       | Generate local images during upload.                                            |
| `--audio-analysis-only`  | Analyze one media file without configuration or upload.                         |
| `--audio-output <path>`  | Required output directory for `--audio-analysis-only`.                          |
| `--audio-tracks <value>` | Select `primary` (default), `all`, or comma-separated one-based audio ordinals. |
| `--audio-images <value>` | Generate `both`, `waveform`, or `spectrogram` images. Defaults to `both`.       |

Generate waveform and spectrogram images for the prepared primary audio track:

```powershell
.\upbrr.exe --audio-analysis "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

Generate only spectrograms for the first and third audio tracks:

```powershell
.\upbrr.exe --audio-analysis --audio-tracks 1,3 --audio-images spectrogram "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

Analyze only audio and save it outside managed temporary storage:

```powershell
.\upbrr.exe --audio-analysis-only --audio-output "D:\reports\audio" "D:\releases\Example.Release.2026.1080p-GRP.mkv"
```

`primary`, `all`, and audio image-type values ignore case and surrounding whitespace. Empty values are invalid. Numeric lists require positive decimal ordinals and reject blank segments.

The numeric selectors are audio-only ordinals, not container-wide stream indexes. Repeated ordinals are ignored, and results retain source track order. `--audio-tracks` and `--audio-images` require either analysis mode.

`--audio-output` is only valid with `--audio-analysis-only`. Standalone analysis requires exactly one regular media file and accepts its audio-analysis options, `--ui`, and `--ui-keep-open`; do not combine it with `--config`, upload options, or `--audio-analysis`.

During upload, the CLI prints the path of every successfully retained PNG and statistics file. Standalone analysis prints paths to PNGs and statistics files in a new directory under `--audio-output`. A partial or failed analysis exits nonzero, even when some artifacts succeeded. See [Audio analysis](../workflow/audio-analysis.md) for output, retry, and retention behavior.

## Client and torrent

| Option                   | Aliases                      | Purpose                                                                                  |
| ------------------------ | ---------------------------- | ---------------------------------------------------------------------------------------- |
| `--client <name>`        | `-client`                    | Override torrent client.                                                                 |
| `--qbit-tag <value>`     | `-qbit-tag`, `-qbt`          | Override qBittorrent tag.                                                                |
| `--qbit-cat <value>`     | `-qbit-cat`, `-qbc`          | Override qBittorrent category.                                                           |
| `--force-recheck`        | `-force-recheck`, `-frc`     | Recheck matched qBittorrent torrents and verify reusable torrent data.                   |
| `--no-seed`              | `-no-seed`, `-ns`            | Do not inject into torrent clients.                                                      |
| `--skip_auto_torrent`    | `-skip_auto_torrent`, `-sat` | Skip automated torrent-client searching.                                                 |
| `--keep-folder`          | `-keep-folder`, `-kf`        | Keep the supplied folder around one movie or episode video.                              |
| `--onlyID`               | `-onlyID`                    | Limit tracker metadata lookup to IDs where supported; does not stop the upload workflow. |
| `--infohash <hash>`      | `-infohash`                  | Find a client torrent using its 40-character hexadecimal v1 info hash.                   |
| `--max-piece-size <MiB>` | `-max-piece-size`, `-mps`    | Override the configured maximum: `1`, `2`, `4`, `8`, `16`, `32`, `64`, or `128` MiB.     |
| `--nohash`               | `-nohash`, `-nh`             | Reuse existing torrents only; do not generate a new torrent.                             |
| `--rehash`               | `-rehash`, `-rh`             | Force generation of a fresh torrent.                                                     |

`--client` takes a configured client entry name, not a client type. Matching trims whitespace, prefers an exact name, then accepts one unambiguous case-insensitive match. Omission uses [client selection order](../web-ui/settings/client-handling.md); `none` disables ordinary selection, subject to the explicit infohash lookup below. Tags and categories are free text. Infohash letters ignore case and surrounding whitespace is trimmed; an empty hash is invalid. `none` ignores case and surrounding whitespace; ambiguous folded names do not select an arbitrary client. `--max-piece-size=0` is invalid when explicitly supplied; omission uses configured/default torrent policy.

`--keep-folder` applies only when a supplied folder contains one selected movie or episode video. The torrent retains that folder as its root and includes only the selected video; samples, NFOs, text files, and other sidecars are excluded. The default remains a single-file torrent. Season packs, multi-video folders, disc sources, and direct file inputs are unchanged. Reused torrents must match the requested layout.

`--infohash` searches the default client first, then other available clients, overriding the configured search-client list, `none` selectors and `--skip_auto_torrent`. A named `--client <name>` still restricts lookup to that client. A miss prints an error identifying the selected client, or the available-client search, and continues ordinary source discovery; another torrent may be reused, and `--nohash` never generates one.

A match requires an exported torrent with source-matching file names, sizes and layout, independently of client save paths. Ordinary matching does not hash media bytes. A one-file folder wrapper is also eligible when it can be removed while preserving the pieces; its metadata and validated candidate remain available until torrent preparation performs that conversion. Missing, empty or invalid exports fall back to ordinary discovery. Tracker policy and source-layout checks still govern reuse. Policy retries search the same clients for compatible alternatives without the hash filter. `--infohash` leaves `--onlyID`, the **Keep images** metadata setting and client-injection choices unchanged. `--rehash` retains metadata discovery but generates a fresh torrent. With `--nohash`, trackers whose torrent restrictions cannot be satisfied by available artifacts are skipped. The former `--torrenthash` and `-th` aliases are removed.

`--nohash` and `--rehash` cannot be combined, even with explicit false values.

Preparation selects the best compatible artifact among discovered torrents for each tracker, ranking candidates that need no partial-tail hashing ahead of those that do. Within each group, selection prefers 1,000–2,000 pieces and then the closest available count. Equal scores prefer already usable artifacts over layout conversions, then preserve candidate order within each category. Existing torrents are capped at 10,000 pieces unless using the effective maximum piece size makes a larger count unavoidable. Tracker limits and `--max-piece-size` take precedence over piece-count goals; torrent-file size limits still apply. Compatible existing pieces are reused without generating optional variants. With `--nohash`, missing artifacts are never generated: trackers without compatible existing pieces are skipped.

Ordinary torrent reuse checks the metainfo, requested file layout and applicable limits without reading every media byte. This does not prove that same-sized media bytes match the piece hashes. Use `--force-recheck` to request a piece check, or `--rehash` to create a fresh torrent. A reusable artifact may need a metadata-only layout change; `--nohash` permits that but fails if new pieces are required.

Reuse currently supports v1 torrents; v2 and hybrid v1/v2 torrents require a new torrent, so `--nohash` cannot use them.

## `serve`

```text
upbrr serve [options]
```

`--config` seeds an empty database. For an existing database, the server uses its stored settings. Use Settings or configuration import to activate later changes.

| Option                     | Purpose                                                                    |
| -------------------------- | -------------------------------------------------------------------------- |
| `--config <path>`          | Use a config file path.                                                    |
| `--addr <host:port>`       | Set the complete listen address.                                           |
| `--host <host>`            | Set the listen host.                                                       |
| `--port <port>`            | Set the decimal listen port, from `1` through `65535`.                     |
| `--base-url <url-or-path>` | Set the external Web UI URL or path prefix.                                |
| `--persist-listen`         | Persist listen host and port to `web-config.json`.                         |
| `--persist-web-config`     | Persist the full resolved Web UI settings, including listen host and port. |
| `--dev-no-auth`            | Disable Web auth for local development on loopback only.                   |

`--addr` cannot be combined with `--host` or `--port`. Without overrides, the listener defaults to `localhost:7480`; environment variables and saved settings can change it. `--live-test` and `--live-test-max-images` also apply to `serve`. See [Web server and reverse proxy](../configuration/web-server.md) for precedence and proxy examples.

## `auth`

Password changes and browse-policy changes after initial setup are available only through the local binary. The first authenticated Web UI setup may establish the initial browse policy. Stop `upbrr serve` before running either command, then restart it when the command completes.

After validation, each command creates a unique `web-auth.json.backup-*` beside the active auth file and prints its exact path. The path is still printed if a later update step fails. Backups contain sensitive authentication and encryption material; protect them like `web-auth.json` and remove obsolete copies manually.

Change the Web UI password interactively:

```powershell
.\upbrr.exe auth password
```

The command prompts for the current password, the replacement, and confirmation without accepting password flags. Retained browser sessions are revoked immediately after the current password is verified. If a later update step fails, sign in again with the existing password before retrying.

Replace all browse roots by passing each existing directory as a separate argument:

```powershell
.\upbrr.exe auth browse-roots "D:\Media" "E:\Downloads"
```

To remove the roots and explicitly allow unrestricted host browsing:

```powershell
.\upbrr.exe auth browse-roots --allow-unrestricted
```

`auth password` also accepts `--ui plain|auto|tui` (default `plain`) and `--ui-keep-open` (default false). `auth browse-roots` accepts `--allow-unrestricted` (default false) and requires at least one existing directory otherwise. Both subcommands accept `--help`.

Both subcommands accept `--config` when the active database is selected through a non-default config file. For containers, run the command in the same environment as upbrr and use paths visible inside the container.

## `api-token`

Create persistent bearer tokens in [Settings → API Tokens](../web-ui/settings/api-tokens.md). The authenticated Web UI displays each plaintext token once so it can be copied directly into a secret manager; CLI output never includes tokens.

List safe token metadata:

```powershell
.\upbrr.exe api-token list
```

Revoke by token ID:

```powershell
.\upbrr.exe api-token revoke tok_example
```

List and revoke accept `--config`.

See the [API reference](../api/index.md) before granting `workflow:execute`.

## `recover`

`upbrr recover [--config <path>]` accepts `--help` and no other options or positional arguments. It requires the existing database and uses interactive confirmations; it has no unattended switch. See [interaction and safety](#interaction-and-safety) before confirming any remote outcome.

## `live-test`

These development commands create an isolated profile from existing configuration and authentication state, or clean up image uploads recorded by that profile. They do not replace normal upload commands.

Run `upbrr live-test --help` to list these commands, or add `--help` after `init` or `cleanup` to see its options.

```text
upbrr live-test init --run-dir <path> [--config <path>] [--prefer-deletable-hosts]
upbrr live-test cleanup --run-dir <path>
```

Use double-hyphen option names for these subcommands. `init` requires a new immediate child of the operating system's private cache directory under `upbrr-live-testing/runs`; `cleanup` requires an existing run. `--config` selects the source configuration. `--prefer-deletable-hosts` defaults to false and changes image-host preferences only inside the new profile. The profile includes sensitive configuration and authentication material; keep it private.

To use the profile with an upload command or `serve`, pass `--live-test --config <profile-config-path>`. Tracker submission and torrent-client writes are disabled. `--live-test-max-images <count>` requires `--live-test`, accepts `0` through `500`, and defaults to `0`, which keeps captured images local. A positive value permits that many journaled image-upload attempts. The budget cannot change after the run starts.

Upload-mode `--live-test` cannot be combined with `--create-auth`, `--export-config`, `--import-config`, `--cleanup`, or `--delete-tmp`. Use the dedicated `live-test init` and `live-test cleanup` commands for profile management.

`cleanup` attempts deletion only for journaled images owned by the run and reports uploads that must remain on their host. Unknown or failed outcomes require manual reconciliation. Once cleanup starts, the profile cannot be used for further preparation. For the opt-in runner and safety requirements, see the [live-testing guide](https://github.com/autobrr/upbrr/blob/main/scripts/live-testing/README.md).

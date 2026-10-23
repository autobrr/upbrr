---
title: Logging
description: Configure runtime logging and use the Web UI live log viewer.
---

# Logging

Open **Logging** from the main navigation to configure application logging and inspect recent activity.

## Runtime settings

These edits require **Save** before they become active.

| Field              | Default | Effect                                                                                                          |
| ------------------ | ------- | --------------------------------------------------------------------------------------------------------------- |
| **Level**          | `info`  | Sets runtime verbosity from `trace` through `error`. `trace` records the most detail.                           |
| **File enabled**   | Off     | Writes application logs to the displayed host path.                                                             |
| **Max total size** | `20 MB` | Sets the approximate total file budget. The per-file rotation threshold is this value divided by **Max files**. |
| **Max files**      | `3`     | Sets the file-rotation depth used with the total-size budget.                                                   |

File logging requires positive **Max total size** and **Max files** values. The log path is `<database directory>/logs/upbrr.log`; displaying a path does not mean file logging is enabled.

## Choosing a level

- **Info** shows concise progress and outcomes. Source-inspection bookkeeping without a known byte total is kept at debug; failures remain warnings.
- **Debug** adds effective prepared-generation facts, including compatible cached results and explicit empty fields, concise provider results, and a bounded outcome for each tracker duplicate check. Provider metadata availability does not mean a new network fetch occurred.
- **Trace** adds detailed cache decisions, parser intermediates, search pages, candidate evidence, and workflow lifecycle counters. Use it when debug does not answer the specific question being investigated.

## Upload run verbosity

On **Upload**, choose **Run options → Log level** before starting a dry run or upload. The selected level applies to that run's application logs, including background work and resumed work. It reaches the live viewer, retained log entries, and file output when enabled.

This choice does not save the global logging setting or change another operation's verbosity. The server console keeps its configured threshold. The live viewer's level checkboxes still control which retained entries are displayed.

## Live viewer

**Connected** means the browser is receiving the live stream. The page first loads up to 1,000 recent sanitized entries, then appends new entries.

- Level checkboxes and search filter only the current display; they do not change runtime logging.
- **Clear** empties the browser buffer only. It does not delete the log file or clear the backend buffer.
- With **Auto-scroll** enabled, the browser retains the latest 1,000 entries. With it disabled, the buffer can grow to 10,000 entries before dropping the oldest entries and showing a warning.
- A muted pattern hides exact whole-message matches in the viewer. Adding, clicking, or removing a mute persists it immediately without **Save**; it does not suppress the underlying log event.

Torrent-client connection diagnostics omit configured private endpoints and retain safe operation and failure context.

The viewer receives centrally sanitized messages, but ordinary filesystem paths remain visible for troubleshooting, including in CLI diagnostics and log files. Paths may contain usernames, directories, filenames, and release names. Automatic redaction is intended to protect credentials, not to make every log safe to share. Inspect each excerpt and remove any remaining credentials, cookies, tokens, private URLs, private tracker data, and unrelated personal information before sharing.

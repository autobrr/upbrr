---
title: Logging
description: Configure log files and use the Web UI live log viewer.
---

# Logging

Open **Logging** from the main navigation to configure application logging and inspect recent activity.

Use the information icon beside a label or group heading for contextual help. Focus it with the keyboard or tap it on touch screens; Escape or an outside interaction dismisses it. Help does not change settings or filters.

## Runtime settings

These edits require **Save** before they become active.

| Field                          | Default | Effect                                                                                                                   |
| ------------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------ |
| **Runtime log level**          | `INFO`  | Sets the minimum severity for the server console and the default live viewer stream.                                     |
| **Write log files**            | Off     | Writes application logs to the displayed host path.                                                                      |
| **Total log-file budget (MB)** | `50 MB` | Sets the approximate total file budget. The per-file rotation threshold is this value divided by **Retained log files**. |
| **Retained log files**         | `3`     | Sets the file-rotation depth used with the total-size budget.                                                            |

File logging requires positive **Total log-file budget (MB)** and **Retained log files** values. The log path is `<database directory>/logs/upbrr.log`; displaying a path does not mean file logging is enabled. When enabled, files receive every level through TRACE. The live viewer and server console keep their configured verbosity. The input log captures through TRACE, even if the live viewer or console is less verbose. An existing custom file budget is unchanged.

## Choosing a level

- **Warn** highlights problems needing attention; **Error** limits output to failures.
- **Info** shows concise progress and outcomes. Source-inspection bookkeeping without a known byte total is kept at debug; failures remain warnings.
- **Debug** adds effective prepared-generation facts, including compatible cached results and explicit empty fields, concise provider results, and a bounded outcome for each tracker duplicate check. Provider metadata availability does not mean a new network fetch occurred.
- **Trace** adds detailed cache decisions, parser intermediates, search pages, candidate evidence, and workflow lifecycle counters. Use it when debug does not answer the specific question being investigated.

## Upload run verbosity

On **Upload**, choose **Run options → Log level** before starting a dry run or upload. The selected level applies to that run's application logs, including background work and resumed work. It reaches the live viewer and recent entries. An enabled log file already records TRACE.

This choice does not change another operation’s verbosity. The server console keeps its configured threshold. The live viewer's level checkboxes still control which retained entries are displayed.

## View or download one input's logs

In **History**, select the input and choose **Logs**. Opening, preparation, upload, and other retained operations for that input appear together in one log. **Download log** saves a text `.log` file through your browser, including earlier retained lines when the preview only shows the end. An input that failed before metadata was stored can still appear with processing logs only. If stored release history is unavailable, the list can still show retained input logs.

upbrr reads matching entries from the active and rotated application logs. When no matching disk entries remain, it can show entries still in the current session's input-log buffer. The page labels memory-only, partial and expired logs. Exports include up to 50 MB of log entries and prefer newer entries when that limit is reached, and are marked partial if entries are omitted; it cannot attribute older, untagged records to an input. If a capped export mixes older disk entries and in-memory entries, it may not know which record came first within the same second. Removing a History entry removes its log association, not the lines already written to the shared rotating files. Inspect and sanitize exports before sharing them.

## Live viewer

**Connected** means the browser has subscribed to the live stream. Up to 1,000 recent sanitized entries load in the background while live events append as they arrive, so rows can appear out of time order while connecting.

- **Visible levels** and **Search logs** filter only the current display; they do not change runtime logging. Search is a case-insensitive substring match on message text; an empty search restores messages allowed by the other filters.
- **Clear viewer** empties the browser buffer only. It does not delete the log file or clear the backend buffer.
- With **Auto-scroll** enabled, the browser retains the latest 1,000 entries. With it disabled, the buffer can grow to 10,000 entries before dropping the oldest entries and showing a warning.
- A muted message hides exact, case-sensitive whole-message matches in the viewer. Saved patterns trim leading and trailing whitespace, so messages with that padding are not matched after reload; clicking a row’s level badge can still hide them in the current view. Adding a message under **Muted messages**, clicking a row’s level badge, or removing a mute persists it immediately without **Save**; it does not suppress the underlying log event.

Torrent-client connection diagnostics omit configured private endpoints and retain safe operation and failure context.

The viewer receives centrally sanitized messages, but ordinary filesystem paths remain visible for troubleshooting, including in CLI diagnostics and log files. Paths may contain usernames, directories, filenames, and release names. Automatic redaction is intended to protect credentials, not to make every log safe to share. Inspect each excerpt and remove any remaining credentials, cookies, tokens, private URLs, private tracker data, and unrelated personal information before sharing.

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

## Live viewer

**Connected** means the browser is receiving the live stream. The page first loads up to 1,000 recent sanitized entries, then appends new entries.

- Level checkboxes and search filter only the current display; they do not change runtime logging.
- **Clear** empties the browser buffer only. It does not delete the log file or clear the backend buffer.
- With **Auto-scroll** enabled, the browser retains the latest 1,000 entries. With it disabled, the buffer can grow to 10,000 entries before dropping the oldest entries and showing a warning.
- A muted pattern hides exact whole-message matches in the viewer. Adding, clicking, or removing a mute persists it immediately without **Save**; it does not suppress the underlying log event.

The viewer receives centrally sanitized messages, but inspect entries before sharing them. Never publish credentials, cookies, tokens, announce URLs, filesystem details, or private tracker data.

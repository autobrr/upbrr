---
title: Web UI
description: Navigate the embedded browser workflow, settings, history, logging, and host browse access.
---

# Web UI

The Web UI is embedded in the upbrr binary and uses the same preparation, tracker, configuration, and persistence services as the CLI.

Start it with:

```powershell
.\upbrr.exe serve
```

The default address is [http://localhost:7480](http://localhost:7480).

## First-run setup

Create the administrator account in the browser, then configure one or more host browse roots in the initial setup screen. Browse roots control which folders the browser can open for release selection and file import.

Use a dedicated account password. Do not expose an unconfigured instance to an untrusted network.

## Release workspace

The left navigation follows the release workflow. Some pages appear or unlock only when their input exists.

| Page              | Purpose                                                                  |
| ----------------- | ------------------------------------------------------------------------ |
| **Input**         | Choose the source path, trackers, metadata IDs, and preparation options. |
| **Tracker Data**  | Review tracker-derived metadata when available.                          |
| **Blu-ray**       | Select Blu-ray playlist or candidate data when the source requires it.   |
| **Dupe Check**    | Review per-tracker search results, rules, and candidate upload names.    |
| **Screenshots**   | Generate, import, order, and select screenshots.                         |
| **Disc Menus**    | Capture DVD menus automatically or import disc-menu images.              |
| **Upload Images** | Publish selected images through configured hosts.                        |
| **Descriptions**  | Build and inspect tracker-specific rendered descriptions.                |
| **Upload**        | Preview payloads, dry run, approve eligible trackers, and submit.        |

Navigation guards prevent later operations from silently using missing or stale prerequisites. When a page is unavailable, read the notice and return to the required stage.

## Settings

Use **Settings** to manage:

- metadata services;
- tracker defaults, credentials, auth status, and tracker-owned fields;
- image-host priority and credentials;
- screenshot and description behavior;
- torrent creation and client integration;
- post-upload behavior;
- config import and export.

Saving settings activates the new runtime configuration. Recheck tracker auth and run a dry run after changing credentials or upload behavior.

See the [Settings reference](./settings/index.md) for every section, field behavior, and verification guidance.

## History

**History** shows retained releases and lets you reopen their overview. Deleting a release from History removes its stored release state; it does not delete the source media.

## Logging

**Logging** shows recent sanitized application logs, a live stream, runtime verbosity, and file-rotation settings.

Logs are designed for safer sharing, but inspect them before posting. Never include configuration exports, cookies, credentials, announce URLs, or private API payloads.

See [Logging](./logging.md) for persistence, filtering, rotation, and buffer behavior.

## Host browser

The host browser lists only configured roots unless unrestricted browsing was explicitly enabled. If a valid folder is missing:

1. verify the upbrr process or container can read it;
2. verify the path is mounted into the container when using Docker;
3. stop the server and replace the roots with `upbrr auth browse-roots <path>...`;
4. use the path as visible inside the container, not the host-only path.

Imported application config does not change browse roots. The Web UI route accepts only the initial browse policy; later password and browse-policy changes require the local CLI.

## Safe first use

Use **Dry Run** on the Upload page and disable client injection for the first test. Compare every tracker tab with the tracker's current upload form and rules before allowing submission.

See [Upload workflow](../workflow/index.md) for stage ownership and review boundaries.

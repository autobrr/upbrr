---
title: Quick start
description: Start the Web UI, complete first-run setup, configure services, and prepare a safe first upload.
---

# Quick start

This tutorial starts the embedded Web UI and takes one release through a non-submitting review.

## Prerequisites

Have these ready:

- an installed upbrr binary or running container;
- FFmpeg available to the upbrr process for screenshot generation;
- a release path that upbrr can read;
- the credentials required by your trackers and image hosts;
- optional torrent-client details for search and injection.

Most tracker workflows also need a TMDB API key. Requirements vary by tracker.

## 1. Start upbrr

From a Windows PowerShell terminal beside the extracted binary:

```powershell
.\upbrr.exe serve
```

upbrr listens on `localhost:7480` by default and opens a browser. If it does not open automatically, visit [http://localhost:7480](http://localhost:7480).

For Linux or macOS, run the equivalent executable:

```bash
./upbrr serve
```

## 2. Secure browser access

The first-run screens create the administrator account and initial browse policy. Choose a unique password, then set the narrowest folders containing your releases, such as `D:\releases` and `E:\Downloads`.

These roots limit which host folders the browser can select. Initial setup can explicitly allow unrestricted host browsing, but this broadens access. Later password or browse-policy changes require the local `upbrr auth` commands while the server is stopped.

:::caution Network exposure

Complete first-run setup before exposing port `7480` to other systems. Anyone who can reach an unconfigured instance can reach the setup screen.

:::

## 3. Configure integrations

Open **Settings** and configure only the services you use:

1. add the metadata credentials required by your trackers;
2. choose default trackers and configure their credentials;
3. configure at least one permitted image host;
4. configure screenshot behavior;
5. optionally configure torrent-client search and injection;
6. save settings;
7. test tracker authentication where the tracker exposes that action.

See [Configuration](../configuration/index.md) for storage, import, export, and setting groups.

## 4. Prepare a release

1. Open **Input**.
2. Select `D:\releases\Example.Release.2026.1080p-GRP` or enter its path.
3. Select the intended trackers.
4. fetch metadata;
5. review the parsed media fields and generated release name;
6. continue through duplicate checks, screenshots, image uploads, and descriptions;
7. use **Dry Run** or payload preview on the upload stage;
8. do not submit until every tracker view matches its current rules.

## 5. Try the CLI safety path

The equivalent non-submitting CLI run is:

```powershell
.\upbrr.exe --debug --no-seed "D:\releases\Example.Release.2026.1080p-GRP"
```

`--debug` suppresses tracker submission. `--no-seed` also prevents torrent-client injection.

## Expected result

You should have reviewed metadata, duplicate evidence, images, descriptions, and tracker payload previews without submitting an upload or injecting a torrent.

Next, learn the [upload workflow](../workflow/index.md) and the [required manual checks](../workflow/index.md#final-review-checklist).

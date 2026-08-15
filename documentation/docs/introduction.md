---
title: Introduction
description: What upbrr does, who it is for, and where operator review remains essential.
---

# Introduction

upbrr is an upload preparation app for private-tracker workflows. It combines metadata, duplicate checks, screenshots, image hosting, descriptions, tracker payload review, submission, and torrent-client injection in one workspace.

:::warning Alpha software

Quality-check every upload. Confirm the generated name, category, type, source, resolution, edition, description, images, torrent contents, and client settings against current tracker rules before submission.

:::

## Intended audience

upbrr is for uploaders who already understand:

- release naming and media classification;
- tracker categories, types, and upload rules;
- duplicate and trumping decisions;
- screenshot and description requirements;
- torrent creation, cross-seeding, and client injection.

It guides those tasks. It does not replace tracker rules, staff direction, or operator judgment.

## What upbrr does

A normal workflow can:

1. inspect a release folder or file;
2. fetch metadata and let you correct it;
3. prepare tracker-specific upload and search names;
4. run duplicate searches and eligibility checks;
5. generate or import screenshots and disc-menu images;
6. upload selected images to configured hosts;
7. build and preview tracker descriptions;
8. prepare immutable tracker payload previews;
9. submit approved uploads;
10. retain tracker-registered torrents and inject them into configured clients.

The embedded Web UI and CLI share the same configuration, preparation services, tracker adapters, and durable workflow state.

## Safety boundaries

- Duplicate results and tracker warnings need manual review.
- `--debug` runs the workflow without tracker submission. It can still perform local and remote preparation work and inject into clients unless `--no-seed` is also set.
- `--unattended` never prompts. Missing global decisions stop the run; tracker-specific manual prerequisites can leave only that tracker blocked.
- Tracker credentials, cookies, and API keys must never appear in issue reports or shared diagnostics.

Continue with the [quick start](./getting-started/quick-start.md), or read the [workflow explanation](./workflow/index.md) first.

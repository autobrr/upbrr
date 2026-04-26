---
sidebar_position: 1
title: Introduction
---

# upbrr

`upbrr` is a Go-based upload preparation and tracker submission tool for private-tracker workflows.

It shares one core across three surfaces:

- a command-line app in `cmd/upbrr`
- a Wails desktop GUI in `gui`
- an embedded web-serving mode exposed by `upbrr serve`

The preparation pipeline can resolve metadata, apply naming overrides, check dupes, plan screenshots, upload images, build descriptions, create tracker-specific payloads, create torrents, and hand completed uploads to configured seeding destinations.

## When to use it

Use `upbrr` when you want a local tool that keeps tracker upload preparation reproducible across interactive and unattended flows. The CLI is best for scripts and queue-style processing. The GUI and web mode are best when you want to inspect and adjust metadata, screenshots, descriptions, and tracker upload choices before continuing.

## Start here

- [Quick start](./getting-started/quick-start.md)
- [CLI usage](./usage/cli.md)
- [GUI and web mode](./usage/gui-web.md)
- [Upload workflow](./workflows/upload-workflow.md)

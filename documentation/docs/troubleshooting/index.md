---
title: Troubleshooting
description: Diagnose startup, browse access, reverse proxy, FFmpeg, tracker auth, image, and unattended workflow failures.
---

# Troubleshooting

Start with the shortest failing operation and the **Logging** page. Increase log level only long enough to reproduce the problem, then restore normal verbosity.

## Web UI does not open

1. Run `upbrr serve --help` and verify the intended host and port.
2. Check startup output for `listen` errors.
3. Open `http://localhost:7480` manually for the default configuration.
4. If using Docker, confirm the container is healthy and port `7480` is published.
5. If another process owns the port, select another with `--port`.

The default host is `localhost`, so another machine cannot connect unless you deliberately bind a network address or use Docker's `0.0.0.0` default.

## First-run setup is exposed

Stop the instance or restrict its port to loopback until setup is complete. First-run account creation is reachable by anyone who can reach an unconfigured instance.

## A release folder is missing from the browser

- Confirm the path is inside a configured browse root.
- Confirm the upbrr process can read the folder.
- In Docker, mount the folder and select its container path, such as `/data/...`.
- Remember that config import does not import browse roots.

Do not enable unrestricted browsing solely to hide a mount or permission error.

## A reverse-proxied page partly works

If HTML loads but assets, login, API calls, or live progress fail under `/upbrr/`:

1. set `--base-url /upbrr/` or `UPBRR_WEB_BASE_URL=/upbrr/`;
2. keep `/upbrr/` on both sides of `proxy_pass`;
3. inspect browser requests for `/upbrr/api/...` and `/upbrr/assets/...`;
4. disable proxy buffering for live event streams;
5. configure trusted proxies when HTTPS terminates upstream.

See the [reverse proxy guide](../configuration/web-server.md#path-prefix-proxy).

## FFmpeg is not found

Run `ffmpeg -version` in the same environment that starts upbrr. On Windows, add FFmpeg's directory to `PATH` before starting upbrr or its service. Containers already include FFmpeg.

The Web UI **Application Details** surface reports detected FFmpeg capability without exposing the local executable path.

## Automatic DVD menu capture fails

Automatic capture accepts an extracted DVD directory containing `VIDEO_TS`, or `VIDEO_TS` itself. It does not accept ISO images, optical drives, or Blu-ray menus.

The selected FFmpeg must expose the `dvdvideo` demuxer plus `menu`, `menu_lu`, `menu_vts`, `pgc`, and `pg`. Encrypted, protected, unreadable, region-restricted, or corrupt inputs can still fail. upbrr does not provide CSS decryption.

Use manual disc-menu image import when automatic capture is not available.

## Tracker authentication is blocked

1. Open the tracker in **Settings**.
2. confirm every required field is present;
3. import a current cookie or use the supported login flow;
4. complete 2FA when requested;
5. run the tracker auth test;
6. retry preparation.

Upload preflight does not silently log in or mutate auth state. Fix auth on the dedicated Settings surface.

## No screenshots or image links

- Confirm FFmpeg access and screenshot count.
- Confirm the chosen image host is configured and allowed by the tracker.
- Review host-specific failure messages without copying credentials or raw responses.
- Check `min_successful_image_uploads`: zero requires the whole batch to succeed; a positive value permits a partially successful batch only after that many images publish.
- Use `--skip-imagehost-upload` only when you will supply valid hosted images another way.

## Config import reports warnings

Warnings can identify unknown legacy keys, unsupported tracker fields, or unsupported image-host settings. Open Settings and correct each affected group. An import warning is not proof that the remaining configuration is ready for live upload.

## Unattended mode stops

`--unattended` never prompts. It stops when a workflow-global decision or unsafe ambiguity needs operator input. Use `--unattended_confirm` only when prompts are acceptable, or provide the missing input explicitly.

A tracker-specific manual prerequisite can block that tracker while other viable tracker lanes continue.

## Safe issue reports

Include:

- upbrr version and operating system;
- binary or Docker installation;
- the shortest reproduction using `Example.Release.2026.1080p-GRP`;
- sanitized logs around the failure;
- expected and actual stage outcome.

Exclude config exports, API keys, passwords, cookies, passkeys, announce URLs, OTP data, private tracker pages, and raw request or response bodies.

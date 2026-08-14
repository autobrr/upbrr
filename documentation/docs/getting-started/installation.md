---
title: Installation
description: Install upbrr from a release binary or Docker image and provide FFmpeg.
---

# Installation

upbrr ships as a single binary with the Web UI embedded. Release automation builds these targets:

| Operating system | Architectures       |
| ---------------- | ------------------- |
| Windows          | amd64, arm64        |
| Linux            | amd64, arm64, armv7 |
| macOS            | amd64, arm64        |

Docker images are published for Linux amd64 and arm64.

## Release binary

1. Open [GitHub Releases](https://github.com/autobrr/upbrr/releases).
2. Download the archive matching your operating system and CPU.
3. Extract it into a directory owned by your user.
4. Verify the executable starts:

   ```powershell
   .\upbrr.exe --version
   ```

5. Start the Web UI:

   ```powershell
   .\upbrr.exe serve
   ```

On Linux or macOS, use `./upbrr` instead of `.\upbrr.exe`. If required, make it executable with `chmod +x ./upbrr`.

## FFmpeg

FFmpeg is required for screenshot generation.

- **Windows:** install FFmpeg and add its executable directory to `PATH` before starting upbrr.
- **Linux:** install FFmpeg through your distribution package manager.
- **macOS:** install with Homebrew using `brew install ffmpeg`, then start upbrr from an environment that includes Homebrew's binary directory.

Automatic DVD menu capture has stricter requirements. The selected FFmpeg must expose the `dvdvideo` demuxer and its menu-coordinate options. upbrr checks this at runtime; a version number alone does not prove support.

## Docker Compose

The repository includes a maintained [Compose example](https://github.com/autobrr/upbrr/blob/main/example-docker-compose.yml). Save it as `docker-compose.yml`, replace both host paths, then start it:

```bash
docker compose up -d
```

The image:

- serves the Web UI on `0.0.0.0:7480`;
- stores application state below the `/config` volume;
- expects release data below mounted paths such as `/data`;
- runs as uid and gid `1000:1000` by default;
- includes FFmpeg and the fonts needed by screenshot overlays.

On Linux, create bind-mount directories before starting and give the container user read/write access:

```bash
mkdir -p /path/to/config /path/to/torrents
sudo chown -R 1000:1000 /path/to/config /path/to/torrents
```

For production, replace `latest` with a specific release tag. If you publish port `7480` on all interfaces, finish first-run account setup promptly. Bind to `127.0.0.1:7480:7480` or use a reverse proxy when LAN access is not required.

## Next step

Complete the [quick start](./quick-start.md). For subdomain or path-prefix proxying, see [Web server and reverse proxy](../configuration/web-server.md).

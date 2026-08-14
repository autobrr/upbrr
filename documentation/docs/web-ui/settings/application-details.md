---
title: Application Details
description: Read build, platform, FFmpeg capability, and uptime diagnostics from the running instance.
---

# Application Details

**Settings → Application Details** is read-only. It reports the current process rather than persisted configuration.

| Detail               | Meaning                                                            |
| -------------------- | ------------------------------------------------------------------ |
| **Project**          | Link to `autobrr/upbrr`.                                           |
| **Version**          | Release version reported by the binary.                            |
| **Build**            | Build identifier compiled into the binary.                         |
| **Go Runtime**       | Go version used by the process.                                    |
| **DVD Menu Engine**  | Detected DVD menu engine version.                                  |
| **FFmpeg DVD Menus** | Available, incompatible, or unavailable, with the detected reason. |
| **FFmpeg Version**   | FFmpeg version seen by the DVD menu capability check.              |
| **Platform**         | Runtime OS and architecture, shown as `GOOS/GOARCH`.               |
| **Uptime**           | Elapsed process uptime.                                            |

Auth settings, bind addresses, and storage paths are intentionally excluded. For a support request, share the relevant version, build, platform, capability message, and sanitized logs; never attach raw configuration or credentials.

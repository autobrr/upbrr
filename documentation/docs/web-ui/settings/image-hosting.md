---
title: Image Hosting settings
description: Order image-upload hosts and configure only the credentials each selected host needs.
---

# Image Hosting settings

Use **Settings → Image Hosting** to define global image-upload candidates. **Host 1** has highest priority; blank and duplicate entries are ignored. Tracker policy can restrict the candidates, and a tracker-specific **Image host** setting can override the global preference.

## Configure host priority

Choose up to six hosts in preferred order. The Web UI reveals credential fields only for selected hosts.

| Host         | Required setting         |
| ------------ | ------------------------ |
| ImgBB        | API key                  |
| ImgBox       | None                     |
| Pixhost      | None                     |
| Lensdump     | API key                  |
| PTScreens    | API key                  |
| OnlyImage    | API key                  |
| Dalexni      | API key                  |
| Zipline      | Base URL and API key     |
| PassTheImage | API key                  |
| Seedpool CDN | API key                  |
| ShareX       | Endpoint URL and API key |
| UTPPM        | API key                  |

When an allowed host fails, upbrr can try the next eligible configured host. A host rejected by the target tracker's policy is skipped regardless of its global position.

## Additional hosts

**Lostimg**, **ReelFliX**, and **Samaritano** are conditional, tracker-owned integrations. Enable one and enter its API key only when the corresponding tracker advertises support. They are not general global fallback slots; Samaritano is available only for SAM.

## Verify the change

Save, open **Upload Images**, select test images, and inspect the planned host per tracker before uploading. A successful image upload does not prove every tracker accepts that host; verify the generated description or dry-run preview too.

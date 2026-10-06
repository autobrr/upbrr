---
title: Image Hosting settings
description: Order image-upload hosts and configure only the credentials each selected host needs.
---

# Image Hosting settings

Use **Settings → Image Hosting** to define global image-upload candidates. **Host 1** has highest priority; blank and duplicate entries are ignored. Tracker policy can restrict the candidates, and a tracker-specific **Image host** setting can override the global preference.

## Configure host priority

Choose up to six hosts in preferred order. The Web UI reveals credential fields only for selected hosts.

Choose from the hosts advertised in the running application and provide only their requested credentials. Available choices can depend on the selected destination.

Tracker-specific host choices still apply after warning acknowledgement and when existing images are reused. Cached images on another host do not replace the configured choice.

When an allowed host fails, upbrr can try the next eligible configured host. A host rejected by the target tracker's policy is skipped regardless of its global position.

Imported tracker images still follow the destination's host policy. upbrr resolves supported thumbnail and proxy URLs to full-size sources where possible. Links tied to the source tracker require an eligible image host; this also applies to images inside imported comparison blocks. If a required image cannot be downloaded or rehosted, preparation reports the failure instead of treating the source URL as a valid destination upload.

## Additional hosts

Some integrations are available only when advertised by a configured tracker. They are not general global fallback slots.

## Verify the change

Save, open **Upload Images**, select test images, and inspect the planned host per tracker before uploading. A successful image upload does not prove every tracker accepts that host; verify the generated description or dry-run preview too.

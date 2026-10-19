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

Tracker-specific host choices still apply after warning acknowledgement and when existing images are reused. Cached images on another host do not replace the configured choice.

When an allowed host fails, upbrr can try the next eligible configured host. A host rejected by the target tracker's policy is skipped regardless of its global position.

Imported tracker images still follow the destination's host policy. upbrr resolves supported thumbnail and proxy URLs to full-size sources where possible. Links tied to the source tracker, including PTP-hosted images when uploading elsewhere, require an eligible image host; this also applies to images inside imported comparison blocks. If a required image cannot be downloaded or rehosted, preparation reports the failure instead of treating the source URL as a valid destination upload.

## Additional hosts

**Lostimg**, **ReelFliX**, and **Samaritano** are conditional, tracker-owned integrations. Enable one and enter its API key only when the corresponding tracker advertises support. They are not general global fallback slots; Samaritano is available only for SAM.

## Verify the change

Save, open **Upload Images**, select test images, and inspect the planned host per tracker before uploading. A successful image upload does not prove every tracker accepts that host; verify the generated description or dry-run preview too.

## Use slow.pics collection links for analysis

Choose **Comparison and analysis host → slow.pics collection links**, or set
`image_hosting.analysis_host: slowpics` in the shared CLI/server configuration.
Leaving it empty preserves ordinary hosting. Do not put `slowpics` in Host 1–6
or a tracker's image-host override: it cannot host final screenshots, previews,
or DVD menus.

Collection mode supports existing comparison, HDR-analysis and audio-analysis
images. Each top-level `--comparison` file or folder becomes one flat set; folders
retain their sorted accepted-file order and `--comparison-primary` still reorders
inputs. It does not align source frames or pair files across folders. All members
of a comparison set must remain selected. Reattaching a complete explicit group
replaces that group, including after a restart, and preserves unrelated groups.
Partial group attachments are rejected. Older flat attachments keep their
ordinary route; reattach them to retain set boundaries. PNG bytes are required;
JPEG and WebP are rejected before creating a remote collection.

Descriptions contain one labeled collection link per completed set, never
embedded slow.pics images or individual-image links. Existing HDR/audio producers
supply the images and categories; audio statistics remain text. Saved final
edited descriptions are preserved. Explicit image-upload skip does not create
collections and can reuse only a complete compatible cached set.

slow.pics collection links are direct links in descriptions, so approved-image-host
restrictions do not apply to them. Ordinary screenshot uploads and analysis images
uploaded without slow.pics still follow the destination's image-host policy.
Grouped comparisons in collection mode do not fall back to ordinary image uploads.

Uploads use a new anonymous session and request `public=false` and
`optimizeImages=false`. These parameters are not guarantees of private,
access-controlled or lossless hosting. Live anonymous availability, single-image
acceptance, visibility, retention and service limits have not been verified.
Local safety limits are 100 images, 20 MiB per PNG and 100 MiB per collection.

A collection link is retained only after every member succeeds. If creation or
member upload has an uncertain outcome, automatic recreation is blocked across
restarts, including an unchanged set in a new preparation generation. Local
images are preserved. There is no automatic remote cleanup or account-login
support. Check slow.pics before resolving an uncertain attempt. Only select
**I verified no collection was created** after confirming that no remote set
exists. This releases that attempt's block without uploading anything. Retry
description generation separately when ready. An upload still in progress cannot
be cleared, and a stale confirmation cannot clear a newer attempt. Older
reservations created before this recovery mechanism remain blocked because they
lack the authority needed for this confirmation. Cloudflare blocking is reported
without bypassing it.

Deleting an input from **History** also removes its saved collection links and
local slow.pics attempts, including uncertain-upload blocks. This is an explicit
reset: it does not delete remote collections or upload anything. A later upload
can create a new collection, so first check slow.pics if the previous attempt
had an uncertain outcome to avoid duplicates. History cannot be deleted while
that input has a live operation.

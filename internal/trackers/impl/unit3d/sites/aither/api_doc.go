// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

// Aither API contract
//
// Aither is UNIT3D-based but publishes a site-specific API reference and adds
// behavior beyond the upstream UNIT3D baseline. This package owns Aither's
// deviations; shared request construction remains in the parent unit3d package.
// All requests use the configured AITHER base URL and Bearer API key.
//
// Upload
//
// POST /api/torrents/upload uses the shared multipart UNIT3D upload contract.
// Aither additionally receives these HDR classification fields:
//
//   - dv: 1 when the release contains Dolby Vision; otherwise 0.
//   - hdr10p: 1 when HDR10+ is present.
//   - hdr: 1 for other HDR or HLG content. HDR10+ takes precedence, so hdr is
//     not added by the Aither profile when hdr10p is set.
//
// The shared payload also carries deployment-supported fields such as stream,
// sd, pack, internalrip, keywords, and mod_queue_opt_in when applicable. Their
// inclusion is driven by prepared metadata and effective tracker configuration.
//
// Torrent lookup and duplicate search
//
// GET /api/torrents/:id fetches one torrent record. When no tracker ID is known,
// GET /api/torrents/filter supplies the standard UNIT3D filter fallback.
// Duplicate search uses tmdbId, categories[], resolutions[], optional types[],
// perPage=100, and an optional season token. Returned candidates are evaluated
// with Aither policy: trumpable IDs are tracked, DVD groups must match, SD may
// match HD, and 1080p size variance is permitted.
//
// Release-group blacklist
//
// GET /api/blacklists/releasegroups returns Aither's dynamic banned release
// groups. This endpoint is an Aither extension, not part of the standard UNIT3D
// torrent API.
//
// Request contract:
//
//   - Method: GET.
//   - Authentication: Authorization: Bearer <API key>.
//   - Response format: application/json.
//   - Query: per_page=100 on every request; cursor=<next_cursor> after the first
//     page.
//   - Timeout: 20 seconds per HTTP request.
//
// Accepted response shapes are intentionally tolerant because deployed UNIT3D
// variants have returned several compatible representations. The decoder accepts
// a top-level array, a single group record, or a paginated object containing data
// and meta.next_cursor. Each data item may be a string or an object whose group
// name is stored under name, group, release_group, releaseGroup, or the same keys
// beneath a JSON:API attributes object. Blank or unrecognized records are ignored.
// Pagination stops when next_cursor is blank, rejects repeated cursors, and is
// capped at 1,000 pages.
//
// Successful bodies are bounded to 4 MiB and must contain exactly one JSON value.
// Non-2xx responses are reported by status only; response bodies are not copied
// into errors or logs. The normalized names and raw source records are persisted
// under the application's banned-group cache with mode 0600. A cache remains
// fresh for 24 hours. Refresh failure preserves the previous cache; cancellation
// is checked before fetching and before durable replacement.
//
// Aither registers this endpoint through BannedGroupPolicy with RequireAPIKey.
// Therefore a missing key skips remote refresh rather than making an anonymous
// request. The generic banned-group evaluator merges cached names with static
// tracker-owned groups and compares normalized release-group names.
//
// Internal-release claims
//
// GET /api/internals/claim returns active internal-release claims. This endpoint
// is also Aither-specific and is enabled by Aither's API-backed ClaimPolicy.
//
// Request contract:
//
//   - Method: GET.
//   - Authentication: Authorization: Bearer <API key>.
//   - Response format: application/json.
//   - Query: per_page=100 and optional cursor from the previous response.
//   - Timeout: 20 seconds per HTTP request.
//
// The response is a JSON:API-style object. data contains records with an
// attributes object; meta.next_cursor selects the next page. Relevant attributes
// are title, season, tmdb_id, categories, resolutions, and types. season and
// tmdb_id may be JSON numbers or numeric strings. Records without a non-zero,
// parseable tmdb_id are discarded. Category, resolution, and type arrays are
// copied before caching so decoded response storage is not retained by callers.
//
// Claims are cached for 24 hours in the application's cache/banned directory as
// AITHER_claimed_releases.json with mode 0600. A fresh cache avoids network I/O.
// Missing base URL or API key produces no claims and no anonymous request. Any
// request construction, transport, non-2xx, or decode failure aborts claim
// evaluation and is returned with tracker context; remote response bodies are not
// included.
//
// Matching uses upload identity, not title alone. A claim requires the current
// release's TMDB ID, canonical Unit3D category, type, and resolution to match the
// claim. TV claims additionally require the canonical provider-resolved season;
// parsed filename season fallbacks are deliberately ignored. A semantic match
// adds the claim tracker block and claim_active rule failure before dupe checking
// or upload. A non-match leaves the release eligible.
//
// Naming and validation
//
// Aither naming inserts foreign-language markers for applicable non-English
// releases and applies DVD/DVD-rip normalization. Upload rules require a unique
// external ID and English audio or subtitles for non-disc releases, while
// allowing the original language.
//
// Site reference: https://aither.cc/pages/api#torrent-POSTapi-torrents-upload
// Upstream reference: https://github.com/HDInnovations/UNIT3D/blob/master/book/src/torrent_api.md

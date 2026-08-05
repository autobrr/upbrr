// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

// Unit3D upload contract
//
// The upstream UNIT3D Torrent API documents uploads as multipart POST requests
// to /api/torrents/upload. API authentication may be supplied as an api_token
// query/form value or as a Bearer token. upbrr uses Bearer authentication and
// never includes credentials in diagnostic payload output; see
// authentication_doc.go for the local security contract.
//
// Upstream request fields are grouped as follows:
//
//   - Files: torrent and optional nfo.
//   - Description data: name, description, mediainfo, and bdinfo.
//   - Classification IDs: category_id, type_id, resolution_id, region_id, and
//     distributor_id.
//   - TV coordinates: season_number and episode_number.
//   - External IDs: tmdb, imdb, tvdb, mal, and the games-only igdb field.
//   - Release flags: anonymous, personal_release, and internal.
//   - Staff/internal controls: refundable, featured, free, fl_until, doubleup,
//     du_until, sticky, and mod_queue_opt_in.
//
// Boolean values are encoded through boolFlag. Category, type, resolution, and
// external identifiers are resolved by the common Unit3D engine before site
// profiles apply their overrides. TV-only season and episode fields are omitted
// for movie uploads. The torrent and NFO fields are attached as multipart files;
// all remaining values are form fields.
//
// Individual UNIT3D deployments may extend the upstream contract. Fields such
// as internalrip, stream, sd, pack, keywords, modq, and site-specific IDs are
// compatibility extensions selected by site profiles or tracker configuration;
// they are not part of the upstream baseline documented above.
//
// Source: https://github.com/HDInnovations/UNIT3D/blob/master/book/src/torrent_api.md

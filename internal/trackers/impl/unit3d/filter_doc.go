// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

// Unit3D filter contract
//
// The upstream torrent filter endpoint is GET /api/torrents/filter. It returns
// a paginated torrent index and accepts optional query parameters in these
// groups:
//
//   - Pagination and ordering: perPage, sortField, and sortDirection.
//   - Text matching: name, description, mediainfo, bdinfo, uploader, keywords,
//     and file_name.
//   - Release dates and taxonomy: startYear, endYear, categories[], types[],
//     resolutions[], and genres[].
//   - External IDs: tmdbId, imdbId, tvdbId, and malId.
//   - Collection membership: playlistId and collectionId.
//   - Promotion and provenance: free, doubleup, featured, refundable,
//     highspeed, internal, and personalRelease.
//   - Swarm state: alive, dying, and dead.
//   - TV coordinates: seasonNumber and episodeNumber.
//
// upbrr builds only the subset needed for duplicate detection in dupe_params.go,
// encodes parameters with net/url.Values, and normalizes returned torrents in
// the shared tracker-data client. Some deployments expose additional endpoints,
// such as /api/torrents/pending; those are site compatibility extensions and not
// part of the upstream filter contract.
//
// Source: https://github.com/HDInnovations/UNIT3D/blob/master/book/src/torrent_api.md

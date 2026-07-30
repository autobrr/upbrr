// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

// Unit3D API authentication
//
// UNIT3D documents three API-token transports: an api_token query parameter,
// an api_token form field, or an Authorization header using the Bearer scheme.
// upbrr deliberately uses only Bearer authentication for Unit3D upload, lookup,
// and filter requests. trackerdata.SetUnit3DAPIHeaders trims the configured key,
// sets Authorization when the key is non-empty, and requests JSON responses.
// It does not add api_token to URLs or multipart form data, which keeps tokens out
// of request URLs and upload payload diagnostics.
//
// API keys come from the effective tracker configuration resolved by
// trackerdata.TrackerAPIKey. A missing key leaves the request unauthenticated;
// callers decide whether that is a configuration failure or a request the remote
// server may reject. Credentials, authorization headers, and token-bearing URLs
// must remain excluded from logs, errors, dry-run payloads, and failure artifacts.
//
// Source: https://github.com/HDInnovations/UNIT3D/blob/master/book/src/torrent_api.md

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package redaction

import (
	"net/url"
	"strings"
)

// TrackerPageURL sanitizes a public tracker-page link while retaining its opaque
// identity and known page route. Invalid URLs and URLs without an HTTP(S) host
// return an empty string. User information, fragments, and all query parameters
// except single safe id, torrentid, hash, and page=torrent-details values are removed.
// Host and path still undergo generic redaction. Download URLs must not use this boundary.
func TrackerPageURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	public := make(url.Values)
	if query, queryErr := url.ParseQuery(parsed.RawQuery); queryErr == nil {
		for _, key := range []string{"id", "torrentid", "hash"} {
			values := query[key]
			if len(values) == 1 && values[0] != "" && strings.Trim(values[0], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~") == "" {
				public.Set(key, values[0])
			}
		}
		if values := query["page"]; len(values) == 1 && values[0] == "torrent-details" {
			public.Set("page", values[0])
		}
	}
	parsed.RawQuery, parsed.Fragment, parsed.RawFragment = "", "", ""
	parsed.User, parsed.ForceQuery = nil, false
	// Preserve heuristic redaction on the host/path, while public identifiers
	// may legitimately be long hexadecimal values.
	base := RedactValue(parsed.String(), nil)
	if len(public) > 0 {
		return base + "?" + public.Encode()
	}
	return base
}

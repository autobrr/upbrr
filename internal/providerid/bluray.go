// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package providerid

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ParseBlurayRelease validates a positive release ID or a Blu-ray.com movie/DVD
// release URL. It returns an HTTPS URL on the provider's canonical host;
// arbitrary hosts, credentials, ports and non-release paths are rejected.
func ParseBlurayRelease(value string) (id, target string, err error) {
	value = strings.TrimSpace(value)
	validID := func(s string) bool {
		n, e := strconv.ParseUint(s, 10, 63)
		return e == nil && n > 0 && strconv.FormatUint(n, 10) == s
	}
	if validID(value) {
		return value, "https://www.blu-ray.com/movies/movies.php?id=" + value, nil
	}
	u, e := url.Parse(value)
	if e != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" ||
		(!strings.EqualFold(u.Hostname(), "www.blu-ray.com") && !strings.EqualFold(u.Hostname(), "blu-ray.com")) {
		return "", "", errors.New("bluray.com: supply a positive release ID or a Blu-ray.com release URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	query, queryErr := url.ParseQuery(u.RawQuery)
	switch {
	case len(parts) == 3 && (parts[0] == "movies" || parts[0] == "dvd") && validID(parts[2]) && u.RawQuery == "":
		id = parts[2]
	case (u.Path == "/movies/movies.php" || u.Path == "/dvd/movies.php") && queryErr == nil && len(query) == 1 && len(query["id"]) == 1 && validID(query.Get("id")):
		id = query.Get("id")
	default:
		return "", "", errors.New("bluray.com: URL does not identify one release")
	}
	u.Scheme, u.Host, u.Fragment = "https", "www.blu-ray.com", ""
	return id, u.String(), nil
}

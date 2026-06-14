// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehost

import (
	"net/url"
	"strings"
)

// HostMapping maps URL domain patterns to normalized host names
var HostMapping = map[string]string{
	"ibb.co":               "imgbb",
	"pixhost.cc":           "pixhost",
	"pixhost.to":           "pixhost",
	"imgbox.com":           "imgbox",
	"beyondhd.co":          "bhd",
	"imagebam.com":         "bam",
	"onlyimage.org":        "onlyimage",
	"ptscreens.com":        "ptscreens",
	"img.passtheima.ge":    "passtheimage",
	"imgur.com":            "imgur",
	"postimg.cc":           "postimg",
	"digitalcore.club":     "sharex",
	"img.digitalcore.club": "sharex",
	"kshare.club":          "kshare",
	"img.pterclub.com":     "pterclub",
	"s3.pterclub.com":      "pterclub",
	"yes.ilikeshots.club":  "ilikeshots",
	// Add imgbox subdomain
	"imgbox": "imgbox",
	// Add common variations
	"i.ibb.co": "imgbb",
}

// ExtractHost extracts the normalized host name from a URL.
// Returns the mapped host name if found, otherwise returns the domain name.
func ExtractHost(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return ""
	}

	hostname := strings.ToLower(parsed.Host)

	// Try exact match first
	if mapped, ok := HostMapping[hostname]; ok {
		return mapped
	}

	// Try removing www. prefix
	if strings.HasPrefix(hostname, "www.") {
		withoutWWW := strings.TrimPrefix(hostname, "www.")
		if mapped, ok := HostMapping[withoutWWW]; ok {
			return mapped
		}
	}

	// Try matching by domain (extract main domain)
	// e.g., "cdn.imgbox.com" -> "imgbox.com"
	parts := strings.Split(hostname, ".")
	if len(parts) >= 2 {
		mainDomain := parts[len(parts)-2] + "." + parts[len(parts)-1]
		if mapped, ok := HostMapping[mainDomain]; ok {
			return mapped
		}

		// Also try the first part (subdomain-less) as a key
		// e.g., "imgbox.com" -> "imgbox"
		firstPart := parts[len(parts)-2]
		if mapped, ok := HostMapping[firstPart]; ok {
			return mapped
		}
	}

	// Return the hostname as-is if no mapping found
	return hostname
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// isRenamedRelease reports whether the source media was renamed away from its
// original scene/P2P release name. The high-signal, low-false-positive case is a
// grouped release (a trailing "-GROUP" tag, which scene/P2P releases always
// dot-delimit) whose on-disk name has had its dots replaced with spaces — e.g. a
// library manager rewriting "Fury.2014.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-HHWEB" to
// spaces. Trackers reject such modified releases ([Modified Release] Renamed), so
// detecting it lets us skip the upload before it is rejected.
//
// Detection is deliberately conservative to avoid false positives:
//   - it only fires on whitespace renames (the dominant library-manager behavior);
//     underscore/other separator renames are intentionally out of scope;
//   - it requires the parsed group to actually appear as the trailing "-GROUP"
//     suffix, so a mis-parsed token (e.g. an IMDb id) cannot trigger it;
//   - it skips names containing parentheses/brackets/braces, which are markers of
//     human/library naming (e.g. "Fury (2014) {imdb-tt2713180}"), never scene/P2P
//     names;
//   - it excludes personal releases and disc-based sources.
//
// Both the source path (folder) and the primary video file are checked, since the
// tracker inspects the file (MediaInfo "Complete name") and the in-torrent names.
func isRenamedRelease(meta api.PreparedMetadata) (bool, string) {
	if meta.PersonalRelease {
		return false, ""
	}
	if strings.TrimSpace(meta.DiscType) != "" {
		return false, ""
	}
	group := strings.TrimSpace(meta.Release.Group)
	if group == "" {
		return false, ""
	}

	for _, name := range candidateReleaseNames(meta) {
		if isRenamedReleaseName(name, group) {
			return true, fmt.Sprintf("source renamed from original release name (contains spaces): %q", name)
		}
	}
	return false, ""
}

// candidateReleaseNames returns the on-disk base names that should carry the
// release name (the source path and the primary video file), with any media
// extension stripped.
func candidateReleaseNames(meta api.PreparedMetadata) []string {
	names := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	for _, candidate := range []string{meta.SourcePath, meta.VideoPath} {
		base := strings.TrimSpace(filepath.Base(strings.TrimSpace(candidate)))
		if base == "" || base == "." || base == string(filepath.Separator) {
			continue
		}
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		names = append(names, base)
	}
	return names
}

// isRenamedReleaseName reports whether a single base name looks like a
// space-renamed copy of a "-group" scene/P2P release.
func isRenamedReleaseName(name, group string) bool {
	if name == "" {
		return false
	}
	if !strings.ContainsAny(name, " \t") {
		return false
	}
	// Parentheses/brackets/braces indicate human/library naming (Plex/Radarr/
	// Jellyfin), never a scene/P2P release name, so do not treat them as renames.
	if strings.ContainsAny(name, "()[]{}") {
		return false
	}
	// Require the parsed group to be the actual trailing "-GROUP" tag so a token
	// the parser mistook for a group (e.g. an id) cannot trigger a false positive.
	return strings.HasSuffix(strings.ToUpper(name), "-"+strings.ToUpper(group))
}

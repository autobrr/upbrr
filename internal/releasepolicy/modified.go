// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package releasepolicy applies release-wide upload eligibility rules.
package releasepolicy

import (
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ModifiedReleaseSubject contains only source and naming facts needed by the
// modified-release policy.
type ModifiedReleaseSubject struct {
	SourcePath         string
	VideoPath          string
	DiscType           string
	PersonalRelease    bool
	SceneRenamed       bool
	SceneRenamedReason string
	Release            api.ReleaseInfo
}

// DetectModifiedRelease reports whether source media was renamed away from its
// original scene/P2P release name. Trackers reject such modified releases
// ([Modified Release] Renamed), so detecting it lets us skip the upload before it
// is rejected. Two independent, deliberately high-signal cases are checked:
//
//   - *arr id tokens: Radarr/Sonarr inject "{tmdb-…}", "{imdb-…}", "{tvdb-…}",
//     jellyfin "[tmdb" style, or bracket/paren variants into renamed names
//     (e.g. "Example Release (2026) {imdb-tt1234567}"). These never occur in an
//     original scene/P2P name, so their presence alone marks a rename. This is
//     independent of the release group, which an *arr rename often strips.
//
//   - whitespace renames: a grouped release (a trailing "-GROUP" tag, which
//     scene/P2P releases always dot-delimit) whose on-disk name has had its dots
//     replaced with spaces — e.g. a library manager rewriting
//     "Example.Release.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP" to spaces.
//     This path is deliberately conservative: it only fires on whitespace
//     (underscore/other separator renames are out of scope), requires the parsed
//     group to be the actual trailing "-GROUP" suffix so a mis-parsed token (e.g.
//     an id) cannot trigger it, and skips names with parentheses/brackets/braces
//     (human/library naming markers) that lack an *arr id token.
//
// Personal releases and disc-based sources are excluded from all rename checks.
// Groups in standardRenameCheckSkippedGroups skip only the heuristic checks;
// SceneRenamed remains authoritative for those groups.
//
// Both the source path (folder) and the primary video file are checked, since the
// tracker inspects the file (MediaInfo "Complete name") and the in-torrent names.
func DetectModifiedRelease(meta ModifiedReleaseSubject) (bool, string) {
	if meta.PersonalRelease {
		return false, ""
	}
	if strings.TrimSpace(meta.DiscType) != "" {
		return false, ""
	}

	// srrdb scene detection (the imdb: fallback) authoritatively compares the
	// on-disk basename to the canonical scene media filename and sets this when
	// they differ. It is the strongest signal (a database match, not a heuristic),
	// so it is checked first and independent of the release group an *arr rename
	// may strip.
	if meta.SceneRenamed {
		reason := strings.TrimSpace(meta.SceneRenamedReason)
		if reason == "" {
			reason = modifiedReleaseReason
		}
		return true, reason
	}

	group := strings.TrimSpace(meta.Release.Group)
	if skipsStandardRenameCheck(group) {
		return false, ""
	}

	names := candidateReleaseNames(meta)

	// *arr id tokens mark a rename on their own, independent of the release group
	// (which an *arr rename often strips), so check them before the group gate.
	for _, name := range names {
		if arrRenameToken(name) != "" {
			return true, modifiedReleaseReason
		}
	}

	if group == "" {
		return false, ""
	}
	for _, name := range names {
		if isRenamedReleaseName(name, group) {
			return true, modifiedReleaseReason
		}
	}
	return false, ""
}

// modifiedReleaseReason is deliberately generic: it discloses neither the
// on-disk name nor which signal fired, so a modified release cannot be trivially
// resolved by renaming the file back. The source should be investigated (file
// hash, provenance) rather than papered over with a rename.
const modifiedReleaseReason = "source appears renamed or modified from its original release name; verify the file hash and source provenance"

// standardRenameCheckSkippedGroups contains release groups that bypass only the
// standard heuristic rename checks. SRRDB scene rename matches still apply.
var standardRenameCheckSkippedGroups = map[string]struct{}{
	"HIDT": {},
	"SPHD": {},
}

// skipsStandardRenameCheck reports whether group is configured to bypass the
// standard non-scene rename heuristics.
func skipsStandardRenameCheck(group string) bool {
	_, ok := standardRenameCheckSkippedGroups[strings.ToUpper(strings.TrimSpace(group))]
	return ok
}

// arrReleaseIDTokens are the Radarr/Sonarr id-injection tokens that appear in
// renamed filenames (e.g. "Example Release (2026) {imdb-tt1234567}") and never
// in an original scene/P2P release name.
var arrReleaseIDTokens = []string{
	"{tmdb-", "{imdb-", "{tvdb-",
	"[tmdb", "(tmdb",
	"[imdb", "(imdb",
	"[tvdb", "(tvdb",
}

// arrRenameToken returns the first *arr id-injection token present in name
// (case-insensitive), or "" when none is found.
func arrRenameToken(name string) string {
	lower := strings.ToLower(name)
	for _, token := range arrReleaseIDTokens {
		if strings.Contains(lower, token) {
			return token
		}
	}
	return ""
}

// mediaFileExtensions are the on-disk media file extensions whose suffix is
// stripped from a candidate release name. A source path is frequently a
// directory whose dotted name legitimately ends in a token like "H.265-GROUP",
// so the extension must only be removed for actual media files — otherwise
// filepath.Ext would strip that trailing token and hide the "-GROUP" tag.
var mediaFileExtensions = map[string]struct{}{
	".mkv":  {},
	".mp4":  {},
	".avi":  {},
	".ts":   {},
	".m2ts": {},
	".m4v":  {},
	".mov":  {},
	".wmv":  {},
	".mpg":  {},
	".mpeg": {},
	".vob":  {},
	".iso":  {},
}

// candidateReleaseNames returns the on-disk base names that should carry the
// release name (the source path and the primary video file). A recognized media
// file extension is stripped; a directory basename is kept whole so dotted
// tokens in a renamed folder name are preserved.
func candidateReleaseNames(meta ModifiedReleaseSubject) []string {
	names := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	for _, candidate := range []string{meta.SourcePath, meta.VideoPath} {
		base := strings.TrimSpace(filepath.Base(strings.TrimSpace(candidate)))
		if base == "" || base == "." || base == string(filepath.Separator) {
			continue
		}
		if ext := filepath.Ext(base); ext != "" {
			if _, ok := mediaFileExtensions[strings.ToLower(ext)]; ok {
				base = strings.TrimSuffix(base, ext)
			}
		}
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

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/rls"
	"github.com/autobrr/rls/taginfo"
	"golang.org/x/text/unicode/norm"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

// ParsedRelease is the subset of basename parser output used by metadata
// provider searches.
type ParsedRelease struct {
	Title    string
	Alt      string
	Subtitle string
	Category string
	Year     int
}

// ParseRelease parses the host-path basename and normalizes its category to
// MOVIE, TV, or empty.
func ParseRelease(filename string) ParsedRelease {
	base := strings.TrimSpace(filename)
	if base == "" {
		return ParsedRelease{}
	}
	base = pathutil.Base(base)
	release := ParseReleaseTokens(base)
	return ParsedRelease{
		Title:    release.Title,
		Alt:      release.Alt,
		Subtitle: release.Subtitle,
		Category: ReleaseCategoryFromRLS(release.Type.String()),
		Year:     release.Year,
	}
}

var releaseSeasonTokenPattern = regexp.MustCompile(`(?i)^(?:s(?:eason|eries)?[ ._-]*(\d+)|(\d+)x)`)
var releaseAKAMarker = regexp.MustCompile(`(?i) aka `)
var releaseTagBuilder = rls.NewTagBuilder().Init(taginfo.All())

// ParseReleaseTokens parses source-name text with RLS. X-separated pairs and
// season tokens without exactly two or four digits remain ordinary text, so
// they cannot supply episodic values or a TV category to metadata searches.
func ParseReleaseTokens(name string) rls.Release {
	tags, end := rls.ParseTagsString(name)
	for i, tag := range tags {
		if !tag.Is(rls.TagTypeSeries) {
			continue
		}
		match := releaseSeasonTokenPattern.FindStringSubmatch(fmt.Sprintf("%o", tag))
		if len(match) > 0 && (match[2] != "" || len(match[1]) != 2 && len(match[1]) != 4) {
			tags[i] = tag.As(rls.TagTypeText, nil)
		}
	}
	release := releaseTagBuilder.Build(tags, end)
	if release.Alt == "" && (release.Type == rls.Movie || release.Type == rls.Series || release.Type == rls.Episode) {
		parts := releaseAKAMarker.Split(release.Title, -1)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			release.Title, release.Alt = parts[0], parts[1]
		}
	}
	return release
}

// TrimAKAPrefix removes a standalone AKA marker preceding an alternate title.
func TrimAKAPrefix(title string) string {
	title = strings.TrimSpace(title)
	if split := strings.IndexFunc(title, unicode.IsSpace); split >= 0 && strings.EqualFold(title[:split], "AKA") {
		return strings.TrimSpace(title[split:])
	}
	return title
}

// FormatAKA renders a non-empty alternate title with an AKA marker.
func FormatAKA(title string) string {
	return FormatOriginalAKA(TrimAKAPrefix(title))
}

// FormatOriginalAKA adds an AKA marker to raw title evidence.
func FormatOriginalAKA(title string) string {
	if title = strings.TrimSpace(title); title != "" {
		return "AKA " + title
	}
	return ""
}

// TitleIdentityKey compares titles across punctuation and spacing differences.
func TitleIdentityKey(title string) string {
	var key strings.Builder
	for _, r := range norm.NFC.String(title) {
		if unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) {
			key.WriteRune(unicode.ToLower(r))
		}
	}
	return key.String()
}

// DistinctTitle reports whether an alternate is sufficiently different from the primary.
func DistinctTitle(primary, alternate string) bool {
	return strings.TrimSpace(alternate) != "" && !strings.EqualFold(strings.TrimSpace(primary), strings.TrimSpace(alternate)) &&
		SimilarityRatio(TitleIdentityKey(primary), TitleIdentityKey(alternate)) < 0.7
}

// AnimeRomanizedTitle selects raw romanization bound to the effective MAL and TMDB IDs.
// The caller verifies the provider metadata's source and any published generation.
func AnimeRomanizedTitle(identity api.ExternalIdentity, metadata api.SourceScopedMetadata) string {
	tmdb := metadata.TMDB
	if identity.MALID <= 0 || identity.Overrides.MAL == api.OverrideStateClear ||
		tmdb == nil || identity.TMDBID <= 0 || tmdb.TMDBID != identity.TMDBID {
		return ""
	}
	category, err := api.NormalizeCanonicalCategory(string(identity.Category))
	tmdbCategory, tmdbErr := api.NormalizeCanonicalCategory(tmdb.Category)
	if err != nil || tmdbErr != nil || category != api.CanonicalCategoryUnknown && tmdbCategory != category {
		return ""
	}
	retained := ""
	if tmdb.MALID == identity.MALID {
		retained = TrimAKAPrefix(tmdb.RetrievedAKA)
	}
	if anilist := metadata.AniList; anilist != nil && anilist.MALID == identity.MALID &&
		(identity.Overrides.MAL == api.OverrideStateValue ||
			tmdb.Anime && retained == "" && (tmdb.MALID == 0 || tmdb.MALID == identity.MALID)) {
		return TrimTrailingParentheticalYear(FirstNonEmptyTrimmed(anilist.TitleRomaji, retained), tmdb.Year)
	}
	return retained
}

// NormalizeIMDbID adds the tt prefix and seven-digit padding to numeric IDs.
// Empty and zero values become empty; non-numeric input is preserved verbatim
// after trimming.
func NormalizeIMDbID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "0" {
		return ""
	}
	numeric := trimmed
	if len(numeric) >= len("tt") && strings.EqualFold(numeric[:len("tt")], "tt") {
		numeric = numeric[len("tt"):]
	}
	id, err := strconv.Atoi(numeric)
	if err != nil {
		return trimmed
	}
	return providerid.IMDb(id).Prefixed()
}

// ParseIMDbNumeric accepts a trimmed numeric or tt-prefixed ID and returns zero
// for malformed input.
func ParseIMDbNumeric(value string) int {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "tt")
	value = strings.Trim(value, "/")
	if value == "" {
		return 0
	}
	id, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return id
}

func FirstInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// FirstNonEmpty returns the first value containing non-whitespace text without
// trimming the returned value.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// FirstNonEmptyTrimmed returns the first non-empty value after trimming it.
func FirstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ReduceTitle splits on whitespace, removes standalone common video-extension
// tokens, and drops the requested number of trailing tokens. It returns empty
// when too few tokens remain.
func ReduceTitle(filename string, drop int) string {
	words := strings.Fields(filename)
	if len(words) <= drop {
		return ""
	}
	extensions := map[string]struct{}{
		"mp4":  {},
		"mkv":  {},
		"avi":  {},
		"webm": {},
		"mov":  {},
		"wmv":  {},
	}
	filtered := make([]string, 0, len(words))
	for _, word := range words {
		if _, ok := extensions[strings.ToLower(word)]; ok {
			continue
		}
		filtered = append(filtered, word)
	}
	if len(filtered) <= drop {
		return ""
	}
	return strings.Join(filtered[:len(filtered)-drop], " ")
}

// SimilarityRatio returns a case-sensitive, rune-aware recursive
// longest-common-substring score in the range 0 to 1.
func SimilarityRatio(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	matches := float64(matchCount([]rune(a), []rune(b)))
	if matches == 0 {
		return 0
	}
	total := float64(len([]rune(a)) + len([]rune(b)))
	return (2 * matches) / total
}

func AbsInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// ReleaseCategoryFromRLS collapses supported parser categories to MOVIE or TV
// and returns empty for unknown categories.
func ReleaseCategoryFromRLS(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	switch upper {
	case "MOVIE":
		return "MOVIE"
	case "EP", "EPS", "EPISODE", "SEASON", "SEASONPACK", "SERIES", "TV", "TVSHOW", "TV-SHOW":
		return "TV"
	default:
		return ""
	}
}

func matchCount(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	la, lb, length := longestCommonSubstring(a, b)
	if length == 0 {
		return 0
	}
	return length + matchCount(a[:la], b[:lb]) + matchCount(a[la+length:], b[lb+length:])
}

func longestCommonSubstring(a, b []rune) (int, int, int) {
	longest := 0
	endA := 0
	endB := 0

	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
	}

	for i, aRune := range a {
		row := i + 1
		for j, bRune := range b {
			col := j + 1
			if aRune == bRune {
				matrix[row][col] = matrix[row-1][col-1] + 1
				if matrix[row][col] > longest {
					longest = matrix[row][col]
					endA = row
					endB = col
				}
			}
		}
	}

	return endA - longest, endB - longest, longest
}

// TrimTrailingParentheticalYear removes an exact trailing parenthesized year
// only when that year is known. It trims outer whitespace and leaves other
// parenthetical text and different years unchanged.
func TrimTrailingParentheticalYear(title string, year int) string {
	trimmed := strings.TrimSpace(title)
	if year <= 0 {
		return trimmed
	}
	suffix := "(" + strconv.Itoa(year) + ")"
	if !strings.HasSuffix(trimmed, suffix) {
		return trimmed
	}
	return strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
}

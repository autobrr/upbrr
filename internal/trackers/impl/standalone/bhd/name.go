// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	bhdNoGroupSuffixPattern = regexp.MustCompile(`(?i)-(?:nogrp|nogroup|notag|unknown|unk)$`)
	bhdAudioChannelPattern  = regexp.MustCompile(`^(.+?)(\d+(?:\.\d+){1,2})$`)
)

// resolveUploadName preserves explicit names and normalizes generated BHD
// names using provider title, media, disc, and group rules.
func resolveUploadName(meta api.UploadSubject) string {
	if sceneName := strings.TrimSpace(meta.SceneName); sceneName != "" {
		return sceneName
	}
	name := selectedBHDReleaseName(meta)
	if !isBHDGeneratedReleaseName(meta, name) {
		return name
	}

	name = strings.Join(strings.Fields(name), " ")
	name = strings.ReplaceAll(name, "DD+", "DDP")
	name = applyBHDTitlePolicy(name, meta)
	name, audio := applyBHDAudioPolicy(name, meta.Audio)
	if isBHDFullDisc(meta) && IsDVDSource(meta.Source) {
		name = applyBHDDVDVideoAudioOrder(name, strings.TrimSpace(meta.VideoCodec), audio)
	}
	name = applyBHDSDRMarker(name, meta)
	name = applyBHDGroupPolicy(name, meta)
	return strings.Join(strings.Fields(name), " ")
}

func selectedBHDReleaseName(meta api.UploadSubject) string {
	return metautil.FirstNonEmptyTrimmed(
		strings.TrimSpace(meta.ReleaseName),
		strings.TrimSpace(meta.ReleaseNameNoTag),
		strings.TrimSpace(meta.Filename),
		pathutil.Base(meta.SourcePath),
	)
}

func isBHDGeneratedReleaseName(meta api.UploadSubject, name string) bool {
	name = strings.TrimSpace(name)
	variants := []api.ReleaseNameVariant{
		meta.GeneratedReleaseNames.IncludeEpisodeTitle,
		meta.GeneratedReleaseNames.OmitEpisodeTitle,
	}
	for _, variant := range variants {
		for _, candidate := range []string{variant.Name, variant.NameNoTag, variant.CleanName} {
			if name != "" && name == strings.TrimSpace(candidate) {
				return true
			}
		}
	}
	return false
}

// applyBHDTitlePolicy replaces generated title/year elements with authoritative
// provider metadata while retaining the technical suffix.
func applyBHDTitlePolicy(name string, meta api.UploadSubject) string {
	if isBHDTV(meta) {
		return applyBHDTVTitlePolicy(name, meta)
	}
	return applyBHDMovieTitlePolicy(name, meta)
}

func applyBHDMovieTitlePolicy(name string, meta api.UploadSubject) string {
	title, original, year := bhdMovieTitles(meta)
	if title == "" || year <= 0 {
		return name
	}
	nameYear := meta.Release.Year
	if nameYear <= 0 {
		nameYear = year
	}
	_, end, ok := findBHDLastNameElement(name, strconv.Itoa(nameYear))
	if !ok {
		return name
	}
	prefix := bhdTitlePrefix(title, original)
	return joinBHDName(prefix+" "+strconv.Itoa(year), name[end:])
}

// bhdMovieTitles prefers TMDB titles and the IMDb year, with release metadata and TMDB-year fallbacks.
func bhdMovieTitles(meta api.UploadSubject) (string, string, int) {
	title := strings.TrimSpace(meta.Release.Title)
	original := trimBHDAKAPrefix(meta.Release.Alt)
	year := meta.Release.Year
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Title) != "":
		title = strings.TrimSpace(meta.ProviderMetadata.TMDB.Title)
		if providerOriginal := trimBHDAKAPrefix(meta.ProviderMetadata.TMDB.OriginalTitle); providerOriginal != "" {
			original = providerOriginal
		}
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Title) != "":
		title = strings.TrimSpace(meta.ProviderMetadata.IMDB.Title)
		if providerOriginal := trimBHDAKAPrefix(meta.ProviderMetadata.IMDB.AKA); providerOriginal != "" {
			original = providerOriginal
		}
	}
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
		year = meta.ProviderMetadata.IMDB.Year
	} else if meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0 {
		year = meta.ProviderMetadata.TMDB.Year
	}
	return title, original, year
}

func applyBHDTVTitlePolicy(name string, meta api.UploadSubject) string {
	tvdb := meta.ProviderMetadata.TVDB
	if tvdb == nil {
		return name
	}
	evidence := tvdb.NameDisambiguation
	title := strings.TrimSpace(tvdb.NameEnglish)
	if title == "" {
		title = strings.TrimSpace(evidence.CanonicalName)
	}
	if title == "" {
		title = strings.TrimSpace(meta.Release.Title)
	}
	original := trimBHDAKAPrefix(tvdb.Name)
	if original == "" {
		original = trimBHDAKAPrefix(meta.Release.Alt)
	}
	tailStart := findBHDTVTailStart(name, meta)
	if title == "" || tailStart < 0 {
		return name
	}
	prefix := bhdTitlePrefix(title, original)
	if evidence.IncludeYear && evidence.SeriesYear > 0 {
		prefix = strings.TrimSpace(prefix + " " + strconv.Itoa(evidence.SeriesYear))
	}
	return joinBHDName(prefix, name[tailStart:])
}

func findBHDTVTailStart(name string, meta api.UploadSubject) int {
	candidates := []string{
		strings.TrimSpace(meta.SeasonStr + meta.EpisodeStr),
		strings.TrimSpace(meta.SeasonStr),
		strings.TrimSpace(meta.DailyEpisodeDate),
		strings.TrimSpace(meta.Release.Resolution),
	}
	best := -1
	for _, candidate := range candidates {
		start, _, ok := findBHDNameElement(name, candidate)
		if ok && (best < 0 || start < best) {
			best = start
		}
	}
	return best
}

func bhdTitlePrefix(title, original string) string {
	title = strings.Join(strings.Fields(title), " ")
	original = strings.Join(strings.Fields(trimBHDAKAPrefix(original)), " ")
	if original == "" || strings.EqualFold(title, original) {
		return title
	}
	return strings.TrimSpace(title + " AKA " + original)
}

func trimBHDAKAPrefix(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > len("AKA ") && strings.EqualFold(value[:len("AKA ")], "AKA ") {
		return strings.TrimSpace(value[len("AKA "):])
	}
	return value
}

func isBHDTV(meta api.UploadSubject) bool {
	return strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), string(api.CanonicalCategoryTV)) ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Category), "TV")
}

func applyBHDAudioPolicy(name, value string) (string, string) {
	audio := parseBHDAudioName(value)
	if audio.codec == "" {
		return name, ""
	}
	for _, candidate := range audio.candidates() {
		start, end, ok := findBHDNameElement(name, candidate)
		if !ok {
			continue
		}
		return name[:start] + audio.formatted() + name[end:], audio.formatted()
	}
	return name, audio.formatted()
}

type bhdAudioName struct {
	codec    string
	channels string
	object   string
}

func parseBHDAudioName(value string) bhdAudioName {
	fields := strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), "DD+", "DDP"))
	if len(fields) == 0 {
		return bhdAudioName{}
	}
	result := bhdAudioName{}
	codec := make([]string, 0, len(fields))
	for _, field := range fields {
		switch {
		case strings.EqualFold(field, "Atmos"):
			result.object = "Atmos"
		case isBHDAudioChannel(field):
			result.channels = field
		default:
			matches := bhdAudioChannelPattern.FindStringSubmatch(field)
			if len(matches) == 3 && isBHDAudioChannel(matches[2]) {
				codec = append(codec, matches[1])
				result.channels = matches[2]
				continue
			}
			codec = append(codec, field)
		}
	}
	result.codec = strings.Join(codec, " ")
	return result
}

func (a bhdAudioName) formatted() string {
	if strings.EqualFold(a.codec, "DD") && a.channels != "" {
		return strings.TrimSpace(a.codec + a.channels + " " + a.object)
	}
	return strings.Join(strings.Fields(strings.Join([]string{a.codec, a.object, a.channels}, " ")), " ")
}

func (a bhdAudioName) candidates() []string {
	candidates := []string{
		strings.Join(strings.Fields(strings.Join([]string{a.codec, a.channels, a.object}, " ")), " "),
		strings.Join(strings.Fields(strings.Join([]string{a.codec, a.object, a.channels}, " ")), " "),
		a.formatted(),
	}
	if a.channels != "" {
		candidates = append(candidates,
			strings.TrimSpace(a.codec+a.channels+" "+a.object),
			strings.TrimSpace(a.codec+a.object+" "+a.channels),
		)
	}
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate != "" && !slices.Contains(result, candidate) {
			result = append(result, candidate)
		}
	}
	return result
}

func isBHDAudioChannel(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func applyBHDDVDVideoAudioOrder(name, videoCodec, audio string) string {
	videoCodec = strings.Join(strings.Fields(videoCodec), " ")
	if videoCodec == "" || audio == "" {
		return name
	}
	audioStart, _, audioFound := findBHDNameElement(name, audio)
	if !audioFound {
		return name
	}
	codecStart, codecEnd, codecFound := findBHDNameElement(name, videoCodec)
	if codecFound && codecStart < audioStart {
		return name
	}
	if codecFound {
		name = strings.TrimSpace(name[:codecStart] + " " + name[codecEnd:])
		audioStart, _, audioFound = findBHDNameElement(name, audio)
		if !audioFound {
			return name
		}
	}
	return strings.TrimSpace(name[:audioStart] + videoCodec + " " + name[audioStart:])
}

func applyBHDSDRMarker(name string, meta api.UploadSubject) string {
	if !isBHDSDRHEVC(meta) {
		return name
	}
	if _, _, ok := findBHDNameElement(name, "SDR"); ok {
		return name
	}
	start, _, ok := findBHDNameElement(name, strings.TrimSpace(meta.VideoCodec))
	if !ok {
		return name
	}
	return strings.TrimSpace(name[:start] + "SDR " + name[start:])
}

func isBHDSDRHEVC(meta api.UploadSubject) bool {
	if !strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") ||
		!isBHDFullDisc(meta) && !strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		return false
	}
	return meta.HDRFacts.Status == api.HDREvidenceComplete &&
		len(meta.HDRFacts.Formats) == 1 &&
		meta.HDRFacts.Formats[0] == api.HDRFormatSDR
}

func applyBHDGroupPolicy(name string, meta api.UploadSubject) string {
	group := bhdReleaseGroup(meta)
	if group != "" {
		name = bhdNoGroupSuffixPattern.ReplaceAllString(name, "")
		if !hasBHDGroupSuffix(name, group) {
			name = strings.TrimRight(name, ".-_ ") + "-" + group
		}
		return name
	}
	name = bhdNoGroupSuffixPattern.ReplaceAllString(name, "")
	if isBHDFullDisc(meta) {
		return strings.TrimRight(name, ".-_ ")
	}
	return strings.TrimRight(name, ".-_ ") + "-NOGROUP"
}

func bhdReleaseGroup(meta api.UploadSubject) string {
	for _, candidate := range []string{meta.Tag, meta.Release.Group, meta.ArrReleaseGroup} {
		group := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "-"))
		switch strings.ToLower(group) {
		case "", "nogrp", "nogroup", "notag", "unknown", "unk":
			continue
		default:
			return group
		}
	}
	return ""
}

func hasBHDGroupSuffix(name, group string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), "-"+strings.ToLower(strings.TrimSpace(group)))
}

func isBHDFullDisc(meta api.UploadSubject) bool {
	if strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV", "DVD", "HDDVD", "HD-DVD":
		return true
	default:
		return false
	}
}

func findBHDNameElement(value, element string) (int, int, bool) {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return 0, 0, false
	}
	lowerValue := strings.ToLower(value)
	lowerElement := strings.ToLower(element)
	offset := 0
	for {
		index := strings.Index(lowerValue[offset:], lowerElement)
		if index < 0 {
			return 0, 0, false
		}
		start := offset + index
		end := start + len(element)
		if (start == 0 || isBHDNameSeparator(value[start-1])) &&
			(end == len(value) || isBHDNameSeparator(value[end])) {
			return start, end, true
		}
		offset = start + 1
	}
}

func findBHDLastNameElement(value, element string) (int, int, bool) {
	lastStart := 0
	lastEnd := 0
	found := false
	offset := 0
	for offset < len(value) {
		start, end, ok := findBHDNameElement(value[offset:], element)
		if !ok {
			break
		}
		lastStart = offset + start
		lastEnd = offset + end
		found = true
		offset = lastStart + 1
	}
	return lastStart, lastEnd, found
}

func isBHDNameSeparator(value byte) bool {
	return value == ' ' || value == '.' || value == '_' || value == '-'
}

func joinBHDName(prefix, suffix string) string {
	if strings.HasPrefix(suffix, "-") {
		return strings.TrimSpace(prefix) + suffix
	}
	return strings.Join(strings.Fields(strings.TrimSpace(prefix)+" "+strings.TrimSpace(suffix)), " ")
}

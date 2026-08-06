// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/moistari/rls"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// GeneralPolicyID identifies the always-on structural comparison policy.
const GeneralPolicyID = "general/duplicate/v2"

// FactStatus records evidence completeness for one normalized dimension.
type FactStatus string

const (
	// FactComplete indicates the normalized value is fully evidenced.
	FactComplete FactStatus = "complete"
	// FactPartial indicates only partial evidence supports the normalized value.
	FactPartial FactStatus = "partial"
	// FactMissing indicates no usable evidence supports the normalized value.
	FactMissing FactStatus = "missing"
	// FactContradictory indicates available evidence conflicts.
	FactContradictory FactStatus = "contradictory"
)

// FactOrigin identifies the strongest source used for one normalized fact.
type FactOrigin string

const (
	// FactOriginTargetMedia identifies prepared target media evidence.
	FactOriginTargetMedia FactOrigin = "target_media"
	// FactOriginTrackerAPI identifies tracker API evidence.
	FactOriginTrackerAPI FactOrigin = "tracker_api"
	// FactOriginTrackerTitle identifies tracker-title evidence.
	FactOriginTrackerTitle FactOrigin = "tracker_title"
	// FactOriginContentName identifies release/content-name evidence.
	FactOriginContentName FactOrigin = "content_name"
	// FactOriginTrackerPolicy identifies tracker-policy evidence.
	FactOriginTrackerPolicy FactOrigin = "tracker_policy"
	// FactOriginUnknown identifies an unclassified evidence source.
	FactOriginUnknown FactOrigin = "unknown"
)

// Fact preserves a normalized value and its provenance without raw protocol
// payloads or private URLs.
type Fact struct {
	Value          string
	Status         FactStatus
	Origin         FactOrigin
	SourceFields   []string
	Contradictions []string
}

// DimensionComparison is the four-state result for one fact dimension.
type DimensionComparison string

const (
	// DimensionEqual indicates both sides have the same known value.
	DimensionEqual DimensionComparison = "equal"
	// DimensionDifferent indicates both sides have different known values.
	DimensionDifferent DimensionComparison = "different"
	// DimensionUnknown indicates the comparison lacks sufficient evidence.
	DimensionUnknown DimensionComparison = "unknown"
	// DimensionNotApplicable indicates this dimension does not apply.
	DimensionNotApplicable DimensionComparison = "not_applicable"
)

type mediaKind string

const (
	mediaKindFullDisc    mediaKind = "full_disc"
	mediaKindRemux       mediaKind = "remux"
	mediaKindDiscEncode  mediaKind = "disc_encode"
	mediaKindWEBDL       mediaKind = "web_dl"
	mediaKindWEBRip      mediaKind = "web_rip"
	mediaKindHDTV        mediaKind = "hdtv"
	mediaKindSDTV        mediaKind = "sdtv"
	mediaKindDVDRip      mediaKind = "dvd_rip"
	mediaKindOtherEncode mediaKind = "other_encode"
	mediaKindUnknown     mediaKind = "unknown"
)

type mediaClass string

const (
	mediaClassFullDisc  mediaClass = "full_disc"
	mediaClassRemux     mediaClass = "remux"
	mediaClassEncode    mediaClass = "encode"
	mediaClassWEB       mediaClass = "web"
	mediaClassBroadcast mediaClass = "broadcast"
	mediaClassUnknown   mediaClass = "unknown"
)

type sourceFamily string

const (
	sourceFamilyDisc      sourceFamily = "disc"
	sourceFamilyWEB       sourceFamily = "web"
	sourceFamilyBroadcast sourceFamily = "broadcast"
	sourceFamilyOther     sourceFamily = "other"
	sourceFamilyUnknown   sourceFamily = "unknown"
)

type contentScopeKind string

const (
	contentScopeWork           contentScopeKind = "work"
	contentScopeDaily          contentScopeKind = "daily_date"
	contentScopeEpisode        contentScopeKind = "episode"
	contentScopeEpisodeRange   contentScopeKind = "episode_range"
	contentScopeSeasonPack     contentScopeKind = "season_pack"
	contentScopeCompleteSeries contentScopeKind = "complete_series"
	contentScopeUnknownTV      contentScopeKind = "unknown_tv"
)

type contentOverlap string

const (
	contentDefinitelyDisjoint contentOverlap = "disjoint"
	contentOverlaps           contentOverlap = "overlaps"
	contentEqual              contentOverlap = "equal"
	contentUnknown            contentOverlap = "unknown"
)

type contentScope struct {
	Kind         contentScopeKind
	Season       int
	EpisodeStart int
	EpisodeEnd   int
	Date         string
	Origin       FactOrigin
}

type normalizedFacts struct {
	Type         Fact
	Source       Fact
	Resolution   Fact
	Codec        Fact
	Container    Fact
	Provider     Fact
	Group        Fact
	Edition      Fact
	Region       Fact
	ThreeD       Fact
	Repack       Fact
	Size         Fact
	Files        Fact
	MediaKind    mediaKind
	MediaClass   mediaClass
	SourceFamily sourceFamily
	Content      contentScope
	HDR          api.HDRFacts
}

type parsedTitleFacts struct {
	Resolution string
	MediaKind  mediaKind
	Source     string
	Codec      string
	Container  string
	Provider   string
	Group      string
	Edition    string
	Region     string
	Repack     string
	ThreeD     string
	Content    contentScope
	HDR        api.HDRFacts
}

var (
	titleEpisodeRangePattern = regexp.MustCompile(`(?i)\bS(\d{1,3})E(\d{1,4})(?:-(?:S\d{1,3})?E?(\d{1,4}))?\b`)
	titleSeasonPackPattern   = regexp.MustCompile(`(?i)\bS(\d{1,3})(?:[.\-_ ]|$)`)
)

func normalizeTargetFacts(target api.TrackerDuplicateTarget) normalizedFacts {
	title := parseBestTitle(target.Names)
	facts := normalizedFacts{
		Type: mergeStructuredAndTitleFact(
			target.Type,
			"type",
			"",
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Source: mergeStructuredAndTitleFact(
			canonicalSource(target.Source),
			"source",
			title.Source,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Resolution: mergeStructuredAndTitleFact(
			canonicalResolution(target.Resolution),
			"resolution",
			title.Resolution,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Codec: mergeStructuredAndTitleFact(
			canonicalCodec(firstNonEmpty(target.VideoEncode, target.VideoCodec)),
			"videoCodec",
			title.Codec,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Container: mergeStructuredAndTitleFact(
			canonicalContainer(target.Container),
			"container",
			title.Container,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Provider: mergeStructuredAndTitleFact(
			canonicalProvider(target.Provider),
			"provider",
			title.Provider,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Group: mergeStructuredAndTitleFact(
			canonicalGroup(target.Group),
			"group",
			title.Group,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Edition: mergeStructuredAndTitleFact(
			canonicalEdition(target.Edition),
			"edition",
			title.Edition,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Region: mergeStructuredAndTitleFact(
			canonicalRegion(target.Region),
			"region",
			title.Region,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		ThreeD: mergeStructuredAndTitleFact(
			target.ThreeD,
			"threeD",
			title.ThreeD,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		Repack: mergeStructuredAndTitleFact(
			target.Repack,
			"repack",
			title.Repack,
			FactOriginTargetMedia,
			FactOriginContentName,
		),
		HDR: cloneHDRFacts(target.HDR),
		Content: contentScopeFromValues(
			target.Season,
			target.Episode,
			target.Date,
			target.Pack,
			title.Content,
			FactOriginTargetMedia,
		),
	}
	if target.SizeBytes > 0 {
		facts.Size = Fact{
			Value:        strconv.FormatInt(target.SizeBytes, 10),
			Status:       FactComplete,
			Origin:       FactOriginTargetMedia,
			SourceFields: []string{"sizeBytes"},
		}
	} else {
		facts.Size = missingFact()
	}
	if len(target.FileNames) > 0 {
		facts.Files = Fact{
			Value:        strconv.Itoa(len(target.FileNames)),
			Status:       FactComplete,
			Origin:       FactOriginTargetMedia,
			SourceFields: []string{"fileNames"},
		}
	} else {
		facts.Files = missingFact()
	}
	facts.MediaKind, facts.MediaClass, facts.SourceFamily = deriveMediaFacts(
		facts.Type.Value,
		facts.Source.Value,
		facts.Container.Value,
		target.VideoEncode,
		title.MediaKind,
	)
	facts.HDR = mergeHDRWithTitle(facts.HDR, title.HDR)
	return facts
}

func normalizeCandidateFacts(candidate TrackerCandidate) normalizedFacts {
	title := parseReleaseTitle(candidate.Name, FactOriginTrackerTitle)
	typeValue := candidate.Type
	typeSourceField := "type"
	if candidate.CanonicalType != "" {
		typeValue = candidate.CanonicalType
		typeSourceField = "canonicalType"
	}
	facts := normalizedFacts{
		Type: mergeStructuredAndTitleFact(
			typeValue,
			typeSourceField,
			"",
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Source: mergeStructuredAndTitleFact(
			canonicalSource(candidate.Source),
			"source",
			title.Source,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Resolution: mergeStructuredAndTitleFact(
			canonicalResolution(candidate.Resolution),
			"resolution",
			title.Resolution,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Codec: mergeStructuredAndTitleFact(
			canonicalCodec(candidate.Codec),
			"codec",
			title.Codec,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Container: mergeStructuredAndTitleFact(
			canonicalContainer(candidate.Container),
			"container",
			title.Container,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Provider: mergeStructuredAndTitleFact(
			canonicalProvider(candidate.Provider),
			"provider",
			title.Provider,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Group: mergeStructuredAndTitleFact(
			canonicalGroup(candidate.Group),
			"group",
			title.Group,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Edition: mergeStructuredAndTitleFact(
			canonicalEdition(candidate.Edition),
			"edition",
			title.Edition,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Region: mergeStructuredAndTitleFact(
			canonicalRegion(candidate.Region),
			"region",
			title.Region,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		ThreeD: mergeStructuredAndTitleFact(
			candidate.ThreeD,
			"threeD",
			title.ThreeD,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		Repack: mergeStructuredAndTitleFact(
			candidate.Repack,
			"repack",
			title.Repack,
			FactOriginTrackerAPI,
			FactOriginTrackerTitle,
		),
		HDR: cloneHDRFacts(candidate.HDR),
		Content: contentScopeFromValues(
			candidate.Season,
			candidate.Episode,
			candidate.Date,
			candidate.Pack,
			title.Content,
			FactOriginTrackerAPI,
		),
	}
	if candidate.SizeKnown && candidate.SizeBytes > 0 {
		facts.Size = Fact{
			Value:        strconv.FormatInt(candidate.SizeBytes, 10),
			Status:       FactComplete,
			Origin:       FactOriginTrackerAPI,
			SourceFields: []string{"size"},
		}
	} else {
		facts.Size = missingFact()
	}
	switch {
	case len(candidate.Files) > 0 && (candidate.FileCount == 0 || candidate.FileCount == len(candidate.Files)):
		facts.Files = Fact{
			Value:        strconv.Itoa(len(candidate.Files)),
			Status:       FactComplete,
			Origin:       FactOriginTrackerAPI,
			SourceFields: []string{"files"},
		}
	case len(candidate.Files) > 0:
		facts.Files = Fact{
			Value:        strconv.Itoa(len(candidate.Files)),
			Status:       FactPartial,
			Origin:       FactOriginTrackerAPI,
			SourceFields: []string{"files"},
		}
	default:
		facts.Files = missingFact()
	}
	facts.MediaKind, facts.MediaClass, facts.SourceFamily = deriveMediaFacts(
		facts.Type.Value,
		facts.Source.Value,
		facts.Container.Value,
		"",
		title.MediaKind,
	)
	facts.HDR = mergeHDRWithTitle(facts.HDR, title.HDR)
	return facts
}

func parseBestTitle(names []string) parsedTitleFacts {
	for _, name := range names {
		if parsed := parseReleaseTitle(name, FactOriginContentName); parsed.hasEvidence() ||
			parsed.Content.Kind != contentScopeWork || parsed.Repack != "" || parsed.ThreeD != "" ||
			parsed.HDR.Status != api.HDREvidenceMissing {
			return parsed
		}
	}
	return parsedTitleFacts{MediaKind: mediaKindUnknown, Content: contentScope{Kind: contentScopeWork}}
}

func parseReleaseTitle(name string, origin FactOrigin) parsedTitleFacts {
	name = strings.TrimSpace(name)
	upper := strings.ToUpper(name)
	release := rls.ParseString(name)
	mediaKind := mediaKindFromText(upper)
	if mediaKind == mediaKindUnknown && hasBareWEBTitleMarker(upper) {
		mediaKind = mediaKindWEBDL
	}
	parsed := parsedTitleFacts{
		Resolution: canonicalResolution(candidateResolutionPattern.FindString(name)),
		MediaKind:  mediaKind,
		Source:     canonicalSource(release.Source),
		Codec:      canonicalCodec(firstNonEmpty(release.Codec...)),
		Container:  canonicalContainer(firstNonEmpty(release.Container, release.Ext)),
		Provider:   canonicalProvider(release.Collection),
		Group:      canonicalGroup(release.Group),
		Edition:    canonicalTitleEdition(release.Cut, release.Edition, name),
		Region:     canonicalRegion(release.Region),
		Content:    contentScope{Kind: contentScopeWork, Origin: origin},
		HDR:        hdrFactsFromCandidateTitle(name),
	}
	if parsed.Source == "" {
		parsed.Source = sourceFromTitle(upper)
	}
	if parsed.Codec == "" {
		parsed.Codec = codecFromTitle(upper)
	}
	if parsed.Container == "" {
		parsed.Container = containerFromTitle(upper)
	}
	if parsed.Group == "" {
		parsed.Group = groupFromTitle(name)
	}
	if parsed.Region == "" {
		parsed.Region = regionFromTitle(upper)
	}
	if tokenPresent(upper, "REPACK") || tokenPresent(upper, "PROPER") || tokenPresent(upper, "RERIP") ||
		tokenPresent(upper, "REISSUE") {
		for _, value := range []string{"REPACK", "PROPER", "RERIP", "REISSUE"} {
			if tokenPresent(upper, value) {
				parsed.Repack = strings.ToLower(value)
				break
			}
		}
	}
	if tokenPresent(upper, "3D") {
		parsed.ThreeD = "3d"
	}
	if match := candidateDailyDatePattern.FindStringSubmatch(name); len(match) == 4 {
		parsed.Content = contentScope{
			Kind:   contentScopeDaily,
			Date:   strings.Join(match[1:], "-"),
			Origin: origin,
		}
		return parsed
	}
	if strings.Contains(upper, "COMPLETE") && strings.Contains(upper, "SERIES") {
		parsed.Content = contentScope{Kind: contentScopeCompleteSeries, Origin: origin}
		return parsed
	}
	if match := titleEpisodeRangePattern.FindStringSubmatch(name); len(match) >= 3 {
		season, _ := strconv.Atoi(match[1])
		start, _ := strconv.Atoi(match[2])
		end := start
		kind := contentScopeEpisode
		if len(match) > 3 && match[3] != "" {
			end, _ = strconv.Atoi(match[3])
			kind = contentScopeEpisodeRange
		}
		parsed.Content = contentScope{
			Kind:         kind,
			Season:       season,
			EpisodeStart: start,
			EpisodeEnd:   end,
			Origin:       origin,
		}
		return parsed
	}
	if match := titleSeasonPackPattern.FindStringSubmatch(name); len(match) == 2 {
		season, _ := strconv.Atoi(match[1])
		parsed.Content = contentScope{
			Kind:   contentScopeSeasonPack,
			Season: season,
			Origin: origin,
		}
	}
	return parsed
}

func (facts parsedTitleFacts) hasEvidence() bool {
	return facts.Resolution != "" || facts.MediaKind != mediaKindUnknown || facts.Source != "" || facts.Codec != "" ||
		facts.Container != "" || facts.Provider != "" || facts.Group != "" || facts.Edition != "" || facts.Region != ""
}

func hasBareWEBTitleMarker(value string) bool {
	tokens := strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == ' ' || r == '_' || r == '-' || r == '[' || r == ']'
	})
	resolutionSeen := false
	for index, token := range tokens {
		switch {
		case candidateResolutionPattern.MatchString(token):
			resolutionSeen = true
		case resolutionSeen && token == "WEB" && webCodecTokenFollows(tokens, index+1):
			return true
		}
	}
	return false
}

func webCodecTokenFollows(tokens []string, start int) bool {
	if start >= len(tokens) {
		return false
	}
	switch tokens[start] {
	case "H264", "H265", "X264", "X265", "AVC", "HEVC", "AV1":
		return true
	case "H":
		return start+1 < len(tokens) && (tokens[start+1] == "264" || tokens[start+1] == "265")
	default:
		return false
	}
}

func mediaKindFromText(value string) mediaKind {
	normalized := strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(strings.ToUpper(value))
	compact := compactAlphaNumeric(value)
	switch {
	case strings.Contains(normalized, "REMUX"):
		return mediaKindRemux
	case strings.Contains(normalized, "WEB DL"), strings.Contains(normalized, "WEBDL"):
		return mediaKindWEBDL
	case strings.Contains(normalized, "WEB RIP"), strings.Contains(normalized, "WEBRIP"):
		return mediaKindWEBRip
	case strings.Contains(normalized, "HDTV"):
		return mediaKindHDTV
	case strings.Contains(normalized, "SDTV"):
		return mediaKindSDTV
	case strings.Contains(normalized, "DVD RIP"), strings.Contains(normalized, "DVDRIP"):
		return mediaKindDVDRip
	case strings.Contains(compact, "bd25"), strings.Contains(compact, "bd50"), strings.Contains(compact, "bd66"),
		strings.Contains(compact, "bd100"), strings.Contains(normalized, "FULL DISC"), strings.Contains(normalized, "BLU RAY DISC"),
		strings.Contains(normalized, "UHD DISC"), strings.Contains(normalized, "BLURAY RAW"), strings.Contains(compact, "dvd5"),
		strings.Contains(compact, "dvd9"):
		return mediaKindFullDisc
	case strings.Contains(normalized, "BD RIP"), strings.Contains(normalized, "BDRIP"),
		(strings.Contains(normalized, "BLU RAY") || strings.Contains(normalized, "BLURAY")) &&
			(strings.Contains(compact, "x264") || strings.Contains(compact, "x265") || strings.Contains(compact, "xvid") ||
				strings.Contains(compact, "divx")):
		return mediaKindDiscEncode
	default:
		return mediaKindUnknown
	}
}

// deriveMediaFacts classifies media from structured type and container evidence before falling back to title and source evidence.
func deriveMediaFacts(
	typeValue string,
	sourceValue string,
	containerValue string,
	encodeValue string,
	titleKind mediaKind,
) (mediaKind, mediaClass, sourceFamily) {
	kind := mediaKindFromText(strings.Join([]string{typeValue, encodeValue}, " "))
	if strings.Contains(strings.ToUpper(typeValue), "DISC") {
		kind = mediaKindFullDisc
	}
	if kind == mediaKindUnknown {
		switch canonicalContainer(containerValue) {
		case "m2ts", "vob_ifo":
			kind = mediaKindFullDisc
		case "iso":
			if family := canonicalSource(sourceValue); family == "bluray" || family == "uhd_bluray" || family == "hd_dvd" || family == "dvd" {
				kind = mediaKindFullDisc
			}
		}
	}
	if kind == mediaKindUnknown {
		kind = titleKind
	}
	if kind == mediaKindUnknown {
		kind = mediaKindFromText(sourceValue)
	}
	if kind == mediaKindUnknown {
		switch {
		case strings.Contains(strings.ToUpper(typeValue), "DISC"):
			kind = mediaKindFullDisc
		case strings.Contains(strings.ToUpper(sourceValue), "BLURAY"), strings.Contains(strings.ToUpper(sourceValue), "BLU-RAY"),
			strings.Contains(strings.ToUpper(sourceValue), "UHD"), strings.Contains(strings.ToUpper(sourceValue), "DVD"):
			if strings.TrimSpace(encodeValue) != "" || strings.Contains(strings.ToUpper(typeValue), "ENCODE") {
				kind = mediaKindDiscEncode
			}
		}
	}
	if kind == mediaKindUnknown &&
		(strings.Contains(strings.ToUpper(typeValue), "ENCODE") || strings.TrimSpace(encodeValue) != "") {
		kind = mediaKindOtherEncode
	}
	switch kind {
	case mediaKindFullDisc:
		return kind, mediaClassFullDisc, sourceFamilyDisc
	case mediaKindRemux:
		return kind, mediaClassRemux, sourceFamilyDisc
	case mediaKindDiscEncode, mediaKindDVDRip, mediaKindOtherEncode:
		family := sourceFamilyOther
		if kind == mediaKindDiscEncode || kind == mediaKindDVDRip {
			family = sourceFamilyDisc
		}
		return kind, mediaClassEncode, family
	case mediaKindWEBDL:
		return kind, mediaClassWEB, sourceFamilyWEB
	case mediaKindWEBRip:
		return kind, mediaClassEncode, sourceFamilyWEB
	case mediaKindHDTV, mediaKindSDTV:
		return kind, mediaClassBroadcast, sourceFamilyBroadcast
	case mediaKindUnknown:
		return mediaKindUnknown, mediaClassUnknown, sourceFamilyUnknown
	}
	return mediaKindUnknown, mediaClassUnknown, sourceFamilyUnknown
}

func canonicalSource(value string) string {
	normalized := compactAlphaNumeric(value)
	switch {
	case strings.Contains(normalized, "uhdbluray"), strings.Contains(normalized, "uhdbd"):
		return "uhd_bluray"
	case strings.Contains(normalized, "bluray"), strings.Contains(normalized, "bdrip"):
		return "bluray"
	case strings.Contains(normalized, "hddvd"):
		return "hd_dvd"
	case strings.Contains(normalized, "web"):
		return "web"
	case strings.Contains(normalized, "hdtv"):
		return "hdtv"
	case strings.Contains(normalized, "sdtv"):
		return "sdtv"
	case strings.Contains(normalized, "dvd"):
		return "dvd"
	case normalized == "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func canonicalResolution(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "uhd", "4k":
		return "2160p"
	case "fhd":
		return "1080p"
	case "hd":
		return "720p"
	case "sd":
		return "sd"
	default:
		return normalized
	}
}

func canonicalCodec(value string) string {
	normalized := compactAlphaNumeric(value)
	switch normalized {
	case "x264", "h264", "avc", "avc1":
		return "h264"
	case "x265", "h265", "hevc", "hevc1":
		return "h265"
	case "xvid", "divx":
		return "xvid"
	case "av1", "vp9", "mpeg2", "mpeg4":
		return normalized
	case "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func canonicalContainer(value string) string {
	normalized := compactAlphaNumeric(value)
	switch normalized {
	case "matroska", "mkv":
		return "mkv"
	case "mpeg4", "mp4":
		return "mp4"
	case "vobifo", "videots":
		return "vob_ifo"
	case "bluray", "bdmv", "m2ts":
		return "m2ts"
	case "transportstream", "ts":
		return "ts"
	case "iso", "avi":
		return normalized
	case "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func canonicalProvider(value string) string {
	return compactAlphaNumeric(value)
}

func canonicalGroup(value string) string {
	return strings.TrimLeft(strings.TrimSpace(value), "-")
}

func canonicalEdition(value string) string {
	if canonical := canonicalTitleEdition(nil, []string{value}, value); canonical != "" {
		return canonical
	}
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func canonicalTitleEdition(cuts []string, editions []string, title string) string {
	joined := strings.Join(append(append([]string(nil), cuts...), editions...), " ") + " " + title
	normalized := strings.NewReplacer(".", " ", "_", " ", "-", " ", "'", "").Replace(strings.ToLower(joined))
	normalized = strings.Join(strings.Fields(normalized), " ")
	searchable := " " + normalized + " "
	values := make([]string, 0, 4)
	for _, marker := range []struct {
		phrase string
		value  string
	}{
		{phrase: "directors cut", value: "directors_cut"},
		{phrase: "director s cut", value: "directors_cut"},
		{phrase: "extended cut", value: "extended"},
		{phrase: "extended edition", value: "extended"},
		{phrase: "theatrical cut", value: "theatrical"},
		{phrase: "theatrical edition", value: "theatrical"},
		{phrase: "final cut", value: "final_cut"},
		{phrase: "international cut", value: "international_cut"},
		{phrase: "alternate cut", value: "alternate_cut"},
		{phrase: "open matte", value: "open_matte"},
		{phrase: "oar", value: "oar"},
		{phrase: "uncut", value: "uncut"},
		{phrase: "unrated", value: "unrated"},
	} {
		if strings.Contains(searchable, " "+marker.phrase+" ") && !slices.Contains(values, marker.value) {
			values = append(values, marker.value)
		}
	}
	slices.Sort(values)
	return strings.Join(values, "+")
}

func canonicalRegion(value string) string {
	normalized := compactAlphaNumeric(value)
	switch {
	case strings.Contains(normalized, "pal"):
		return "pal"
	case strings.Contains(normalized, "ntsc"):
		return "ntsc"
	case normalized == "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func sourceFromTitle(upper string) string {
	for _, marker := range []struct {
		tokens []string
		value  string
	}{
		{tokens: []string{"UHD BLURAY", "UHD BLU-RAY", "UHD.BLU-RAY", "UHDBD"}, value: "uhd_bluray"},
		{tokens: []string{"BLURAY", "BLU-RAY", "BLU.RAY", "BDRIP"}, value: "bluray"},
		{tokens: []string{"HD-DVD", "HDDVD"}, value: "hd_dvd"},
		{tokens: []string{"WEB-DL", "WEBDL", "WEBRIP", "WEB-RIP", "WEB.RIP"}, value: "web"},
		{tokens: []string{"HDTV"}, value: "hdtv"},
		{tokens: []string{"SDTV"}, value: "sdtv"},
		{tokens: []string{"DVD", "DVDRIP"}, value: "dvd"},
	} {
		for _, token := range marker.tokens {
			if tokenPresent(upper, token) || strings.Contains(upper, token) {
				return marker.value
			}
		}
	}
	return ""
}

func codecFromTitle(upper string) string {
	for _, token := range []string{"X265", "H265", "H.265", "HEVC", "X264", "H264", "H.264", "AVC", "XVID", "AV1", "VP9", "MPEG2"} {
		if tokenPresent(upper, token) || strings.Contains(upper, token) {
			return canonicalCodec(token)
		}
	}
	return ""
}

func containerFromTitle(upper string) string {
	for _, token := range []string{"MKV", "MP4", "M2TS", "VOB_IFO", "VIDEO_TS", "ISO", "AVI"} {
		if tokenPresent(upper, token) || strings.HasSuffix(upper, "."+token) {
			return canonicalContainer(token)
		}
	}
	return ""
}

func groupFromTitle(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".mkv")
	name = strings.TrimSuffix(name, ".mp4")
	separator := strings.LastIndex(name, "-")
	if separator < 0 || separator == len(name)-1 {
		return ""
	}
	group := name[separator+1:]
	if strings.ContainsAny(group, ". _[]()") {
		return ""
	}
	return canonicalGroup(group)
}

func regionFromTitle(upper string) string {
	for _, value := range []string{"PAL", "NTSC"} {
		if tokenPresent(upper, value) {
			return strings.ToLower(value)
		}
	}
	return ""
}

func compactAlphaNumeric(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
}

func contentScopeFromValues(
	season int,
	episode int,
	date string,
	pack bool,
	fallback contentScope,
	origin FactOrigin,
) contentScope {
	if date = strings.TrimSpace(date); date != "" {
		return contentScope{
			Kind:   contentScopeDaily,
			Date:   date,
			Origin: origin,
		}
	}
	switch {
	case pack && season > 0:
		return contentScope{
			Kind:   contentScopeSeasonPack,
			Season: season,
			Origin: origin,
		}
	case pack && (fallback.Kind == contentScopeSeasonPack || fallback.Kind == contentScopeCompleteSeries):
		return fallback
	case pack:
		return contentScope{Kind: contentScopeUnknownTV, Origin: origin}
	case episode > 0:
		return contentScope{
			Kind:         contentScopeEpisode,
			Season:       season,
			EpisodeStart: episode,
			EpisodeEnd:   episode,
			Origin:       origin,
		}
	case season > 0:
		return contentScope{
			Kind:   contentScopeUnknownTV,
			Season: season,
			Origin: origin,
		}
	case fallback.Kind != "":
		return fallback
	default:
		return contentScope{Kind: contentScopeWork, Origin: origin}
	}
}

func compareContentScopes(target contentScope, candidate contentScope) contentOverlap {
	if target.Kind == contentScopeDaily || candidate.Kind == contentScopeDaily {
		if target.Kind != contentScopeDaily || candidate.Kind != contentScopeDaily || target.Date == "" || candidate.Date == "" {
			return contentUnknown
		}
		if strings.EqualFold(target.Date, candidate.Date) {
			return contentEqual
		}
		return contentDefinitelyDisjoint
	}
	if target.Kind == contentScopeWork && candidate.Kind == contentScopeWork {
		return contentEqual
	}
	if target.Kind == contentScopeCompleteSeries || candidate.Kind == contentScopeCompleteSeries {
		return contentOverlaps
	}
	if contentSeasonKnown(target) && contentSeasonKnown(candidate) && target.Season != candidate.Season {
		return contentDefinitelyDisjoint
	}
	if target.Kind == contentScopeSeasonPack || candidate.Kind == contentScopeSeasonPack {
		if target.Season > 0 && candidate.Season > 0 {
			return contentOverlaps
		}
		return contentUnknown
	}
	if target.EpisodeStart > 0 && candidate.EpisodeStart > 0 {
		if target.EpisodeEnd < candidate.EpisodeStart || candidate.EpisodeEnd < target.EpisodeStart {
			return contentDefinitelyDisjoint
		}
		if target.EpisodeStart == candidate.EpisodeStart && target.EpisodeEnd == candidate.EpisodeEnd {
			return contentEqual
		}
		return contentOverlaps
	}
	if target.Kind == contentScopeUnknownTV || candidate.Kind == contentScopeUnknownTV {
		return contentUnknown
	}
	return contentUnknown
}

func contentSeasonKnown(scope contentScope) bool {
	switch scope.Kind {
	case contentScopeEpisode, contentScopeEpisodeRange, contentScopeSeasonPack:
		return true
	case contentScopeUnknownTV:
		return scope.Season > 0
	case contentScopeWork, contentScopeDaily, contentScopeCompleteSeries:
		return false
	}
	return false
}

func mergeStructuredAndTitleFact(
	structured string,
	sourceField string,
	title string,
	structuredOrigin FactOrigin,
	titleOrigin FactOrigin,
) Fact {
	structured = strings.TrimSpace(structured)
	title = strings.TrimSpace(title)
	switch {
	case structured == "" && title == "":
		return missingFact()
	case structured == "":
		return Fact{
			Value:        title,
			Status:       FactPartial,
			Origin:       titleOrigin,
			SourceFields: []string{"title"},
		}
	case title == "", strings.EqualFold(structured, title):
		return Fact{
			Value:        structured,
			Status:       FactComplete,
			Origin:       structuredOrigin,
			SourceFields: []string{sourceField},
		}
	default:
		return Fact{
			Value:          structured,
			Status:         FactContradictory,
			Origin:         structuredOrigin,
			SourceFields:   []string{sourceField, "title"},
			Contradictions: []string{title},
		}
	}
}

func completeFact(value string, origin FactOrigin, sourceField string) Fact {
	value = strings.TrimSpace(value)
	if value == "" {
		return missingFact()
	}
	return Fact{
		Value:        value,
		Status:       FactComplete,
		Origin:       origin,
		SourceFields: []string{sourceField},
	}
}

func missingFact() Fact {
	return Fact{Status: FactMissing, Origin: FactOriginUnknown}
}

func compareFacts(target Fact, candidate Fact) DimensionComparison {
	if target.Status == FactMissing && candidate.Status == FactMissing {
		return DimensionNotApplicable
	}
	if target.Status == FactMissing || candidate.Status == FactMissing ||
		target.Status == FactContradictory || candidate.Status == FactContradictory {
		return DimensionUnknown
	}
	if strings.EqualFold(strings.TrimSpace(target.Value), strings.TrimSpace(candidate.Value)) {
		return DimensionEqual
	}
	return DimensionDifferent
}

func mergeHDRWithTitle(structured api.HDRFacts, title api.HDRFacts) api.HDRFacts {
	if structured.Status == "" {
		structured.Status = api.HDREvidenceMissing
		structured.Origin = api.HDREvidenceUnknown
	}
	if title.Status == api.HDREvidenceMissing {
		return structured
	}
	if structured.Status == api.HDREvidenceMissing {
		return title
	}
	if !sameCandidateHDR(structured.Formats, title.Formats) && len(title.Formats) > 0 {
		structured.Status = api.HDREvidenceContradictory
		structured.Contradictions = append(structured.Contradictions, "explicit title HDR differs from structured HDR")
		if !containsFold(structured.SourceFields, "title") {
			structured.SourceFields = append(structured.SourceFields, "title")
		}
	}
	return structured
}

func dimensionFact(facts normalizedFacts, dimension trackerspkg.DupeDimension) Fact {
	switch dimension {
	case trackerspkg.DupeDimensionType:
		return facts.Type
	case trackerspkg.DupeDimensionSource:
		return facts.Source
	case trackerspkg.DupeDimensionMediaKind:
		return canonicalFact(string(facts.MediaKind))
	case trackerspkg.DupeDimensionMediaClass:
		return canonicalFact(string(facts.MediaClass))
	case trackerspkg.DupeDimensionSourceFamily:
		return canonicalFact(string(facts.SourceFamily))
	case trackerspkg.DupeDimensionResolution:
		return facts.Resolution
	case trackerspkg.DupeDimensionCodec:
		return facts.Codec
	case trackerspkg.DupeDimensionContainer:
		return facts.Container
	case trackerspkg.DupeDimensionEdition:
		return facts.Edition
	case trackerspkg.DupeDimensionRegion:
		return facts.Region
	case trackerspkg.DupeDimensionThreeD:
		return facts.ThreeD
	case trackerspkg.DupeDimensionProvider:
		return facts.Provider
	case trackerspkg.DupeDimensionGroup:
		return facts.Group
	case trackerspkg.DupeDimensionRepack:
		return facts.Repack
	case trackerspkg.DupeDimensionSize:
		return facts.Size
	case trackerspkg.DupeDimensionHDR,
		trackerspkg.DupeDimensionPack,
		trackerspkg.DupeDimensionSeason,
		trackerspkg.DupeDimensionEpisode,
		trackerspkg.DupeDimensionDate:
		return missingFact()
	}
	return missingFact()
}

func canonicalFact(value string) Fact {
	if value == "" || value == string(mediaKindUnknown) || value == string(mediaClassUnknown) || value == string(sourceFamilyUnknown) {
		return missingFact()
	}
	return Fact{
		Value:  value,
		Status: FactComplete,
		Origin: FactOriginTrackerPolicy,
	}
}

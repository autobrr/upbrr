// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	candidateResolutionPattern = regexp.MustCompile(`(?i)\b(8640p|4320p|2160p|1080[pi]|720p|576[pi]|540p|480[pi])\b`)
	candidateEpisodePattern    = regexp.MustCompile(`(?i)\bS(\d{1,3})(?:E(\d{1,4}))?\b`)
	candidateDailyDatePattern  = regexp.MustCompile(`\b(20\d{2})[.\-_ ](\d{2})[.\-_ ](\d{2})\b`)
)

// TrackerCandidate is protocol-independent private candidate evidence.
type TrackerCandidate struct {
	ID              string
	Name            string
	Files           []string
	FileCount       int
	SizeBytes       int64
	SizeKnown       bool
	Category        string
	Type            string
	CanonicalType   string
	Source          string
	Resolution      string
	Codec           string
	Container       string
	Provider        string
	Group           string
	ReleaseOrigin   string
	Edition         string
	Region          string
	ThreeD          string
	Repack          string
	Season          int
	Episode         int
	Date            string
	Pack            bool
	Internal        bool
	Trumpable       bool
	Flags           []string
	HDR             api.HDRFacts
	DetailsLink     string
	privateDownload string
}

// NormalizeCandidate converts one adapter entry without exposing private
// download evidence to public evaluation projections.
func NormalizeCandidate(entry api.DupeEntry, _ string) TrackerCandidate {
	candidate := TrackerCandidate{
		ID:              strings.TrimSpace(entry.ID),
		Name:            strings.TrimSpace(entry.Name),
		Files:           append([]string(nil), entry.Files...),
		FileCount:       entry.FileCount,
		SizeBytes:       entry.SizeBytes,
		SizeKnown:       entry.SizeKnown,
		Category:        strings.TrimSpace(entry.Category),
		Type:            strings.TrimSpace(entry.Type),
		CanonicalType:   strings.TrimSpace(entry.CanonicalType),
		Source:          strings.TrimSpace(entry.Source),
		Resolution:      strings.TrimSpace(entry.Res),
		Codec:           strings.TrimSpace(entry.Codec),
		Container:       strings.TrimSpace(entry.Container),
		Provider:        strings.TrimSpace(entry.Provider),
		Group:           strings.TrimSpace(entry.Group),
		ReleaseOrigin:   strings.TrimSpace(entry.ReleaseOrigin),
		Edition:         strings.TrimSpace(entry.Edition),
		Region:          strings.TrimSpace(entry.Region),
		ThreeD:          strings.TrimSpace(entry.ThreeD),
		Repack:          strings.TrimSpace(entry.Repack),
		Season:          entry.Season,
		Episode:         entry.Episode,
		Pack:            entry.Pack,
		Internal:        entry.Internal,
		Trumpable:       entry.Trumpable,
		Flags:           append([]string(nil), entry.Flags...),
		HDR:             cloneHDRFacts(entry.HDR),
		DetailsLink:     strings.TrimSpace(entry.Link),
		privateDownload: strings.TrimSpace(entry.Download),
	}
	if candidate.FileCount == 0 {
		candidate.FileCount = len(candidate.Files)
	}
	if candidate.HDR.Status == "" {
		candidate.HDR = NormalizeTrackerHDRFlags(entry.Flags, entry.FlagsPresent, entry.FlagsComplete)
	}
	enrichCandidateCoordinatesFromTitle(&candidate)
	if candidate.HDR.Status == api.HDREvidenceMissing {
		if titleHDR := hdrFactsFromCandidateTitle(candidate.Name); titleHDR.Status != api.HDREvidenceMissing {
			candidate.HDR = titleHDR
		}
	}
	return candidate
}

// NormalizeTrackerHDRFlags maps fixture-backed tracker flags while preserving
// partial and missing evidence semantics.
func NormalizeTrackerHDRFlags(flags []string, present bool, complete bool) api.HDRFacts {
	facts := api.HDRFacts{
		Origin: api.HDREvidenceTrackerAPI,
		Status: api.HDREvidencePartial,
	}
	if present {
		facts.SourceFields = []string{"flags"}
	}
	for _, flag := range flags {
		normalized := strings.ToUpper(strings.TrimSpace(flag))
		switch normalized {
		case "DV", "DOVI", "DOLBY VISION":
			mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatDolbyVision)
		case "HDR10+":
			mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatHDR10Plus)
		case "HDR", "HDR10":
			mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatHDR10)
		case "HLG":
			mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatHLG)
		case "PQ10":
			mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatPQ10)
		case "HDR VIVID", "HDRVIVID":
			mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatHDRVivid)
		}
	}
	switch {
	case complete && present && len(flags) == 0:
		facts.Formats = []api.HDRFormat{api.HDRFormatSDR}
		facts.Status = api.HDREvidenceComplete
	case complete && len(facts.Formats) > 0:
		facts.Status = api.HDREvidenceComplete
	case len(facts.Formats) == 0:
		facts.Origin = api.HDREvidenceUnknown
		facts.Status = api.HDREvidenceMissing
	}
	mediafacts.AddHDRFallbacks(&facts)
	return facts
}

func enrichCandidateCoordinatesFromTitle(candidate *TrackerCandidate) {
	if candidate == nil {
		return
	}
	if candidate.Season == 0 || (!candidate.Pack && candidate.Episode == 0) {
		if match := candidateEpisodePattern.FindStringSubmatch(candidate.Name); len(match) >= 2 {
			if candidate.Season == 0 {
				candidate.Season, _ = strconv.Atoi(match[1])
			}
			if len(match) == 3 && match[2] != "" && candidate.Episode == 0 {
				candidate.Episode, _ = strconv.Atoi(match[2])
			} else if len(match) < 3 || match[2] == "" {
				candidate.Pack = true
			}
		}
	}
	if candidate.Date == "" {
		if match := candidateDailyDatePattern.FindStringSubmatch(candidate.Name); len(match) == 4 {
			candidate.Date = strings.Join(match[1:], "-")
		}
	}
}

func hdrFactsFromCandidateTitle(name string) api.HDRFacts {
	upper := strings.ToUpper(name)
	facts := api.HDRFacts{
		Origin:       api.HDREvidenceTrackerTitle,
		Status:       api.HDREvidencePartial,
		SourceFields: []string{"title"},
	}
	if strings.Contains(upper, "DOLBY.VISION") || strings.Contains(upper, "DOLBY VISION") || strings.Contains(upper, "DOVI") ||
		tokenPresent(upper, "DV") || tokenPresent(upper, "DV5") {
		mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatDolbyVision)
		if strings.Contains(upper, "DOLBY.VISION.PROFILE.5") || strings.Contains(upper, "DOLBY VISION PROFILE 5") ||
			strings.Contains(upper, "DOVI.P5") || tokenPresent(upper, "DV5") {
			facts.DolbyVisionProfile = "5"
		}
	}
	switch {
	case strings.Contains(upper, "HDR10+"), strings.Contains(upper, "HDR10PLUS"):
		mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatHDR10Plus)
	case tokenPresent(upper, "HDR10"), tokenPresent(upper, "HDR"):
		mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatHDR10)
	}
	if tokenPresent(upper, "HLG") {
		mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatHLG)
	}
	if tokenPresent(upper, "PQ10") {
		mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatPQ10)
	}
	if len(facts.Formats) == 0 && tokenPresent(upper, "SDR") {
		mediafacts.AddHDRFormat(&facts.Formats, api.HDRFormatSDR)
	}
	if len(facts.Formats) == 0 {
		facts.Origin = api.HDREvidenceUnknown
		facts.Status = api.HDREvidenceMissing
		return facts
	}
	mediafacts.AddHDRFallbacks(&facts)
	return facts
}

// NormalizeTrackerTitleHDR maps explicit tracker-title markers to partial HDR
// evidence without inferring SDR from marker absence.
func NormalizeTrackerTitleHDR(name string) api.HDRFacts {
	return hdrFactsFromCandidateTitle(name)
}

func tokenPresent(value string, token string) bool {
	return slices.Contains(strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == ' ' || r == '_' || r == '-' || r == '[' || r == ']'
	}), token)
}

func cloneHDRFacts(facts api.HDRFacts) api.HDRFacts {
	facts.Formats = append([]api.HDRFormat(nil), facts.Formats...)
	facts.FallbackFormats = append([]api.HDRFormat(nil), facts.FallbackFormats...)
	facts.SourceFields = append([]string(nil), facts.SourceFields...)
	facts.Contradictions = append([]string(nil), facts.Contradictions...)
	return facts
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "slices"

// FactProvenance identifies how a finalized field was selected.
type FactProvenance string

const (
	FactProvenanceAutomatic   FactProvenance = "automatic"
	FactProvenanceManual      FactProvenance = "manual"
	FactProvenanceManualEmpty FactProvenance = "manual_empty"
)

// IsManual reports whether even an empty resolved value has explicit authority.
func (p FactProvenance) IsManual() bool {
	return p == FactProvenanceManual || p == FactProvenanceManualEmpty
}

// EffectiveMetadata contains detached resolved descriptive facts. Provider
// snapshots remain evidence; preference methods preserve manual authority.
type EffectiveMetadata struct {
	Title                      string
	AlternateTitle             string
	OriginalTitle              string
	Year                       int
	Genres                     []string
	OriginalLanguage           string
	Distributor                string
	TitleProvenance            FactProvenance
	AlternateTitleProvenance   FactProvenance
	OriginalTitleProvenance    FactProvenance
	YearProvenance             FactProvenance
	GenresProvenance           FactProvenance
	OriginalLanguageProvenance FactProvenance
	DistributorProvenance      FactProvenance
}

// PreferredTitle selects manual intent, then the tracker's provider, then the canonical title.
func (m EffectiveMetadata) PreferredTitle(provider string) string {
	return preferMetadataString(m.Title, provider, m.TitleProvenance)
}

// PreferredAlternateTitle preserves an explicit empty alternate title.
func (m EffectiveMetadata) PreferredAlternateTitle(provider string) string {
	return preferMetadataString(m.AlternateTitle, provider, m.AlternateTitleProvenance)
}

// PreferredOriginalTitle preserves an explicit empty original title.
func (m EffectiveMetadata) PreferredOriginalTitle(provider string) string {
	return preferMetadataString(m.OriginalTitle, provider, m.OriginalTitleProvenance)
}

// PreferredYear selects the manual year before a tracker-preferred provider year.
func (m EffectiveMetadata) PreferredYear(provider int) int {
	if m.YearProvenance.IsManual() || provider == 0 {
		return m.Year
	}
	return provider
}

// PreferredGenres returns a detached list, retaining an explicit empty correction.
func (m EffectiveMetadata) PreferredGenres(provider []string) []string {
	if m.GenresProvenance.IsManual() || len(provider) == 0 {
		return slices.Clone(m.Genres)
	}
	return slices.Clone(provider)
}

// PreferredOriginalLanguage preserves explicit language clears.
func (m EffectiveMetadata) PreferredOriginalLanguage(provider string) string {
	return preferMetadataString(m.OriginalLanguage, provider, m.OriginalLanguageProvenance)
}

// PreferredDistributor preserves explicit distributor clears.
func (m EffectiveMetadata) PreferredDistributor(provider string) string {
	return preferMetadataString(m.Distributor, provider, m.DistributorProvenance)
}

func preferMetadataString(resolved, provider string, provenance FactProvenance) string {
	if provenance.IsManual() || provider == "" {
		return resolved
	}
	return provider
}

// MetadataFacts projects finalized descriptive values without mutable instructions.
func (r PreparedRelease) MetadataFacts() EffectiveMetadata {
	return EffectiveMetadata{
		Title:                      r.Naming.Title,
		AlternateTitle:             r.Naming.AlternateTitle,
		OriginalTitle:              r.Naming.OriginalTitle,
		Year:                       r.Naming.Year,
		Genres:                     slices.Clone(r.Naming.Genres),
		OriginalLanguage:           r.Media.OriginalLanguage,
		Distributor:                r.Media.Distributor,
		TitleProvenance:            r.Naming.TitleProvenance,
		AlternateTitleProvenance:   r.Naming.AlternateTitleProvenance,
		OriginalTitleProvenance:    r.Naming.OriginalTitleProvenance,
		YearProvenance:             r.Naming.YearProvenance,
		GenresProvenance:           r.Naming.GenresProvenance,
		OriginalLanguageProvenance: r.Media.OriginalLanguageProvenance,
		DistributorProvenance:      r.Media.DistributorProvenance,
	}
}

// MediaTrackKind distinguishes physical audio and subtitle streams.
type MediaTrackKind string

const (
	MediaTrackAudio    MediaTrackKind = "audio"
	MediaTrackSubtitle MediaTrackKind = "subtitle"
)

// MediaTrackFacts describes only an inspected stream. An opaque resource ID and
// exact manifest bind ordinal identities when native IDs are unavailable.
type MediaTrackFacts struct {
	ID                  string
	Kind                MediaTrackKind
	ResourceID          string
	ManifestFingerprint string
	NativeID            string
	Ordinal             int
	DetectedLanguages   []string
	Languages           []string
	LanguageProvenance  FactProvenance
	Default             bool
	Commentary          bool
}

// ManualLanguageFacts contains the resolved manual lists used in generated descriptions.
type ManualLanguageFacts struct {
	Audio              []string
	Subtitles          []string
	HardcodedSubtitles []string
}

// ManualLanguages projects aggregate intent or corrected tracks in manifest order.
func (m MediaFacts) ManualLanguages() ManualLanguageFacts {
	var result ManualLanguageFacts
	for _, track := range m.Tracks {
		if !track.LanguageProvenance.IsManual() {
			continue
		}
		for _, language := range track.Languages {
			if track.Kind == MediaTrackAudio && !slices.Contains(result.Audio, language) {
				result.Audio = append(result.Audio, language)
			}
			if track.Kind == MediaTrackSubtitle && !slices.Contains(result.Subtitles, language) {
				result.Subtitles = append(result.Subtitles, language)
			}
		}
	}
	if m.AudioLanguagesProvenance.IsManual() {
		result.Audio = slices.Clone(m.AudioLanguages)
	}
	if m.SubtitleLanguagesProvenance.IsManual() {
		result.Subtitles = slices.Clone(m.SubtitleLanguages)
	}
	if m.HardcodedSubtitleLanguagesProvenance.IsManual() {
		result.HardcodedSubtitles = slices.Clone(m.HardcodedSubtitleLanguages)
	}
	return result
}

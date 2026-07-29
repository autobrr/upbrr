// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "strings"

const (
	// ReleaseNameElementPolicyVersionV1 identifies the first shared release-name element contract.
	ReleaseNameElementPolicyVersionV1 = "release-name-elements/v1"
)

// EpisodeTitleMode controls automatic episode-title rendering in generated release names.
type EpisodeTitleMode string

const (
	// EpisodeTitleModeUnspecified inherits the shared include default.
	EpisodeTitleModeUnspecified EpisodeTitleMode = ""
	// EpisodeTitleModeInclude keeps an automatically generated single-episode title.
	EpisodeTitleModeInclude EpisodeTitleMode = "include"
	// EpisodeTitleModeOmit removes an automatically generated single-episode title.
	EpisodeTitleModeOmit EpisodeTitleMode = "omit"
)

// NormalizeEpisodeTitleMode resolves the shared unspecified default while
// preserving unsupported values for policy validation.
func NormalizeEpisodeTitleMode(mode EpisodeTitleMode) EpisodeTitleMode {
	normalized := EpisodeTitleMode(strings.ToLower(strings.TrimSpace(string(mode))))
	if normalized == EpisodeTitleModeUnspecified {
		return EpisodeTitleModeInclude
	}
	return normalized
}

// ReleaseNameElementPolicy is the versioned shared contract applied to
// structurally generated names before tracker-specific formatting.
type ReleaseNameElementPolicy struct {
	// Version identifies the element-policy contract included in projection
	// fingerprints.
	Version string
	// EpisodeTitleMode applies only to structurally generated names; exact
	// source names and explicit release-name overrides remain authoritative.
	EpisodeTitleMode EpisodeTitleMode
}

// Normalized resolves shared defaults without accepting unsupported values.
func (p ReleaseNameElementPolicy) Normalized() ReleaseNameElementPolicy {
	p.Version = strings.TrimSpace(p.Version)
	p.EpisodeTitleMode = NormalizeEpisodeTitleMode(p.EpisodeTitleMode)
	return p
}

type ReleaseNameRequest struct {
	Category     string
	Type         string
	Title        string
	AltTitle     string
	Year         int
	ManualYear   int
	Resolution   string
	Audio        string
	Service      string
	Season       string
	Episode      string
	Part         string
	Repack       string
	ThreeD       string
	Tag          string
	Source       string
	UHD          string
	HDR          string
	WebDV        bool
	EpisodeTitle string
	// ManualEpisodeTitle reports that EpisodeTitle came from an explicit
	// override. Both blank and nonblank manual values remain authoritative.
	ManualEpisodeTitle bool
	VideoCodec         string
	VideoEncode        string
	DiscType           string
	Region             string
	DVDSize            string
	Edition            string
	SearchYear         string
	DailyDate          string
	ManualDate         bool
	TMDBDateMatch      bool
	NoSeason           bool
	NoYear             bool
	NoAKA              bool
}

// ReleaseNameVariant contains one internally consistent generated name form.
type ReleaseNameVariant struct {
	// NameNoTag, Name, and CleanName are the same projections exposed by
	// [ReleaseNameResult] for this structural alternative.
	NameNoTag string
	Name      string
	CleanName string
}

// GeneratedReleaseNameVariants contains safe structural alternatives produced
// by the canonical name builder. Empty variants mean the current name must not
// be treated as automatically generated.
type GeneratedReleaseNameVariants struct {
	// IncludeEpisodeTitle retains the canonical single-episode title.
	IncludeEpisodeTitle ReleaseNameVariant
	// OmitEpisodeTitle removes only the automatically generated episode title.
	OmitEpisodeTitle ReleaseNameVariant
}

type ReleaseNameResult struct {
	NameNoTag         string
	Name              string
	CleanName         string
	GeneratedVariants GeneratedReleaseNameVariants
	MissingFields     []string
}

type ReleaseNameOverrides struct {
	Category         *string
	Type             *string
	Source           *string
	Resolution       *string
	Tag              *string
	Service          *string
	Edition          *string
	Season           *string
	Episode          *string
	EpisodeTitle     *string
	ManualYear       *int
	ManualDate       *string
	UseSeasonEpisode *bool
	NoSeason         *bool
	NoYear           *bool
	NoAKA            *bool
	NoTag            *bool
	NoEdition        *bool
	NoDub            *bool
	NoDual           *bool
	DualAudio        *bool
	Region           *string
}

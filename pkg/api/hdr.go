// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// HDRFormat is one normalized video dynamic-range or compatibility format.
type HDRFormat string

const (
	HDRFormatSDR         HDRFormat = "sdr"
	HDRFormatDolbyVision HDRFormat = "dolby_vision"
	HDRFormatHDR10       HDRFormat = "hdr10"
	HDRFormatHDR10Plus   HDRFormat = "hdr10_plus"
	HDRFormatHLG         HDRFormat = "hlg"
	HDRFormatPQ10        HDRFormat = "pq10"
	HDRFormatHDRVivid    HDRFormat = "hdr_vivid"
	HDRFormatWCG         HDRFormat = "wide_color_gamut"
)

// HDREvidenceOrigin identifies the strongest evidence used to derive HDR facts.
type HDREvidenceOrigin string

const (
	HDREvidenceUnknown         HDREvidenceOrigin = "unknown"
	HDREvidenceBDInfo          HDREvidenceOrigin = "bdinfo"
	HDREvidenceMediaInfo       HDREvidenceOrigin = "mediainfo"
	HDREvidenceTrackerAPI      HDREvidenceOrigin = "tracker_api"
	HDREvidenceTrackerDetails  HDREvidenceOrigin = "tracker_details"
	HDREvidenceTrackerTitle    HDREvidenceOrigin = "tracker_title"
	HDREvidenceContentFilename HDREvidenceOrigin = "content_filename"
	HDREvidenceReleaseName     HDREvidenceOrigin = "release_name"
)

// HDREvidenceStatus describes whether normalized HDR facts are authoritative
// enough for automatic comparison.
type HDREvidenceStatus string

const (
	HDREvidenceComplete      HDREvidenceStatus = "complete"
	HDREvidencePartial       HDREvidenceStatus = "partial"
	HDREvidenceMissing       HDREvidenceStatus = "missing"
	HDREvidenceContradictory HDREvidenceStatus = "contradictory"
)

// HDRFacts preserves normalized HDR formats, compatibility, and provenance.
// Formats and fallback formats are ordered, duplicate-free values.
type HDRFacts struct {
	Formats            []HDRFormat       `json:"formats,omitempty"`
	DolbyVisionProfile string            `json:"dolbyVisionProfile,omitempty"`
	FallbackFormats    []HDRFormat       `json:"fallbackFormats,omitempty"`
	Origin             HDREvidenceOrigin `json:"origin"`
	Status             HDREvidenceStatus `json:"status"`
	SourceFields       []string          `json:"sourceFields,omitempty"`
	Contradictions     []string          `json:"contradictions,omitempty"`
}

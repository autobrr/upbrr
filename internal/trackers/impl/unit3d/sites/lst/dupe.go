// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"net/url"
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// titleSearchParams keeps the provider and complete category family while
// removing TV-season narrowing: any torrent for the title defeats the exception.
func titleSearchParams(params url.Values) {
	params.Del("name")
	params.Del("seasonNumber")
}

func titleSearchPolicy() trackers.TitleSearchPolicy {
	return trackers.TitleSearchPolicy{
		ID: "lst/title-language-search/v1",
		Required: func(subject api.TrackerValidationSubject) bool {
			facts := subject.LanguageFacts
			return !trackers.IsFullDiscUpload(subject.DiscType, subject.Type) && len(facts.OriginalLanguages) > 0 &&
				facts.AudioStatus == api.MetadataEvidenceStatusComplete && facts.SubtitleStatus == api.MetadataEvidenceStatusComplete &&
				!slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX") &&
				!slices.Contains(facts.ProgrammeLanguages, "English") &&
				!slices.Contains(facts.SubtitleLanguages, "English")
		},
	}
}

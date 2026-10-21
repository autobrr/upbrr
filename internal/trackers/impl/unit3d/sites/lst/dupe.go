// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"net/url"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// titleSearchParams keeps the provider and complete category family while
// removing TV-season narrowing for language and source-preservation checks.
func titleSearchParams(params url.Values) {
	params.Del("name")
	params.Del("seasonNumber")
}

func titleSearchPolicy() trackers.TitleSearchPolicy {
	return trackers.TitleSearchPolicy{
		ID: "lst/title-language-search/v3",
		Required: func(subject api.TrackerValidationSubject) bool {
			if legacyContainerCandidate(subject) {
				return true
			}
			return resourceTitleSearchRequired(subject)
		},
	}
}

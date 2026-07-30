// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validateAZConstructibility(site siteDefinition, subject api.TrackerValidationSubject) []api.RuleFailure {
	meta := azUploadSubject(subject)
	failures := make([]api.RuleFailure, 0, 3)
	if strings.TrimSpace(categoryID(meta)) == "" || strings.TrimSpace(categorySlug(meta)) == "" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_category",
			"Tracker does not support the canonical release category.",
			api.RuleDispositionStrict,
		))
	}
	if ripTypeID(meta) == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_type",
			"Tracker does not support the release type.",
			api.RuleDispositionStrict,
		))
	}
	if videoQualityID(site, meta) == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_resolution",
			"Tracker does not support the release resolution.",
			api.RuleDispositionStrict,
		))
	}
	return failures
}

func azUploadSubject(subject api.TrackerValidationSubject) api.UploadSubject {
	return api.UploadSubject{
		SourcePath:           subject.SourcePath,
		DiscType:             subject.DiscType,
		Tag:                  subject.Tag,
		Release:              subject.Release,
		Identity:             subject.Identity,
		ProviderMetadata:     subject.ProviderMetadata,
		SeasonInt:            subject.SeasonInt,
		EpisodeInt:           subject.EpisodeInt,
		TVPack:               subject.TVPack,
		Anime:                subject.Anime,
		AudioLanguages:       append([]string(nil), subject.AudioLanguages...),
		SubtitleLanguages:    append([]string(nil), subject.SubtitleLanguages...),
		Container:            subject.Container,
		Source:               subject.Source,
		Type:                 subject.Type,
		HDR:                  subject.HDR,
		Region:               subject.Region,
		VideoCodec:           subject.VideoCodec,
		VideoEncode:          subject.VideoEncode,
		BitDepth:             subject.BitDepth,
		Edition:              subject.Edition,
		WebDV:                subject.WebDV,
		Service:              subject.Service,
		ServiceLongName:      subject.ServiceLongName,
		ReleaseName:          subject.ReleaseName,
		ReleaseNameNoTag:     subject.ReleaseNameNoTag,
		ReleaseNameOverrides: subject.ReleaseNameOverrides,
	}
}

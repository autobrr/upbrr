// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validateUnit3DConstructibility(
	trackerName string,
	subject api.TrackerValidationSubject,
	profile SiteProfile,
) []api.RuleFailure {
	meta := unit3DUploadSubject(subject)
	failures := make([]api.RuleFailure, 0, 4)
	if categoryID := strings.TrimSpace(resolveUnit3DCategoryIDForTracker(trackerName, meta, profile)); categoryID == "" || categoryID == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_category",
			"Tracker does not support the canonical release category.",
			api.RuleDispositionStrict,
		))
	}
	if _, err := resolveUnit3DTypeIDForTracker(trackerName, meta, profile); err != nil {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_type",
			"Tracker does not support the release type.",
			api.RuleDispositionStrict,
		))
	}
	if resolutionID := strings.TrimSpace(resolveUnit3DResolutionIDForTracker(trackerName, meta, profile)); resolutionID == "" || resolutionID == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_resolution",
			"Tracker does not support the release resolution.",
			api.RuleDispositionStrict,
		))
	}
	if strings.EqualFold(resolveUnit3DCategory(meta), "TV") &&
		(!validUnit3DSeason(meta, profile) || subject.EpisodeInt <= 0 && !subject.TVPack) {
		failures = append(failures, trackers.NewRuleFailure(
			"canonical_tv_metadata",
			"Canonical TV season/episode metadata is required for this tracker.",
			api.RuleDispositionStrict,
		))
	}
	return failures
}

// validUnit3DSeason preserves the positive-season default and permits only a
// site's explicit opt-in to canonical, individually identified specials.
func validUnit3DSeason(meta api.UploadSubject, profile SiteProfile) bool {
	return meta.SeasonInt > 0 || profile.AllowSeasonZeroEpisodes && meta.SeasonInt == 0 &&
		meta.SeasonStr == "S00" && meta.EpisodeInt > 0 && !meta.TVPack && !meta.MultipleEpisodes
}

func unit3DUploadSubject(subject api.TrackerValidationSubject) api.UploadSubject {
	return api.UploadSubject{
		SourcePath:             subject.SourcePath,
		DiscType:               subject.DiscType,
		Scene:                  subject.Scene,
		Tag:                    subject.Tag,
		Release:                subject.Release,
		Identity:               subject.Identity,
		ProviderMetadata:       subject.ProviderMetadata,
		LanguageFacts:          subject.LanguageFacts.Clone(),
		EffectiveMetadata:      subject.EffectiveMetadata,
		SeasonInt:              subject.SeasonInt,
		EpisodeInt:             subject.EpisodeInt,
		SeasonStr:              subject.SeasonStr,
		MultipleEpisodes:       subject.MultipleEpisodes,
		TVPack:                 subject.TVPack,
		DailyEpisodeDate:       subject.DailyEpisodeDate,
		Anime:                  subject.Anime,
		Disc:                   subject.Disc,
		AudioLanguages:         append([]string(nil), subject.AudioLanguages...),
		SubtitleLanguages:      append([]string(nil), subject.SubtitleLanguages...),
		Container:              subject.Container,
		Audio:                  subject.Audio,
		Channels:               subject.Channels,
		Source:                 subject.Source,
		Type:                   subject.Type,
		UHD:                    subject.UHD,
		HDR:                    subject.HDR,
		Distributor:            subject.Distributor,
		Region:                 subject.Region,
		VideoCodec:             subject.VideoCodec,
		VideoEncode:            subject.VideoEncode,
		BitDepth:               subject.BitDepth,
		EditionSet:             subject.EditionSet,
		Cut:                    subject.Cut,
		Presentation:           subject.Presentation,
		Edition:                subject.Edition,
		Repack:                 subject.Repack,
		WebDV:                  subject.WebDV,
		Assessments:            subject.Assessments,
		Service:                subject.Service,
		ServiceLongName:        subject.ServiceLongName,
		ReleaseName:            subject.ReleaseName,
		ReleaseNameNoTag:       subject.ReleaseNameNoTag,
		TrackerConfigOverrides: subject.TrackerConfigOverrides,
		TrackerSiteOverrides:   subject.TrackerSiteOverrides,
		ReleaseNameOverrides:   subject.ReleaseNameOverrides,
	}
}

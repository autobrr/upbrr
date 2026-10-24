// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

// inputLanguageReview requests facts only when this tracker's current predicates
// need programme evidence. Unknown roles and incomplete coverage are not languages.
func inputLanguageReview(binding ValidationPolicyBinding, subject api.UploadSubject, trackerID api.TrackerID) []api.InputReadinessFieldOutcome {
	if binding.LanguageAssessment == nil {
		return nil
	}
	failures := binding.LanguageAssessment(api.NewTrackerValidationSubject(subject, string(trackerID)))
	required := make(map[string]bool)
	origins := make(map[string]bool)
	for _, failure := range failures {
		if failure.EvidenceStatus != api.MetadataEvidenceStatusPartial || api.NormalizeRuleDisposition(failure.Disposition) == api.RuleDispositionAdvisory {
			continue
		}
		for _, id := range failure.AudioOriginCorrectionTrackIDs {
			origins[id] = true
		}
		for _, id := range failure.LanguageCorrectionTrackIDs {
			required[id] = true
		}
	}
	var outcomes []api.InputReadinessFieldOutcome
	for _, track := range mediafacts.UnresolvedProgrammeLanguages(subject.LanguageFacts) {
		if !required[track.ID] {
			continue
		}
		outcomes = append(outcomes, api.InputReadinessFieldOutcome{
			Key:             "metadata.track_languages." + track.ID,
			TrackID:         track.ID,
			CorrectionField: new(api.CorrectionFieldMetadataTrackLanguages),
			Status:          api.InputReadinessFieldMissing,
			Disposition:     api.RuleDispositionStrict,
			Message:         "Identify this programme track's languages in Inspected tracks or with --track-languages; do not infer a language from the title.",
		})
	}
	for _, track := range mediafacts.UnresolvedProgrammeAudioOrigins(subject.LanguageFacts) {
		if !origins[track.ID] {
			continue
		}
		outcomes = append(outcomes, api.InputReadinessFieldOutcome{
			Key:             "metadata.track_audio_origins." + track.ID,
			TrackID:         track.ID,
			CorrectionField: new(api.CorrectionFieldMetadataTrackAudioOrigins),
			Status:          api.InputReadinessFieldMissing,
			Disposition:     api.RuleDispositionStrict,
			Message:         "Review whether this programme track is an original soundtrack or a dub in Inspected tracks; language tags alone do not establish dubbing.",
		})
	}
	return outcomes
}

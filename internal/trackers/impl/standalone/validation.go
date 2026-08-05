// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// UploadSubjectForValidation reconstructs the semantic subset consumed by
// tracker-local pure mapping helpers. It does not add paths, secrets, clients,
// or mutable runtime services that are absent from the validation contract.
func UploadSubjectForValidation(subject api.TrackerValidationSubject) api.UploadSubject {
	answers := map[string]map[string]string{}
	if tracker := strings.ToUpper(strings.TrimSpace(subject.Tracker)); tracker != "" {
		answers[tracker] = cloneAnswers(subject.QuestionnaireAnswers)
	}
	return api.UploadSubject{
		SourcePath:                  subject.SourcePath,
		VideoPath:                   subject.VideoPath,
		FileList:                    append([]string(nil), subject.FileList...),
		SourceSize:                  subject.SourceSize,
		DiscType:                    subject.DiscType,
		Scene:                       subject.Scene,
		SceneRenamed:                subject.SceneRenamed,
		SceneRenamedReason:          subject.SceneRenamedReason,
		PersonalRelease:             subject.PersonalRelease,
		Tag:                         subject.Tag,
		Release:                     subject.Release,
		Identity:                    subject.Identity,
		ProviderMetadata:            subject.ProviderMetadata,
		SeasonInt:                   subject.SeasonInt,
		EpisodeInt:                  subject.EpisodeInt,
		SeasonStr:                   subject.SeasonStr,
		EpisodeStr:                  subject.EpisodeStr,
		TVPack:                      subject.TVPack,
		DailyEpisodeDate:            subject.DailyEpisodeDate,
		Anime:                       subject.Anime,
		EpisodeTitle:                subject.EpisodeTitle,
		EpisodeOverview:             subject.EpisodeOverview,
		Disc:                        subject.Disc,
		AudioLanguages:              append([]string(nil), subject.AudioLanguages...),
		SubtitleLanguages:           append([]string(nil), subject.SubtitleLanguages...),
		Container:                   subject.Container,
		Audio:                       subject.Audio,
		Channels:                    subject.Channels,
		HasCommentary:               subject.HasCommentary,
		Is3D:                        subject.Is3D,
		Source:                      subject.Source,
		Type:                        subject.Type,
		UHD:                         subject.UHD,
		HDR:                         subject.HDR,
		Distributor:                 subject.Distributor,
		Region:                      subject.Region,
		VideoCodec:                  subject.VideoCodec,
		VideoEncode:                 subject.VideoEncode,
		HasEncodeSettings:           subject.HasEncodeSettings,
		BitDepth:                    subject.BitDepth,
		Edition:                     subject.Edition,
		Repack:                      subject.Repack,
		WebDV:                       subject.WebDV,
		Assessments:                 subject.Assessments,
		StreamOptimized:             subject.StreamOptimized,
		Service:                     subject.Service,
		ServiceLongName:             subject.ServiceLongName,
		ReleaseName:                 subject.ReleaseName,
		ReleaseNameNoTag:            subject.ReleaseNameNoTag,
		TrackerQuestionnaireAnswers: answers,
		TrackerConfigOverrides:      subject.TrackerConfigOverrides,
		TrackerSiteOverrides:        subject.TrackerSiteOverrides,
		ReleaseNameOverrides:        subject.ReleaseNameOverrides,
		DescriptionOverride:         subject.DescriptionOverride,
		DescriptionGroupsFinal:      subject.DescriptionGroupsFinal,
	}
}

func cloneAnswers(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	maps.Copy(clone, values)
	return clone
}

// PreparedMediaReady reports whether the already-produced local media facts
// needed by tracker payloads are available without reading private paths.
func PreparedMediaReady(subject api.TrackerValidationSubject) bool {
	if strings.EqualFold(strings.TrimSpace(subject.DiscType), "BDMV") {
		return subject.BDInfoReady
	}
	return subject.MediaInfoTextReady || subject.DVDVOBMediaInfoReady
}

// ValidatePreparation retains constructibility as a defensive invariant for
// direct tracker preparation callers.
func ValidatePreparation(
	ctx context.Context,
	input trackers.PreparationInput,
	policy trackers.ValidationPolicyBinding,
) error {
	if policy.Check == nil {
		return nil
	}
	subject := api.NewTrackerValidationSubject(input.Meta, input.Tracker)
	if strings.EqualFold(strings.TrimSpace(input.Meta.DiscType), "BDMV") &&
		!subject.BDInfoReady &&
		strings.TrimSpace(input.Meta.SourcePath) != "" &&
		len(input.Meta.SelectedBDMVPlaylists) > 0 {
		// Legacy direct preparation callers may still resolve a previously
		// prepared BDInfo summary from the release temp directory.
		subject.BDInfoReady = true
	}
	failures, err := policy.Check(ctx, subject, input.Logger)
	if err != nil {
		return fmt.Errorf("trackers: %s constructibility: %w", strings.ToUpper(strings.TrimSpace(input.Tracker)), err)
	}
	var blockingFailure *api.RuleFailure
	for i := range failures {
		if trackers.RuleFailureBlocksExecution(failures[i], input.ExecutionMode) {
			blockingFailure = &failures[i]
			break
		}
	}
	if blockingFailure == nil {
		return nil
	}
	if input.Logger != nil {
		input.Logger.Warnf(
			"tracker constructibility failed tracker=%s rule=%s reason=%s",
			strings.ToUpper(strings.TrimSpace(input.Tracker)),
			strings.TrimSpace(blockingFailure.Rule),
			strings.TrimSpace(blockingFailure.Reason),
		)
	}
	return fmt.Errorf(
		"trackers: %s constructibility %s: %s",
		strings.ToUpper(strings.TrimSpace(input.Tracker)),
		strings.TrimSpace(blockingFailure.Rule),
		strings.TrimSpace(blockingFailure.Reason),
	)
}

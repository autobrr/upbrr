// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func primaryTechnicalSubject(subject api.TrackerValidationSubject) api.TrackerValidationSubject {
	for _, file := range subject.MediaFileFacts.Files {
		if !file.Primary || file.ResourceID == "" {
			continue
		}
		subject.Container, subject.VideoCodec, subject.VideoEncoder = file.Container, file.VideoCodec, file.VideoEncoder
		// Programme inspection may not provide source provenance.
		if strings.TrimSpace(file.Source) != "" {
			subject.Source = file.Source
		}
		subject.HDRFacts = file.HDRFacts
		subject.Release.Resolution = file.Resolution
		subject.HasEncodeSettings = file.HasEncodeSettings
		if file.EncodeSettingsKnown {
			if file.HasEncodeSettings {
				subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusPresent
			} else {
				subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusMissing
			}
		} else {
			subject.Assessments.MediaInfoEncodeSettings = ""
		}
		break
	}
	return subject
}

func legacyContainerCandidate(subject api.TrackerValidationSubject) bool {
	subject = primaryTechnicalSubject(subject)
	if singleLegacyContainerCandidate(subject) {
		return true
	}
	for _, file := range subject.MediaFileFacts.Files {
		if singleLegacyContainerCandidate(containerFileSubject(subject, file)) {
			return true
		}
	}
	return false
}

func singleLegacyContainerCandidate(subject api.TrackerValidationSubject) bool {
	subject = primaryTechnicalSubject(subject)
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return false
	}
	container := strings.ToLower(strings.TrimSpace(subject.Container))
	return (container == "avi" || container == "mkv") &&
		(strings.EqualFold(subject.VideoEncoder, "XviD") || strings.EqualFold(subject.VideoEncoder, "DivX") ||
			strings.EqualFold(subject.VideoCodec, "XviD") || strings.EqualFold(subject.VideoCodec, "DivX"))
}

// containerException identifies only supported exception candidates; eligibility
// and the required explanation are checked independently before submission.
// An unresolved source remains unverified and is still sent to moderation.
func containerException(subject api.TrackerValidationSubject) string {
	subject = primaryTechnicalSubject(subject)
	if legacyContainerCandidate(subject) {
		return "legacy"
	}
	if kind := singleContainerException(subject); kind != "" {
		return kind
	}
	for _, file := range subject.MediaFileFacts.Files {
		if kind := singleContainerException(containerFileSubject(subject, file)); kind != "" {
			return kind
		}
	}
	return ""
}

func containerFileSubject(subject api.TrackerValidationSubject, file api.MediaFileFact) api.TrackerValidationSubject {
	subject.Container = file.Container
	if !file.Primary || file.ResourceID == "" || strings.TrimSpace(file.Source) != "" {
		subject.Source = file.Source
	}
	subject.VideoCodec, subject.VideoEncoder = file.VideoCodec, file.VideoEncoder
	subject.MediaFileFacts = api.MediaFileFacts{}
	subject.HDRFacts = file.HDRFacts
	return subject
}

func singleContainerException(subject api.TrackerValidationSubject) string {
	subject = primaryTechnicalSubject(subject)

	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return ""
	}
	if singleLegacyContainerCandidate(subject) {
		return "legacy"
	}
	switch strings.ToLower(strings.TrimSpace(subject.Container)) {
	case "ts":
		switch strings.ToUpper(strings.TrimSpace(subject.Source)) {
		case "UHDTV", "HDTV", "SDTV":
			return "broadcast"
		case "":
			return "unresolved_broadcast"
		}
	case "mp4":
		if strings.EqualFold(subject.Type, "WEBDL") &&
			(subject.Source == "" || normalizedLSTSource(subject.Source) == "WEBDL" || normalizedLSTSource(subject.Source) == "WEB") &&
			subject.HDRFacts.Status == api.HDREvidenceComplete &&
			subject.HDRFacts.DolbyVisionProfile == "5" &&
			slices.Contains(subject.HDRFacts.Formats, api.HDRFormatDolbyVision) &&
			len(subject.HDRFacts.Contradictions) == 0 {
			return "profile5"
		}
	}
	return ""
}

func exceptionValidationFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	subject = primaryTechnicalSubject(subject)
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	failure := func(rule, reason string, status api.MetadataEvidenceStatus) []api.RuleFailure {
		f := trackers.NewEvidenceRuleFailure(rule, reason, api.RuleDispositionStrict, status)
		f.DebugBypass = true
		f.QuestionnaireInput = rule == "lst_exception_note"
		return []api.RuleFailure{f}
	}
	if subject.MediaFileFacts.ExpectedFileCount > len(subject.MediaFileFacts.Files) {
		return failure("lst_container_evidence", "Container inspection does not cover every programme resource", api.MetadataEvidenceStatusPartial)
	}
	for _, file := range subject.MediaFileFacts.Files {
		current := containerFileSubject(subject, file)
		if strings.EqualFold(strings.TrimSpace(file.Container), "mkv") || singleContainerException(current) != "" {
			continue
		}
		status := api.MetadataEvidenceStatusComplete
		if strings.TrimSpace(file.Container) == "" || strings.EqualFold(file.Container, "mp4") ||
			strings.EqualFold(file.Container, "ts") && file.Source == "" ||
			strings.EqualFold(file.Container, "avi") && file.VideoCodec == "" && file.VideoEncoder == "" {
			status = api.MetadataEvidenceStatusPartial
		}
		return failure("lst_resource_container", "A programme resource has an unsupported container or unresolved container-exception evidence", status)
	}
	container := strings.ToLower(strings.TrimSpace(subject.Container))
	kind := containerException(subject)
	if singleContainerException(subject) == "" && container != "mkv" {
		if container == "" || container == "ts" && strings.TrimSpace(subject.Source) == "" ||
			container == "avi" && strings.TrimSpace(subject.VideoCodec) == "" && strings.TrimSpace(subject.VideoEncoder) == "" ||
			container == "mp4" && strings.EqualFold(subject.Type, "WEBDL") &&
				(subject.HDRFacts.Status != api.HDREvidenceComplete || subject.HDRFacts.DolbyVisionProfile == "") {
			return failure(
				"lst_container_evidence",
				"Container exception eligibility is unresolved; inspect the container and Dolby Vision profile before upload",
				api.MetadataEvidenceStatusPartial,
			)
		}
		return failure("lst_container", "This container is not permitted for the prepared release", api.MetadataEvidenceStatusComplete)
	}
	if kind == "" {
		return nil
	}

	if strings.TrimSpace(subject.QuestionnaireAnswers[exceptionQuestionKey(subject)]) == "" {
		return failure(
			"lst_exception_note",
			"Enter a container exception explanation in LST upload inputs before submission",
			api.MetadataEvidenceStatusPartial,
		)
	}
	var guidance []api.RuleFailure
	if kind == "legacy" {
		guidance = append(
			guidance,
			trackers.NewEvidenceRuleFailure(
				"guidance_lst_container_source",
				"Source availability in every format cannot be established from the prepared media or a title-search count. Review source records and explain the preservation exception; this does not establish staff permission.",
				api.RuleDispositionAdvisory,
				api.MetadataEvidenceStatusPartial,
			),
		)
	}
	unresolvedBroadcast := singleContainerException(subject) == "unresolved_broadcast" ||
		slices.ContainsFunc(subject.MediaFileFacts.Files, func(file api.MediaFileFact) bool {
			return singleContainerException(containerFileSubject(subject, file)) == "unresolved_broadcast"
		})
	if unresolvedBroadcast {
		guidance = append(
			guidance,
			trackers.NewEvidenceRuleFailure(
				"guidance_lst_broadcast_source",
				"An inspected transport stream has no established broadcast source. Check each resource's source records and explain the container exception; neither its container nor a saved answer establishes broadcast provenance.",
				api.RuleDispositionAdvisory,
				api.MetadataEvidenceStatusPartial,
			),
		)
	}
	return guidance
}

func exceptionQuestionKey(subject api.TrackerValidationSubject) string {
	subject = primaryTechnicalSubject(subject)
	evidence := exceptionFingerprint(subject)

	return trackers.LanguageQuestionKey(subject, "exception_note_"+evidence)
}

func exceptionFingerprint(subject api.TrackerValidationSubject) string {
	fingerprint, _ := api.CanonicalWorkflowFingerprint(struct {
		Container, Codec, Encoder string
		HDR                       api.HDRFacts
		Files                     api.MediaFileFacts
	}{subject.Container, subject.VideoCodec, subject.VideoEncoder, subject.HDRFacts, subject.MediaFileFacts})
	return string(fingerprint)
}

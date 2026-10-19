// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"cmp"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const trackPresentationHelp = "Review each prepared resource: correct audio language tags, subtitle language and forced flags, other default flags, and reported bitrates. Descriptive audio titles are recommended; subtitle ordering is optional. A description note does not correct an incorrect file tag."

func presentationReviewQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	if subject.PersonalRelease {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:     "track_presentation",
			Label:   "Review track presentation",
			Kind:    "select",
			Options: []string{"unresolved", "reviewed_correct", "corrections_needed"},
			Help:    trackPresentationHelp,
		})
	}
	switch strings.ToUpper(strings.TrimSpace(subject.Type)) {
	case "REMUX", "ENCODE":
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:     "source_documentation",
				Label:   "Include source and processing references",
				Kind:    "select",
				Options: []string{"unresolved", "will_add_required_data"},
				Help:    "Confirm that you will include the exact source releases, relevant comparisons, processing logs (including eac3to where applicable), and release notes in the description. Explain any genuinely inapplicable item there.",
			},
		)
	case "WEBDL", "WEB-DL":
		if cmp.Or(strings.TrimSpace(subject.Service), strings.TrimSpace(subject.ServiceLongName)) != "" {
			break
		}
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:     "web_source_naming",
				Label:   "Review WEB source naming",
				Kind:    "select",
				Options: []string{"unresolved", "reviewed_correct", "corrections_needed"},
				Help:    "Confirm that the final release name accurately identifies the source service using your source records.",
			},
		)
	}
	for i := range fields {
		nameKey, _ := api.CanonicalWorkflowFingerprint(
			[]any{subject.FinalizedUploadName, subject.Release, subject.Service, subject.ServiceLongName, subject.ReleaseNameOverrides},
		)
		key := fields[i].Key
		fields[i].Key = assetQuestionKey(subject, "presentation_"+key+"_"+string(nameKey))
		if key == "source_documentation" {
			// A promise to add source references survives later screenshot/hosting stages.
			fields[i].Key = trackers.LanguageQuestionKey(subject, "presentation_"+key+"_will_add_"+string(nameKey))
			if !subject.PersonalRelease {
				fields[i].Placeholder = "No confirmation needed"
				fields[i].Help = "No confirmation is needed for ordinary releases. Source releases, relevant comparisons, processing logs (including eac3to where applicable), and release notes are recommended in the description. Explain any genuinely inapplicable item there."
			}
		}
		fields[i].Required = subject.PersonalRelease
		fields[i].Value = strings.TrimSpace(subject.QuestionnaireAnswers[fields[i].Key])
		fields[i].NeedsReview = fields[i].Required && (fields[i].Value == "" || fields[i].Value == "unresolved")
	}
	return fields
}

func presentationReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var failures []api.RuleFailure
	if !subject.PersonalRelease {
		failure := trackers.LanguageRuleFailure(subject, "track_presentation", trackPresentationHelp, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	var untitledAudio []string
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind == api.MediaTrackAudio && strings.TrimSpace(track.Title) == "" {
			untitledAudio = append(untitledAudio, track.ResourceID+":"+track.ID)
		}
	}
	if len(untitledAudio) > 0 {
		failures = append(failures, trackers.LanguageRuleFailure(
			subject,
			"audio_titles",
			"audio tracks should have descriptive titles for context; missing title tags for tracks: "+strings.Join(untitledAudio, ", "),
			trackers.LanguageAdvisory,
		))
	}
	switch strings.ToUpper(strings.TrimSpace(subject.Type)) {
	case "WEBDL", "WEB-DL":
		if service := cmp.Or(strings.TrimSpace(subject.Service), strings.TrimSpace(subject.ServiceLongName)); service != "" {
			failures = append(failures, trackers.LanguageRuleFailure(
				subject,
				"web_source_naming",
				"WEB release names should accurately identify the source service using your source records; resolved service: "+service,
				trackers.LanguageAdvisory,
			))
		}
	}
	if tracks := tracksMissingLanguages(subject); subject.PersonalRelease && len(tracks) > 0 {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"presentation_tags",
				"correct missing file language tags for tracks: "+strings.Join(tracks, ", "),
				trackers.LanguageProhibited,
			),
		)
	}
	for _, field := range presentationReviewQuestions(subject) {
		value := strings.TrimSpace(subject.QuestionnaireAnswers[field.Key])
		if !field.Required && (value == "" || value == "unresolved") {
			if slices.Contains(field.Options, "will_add_required_data") {
				failure := trackers.LanguageRuleFailure(subject, "source_documentation", field.Help, trackers.LanguageAdvisory)
				failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
				failures = append(failures, failure)
			}
			continue
		}
		valid := slices.Contains(field.Options, value) && (value == "reviewed_correct" || value == "will_add_required_data")
		if valid {
			continue
		}
		outcome := trackers.LanguageAdvisory
		if subject.PersonalRelease {
			outcome = trackers.LanguageUnresolved
			if value == "corrections_needed" {
				outcome = trackers.LanguageProhibited
			}
		}
		failure := trackers.LanguageRuleFailure(subject, "presentation_review", field.Label+": current source-bound review or correction is needed", outcome)
		failure.QuestionnaireInput = true
		if value == "" || value == "unresolved" {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failures = append(failures, failure)
	}
	return failures
}

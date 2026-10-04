// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, groupID string) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(meta, "PTP")
	fields := make([]api.TrackerQuestionnaireField, 0, 7)
	if strings.TrimSpace(groupID) == "" {
		title, year := resolveGroupTitleYear(meta)
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      "title",
			Label:    "Group Title",
			Kind:     "text",
			Value:    title,
			Required: true,
		}, api.TrackerQuestionnaireField{
			Key:         "year",
			Label:       "Year",
			Kind:        "text",
			Value:       year,
			Required:    false,
			Placeholder: "Release year",
		}, api.TrackerQuestionnaireField{
			Key:      "poster",
			Label:    "Poster URL",
			Kind:     "text",
			Value:    resolvePoster(meta),
			Required: true,
		}, api.TrackerQuestionnaireField{
			Key:         "tags",
			Label:       "Tags",
			Kind:        "text",
			Value:       resolveTags(meta),
			Required:    true,
			Placeholder: "Comma separated tags",
		}, api.TrackerQuestionnaireField{
			Key:         "trailer",
			Label:       "Trailer URL",
			Kind:        "text",
			Value:       resolveTrailer(meta),
			Required:    false,
			Placeholder: "YouTube trailer URL",
		}, api.TrackerQuestionnaireField{
			Key:      "album_desc",
			Label:    "Group Description",
			Kind:     "textarea",
			Value:    resolveOverview(meta),
			Required: false,
		})
	}
	fields = append(fields, subtitleReviewFields(meta, answers)...)
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{
		Tracker: "PTP",
		Fields:  fields,
	}
}

var subtitleReviewOptions = []string{
	"English Hardcoded Subs (Full)",
	"English Hardcoded Subs (Forced)",
	"No English Subs",
	"English Softsubs Exist (Mislabeled)",
	"Hardcoded Subs (Non-English)",
}

// ProjectionQuestionnaire requests an explicit, tracker-scoped subtitle decision.
// It never changes canonical language facts or introduces a global Input gate.
func (d *Definition) ProjectionQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(meta, "PTP")
	fields := subtitleReviewFields(meta, answers)
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "PTP", Fields: fields}
}

func subtitleReviewFields(meta api.UploadSubject, answers map[string]string) []api.TrackerQuestionnaireField {
	legacy := strings.ToLower(strings.TrimSpace(answers["no_english_subtitles"]))
	if !meta.HardcodedSubs && (legacy == "yes" || legacy == "no") {
		return nil
	}
	if !requiresSubtitleReview(meta) {
		return nil
	}
	fields := make([]api.TrackerQuestionnaireField, 0, 3)
	if !meta.HardcodedSubs {
		decision := answers["trumpable_review"]
		if decision != "yes" && decision != "no" {
			decision = ""
		}
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:      "trumpable_review",
				Label:    "No English subtitles and no first English audio track: mark trumpable?",
				Kind:     "select",
				Options:  []string{"yes", "no"},
				Value:    decision,
				Required: true,
			},
		)
		if decision != "yes" {
			return fields
		}
	}
	selection, err := parseSubtitleReview(answers["subtitle_tags"])
	if err == nil {
		err = validateSubtitleReview(meta, answers, selection)
	}
	value := strings.Join(selection, ",")
	help := "Select all applicable choices. Correct mislabeled track languages separately in Input."
	if err != nil {
		value = ""
		help = err.Error()
	}
	fields = append(
		fields,
		api.TrackerQuestionnaireField{
			Key:      "subtitle_tags",
			Label:    "Subtitle and trumpable tags",
			Kind:     "multiselect",
			Options:  slices.Clone(subtitleReviewOptions),
			Value:    value,
			Help:     help,
			Required: true,
		},
	)
	if slices.Contains(selection, "Hardcoded Subs (Non-English)") && len(meta.HardcodedSubtitleLanguages) == 0 {
		languages := strings.TrimSpace(answers["hardcoded_subtitle_languages"])
		if _, err := withHardcodedSubtitleLanguages(api.UploadSubject{HardcodedSubs: true}, languages); err != nil {
			languages = ""
		}
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:      "hardcoded_subtitle_languages",
				Label:    "Hardcoded Subtitle Languages",
				Kind:     "text",
				Value:    languages,
				Required: true,
			},
		)
	}
	return fields
}

func subtitleReviewPending(meta api.UploadSubject, answers map[string]string) bool {
	return slices.ContainsFunc(
		subtitleReviewFields(meta, answers),
		func(field api.TrackerQuestionnaireField) bool { return field.Required && field.Value == "" },
	)
}

// parseSubtitleReview validates the comma-separated selection shared by CLI and WebUI.
func parseSubtitleReview(value string) ([]string, error) {
	var result []string
	for item := range strings.SplitSeq(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !slices.Contains(subtitleReviewOptions, item) {
			return nil, fmt.Errorf("PTP unsupported subtitle review choice %q", item)
		}
		if !slices.Contains(result, item) {
			result = append(result, item)
		}
	}
	return result, nil
}

// requiresSubtitleReview uses current prepared evidence, so a retained answer
// cannot assert missing English after a language correction removes that condition.
func requiresSubtitleReview(meta api.UploadSubject) bool {
	if meta.HardcodedSubs && len(meta.HardcodedSubtitleLanguages) > 0 {
		return false
	}
	subtitles := resolveSubtitles(meta)
	return meta.HardcodedSubs ||
		(!slices.Contains(subtitles, 3) && !slices.Contains(subtitles, 50) && (len(meta.AudioLanguages) == 0 || !ptpEnglishLanguage(meta.AudioLanguages[0])))
}

// validateSubtitleReview rejects contradictory claims before projection or payload use.
func validateSubtitleReview(meta api.UploadSubject, answers map[string]string, selected []string) error {
	englishSelected := slices.Contains(selected, "English Hardcoded Subs (Full)") || slices.Contains(selected, "English Hardcoded Subs (Forced)") ||
		slices.Contains(selected, "English Softsubs Exist (Mislabeled)")
	noEnglish := slices.Contains(selected, "No English Subs") || answers["no_english_subtitles"] == "yes"
	subtitles := append(slices.Clone(meta.SubtitleLanguages), meta.HardcodedSubtitleLanguages...)
	if slices.Contains(selected, "Hardcoded Subs (Non-English)") && len(meta.HardcodedSubtitleLanguages) == 0 &&
		strings.TrimSpace(answers["hardcoded_subtitle_languages"]) != "" {
		resolved, err := withHardcodedSubtitleLanguages(api.UploadSubject{HardcodedSubs: true}, answers["hardcoded_subtitle_languages"])
		if err != nil {
			return err
		}
		subtitles = append(subtitles, resolved.HardcodedSubtitleLanguages...)
	}
	if noEnglish && (englishSelected || ptpHasEnglishLanguage(subtitles)) {
		return errors.New("PTP No English Subtitles conflicts with English subtitle evidence or choices")
	}
	return nil
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func projectionQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(input.Meta, "ANT")
	typeName, _ := resolveType(input.Meta, answers)
	tags, _ := resolveTags(input.Meta, answers)
	questionnaire := buildQuestionnaire(input.Meta, uploadState{
		typeName:     typeName,
		tags:         tags,
		adultContent: detectAdult(input.Meta),
	})
	questionnaire.Fields = append(questionnaire.Fields, trumpQuestionnaireFields(input)...)
	return questionnaire
}

func buildQuestionnaire(meta api.UploadSubject, state uploadState) *api.TrackerQuestionnaire {
	current := standalone.QuestionnaireAnswers(meta, "ANT")
	fields := make([]api.TrackerQuestionnaireField, 0, 3)
	if _, answered := current["type"]; movieContent(meta) && (strings.TrimSpace(state.typeName) == "" || answered) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "type",
			Label:       "ANT Type",
			Kind:        "select",
			Options:     []string{"Feature Film", "Short Film"},
			Value:       metautil.FirstNonEmptyTrimmed(state.typeName, current["type"]),
			Placeholder: "Select a release type",
			Help:        "Pick the ANT content type for this release",
			Required:    true,
		})
	}
	if _, answered := current["tags"]; strings.TrimSpace(state.tags) == "" || answered {
		// ANT can obtain genres from TMDB when an explicit tag value is omitted.
		required := meta.ProviderMetadata.TMDB == nil || genreTags(meta.ProviderMetadata.TMDB.Genres) == ""
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "tags",
			Label:       "Tags",
			Kind:        "text",
			Value:       state.tags,
			Placeholder: "action, drama",
			Help:        "Comma-separated ANT tags",
			Required:    required,
		})
	}
	if _, answered := current["adult_screens"]; state.adultContent || answered {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "adult_screens",
			Label:       "Upload Screenshots",
			Kind:        "select",
			Options:     []string{"no", "yes"},
			Value:       metautil.FirstNonEmptyTrimmed(strings.TrimSpace(current["adult_screens"]), "no"),
			Placeholder: "Select yes or no",
			Help:        "Set to yes to include screenshots for adult content",
			Required:    true,
		})
	}
	fields = append(fields, api.TrackerQuestionnaireField{
		Key:         "requestid",
		Label:       "ANT Request ID",
		Kind:        "text",
		Value:       current["requestid"],
		Placeholder: "Optional request ID",
		Help:        "Positive ANT request ID to include with the upload",
	})
	if _, err := requestID(current); err != nil {
		fields[len(fields)-1].NeedsReview = true
		fields[len(fields)-1].Help = err.Error()
	}
	return &api.TrackerQuestionnaire{Tracker: "ANT", Fields: fields}
}

// requestID returns the optional request answer as canonical decimal text.
// Zero, malformed, negative, or overflowing IDs are rejected; blank means absent.
func requestID(answers map[string]string) (string, error) {
	value := strings.TrimSpace(answers["requestid"])
	if value == "" {
		return "", nil
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return "", errors.New("ANT request ID must be a positive integer")
	}
	return strconv.FormatUint(id, 10), nil
}


// trumpQuestionKeys bind an explicit upload intention to this source generation.
// Candidate review remains independently bound by the duplicate assessment.
func trumpQuestionKeys(source string, identity api.ExternalIdentity) (string, string) {
	if strings.TrimSpace(source) == "" || identity.Generation == 0 {
		return "", ""
	}
	binding := sha256.Sum256([]byte(fmt.Sprintf("%q|%d", source, identity.Generation)))
	return fmt.Sprintf("intended_trump_%x", binding), fmt.Sprintf("trump_reason_%x", binding)
}

// intendedTrump consumes only the current reviewed projection when available.
// Enabling Guru mode alone never authorizes the challenge retry.
func intendedTrump(input trackers.PreparationInput) (bool, string) {
	if !input.TrackerConfig.GuruTrump {
		return false, ""
	}
	choice, reason := trumpQuestionKeys(input.Meta.SourcePath, input.Meta.Identity)
	if choice == "" {
		return false, ""
	}
	answers := standalone.QuestionnaireAnswers(input.Meta, "ANT")
	if input.Projection != nil {
		answers = input.Projection.QuestionnaireAnswers
	}
	if answers[choice] != "yes" {
		return false, ""
	}
	return true, strings.TrimSpace(answers[reason])
}

func trumpQuestionnaireFields(input trackers.PreparationInput) []api.TrackerQuestionnaireField {
	if !input.TrackerConfig.GuruTrump {
		return nil
	}
	choice, reason := trumpQuestionKeys(input.Meta.SourcePath, input.Meta.Identity)
	if choice == "" {
		return nil
	}
	value := "no"
	intended, explanation := intendedTrump(input)
	if intended {
		value = "yes"
	}
	return []api.TrackerQuestionnaireField{
		{
			Key:     choice,
			Label:   "Intended ANT trump",
			Kind:    "select",
			Options: []string{"no", "yes"},
			Value:   value,
			Help:    "Guru+ only: explicitly intend to replace an existing release. Duplicate review and upload confirmation are still required; ANT decides eligibility.",
		},
		{
			Key:   reason,
			Label: "ANT trump reason",
			Kind:  "text",
			Value: explanation,
			Help:  "Optional explanation sent only for an explicitly intended Guru+ trump.",
		},
	}
}

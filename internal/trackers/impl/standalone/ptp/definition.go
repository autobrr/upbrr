// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// DataLookupConfigured reports whether PTP metadata lookup credentials are available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "PTP") {
			return strings.TrimSpace(entry.PTPAPIUser) != "" && strings.TrimSpace(entry.PTPAPIKey) != ""
		}
	}
	return false
}

// InputSchema exposes PTP's fact-bound No English Subtitles intent before
// projection. Auto leaves the final subtitle evidence authoritative.
func (d *Definition) InputSchema(meta api.UploadSubject) *api.TrackerQuestionnaire {
	value := strings.ToLower(strings.TrimSpace(standalone.QuestionnaireAnswers(meta, "PTP")["no_english_subtitles"]))
	if value == "" {
		value = "auto"
	}
	return &api.TrackerQuestionnaire{Tracker: "PTP", Fields: []api.TrackerQuestionnaireField{{
		Key:     "no_english_subtitles",
		Label:   "No English Subtitles",
		Kind:    "select",
		Options: []string{"auto", "yes", "no"},
		Value:   value,
		Help:    "Auto uses finalized subtitle and hardcoded-subtitle languages.",
	}}}
}

// InputReadiness validates PTP's fact-bound hardcoded and subtitle intent.
func (d *Definition) InputReadiness(meta api.UploadSubject) []api.InputReadinessFieldOutcome {
	if !meta.HardcodedSubs {
		return validateNoEnglishSubtitles(meta)
	}
	if len(meta.HardcodedSubtitleLanguages) == 0 {
		field := api.CorrectionFieldMetadataHardcodedSubtitleLanguages
		return append([]api.InputReadinessFieldOutcome{{
			Key:             "metadata.hardcoded_subtitle_languages",
			CorrectionField: &field,
			Status:          api.InputReadinessFieldMissing,
			Disposition:     api.RuleDispositionStrict,
			Message:         "PTP requires hardcoded subtitle languages when hardcoded subtitles are enabled",
		}}, validateNoEnglishSubtitles(meta)...)
	}
	for _, language := range meta.HardcodedSubtitleLanguages {
		if _, ok := subtitleIDs[strings.ToLower(strings.TrimSpace(language))]; !ok {
			field := api.CorrectionFieldMetadataHardcodedSubtitleLanguages
			return append([]api.InputReadinessFieldOutcome{{
				Key:             "metadata.hardcoded_subtitle_languages",
				CorrectionField: &field,
				Status:          api.InputReadinessFieldInvalid,
				Disposition:     api.RuleDispositionStrict,
				Message:         "PTP does not support one or more hardcoded subtitle languages",
			}}, validateNoEnglishSubtitles(meta)...)
		}
	}
	return validateNoEnglishSubtitles(meta)
}

func validateNoEnglishSubtitles(meta api.UploadSubject) []api.InputReadinessFieldOutcome {
	value := strings.ToLower(strings.TrimSpace(standalone.QuestionnaireAnswers(meta, "PTP")["no_english_subtitles"]))
	if value == "" || value == "auto" || value == "no" {
		return nil
	}
	if value != "yes" {
		return []api.InputReadinessFieldOutcome{{
			Key:         "tracker_input.no_english_subtitles",
			Status:      api.InputReadinessFieldInvalid,
			Disposition: api.RuleDispositionStrict,
			Message:     "PTP No English Subtitles must be auto, yes, or no",
		}}
	}
	allSubtitles := append(append([]string(nil), meta.SubtitleLanguages...), meta.HardcodedSubtitleLanguages...)
	if ptpHasEnglishLanguage(allSubtitles) {
		return []api.InputReadinessFieldOutcome{{
			Key:         "tracker_input.no_english_subtitles",
			Status:      api.InputReadinessFieldInvalid,
			Disposition: api.RuleDispositionStrict,
			Message:     "PTP No English Subtitles conflicts with finalized English subtitle evidence",
		}}
	}
	return nil
}
func prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		resolvedAssets, err := trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return trackers.DescriptionResult{}, fmt.Errorf("trackers: %w", err)
			}
			if req.Logger != nil {
				req.Logger.Warnf("trackers: PTP description assets failed: %v", err)
			}
		} else {
			assets = resolvedAssets
		}
	}

	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description = buildDescription(req.Meta, req.TrackerConfig, req.Runtime.DescriptionConfig(), assets)
	}
	if strings.TrimSpace(description) == "" && req.Logger != nil {
		req.Logger.Infof("trackers: PTP preparation description empty")
	}

	return trackers.DescriptionResult{
		Group:       "ptp",
		Description: description,
	}, nil
}

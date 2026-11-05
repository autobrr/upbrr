// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-gpw-constructibility-v2",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 2)
			if strings.TrimSpace(meta.Container) == "" || codecFact(meta) == "" {
				failures = append(
					failures,
					trackers.NewRuleFailure(
						"unsupported_taxonomy",
						"GPW requires finalized codec and container facts, including detail for Other",
						api.RuleDispositionStrict,
					),
				)
			}
			for _, value := range []string{
				resolveCodec(meta),
				resolveContainer(meta),
				resolveProcessing(meta),
				resolveResolution(meta),
				resolveSource(meta),
			} {
				if strings.TrimSpace(value) == "" {
					failures = append(failures, trackers.NewRuleFailure(
						"unsupported_taxonomy",
						"release does not map to GPW upload taxonomy",
						api.RuleDispositionStrict,
					))
					break
				}
			}
			if !standalone.PreparedMediaReady(subject) {
				failures = append(failures, trackers.NewRuleFailure(
					"prepared_media_missing",
					"GPW requires prepared MediaInfo or BDInfo text",
					api.RuleDispositionStrict,
				))
			}
			return failures, nil
		},
	}
}

// validateFields retains remote group-dependent requirements as a deferred
// defensive assertion after the tracker lookup has completed.
func validateFields(groupID string, form map[string][]string) string {
	fields := previewFields(form)
	if groupID == "" {
		for _, key := range []string{"image", "artists[]", "artist_ids[]", "tags[]", "name", "year"} {
			if strings.TrimSpace(fields[key]) == "" {
				return "missing required new-group data"
			}
		}
		switch fields["data_source"] {
		case "imdb":
			id := strings.TrimPrefix(fields["identifier"], "tt")
			if !strings.HasPrefix(fields["identifier"], "tt") || !positiveID(id) {
				return "invalid IMDb source/identifier pair"
			}
		case "tmdb":
			if !positiveID(fields["identifier"]) {
				return "invalid TMDB source/identifier pair"
			}
		case "manual":
			if fields["identifier"] != "" {
				return "manual source cannot use a provider identifier"
			}
		default:
			return "unsupported GPW data source"
		}
	}
	return ""
}

func positiveID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id > 0
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-gpw-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 2)
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
func validateFields(groupID string, fields map[string]string) string {
	if groupID == "" {
		for _, key := range []string{"image", "artists[]", "artist_ids[]", "tags"} {
			if strings.TrimSpace(fields[key]) == "" {
				return "missing required new-group data"
			}
		}
	}
	return ""
}

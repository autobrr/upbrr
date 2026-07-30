// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "unit3d-lst-payload-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			if strings.TrimSpace(subject.Edition) != "" {
				if _, ok := editionID(subject.Edition); !ok {
					return []api.RuleFailure{trackers.NewRuleFailure(
						"unsupported_edition",
						"LST does not support the supplied edition",
						api.RuleDispositionStrict,
					)}, nil
				}
			}
			return nil, nil
		},
	}
}

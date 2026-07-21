// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"

	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func checkDuplicateAssessment(
	ctx context.Context,
	service api.DupeService,
	meta api.DuplicateSubject,
	trackers []string,
	options dupechecking.CheckOptions,
) (api.DupeCheckSummary, dupechecking.Assessment, error) {
	checker, ok := service.(duplicateAssessmentChecker)
	if !ok {
		return api.DupeCheckSummary{}, dupechecking.EmptyAssessment(), errors.New("core: duplicate service does not provide structural assessment")
	}
	summary, assessment, err := checker.CheckWithAssessment(ctx, meta, trackers, options)
	if err != nil {
		return summary, assessment, fmt.Errorf("core: duplicate assessment: %w", err)
	}
	return summary, assessment, nil
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestRetainedPreparationRejectsNilService(t *testing.T) {
	t.Parallel()
	var service *Service
	if _, err := service.PrepareRetainedUploadPlan(t.Context(), api.UploadSubject{SourcePath: "Example.Release.2026"}, nil); err == nil {
		t.Fatal("nil service did not return an error")
	}
}

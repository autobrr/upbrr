// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRDescriptionResourceProducerDefersUnusedAuthority(t *testing.T) {
	module := &Module{}
	state := &State{Workflow: api.ReleaseWorkflow{HDRAnalysisEnabled: true}}
	value, err := module.descriptionResources(t.Context(), "owner", state, "media", time.Now())
	if err != nil {
		t.Fatalf("unused HDR resource was eagerly required: %v", err)
	}
	resources, ok := value.(DescriptionResources)
	if !ok || resources.HDR == nil || resources.Media != "media" {
		t.Fatalf("HDR producer lost deferred authority: %#v", value)
	}
	if _, _, err := resources.HDR(t.Context()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("generated description did not require HDR authority: %v", err)
	}
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildFieldsPersonalReleaseAndExclusiveFlags(t *testing.T) {
	t.Parallel()

	base := api.UploadSubject{PersonalRelease: true}

	nonDisc := buildFields(trackers.PreparationInput{Meta: base}, config.TrackerConfig{Exclusive: true}, "description", "group", nil)
	if nonDisc["diy"] != "on" {
		t.Fatalf("expected non-disc personal release to use diy flag, got %#v", nonDisc)
	}
	if _, ok := nonDisc["self_rip"]; ok {
		t.Fatalf("did not expect legacy self_rip flag, got %#v", nonDisc)
	}
	if nonDisc["jinzhuan"] != "on" {
		t.Fatalf("expected exclusive flag to set jinzhuan, got %#v", nonDisc)
	}

	discMeta := base
	discMeta.DiscType = "BDMV"
	disc := buildFields(trackers.PreparationInput{Meta: discMeta}, config.TrackerConfig{}, "description", "group", nil)
	if disc["buy"] != "on" {
		t.Fatalf("expected disc personal release to use buy flag, got %#v", disc)
	}
	if _, ok := disc["diy"]; ok {
		t.Fatalf("did not expect diy for disc personal release, got %#v", disc)
	}
}

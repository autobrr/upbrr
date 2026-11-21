// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestLogoOrderChangesOnlyPreparationPolicyCompatibility(t *testing.T) {
	initial, err := preparationCompatibility(api.PrepareInput{LogoLanguagePreference: "pt,ja"}, "same-source", 1)
	if err != nil {
		t.Fatal(err)
	}
	next, err := preparationCompatibility(api.PrepareInput{LogoLanguagePreference: "ja,pt"}, "same-source", 1)
	if err != nil {
		t.Fatal(err)
	}
	if initial.PolicyFingerprint == next.PolicyFingerprint || initial.SourceFingerprint != next.SourceFingerprint ||
		initial.FactInstructionFingerprint != next.FactInstructionFingerprint {
		t.Fatalf("artwork custom order must affect only reuse policy: initial=%+v next=%+v", initial, next)
	}
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrimaryReportPolicyRetiresOnlyOldDVDPreparations(t *testing.T) {
	previous, err := preparationCompatibility(api.PrepareInput{}, "same-source", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, media := range []string{"DVD", "BDMV", ""} {
		current, err := preparationCompatibilityForSource(api.PrepareInput{}, "same-source", 2, media)
		if err != nil {
			t.Fatal(err)
		}
		if (current == previous) == (media == "DVD") || current.SourceFingerprint != previous.SourceFingerprint {
			t.Fatalf("%q: prior=%+v current=%+v", media, previous, current)
		}
	}
}

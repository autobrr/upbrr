// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import "testing"

func TestDiscClassification(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"BDMV", " dvd ", "HD DVD", "Blu-ray", "blu_ray"} {
		t.Run(label, func(t *testing.T) {
			if !IsDiscType(label) || !IsFullDisc(label, "") || !IsFullDisc(label, "DISC") {
				t.Fatalf("full disc label %q not recognized", label)
			}
			if IsFullDisc(label, " remux ") {
				t.Fatalf("disc-sourced remux %q exempted", label)
			}
		})
	}
	for _, label := range []string{"", "Web", "unknown"} {
		t.Run(label, func(t *testing.T) {
			if IsDiscType(label) || IsFullDisc(label, "") || IsFullDisc(label, "REMUX") {
				t.Fatalf("non-disc label %q recognized as disc", label)
			}
			if !IsFullDisc(label, " disc ") {
				t.Fatal("canonical DISC without a known layout label not recognized")
			}
		})
	}
}

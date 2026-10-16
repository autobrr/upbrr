// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRSamplingIdentityPreservesAudioAndLegacyFull(t *testing.T) {
	native := releaseworkflow.HDRExtractionRecord{ID: "synthetic-native", SHA256: "synthetic-hash"}
	full := api.HDRDecodedOptions{Automatic: true}
	sampled := full
	sampled.Sampled = true
	for _, mode := range []api.HDRDecodedMode{api.HDRDecodedAutomatic, api.HDRDecodedAudio} {
		a, err := hdrMeasurementIdentity(native, full, mode)
		if err != nil {
			t.Fatal(err)
		}
		b, err := hdrMeasurementIdentity(native, sampled, mode)
		if err != nil {
			t.Fatal(err)
		}
		if (a == b) != (mode == api.HDRDecodedAudio) {
			t.Fatal("sampling identity failed to isolate picture coverage while preserving audio")
		}
	}
	fullAudio, err := hdrMeasurementIdentity(native, full, api.HDRDecodedAudio)
	if err != nil {
		t.Fatal(err)
	}
	onlyAudio, err := hdrMeasurementIdentity(native, api.HDRDecodedOptions{Audio: true}, api.HDRDecodedAudio)
	if err != nil || onlyAudio != fullAudio {
		t.Fatalf("audio-only did not reuse identical audio evidence: %v", err)
	}
}

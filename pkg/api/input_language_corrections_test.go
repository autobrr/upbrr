// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"testing"
)

func TestInputLanguageReviewPatchRetainsOtherTracksAndRevisionAuthority(t *testing.T) {
	snapshot := ReleaseCorrectionsSnapshot{Revision: 4, Corrections: StoredReleaseCorrectionsV1{
		Version: 1,
		Metadata: MetadataOverrides{TrackLanguages: []TrackLanguageCorrection{{
			TrackID:             "subtitle",
			Languages:           []string{"English"},
			ManifestFingerprint: "current",
		}}},
	}}
	patch := ReleaseCorrectionPatch{ExpectedRevision: new(uint64(4)), Values: ReleaseCorrectionValues{
		Metadata: MetadataOverrides{TrackLanguages: []TrackLanguageCorrection{
			{
				TrackID:             "programme-one",
				Languages:           []string{"Japanese"},
				ManifestFingerprint: "current",
			},
			{
				TrackID:             "programme-two",
				Languages:           []string{"Japanese", "English"},
				ManifestFingerprint: "current",
			},
		}},
	}}
	update := ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &patch}
	corrected, err := ApplyReleaseCorrectionUpdate(snapshot, update)
	if err != nil {
		t.Fatal(err)
	}
	if len(corrected.Metadata.TrackLanguages) != 3 || corrected.Metadata.TrackLanguages[0].TrackID != "subtitle" {
		t.Fatalf("source review replaced unrelated correction: %#v", corrected.Metadata.TrackLanguages)
	}
	if len(snapshot.Corrections.Metadata.TrackLanguages) != 1 {
		t.Fatal("patch mutated previous authority")
	}
	snapshot.Corrections = corrected
	repeated, err := ApplyReleaseCorrectionUpdate(snapshot, update)
	if err != nil || len(repeated.Metadata.TrackLanguages) != 3 {
		t.Fatalf("repeated correction accumulated tracks: %v %#v", err, repeated)
	}
	snapshot.Revision++
	_, err = ApplyReleaseCorrectionUpdate(snapshot, update)
	var conflict *CorrectionRevisionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("stale source review did not conflict: %v", err)
	}
}

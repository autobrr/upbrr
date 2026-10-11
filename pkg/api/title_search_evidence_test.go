// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"path/filepath"
	"testing"
)

func TestTitleSearchEvidenceRequiresExactPreparedIdentity(t *testing.T) {
	t.Parallel()
	identity := ExternalIdentity{
		SourcePath: filepath.Join(t.TempDir(), "Example.mkv"),
		Generation: 1,
		Category:   CanonicalCategoryMovie,
		TMDBID:     123,
	}
	fingerprint, err := TitleSearchIdentityFingerprint(identity)
	if err != nil {
		t.Fatal(err)
	}
	evidence := TrackerTitleSearchEvidence{
		Status:            MetadataEvidenceStatusComplete,
		TitleFingerprint:  fingerprint,
		ResultFingerprint: "result",
	}
	if !evidence.Current(identity) {
		t.Fatal("complete matching evidence rejected")
	}
	for _, change := range []func(*ExternalIdentity){
		func(i *ExternalIdentity) { i.Generation++ }, func(i *ExternalIdentity) { i.TMDBID++ }, func(i *ExternalIdentity) { i.Category = CanonicalCategoryTV }, func(i *ExternalIdentity) { i.SourcePath = "" },
	} {
		altered := identity
		change(&altered)
		if evidence.Current(altered) {
			t.Fatal("changed prepared identity accepted")
		}
	}
	for _, status := range []MetadataEvidenceStatus{"", MetadataEvidenceStatusUnavailable, MetadataEvidenceStatusPartial, MetadataEvidenceStatusContradictory} {
		evidence.Status = status
		if evidence.Current(identity) {
			t.Fatalf("status %q accepted", status)
		}
	}
}

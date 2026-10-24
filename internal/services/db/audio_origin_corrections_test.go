// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioOriginCorrectionsPersistIndependentlyAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "origins.db")
	repo, err := OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	_, err = repo.UpdateReleaseCorrections(t.Context(), "source", api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{
			TrackLanguages: []api.TrackLanguageCorrection{{
				TrackID:             "audio",
				Languages:           []string{"Japanese", "English"},
				ManifestFingerprint: "manifest",
			}},
			TrackAudioOrigins: []api.TrackAudioOriginCorrection{{
				TrackID:             "audio",
				Origin:              api.TrackAudioOriginal,
				ManifestFingerprint: "manifest",
			}},
		}}},
	})
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenWithLogger(path, nopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	loaded, err := repo.LoadReleaseCorrections(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Corrections.Metadata.TrackAudioOrigins) != 1 || loaded.Corrections.Metadata.TrackAudioOrigins[0].Origin != api.TrackAudioOriginal || loaded.Corrections.Metadata.TrackAudioOrigins[0].ManifestFingerprint != "manifest" {
		t.Fatalf("lost origin: %#v", loaded)
	}
	updated, err := repo.UpdateReleaseCorrections(t.Context(), "source", api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
		ExpectedRevision: new(loaded.Revision), Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{AlternateTitle: new("Synthetic alternate")}},
	}})
	if err != nil || len(updated.Corrections.Metadata.TrackAudioOrigins) != 1 || len(updated.Corrections.Metadata.TrackLanguages) != 1 {
		t.Fatalf("unrelated edit lost evidence: %#v %v", updated, err)
	}
	reset, err := repo.UpdateReleaseCorrections(t.Context(), "source", api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
		ExpectedRevision: new(updated.Revision), ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataTrackAudioOrigins, TrackID: "audio"}},
	}})
	if err != nil || len(reset.Corrections.Metadata.TrackAudioOrigins) != 0 || len(reset.Corrections.Metadata.TrackLanguages) != 1 || reset.Corrections.Metadata.AlternateTitle == nil {
		t.Fatalf("reset changed independent fields: %#v %v", reset, err)
	}
}

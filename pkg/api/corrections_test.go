// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestReleaseCorrectionPatchRejectsUnknownAndConflictingTargets(t *testing.T) {
	t.Parallel()

	var patch ReleaseCorrectionPatch
	err := json.Unmarshal([]byte(`{"values":{"Metadata":{"AudioLanguages":[]},"unknown":true}}`), &patch)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown correction value error = %v", err)
	}
	err = json.Unmarshal([]byte(`{"values":{"Metadata":{"unknown":true}}}`), &patch)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown correction metadata error = %v", err)
	}

	value := "Example Distributor"
	patch = ReleaseCorrectionPatch{
		Values:      ReleaseCorrectionValues{Metadata: MetadataOverrides{Distributor: &value}},
		ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldMetadataDistributor}},
	}
	if err := patch.Validate(); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("value/reset error = %v", err)
	}
	patch = ReleaseCorrectionPatch{ResetFields: []CorrectionFieldRef{
		{Field: CorrectionFieldMetadataDistributor}, {Field: CorrectionFieldMetadataDistributor},
	}}
	if err := patch.Validate(); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("duplicate reset error = %v", err)
	}
	patch = ReleaseCorrectionPatch{ConfirmFields: []CorrectionFieldRef{{Field: CorrectionFieldMetadataTitle}}}
	if err := patch.Validate(); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("confirmation without revision error = %v", err)
	}
	patch = ReleaseCorrectionPatch{ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldMetadataTrackLanguages}}}
	if err := patch.Validate(); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("track reset without ID error = %v", err)
	}
}

func TestReleaseCorrectionPatchAcceptsClosedResetFields(t *testing.T) {
	t.Parallel()

	refs := make([]CorrectionFieldRef, 0, len(allCorrectionFields))
	for _, field := range allCorrectionFields {
		ref := CorrectionFieldRef{Field: field}
		if field == CorrectionFieldMetadataTrackLanguages {
			ref.TrackID = "audio:1"
		}
		refs = append(refs, ref)
	}
	if err := (ReleaseCorrectionPatch{ResetFields: refs}).Validate(); err != nil {
		t.Fatalf("full reset enum validation: %v", err)
	}
	if len(refs) != 45 {
		t.Fatalf("reset enum count = %d, want 45", len(refs))
	}
}

func TestReleaseCorrectionUpdateReplaceValidatesTrackValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		values ReleaseCorrectionValues
	}{
		{
			name: "blank track ID",
			values: ReleaseCorrectionValues{Metadata: MetadataOverrides{TrackLanguages: []TrackLanguageCorrection{{
				ManifestFingerprint: "scan",
			}}}},
		},
		{
			name: "missing manifest fingerprint",
			values: ReleaseCorrectionValues{Metadata: MetadataOverrides{TrackLanguages: []TrackLanguageCorrection{{
				TrackID: "audio:1",
			}}}},
		},
		{
			name: "duplicate track ID",
			values: ReleaseCorrectionValues{Metadata: MetadataOverrides{TrackLanguages: []TrackLanguageCorrection{
				{TrackID: "audio:1", ManifestFingerprint: "first-scan"},
				{TrackID: "audio:1", ManifestFingerprint: "second-scan"},
			}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{}, ReleaseCorrectionUpdate{
				Mode:   ReleaseCorrectionUpdateReplace,
				Values: test.values,
			})
			if !errors.Is(err, ErrCorrectionConflict) {
				t.Fatalf("replace error = %v", err)
			}
		})
	}

	category := "television"
	trackLanguages := []TrackLanguageCorrection{{
		TrackID:             "subtitle:2",
		Languages:           []string{"French"},
		ManifestFingerprint: "scan",
	}}
	previousTitle := "Previous title"
	stored, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{Corrections: StoredReleaseCorrectionsV1{
		Version:  1,
		Metadata: MetadataOverrides{Title: &previousTitle},
	}}, ReleaseCorrectionUpdate{
		Mode: ReleaseCorrectionUpdateReplace,
		Values: ReleaseCorrectionValues{
			ReleaseName: ReleaseNameOverrides{Category: &category},
			Metadata:    MetadataOverrides{TrackLanguages: trackLanguages},
		},
	})
	if err != nil {
		t.Fatalf("replace valid values: %v", err)
	}
	if stored.Metadata.Title != nil || stored.ReleaseName.Category == nil || *stored.ReleaseName.Category != "tv" ||
		len(stored.Metadata.TrackLanguages) != 1 || stored.Metadata.TrackLanguages[0].TrackID != "subtitle:2" ||
		!slices.Equal(stored.Metadata.TrackLanguages[0].Languages, []string{"French"}) {
		t.Fatalf("replace result = %#v", stored)
	}
	if category != "television" {
		t.Fatalf("replace normalized caller category = %q", category)
	}
	trackLanguages[0].Languages[0] = "German"
	if !slices.Equal(stored.Metadata.TrackLanguages[0].Languages, []string{"French"}) {
		t.Fatalf("replace result aliases caller track languages = %#v", stored.Metadata.TrackLanguages)
	}
}

func TestApplyReleaseCorrectionUpdatePreservesExplicitZeroFalseAndEmpty(t *testing.T) {
	t.Parallel()

	oldLanguages := []string{"English"}
	oldTitle := "Old title"
	stored := ReleaseCorrectionsSnapshot{
		Corrections: StoredReleaseCorrectionsV1{
			Version: 1,
			Metadata: MetadataOverrides{
				Title:          &oldTitle,
				AudioLanguages: &oldLanguages,
				TrackLanguages: []TrackLanguageCorrection{{
					TrackID:             "audio:1",
					Languages:           []string{"English"},
					ManifestFingerprint: "old-manifest",
				}},
			},
		},
		Revision: 4,
	}
	zero := 0
	no := false
	empty := make([]string, 0, 1)
	update := ReleaseCorrectionUpdate{
		Mode: ReleaseCorrectionUpdatePatch,
		Patch: &ReleaseCorrectionPatch{
			Values: ReleaseCorrectionValues{
				Identity: ExternalIDOverrides{TMDBID: &zero},
				Metadata: MetadataOverrides{
					PersonalRelease: &no,
					AudioLanguages:  &empty,
					TrackLanguages: []TrackLanguageCorrection{{
						TrackID:             "audio:1",
						Languages:           []string{},
						ManifestFingerprint: "new-manifest",
					}},
				},
			},
			ExpectedRevision: new(uint64(4)),
		},
	}
	got, err := ApplyReleaseCorrectionUpdate(stored, update)
	if err != nil {
		t.Fatalf("apply update: %v", err)
	}
	if got.Identity.TMDBID == nil || *got.Identity.TMDBID != 0 || got.Metadata.PersonalRelease == nil || *got.Metadata.PersonalRelease ||
		got.Metadata.AudioLanguages == nil || len(*got.Metadata.AudioLanguages) != 0 {
		t.Fatalf("explicit values were not preserved: %#v", got)
	}
	if got.Metadata.TrackLanguages[0].ManifestFingerprint != "new-manifest" || got.Metadata.TrackLanguages[0].Languages == nil ||
		len(got.Metadata.TrackLanguages[0].Languages) != 0 {
		t.Fatalf("track correction = %#v", got.Metadata.TrackLanguages)
	}
	empty = append(empty, "Spanish")
	if len(*got.Metadata.AudioLanguages) != 0 {
		t.Fatal("stored correction aliases caller list")
	}
	if stored.Corrections.Metadata.TrackLanguages[0].ManifestFingerprint != "old-manifest" {
		t.Fatal("stored correction was mutated")
	}
}

func TestApplyReleaseCorrectionUpdateResetAndRevisionConflict(t *testing.T) {
	t.Parallel()

	title := "Manual title"
	tag := "GRP"
	stored := ReleaseCorrectionsSnapshot{
		Corrections: StoredReleaseCorrectionsV1{
			Version:     1,
			ReleaseName: ReleaseNameOverrides{Tag: &tag},
			Metadata: MetadataOverrides{Title: &title, TrackLanguages: []TrackLanguageCorrection{{
				TrackID:             "audio:1",
				Languages:           []string{"English"},
				ManifestFingerprint: "scan",
			}}},
			ContentBindings:    map[CorrectionField]ContentBinding{CorrectionFieldMetadataTitle: {}},
			StaleContentFields: []CorrectionField{CorrectionFieldMetadataTitle},
		},
		Revision: 3,
	}
	got, err := ApplyReleaseCorrectionUpdate(stored, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{
		ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldMetadataTitle}, {Field: CorrectionFieldMetadataTrackLanguages, TrackID: "audio:1"}},
	}})
	if err != nil {
		t.Fatalf("apply reset: %v", err)
	}
	if got.Metadata.Title != nil || got.Metadata.TrackLanguages != nil || got.ReleaseName.Tag == nil || *got.ReleaseName.Tag != "GRP" ||
		len(got.ContentBindings) != 0 || len(got.StaleContentFields) != 0 {
		t.Fatalf("reset result = %#v", got)
	}
	_, err = ApplyReleaseCorrectionUpdate(stored, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{ExpectedRevision: new(uint64(2))}})
	var revisionConflict *CorrectionRevisionConflictError
	if !errors.As(err, &revisionConflict) || !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("revision conflict = %v", err)
	}
	_, err = ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{Corrections: StoredReleaseCorrectionsV1{Version: 2}}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{}})
	var versionError *UnsupportedCorrectionVersionError
	if !errors.As(err, &versionError) || !errors.Is(err, ErrUnsupportedCorrectionVersion) {
		t.Fatalf("unsupported version error = %v", err)
	}
}

func TestReleaseCorrectionValuesFieldsAndWithoutFields(t *testing.T) {
	t.Parallel()

	title := "Manual title"
	genres := []string{}
	values := ReleaseCorrectionValues{Metadata: MetadataOverrides{
		Title:  &title,
		Genres: &genres,
		TrackLanguages: []TrackLanguageCorrection{{
			TrackID:             "subtitle:2",
			Languages:           []string{"French"},
			ManifestFingerprint: "scan",
		}},
	}}
	if got, want := values.Fields(), []CorrectionFieldRef{
		{Field: CorrectionFieldMetadataTitle}, {Field: CorrectionFieldMetadataGenres}, {Field: CorrectionFieldMetadataTrackLanguages, TrackID: "subtitle:2"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %#v, want %#v", got, want)
	}
	without, err := values.WithoutFields([]CorrectionFieldRef{{Field: CorrectionFieldMetadataTitle}, {Field: CorrectionFieldMetadataTrackLanguages, TrackID: "subtitle:2"}})
	if err != nil {
		t.Fatalf("without fields: %v", err)
	}
	if without.Metadata.Title != nil || without.Metadata.TrackLanguages != nil || without.Metadata.Genres == nil || !slices.Equal(*without.Metadata.Genres, []string{}) {
		t.Fatalf("without fields = %#v", without)
	}
}

func TestNormalizeReleaseFactInstructionsCategory(t *testing.T) {
	t.Parallel()

	tv := CanonicalCategoryTV
	television := "television"
	instructions := ReleaseFactInstructions{Category: &tv}
	if err := NormalizeReleaseFactInstructionsCategory(&instructions); err != nil || instructions.ReleaseName.Category == nil || *instructions.ReleaseName.Category != "tv" {
		t.Fatalf("category-only normalization = %#v, %v", instructions, err)
	}
	instructions = ReleaseFactInstructions{Category: &tv, ReleaseName: ReleaseNameOverrides{Category: &television}}
	if err := NormalizeReleaseFactInstructionsCategory(&instructions); err != nil || *instructions.Category != CanonicalCategoryTV {
		t.Fatalf("matching normalization = %#v, %v", instructions, err)
	}
	movie := "movie"
	instructions = ReleaseFactInstructions{Category: &tv, ReleaseName: ReleaseNameOverrides{Category: &movie}}
	if err := NormalizeReleaseFactInstructionsCategory(&instructions); !errors.Is(err, ErrCorrectionConflict) {
		t.Fatalf("category conflict = %v", err)
	}
}

func TestIdentityResetMarkersPersistUntilAnExplicitValue(t *testing.T) {
	t.Parallel()
	snapshot := ReleaseCorrectionsSnapshot{Corrections: StoredReleaseCorrectionsV1{Version: 1}}
	for _, field := range identityCorrectionFields {
		reset, err := ApplyReleaseCorrectionUpdate(snapshot, ReleaseCorrectionUpdate{
			Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{ResetFields: []CorrectionFieldRef{{Field: field}}},
		})
		if err != nil || !slices.Contains(reset.IdentityResetFields, field) {
			t.Fatalf("reset absent %s: %v", field, err)
		}
		snapshot.Corrections = reset
	}
	if len(snapshot.Corrections.IdentityResetFields) != 6 || !slices.IsSorted(snapshot.Corrections.IdentityResetFields) {
		t.Fatal("identity resets are not canonical")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restarted ReleaseCorrectionsSnapshot
	if err := json.Unmarshal(payload, &restarted); err != nil {
		t.Fatal(err)
	}
	retained, err := ApplyReleaseCorrectionUpdate(restarted, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdateInherit})
	if err != nil || !slices.Equal(retained.IdentityResetFields, snapshot.Corrections.IdentityResetFields) {
		t.Fatalf("reset markers did not survive restart: %v", err)
	}
	set, err := ApplyReleaseCorrectionUpdate(restarted, ReleaseCorrectionUpdate{
		Mode: ReleaseCorrectionUpdatePatch,
		Patch: &ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{
			Identity: ExternalIDOverrides{TMDBID: new(0)}, ReleaseName: ReleaseNameOverrides{Category: new("")},
		}},
	})
	if err != nil || slices.Contains(set.IdentityResetFields, CorrectionFieldIdentityTMDB) || slices.Contains(set.IdentityResetFields, CorrectionFieldReleaseNameCategory) || len(set.IdentityResetFields) != 4 {
		t.Fatalf("explicit values did not replace their Auto markers: %v", err)
	}
	if len(restarted.Corrections.IdentityResetFields) != 6 {
		t.Fatal("update mutated the input snapshot")
	}
}

func TestIdentityResetMarkersReplaceAndResetAll(t *testing.T) {
	t.Parallel()
	for _, mode := range []ReleaseCorrectionUpdateMode{ReleaseCorrectionUpdateReplace, ReleaseCorrectionUpdateResetAll} {
		update := ReleaseCorrectionUpdate{Mode: mode}
		if mode == ReleaseCorrectionUpdateReplace {
			update.Values.Identity.IMDBID = new(1234567)
		}
		stored, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{}, update)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range identityCorrectionFields {
			want := mode == ReleaseCorrectionUpdateResetAll || field != CorrectionFieldIdentityIMDB
			if slices.Contains(stored.IdentityResetFields, field) != want {
				t.Fatalf("%s field %s has incorrect reset authority", mode, field)
			}
		}
	}
	for _, fields := range [][]CorrectionField{{"unknown"}, {CorrectionFieldMetadataTitle}, {CorrectionFieldIdentityTMDB}} {
		stored := StoredReleaseCorrectionsV1{Version: 1, IdentityResetFields: fields}
		if fields[0] == CorrectionFieldIdentityTMDB {
			stored.Identity.TMDBID = new(0)
		}
		if _, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{Corrections: stored}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdateInherit}); !errors.Is(err, ErrCorrectionConflict) {
			t.Fatalf("invalid reset marker accepted: %v", err)
		}
	}
}

func TestWithoutResetPinsPreservesOtherIdentityEvidence(t *testing.T) {
	t.Parallel()
	for _, provenance := range []IdentityProvenance{IdentityProvenanceExplicit, IdentityProvenanceProvider, IdentityProvenanceLegacy} {
		original := ExternalIdentity{
			TMDBID:     1234567,
			IMDBID:     2345678,
			Provenance: IdentityProvenanceSet{TMDB: provenance, IMDB: IdentityProvenanceExplicit},
		}
		reset := original.WithoutResetPins([]CorrectionField{CorrectionFieldIdentityTMDB})
		if reset.IMDBID != original.IMDBID || reset.Provenance.IMDB != IdentityProvenanceExplicit || original.TMDBID != 1234567 {
			t.Fatal("reset changed another pin or its input")
		}
		if provenance == IdentityProvenanceExplicit {
			if reset.TMDBID != 0 || reset.Provenance.TMDB != IdentityProvenanceUnknown || reset.Overrides.TMDB != OverrideStateUnset {
				t.Fatal("explicit pin was not released")
			}
		} else if reset.TMDBID != original.TMDBID || reset.Provenance.TMDB != provenance {
			t.Fatal("reset discarded automatic evidence")
		}
	}
}

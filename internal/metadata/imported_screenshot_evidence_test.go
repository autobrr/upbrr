// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestImportedScreenshotHandoffDropsCompetingSourceReceipts(t *testing.T) {
	result := trackerdata.Result{
		TrackerID:                           "42",
		UninspectedImages:                   []api.ImportedScreenshotReference{{RawURL: "https://images.example/first"}},
		ImportedImagePolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
		ImportedImageDescriptionFingerprint: "first-description-images",
	}
	other := result
	other.TrackerID, other.TMDBID = "99", 123456
	other.UninspectedImages = []api.ImportedScreenshotReference{{RawURL: "https://images.example/other"}}
	other.ImportedImageDescriptionFingerprint = "other-description-images"
	service := NewService(&fakeRepo{}, WithTrackerDataLookup(&stubTrackerLookup{results: map[string]trackerdata.Result{"AITHER": result, "BLU": other}}))
	meta := preparationstate.State{
		SourcePath:        filepath.Join(t.TempDir(), "Synthetic.mkv"),
		SourceFingerprint: "source",
		Policy:            preparationstate.CollectionPolicy{KeepImages: true},
	}
	got, err := service.enrichTrackerDataPriority(t.Context(), meta, []string{"AITHER", "BLU"}, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TrackerData) != 2 || got.TrackerData[0].ImportedScreenshotEvidence == nil || got.TrackerData[1].ImportedScreenshotEvidence != nil {
		t.Fatalf("competing source retained acceptance: %+v", got.TrackerData)
	}
}

func TestImportedScreenshotHandoffDoesNotRecoverReceiptFromSourceCache(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Synthetic.mkv")
	stored := api.TrackerMetadata{
		SourcePath:  source,
		Tracker:     "ANT",
		TrackerID:   "42",
		TMDBID:      123456,
		Description: "Release notes",
		ImageURLs:   []string{"https://images.example/full"},
		ImportedScreenshotEvidence: &api.ImportedScreenshotEvidence{
			Tracker:                "ANT",
			TrackerID:              "42",
			PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
			SourceFingerprint:      "source",
			DescriptionFingerprint: "cached",
			References:             []api.ImportedScreenshotReference{{RawURL: "https://images.example/full"}},
		},
	}
	lookup := &stubTrackerLookup{}
	service := NewService(&fakeRepo{trackerMetadata: []api.TrackerMetadata{stored}}, WithTrackerDataLookup(lookup),
		WithConfig(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ANT": {APIKey: "synthetic-key"}}}}),
		WithTrackerRegistry(trackerDataTestRegistry(t)))
	got, err := service.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath:        source,
		SourceFingerprint: "source",
		StoredDataFresh:   true,
		TrackerIDs:        map[string]string{"ant": "42"},
		Policy:            preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TrackerData) != 1 || got.TrackerData[0].ImportedScreenshotEvidence != nil || len(lookup.Calls()) != 0 {
		t.Fatalf("source cache manufactured or fetched receipt: %+v calls=%v", got.TrackerData, lookup.Calls())
	}
}

func TestImportedScreenshotHandoffRequiresCurrentLookupReceipt(t *testing.T) {
	meta := preparationstate.State{SourceFingerprint: "source", Policy: preparationstate.CollectionPolicy{KeepImages: true}}
	record := api.TrackerMetadata{Tracker: "AITHER", TrackerID: "42"}
	result := trackerdata.Result{
		UninspectedImages:                   []api.ImportedScreenshotReference{{RawURL: "https://images.example/full"}},
		ImportedImagePolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
		ImportedImageDescriptionFingerprint: "original-description-images",
	}
	got := importedScreenshotEvidenceFromResult(meta, record, result)
	if got == nil || got.Tracker != record.Tracker || got.SourceFingerprint != meta.SourceFingerprint || len(got.References) != 1 {
		t.Fatal("current receipt lost")
	}
	result.UninspectedImages[0].RawURL = "changed"
	if got.References[0].RawURL == "changed" {
		t.Fatal("receipt aliases lookup result")
	}
	result.ImportedImagePolicyVersion = ""
	if importedScreenshotEvidenceFromResult(meta, record, result) != nil {
		t.Fatal("legacy result manufactured receipt")
	}
	result.ImportedImagePolicyVersion = api.ImportedScreenshotEvidencePolicyVersion
	result.ImportedImageDescriptionFingerprint = ""
	if importedScreenshotEvidenceFromResult(meta, record, result) != nil {
		t.Fatal("missing original description accepted")
	}
}

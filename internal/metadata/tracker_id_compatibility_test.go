// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerIDUpgradeRetainsFailuresAndAssetsAcrossRestart(t *testing.T) {
	for _, test := range []struct {
		name                                                  string
		failed, empty, description, partial, onlyID, filename bool
	}{
		{name: "successful legacy lookup", onlyID: true},
		{
			name:   "empty replacement",
			empty:  true,
			onlyID: true,
		},
		{
			name:   "failed legacy partial",
			failed: true,
			onlyID: true,
		},
		{name: "fresh description without IDs", description: true},
		{name: "current partial IDs", partial: true},
		{name: "current IDs with missing description"},
		{
			name:     "current filename lookup with legacy direct alias",
			onlyID:   true,
			filename: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.sqlite")
			repo, err := db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "Example.Movie.2026-GRP.mkv")
			calls := 0
			lookup := evidenceTrackerLookup(func(context.Context) (trackerdata.Result, error) {
				calls++
				if test.empty {
					return trackerdata.Result{}, nil
				}
				if test.description {
					return trackerdata.Result{TrackerID: "42", Description: "Fresh notes"}, nil
				}
				if test.partial {
					return trackerdata.Result{TrackerID: "42", IMDBID: 12345678}, errors.New("description unavailable")
				}
				return trackerdata.Result{TrackerID: "42", IMDBID: 12345678}, nil
			})
			query := lookup.CacheKey("HDB", "42", api.UploadSubject{}, "", test.onlyID, false)
			ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
			_, err = evidence.Lookup(ctx, "tracker.lookup", query, func() (trackerdata.Result, error) {
				var err error
				if test.failed {
					err = errors.New("synthetic description failure")
				}
				return trackerdata.Result{TrackerID: "42", IMDBID: 1}, err
			}, nil)
			if test.failed != (err != nil) || scope.Err() != nil {
				t.Fatalf("seed legacy evidence: %v, %v", err, scope.Err())
			}
			legacy := api.ExternalIdentity{
				SourcePath: source,
				IMDBID:     1,
				Resolution: api.IdentityResolutionKey{ContractVersion: "external-identity-v3"},
			}
			if err := repo.SaveExternalIdentity(t.Context(), legacy); err != nil {
				t.Fatal(err)
			}
			for _, tracker := range []string{"HDB", "ANT"} {
				if test.filename && tracker == "HDB" {
					continue
				}
				if err := repo.SaveTrackerMetadata(t.Context(), api.TrackerMetadata{
					SourcePath:  source,
					Tracker:     tracker,
					TrackerID:   "42",
					IMDBID:      1,
					Description: "Retained notes",
					ImageURLs:   []string{"https://images.example.invalid/screen.png"},
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			repo, err = db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(repo, WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)),
				WithConfig(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					"HDB": {Username: "synthetic-user", Passkey: "synthetic-key"},
				}}}))
			meta := preparationstate.State{
				SourcePath:       source,
				StoredDataFresh:  true,
				Identity:         legacy,
				TrackerIDs:       map[string]string{"hdb": "42"},
				EvidenceTrackers: []string{"HDB"},
				Policy:           preparationstate.CollectionPolicy{OnlyID: test.onlyID},
			}
			if test.filename {
				meta.TrackerIDs = nil
			}
			ctx, scope = evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
			got, err := service.collectTrackerEvidence(ctx, meta)
			if err != nil || scope.Err() != nil || len(got.TrackerData) != 1 {
				t.Fatalf("upgrade: records=%+v error=%v persistence=%v", got.TrackerData, err, scope.Err())
			}
			wantID, wantCalls := 12345678, 1
			wantDescription, wantImages := "Retained notes", 1
			switch {
			case test.failed:
				wantID, wantCalls = 0, 0
			case test.empty:
				wantID = 0
			case test.description:
				wantID, wantDescription, wantImages = 0, "Fresh notes", 0
			case test.filename:
				wantDescription, wantImages = "", 0
			}
			if got.TrackerData[0].IMDBID != wantID || calls != wantCalls || got.TrackerData[0].Description != wantDescription || len(got.TrackerData[0].ImageURLs) != wantImages {
				t.Fatalf("old IDs revived, assets lost or failure retried: records=%+v calls=%d", got.TrackerData, calls)
			}
			// Preparation can stop after tracker persistence but before canonical publication.
			// The next attempt must retain current exact IDs even while requests are cooling down.
			if err := repo.SaveTrackerTimestamp(t.Context(), api.TrackerTimestamp{Tracker: "HDB", UpdatedAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			meta.TrackerIDs = map[string]string{"hdb": "42"}
			ctx, _ = evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
			retried, err := service.collectTrackerEvidence(ctx, meta)
			if err != nil || len(retried.TrackerData) != 1 || retried.TrackerData[0].IMDBID != wantID || calls != wantCalls {
				t.Fatalf("retry before canonical publication lost current IDs: records=%+v calls=%d err=%v", retried.TrackerData, calls, err)
			}
			// Unselected adapter rows must also be repaired before the identity epoch advances.
			rows, err := repo.ListTrackerMetadataByPath(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Tracker == "ANT" && (row.IMDBID != 0 || row.Description != "Retained notes" || len(row.ImageURLs) != 1) {
					t.Fatalf("unselected legacy tracker row = %+v", row)
				}
			}
			legacy.Resolution.ContractVersion = externalidentity.ContractVersion
			if err := repo.SaveExternalIdentity(t.Context(), legacy); err != nil {
				t.Fatal(err)
			}
			meta.Identity = legacy
			if err := repo.SaveTrackerTimestamp(t.Context(), api.TrackerTimestamp{Tracker: "HDB", UpdatedAt: time.Now().Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
			ctx, _ = evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
			replayed, err := service.collectTrackerEvidence(ctx, meta)
			if test.empty || !test.onlyID && !test.partial && !test.description {
				wantCalls++
			}
			if err != nil || len(replayed.TrackerData) != 1 || replayed.TrackerData[0].IMDBID != wantID || calls != wantCalls {
				t.Fatalf("post-upgrade replay: records=%+v calls=%d err=%v", replayed.TrackerData, calls, err)
			}
		})
	}
}

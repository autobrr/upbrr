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
		name          string
		failed, empty bool
	}{
		{name: "successful legacy lookup"},
		{name: "empty replacement", empty: true},
		{name: "failed legacy partial", failed: true},
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
				return trackerdata.Result{TrackerID: "42", IMDBID: 12345678}, nil
			})
			query := lookup.CacheKey("HDB", "42", api.UploadSubject{}, "", true, false)
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
				SourcePath:      source,
				StoredDataFresh: true,
				Identity:        legacy,
				TrackerIDs:      map[string]string{"hdb": "42"},
				Policy:          preparationstate.CollectionPolicy{OnlyID: true},
			}
			ctx, scope = evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
			got, err := service.collectTrackerEvidence(ctx, meta)
			if err != nil || scope.Err() != nil || len(got.TrackerData) != 1 {
				t.Fatalf("upgrade: records=%+v error=%v persistence=%v", got.TrackerData, err, scope.Err())
			}
			wantID, wantCalls := 12345678, 1
			if test.failed {
				wantID, wantCalls = 0, 0
			} else if test.empty {
				wantID = 0
			}
			if got.TrackerData[0].IMDBID != wantID || calls != wantCalls || got.TrackerData[0].Description != "Retained notes" || len(got.TrackerData[0].ImageURLs) != 1 {
				t.Fatalf("old IDs revived, assets lost or failure retried: records=%+v calls=%d", got.TrackerData, calls)
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
			if test.empty {
				wantCalls++
			}
			if err != nil || len(replayed.TrackerData) != 1 || replayed.TrackerData[0].IMDBID != wantID || calls != wantCalls {
				t.Fatalf("post-upgrade replay: records=%+v calls=%d err=%v", replayed.TrackerData, calls, err)
			}
		})
	}
}

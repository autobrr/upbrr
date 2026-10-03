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
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

type evidenceTrackerLookup func(context.Context) (trackerdata.Result, error)

type keyedEvidenceTrackerLookup struct {
	evidenceTrackerLookup
	key string
}

func (l keyedEvidenceTrackerLookup) CacheKey(tracker, trackerID string, subject api.UploadSubject, searchFileName string, onlyID, keepImages bool) any {
	return []any{l.key, l.evidenceTrackerLookup.CacheKey(tracker, trackerID, subject, searchFileName, onlyID, keepImages)}
}

func TestTrackerEvidenceStoredIDsRespectLookupConfiguration(t *testing.T) {
	t.Parallel()
	repo, source := openProviderEvidenceStore(t)
	calls := 0
	fetch := evidenceTrackerLookup(func(context.Context) (trackerdata.Result, error) {
		calls++
		return trackerdata.Result{
			TrackerID:   "42",
			TMDBID:      calls,
			Description: "Example release notes",
		}, nil
	})
	svc := NewService(repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "metadata.sqlite")}, Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ANT": {APIKey: "synthetic-key"}}}}),
		WithTrackerRegistry(trackerDataTestRegistry(t)), WithTrackerDataLookup(keyedEvidenceTrackerLookup{evidenceTrackerLookup: fetch, key: "first-config"}))
	meta := preparationstate.State{
		SourcePath:      source,
		StoredDataFresh: true,
		TrackerIDs:      map[string]string{"ant": "42"},
		FileList:        []string{source},
	}
	ctx, _ := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
	initial, err := svc.collectTrackerEvidence(ctx, meta)
	if err != nil || len(initial.TrackerData) != 1 {
		t.Fatalf("initial lookup: %+v, %v", initial.TrackerData, err)
	}
	for _, key := range []string{"first-config", "changed-config"} {
		if key == "changed-config" {
			if err := repo.SaveTrackerTimestamp(t.Context(), api.TrackerTimestamp{Tracker: "ANT", UpdatedAt: time.Now().Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
		svc.tracker = keyedEvidenceTrackerLookup{evidenceTrackerLookup: fetch, key: key}
		ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
		got, err := svc.collectTrackerEvidence(ctx, meta)
		want := 1
		if key == "changed-config" {
			want = 2
		}
		if err != nil || scope.Err() != nil || len(got.TrackerData) != 1 || got.TrackerData[0].TMDBID != want || calls != want {
			t.Fatalf("configuration %s: records=%+v calls=%d want=%d error=%v persistence=%v", key, got.TrackerData, calls, want, err, scope.Err())
		}
	}
}

func (f evidenceTrackerLookup) Lookup(ctx context.Context, _ string, _ string, _ api.UploadSubject, _ string, _ bool, _ bool) (trackerdata.Result, error) {
	return f(ctx)
}

func TestTrackerEvidenceRetainsFailuresAndRetriesEmptyDemandOnlyOnLoad(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty description", true: "failed lookup"}[failed], func(t *testing.T) {
			root := t.TempDir()
			repo, err := db.Open(filepath.Join(root, "metadata.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "Example.Movie.2026-GRP.mkv")
			calls := 0
			lookup := evidenceTrackerLookup(func(context.Context) (trackerdata.Result, error) {
				calls++
				if failed {
					return trackerdata.Result{}, errors.New("synthetic lookup failure")
				}
				return trackerdata.Result{TrackerID: "42", TMDBID: 73}, nil
			})
			svc := NewService(repo, WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "metadata.sqlite")}}), WithTrackerDataLookup(lookup))
			meta := preparationstate.State{SourcePath: source, TrackerIDs: map[string]string{"aither": "42"}}
			run := func(mode api.ExternalFreshness) {
				t.Helper()
				ctx, scope := evidence.WithScope(t.Context(), repo, source, "source-fingerprint", mode, nil)
				record, _, _, err := svc.lookupTrackerData(ctx, meta, "AITHER", time.Now())
				if failed && err == nil || !failed && (err != nil || record.TMDBID != 73) {
					t.Fatalf("record=%#v err=%v", record, err)
				}
				if err := scope.Err(); err != nil {
					t.Fatal(err)
				}
			}
			run(api.ExternalFreshnessLoad)
			run(api.ExternalFreshnessReuse)
			if calls != 1 {
				t.Fatalf("ordinary apply made %d calls", calls)
			}
			run(api.ExternalFreshnessLoad)
			want := 2
			if failed {
				want = 1
			}
			if calls != want {
				t.Fatalf("fresh load calls=%d want=%d", calls, want)
			}
			if err := repo.PurgeContentData(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			run(api.ExternalFreshnessLoad)
			if calls != want+1 {
				t.Fatalf("History purge calls=%d", calls)
			}
		})
	}
}

func TestTrackerEvidenceRetainsPartialIDsWithFailedDescription(t *testing.T) {
	root := t.TempDir()
	repo, err := db.Open(filepath.Join(root, "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "Example.Movie.2026-GRP.mkv")
	calls := 0
	svc := NewService(repo, WithTrackerDataLookup(evidenceTrackerLookup(func(context.Context) (trackerdata.Result, error) {
		calls++
		return trackerdata.Result{TrackerID: "42", IMDBID: 1234567}, errors.New("description unavailable")
	})))
	meta := preparationstate.State{SourcePath: source}
	for _, mode := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
		ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", mode, nil)
		result, persistable, hasIDs, err := svc.lookupTrackerData(ctx, meta, "PTP", time.Now())
		if err != nil || !persistable || !hasIDs || result.IMDBID != 1234567 || result.Description != "" {
			t.Fatalf("partial lookup=%#v err=%v", result, err)
		}
		if err := scope.Err(); err != nil {
			t.Fatal(err)
		}
		meta.TrackerIDs = map[string]string{"ptp": "42"}
		meta.InfoHash = "learned-hash"
	}
	if calls != 1 {
		t.Fatalf("failed description attempted %d times", calls)
	}
}

func (f evidenceTrackerLookup) CacheKey(tracker, trackerID string, _ api.UploadSubject, searchFileName string, onlyID, keepImages bool) any {
	if trackerID != "" {
		searchFileName = ""
	}
	return []any{tracker, trackerID, searchFileName, onlyID, keepImages}
}

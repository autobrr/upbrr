// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedTrackerIDsUpgradeBeforeProviderRequestsAndNaming(t *testing.T) {
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv")
	fixture.input.Policy.OnlyID = true
	fixture.input.ExternalFreshness = api.ExternalFreshnessReuse
	fixture.input.Instructions.Category = new(api.CanonicalCategoryMovie)
	fixture.input.Instructions.SourceLookup = ""
	fixture.input.Instructions.TrackerIDs = map[string]string{"hdb": "42"}
	fixture.input.Instructions.Identity.TMDBID = nil
	fixture.input.Instructions.Identity.IMDBID = nil
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: fixture.dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"HDB": {Username: "synthetic-user", Passkey: "synthetic-key"},
		}},
	}
	registry := trackerDataTestRegistry(t)
	lookup := trackerdata.NewClientWithRegistry(cfg, nil, fixture.http, registry)
	baseTransport := fixture.http.Transport
	rawID := 1
	fixture.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch {
		case request.URL.Host == "hdbits.org":
			body = fmt.Sprintf(`{"data":[{"id":42,"imdb":{"id":%d}}]}`, rawID)
		case strings.HasPrefix(request.URL.Path, "/3/find/tt"):
			body = `{"movie_results":[{"id":555001}]}`
		case request.URL.Path == "/3/search/movie":
			body = `{"results":[{"id":555001,"title":"Example Movie","release_date":"2026-01-01"}]}`
		default:
			return baseTransport.RoundTrip(request)
		}
		fixture.mu.Lock()
		fixture.calls[request.URL.Path]++
		fixture.mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
			Request:    request,
		}, nil
	})
	open := func() (*preparedrelease.Module, *db.SQLiteRepository) {
		t.Helper()
		repo, err := db.Open(fixture.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		if err := repo.Migrate(); err != nil {
			t.Fatal(err)
		}
		service := NewService(repo, WithConfig(cfg), WithTrackerRegistry(registry), WithTrackerDataLookup(lookup),
			WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(newSRRDBDetector(fixture.http, "https://scene.example.invalid", "", "")),
			WithTMDBClient(tmdb.NewClient(fixture.http, nil, "synthetic-key")),
			WithIMDBClient(&stubIMDB{infoFn: func(id string) imdb.Info {
				return imdb.Info{
					IMDbID: id,
					Title:  "Example Movie",
					Year:   2026,
				}
			}}),
			WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
		collector, err := preparedrelease.NewEvidenceCollector(service)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := externalidentity.NewWithCandidateSource(repo, collector)
		if err != nil {
			t.Fatal(err)
		}
		module, err := preparedrelease.New(repo, identity, collector)
		if err != nil {
			t.Fatal(err)
		}
		return module, repo
	}
	module, repo := open()
	old := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}).Release
	if old.Identity.IMDBID != 1 {
		t.Fatalf("small valid synthetic ID was not accepted: %+v", old.Identity)
	}
	old.Compatibility.ContractVersion = "prepared-release-v54"
	old.Identity.Resolution.ContractVersion = "external-identity-v3"
	old.Naming.ReleaseName = "Obsolete cached title"
	if err := repo.CommitPreparedRelease(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	query, err := json.Marshal(lookup.CacheKey("HDB", "42", api.UploadSubject{}, filepath.Base(fixture.input.SourcePath), true, false))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(trackerdata.Result{TrackerID: "42", IMDBID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMetadataEvidence(t.Context(), api.MetadataEvidence{
		SourcePath:        fixture.input.SourcePath,
		SourceFingerprint: old.Compatibility.SourceFingerprint,
		Domain:            "tracker.lookup",
		Key:               fmt.Sprintf("%x", sha256.Sum256(query)),
		Outcome:           api.MetadataEvidenceSuccess,
		Payload:           payload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveTrackerTimestamp(t.Context(), api.TrackerTimestamp{Tracker: "HDB", UpdatedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	rawID = 12345678
	module, repo = open()
	updated := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}).Release
	if updated.Generation != old.Generation+1 || updated.Identity.IMDBID != rawID || updated.Identity.TMDBID != 555001 ||
		!strings.Contains(updated.Naming.ReleaseName, "Example Movie") || updated.Identity.Provenance.IMDB != api.IdentityProvenanceTracker {
		t.Fatalf("pre-fix identity or naming survived preparation: %+v", updated)
	}
	if fixture.requests()["/3/find/tt12345678"] != 1 {
		t.Fatalf("corrected tracker ID did not reach the actual provider request: %v", fixture.requests())
	}
	before := fixture.requests()
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	module, repo = open()
	replayed := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}).Release
	display, err := preparedrelease.ProjectDisplay(replayed)
	if err != nil || replayed.Generation != updated.Generation || replayed.Identity.IMDBID != rawID || display.ReleaseName != updated.Naming.ReleaseName {
		t.Fatalf("corrected prepared generation did not survive restart: %+v, %v", replayed, err)
	}
	fixture.assertRequests(t, before, nil)
	for _, id := range []int{1, 0} {
		fixture.input.Instructions.Identity.IMDBID = new(id)
		changed := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}).Release
		if changed.Identity.IMDBID != id || changed.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit {
			t.Fatalf("manual IMDb %d lost to tracker evidence: %+v", id, changed.Identity)
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	module, repo = open()
	stored, err := repo.GetExternalIdentity(t.Context(), fixture.input.SourcePath)
	if err != nil || stored.IMDBID != 0 || stored.Overrides.IMDB != api.OverrideStateClear || stored.Resolution.ContractVersion != externalidentity.ContractVersion {
		t.Fatalf("persisted clear or identity compatibility lost on readback: %+v, %v", stored, err)
	}
	fixture.input.Instructions.Identity.IMDBID = nil
	fixture.input.Force = true
	cleared := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}).Release
	if cleared.Identity.IMDBID != 0 || cleared.Identity.Overrides.IMDB != api.OverrideStateClear {
		t.Fatalf("explicit clear did not survive full recollection after restart: %+v", cleared.Identity)
	}
}

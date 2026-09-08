// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSelectExternalFindResultRequiresAllSuppliedIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		imdb             FindResponse
		tvdb             FindResponse
		hasIMDb          bool
		hasTVDB          bool
		requireAgreement bool
		wantID           int
		wantConflict     bool
	}{
		{
			name:    "IMDb only",
			imdb:    FindResponse{TVResults: []FindItem{{ID: 111}}},
			hasIMDb: true,
			wantID:  111,
		},
		{
			name:    "TVDB only",
			tvdb:    FindResponse{TVResults: []FindItem{{ID: 222}}},
			hasTVDB: true,
			wantID:  222,
		},
		{
			name:             "matching IMDb and TVDB",
			imdb:             FindResponse{TVResults: []FindItem{{ID: 333}}},
			tvdb:             FindResponse{TVResults: []FindItem{{ID: 333}}},
			hasIMDb:          true,
			hasTVDB:          true,
			requireAgreement: true,
			wantID:           333,
		},
		{
			name:             "conflicting IMDb and TVDB",
			imdb:             FindResponse{TVResults: []FindItem{{ID: 444}}},
			tvdb:             FindResponse{TVResults: []FindItem{{ID: 555}}},
			hasIMDb:          true,
			hasTVDB:          true,
			requireAgreement: true,
			wantConflict:     true,
		},
		{
			name:             "missing TVDB evidence is unverified",
			imdb:             FindResponse{TVResults: []FindItem{{ID: 666}}},
			hasIMDb:          true,
			hasTVDB:          true,
			requireAgreement: true,
		},
		{
			name:    "legacy IMDb-first selection ignores conflicting TVDB",
			imdb:    FindResponse{TVResults: []FindItem{{ID: 777}}},
			tvdb:    FindResponse{TVResults: []FindItem{{ID: 888}}},
			hasIMDb: true,
			hasTVDB: true,
			wantID:  777,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, conflict := selectExternalFindResult(tc.imdb, tc.tvdb, tc.hasIMDb, tc.hasTVDB, "TV", tc.requireAgreement)
			if result.TMDBID != tc.wantID || conflict != tc.wantConflict {
				t.Fatalf("result=%#v conflict=%t, want id=%d conflict=%t", result, conflict, tc.wantID, tc.wantConflict)
			}
		})
	}
}

func TestFindByExternalIDWithoutAgreementSkipsTVDBAfterIMDbMatch(t *testing.T) {
	t.Parallel()
	var tvdbRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/find/tt1234567":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"movie_results":[],"tv_results":[{"id":321,"original_language":"en"}]}`))
		case "/find/987654":
			tvdbRequests.Add(1)
			http.Error(w, "unexpected TVDB request", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.Client(), nil, "test-key")
	client.baseURL = server.URL

	result, err := client.FindByExternalID(context.Background(), FindInput{
		IMDbID:             "tt1234567",
		TVDBID:             987654,
		CategoryPreference: "TV",
	})
	if err != nil {
		t.Fatalf("find by external ID: %v", err)
	}
	if result.TMDBID != 321 || result.Category != "TV" || result.FilenameSearch || result.ExternalIDConflict {
		t.Fatalf("IMDb-first result = %#v", result)
	}
	if got := tvdbRequests.Load(); got != 0 {
		t.Fatalf("TVDB requests = %d, want none after an IMDb match", got)
	}
}

func TestApplyExternalIDsRetainsReturnedReferences(t *testing.T) {
	t.Parallel()
	result := applyExternalIDs(MetadataResult{}, externalIDsResponse{
		IMDbID: "tt7654321",
		TVDBID: 654321,
	}, MetadataInput{
		IMDbID: 1234567,
		TVDBID: 234567,
	}, mediaResponse{})

	if result.IMDbID != 1234567 || result.TVDBID != 234567 {
		t.Fatalf("effective IDs = IMDb:%d TVDB:%d", result.IMDbID, result.TVDBID)
	}
	if result.ExternalIMDbID != 7654321 || result.ExternalTVDBID != 654321 {
		t.Fatalf("returned IDs = IMDb:%d TVDB:%d", result.ExternalIMDbID, result.ExternalTVDBID)
	}
}

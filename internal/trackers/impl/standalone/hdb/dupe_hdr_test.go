// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBAPIHDRSlots(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: hdbRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"status":0,"data":[
				{"id":1,"name":"Example Series S01E04 2160p WEB-DL DoVi HEVC-NTb","tags":["Dolby Vision"]},
				{"id":2,"name":"Example Series S01E04 2160p WEB-DL DoVi HDR10+ HEVC-NTb","tags":["Dolby Vision","HDR10","HDR10+"]},
				{"id":3,"name":"Example Series S01E04 2160p WEB-DL HEVC-NTb","tags":[]},
				{"id":4,"name":"Example Series S01E04 2160p WEB-DL HDR HEVC-NTb","tags":["HDR10"]},
				{"id":5,"name":"Example Series S01E04 2160p WEB-DL HEVC-NTb"},
				{"id":6,"name":"Example Series S01E04 2160p WEB-DL HEVC-NTb","tags":null},
				{"id":7,"name":"Example Series S01E04 2160p WEB-DL HEVC-NTb","tags":""},
				{"id":8,"name":"Example Series S01E04 2160p WEB-DL HEVC-NTb","tags":"HDR10"},
				{"id":9,"name":"Example Series S01E04 2160p WEB-DL HEVC-NTb","tags":[""]},
				{"id":10,"name":"Example Series S01E04 2160p WEB-DL HEVC-NTb","tags":[" "]}
			]}`)),
		}, nil
	})}
	adapter := dupe.NewAdapter(New(), "HDB", config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"HDB": {Username: "user", Passkey: "pk"},
		}},
	}, client, &hdbCaptureLogger{})
	search := adapter.Search(t.Context(), api.DuplicateSubject{
		SourcePath: t.TempDir(),
		Identity:   api.ExternalIdentity{IMDBID: 1234567, Category: api.CanonicalCategoryTV},
	})
	if search.Cause() != nil || len(search.Entries()) != 10 {
		t.Fatalf("HDB search = %#v", search)
	}
	target := api.TrackerDuplicateTarget{
		Names:      []string{"Example.Series.S01E04.DV.HDR.2160p.WEB.H265-CAKES"},
		Type:       "WEBDL",
		Source:     "WEB",
		Resolution: "2160p",
		VideoCodec: "H.265",
		Season:     1,
		Episode:    4,
		HDR: api.HDRFacts{
			Status:  api.HDREvidenceComplete,
			Origin:  api.HDREvidenceMediaInfo,
			Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		},
	}
	for index, entry := range search.Entries() {
		candidate := dupe.NormalizeCandidate(entry, "HDB")
		result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy,
			dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
		want := api.DupeRelationCoexists
		if index == 1 || index >= 4 {
			want = api.DupeRelationSameSlot
		}
		if got := result.Candidates[0].Relation; got != want || result.RequiresAction != (want == api.DupeRelationSameSlot) {
			t.Fatalf("id=%s HDR slot evaluation = %#v, want %s", entry.ID, result, want)
		}
		if index == 1 && result.Candidates[0].Facts.HDR.Status != api.HDREvidenceComplete {
			t.Fatalf("HDR10 fallback created a false contradiction: %#v", result.Candidates[0].Facts.HDR)
		}
		if index >= 4 && entry.HDR.Status != api.HDREvidenceMissing {
			t.Fatalf("id=%s: absent tags manufactured HDR evidence: %#v", entry.ID, entry.HDR)
		}
	}
}

func TestHDBMalformedTagsDoNotEstablishSDR(t *testing.T) {
	t.Parallel()
	for _, value := range []any{
		nil, "", "HDR10", 1, map[string]any{},
		[]any{"Dolby Vision", 42}, []any{""}, []any{" "}, []any{"Dolby Vision", ""},
		[]string{""}, []string{" "}, []string{"Dolby Vision", ""},
	} {
		if tags, present := hdbTags(map[string]any{"tags": value}); present {
			t.Fatalf("malformed tags %#v accepted as complete evidence: %#v", value, tags)
		}
	}
}

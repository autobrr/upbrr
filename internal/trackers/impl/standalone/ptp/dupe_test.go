// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type ptpRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ptpRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestPTPHandlerReturnsCompleteExactGroup(t *testing.T) {
	t.Parallel()

	requests := 0
	client := &http.Client{Transport: ptpRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Header.Get("Apiuser") != "api-user" || req.Header.Get("Apikey") != "api-key" {
			t.Fatal("PTP API credentials missing from request headers")
		}
		fixture := ""
		switch requests {
		case 1:
			if req.URL.Query().Get("imdb") != "1234567" || req.URL.Query().Get("json") != "noredirect" {
				t.Fatalf("IMDb query = %q", req.URL.RawQuery)
			}
			fixture = "imdb_group.json"
		case 2:
			if req.URL.Query().Get("id") != "700" || req.URL.Query().Get("json") != "1" || req.URL.Query().Get("jsontrumpable") != "1" {
				t.Fatalf("group query = %q", req.URL.RawQuery)
			}
			fixture = "torrent_group_variants.json"
		default:
			t.Fatalf("unexpected PTP request %d", requests)
		}
		body, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatalf("read PTP fixture: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Header:     make(http.Header),
		}, nil
	})}

	handler := dupe.NewAdapter(New(), "PTP", config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "api-user", PTPAPIKey: "api-key"},
		},
	}}, client, api.NopLogger{})
	result := handler.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 1234567, Category: "MOVIE"},
	})
	if result.Disposition() != dupe.DispositionResolved || result.Cause() != nil {
		t.Fatalf("PTP result disposition=%v error=%v", result.Disposition(), result.Cause())
	}
	search := result.SearchEvidence()
	if !search.Complete || search.Pages != 1 || search.Scope != "work_identity" || len(search.Warnings) != 0 {
		t.Fatalf("PTP search evidence = %#v", search)
	}
	entries := result.Entries()
	if len(entries) != 5 {
		t.Fatalf("PTP entries = %d, want all five qualities", len(entries))
	}
	if entries[0].Type != "DISC" || entries[0].Res != "480i" || entries[0].Source != "DVD" ||
		entries[0].Container != "VOB IFO" || !entries[0].SizeKnown || entries[0].SizeBytes != 4700000000 {
		t.Fatalf("PTP DVD mapping = %#v", entries[0])
	}
	if entries[1].Edition != "" {
		t.Fatalf("generic remaster became cut identity: %#v", entries[1])
	}
	if !entries[1].Trumpable {
		t.Fatalf("PTP trumpable mapping = %#v", entries[1])
	}
	if entries[0].HDR.Status != api.HDREvidenceComplete || len(entries[0].HDR.Formats) != 1 || entries[0].HDR.Formats[0] != api.HDRFormatSDR {
		t.Fatalf("PTP omitted RemasterTitle HDR evidence = %#v", entries[0].HDR)
	}
	if entries[1].HDR.Status != api.HDREvidenceComplete || len(entries[1].HDR.Formats) != 1 || entries[1].HDR.Formats[0] != api.HDRFormatSDR {
		t.Fatalf("PTP non-HDR RemasterTitle evidence = %#v", entries[1].HDR)
	}
	if entries[3].Edition != "directors_cut" {
		t.Fatalf("explicit cut mapping = %#v", entries[3])
	}
	if entries[3].HDR.Status != api.HDREvidenceComplete || len(entries[3].HDR.Formats) != 1 || entries[3].HDR.Formats[0] != api.HDRFormatHDR10 {
		t.Fatalf("PTP HDR10 RemasterTitle evidence = %#v", entries[3].HDR)
	}
	if entries[4].HDR.Status != api.HDREvidenceComplete || entries[4].HDR.DolbyVisionProfile != "5" {
		t.Fatalf("PTP DV title evidence = %#v", entries[4].HDR)
	}
}

func TestPTPEmptyIMDbLookupIsComplete(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: ptpRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"TotalResults":"0","Movies":[],"Page":"1"}`)),
			Header:     make(http.Header),
		}, nil
	})}
	handler := dupe.NewAdapter(New(), "PTP", config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "api-user", PTPAPIKey: "api-key"},
		},
	}}, client, api.NopLogger{})
	result := handler.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	if search := result.SearchEvidence(); !search.Complete || len(result.Entries()) != 0 {
		t.Fatalf("empty PTP search = %#v entries=%#v", search, result.Entries())
	}
}

func TestPTPMalformedGroupPayloadFails(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: ptpRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"Movies":[{}]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	handler := dupe.NewAdapter(New(), "PTP", config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "api-user", PTPAPIKey: "api-key"},
		},
	}}, client, api.NopLogger{})
	result := handler.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	if result.Disposition() != dupe.DispositionFailed || result.Code() != dupe.FailureResponseParse {
		t.Fatalf("malformed PTP group disposition=%v code=%q", result.Disposition(), result.Code())
	}
}

func TestPTPMalformedTorrentPayloadFails(t *testing.T) {
	t.Parallel()

	requests := 0
	client := &http.Client{Transport: ptpRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		body := `{"Movies":[{"GroupId":"700"}]}`
		if requests == 2 {
			body = `{"Torrents":[{}]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	handler := dupe.NewAdapter(New(), "PTP", config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "api-user", PTPAPIKey: "api-key"},
		},
	}}, client, api.NopLogger{})
	result := handler.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	if result.Disposition() != dupe.DispositionFailed || result.Code() != dupe.FailureResponseParse {
		t.Fatalf("malformed PTP torrent disposition=%v code=%q", result.Disposition(), result.Code())
	}
}

func TestPTPSetCapacityFamilies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		resolution     string
		targetCodec    string
		candidateCodec string
		hdr            api.HDRFacts
		targetSize     int64
		candidateSize  int64
		want           api.DupeRelation
	}{
		{
			name:           "SD x264 40 percent",
			resolution:     "480p",
			targetCodec:    "x264",
			candidateCodec: "x264",
			hdr:            ptpCompleteHDR(api.HDRFormatSDR),
			targetSize:     100,
			candidateSize:  60,
			want:           api.DupeRelationCoexists,
		},
		{
			name:           "720p x264 20 percent",
			resolution:     "720p",
			targetCodec:    "x264",
			candidateCodec: "x264",
			hdr:            ptpCompleteHDR(api.HDRFormatSDR),
			targetSize:     100,
			candidateSize:  80,
			want:           api.DupeRelationCoexists,
		},
		{
			name:           "1080p x264 20 percent",
			resolution:     "1080p",
			targetCodec:    "x264",
			candidateCodec: "x264",
			hdr:            ptpCompleteHDR(api.HDRFormatSDR),
			targetSize:     100,
			candidateSize:  80,
			want:           api.DupeRelationCoexists,
		},
		{
			name:           "2160p x265 HDR 20 percent",
			resolution:     "2160p",
			targetCodec:    "x265",
			candidateCodec: "x265",
			hdr:            ptpCompleteHDR(api.HDRFormatHDR10),
			targetSize:     100,
			candidateSize:  80,
			want:           api.DupeRelationCoexists,
		},
		{
			name:           "2160p SDR independent capacity",
			resolution:     "2160p",
			targetCodec:    "x264",
			candidateCodec: "x265",
			hdr:            ptpCompleteHDR(api.HDRFormatSDR),
			targetSize:     100,
			candidateSize:  80,
			want:           api.DupeRelationCoexists,
		},
		{
			name:           "HD sourced 576p single slot",
			resolution:     "576p",
			targetCodec:    "x264",
			candidateCodec: "x264",
			hdr:            ptpCompleteHDR(api.HDRFormatSDR),
			targetSize:     100,
			candidateSize:  60,
			want:           api.DupeRelationManualReview,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := dupe.Evaluate(
				api.TrackerDuplicateTarget{
					Type:        "ENCODE",
					Source:      "BluRay",
					Resolution:  test.resolution,
					VideoEncode: test.targetCodec,
					HDR:         test.hdr,
					SizeBytes:   test.targetSize,
				},
				[]dupe.TrackerCandidate{{
					ID:         "candidate-1",
					Type:       "ENCODE",
					Source:     "BluRay",
					Resolution: test.resolution,
					Codec:      test.candidateCodec,
					HDR:        test.hdr,
					SizeBytes:  test.candidateSize,
					SizeKnown:  true,
				}},
				*duplicatePolicy(),
				dupe.SearchEvidence{Complete: true},
			).Candidates[0]
			if got.Relation != test.want {
				t.Fatalf("PTP set relation = %#v, want %s", got, test.want)
			}
		})
	}
}

func TestPTPStructuredExactIdentity(t *testing.T) {
	t.Parallel()

	sdr := ptpCompleteHDR(api.HDRFormatSDR)
	target := api.TrackerDuplicateTarget{
		Names:       []string{"Example.Release.2026.Generated-GRP"},
		Type:        "ENCODE",
		Source:      "BluRay",
		Resolution:  "1080p",
		Container:   "MKV",
		VideoCodec:  "H.264",
		VideoEncode: "x264",
		HDR:         sdr,
		Group:       "GRP",
		SizeBytes:   8_000_000_000,
	}
	candidate := dupe.TrackerCandidate{
		ID:         "candidate-1",
		Name:       "Example.Release.2026.Original-GRP",
		Type:       "ENCODE",
		Source:     "BluRay",
		Resolution: "1080p",
		Codec:      "H.264",
		Container:  "MKV",
		HDR:        sdr,
		Group:      "GRP",
		SizeBytes:  8_000_000_000,
		SizeKnown:  true,
	}

	got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true}).Candidates[0]
	if got.Relation != api.DupeRelationExactDuplicate || got.WinningRule != "ptp/duplicate/v2/structured_exact_identity" {
		t.Fatalf("PTP structured exact identity = %#v", got)
	}

	candidate.SizeBytes--
	got = dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true}).Candidates[0]
	if got.Relation == api.DupeRelationExactDuplicate {
		t.Fatalf("PTP mismatched size classified exact: %#v", got)
	}
}

func TestPTPSDSetCapacityDropsWhenHigherAlternativeExists(t *testing.T) {
	t.Parallel()

	sdr := ptpCompleteHDR(api.HDRFormatSDR)
	got := dupe.Evaluate(
		api.TrackerDuplicateTarget{
			Type:        "ENCODE",
			Source:      "DVD",
			Resolution:  "480p",
			VideoEncode: "x264",
			HDR:         sdr,
			SizeBytes:   100,
		},
		[]dupe.TrackerCandidate{
			{
				ID:         "sd",
				Type:       "ENCODE",
				Source:     "DVD",
				Resolution: "480p",
				Codec:      "x264",
				HDR:        sdr,
				SizeBytes:  60,
				SizeKnown:  true,
			},
			{
				ID:         "hd",
				Type:       "REMUX",
				Source:     "BluRay",
				Resolution: "1080p",
				Codec:      "H.264",
				HDR:        sdr,
				SizeBytes:  200,
				SizeKnown:  true,
			},
		},
		*duplicatePolicy(),
		dupe.SearchEvidence{Complete: true},
	)
	if got.SetFindings[0].Capacity != 1 || got.Candidates[0].Relation != api.DupeRelationManualReview {
		t.Fatalf("PTP SD capacity override = %#v", got)
	}
}

func ptpCompleteHDR(formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: formats,
		Origin:  api.HDREvidenceTrackerAPI,
		Status:  api.HDREvidenceComplete,
	}
}

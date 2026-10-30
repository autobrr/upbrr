// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestANTCandidateIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, guid, want string
	}{
		{"torrent details", "https://anthelion.me/torrents.php?torrentid=42", "42"},
		{"group and torrent", "https://anthelion.me/torrents.php?id=7&amp;torrentid=42#torrent42", "42"},
		{"missing", "", ""},
		{"group only", "https://anthelion.me/torrents.php?id=42", ""},
		{"invalid ID", "https://anthelion.me/torrents.php?torrentid=42token", ""},
		{"zero ID", "https://anthelion.me/torrents.php?torrentid=0", ""},
		{"negative ID", "https://anthelion.me/torrents.php?torrentid=-42", ""},
		{"repeated ID", "https://anthelion.me/torrents.php?torrentid=42&torrentid=43", ""},
		{"malformed query", "https://anthelion.me/torrents.php?torrentid=42&key=%zz", ""},
		{"foreign host", "https://anthelion.me.example.invalid/torrents.php?torrentid=42", ""},
		{"insecure URL", "http://anthelion.me/torrents.php?torrentid=42", ""},
		{"URL credentials", "https://user:secret@anthelion.me/torrents.php?torrentid=42", ""},
		{"download URL", "https://anthelion.me/download.php?torrentid=42", ""},
		{"relative URL", "/torrents.php?torrentid=42", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
				"fileName": "Example.2026.1080p.BluRay.m2ts.999.torrent",
				"guid":     tc.guid,
			}}})
			if len(entries) != 1 || entries[0].ID != tc.want {
				t.Fatalf("candidate identity: entries=%+v want=%q", entries, tc.want)
			}
			if got := dupe.NormalizeCandidate(entries[0], "ANT").ID; got != tc.want {
				t.Fatalf("normalized ID=%q want=%q", got, tc.want)
			}
		})
	}
}

type antCandidateLogger struct {
	api.NopLogger
	trace strings.Builder
}

func (l *antCandidateLogger) Tracef(format string, args ...any) {
	fmt.Fprintf(&l.trace, format, args...)
	l.trace.WriteByte('\n')
}

func TestDupeSearcherSendsAPIKeyHeader(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		if query.Get("apikey") != "" {
			t.Fatal("apikey should not be sent as a query parameter")
		}
		for key, want := range map[string]string{
			"t":      "search",
			"o":      "json",
			"imdbid": "0000456",
			"limit":  "100",
		} {
			if got := query.Get(key); got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		if got := req.Header.Get("X-Api-Key"); got != "token" {
			t.Fatal("unexpected X-API-Key header")
		}
		if req.Header.Get("User-Agent") == "" {
			t.Fatal("expected User-Agent header")
		}
		body := `{"item":[{"fileName":"Example.Release.2026.1080p-GRP","resolution":"1080p","guid":"https://example.invalid/torrents.php?id=1","link":"https://example.invalid/download.php?id=1"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ANT": {APIKey: "token"}}}}
	searcher := dupe.NewAdapter(New(), "ANT", cfg, client, api.NopLogger{})
	entries, notes, err := adapterEvidence(searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 456},
		Release:  api.ReleaseInfo{Resolution: "1080p"},
	}))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(notes) != 0 || len(entries) != 1 {
		t.Fatalf("entries=%#v notes=%#v", entries, notes)
	}
}

func TestDupeSearcherConsumesANTOffsetPages(t *testing.T) {
	for _, tc := range []struct {
		name              string
		includePagination bool
	}{
		{name: "advertised total", includePagination: true},
		{name: "page capacity fallback", includePagination: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var offsets []string
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				offsets = append(offsets, req.URL.Query().Get("offset"))
				var body string
				switch len(offsets) {
				case 1:
					body = antSearchPageJSON(t, 0, 101, 100, tc.includePagination)
				case 2:
					body = antSearchPageJSON(t, 100, 101, 1, tc.includePagination)
				default:
					t.Fatalf("unexpected request count: %d", len(offsets))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			})}

			cfg := antDupeTestConfig()
			searcher := dupe.NewAdapter(New(), "ANT", cfg, client, api.NopLogger{})
			result := searcher.Search(
				context.Background(),
				api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}},
			)
			if result.Cause() != nil {
				t.Fatalf("search: %v", result.Cause())
			}
			evidence := result.SearchEvidence()
			if !evidence.Complete || evidence.Pages != 2 {
				t.Fatalf("search evidence = %#v", evidence)
			}
			entries := result.Entries()
			if len(entries) != 101 || entries[100].Name != "Example.Release.100" {
				t.Fatalf("paginated entries = %d last=%#v", len(entries), entries[len(entries)-1])
			}
			if len(offsets) != 2 || offsets[0] != "" || offsets[1] != "100" {
				t.Fatalf("requested offsets = %#v", offsets)
			}
		})
	}
}

func TestANTExactPageTotalRetainsStaffTokenBoundary(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		name          string
		total, offset int
		complete      bool
	}{
		{
			name:     "exact cumulative total",
			total:    100,
			offset:   100,
			complete: true,
		},
		{
			name:     "empty page total",
			total:    0,
			offset:   100,
			complete: true,
		},
		{
			name:   "decreased total",
			total:  99,
			offset: 100,
		},
		{
			name:   "unseen result",
			total:  101,
			offset: 100,
		},
		{
			name:   "incorrect offset",
			total:  100,
			offset: 99,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := antSearchPageJSON(t, 0, 100, 100, true)
			last := antSearchPageJSON(t, tc.offset, tc.total, 0, true)
			requests := 0
			http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				body := first
				if requests > 1 {
					body = last
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})
			service := dupe.NewServiceWithRegistry(antDupeTestConfig(), api.NopLogger{}, registry)
			summary, err := service.Check(t.Context(), api.DuplicateSubject{
				SourcePath: "proposed.mkv",
				Identity:   api.ExternalIdentity{TMDBID: 123},
				Projection: &api.TrackerReleaseProjection{
					DuplicateTarget: api.TrackerDuplicateTarget{TrackerSlot: "Encode/1080/SDR//", Resolution: "1080p"},
				},
			}, []string{"ANT"})
			if err != nil || len(summary.Results) != 1 {
				t.Fatalf("search failed: summary=%+v err=%v", summary, err)
			}
			result := summary.Results[0]
			if requests != 2 || result.Search.CandidateCount != 100 || result.Search.Complete != tc.complete || result.RequiresStaffToken != !tc.complete {
				t.Fatalf("wrong exact-page result: requests=%d result=%+v", requests, result)
			}
		})
	}
}

func TestDupeSearcherANTPaginationBoundFailsClosed(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(antSearchPageJSON(t, 0, 101, 100, true))),
			Header:     make(http.Header),
		}, nil
	})}
	searcher := &dupeSearcher{
		cfg:      antDupeTestConfig(),
		http:     client,
		endpoint: "https://example.invalid/api.php",
		maxPages: 1,
	}
	result := searcher.Search(
		context.Background(),
		api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}},
	)
	evidence := result.SearchEvidence()
	if evidence.Complete || evidence.Pages != 1 || len(evidence.Warnings) != 1 {
		t.Fatalf("bounded search evidence = %#v", evidence)
	}
	if len(result.Entries()) != 100 || len(result.Notes()) != 1 {
		t.Fatalf("bounded result entries=%d notes=%#v", len(result.Entries()), result.Notes())
	}
}

func TestDupeSearcherANTTotalChangeFailsClosed(t *testing.T) {
	t.Parallel()

	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		total := 101
		count := 100
		if requests == 2 {
			total = 102
			count = 1
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(antSearchPageJSON(t, (requests-1)*100, total, count, true))),
			Header:     make(http.Header),
		}, nil
	})}
	searcher := dupe.NewAdapter(New(), "ANT", antDupeTestConfig(), client, api.NopLogger{})
	result := searcher.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}})
	if search := result.SearchEvidence(); search.Complete || search.Pages != 2 || len(search.Warnings) != 1 || len(result.Entries()) != 101 {
		t.Fatalf("changed total search=%#v entries=%d", search, len(result.Entries()))
	}
}

func TestDupeSearcherMissingCredentialsSkips(t *testing.T) {
	t.Parallel()
	searcher := dupe.NewAdapter(New(), "ANT", config.Config{}, http.DefaultClient, api.NopLogger{})
	result := searcher.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 1}})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingCredentials || result.SafeMessage() == "" {
		t.Fatalf("unexpected result disposition=%v code=%q message=%q", result.Disposition(), result.Code(), result.SafeMessage())
	}
}

func TestANTFullDiscCountriesAndSlots(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{
		TrackerSlot: "FullDisc/1080/BluRay/JP/",
		Type:        "DISC",
		Source:      "Blu-ray",
		Resolution:  "1080p",
		VideoCodec:  "AVC",
		Group:       "GRP",
		SizeBytes:   1000,
	}
	entry := api.DupeEntry{
		TrackerSlot:   "FullDisc/1080/BluRay/JP/",
		Name:          "Example.Release.2026.1080p.Blu-ray.AVC-GRP",
		CanonicalType: "DISC",
		Source:        "Blu-ray",
		Res:           "1080p",
		Codec:         "AVC",
		Container:     "m2ts",
		Group:         "GRP",
		SizeBytes:     1000,
		SizeKnown:     true,
	}
	policy := *Profile().DupePolicy
	result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entry, "ANT")}, policy, dupe.SearchEvidence{Complete: true})
	if got := result.Candidates[0].Relation; got != api.DupeRelationSameSlot {
		t.Fatalf("same ANT full disc relation = %q", got)
	}

	entry.Group = "OTHER"
	entry.SizeBytes = 900
	result = dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entry, "ANT")}, policy, dupe.SearchEvidence{Complete: true})
	if got := result.Candidates[0].Relation; got != api.DupeRelationSameSlot {
		t.Fatalf("second ANT full disc relation = %q", got)
	}
}

func TestANTCandidatePreservesDiscFields(t *testing.T) {
	t.Parallel()

	entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
		"title":        "Example Release 2026",
		"fileName":     "generated.123.torrent",
		"container":    "m2ts",
		"releaseGroup": "GRP",
		"flags":        []any{"HDR10"},
		"files": []any{
			map[string]any{"name": "BDMV/BACKUP/BDJO/00000.bdjo"},
			map[string]any{"name": "BDMV/index.bdmv"},
		},
	}}})
	if len(entries) != 1 || entries[0].Name != "generated.123.torrent" || entries[0].CanonicalType != "DISC" ||
		entries[0].Container != "m2ts" || entries[0].Group != "GRP" || len(entries[0].Flags) != 1 {
		t.Fatalf("ANT disc entry = %#v", entries)
	}
}

func TestANTListedWEBFileCoexistsWithDisc(t *testing.T) {
	t.Parallel()

	entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
		"title":        "Example Release 2026",
		"media":        "WEB",
		"resolution":   "1080p",
		"codec":        "H264",
		"container":    "MKV",
		"releaseGroup": "WEBGRP",
		"files": []any{map[string]any{
			"name": "Example.Release.2026.1080p.WEB-DL.H.264-WEBGRP.mkv",
		}},
	}}})
	if len(entries) != 1 || entries[0].Name != "Example.Release.2026.1080p.WEB-DL.H.264-WEBGRP.mkv" || entries[0].Source != "WEB" {
		t.Fatalf("ANT WEB entry = %#v", entries)
	}

	target := api.TrackerDuplicateTarget{
		Type:       "DISC",
		Source:     "Blu-ray",
		Resolution: "1080p",
		VideoCodec: "AVC",
		Group:      "GRP",
	}
	target.TrackerSlot = resolveTargetSlot(api.UploadSubject{
		Type:       target.Type,
		Source:     target.Source,
		VideoCodec: target.VideoCodec,
		Release:    api.ReleaseInfo{Resolution: target.Resolution},
	})
	result := dupe.Evaluate(
		target,
		[]dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "ANT")},
		*Profile().DupePolicy,
		dupe.SearchEvidence{Complete: true},
	)
	if got := result.Candidates[0].Relation; got != api.DupeRelationCoexists {
		t.Fatalf("ANT WEB candidate relation = %q", got)
	}
}

func antSearchPageJSON(t *testing.T, offset int, total int, count int, includePagination bool) string {
	t.Helper()
	items := make([]map[string]any, count)
	for index := range count {
		items[index] = map[string]any{
			"fileName":   "Example.Release." + strconv.Itoa(offset+index),
			"resolution": "2160p",
		}
	}
	payload := map[string]any{
		"item": items,
	}
	if includePagination {
		payload["response"] = map[string]any{
			"offset": offset,
			"total":  total,
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal ANT search page: %v", err)
	}
	return string(encoded)
}

func antDupeTestConfig() config.Config {
	return config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"ANT": {APIKey: "token"},
	}}}
}

func TestANTDiscCandidatesOccupyDistinctSlots(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, filename, container, source, resolution, codec, targetType string
		wantContainer                                                 string
		want                                                          api.DupeRelation
	}{
		{"DVD API container", "Example.2026.SD.DVD.VOBIFO.123.torrent", "VOBIFO", "DVD", "SD", "MPEG2", "ENCODE", "VOBIFO", api.DupeRelationCoexists},
		{"DVD torrent filename", "Example.2026.SD.DVD.VOBIFO.123.torrent", "", "DVD", "SD", "MPEG2", "ENCODE", "vobifo", api.DupeRelationCoexists},
		{"Blu-ray torrent filename", "Example.2026.1080p.BluRay.m2ts.123.torrent", "", "BluRay", "1080p", "H264", "ENCODE", "m2ts", api.DupeRelationCoexists},
		{"Blu-ray disc versus remux", "Example.2026.1080p.BluRay.m2ts.123.torrent", "", "BluRay", "1080p", "H264", "REMUX", "m2ts", api.DupeRelationCoexists},
		{"case insensitive filename", "Example.2026.1080p.BluRay.M2TS.123.TORRENT", "", "BluRay", "1080p", "H264", "ENCODE", "m2ts", api.DupeRelationCoexists},
		{"same DVD disc unknown country", "Example.2026.SD.DVD.VOBIFO.123.torrent", "", "DVD", "SD", "MPEG2", "DISC", "vobifo", api.DupeRelationManualReview},
		{"same Blu-ray disc unknown country", "Example.2026.1080p.BluRay.m2ts.123.torrent", "", "BluRay", "1080p", "H264", "DISC", "m2ts", api.DupeRelationManualReview},
		{"explicit conflicting container", "Example.2026.1080p.BluRay.m2ts.123.torrent", "MKV", "BluRay", "1080p", "H264", "DISC", "MKV", api.DupeRelationManualReview},
		{"explicit DVD MKV conflict", "Example.2026.SD.DVD.VOBIFO.123.torrent", "MKV", "DVD", "SD", "MPEG2", "DISC", "MKV", api.DupeRelationManualReview},
		{"explicit DVD MP4 conflict", "Example.2026.SD.DVD.VOBIFO.123.torrent", "MP4", "DVD", "SD", "MPEG2", "DISC", "MP4", api.DupeRelationManualReview},
		{"explicit DVD AVI conflict", "Example.2026.SD.DVD.VOBIFO.123.torrent", "AVI", "DVD", "SD", "MPEG2", "DISC", "AVI", api.DupeRelationManualReview},
		{"ordinary encode", "Example.2026.1080p.BluRay.x264-GRP.mkv", "MKV", "BluRay", "1080p", "H264", "DISC", "MKV", api.DupeRelationCoexists},
		{"DVD encode versus disc", "Example.2026.SD.DVD.MPEG2-GRP.mkv", "MKV", "DVD", "SD", "MPEG2", "DISC", "MKV", api.DupeRelationCoexists},
		{"generated MKV versus disc", "Example.2026.1080p.BluRay.MKV.123.torrent", "MKV", "BluRay", "1080p", "H264", "DISC", "MKV", api.DupeRelationCoexists},
		{"title word is not a disc marker", "VOBIFO.2026.SD.DVD.MPEG2.123.torrent", "", "DVD", "SD", "MPEG2", "ENCODE", "", api.DupeRelationSameSlot},
		{"non-numeric suffix is not a disc marker", "Example.2026.SD.DVD.VOBIFO.GRP.torrent", "", "DVD", "SD", "MPEG2", "ENCODE", "", api.DupeRelationSameSlot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
				"fileName":   tc.filename,
				"container":  tc.container,
				"media":      tc.source,
				"resolution": tc.resolution,
				"codec":      tc.codec,
			}}})
			if len(entries) != 1 || entries[0].Container != tc.wantContainer {
				t.Fatalf("candidate container: entries=%+v want=%q", entries, tc.wantContainer)
			}
			meta := api.UploadSubject{
				Type:       tc.targetType,
				Source:     tc.source,
				VideoCodec: tc.codec,
				Release:    api.ReleaseInfo{Resolution: tc.resolution},
			}
			target := api.TrackerDuplicateTarget{
				TrackerSlot: resolveTargetSlot(meta),
				Type:        meta.Type,
				Source:      meta.Source,
				Resolution:  tc.resolution,
				VideoCodec:  meta.VideoCodec,
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "ANT")},
				*duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != tc.want {
				t.Fatalf("relation=%s want=%s facts=%+v", got, tc.want, result.Candidates[0].Facts)
			}
		})
	}
}

func TestANTDiscSearchResultsFilterIncompatibleVariants(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	const filename = "Example.2026.1080p.BluRay.x264-GRP.mkv"
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"item":[
			{"fileName":"Example.2026.SD.DVD.VOBIFO.123.torrent","media":"DVD","resolution":"SD","codec":"MPEG2"},
			{"fileName":"Example.2026.1080p.BluRay.m2ts.124.torrent","guid":"https://anthelion.me/torrents.php?torrentid=42&passkey=private-key","files":[{"name":"BDMV/index.bdmv"},{"name":"BDMV/MovieObject.bdmv"}],"media":"BluRay","resolution":"1080p","codec":"H264"},
			{"fileName":"generated.999.torrent","guid":"https://anthelion.me/torrents.php?torrentid=43&passkey=private-key","link":"https://anthelion.me/download.php?id=43&passkey=private-key","files":[{"name":"` + filename + `"}],"media":"BluRay","resolution":"1080p","codec":"H264","container":"MKV"},
			{"files":[{"name":"Example.2026.1080p.BluRay.REMUX.AVC-GRP.mkv"}],"media":"BluRay","resolution":"1080p","codec":"H264","container":"MKV","flags":["Remux"]}
		]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	for _, tc := range []struct {
		name, kind, source, resolution, codec, matchName, matchedID string
		want                                                      api.DupeRelation
	}{
		{"encode exact match", "ENCODE", "BluRay", "1080p", "H264", filename, "43", api.DupeRelationExactDuplicate},
		{"Blu-ray disc", "DISC", "BluRay", "1080p", "H264", "Example.2026.1080p.BluRay.m2ts.124.torrent", "42", api.DupeRelationManualReview},
		{"DVD disc", "DISC", "DVD", "SD", "MPEG2", "Example.2026.SD.DVD.VOBIFO.123.torrent", "", api.DupeRelationManualReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := api.UploadSubject{
				Type:       tc.kind,
				Source:     tc.source,
				VideoCodec: tc.codec,
				Release:    api.ReleaseInfo{Resolution: tc.resolution},
			}
			target := api.TrackerDuplicateTarget{
				TrackerSlot: resolveTargetSlot(meta),
				Type:        meta.Type,
				Source:      meta.Source,
				Resolution:  tc.resolution,
				VideoCodec:  meta.VideoCodec,
			}
			if tc.kind == "ENCODE" {
				target.Names, target.FileNames = []string{filename}, []string{filename}
			}
			logger := &antCandidateLogger{}
			service := dupe.NewServiceWithRegistry(antDupeTestConfig(), logger, registry)
			summary, assessment, err := service.CheckWithAssessment(t.Context(), api.DuplicateSubject{
				SourcePath: "proposed",
				Identity:   api.ExternalIdentity{TMDBID: 123},
				Projection: &api.TrackerReleaseProjection{DuplicateTarget: target},
			}, []string{"ANT"}, dupe.CheckOptions{})
			if err != nil || len(summary.Results) != 1 {
				t.Fatalf("search: summary=%+v err=%v", summary, err)
			}
			result := summary.Results[0]
			if !result.HasDupes || !result.RequiresStaffToken || !result.Search.Complete || result.Search.CandidateCount != 4 {
				t.Fatalf("unexpected duplicate result: %+v", result)
			}
			for _, candidate := range result.Evaluations {
				wantID := ""
				switch candidate.Name {
				case "Example.2026.1080p.BluRay.m2ts.124.torrent":
					wantID = "42"
				case filename:
					wantID = "43"
				}
				if candidate.ID != wantID || !strings.Contains(logger.trace.String(), fmt.Sprintf("candidate_id=%q", wantID)) {
					t.Errorf("candidate=%q ID=%q want=%q trace=%s", candidate.Name, candidate.ID, wantID, logger.trace.String())
				}
				want := api.DupeRelationCoexists
				if candidate.Name == tc.matchName {
					want = tc.want
				}
				if candidate.Relation != want {
					t.Errorf("candidate=%q relation=%s want=%s", candidate.Name, candidate.Relation, want)
				}
			}
			if len(result.Evaluations) != 4 {
				t.Fatalf("evaluations=%+v", result.Evaluations)
			}
			decision, ok := assessment.Decision("ANT")
			if !ok || decision.Verdict != dupe.VerdictBlocked || decision.Match.MatchedID != tc.matchedID {
				t.Fatalf("assessment lost matched identity: decision=%+v result=%+v", decision, result)
			}
			public, err := json.Marshal(summary)
			if err != nil || strings.Contains(string(public)+logger.trace.String(), "private-key") {
				t.Fatalf("public candidate or diagnostics leaked download authority: %s err=%v", public, err)
			}
		})
	}
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNBLHDRFactsPreferStructuredTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		tags        []string
		tagsPresent bool
		release     string
		status      api.HDREvidenceStatus
		origin      api.HDREvidenceOrigin
		formats     []api.HDRFormat
	}{
		{
			name:        "structured sdr",
			tagsPresent: true,
			release:     "Example.Show.S01E01.1080p.WEB-DL-GRP",
			status:      api.HDREvidenceComplete,
			origin:      api.HDREvidenceTrackerAPI,
			formats:     []api.HDRFormat{api.HDRFormatSDR},
		},
		{
			name:        "structured hdr",
			tags:        []string{"hdr"},
			tagsPresent: true,
			release:     "Example.Show.S01E01.HDR10Plus.2160p.WEB-DL-GRP",
			status:      api.HDREvidenceComplete,
			origin:      api.HDREvidenceTrackerAPI,
			formats:     []api.HDRFormat{api.HDRFormatHDR10},
		},
		{
			name:        "structured dovi overrides title hdr",
			tags:        []string{"dovi"},
			tagsPresent: true,
			release:     "Example.Show.S01E01.DoVi.HDR.2160p.WEB-DL-GRP",
			status:      api.HDREvidenceComplete,
			origin:      api.HDREvidenceTrackerAPI,
			formats:     []api.HDRFormat{api.HDRFormatDolbyVision},
		},
		{
			name:        "structured dovi hdr",
			tags:        []string{"hdr", "dovi"},
			tagsPresent: true,
			release:     "Example.Show.S01E01.DoVi.HDR.2160p.WEB-DL-GRP",
			status:      api.HDREvidenceComplete,
			origin:      api.HDREvidenceTrackerAPI,
			formats:     []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		},
		{
			name:    "title fallback is partial",
			release: "Example.Show.S01E01.DV8.2160p.WEB-DL-GRP",
			status:  api.HDREvidencePartial,
			origin:  api.HDREvidenceTrackerTitle,
			formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		},
		{
			name:    "missing tags and title evidence",
			release: "Example.Show.S01E01.1080p.WEB-DL-GRP",
			status:  api.HDREvidenceMissing,
			origin:  api.HDREvidenceUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var tags *[]string
			if test.tagsPresent {
				values := append([]string(nil), test.tags...)
				tags = &values
			}
			facts, _, present := nblHDRFacts(tags, test.release)
			if present != test.tagsPresent || facts.Origin != test.origin || facts.Status != test.status ||
				len(facts.Formats) != len(test.formats) ||
				slices.ContainsFunc(facts.Formats, func(format api.HDRFormat) bool {
					return !slices.Contains(test.formats, format)
				}) {
				t.Fatalf("NBL HDR evidence = %#v, present=%t, want formats %v", facts, present, test.formats)
			}
		})
	}
}

func TestNBLSearchUsesTagsAndTraversesAllPages(t *testing.T) {
	requestPages := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		requestPages = append(requestPages, query.Get("page"))
		if request.Method != http.MethodGet || query.Get("action") != "search" || query.Get("api_key") != "synthetic-token" ||
			query.Get("per_page") != "100" || query.Get("tvmaze") != "81" {
			t.Errorf("unexpected NBL request: method=%s query=%v", request.Method, query)
		}
		response.Header().Set("Content-Type", "application/json")
		page := 0
		if query.Get("page") != "" {
			var err error
			page, err = strconv.Atoi(query.Get("page"))
			if err != nil {
				t.Errorf("parse page: %v", err)
			}
		}
		switch page {
		case 0:
			_, _ = io.WriteString(response, `{
				"current_page":0,
				"total_pages":2,
				"count":1,
				"total_results":2,
				"items":[{
					"rls_name":"Example.Show.S01.2160p.WEB-DL.DV.HDR-GRP",
					"cat":"Season",
					"download":"https://nebulance.io/api.php?action=download&apikey=SYNTHETIC&torrentid=101",
					"file_list":["Example.Show.S01E01.mkv"],
					"group_id":101,
					"season":1,
					"size":1000,
					"tags":["hdr"]
				}]
			}`)
		case 1:
			_, _ = io.WriteString(response, `{
				"current_page":1,
				"total_pages":2,
				"count":1,
				"total_results":2,
				"items":[{
					"rls_name":"Example.Show.S01E01.2160p.WEB-DL.HDR-GRP.mkv",
					"cat":"Episode",
					"download":"https://nebulance.io/api.php?action=download&apikey=SYNTHETIC&torrentid=102",
					"file_list":["Example.Show.S01E01.mkv"],
					"group_id":102,
					"season":1,
					"episode":1,
					"size":500,
					"tags":["dovi"]
				}]
			}`)
		default:
			t.Errorf("unexpected page %d", page)
		}
	}))
	defer server.Close()

	searcher := &dupeSearcher{
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{"NBL": {APIKey: "synthetic-token"}},
			},
		},
		http:     server.Client(),
		endpoint: server.URL,
		maxPages: 10,
	}
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{TVmazeID: 81},
	})
	search := result.SearchEvidence()
	entries := result.Entries()
	if !search.Complete || search.Pages != 2 || len(search.Warnings) != 0 || len(entries) != 2 {
		t.Fatalf("NBL search = %#v, entries=%#v", search, entries)
	}
	if !slices.Equal(requestPages, []string{"", "1"}) {
		t.Fatalf("requested pages = %v", requestPages)
	}
	if entries[0].HDR.Origin != api.HDREvidenceTrackerAPI ||
		!slices.Equal(entries[0].HDR.Formats, []api.HDRFormat{api.HDRFormatHDR10}) {
		t.Fatalf("page 0 HDR evidence = %#v", entries[0].HDR)
	}
	if entries[1].HDR.Origin != api.HDREvidenceTrackerAPI ||
		!slices.Equal(entries[1].HDR.Formats, []api.HDRFormat{api.HDRFormatDolbyVision}) {
		t.Fatalf("page 1 HDR evidence = %#v", entries[1].HDR)
	}
	if !entries[0].Pack || entries[0].Season != 1 || entries[1].Pack || entries[1].Episode != 1 {
		t.Fatalf("NBL candidate coordinates = %#v", entries)
	}
}

func TestNBLSearchReportsPaginationBound(t *testing.T) {
	requestedIMDb := ""
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestedIMDb = request.URL.Query().Get("imdb")
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{
			"current_page":0,
			"total_pages":2,
			"count":1,
			"total_results":2,
			"items":[{"rls_name":"Example.Show.S01E01.1080p.WEB-DL-GRP","group_id":101,"tags":[]}]
		}`)
	}))
	defer server.Close()

	searcher := &dupeSearcher{
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{"NBL": {APIKey: "synthetic-token"}},
			},
		},
		http:     server.Client(),
		endpoint: server.URL,
		maxPages: 1,
	}
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 456},
	})
	search := result.SearchEvidence()
	if search.Complete || search.Pages != 1 || len(search.Warnings) != 1 {
		t.Fatalf("bounded NBL search = %#v", search)
	}
	if requestedIMDb != "tt0000456" {
		t.Fatalf("NBL IMDb query = %q, want tt0000456", requestedIMDb)
	}
}

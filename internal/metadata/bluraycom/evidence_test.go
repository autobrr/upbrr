// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
	"github.com/autobrr/upbrr/pkg/api"
)

const evidenceSearchHTML = `<div class="figure">
	<a class="alphaborder" href="https://www.blu-ray.com/Example-Movie/900/">Example Movie</a>
	<div style="font-weight: bold">Example Movie</div><div style="margin-top: 2px">2026</div>
</div>`

const evidenceReleasesHTML = `<table>
	<tr><td><h3>4K Blu-ray Editions</h3></td></tr>
	<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example-A/101/" title="Example A">Example A</a></td></tr>
	<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example-B/102/" title="Example B">Example B</a></td></tr>
</table>`

const evidenceDetailsHTML = `<table><tr><td width="228px" style="font-size: 12px">
	<span class="subheading">Video</span>Codec: HEVC / H.265<br>Resolution: 2160p<br>
	<span class="subheading">Audio</span><div id="longaudio">English: Dolby TrueHD Atmos 7.1</div>
	<span class="subheading">Subtitles</span><div id="longsubs">English</div>
	<span class="subheading">Discs</span>Single disc (1 BD-100)
</td></tr></table>`

type evidenceRoundTrip func(*http.Request) (*http.Response, error)

func (f evidenceRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func openBlurayEvidenceRepo(t *testing.T, databasePath string) *db.SQLiteRepository {
	t.Helper()
	repo, err := db.OpenContext(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func blurayEvidenceResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestBlurayEvidenceRescoresAndPreservesManualSelectionAcrossRestart(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
	dbfixture.WriteMigrated(t, databasePath)
	repo := openBlurayEvidenceRepo(t, databasePath)
	var requests atomic.Int32
	httpClient := &http.Client{Transport: evidenceRoundTrip(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		var body string
		switch request.URL.Path {
		case "/search/":
			body = evidenceSearchHTML
		case "/products/menu_ajax.php":
			body = evidenceReleasesHTML
		case "/movies/Example-A/101/":
			body = evidenceDetailsHTML
		case "/movies/Example-B/102/":
			body = strings.ReplaceAll(evidenceDetailsHTML, "BD-100", "BD-50")
		default:
			t.Errorf("unexpected Blu-ray request: %s", request.URL.Path)
		}
		return blurayEvidenceResponse(request, http.StatusOK, body), nil
	})}
	input := LookupInput{
		SourcePath: filepath.Join(t.TempDir(), "Example.Disc"),
		IMDBID:     1234567,
		DiscType:   "BDMV",
		Resolution: "2160p",
		BDInfo:     matchingBDInfo(),
	}
	for _, correction := range []struct {
		sizeGB    float64
		threshold float64
		manual    string
		want      string
		wantScore float64
		wantAuto  bool
	}{
		{
			sizeGB:    70,
			threshold: 100,
			want:      "101",
			wantScore: 100,
			wantAuto:  true,
		},
		{sizeGB: 70, threshold: 101},
		{
			sizeGB:    40,
			threshold: 101,
			manual:    "101",
			want:      "101",
			wantScore: 50,
		},
		{
			sizeGB:    40,
			threshold: 101,
			manual:    "102",
			want:      "102",
			wantScore: 100,
		},
		{
			sizeGB:    40,
			threshold: 90,
			want:      "102",
			wantScore: 100,
			wantAuto:  true,
		},
	} {
		input.BDInfo.SizeGB = correction.sizeGB
		input.ScoreThreshold = correction.threshold
		input.SelectedReleaseID = correction.manual
		ctx, scope := evidence.WithScope(t.Context(), repo, input.SourcePath, "fingerprint", api.ExternalFreshnessLoad, nil)
		for range 2 {
			result, err := NewClient(httpClient).Lookup(ctx, input)
			if err != nil || scope.Err() != nil {
				t.Fatalf("lookup: %v; evidence: %v", err, scope.Err())
			}
			if len(result.Candidates) != 2 || result.BestScore != 100 || result.Threshold != correction.threshold || result.SelectedReleaseID != correction.want || result.AutoSelected != correction.wantAuto {
				t.Fatalf("correction %#v: result = %#v", correction, result)
			}
			if correction.want != "" {
				candidate := result.CandidateByID(correction.want)
				if candidate == nil || !candidate.Accepted || candidate.Score != correction.wantScore {
					t.Fatalf("selected candidate = %#v, want score %.1f", candidate, correction.wantScore)
				}
				if correction.manual != "" && result.SelectionReason != "manual" {
					t.Fatalf("manual selection reason = %q", result.SelectionReason)
				}
			}
			if requests.Load() != 4 {
				t.Fatalf("local correction refetched pages: requests = %d, want 4", requests.Load())
			}
			result.Candidates[0].Specs.Video.Codec = "Caller mutation"
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
		repo = openBlurayEvidenceRepo(t, databasePath)
	}
	input.IMDBID = 2345678
	ctx, scope := evidence.WithScope(t.Context(), repo, input.SourcePath, "fingerprint", api.ExternalFreshnessReuse, nil)
	result, err := NewClient(httpClient).Lookup(ctx, input)
	if err != nil || scope.Err() != nil || result.IMDBID != input.IMDBID || requests.Load() != 5 {
		t.Fatalf("changed IMDb lookup = %#v, %v; requests = %d, want 5; evidence: %v", result, err, requests.Load(), scope.Err())
	}
}

func TestBlurayEvidenceEmptyPagesRetryOnlyOnFreshLoad(t *testing.T) {
	t.Parallel()
	for _, emptyAt := range []string{"search", "releases", "details"} {
		t.Run(emptyAt, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
			dbfixture.WriteMigrated(t, databasePath)
			repo := openBlurayEvidenceRepo(t, databasePath)
			var requests atomic.Int32
			httpClient := &http.Client{Transport: evidenceRoundTrip(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				body := ""
				switch request.URL.Path {
				case "/search/":
					body = `<html>No matching movie</html>`
					if emptyAt != "search" {
						body = evidenceSearchHTML
					}
				case "/products/menu_ajax.php":
					body = `<table><tr><td><h3>4K Blu-ray Editions</h3></td></tr></table>`
					if emptyAt != "releases" {
						body = evidenceReleasesHTML
					}
				}
				return blurayEvidenceResponse(request, http.StatusOK, body), nil
			})}
			input := LookupInput{
				SourcePath: filepath.Join(t.TempDir(), "Example.Disc"),
				IMDBID:     1234567,
				DiscType:   "BDMV",
				Resolution: "2160p",
			}
			initial := int32(1)
			additional := int32(1)
			if emptyAt == "releases" {
				initial = 2
			}
			if emptyAt == "details" {
				initial, additional = 4, 2
			}
			for pass, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
				ctx, scope := evidence.WithScope(t.Context(), repo, input.SourcePath, "fingerprint", freshness, nil)
				for range 2 {
					result, err := NewClient(httpClient).Lookup(ctx, input)
					if err != nil || scope.Err() != nil {
						t.Fatalf("lookup: %v; evidence: %v", err, scope.Err())
					}
					if emptyAt != "details" && len(result.Candidates) != 0 {
						t.Fatalf("unexpected candidates: %#v", result.Candidates)
					}
				}
				want := initial
				if pass == 2 {
					want += additional
				}
				if requests.Load() != want {
					t.Fatalf("freshness %q: requests = %d, want %d", freshness, requests.Load(), want)
				}
			}
		})
	}
}

func TestBlurayEvidenceFailuresStaySuppressedUntilHistoryPurge(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"HTTP 500", "anti-scraping"} {
		t.Run(failure, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
			dbfixture.WriteMigrated(t, databasePath)
			repo := openBlurayEvidenceRepo(t, databasePath)
			var requests atomic.Int32
			httpClient := &http.Client{Transport: evidenceRoundTrip(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				status, body := http.StatusInternalServerError, "Provider unavailable"
				if failure == "anti-scraping" {
					status, body = http.StatusOK, "No index"
				}
				return blurayEvidenceResponse(request, status, body), nil
			})}
			input := LookupInput{SourcePath: filepath.Join(t.TempDir(), "Example.Disc"), IMDBID: 1234567}
			for pass, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessLoad, api.ExternalFreshnessRefresh} {
				ctx, scope := evidence.WithScope(t.Context(), repo, input.SourcePath, "fingerprint", freshness, nil)
				_, err := NewClient(httpClient).Lookup(ctx, input)
				if err == nil || pass > 0 && !errors.Is(err, evidence.ErrSuppressed) {
					t.Fatalf("freshness %q: failure = %v", freshness, err)
				}
				if requests.Load() != 1 || scope.Err() != nil {
					t.Fatalf("failed request retried: requests = %d; evidence: %v", requests.Load(), scope.Err())
				}
			}
			if err := repo.PurgeContentData(t.Context(), input.SourcePath); err != nil {
				t.Fatal(err)
			}
			ctx, _ := evidence.WithScope(t.Context(), repo, input.SourcePath, "fingerprint", api.ExternalFreshnessLoad, nil)
			_, err := NewClient(httpClient).Lookup(ctx, input)
			if err == nil || errors.Is(err, evidence.ErrSuppressed) || requests.Load() != 2 {
				t.Fatalf("lookup after History purge = %v; requests = %d", err, requests.Load())
			}
		})
	}
}

func TestBlurayEvidenceCancellationDoesNotPoisonRetry(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
	dbfixture.WriteMigrated(t, databasePath)
	repo := openBlurayEvidenceRepo(t, databasePath)
	var requests atomic.Int32
	httpClient := &http.Client{Transport: evidenceRoundTrip(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return nil, context.Canceled
		}
		return blurayEvidenceResponse(request, http.StatusOK, "<html>No matching movie</html>"), nil
	})}
	input := LookupInput{SourcePath: filepath.Join(t.TempDir(), "Example.Disc"), IMDBID: 1234567}
	ctx, _ := evidence.WithScope(t.Context(), repo, input.SourcePath, "fingerprint", api.ExternalFreshnessReuse, nil)
	if _, err := NewClient(httpClient).Lookup(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup = %v", err)
	}
	ctx, scope := evidence.WithScope(t.Context(), repo, input.SourcePath, "fingerprint", api.ExternalFreshnessReuse, nil)
	result, err := NewClient(httpClient).Lookup(ctx, input)
	if err != nil || result == nil || requests.Load() != 2 || scope.Err() != nil {
		t.Fatalf("retry = %#v, %v; requests = %d; evidence: %v", result, err, requests.Load(), scope.Err())
	}
}

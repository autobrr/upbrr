// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLookupListingSingleAllAndSelectionReuse(t *testing.T) {
	t.Parallel()
	counts := map[string]int{}
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		counts[r.URL.Path]++
		body := evidenceDetailsHTML
		switch r.URL.Path {
		case "/search/":
			body = evidenceSearchHTML
		case "/products/menu_ajax.php":
			body = evidenceReleasesHTML
		}
		return blurayEvidenceResponse(r, http.StatusOK, body), nil
	})})
	input := LookupInput{
		IMDBID:     1234567,
		DiscType:   "BDMV",
		Resolution: "2160p",
		BDInfo:     matchingBDInfo(),
	}
	listing, err := client.Lookup(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 || len(listing.Candidates) != 2 || listing.SelectedReleaseID != "" {
		t.Fatalf("discovery made detail requests or selected an unscored match: %#v %v", listing, counts)
	}
	for _, c := range listing.Candidates {
		if c.DetailsStatus != "" || len(c.MatchNotes) != 0 || c.SpecsMissing {
			t.Fatalf("listing was presented as a completed comparison: %#v", c)
		}
	}
	input.Retained, input.SelectedReleaseID = listing, "102"
	selected, err := client.Lookup(t.Context(), input)
	if err != nil || len(counts) != 2 || selected.SelectedReleaseID != "102" || selected.AutoSelected {
		t.Fatalf("selection fetched pages or lost authority: %v %#v %v", err, selected, counts)
	}
	input.Retained, input.Details = selected, &api.BlurayDetailRequest{ReleaseID: " 101 "}
	input.FetchSelected = true
	one, err := client.Lookup(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if counts["/movies/Example-A/101/"] != 1 || counts["/movies/Example-B/102/"] != 0 || one.CandidateByID("101").DetailsStatus != "complete" || one.CandidateByID("102").DetailsStatus != "" || one.SelectedReleaseID != "102" {
		t.Fatalf("single inspection: %#v %v", one, counts)
	}
	input.Retained, input.Details = one, &api.BlurayDetailRequest{All: true}
	all, err := client.Lookup(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if counts["/movies/Example-A/101/"] != 1 || counts["/movies/Example-B/102/"] != 1 || all.CandidateByID("102").DetailsStatus != "complete" || all.SelectedReleaseID != "102" {
		t.Fatalf("all inspection: %#v %v", all, counts)
	}
	input.Retained, input.Details, input.BDInfo = all, nil, nil
	rescored, err := client.Lookup(t.Context(), input)
	if err != nil || rescored.SelectedReleaseID != "102" || counts["/search/"] != 1 || counts["/movies/Example-B/102/"] != 1 {
		t.Fatalf("ordinary rescore refetched or replaced manual selection: %v %v", err, counts)
	}
	input.SkipSelection = true
	skipped, err := client.Lookup(t.Context(), input)
	if err != nil || skipped.SelectedReleaseID != "" || len(counts) != 4 {
		t.Fatalf("skip performed detail work: %v %v", err, counts)
	}
}

func TestDetailRedirectMustRetainReleaseIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, location, status string
	}{
		{
			name:     "same edition",
			location: "https://www.blu-ray.com/movies/Renamed/101/",
			status:   "complete",
		},
		{
			name:     "different edition",
			location: "https://www.blu-ray.com/movies/Other/102/",
			status:   "failed",
		},
		{
			name:     "different namespace",
			location: "https://www.blu-ray.com/dvd/Other/101/",
			status:   "failed",
		},
		{
			name:     "different host",
			location: "https://other.invalid/movies/Other/101/",
			status:   "failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/movies/Original/101/" {
					response := blurayEvidenceResponse(r, http.StatusFound, "")
					response.Header.Set("Location", tc.location)
					return response, nil
				}
				return blurayEvidenceResponse(r, http.StatusOK, `<script>$("#x").append('<img id="frontimage" src="https://img.example/front.jpg">')</script>`), nil
			})})
			result, err := client.Lookup(t.Context(), LookupInput{Retained: &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
				ReleaseID: "101",
				Title:     "Example",
				URL:       "https://www.blu-ray.com/movies/Original/101/",
			}}}, Details: &api.BlurayDetailRequest{ReleaseID: "101"}})
			if err != nil || result.CandidateByID("101").DetailsStatus != tc.status {
				t.Fatalf("redirect status: %#v, error %v", result, err)
			}
		})
	}
}

func TestDetailLookupDoesNotReuseUnverifiedPageEvidence(t *testing.T) {
	const target = "https://www.blu-ray.com/movies/Original/101/"
	store := pageEvidenceStore{}
	ctx, scope := evidence.WithScope(t.Context(), store, "Example.Release", "fingerprint", api.ExternalFreshnessReuse, nil)
	_, err := evidence.Lookup(ctx, "bluray.page", []string{target, baseURL + "/"}, func() (string, error) {
		return `<script>$("#x").append('<img id="frontimage" src="https://img.example/stale.jpg">')</script>`, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return blurayEvidenceResponse(r, http.StatusOK, `<link rel="canonical" href="https://www.blu-ray.com/movies/Original/101/"><h1>Current edition</h1><script>$("#x").append('<img id="frontimage" src="https://img.example/current.jpg">')</script>`), nil
	})})
	result, err := client.Lookup(ctx, LookupInput{Retained: &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
		ReleaseID: "101",
		Title:     "Example",
		URL:       target,
	}}}, Details: &api.BlurayDetailRequest{ReleaseID: "101"}})
	if err != nil || scope.Err() != nil || calls != 1 || result == nil || result.CandidateByID("101") == nil || len(result.CandidateByID("101").CoverImages) != 1 || result.CandidateByID("101").CoverImages[0].URL != "https://img.example/current.jpg" {
		t.Fatalf("unverified page evidence was reused: calls=%d result=%#v err=%v", calls, result, err)
	}
}

func TestDetailLookupPreservesLegacyFailedEvidence(t *testing.T) {
	const target = "https://www.blu-ray.com/movies/Original/101/"
	ctx, _ := evidence.WithScope(t.Context(), pageEvidenceStore{}, "Example.Release", "fingerprint", api.ExternalFreshnessReuse, nil)
	_, _ = evidence.Lookup(ctx, "bluray.page", []string{target, baseURL + "/"}, func() (string, error) {
		return "", errors.New("synthetic provider failure")
	}, nil)
	calls := 0
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return blurayEvidenceResponse(r, http.StatusOK, evidenceDetailsHTML), nil
	})})
	result, err := client.Lookup(ctx, LookupInput{Retained: &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
		ReleaseID: "101",
		Title:     "Example",
		URL:       target,
	}}}, Details: &api.BlurayDetailRequest{ReleaseID: "101"}})
	if err != nil || calls != 0 || result.CandidateByID("101").DetailsStatus != "failed" {
		t.Fatalf("legacy failed evidence was retried: calls=%d result=%#v err=%v", calls, result, err)
	}
}

func TestDetailLookupDoesNotTrustUnverifiedRetainedDetails(t *testing.T) {
	const target = "https://www.blu-ray.com/movies/Original/101/"
	ctx, _ := evidence.WithScope(t.Context(), pageEvidenceStore{}, "Example.Release", "fingerprint", api.ExternalFreshnessReuse, nil)
	calls := 0
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return blurayEvidenceResponse(r, http.StatusOK, `<link rel="canonical" href="https://www.blu-ray.com/movies/Original/101/"><h1>Current edition</h1><script>$("#x").append('<img id="frontimage" src="https://img.example/current.jpg">')</script>`), nil
	})})
	result, err := client.Lookup(ctx, LookupInput{
		SelectedReleaseID: "101",
		Retained: &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
			ReleaseID:     "101",
			Title:         "Example",
			URL:           target,
			DetailsStatus: "complete",
			CoverImages:   []api.BlurayImage{{Kind: "front", URL: "https://img.example/stale.jpg"}},
		}}},
		Details: &api.BlurayDetailRequest{ReleaseID: "101"},
	})
	if err != nil || calls != 1 || result == nil || result.SelectedReleaseID != "101" || result.AutoSelected || result.CandidateByID("101") == nil || len(result.CandidateByID("101").CoverImages) != 1 || result.CandidateByID("101").CoverImages[0].URL != "https://img.example/current.jpg" {
		t.Fatalf("unverified retained details were reused: calls=%d result=%#v err=%v", calls, result, err)
	}
	if _, err := client.Lookup(t.Context(), LookupInput{
		Retained:          result,
		SelectedReleaseID: "101",
		Details:           &api.BlurayDetailRequest{ReleaseID: "101"},
	}); err != nil || calls != 1 {
		t.Fatalf("verified details were not reused without an evidence scope: calls=%d error=%v", calls, err)
	}
	_, err = client.Lookup(t.Context(), LookupInput{Retained: &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
		ReleaseID:     "101",
		URL:           target,
		DetailsStatus: "complete",
	}}}, Details: &api.BlurayDetailRequest{ReleaseID: "101"}})
	if !errors.Is(err, ErrDetailReleaseNotRetained) || calls != 1 {
		t.Fatalf("unverifiable unselected details were fetched or reused: calls=%d error=%v", calls, err)
	}
}

func TestSingleDetailRequestRejectsUnavailableOrFilteredCandidate(t *testing.T) {
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		return blurayEvidenceResponse(r, http.StatusOK, "<html>No results</html>"), nil
	})})
	_, err := client.Lookup(t.Context(), LookupInput{IMDBID: 1234567, Details: &api.BlurayDetailRequest{ReleaseID: "101"}})
	if !errors.Is(err, ErrDetailReleaseNotRetained) {
		t.Fatalf("empty catalogue did not reject the requested candidate: %v", err)
	}
	_, err = client.Lookup(t.Context(), LookupInput{
		CountryOverride: new("GER"),
		Retained:        &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{ReleaseID: "101", Country: "United States"}}},
		Details:         &api.BlurayDetailRequest{ReleaseID: "101"},
	})
	if !errors.Is(err, ErrDetailReleaseNotRetained) {
		t.Fatalf("country-filtered candidate was silently accepted: %v", err)
	}
}

func TestLookupExactReleaseByIDOrURLDoesNotDiscoverOtherReleases(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ override, discType, retainedURL string }{{"101", "BDMV", ""}, {"https://www.blu-ray.com/movies/Example-A/101/", "BDMV", ""}, {"101", "DVD", ""}, {"https://www.blu-ray.com/dvd/movies.php?id=101", "DVD", ""}, {"https://www.blu-ray.com/movies/Example-A/101/", "BDMV", "https://www.blu-ray.com/dvd/Other/101/"}, {"https://www.blu-ray.com/dvd/movies.php?id=101", "DVD", "https://www.blu-ray.com/movies/Other/101/"}} {
		override := test.override
		t.Run(override, func(t *testing.T) {
			requests := 0
			client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if test.discType == "DVD" && r.URL.Path != "/dvd/movies.php" {
					t.Fatal("DVD ID resolved through the Blu-ray namespace")
				}
				if r.URL.Path != "/movies/movies.php" && r.URL.Path != "/movies/Example-A/101/" && r.URL.Path != "/dvd/movies.php" {
					t.Fatalf("exact release scanned unrelated page: %s", r.URL.Path)
				}
				body := `<link rel="canonical" href="https://www.blu-ray.com/movies/Example-A/101/"><a><h1>Example Edition</h1></a><img src="https://images.static-bluray.com/flags/US.png" title="United States"><br><a href="/movies/movies.php?studioid=123">Example Publisher</a>` + evidenceDetailsHTML
				if test.discType == "DVD" {
					body = strings.Replace(body, "/movies/Example-A/101/", "/dvd/Example-A/101/", 1)
				}
				return blurayEvidenceResponse(r, http.StatusOK, body), nil
			})})
			var retained *api.BlurayMetadata
			if test.retainedURL != "" {
				retained = &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
					ReleaseID:     "101",
					URL:           test.retainedURL,
					Title:         "Other namespace",
					DetailsStatus: "complete",
				}}}
			}
			result, err := client.Lookup(t.Context(), LookupInput{
				ExactRelease: override,
				DiscType:     test.discType,
				Retained:     retained,
			})
			if err != nil {
				t.Fatal(err)
			}
			selected := result.SelectedCandidate()
			if requests != 1 || selected == nil || selected.ReleaseID != "101" || selected.Publisher != "Example Publisher" || selected.Region != "USA" || selected.DetailsStatus != "complete" || result.AutoSelected {
				t.Fatalf("exact override: requests=%d %#v", requests, result)
			}
			// The same compatible release accepts a URL instruction without refetching.
			result, err = client.Lookup(t.Context(), LookupInput{
				ExactRelease: override,
				DiscType:     test.discType,
				Retained:     result,
			})
			if err != nil || requests != 1 || result.SelectedReleaseID != "101" {
				t.Fatalf("exact override did not reuse: %v requests=%d", err, requests)
			}
		})
	}
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		return blurayEvidenceResponse(r, http.StatusNotFound, "missing"), nil
	})})
	if _, err := client.Lookup(t.Context(), LookupInput{ExactRelease: "101"}); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unavailable exact release: %v", err)
	}
}

func TestRequestedDetailsRetainPerReleaseFailuresAndHonorCancellation(t *testing.T) {
	t.Parallel()
	retained := &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{
		{
			ReleaseID: "101",
			Title:     "Example A",
			URL:       "https://www.blu-ray.com/movies/Example-A/101/",
		},
		{
			ReleaseID: "102",
			Title:     "Example B",
			URL:       "https://www.blu-ray.com/movies/Example-B/102/",
		},
	}}
	calls := 0
	var snapshots []api.BlurayReleaseCandidate
	observed := api.WithPreparationProgressReporter(t.Context(), func(update api.PreparationProgressUpdate) {
		if update.BlurayCandidate != nil {
			snapshots = append(snapshots, *update.BlurayCandidate)
		}
	})
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(r.URL.Path, "102") {
			if len(snapshots) != 1 || snapshots[0].ReleaseID != "101" || snapshots[0].DetailsStatus != "complete" {
				t.Fatal("successful detail was not published before the next request")
			}
			return blurayEvidenceResponse(r, http.StatusServiceUnavailable, "unavailable"), nil
		}
		return blurayEvidenceResponse(r, http.StatusOK, evidenceDetailsHTML), nil
	})})
	input := LookupInput{Retained: retained, Details: &api.BlurayDetailRequest{All: true}}
	result, err := client.Lookup(observed, input)
	if err != nil || calls != 2 || len(result.Candidates) != 2 || result.CandidateByID("101").DetailsStatus != "complete" || result.CandidateByID("102").DetailsStatus != "failed" || result.SelectedReleaseID != "" || len(result.CandidateByID("102").MatchNotes) != 0 {
		t.Fatalf("partial inspection: %v %#v", err, result)
	}
	if len(snapshots) != 2 || snapshots[1].DetailsStatus != "failed" {
		t.Fatal("detail failure did not update its own listing")
	}
	if retained.Candidates[0].DetailsStatus != "" {
		t.Fatal("inspection mutated retained generation")
	}
	reused, err := client.Lookup(t.Context(), LookupInput{Retained: result})
	if err != nil || reused == nil || calls != 2 || reused.CandidateByID("101").DetailsStatus != "complete" || reused.CandidateByID("102").DetailsStatus != "failed" {
		t.Fatalf("ordinary reuse lost partial inspection: calls=%d result=%#v error=%v", calls, reused, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	client = NewClient(&http.Client{Transport: releaseFixtureTransport(func(_ *http.Request) (*http.Response, error) {
		cancel()
		return nil, ctx.Err()
	})})
	if _, err := client.Lookup(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("detail cancellation: %v", err)
	}
}

func TestExactAndLegacyInspectionControlsRespectCountryAndSingleScope(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, all := range []bool{false, true} {
			details := &api.BlurayDetailRequest{All: all}
			if !all {
				details.ReleaseID = "101"
			}
			calls := map[string]int{}
			client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls[r.URL.Path]++
				body := evidenceDetailsHTML
				switch r.URL.Path {
				case "/search/":
					body = evidenceSearchHTML
				case "/products/menu_ajax.php":
					body = strings.Replace(evidenceReleasesHTML, "title=\"United States\"><a href=\"https://www.blu-ray.com/movies/Example-B", "title=\"Germany\"><a href=\"https://www.blu-ray.com/movies/Example-B", 1)
				case "/movies/Exact/103/":
					body = `<link rel="canonical" href="https://www.blu-ray.com/movies/Exact/103/"><a><h1>Exact edition</h1></a><img src="https://images.static-bluray.com/flags/US.png" title="United States"><br>` + body
				}
				return blurayEvidenceResponse(r, http.StatusOK, body), nil
			})})
			input := LookupInput{
				IMDBID:          1234567,
				DiscType:        "BDMV",
				CountryOverride: new("USA"),
				Details:         details,
				ExactRelease:    "https://www.blu-ray.com/movies/Exact/103/",
			}
			if legacy {
				input.ExactRelease = ""
				input.SelectedReleaseID = "103"
				input.Retained = &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
					ReleaseID:     "103",
					URL:           "https://www.blu-ray.com/movies/Exact/103/",
					DetailsStatus: "complete",
				}}}
			}
			_, err := client.Lookup(t.Context(), input)
			if legacy || !all {
				if !errors.Is(err, ErrDetailReleaseNotRetained) {
					t.Fatalf("conflicting inspection controls accepted: legacy=%t all=%t error=%v", legacy, all, err)
				}
				if calls["/movies/Exact/103/"] != 0 || calls["/movies/Example-A/101/"] != 0 || calls["/movies/Example-B/102/"] != 0 {
					t.Fatal("rejected inspection performed detail work")
				}
			} else if err != nil || calls["/movies/Example-B/102/"] != 0 || calls["/movies/Example-A/101/"] != 1 || calls["/movies/Exact/103/"] != 1 {
				t.Fatalf("all inspection escaped country scope: error=%v calls=%v", err, calls)
			}
		}
	}
}

func TestAutoDiscoversCatalogueAfterExactReleaseAndReusesItsDetails(t *testing.T) {
	t.Parallel()
	counts := map[string]int{}
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		counts[r.URL.Path]++
		body := evidenceDetailsHTML
		switch r.URL.Path {
		case "/search/":
			body = evidenceSearchHTML
		case "/products/menu_ajax.php":
			body = evidenceReleasesHTML
		default:
			body = `<link rel="canonical" href="https://www.blu-ray.com/movies/Example-A/101/"><a><h1>Example A</h1></a><img src="https://images.static-bluray.com/flags/US.png" title="United States"><br>` + body
		}
		return blurayEvidenceResponse(r, http.StatusOK, body), nil
	})})
	input := LookupInput{
		IMDBID:       1234567,
		DiscType:     "BDMV",
		Resolution:   "2160p",
		ExactRelease: "101",
		BDInfo:       matchingBDInfo(),
	}
	exact, err := client.Lookup(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExactRelease, input.SelectedReleaseID, input.Retained = "", "", exact
	automatic, err := client.Lookup(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(automatic.Candidates) != 2 || automatic.SearchURL == "" || automatic.SelectedReleaseID != "" || automatic.CandidateByID("101").DetailsStatus != "complete" || automatic.CandidateByID("102").DetailsStatus != "" || counts["/search/"] != 1 || counts["/products/menu_ajax.php"] != 1 || counts["/movies/movies.php"] != 1 || len(counts) != 3 {
		t.Fatalf("Auto treated an exact subset as a catalogue or refetched details: %#v %v", automatic, counts)
	}
	// An exact-only result cannot assert a unique-country automatic match when discovery fails.
	exact.SelectedReleaseID = ""
	SelectRelease(exact, LookupInput{CountryOverride: new("USA")})
	if exact.SelectedReleaseID != "" {
		t.Fatal("exact-only evidence claimed a catalogue-wide automatic match")
	}
}

func TestRebuiltCatalogueRetainsOnlyVerifiedKnownReleaseFacts(t *testing.T) {
	for _, tc := range []struct {
		name, status, version, country, publisher, listedFlag string
		wantEligible                                          bool
		wantPublisher                                         string
	}{
		{"verified details", "complete", DetailsIdentityVersion, "United States", "Verified Publisher", "", true, "Verified Publisher"},
		{"failed inspection", "failed", DetailsIdentityVersion, "Germany", "Old Publisher", `<img width="18" height="12" title="United States">`, true, "Catalogue Publisher"},
		{"missing detail fields", "complete", DetailsIdentityVersion, "Unknown", "", `<img width="18" height="12" title="United States">`, true, "Catalogue Publisher"},
		{"unverified details", "complete", "", "United States", "Old Publisher", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := evidenceSearchHTML
				if r.URL.Path == "/products/menu_ajax.php" {
					body = `<table><tr><td><h3>4K Blu-ray Editions</h3></td></tr><tr><td>` + tc.listedFlag + `<a href="https://www.blu-ray.com/movies/Example-A/101/">Example A</a><small style="color: #999999">Catalogue Publisher</small></td></tr></table>`
				} else if r.URL.Path != "/search/" {
					t.Fatal("catalogue rebuild repeated a detail request")
				}
				return blurayEvidenceResponse(r, http.StatusOK, body), nil
			})})
			retained := &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{{
				ReleaseID:              "101",
				URL:                    "https://www.blu-ray.com/movies/Example-A/101/",
				DetailsStatus:          tc.status,
				DetailsIdentityVersion: tc.version,
				Country:                tc.country,
				Region:                 countryToRegion(tc.country),
				Publisher:              tc.publisher,
			}}}
			result, err := client.Lookup(t.Context(), LookupInput{
				IMDBID:          1234567,
				DiscType:        "BDMV",
				CountryOverride: new("USA"),
				Retained:        retained,
			})
			if err != nil || calls != 2 || result == nil {
				t.Fatalf("catalogue rebuild: calls=%d error=%v", calls, err)
			}
			selected := result.SelectedCandidate()
			if !tc.wantEligible {
				if selected != nil || len(result.Candidates) != 0 {
					t.Fatal("unverified facts affected country filtering")
				}
			} else if selected == nil || selected.ReleaseID != "101" || selected.Country != "United States" || selected.Region != "USA" || selected.Publisher != tc.wantPublisher {
				t.Fatalf("verified or fresh listing facts lost: %#v", selected)
			}
			if retained.Candidates[0].Country != tc.country || retained.Candidates[0].Publisher != tc.publisher {
				t.Fatal("catalogue rebuilding changed the retained generation")
			}
		})
	}
}

func TestFetchAllPreservesSavedManualReleaseAcrossNamespaceCollision(t *testing.T) {
	t.Parallel()
	requests := 0
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := evidenceSearchHTML
		if r.URL.Path == "/products/menu_ajax.php" {
			body = evidenceReleasesHTML
		} else if r.URL.Path != "/search/" {
			t.Fatal("namespace collision started a detail request")
		}
		return blurayEvidenceResponse(r, http.StatusOK, body), nil
	})})
	retained := &api.BlurayMetadata{
		SelectedReleaseID: "101",
		Candidates: []api.BlurayReleaseCandidate{{
			ReleaseID:     "101",
			URL:           "https://www.blu-ray.com/dvd/Example/101/",
			DetailsStatus: "complete",
		}},
	}
	result, err := client.Lookup(t.Context(), LookupInput{
		IMDBID:            1234567,
		DiscType:          "BDMV",
		Retained:          retained,
		SelectedReleaseID: "101",
		Details:           &api.BlurayDetailRequest{All: true},
	})
	if err == nil || requests != 2 || result.SelectedCandidate().URL != retained.SelectedCandidate().URL || result.AutoSelected {
		t.Fatalf("saved manual edition was replaced across namespaces: %v %#v", err, result)
	}
}

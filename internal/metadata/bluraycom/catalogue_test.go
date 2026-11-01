// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestLookupOmitsFragmentFromCatalogueReferer(t *testing.T) {
	requests := 0
	client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		switch r.URL.Path {
		case "/search/":
			return blurayEvidenceResponse(r, http.StatusOK, evidenceSearchHTML), nil
		case "/products/menu_ajax.php":
			// A browser's HTTP referrer omits the Releases navigation fragment.
			if r.Header.Get("Referer") != "https://www.blu-ray.com/Example-Movie/900/" {
				return blurayEvidenceResponse(r, http.StatusOK, ""), nil
			}
			return blurayEvidenceResponse(r, http.StatusOK, `<h3>Blu-ray Editions</h3><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example/101/">Example</a><img width="18" height="12" title="Germany"><a href="https://www.blu-ray.com/movies/Example/102/">Example</a>`), nil
		default:
			if r.Header.Get("Referer") != "https://www.blu-ray.com/products/menu_ajax.php?p=900&c=20&action=showreleasesall" {
				t.Errorf("detail referrer query lost: %q", r.Header.Get("Referer"))
			}
			return blurayEvidenceResponse(r, http.StatusOK, evidenceDetailsHTML), nil
		}
	})})
	got, err := client.Lookup(t.Context(), LookupInput{
		IMDBID:          1234567,
		SourcePath:      filepath.Join(t.TempDir(), "Example.USA.BluRay-GRP"),
		DiscType:        "BDMV",
		Resolution:      "1080p",
		ScoreThreshold:  100,
		SingleThreshold: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SelectedReleaseID != "101" || !got.AutoSelected || got.Threshold != 0 || len(got.Candidates) != 1 || got.Candidates[0].Country != "United States" || got.Candidates[0].Region != "USA" || got.Candidates[0].Score != 85 {
		t.Fatalf("country discovery/selection: %#v", got)
	}
	if requests != 4 {
		t.Fatalf("requests=%d, want search, catalogue and two edition details", requests)
	}
}

func TestParseReleaseCountryDoesNotLeakAcrossSections(t *testing.T) {
	got, err := parseReleaseInfo(`<h3>4K Blu-ray Editions</h3><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Other/999/">Other</a><h3>Blu-ray Editions</h3><a href="https://www.blu-ray.com/movies/Example/101/">Example</a>`, LookupInput{DiscType: "BDMV", Resolution: "1080p"})
	if err != nil || len(got) != 1 || got[0].Country != "Unknown" || got[0].Region != "" {
		t.Fatalf("country leaked from previous section: %#v %v", got, err)
	}
	result := &api.BlurayMetadata{Candidates: got}
	SelectRelease(result, LookupInput{SourcePath: filepath.Join(t.TempDir(), "Example.USA.BluRay-GRP")})
	if result.SelectedReleaseID != "" || len(result.Candidates) != 0 {
		t.Fatalf("unknown country became a known-country match: %#v", result)
	}
}

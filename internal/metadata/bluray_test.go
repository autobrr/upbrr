// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestShouldLookupBlurayWhenDescriptionNeedsBlurayData(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		wantLookup  bool
		wantEnabled bool
	}{
		{
			name: "metadata lookup enabled",
			cfg: config.Config{
				Metadata: config.MetadataConfig{GetBlurayInfo: true},
			},
			wantLookup:  true,
			wantEnabled: true,
		},
		{
			name: "description link enabled",
			cfg: config.Config{
				Description: config.DescriptionSettingsConfig{AddBlurayLink: true},
			},
			wantLookup:  true,
			wantEnabled: true,
		},
		{
			name: "description images enabled",
			cfg: config.Config{
				Description: config.DescriptionSettingsConfig{UseBlurayImages: true},
			},
			wantLookup:  true,
			wantEnabled: true,
		},
		{
			name: "all bluray options disabled",
			cfg:  config.Config{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := db.Open(filepath.Join(t.TempDir(), "metadata.sqlite"))
			if err != nil {
				t.Fatalf("open repo: %v", err)
			}
			t.Cleanup(func() {
				if err := repo.Close(); err != nil {
					t.Errorf("close repo: %v", err)
				}
			})

			service := newTestService(t, repo, WithConfig(tt.cfg))
			gotEnabled := service.blurayLookupEnabled()
			if gotEnabled != tt.wantEnabled {
				t.Fatalf("blurayLookupEnabled() = %t, want %t", gotEnabled, tt.wantEnabled)
			}

			meta := preparationstate.State{
				DiscType: "BDMV",
				Identity: api.ExternalIdentity{IMDBID: 75784},
			}
			if gotLookup := service.shouldLookupBluray(meta); gotLookup != tt.wantLookup {
				t.Fatalf("shouldLookupBluray() = %t, want %t", gotLookup, tt.wantLookup)
			}
		})
	}
}

func TestReusableBlurayMetadataRefreshesCandidatesScoredWithoutBDInfo(t *testing.T) {
	service := &Service{}
	meta := preparationstate.State{ProviderMetadata: api.SourceScopedMetadata{Bluray: &api.BlurayMetadata{
		IMDBID: 1234567,
		Candidates: []api.BlurayReleaseCandidate{{
			MatchNotes: []string{"local BDInfo unavailable (-15)"},
		}},
	}}}
	if cached := service.reusableBlurayMetadata(meta, bluraycom.LookupInput{IMDBID: 1234567}); cached == nil {
		t.Fatal("candidate scored without BDInfo should remain reusable while BDInfo is unavailable")
	}
	if cached := service.reusableBlurayMetadata(meta, bluraycom.LookupInput{IMDBID: 1234567, BDInfo: &discparse.BDInfo{Playlist: "00001"}}); cached != nil {
		t.Fatal("candidate scored without BDInfo should be refreshed when the summary becomes available")
	}
	meta.ProviderMetadata.Bluray.Candidates[0].MatchNotes = []string{"video codec matches"}
	if cached := service.reusableBlurayMetadata(meta, bluraycom.LookupInput{IMDBID: 1234567, BDInfo: &discparse.BDInfo{Playlist: "00001"}}); cached == nil {
		t.Fatal("candidate scored with BDInfo should remain reusable")
	}
}

func TestBlurayInstructionSelectsFactsBeforeNaming(t *testing.T) {
	service := &Service{cfg: config.Config{Metadata: config.MetadataConfig{GetBlurayInfo: true}}, bluray: bluraycom.NewClient(nil)}
	meta := preparationstate.State{
		SourcePath:      filepath.Join(t.TempDir(), "Example.Movie.2026.COMPLETE.BLURAY-GRP"),
		DiscType:        "BDMV",
		BlurayReleaseID: "second",
		Identity:        api.ExternalIdentity{IMDBID: 1234567},
		ProviderMetadata: api.SourceScopedMetadata{Bluray: &api.BlurayMetadata{
			IMDBID:            1234567,
			SelectedReleaseID: "first",
			Candidates: []api.BlurayReleaseCandidate{
				{
					ReleaseID: "first",
					Country:   "United States",
					Region:    "USA",
					Publisher: "First Publisher",
				},
				{
					ReleaseID: "second",
					Country:   "Germany",
					Region:    "GER",
					Publisher: "Second Publisher",
				},
			},
		}},
	}
	result := service.applyBlurayMetadata(t.Context(), meta, nil)
	if result.ProviderMetadata.Bluray.SelectedReleaseID != "second" || result.Region != "GER" || result.Distributor != "SECOND PUBLISHER" {
		t.Fatalf("selected facts = %#v", result)
	}
}

func TestBlurayAutomaticSelectionIsReconsideredOnRescore(t *testing.T) {
	root := t.TempDir()
	repo, err := db.Open(filepath.Join(root, "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "Example.Movie.2026.COMPLETE.BLURAY-GRP")
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		body := ""
		switch request.URL.Path {
		case "/search/":
			body = `<div class="figure"><a class="alphaborder" href="https://www.blu-ray.com/Example/900/">Example</a><div style="font-weight: bold">Example</div></div>`
		case "/products/menu_ajax.php":
			body = `<table><tr><td><h3>4K Blu-ray Editions</h3></td></tr><tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example/101/" title="Example">Example</a></td></tr></table>`
		case "/movies/Example/101/":
			body = `<table><tr><td width="228px" style="font-size: 12px"><span class="subheading">Video</span>Codec: HEVC / H.265<br>Resolution: 2160p<br><span class="subheading">Discs</span>Single disc (1 BD-100)</td></tr></table>`
		default:
			t.Errorf("unexpected request: %s", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	service := &Service{cfg: config.Config{Metadata: config.MetadataConfig{
		GetBlurayInfo:     true,
		BlurayScore:       101,
		BluraySingleScore: 101,
	}}, bluray: bluraycom.NewClient(client)}
	meta := preparationstate.State{
		SourcePath: source,
		DiscType:   "BDMV",
		Release:    api.ReleaseInfo{Resolution: "2160p"},
		Identity:   api.ExternalIdentity{IMDBID: 1234567},
		ProviderMetadata: api.SourceScopedMetadata{Bluray: &api.BlurayMetadata{
			IMDBID:            1234567,
			SelectedReleaseID: "101",
			AutoSelected:      true,
		}},
	}
	ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
	result := service.applyBlurayMetadata(ctx, meta, nil)
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
	if result.ProviderMetadata.Bluray.SelectedReleaseID != "" || result.ProviderMetadata.Bluray.AutoSelected {
		t.Fatalf("automatic selection became manual: %#v", result.ProviderMetadata.Bluray)
	}
	initialRequests := requests
	meta.ProviderMetadata.Bluray.AutoSelected = false
	ctx, scope = evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
	result = service.applyBlurayMetadata(ctx, meta, nil)
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
	if result.ProviderMetadata.Bluray.SelectedReleaseID != "101" || result.ProviderMetadata.Bluray.AutoSelected || requests != initialRequests {
		t.Fatalf("manual selection lost or refetched: %#v, requests=%d", result.ProviderMetadata.Bluray, requests)
	}
}

func TestRetainedBlurayCountrySelectionAndCalendarDates(t *testing.T) {
	repo, source := openProviderEvidenceStore(t)
	source = filepath.Join(filepath.Dir(source), "Example.Movie.2026.GER.BluRay-GRP")
	metadata := api.SourceScopedMetadata{SourcePath: source, Bluray: &api.BlurayMetadata{
		IMDBID:            1234567,
		AutoSelected:      true,
		SelectedReleaseID: "usa",
		Candidates: []api.BlurayReleaseCandidate{
			{
				ReleaseID:   "usa",
				Country:     "United States",
				Region:      "USA",
				Score:       100,
				Accepted:    true,
				ReleaseDate: "2020-12-01",
			},
			{
				ReleaseID:   "ger",
				Country:     "Germany",
				Region:      "GER",
				Score:       95,
				ReleaseDate: "2024-02-29",
			},
		},
	}}
	if err := repo.SaveExternalMetadata(t.Context(), metadata); err != nil {
		t.Fatal(err)
	}
	retained, err := repo.GetExternalMetadata(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{cfg: config.Config{Metadata: config.MetadataConfig{
		GetBlurayInfo:     true,
		BlurayScore:       90,
		BluraySingleScore: 90,
	}}, bluray: bluraycom.NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("retained candidates triggered a network request")
		return nil, nil
	})})}
	result := service.applyBlurayMetadata(t.Context(), preparationstate.State{
		SourcePath:       source,
		DiscType:         "BDMV",
		Identity:         api.ExternalIdentity{IMDBID: 1234567},
		ProviderMetadata: retained,
	}, nil)
	if result.ProviderMetadata.Bluray.SelectedReleaseID != "ger" || result.Region != "GER" || len(result.ProviderMetadata.Bluray.Candidates) != 1 || result.ProviderMetadata.Bluray.Candidates[0].ReleaseDate != "2024-02-29" {
		t.Fatalf("retained match = %#v", result.ProviderMetadata.Bluray)
	}
	if metadata.Bluray.Candidates[0].ReleaseDate != "2020-12-01" {
		t.Fatal("original date mutated")
	}
}

func TestBlurayCountryCorrectionsReconsiderFilteredCatalogues(t *testing.T) {
	for _, retainedPages := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained_pages_%t", retainedPages), func(t *testing.T) {
			repo, source := openProviderEvidenceStore(t)
			source = filepath.Join(filepath.Dir(source), "Example.Movie.2026.USA.BluRay-GRP")
			requests := 0
			client := bluraycom.NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				var body string
				switch r.URL.Path {
				case "/search/":
					body = `<div class="figure"><a class="alphaborder" href="https://www.blu-ray.com/Example/900/">Example</a></div>`
				case "/products/menu_ajax.php":
					body = `<h3>Blu-ray Editions</h3><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example/101/">Example USA</a><img width="18" height="12" title="Germany"><a href="https://www.blu-ray.com/movies/Example/102/">Example Germany</a>`
				default:
					body = `<td width="228px" style="font-size: 12px"><span class="subheading">Video</span>Codec: AVC<br>Resolution: 1080p<br><span class="subheading">Discs</span>Single disc (1 BD-25)</td>`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    r,
				}, nil
			})})
			service := &Service{cfg: config.Config{Metadata: config.MetadataConfig{
				GetBlurayInfo:     true,
				BlurayScore:       90,
				BluraySingleScore: 100,
			}}, bluray: client}
			meta := preparationstate.State{
				SourcePath: source,
				DiscType:   "BDMV",
				Identity:   api.ExternalIdentity{IMDBID: 1234567},
				ProviderMetadata: api.SourceScopedMetadata{SourcePath: source, Bluray: &api.BlurayMetadata{
					SourcePath:        source,
					IMDBID:            1234567,
					Country:           "United States",
					AutoSelected:      true,
					SelectedReleaseID: "101",
					Candidates: []api.BlurayReleaseCandidate{{
						ReleaseID: "101",
						Country:   "United States",
						Region:    "USA",
						Score:     100,
						Accepted:  true,
					}},
				}},
			}
			for index, tc := range []struct {
				correction                *string
				country, selected, region string
				count                     int
			}{
				{new("GER"), "Germany", "102", "GER", 1},
				{nil, "United States", "101", "USA", 1},
				{new(""), "", "", "", 2},
				{new("XYZ"), "", "", "XYZ", 2},
				{new("JPN"), "Japan", "", "JPN", 0},
			} {
				meta.ReleaseNameOverrides.Region = tc.correction
				ctx := t.Context()
				var scope *evidence.Scope
				if retainedPages {
					ctx, scope = evidence.WithScope(ctx, repo, source, "synthetic-fingerprint", api.ExternalFreshnessReuse, nil)
				}
				meta = service.applyBlurayMetadata(ctx, meta, nil)
				if scope != nil && scope.Err() != nil {
					t.Fatal(scope.Err())
				}
				applyReleaseNameValueOverrides(&meta)
				got := meta.ProviderMetadata.Bluray
				if got.Country != tc.country || got.SelectedReleaseID != tc.selected || len(got.Candidates) != tc.count || meta.Region != tc.region {
					t.Fatalf("correction %d: country=%q selected=%q count=%d region=%q", index, got.Country, got.SelectedReleaseID, len(got.Candidates), meta.Region)
				}
				if retainedPages && requests != 4 {
					t.Fatalf("country correction refetched retained pages: %d requests", requests)
				}
				if err := repo.SaveExternalMetadata(ctx, meta.ProviderMetadata); err != nil {
					t.Fatal(err)
				}
				var err error
				meta.ProviderMetadata, err = repo.GetExternalMetadata(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestBlurayCountryLockSurvivesUnavailableLookup(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, oldCountry := range []string{"", "United States"} {
			t.Run(fmt.Sprintf("enabled_%t_previous_%s", enabled, oldCountry), func(t *testing.T) {
				repo, source := openProviderEvidenceStore(t)
				source = filepath.Join(filepath.Dir(source), "Example.Movie.2026.GER.BluRay-GRP")
				requests := 0
				client := bluraycom.NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					requests++
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("unavailable")),
						Request:    r,
					}, nil
				})})
				candidates := []api.BlurayReleaseCandidate{{
					ReleaseID: "usa",
					Country:   "United States",
					Region:    "USA",
					Score:     100,
					Accepted:  true,
				}}
				if oldCountry == "" {
					candidates = append(candidates, api.BlurayReleaseCandidate{
						ReleaseID: "ger",
						Country:   "Germany",
						Region:    "GER",
						Score:     20,
					})
				}
				meta := preparationstate.State{
					SourcePath: source,
					DiscType:   "BDMV",
					Region:     "USA",
					Identity:   api.ExternalIdentity{IMDBID: 1234567},
					ProviderMetadata: api.SourceScopedMetadata{Bluray: &api.BlurayMetadata{
						Country:           oldCountry,
						IMDBID:            1234567,
						AutoSelected:      true,
						SelectedReleaseID: "usa",
						Candidates:        candidates,
					}},
				}
				service := &Service{cfg: config.Config{Metadata: config.MetadataConfig{GetBlurayInfo: enabled}}, bluray: client}
				ctx, _ := evidence.WithScope(t.Context(), repo, source, "synthetic-fingerprint", api.ExternalFreshnessReuse, nil)
				result := service.applyBlurayMetadata(ctx, meta, nil)
				got := result.ProviderMetadata.Bluray
				if oldCountry != "" {
					if got != nil {
						t.Fatalf("incomplete other-country catalogue survived: %#v", got)
					}
				} else if got == nil || got.Country != "Germany" || len(got.Candidates) != 1 || got.SelectedReleaseID != "ger" || result.Region != "GER" {
					t.Fatalf("available retained country match lost: %#v", got)
				}
				if !enabled && requests != 0 {
					t.Fatalf("disabled lookup performed %d requests", requests)
				}
			})
		}
	}
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
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

			service := NewService(repo, WithConfig(tt.cfg))
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
	if cached := service.reusableBlurayMetadata(meta, 1234567, nil); cached == nil {
		t.Fatal("candidate scored without BDInfo should remain reusable while BDInfo is unavailable")
	}
	if cached := service.reusableBlurayMetadata(meta, 1234567, &discparse.BDInfo{Playlist: "00001"}); cached != nil {
		t.Fatal("candidate scored without BDInfo should be refreshed when the summary becomes available")
	}
	meta.ProviderMetadata.Bluray.Candidates[0].MatchNotes = []string{"video codec matches"}
	if cached := service.reusableBlurayMetadata(meta, 1234567, &discparse.BDInfo{Playlist: "00001"}); cached == nil {
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
					Region:    "A",
					Publisher: "First Publisher",
				},
				{
					ReleaseID: "second",
					Region:    "B",
					Publisher: "Second Publisher",
				},
			},
		}},
	}
	result := service.applyBlurayMetadata(t.Context(), meta, nil)
	if result.ProviderMetadata.Bluray.SelectedReleaseID != "second" || result.Region != "B" || result.Distributor != "SECOND PUBLISHER" {
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

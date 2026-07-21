// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tik

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileResolvers(t *testing.T) {
	profile := Profile().Site
	if got := profile.ResolveTypeID(api.UploadSubject{ReleaseName: "Example.Release.2026.BD50.COMPLETE-GRP", DiscType: "BDMV"}); got != "5" {
		t.Fatalf("BD50 type = %q", got)
	}
	disc := "BD66"
	if got := profile.ResolveTypeID(api.UploadSubject{TrackerSiteOverrides: api.TrackerSiteOverrides{TIK: api.TIKOverrides{DiscType: &disc}}}); got != "4" {
		t.Fatalf("override type = %q", got)
	}
	foreign := api.UploadSubject{Identity: api.ExternalIdentity{Category: "MOVIE"}, ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "fr"}}}
	if got := profile.ResolveCategoryID(foreign); got != "3" {
		t.Fatalf("foreign category = %q", got)
	}
	asian := api.UploadSubject{
		Identity:          api.ExternalIdentity{Category: "MOVIE"},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "en", OriginCountry: []string{"JP"}}},
	}
	if got := profile.ResolveCategoryID(asian); got != "6" {
		t.Fatalf("Asian category = %q", got)
	}
	opera := api.UploadSubject{
		Identity:          api.ExternalIdentity{Category: "TV"},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		Release:           api.ReleaseInfo{Genre: "Opera"},
	}
	if got := profile.ResolveCategoryID(opera); got != "5" {
		t.Fatalf("opera category = %q", got)
	}
}

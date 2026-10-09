// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNormalizeTitleProviderSeparatesPresentation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		provider string
		edition  string
	}{
		{
			name:     "Example.Movie.2026.IMAX.2160p.DSNP.WEB-DL.H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{
			name:     "Example Movie 2026 IMAX Hybrid REPACK 2160p DSNP WEB-DL DD+ 5.1 Atmos DV HDR H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{
			name:     "Example.Movie.2026.IMAX.2160p.DSN+.WEB-DL.H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{
			name:     "Example.Movie.2026.IMAX.2160p.HTSR.WEB-DL.H.265-GRP",
			provider: "htsr",
			edition:  "imax",
		},
		{name: "Example.Movie.2026.Hybrid.2160p.DSNP.WEB-DL.H.265-GRP", provider: "dsnp"},
		{
			name:     "Example.Movie.2026.DSNP.IMAX.2160p.WEB-DL.H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{name: "Example.Movie.2026.IMAX.2160p.WEB-DL.H.265-GRP", edition: "imax"},
		{name: "Example.Movie.2026.Hybrid.2160p.WEB-DL.H.265-GRP"},
		{name: "Example.Movie.2026.IMAX.Hybrid.2160p.BluRay.REMUX.HEVC-GRP", edition: "imax"},
		{name: "Example.Movie.2026.2160p.WEB-DL.DSNP.H.265-GRP", provider: "dsnp"},
		{name: "The.Amazon.2026.2160p.WEB-DL.H.265-GRP"},
		{name: "Example.Movie.2026.1080p.iP.WEB-DL.H.264-GRP", provider: "ip"},
		{name: "Example.Movie.2026.1080p.iPlayer.WEB-DL.H.264-GRP", provider: "ip"},
		{name: "Example.Movie.2026.1080p.BRAV.WEB-DL.H.264-GRP", provider: "brav"},
		{name: "Example.Movie.2026.1080p.BRAVO.WEB-DL.H.264-GRP", provider: "brav"},
		{name: "Example.Movie.2026.1080p.YT.WEB-DL.H.264-GRP", provider: "yt"},
		{name: "Example.Movie.2026.1080p.YOUTUBE.WEB-DL.H.264-GRP", provider: "yt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{Name: test.name}, "LST"))
			target := normalizeTargetFacts(api.TrackerDuplicateTarget{Names: []string{test.name}})
			for _, facts := range []normalizedFacts{candidate, target} {
				if facts.Provider.Value != test.provider || facts.Edition.Value != test.edition {
					t.Fatalf("provider=%#v edition=%#v; want provider=%q edition=%q", facts.Provider, facts.Edition, test.provider, test.edition)
				}
				if test.provider == "" {
					if facts.Provider.Status != FactMissing {
						t.Fatalf("absent provider = %#v", facts.Provider)
					}
				} else if facts.Provider.Status != FactPartial || !slices.Equal(facts.Provider.SourceFields, []string{"title"}) {
					t.Fatalf("title provider provenance = %#v", facts.Provider)
				}
			}
			if test.provider != "" && (candidate.Provider.Origin != FactOriginTrackerTitle || target.Provider.Origin != FactOriginContentName) {
				t.Fatalf("candidate origin=%s target origin=%s", candidate.Provider.Origin, target.Provider.Origin)
			}
		})
	}
}

func TestNormalizeStructuredProviderWithPresentationTitle(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		provider string
		want     string
		status   FactStatus
	}{
		{
			name:     "Example.Movie.2026.IMAX.Hybrid.2160p.DSNP.WEB-DL.HEVC-GRP",
			provider: "DSNP",
			want:     "dsnp",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.2160p.YT.WEB-DL.HEVC-GRP",
			provider: "YT",
			want:     "yt",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.YOUTUBE.WEB-DL.H.264-GRP",
			provider: "YT",
			want:     "yt",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.YT.WEB-DL.H.264-GRP",
			provider: "YOUTUBE",
			want:     "yt",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.iPlayer.WEB-DL.H.264-GRP",
			provider: "iP",
			want:     "ip",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.iP.WEB-DL.H.264-GRP",
			provider: "iPlayer",
			want:     "ip",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.BRAVO.WEB-DL.H.264-GRP",
			provider: "BRAV",
			want:     "brav",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.BRAV.WEB-DL.H.264-GRP",
			provider: "BRAVO",
			want:     "brav",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.iTunes.WEB-DL.H.264-GRP",
			provider: "iT",
			want:     "it",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.ABC.WEB-DL.H.264-GRP",
			provider: "AMBC",
			want:     "ambc",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.STARZ.WEB-DL.H.264-GRP",
			provider: "STARZ",
			want:     "starz",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.STZ.WEB-DL.H.264-GRP",
			provider: "STZ",
			want:     "stz",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.VMEO.WEB-DL.H.264-GRP",
			provider: "VMEO",
			want:     "vmeo",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.1080p.Criterion.Collection.WEB-DL.H.264-GRP",
			provider: "CRIT",
			want:     "crit",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.Hybrid.2160p.WEB-DL.HEVC-GRP",
			provider: "PROVIDER_A",
			want:     "providera",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.Hybrid.2160p.DSNP.WEB-DL.HEVC-GRP",
			provider: "AMZN",
			want:     "amzn",
			status:   FactContradictory,
		},
		{
			name:     "Example.Movie.2026.1080p.iTunes.WEB-DL.H.264-GRP",
			provider: "iTunes",
			want:     "it",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.1080p.ABC.WEB-DL.H.264-GRP",
			provider: "ABC",
			want:     "ambc",
			status:   FactComplete,
		},
	} {
		t.Run(test.provider+"/"+test.name, func(t *testing.T) {
			t.Parallel()
			candidate := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{Name: test.name, Provider: test.provider}, "LST"))
			target := normalizeTargetFacts(api.TrackerDuplicateTarget{Names: []string{test.name}, Provider: test.provider})
			for _, facts := range []normalizedFacts{candidate, target} {
				if facts.Provider.Value != test.want || facts.Provider.Status != test.status {
					t.Fatalf("structured provider = %#v; want %s/%s", facts.Provider, test.want, test.status)
				}
				if test.status == FactContradictory && !slices.Equal(facts.Provider.Contradictions, []string{"dsnp"}) {
					t.Fatalf("real provider contradiction = %#v", facts.Provider)
				}
			}
			if candidate.Provider.Origin != FactOriginTrackerAPI || target.Provider.Origin != FactOriginTargetMedia {
				t.Fatalf("candidate origin=%s target origin=%s", candidate.Provider.Origin, target.Provider.Origin)
			}
		})
	}
}

func TestCanonicalProviderServiceIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: " Disney ", want: "dsny"},
		{value: "Disney+", want: "dsnp"},
		{value: "Apple TV", want: "atv"},
		{value: "Apple TV+", want: "atvp"},
		{value: "STARZ", want: "starz"},
		{value: "Starz", want: "starz"},
		{value: "STZ", want: "stz"},
		{value: "VIMEO", want: "vimeo"},
		{value: "Vimeo", want: "vimeo"},
		{value: "VMEO", want: "vmeo"},
		{value: "YT", want: "yt"},
		{value: "YouTube", want: "yt"},
		{value: "BBC iPlayer", want: "ip"},
		{value: "iPlayer", want: "ip"},
		{value: "BRAVO", want: "brav"},
		{value: "BravoTV", want: "brav"},
		{value: "iTunes", want: "it"},
		{value: "ABC", want: "ambc"},
		{value: "Criterion.Collection", want: "crit"},
		{value: "PROVIDER_A", want: "providera"},
		{value: "PROVIDER_B", want: "providerb"},
		{},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			if got := canonicalProvider(test.value); got != test.want {
				t.Fatalf("canonicalProvider(%q) = %q, want %q", test.value, got, test.want)
			}
			if got := canonicalProvider(test.want); got != test.want {
				t.Fatalf("canonicalProvider(%q) = %q; want stable identity", test.want, got)
			}
		})
	}
}

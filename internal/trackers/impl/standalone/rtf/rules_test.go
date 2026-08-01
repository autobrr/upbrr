// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMinimumContentAgeRuleIsStrict(t *testing.T) {
	t.Parallel()

	policy := Profile().ValidationPolicy
	if policy.Check == nil {
		t.Fatal("RTF minimum content age rule is not registered")
	}
	if policy.ID != "standalone-rtf-constructibility-v2" {
		t.Fatalf("RTF validation policy ID = %q", policy.ID)
	}
	failures, err := policy.Check(context.Background(), api.TrackerValidationSubject{
		Release: api.ReleaseInfo{Year: 9999},
	}, nil)
	if err != nil {
		t.Fatalf("check RTF rules: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("RTF age failures = %#v, want one", failures)
	}
	failure := failures[0]
	if failure.Rule != "minimum_content_age" || failure.Reason != minimumContentAgeReason || failure.Disposition != api.RuleDispositionStrict {
		t.Fatalf("RTF age failure = %#v, want strict minimum_content_age", failure)
	}
}

func TestMinimumContentAgeBoundary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	if minimumContentAgeViolation(api.TrackerValidationSubject{
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{ReleaseDate: "2016-06-18"}},
	}, now) {
		t.Fatal("content on the age boundary was rejected")
	}
	if !minimumContentAgeViolation(api.TrackerValidationSubject{
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{ReleaseDate: "2016-06-19"}},
	}, now) {
		t.Fatal("content newer than the age boundary was allowed")
	}
}

func TestMinimumContentAgeYearFallback(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	if !minimumContentAgeViolation(api.TrackerValidationSubject{
		Release: api.ReleaseInfo{Year: 2016},
	}, now) {
		t.Fatal("content in the boundary year automatically qualified")
	}
	if minimumContentAgeViolation(api.TrackerValidationSubject{
		Release: api.ReleaseInfo{Year: 2015},
	}, now) {
		t.Fatal("content older than the boundary year was rejected")
	}
	if !minimumContentAgeViolation(api.TrackerValidationSubject{
		Release: api.ReleaseInfo{Year: 2017},
	}, now) {
		t.Fatal("content newer than the ten-year-and-one-month fallback year was allowed")
	}
}

func TestRTFContentAgeEligibilityExactBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		now      time.Time
		metadata api.SourceScopedMetadata
		want     rtfAgeEligibilityVerdict
	}{
		{
			name: "exact ten-year-and-one-month boundary qualifies",
			now:  time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2016-06-18"},
			},
			want: rtfAgeEligible,
		},
		{
			name: "day after exact boundary is too new",
			now:  time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2016-06-19"},
			},
			want: rtfAgeIneligibleTooNew,
		},
		{
			name: "leap day is too new before the extra month",
			now:  time.Date(2026, time.March, 28, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2016-02-29"},
			},
			want: rtfAgeIneligibleTooNew,
		},
		{
			name: "leap day qualifies after the extra month",
			now:  time.Date(2026, time.March, 29, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2016-02-29"},
			},
			want: rtfAgeEligible,
		},
		{
			name: "month subtraction boundary qualifies",
			now:  time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2014-01-29"},
			},
			want: rtfAgeEligible,
		},
		{
			name: "day after month subtraction boundary is too new",
			now:  time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2014-01-30"},
			},
			want: rtfAgeIneligibleTooNew,
		},
		{
			name: "timestamp date is normalized to UTC",
			now:  time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2016-07-18T23:30:00-10:00"},
			},
			want: rtfAgeIneligibleTooNew,
		},
		{
			name: "future exact date fails closed",
			now:  time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC),
			metadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2030-01-01"},
			},
			want: rtfAgeIneligibleTooNew,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rtfContentAgeEligibility(api.ReleaseInfo{}, tt.metadata, tt.now)
			if got != tt.want {
				t.Fatalf("eligibility = %q, want %q", got, tt.want)
			}
			if violation := minimumContentAgeViolation(api.TrackerValidationSubject{ProviderMetadata: tt.metadata}, tt.now); violation == (tt.want == rtfAgeEligible) {
				t.Fatalf("validation violation = %t for verdict %q", violation, tt.want)
			}
			if oldEnough := isRTFContentOldEnough(api.DuplicateSubject{ProviderMetadata: tt.metadata}, tt.now); oldEnough != (tt.want == rtfAgeEligible) {
				t.Fatalf("duplicate eligibility = %t for verdict %q", oldEnough, tt.want)
			}
		})
	}
}

func TestRTFContentAgeEligibilityYearEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		release api.ReleaseInfo
		want    rtfAgeEligibilityVerdict
	}{
		{
			name:    "older year qualifies",
			release: api.ReleaseInfo{Year: 2015},
			want:    rtfAgeEligible,
		},
		{
			name:    "boundary year requires exact date",
			release: api.ReleaseInfo{Year: 2016},
			want:    rtfAgeIneligibleBoundaryYear,
		},
		{
			name:    "newer year is too new",
			release: api.ReleaseInfo{Year: 2017},
			want:    rtfAgeIneligibleTooNew,
		},
		{
			name:    "future year fails closed",
			release: api.ReleaseInfo{Year: 2030},
			want:    rtfAgeIneligibleTooNew,
		},
		{
			name: "missing evidence fails closed",
			want: rtfAgeIneligibleMissingEvidence,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rtfContentAgeEligibility(tt.release, api.SourceScopedMetadata{}, now)
			if got != tt.want {
				t.Fatalf("eligibility = %q, want %q", got, tt.want)
			}
			if violation := minimumContentAgeViolation(api.TrackerValidationSubject{Release: tt.release}, now); violation == (tt.want == rtfAgeEligible) {
				t.Fatalf("validation violation = %t for verdict %q", violation, tt.want)
			}
			if oldEnough := isRTFContentOldEnough(api.DuplicateSubject{Release: tt.release}, now); oldEnough != (tt.want == rtfAgeEligible) {
				t.Fatalf("duplicate eligibility = %t for verdict %q", oldEnough, tt.want)
			}
		})
	}
}

func TestYoungestRTFReleaseEvidenceUsesEveryExactDateSource(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	youngest := time.Date(2025, time.May, 4, 0, 0, 0, 0, time.UTC)
	const youngestText = "2025-05-04"
	tests := []struct {
		name  string
		apply func(*api.ReleaseInfo, *api.SourceScopedMetadata)
	}{
		{
			name: "release facts",
			apply: func(release *api.ReleaseInfo, _ *api.SourceScopedMetadata) {
				release.Year = youngest.Year()
				release.Month = int(youngest.Month())
				release.Day = youngest.Day()
			},
		},
		{
			name: "TMDB release date",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TMDB.ReleaseDate = youngestText
			},
		},
		{
			name: "TMDB first air date",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TMDB.FirstAirDate = youngestText
			},
		},
		{
			name: "TMDB last air date",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TMDB.LastAirDate = youngestText
			},
		},
		{
			name: "IMDb episode",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.IMDB = &api.IMDBMetadata{Episodes: []api.IMDBEpisode{{
					ReleaseDate: api.IMDBReleaseDate{
						Year:  youngest.Year(),
						Month: int(youngest.Month()),
						Day:   youngest.Day(),
					},
				}}}
			},
		},
		{
			name: "TVDB series",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TVDB = &api.TVDBMetadata{FirstAired: youngestText}
			},
		},
		{
			name: "TVDB selected episode",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TVDB = &api.TVDBMetadata{EpisodeAired: youngestText}
			},
		},
		{
			name: "TVDB episode list",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TVDB = &api.TVDBMetadata{Episodes: []api.TVDBEpisodeMetadata{{EpisodeAired: youngestText}}}
			},
		},
		{
			name: "TVmaze premiere",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TVmaze = &api.TVmazeMetadata{Premiered: youngestText}
			},
		},
		{
			name: "TVmaze end",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TVmaze = &api.TVmazeMetadata{Ended: youngestText}
			},
		},
		{
			name: "AniList start",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.AniList = &api.AniListMetadata{StartDate: youngestText}
			},
		},
		{
			name: "AniList end",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.AniList = &api.AniListMetadata{EndDate: youngestText}
			},
		},
		{
			name: "AniList next airing episode",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.AniList = &api.AniListMetadata{
					NextAiringEpisode: api.AniListAiringEpisode{AiringAt: int(youngest.Unix())},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			release := api.ReleaseInfo{}
			metadata := api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{ReleaseDate: "2010-01-01"},
			}
			tt.apply(&release, &metadata)

			evidence := youngestRTFReleaseEvidence(release, metadata)
			got, ok := evidence.exactDate()
			if !ok || !got.Equal(youngest) {
				t.Fatalf("youngest exact date = (%s, %t), want (%s, true)", got.Format(time.DateOnly), ok, youngestText)
			}
			if !minimumContentAgeViolation(api.TrackerValidationSubject{
				Release:          release,
				ProviderMetadata: metadata,
			}, now) {
				t.Fatal("youngest exact release date did not enforce the age rule")
			}
		})
	}
}

func TestYoungestRTFReleaseEvidenceUsesEveryYearSource(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	const youngestYear = 2025
	tests := []struct {
		name  string
		apply func(*api.ReleaseInfo, *api.SourceScopedMetadata)
	}{
		{
			name: "release facts",
			apply: func(release *api.ReleaseInfo, _ *api.SourceScopedMetadata) {
				release.Year = youngestYear
			},
		},
		{
			name: "TMDB",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TMDB = &api.TMDBMetadata{Year: youngestYear}
			},
		},
		{
			name: "IMDb",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.IMDB = &api.IMDBMetadata{EndYear: youngestYear}
			},
		},
		{
			name: "IMDb episode",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.IMDB = &api.IMDBMetadata{Episodes: []api.IMDBEpisode{{ReleaseYear: youngestYear}}}
			},
		},
		{
			name: "TVDB",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TVDB = &api.TVDBMetadata{Year: youngestYear}
			},
		},
		{
			name: "TVmaze partial date",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.TVmaze = &api.TVmazeMetadata{Ended: "2025"}
			},
		},
		{
			name: "AniList",
			apply: func(_ *api.ReleaseInfo, metadata *api.SourceScopedMetadata) {
				metadata.AniList = &api.AniListMetadata{SeasonYear: youngestYear}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			release := api.ReleaseInfo{}
			metadata := api.SourceScopedMetadata{}
			tt.apply(&release, &metadata)

			evidence := youngestRTFReleaseEvidence(release, metadata)
			if evidence.year != youngestYear {
				t.Fatalf("youngest year = %d, want %d", evidence.year, youngestYear)
			}
			if _, ok := evidence.exactDate(); ok {
				t.Fatal("year-only evidence was treated as an exact date")
			}
			if !minimumContentAgeViolation(api.TrackerValidationSubject{
				Release:          release,
				ProviderMetadata: metadata,
			}, now) {
				t.Fatal("youngest release year did not enforce the age rule")
			}
		})
	}
}

func TestRTFAgeChecksUseYoungerYearOverOlderExactDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	metadata := api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{ReleaseDate: "2010-01-01"},
		IMDB: &api.IMDBMetadata{Year: 2025},
	}
	evidence := youngestRTFReleaseEvidence(api.ReleaseInfo{}, metadata)
	if evidence.year != 2025 {
		t.Fatalf("youngest year = %d, want 2025", evidence.year)
	}
	if _, ok := evidence.exactDate(); ok {
		t.Fatal("older exact date won over younger year evidence")
	}
	if !minimumContentAgeViolation(api.TrackerValidationSubject{ProviderMetadata: metadata}, now) {
		t.Fatal("validation allowed content with younger year evidence")
	}
	if isRTFContentOldEnough(api.DuplicateSubject{ProviderMetadata: metadata}, now) {
		t.Fatal("duplicate search allowed content with younger year evidence")
	}
}

func TestRTFAgeChecksUseYoungerExactDateOverOlderYear(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	metadata := api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Year: 2010},
		TVDB: &api.TVDBMetadata{EpisodeAired: "2016-07-19"},
	}
	evidence := youngestRTFReleaseEvidence(api.ReleaseInfo{}, metadata)
	got, ok := evidence.exactDate()
	if !ok || got.Format(time.DateOnly) != "2016-07-19" {
		t.Fatalf("youngest exact date = (%s, %t), want (2016-07-19, true)", got.Format(time.DateOnly), ok)
	}
	if !minimumContentAgeViolation(api.TrackerValidationSubject{ProviderMetadata: metadata}, now) {
		t.Fatal("validation allowed younger exact date over older year evidence")
	}
	if isRTFContentOldEnough(api.DuplicateSubject{ProviderMetadata: metadata}, now) {
		t.Fatal("duplicate search allowed younger exact date over older year evidence")
	}
}

func TestRTFReleaseEvidenceIgnoresBlurayMetadata(t *testing.T) {
	t.Parallel()

	evidence := youngestRTFReleaseEvidence(api.ReleaseInfo{Year: 2010}, api.SourceScopedMetadata{
		Bluray: &api.BlurayMetadata{
			SelectedReleaseID: "selected",
			Candidates: []api.BlurayReleaseCandidate{{
				ReleaseID: "selected",
				MovieYear: "2025",
			}},
		},
	})
	if evidence.year != 2010 {
		t.Fatalf("youngest year = %d, want canonical year 2010", evidence.year)
	}
}

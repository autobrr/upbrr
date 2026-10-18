// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (d *Definition) prepareDryRun(ctx context.Context, input trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	input.Intent = trackers.PreparationIntentDryRun
	plan, failure := d.Prepare(ctx, input)
	if failure != nil {
		return api.TrackerDryRunEntry{}, failure
	}
	return plan.DryRun(), nil
}

func TestDefinitionBuildUploadDryRunIncludesQuestionnaire(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "ANT",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{Genres: "Adult", Keywords: "adult"},
			},
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Questionnaire == nil {
		t.Fatal("expected questionnaire")
	}
	if got := len(entry.Questionnaire.Fields); got != 3 {
		t.Fatalf("expected 3 questionnaire fields, got %d", got)
	}
	if entry.Questionnaire.Fields[0].Key != "tags" {
		t.Fatalf("expected tags field first, got %q", entry.Questionnaire.Fields[0].Key)
	}
	if entry.Questionnaire.Fields[1].Kind != "select" {
		t.Fatalf("expected adult screens field to use select control, got %q", entry.Questionnaire.Fields[1].Kind)
	}
}

func TestBuildQuestionnaireIncludesTypeOptionsWhenTypeIsUnresolved(t *testing.T) {
	t.Parallel()

	questionnaire := buildQuestionnaire(api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}}, uploadState{})
	if questionnaire == nil || len(questionnaire.Fields) == 0 {
		t.Fatal("expected unresolved type questionnaire field")
	}
	field := questionnaire.Fields[0]
	if field.Key != "type" || field.Kind != "select" {
		t.Fatalf("unexpected type field: %#v", field)
	}
	if got := len(field.Options); got != 2 {
		t.Fatalf("expected 2 type options, got %d", got)
	}
}

func TestFilmTypeAnswersRequireMovieEligibility(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		answer   string
		wantID   string
		category api.CanonicalCategory
		tmdbType string
		imdb     *api.IMDBMetadata
	}{
		{
			name:     "movie short without IMDb",
			answer:   "Short Film",
			wantID:   "1",
			category: api.CanonicalCategoryMovie,
			tmdbType: "Movie",
		},
		{
			name:     "movie feature with missing IMDb type",
			answer:   "Feature Film",
			wantID:   "0",
			category: api.CanonicalCategoryMovie,
			tmdbType: "Movie",
			imdb:     &api.IMDBMetadata{},
		},
		{
			name:     "TMDB television cannot become a movie short",
			answer:   "Short Film",
			category: api.CanonicalCategoryTV,
			tmdbType: "Scripted",
		},
		{
			name:     "TMDB miniseries cannot become a feature film",
			answer:   "Feature Film",
			category: api.CanonicalCategoryTV,
			tmdbType: "Miniseries",
			imdb:     &api.IMDBMetadata{},
		},
		{
			name:     "TMDB television overrides conflicting movie evidence",
			answer:   "Short Film",
			category: api.CanonicalCategoryMovie,
			tmdbType: "Scripted",
			imdb:     &api.IMDBMetadata{Type: "short"},
		},
		{
			name:     "TMDB TV category cannot be relabeled without programme type",
			answer:   "Feature Film",
			category: api.CanonicalCategoryTV,
			imdb:     &api.IMDBMetadata{Type: "tv movie"},
		},
		{
			name:     "made for TV movie uses a film TMDB record",
			answer:   "Feature Film",
			wantID:   "0",
			category: api.CanonicalCategoryMovie,
			tmdbType: "Movie",
			imdb:     &api.IMDBMetadata{Type: "tv movie"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tmp := t.TempDir()
			mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
			torrentPath := filepath.Join(tmp, "Example.torrent")
			for path, content := range map[string]string{mediaInfoPath: "General\nUnique ID : 123", torrentPath: "dummy"} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			input := trackers.PreparationInput{
				Tracker: "ANT",
				Meta: api.UploadSubject{
					ReleaseName:       "Example.Release.2026.1080p.WEB-DL.x264-GRP",
					Release:           api.ReleaseInfo{Title: "Example Release", Year: 2026},
					SourcePath:        filepath.Join(tmp, "Example.mkv"),
					TorrentPath:       torrentPath,
					MediaInfoTextPath: mediaInfoPath,
					Identity:          api.ExternalIdentity{Category: test.category, TMDBID: 123},
					ProviderMetadata: api.SourceScopedMetadata{
						IMDB: test.imdb,
						TMDB: &api.TMDBMetadata{
							TMDBID:   123,
							Category: string(test.category),
							TMDBType: test.tmdbType,
							Title:    "Example Release",
							Year:     2026,
							Genres:   "Drama",
						},
					},
				},
				TrackerConfig: config.TrackerConfig{APIKey: "token"},
				Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{}),
				Logger:        api.NopLogger{},
			}
			questionnaire := projectionQuestionnaire(input)
			if slices.ContainsFunc(questionnaire.Fields, func(field api.TrackerQuestionnaireField) bool {
				return field.Key == "type"
			}) {
				t.Fatalf("automatic movie types and ineligible TV must not prompt for a type: %#v", questionnaire)
			}
			input.Meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"ANT": {"type": test.answer}}
			projection, failure := registry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			if test.wantID == "" {
				if projection.DupeReady || projection.UploadReady || !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
					return decision.Code == "unsupported_type" && decision.Blocking
				}) {
					t.Fatalf("TV identity must remain ineligible despite a film answer: %+v", projection)
				}
				if slices.ContainsFunc(projection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool {
					return field.Key == "type"
				}) {
					t.Fatalf("unsupported TV must not offer a type override: %+v", projection.Questionnaire)
				}
				return
			}
			if !projection.DupeReady || !projection.UploadReady || len(projection.RequiredActions) != 0 {
				t.Fatalf("supported type answer must clear readiness: %+v", projection)
			}
			input.Projection = &projection
			entry, err := New().prepareDryRun(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if entry.Payload["type"] != test.wantID || entry.Questionnaire.Fields[0].Value != test.answer {
				t.Fatalf("type answer did not reach payload and questionnaire: %#v / %#v", entry.Payload, entry.Questionnaire)
			}
		})
	}
}

func TestDefinitionBuildUploadDryRunUsesIMDbGenresWhenTMDBGenresAreMissing(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "ANT",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
			ProviderMetadata: api.SourceScopedMetadata{
				IMDB: &api.IMDBMetadata{Genres: "Action, Drama"},
			},
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := entry.Payload["flagchangereason"]; got != "" {
		t.Fatalf("automatic IMDb genres should not request manual tags, got %q", got)
	}
	if got := entry.Payload["tags"]; got != "action,drama" {
		t.Fatalf("expected automatic IMDb genre tags, got %q", got)
	}
}

func TestDefinitionBuildUploadDryRunUsesBDInfoForBDMV(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "db.sqlite")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	sourcePath := filepath.Join(tmp, "Movie", "BDMV")
	if err := os.MkdirAll(sourcePath, 0o700); err != nil {
		t.Fatalf("create source path: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	meta := api.UploadSubject{
		SourcePath:            sourcePath,
		TorrentPath:           torrentPath,
		DiscType:              "BDMV",
		Source:                "Blu-ray",
		SelectedBDMVPlaylists: []api.PlaylistInfo{{File: "00001.MPLS"}},
		Identity:              api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{Genres: "Action"},
		},
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"ANT": {"type": "Feature Film"},
		},
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(filepath.Join(tmp, "tmp"), sourcePath, meta.Release)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	bdinfoPath := paths.BDMVSummaryPath(tmpDir, "00001.MPLS")
	if err := os.WriteFile(bdinfoPath, []byte("BDINFO_CONTENT"), 0o600); err != nil {
		t.Fatalf("write bdinfo: %v", err)
	}

	entry, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker:       "ANT",
		Meta:          meta,
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := entry.Payload["bdinfo"]; got != "BDINFO_CONTENT" {
		t.Fatalf("expected BDInfo payload, got %q", got)
	}
	if got := entry.Payload["container_type"]; got != "m2ts" {
		t.Fatalf("expected m2ts container type, got %q", got)
	}
	if _, ok := entry.Payload["mediainfo"]; ok {
		t.Fatalf("expected BDMV upload to omit mediainfo, got %#v", entry.Payload)
	}
	if got := entry.Payload["media"]; got != "BluRay" {
		t.Fatalf("media = %q, want BluRay", got)
	}
}

func TestResolveFlagsIncludesIMAXAndCriterionEdition(t *testing.T) {
	t.Parallel()

	flags := resolveFlags(api.UploadSubject{Edition: "IMAX Criterion Collection"})
	if !containsString(flags, "IMAX") {
		t.Fatalf("expected IMAX flag, got %#v", flags)
	}
	if !containsString(flags, "Criterion") {
		t.Fatalf("expected Criterion flag from edition, got %#v", flags)
	}
}

func TestResolveReleaseGroupBansUpdatedGroups(t *testing.T) {
	t.Parallel()

	for _, group := range []string{"4K4U", "AOC", "EVO", "Flights", "iVy", "MezRips", "QxR", "tigole", "x0r"} {
		if got, ok := resolveReleaseGroup(group); ok || got != "" {
			t.Fatalf("expected %s to be banned, got %q ok=%t", group, got, ok)
		}
	}
	for _, group := range []string{"SM737", "GRP"} {
		if got, ok := resolveReleaseGroup(group); !ok || got != group {
			t.Fatalf("expected unlisted group %s to be allowed, got %q ok=%t", group, got, ok)
		}
	}
}

func TestBuildDescriptionRemovesSelectedScreenshotBlockAndDefaultSignature(t *testing.T) {
	description := requireDescription(t, trackers.DescriptionAssets{
		Description: `[align=center]
[url=https://pixhost.to/fv71hr.png][img width=350]https://pixhost.to/fv71hr.png[/img][/url]
[/align]

[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`,
		Screenshots: []api.ScreenshotImage{{RawURL: "https://pixhost.to/fv71hr.png"}},
	})
	if strings.TrimSpace(description) != "" {
		t.Fatalf("expected screenshot-only/signature-only description removed, got %q", description)
	}
}

func TestBuildDescriptionRemovesKnownSignatures(t *testing.T) {
	for _, footer := range []string{"[right]Created by Upload Assistant[/right]", "[img]https://files.catbox.moe/5izwmx.svg[/img]"} {
		for _, notes := range []string{"", "[b]Release notes[/b]\n"} {
			input := notes + footer
			got := requireDescription(t, trackers.DescriptionAssets{Description: input})
			if strings.Contains(got, "Upload Assistant") || strings.Contains(got, "5izwmx.svg") || notes != "" && !strings.Contains(got, "[b]Release notes[/b]") {
				t.Fatalf("unexpected cleaned description %q", got)
			}
			if final := requireDescription(t, trackers.DescriptionAssets{Description: input, Final: true}); final != input {
				t.Fatalf("final description changed: %q", final)
			}
		}
	}
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

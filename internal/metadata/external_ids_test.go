// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	"github.com/autobrr/upbrr/pkg/api"
)

type fakeRepo struct {
	ids               api.ExternalIDs
	meta              api.ExternalMetadata
	trackerMetadata   []api.TrackerMetadata
	trackerTimestamps []api.TrackerTimestamp
}

func (f *fakeRepo) GetByPath(ctx context.Context, path string) (api.FileMetadata, error) {
	return api.FileMetadata{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) Save(ctx context.Context, metadata api.FileMetadata) error {
	return nil
}

func (f *fakeRepo) GetExternalIDs(ctx context.Context, path string) (api.ExternalIDs, error) {
	return api.ExternalIDs{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveExternalIDs(ctx context.Context, ids api.ExternalIDs) error {
	f.ids = ids
	return nil
}

func (f *fakeRepo) GetExternalMetadata(ctx context.Context, path string) (api.ExternalMetadata, error) {
	return api.ExternalMetadata{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveExternalMetadata(ctx context.Context, metadata api.ExternalMetadata) error {
	f.meta = metadata
	return nil
}

func (f *fakeRepo) GetDVDMediaInfo(ctx context.Context, path string) (api.DVDMediaInfo, error) {
	return api.DVDMediaInfo{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveDVDMediaInfo(ctx context.Context, info api.DVDMediaInfo) error {
	return nil
}

func (f *fakeRepo) GetReleaseNameOverrides(ctx context.Context, path string) (api.ReleaseNameOverrides, error) {
	return api.ReleaseNameOverrides{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveReleaseNameOverrides(ctx context.Context, path string, overrides api.ReleaseNameOverrides) error {
	return nil
}

func (f *fakeRepo) DeleteReleaseNameOverrides(ctx context.Context, path string) error {
	return nil
}

func (f *fakeRepo) ListHistoryEntries(ctx context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (f *fakeRepo) ListUploadHistoryByPath(ctx context.Context, sourcePath string) ([]api.UploadRecord, error) {
	return nil, nil
}

func (f *fakeRepo) ListPendingUploads(ctx context.Context) ([]api.UploadRecord, error) {
	return nil, nil
}

func (f *fakeRepo) CreateUploadRecord(ctx context.Context, record api.UploadRecord) error {
	return nil
}

func (f *fakeRepo) UpdateLatestUploadRecordStatus(ctx context.Context, sourcePath string, tracker string, status string) error {
	return nil
}

func (f *fakeRepo) SaveTrackerRuleFailures(ctx context.Context, sourcePath string, tracker string, failures []api.TrackerRuleFailure) error {
	return nil
}

func (f *fakeRepo) ListTrackerRuleFailuresByPath(ctx context.Context, path string) ([]api.TrackerRuleFailure, error) {
	return nil, nil
}

func (f *fakeRepo) GetTrackerTimestamp(ctx context.Context, tracker string) (time.Time, error) {
	return time.Time{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveTrackerTimestamp(ctx context.Context, timestamp api.TrackerTimestamp) error {
	f.trackerTimestamps = append(f.trackerTimestamps, timestamp)
	return nil
}

func (f *fakeRepo) SaveTrackerMetadata(ctx context.Context, metadata api.TrackerMetadata) error {
	f.trackerMetadata = append(f.trackerMetadata, metadata)
	return nil
}

func (f *fakeRepo) ListTrackerMetadataByPath(ctx context.Context, path string) ([]api.TrackerMetadata, error) {
	return nil, nil
}

func (f *fakeRepo) SaveScreenshot(ctx context.Context, screenshot api.Screenshot) error {
	return nil
}

func (f *fakeRepo) ListScreenshotsByPath(ctx context.Context, path string) ([]api.Screenshot, error) {
	return nil, nil
}

func (f *fakeRepo) DeleteScreenshot(ctx context.Context, imagePath string) error {
	return nil
}

func (f *fakeRepo) SaveFinalSelections(ctx context.Context, path string, selections []api.ScreenshotFinalSelection) error {
	return nil
}

func (f *fakeRepo) ListFinalSelections(ctx context.Context, path string) ([]api.ScreenshotFinalSelection, error) {
	return nil, nil
}

func (f *fakeRepo) DeleteFinalSelection(ctx context.Context, imagePath string) error {
	return nil
}
func (f *fakeRepo) ReplaceScreenshotSlots(ctx context.Context, path string, slots []api.ScreenshotSlot) error {
	return nil
}
func (f *fakeRepo) ListScreenshotSlotsByPath(ctx context.Context, path string) ([]api.ScreenshotSlot, error) {
	return nil, nil
}
func (f *fakeRepo) UpsertScreenshotSlotVariants(ctx context.Context, path string, variants []api.ScreenshotSlotVariant) error {
	return nil
}

func (f *fakeRepo) SaveUploadedImages(ctx context.Context, path string, host string, images []api.UploadedImageLink) error {
	return nil
}

func (f *fakeRepo) ListUploadedImagesByPath(ctx context.Context, path string) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (f *fakeRepo) DeleteUploadedImage(ctx context.Context, path string, imagePath string, host string) error {
	return nil
}

func (f *fakeRepo) GetDescriptionOverride(ctx context.Context, path string, groupKey string) (api.DescriptionOverride, error) {
	return api.DescriptionOverride{}, internalerrors.ErrNotFound
}
func (f *fakeRepo) ListDescriptionOverridesByPath(ctx context.Context, path string) ([]api.DescriptionOverride, error) {
	return nil, nil
}

func (f *fakeRepo) SaveDescriptionOverride(ctx context.Context, override api.DescriptionOverride) error {
	return nil
}

func (f *fakeRepo) DeleteDescriptionOverride(ctx context.Context, path string, groupKey string) error {
	return nil
}

func (f *fakeRepo) GetPlaylistSelection(ctx context.Context, path string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SavePlaylistSelection(ctx context.Context, path string, playlists []string, useAll bool) error {
	return nil
}

func (f *fakeRepo) DeletePlaylistSelection(ctx context.Context, path string) error {
	return nil
}

func (f *fakeRepo) ListStoredReleasePaths(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (f *fakeRepo) PurgeContentData(ctx context.Context, path string) error {
	return nil
}

type stubTMDB struct {
	searchOutcome tmdb.SearchOutcome
	findResult    tmdb.FindResult
	metadata      tmdb.MetadataResult
	searchFn      func(tmdb.SearchInput) (tmdb.SearchOutcome, error)
	dailySeason   int
	dailyEpisode  int
	dailyErr      error
	searchErr     error
	findErr       error
	searchCalls   int
	findCalls     int
	metaCalls     int
	searchInputs  []tmdb.SearchInput
	findInputs    []tmdb.FindInput
	metaInputs    []tmdb.MetadataInput
}

func (s *stubTMDB) FindByExternalID(ctx context.Context, input tmdb.FindInput) (tmdb.FindResult, error) {
	s.findCalls++
	s.findInputs = append(s.findInputs, input)
	if s.findErr != nil {
		return tmdb.FindResult{}, s.findErr
	}
	return s.findResult, nil
}

func (s *stubTMDB) SearchID(ctx context.Context, input tmdb.SearchInput) (tmdb.SearchOutcome, error) {
	s.searchCalls++
	s.searchInputs = append(s.searchInputs, input)
	if s.searchFn != nil {
		return s.searchFn(input)
	}
	if s.searchErr != nil {
		return tmdb.SearchOutcome{}, s.searchErr
	}
	return s.searchOutcome, nil
}

func (s *stubTMDB) FetchMetadata(ctx context.Context, input tmdb.MetadataInput) (tmdb.MetadataResult, error) {
	s.metaCalls++
	s.metaInputs = append(s.metaInputs, input)
	return s.metadata, nil
}

func (s *stubTMDB) GetEpisodeDetails(ctx context.Context, tmdbID, season, episode int) (tmdb.EpisodeDetails, error) {
	return tmdb.EpisodeDetails{}, nil
}

func (s *stubTMDB) GetSeasonDetails(ctx context.Context, tmdbID, season int) (tmdb.SeasonDetails, error) {
	return tmdb.SeasonDetails{}, nil
}

func (s *stubTMDB) DailyToSeasonEpisode(ctx context.Context, tmdbID int, date time.Time) (int, int, error) {
	return s.dailySeason, s.dailyEpisode, s.dailyErr
}

type stubIMDB struct {
	searchResult       imdb.SearchResult
	searchFn           func(imdb.SearchInput) (imdb.SearchResult, error)
	info               imdb.Info
	searchCalls        int
	infoCalls          int
	searchInputs       []imdb.SearchInput
	lastManualLanguage string
}

func (s *stubIMDB) Search(ctx context.Context, input imdb.SearchInput) (imdb.SearchResult, error) {
	s.searchCalls++
	s.searchInputs = append(s.searchInputs, input)
	if s.searchFn != nil {
		return s.searchFn(input)
	}
	return s.searchResult, nil
}

func (s *stubIMDB) GetInfo(ctx context.Context, imdbID string, manualLanguage string, debug bool) (imdb.Info, error) {
	s.infoCalls++
	s.lastManualLanguage = manualLanguage
	return s.info, nil
}

type stubTVDB struct {
	id                int
	name              string
	calls             int
	episodes          tvdb.EpisodesData
	specificAlias     string
	episodeErr        error
	episodeTranslate  tvdb.EpisodeTranslation
	episodeTransErr   error
	seriesMetadata    tvdb.SeriesMetadata
	episodeCalls      int
	seriesLangCalls   []string
	episodeLangCalls  []string
	episodeTransCalls []int
	lastEpisodeQuery  tvdb.EpisodeQuery
}

func (s *stubTVDB) GetByExternalID(ctx context.Context, imdbID, tmdbID string, tvMovie bool) (int, string, error) {
	s.calls++
	return s.id, s.name, nil
}

func (s *stubTVDB) GetSeriesMetadata(ctx context.Context, seriesID int) (tvdb.SeriesMetadata, error) {
	if s.seriesMetadata.TVDBID != 0 || s.seriesMetadata.Name != "" {
		return s.seriesMetadata, nil
	}
	return tvdb.SeriesMetadata{TVDBID: seriesID, Name: s.name}, nil
}

func (s *stubTVDB) GetSeriesMetadataWithLanguage(ctx context.Context, seriesID int, language string) (tvdb.SeriesMetadata, error) {
	s.seriesLangCalls = append(s.seriesLangCalls, language)
	return s.GetSeriesMetadata(ctx, seriesID)
}

func (s *stubTVDB) GetEpisodes(ctx context.Context, seriesID int, query tvdb.EpisodeQuery) (tvdb.EpisodesData, string, error) {
	s.episodeCalls++
	s.lastEpisodeQuery = query
	if s.episodeErr != nil {
		return tvdb.EpisodesData{}, "", s.episodeErr
	}
	return s.episodes, s.specificAlias, nil
}

func (s *stubTVDB) GetEpisodesWithLanguage(ctx context.Context, seriesID int, query tvdb.EpisodeQuery, language string) (tvdb.EpisodesData, string, error) {
	s.episodeLangCalls = append(s.episodeLangCalls, language)
	return s.GetEpisodes(ctx, seriesID, query)
}

func (s *stubTVDB) GetEpisodeTranslation(ctx context.Context, episodeID int, language string) (tvdb.EpisodeTranslation, error) {
	s.episodeTransCalls = append(s.episodeTransCalls, episodeID)
	if s.episodeTransErr != nil {
		return tvdb.EpisodeTranslation{}, s.episodeTransErr
	}
	return s.episodeTranslate, nil
}

type stubTVmaze struct {
	result tvmaze.SearchResult
	calls  int
	inputs []tvmaze.SearchInput
}

func (s *stubTVmaze) Search(ctx context.Context, input tvmaze.SearchInput) (tvmaze.SearchResult, error) {
	s.calls++
	s.inputs = append(s.inputs, input)
	return s.result, nil
}

func (s *stubTVmaze) GetEpisodeByNumber(ctx context.Context, tvmazeID, season, episode int, lookup tvmaze.EpisodeLookupContext) (*tvmaze.EpisodeData, error) {
	return nil, nil
}

func (s *stubTVmaze) GetEpisodeByDate(ctx context.Context, tvmazeID int, airdate string) (*tvmaze.EpisodeData, error) {
	return nil, nil
}

func TestResolveExternalIDsPrecedence(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2024}}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt0000001", Title: "Example", Year: 2024}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/file.mkv",
		MediaInfoTMDBID:   999,
		MediaInfoIMDBID:   888,
		MediaInfoTVDBID:   777,
		SceneIMDB:         666,
		TrackerData:       []api.TrackerMetadata{{TMDBID: 1, IMDBID: 2, TVDBID: 3}},
		MediaInfoCategory: "movie",
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 1 || result.ExternalIDs.IMDBID != 2 || result.ExternalIDs.TVDBID != 3 {
		t.Fatalf("unexpected resolved ids: %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.SourceTMDB != "tracker" || result.ExternalIDs.SourceIMDB != "tracker" || result.ExternalIDs.SourceTVDB != "tracker" {
		t.Fatalf("unexpected sources: %#v", result.ExternalIDs)
	}
	if repo.ids.TMDBID != 1 || repo.ids.IMDBID != 2 || repo.ids.TVDBID != 3 {
		t.Fatalf("unexpected persisted ids: %#v", repo.ids)
	}
	if result.ExternalMetadata.TMDB == nil || result.ExternalMetadata.IMDB == nil {
		t.Fatalf("expected metadata results")
	}
}

func TestResolveExternalIDsSearchAndMetadata(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "MOVIE"},
		metadata:      tmdb.MetadataResult{Title: "Example", Year: 2024, TMDBType: "Movie"},
	}
	imdbClient := &stubIMDB{searchResult: imdb.SearchResult{IMDbID: 24}, info: imdb.Info{IMDbID: "tt0000024", Title: "Example"}}
	tvdbClient := &stubTVDB{id: 12, name: "Example"}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{SelectedID: 55, IMDBID: 24, TVDBID: 12}}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath: "/media/file.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 42 || result.ExternalIDs.IMDBID != 24 {
		t.Fatalf("unexpected resolved ids: %#v", result.ExternalIDs)
	}
	if result.ExternalMetadata.TMDB == nil || result.ExternalMetadata.IMDB == nil {
		t.Fatalf("expected metadata results")
	}
	if tmdbClient.searchCalls == 0 || imdbClient.searchCalls == 0 {
		t.Fatalf("expected search calls to run")
	}
}

func TestResolveExternalIDsUsesStoredFreshData(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		ExternalIDs: api.ExternalIDs{
			SourcePath:   "/media/file.mkv",
			TMDBID:       42,
			IMDBID:       24,
			TVDBID:       12,
			TVmazeID:     55,
			Category:     "TV",
			SourceTMDB:   "db",
			SourceIMDB:   "db",
			SourceTVDB:   "db",
			SourceTVmaze: "db",
		},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "/media/file.mkv",
			TMDB:       &api.TMDBMetadata{TMDBID: 42, Title: "Example"},
			IMDB:       &api.IMDBMetadata{IMDBID: 24, Title: "Example"},
			TVDB:       &api.TVDBMetadata{TVDBID: 12, Name: "Example"},
			TVmaze:     &api.TVmazeMetadata{TVmazeID: 55, Name: "Example"},
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 42 || result.ExternalIDs.IMDBID != 24 || result.ExternalIDs.TVDBID != 12 || result.ExternalIDs.TVmazeID != 55 {
		t.Fatalf("unexpected resolved ids: %#v", result.ExternalIDs)
	}
	if result.ExternalMetadata.TMDB == nil || result.ExternalMetadata.IMDB == nil || result.ExternalMetadata.TVDB == nil || result.ExternalMetadata.TVmaze == nil {
		t.Fatalf("expected stored metadata to be reused")
	}
	if tmdbClient.findCalls != 0 || tmdbClient.searchCalls != 0 || tmdbClient.metaCalls != 0 {
		t.Fatalf("expected tmdb lookups skipped, got find=%d search=%d metadata=%d", tmdbClient.findCalls, tmdbClient.searchCalls, tmdbClient.metaCalls)
	}
	if imdbClient.searchCalls != 0 || imdbClient.infoCalls != 0 {
		t.Fatalf("expected imdb lookups skipped, got search=%d info=%d", imdbClient.searchCalls, imdbClient.infoCalls)
	}
	if tvdbClient.calls != 0 {
		t.Fatalf("expected tvdb external lookup skipped, got %d", tvdbClient.calls)
	}
	if tvmazeClient.calls != 0 {
		t.Fatalf("expected tvmaze lookup skipped, got %d", tvmazeClient.calls)
	}
}

func TestResolveExternalIDsPrefersTMDBFromIMDbBeforeSearch(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 1620301, Category: "TV"},
		findResult:    tmdb.FindResult{TMDBID: 77075, Category: "TV"},
		metadata:      tmdb.MetadataResult{Title: "Jeopardy!", TMDBType: "Scripted"},
	}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt0159881", Title: "Jeopardy!", Type: "tvSeries", Year: 1984}}
	tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 77075, Name: "Jeopardy!", FirstAired: "1984-09-10"}}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:        `D:\temp\Jeopardy.2025.11.10.1080p.PCOK.WEB-DL.AAC2.0.H.264-ASTRiD.mkv`,
		MediaInfoCategory: "TV",
		Release:           api.ReleaseInfo{Title: "Jeopardy", Year: 2025, Type: "episode"},
		TrackerData:       []api.TrackerMetadata{{IMDBID: 159881, TVDBID: 77075, Category: "TV"}},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 77075 {
		t.Fatalf("expected tmdb id from imdb external lookup, got %d", result.ExternalIDs.TMDBID)
	}
	if result.ExternalIDs.SourceTMDB != "tmdb_external" {
		t.Fatalf("expected tmdb source tmdb_external, got %q", result.ExternalIDs.SourceTMDB)
	}
	if tmdbClient.findCalls != 1 {
		t.Fatalf("expected one tmdb external lookup call, got %d", tmdbClient.findCalls)
	}
	if tmdbClient.searchCalls != 0 {
		t.Fatalf("expected no tmdb filename search when imdb lookup resolves tmdb, got %d", tmdbClient.searchCalls)
	}
	if len(tmdbClient.findInputs) != 1 || tmdbClient.findInputs[0].CategoryPreference != "TV" {
		t.Fatalf("expected tmdb external lookup category preference TV, got %#v", tmdbClient.findInputs)
	}
}

func TestResolveExternalIDsEpisodeTypeForcesTVTMDBSearchCategory(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 224372, Category: "TV"},
		metadata:      tmdb.MetadataResult{Title: "Example Show", TMDBType: "Scripted"},
	}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath: `/media/Example.Show.2025.11.10.1080p.WEB-DL.mkv`,
		Release:    api.ReleaseInfo{Title: "Example Show", Year: 2025, Category: "TV", Type: "WEB-DL"},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.Category != "TV" {
		t.Fatalf("expected category TV from release type episode, got %q", result.ExternalIDs.Category)
	}
	if tmdbClient.searchCalls != 1 {
		t.Fatalf("expected one tmdb search call, got %d", tmdbClient.searchCalls)
	}
	if len(tmdbClient.searchInputs) != 1 {
		t.Fatalf("expected one tmdb search input, got %d", len(tmdbClient.searchInputs))
	}
	if tmdbClient.searchInputs[0].Category != "TV" {
		t.Fatalf("expected tmdb search category TV, got %q", tmdbClient.searchInputs[0].Category)
	}
	if !tmdbClient.searchInputs[0].DontSwitch {
		t.Fatalf("expected tmdb tv search to disable category switching")
	}
}

func TestResolveExternalIDsSearchStagesAndUnattendedInteractionMode(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{searchFn: func(input tmdb.SearchInput) (tmdb.SearchOutcome, error) {
		switch input.SearchYear {
		case 2025, 2026:
			return tmdb.SearchOutcome{}, nil
		case 2024:
			return tmdb.SearchOutcome{TMDBID: 4242, Category: "TV"}, nil
		default:
			return tmdb.SearchOutcome{}, nil
		}
	}}
	imdbClient := &stubIMDB{searchFn: func(input imdb.SearchInput) (imdb.SearchResult, error) {
		if input.SearchYear == 2024 {
			return imdb.SearchResult{IMDbID: 2424}, nil
		}
		return imdb.SearchResult{}, nil
	}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath: "/media/Solo.Camping.for.Two.2025.1080p.WEB-DL.mkv",
		Mode:       api.ModeCLI,
		Options:    api.UploadOptions{InteractionMode: api.InteractionModeUnattended},
		Release:    api.ReleaseInfo{Title: "Solo Camping for Two", Year: 2025, Type: "episode"},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 4242 {
		t.Fatalf("expected staged tmdb id 4242, got %d", result.ExternalIDs.TMDBID)
	}
	if result.ExternalIDs.IMDBID != 2424 {
		t.Fatalf("expected staged imdb id 2424, got %d", result.ExternalIDs.IMDBID)
	}

	if len(tmdbClient.searchInputs) < 3 {
		t.Fatalf("expected staged tmdb searches, got %d", len(tmdbClient.searchInputs))
	}
	if tmdbClient.searchInputs[0].SearchYear != 2025 || tmdbClient.searchInputs[1].SearchYear != 2026 || tmdbClient.searchInputs[2].SearchYear != 2024 {
		t.Fatalf("unexpected tmdb stage years: %#v", tmdbClient.searchInputs)
	}
	for _, input := range tmdbClient.searchInputs {
		if !input.Unattended {
			t.Fatalf("expected tmdb unattended search in CLI mode")
		}
	}

	if len(imdbClient.searchInputs) < 3 {
		t.Fatalf("expected staged imdb searches, got %d", len(imdbClient.searchInputs))
	}
	if imdbClient.searchInputs[0].SearchYear != 2025 || imdbClient.searchInputs[1].SearchYear != 2026 || imdbClient.searchInputs[2].SearchYear != 2024 {
		t.Fatalf("unexpected imdb stage years: %#v", imdbClient.searchInputs)
	}
	for _, input := range imdbClient.searchInputs {
		if !input.Unattended {
			t.Fatalf("expected imdb unattended search in CLI mode")
		}
	}
}

func TestResolveExternalIDsInteractiveCLIDoesNotForceUnattendedSearch(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{searchFn: func(input tmdb.SearchInput) (tmdb.SearchOutcome, error) {
		return tmdb.SearchOutcome{TMDBID: 4242}, nil
	}}
	imdbClient := &stubIMDB{searchFn: func(input imdb.SearchInput) (imdb.SearchResult, error) {
		return imdb.SearchResult{IMDbID: 2424}, nil
	}}
	svc := NewService(repo, WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient), WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))

	_, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/media/Solo.Camping.for.Two.2025.1080p.WEB-DL.mkv",
		Mode:       api.ModeCLI,
		Options:    api.UploadOptions{InteractionMode: api.InteractionModeInteractive},
		Release:    api.ReleaseInfo{Title: "Solo Camping for Two", Year: 2025, Type: "episode"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	for _, input := range tmdbClient.searchInputs {
		if input.Unattended {
			t.Fatalf("expected interactive tmdb search")
		}
	}
	for _, input := range imdbClient.searchInputs {
		if input.Unattended {
			t.Fatalf("expected interactive imdb search")
		}
	}
}

func TestResolveExternalIDsDailyDateSkipsParsedYearInSearch(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{searchFn: func(input tmdb.SearchInput) (tmdb.SearchOutcome, error) {
		if input.SearchYear != 0 {
			t.Fatalf("expected tmdb search year 0 for daily-date lookup, got %d", input.SearchYear)
		}
		return tmdb.SearchOutcome{TMDBID: 4242, Category: "TV"}, nil
	}}
	imdbClient := &stubIMDB{searchFn: func(input imdb.SearchInput) (imdb.SearchResult, error) {
		if input.SearchYear != 0 {
			t.Fatalf("expected imdb search year 0 for daily-date lookup, got %d", input.SearchYear)
		}
		return imdb.SearchResult{IMDbID: 2424}, nil
	}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:       "/media/Jeopardy.2026.01.12.720p.HDTV.x264.mkv",
		DailyEpisodeDate: "2026-01-12",
		Release: api.ReleaseInfo{
			Title: "Jeopardy",
			Year:  2026,
			Type:  "episode",
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 4242 || result.ExternalIDs.IMDBID != 2424 {
		t.Fatalf("unexpected resolved ids: %#v", result.ExternalIDs)
	}
	if len(tmdbClient.searchInputs) != 1 {
		t.Fatalf("expected single tmdb search stage with year 0, got %d", len(tmdbClient.searchInputs))
	}
	if len(imdbClient.searchInputs) != 1 {
		t.Fatalf("expected single imdb search stage with year 0, got %d", len(imdbClient.searchInputs))
	}
}

func TestResolveExternalIDsFetchesIMDBAfterTMDBResolvesIt(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 224372, Category: "TV"},
		metadata:      tmdb.MetadataResult{Title: "Example Show", TMDBType: "Scripted", IMDbID: 27497448},
	}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt27497448", Title: "Example Show"}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Example.Show.S01E01.2160p.WEB-DL.mkv",
		MediaInfoCategory: "TV",
		Release:           api.ReleaseInfo{Title: "Example Show"},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.IMDBID != 27497448 {
		t.Fatalf("expected imdb id resolved from tmdb metadata, got %d", result.ExternalIDs.IMDBID)
	}
	if imdbClient.infoCalls == 0 {
		t.Fatalf("expected imdb metadata fetch after tmdb-imdb enrichment")
	}
	if result.ExternalMetadata.IMDB == nil {
		t.Fatalf("expected imdb metadata after enrichment")
	}
}

func TestResolveExternalIDsTVmazeWaitsForIDThenFallsBack(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 224372, Category: "TV"},
		metadata:      tmdb.MetadataResult{Title: "Example Show", TMDBType: "Scripted", TVDBID: 433631},
	}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{SelectedID: 55, TVDBID: 433631}}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Example.Show.S01E01.2160p.WEB-DL.mkv",
		MediaInfoCategory: "TV",
		Release:           api.ReleaseInfo{Title: "Example Show"},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if tvmazeClient.calls != 1 {
		t.Fatalf("expected one tvmaze lookup after id enrichment, got %d", tvmazeClient.calls)
	}
	if len(tvmazeClient.inputs) != 1 {
		t.Fatalf("expected one tvmaze input")
	}
	if tvmazeClient.inputs[0].TVDBID == "" {
		t.Fatalf("expected tvmaze lookup to include tvdb id after enrichment")
	}
	if tvmazeClient.inputs[0].StrictIDOnly {
		t.Fatalf("expected second-pass tvmaze lookup to allow strict fallback")
	}
	if result.ExternalMetadata.TVmaze == nil || result.ExternalMetadata.TVmaze.TVmazeID != 55 {
		t.Fatalf("expected tvmaze metadata result")
	}
}

func TestResolveExternalIDsOverride(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2024}}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt0000009", Title: "Override"}}
	tvdbClient := &stubTVDB{id: 55, name: "Tracker"}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:  "/media/file.mkv",
		TrackerData: []api.TrackerMetadata{{TMDBID: 1, IMDBID: 2, TVDBID: 3}},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: intPtr(999),
			IMDBID: intPtr(111),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 999 || result.ExternalIDs.IMDBID != 111 {
		t.Fatalf("unexpected override ids: %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.SourceTMDB != "override" || result.ExternalIDs.SourceIMDB != "override" {
		t.Fatalf("unexpected override sources: %#v", result.ExternalIDs)
	}
}

func TestResolveExternalIDsClearIMDBReresolvesFromOverriddenTMDB(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{IMDbID: 777, TMDBType: "Movie", Title: "Example"}}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:  "/media/file.mkv",
		TrackerData: []api.TrackerMetadata{{IMDBID: 2}},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: intPtr(999),
			IMDBID: intPtr(0),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 999 {
		t.Fatalf("expected tmdb override 999, got %d", result.ExternalIDs.TMDBID)
	}
	if result.ExternalIDs.IMDBID != 777 {
		t.Fatalf("expected imdb re-resolved from tmdb metadata, got %d", result.ExternalIDs.IMDBID)
	}
	if result.ExternalIDs.SourceIMDB != "tmdb" {
		t.Fatalf("expected imdb source tmdb after clear, got %q", result.ExternalIDs.SourceIMDB)
	}
}

func TestResolveExternalIDsClearTMDBReresolvesFromRetainedIMDB(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{findResult: tmdb.FindResult{TMDBID: 77075, Category: "TV"}}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:  "/media/file.mkv",
		TrackerData: []api.TrackerMetadata{{IMDBID: 159881, TMDBID: 1}},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: intPtr(0),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if tmdbClient.findCalls != 1 {
		t.Fatalf("expected tmdb external lookup call, got %d", tmdbClient.findCalls)
	}
	if result.ExternalIDs.TMDBID != 77075 {
		t.Fatalf("expected tmdb id re-resolved from imdb, got %d", result.ExternalIDs.TMDBID)
	}
	if result.ExternalIDs.SourceTMDB != "tmdb_external" {
		t.Fatalf("expected tmdb source tmdb_external after clear, got %q", result.ExternalIDs.SourceTMDB)
	}
}

func TestResolveExternalIDsMissingRepo(t *testing.T) {
	svc := NewService(nil)
	_, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{SourcePath: "/media/file.mkv"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSearchTitleYearFromPathFallback(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\1982 - Fitzcarraldo [DVD9.PAL]`,
	}

	title, secondary := resolveSearchTitles(meta)
	year := resolveSearchYear(meta)

	if title != "Fitzcarraldo" {
		t.Fatalf("expected normalized title, got %q", title)
	}
	if secondary != "" {
		t.Fatalf("expected empty secondary title, got %q", secondary)
	}
	if year != 1982 {
		t.Fatalf("expected inferred year 1982, got %d", year)
	}
}

func TestApplyTVEpisodeMetadataDailyMappingMatch(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{dailySeason: 2, dailyEpisode: 7}

	meta := api.PreparedMetadata{
		SourcePath:       "/media/Show.2024-01-15.mkv",
		DailyEpisodeDate: "2024-01-15",
	}
	ids := &api.ExternalIDs{
		TMDBID:   100,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, &stubTVDB{}, &stubTVmaze{})
	if updated.SeasonInt != 2 || updated.EpisodeInt != 7 {
		t.Fatalf("expected mapped season/episode 2/7, got %d/%d", updated.SeasonInt, updated.EpisodeInt)
	}
	if updated.SeasonStr != "S02" || updated.EpisodeStr != "E07" {
		t.Fatalf("expected formatted season/episode S02/E07, got %q/%q", updated.SeasonStr, updated.EpisodeStr)
	}
}

func TestApplyTVEpisodeMetadataDailyMappingNoMatchDoesNotCoerceEpisode(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}

	meta := api.PreparedMetadata{
		SourcePath:       "/media/Show.2024-01-15.mkv",
		DailyEpisodeDate: "2024-01-15",
	}
	ids := &api.ExternalIDs{
		TMDBID:   100,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, &stubTVDB{}, &stubTVmaze{})
	if updated.SeasonInt != 0 || updated.EpisodeInt != 0 {
		t.Fatalf("expected no mapped season/episode on non-match, got %d/%d", updated.SeasonInt, updated.EpisodeInt)
	}
	if updated.SeasonStr != "" || updated.EpisodeStr != "" {
		t.Fatalf("expected empty formatted season/episode, got %q/%q", updated.SeasonStr, updated.EpisodeStr)
	}
}

func TestApplyTVEpisodeMetadataUseSeasonEpisodePrefersTMDBDateMapping(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{dailySeason: 2, dailyEpisode: 7}
	useSeasonEpisode := true

	meta := api.PreparedMetadata{
		SourcePath:       "/media/Show.2024-01-15.mkv",
		SeasonInt:        9,
		EpisodeInt:       99,
		SeasonStr:        "S09",
		EpisodeStr:       "E99",
		DailyEpisodeDate: "2024-01-15",
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			UseSeasonEpisode: &useSeasonEpisode,
		},
	}
	ids := &api.ExternalIDs{
		TMDBID:   100,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, &stubTVDB{}, &stubTVmaze{})
	if updated.SeasonInt != 2 || updated.EpisodeInt != 7 {
		t.Fatalf("expected tmdb mapped season/episode 2/7, got %d/%d", updated.SeasonInt, updated.EpisodeInt)
	}
	if !updated.TMDBDateMatch {
		t.Fatalf("expected tmdb date match to be recorded")
	}
}

func TestMapTVmazeMetadataIncludesRichFields(t *testing.T) {
	result := tvmaze.SearchResult{
		SelectedID: 88,
		Candidates: []tvmaze.Candidate{{
			ID:             88,
			Name:           "Example Show",
			Premiered:      "2019-01-01",
			Ended:          "2021-02-02",
			Summary:        "Example summary",
			Status:         "Ended",
			Type:           "Scripted",
			Language:       "English",
			Genres:         []string{"Drama", "Mystery"},
			Runtime:        60,
			AverageRuntime: 58,
			Rating:         8.3,
			Weight:         92,
			OfficialSite:   "https://example.test",
			Country:        "United States",
			Network: tvmaze.TVNetwork{
				Name:      "HBO",
				Country:   "United States",
				Logo:      "https://img.example/network.png",
				LogoSmall: "https://img.example/network-small.png",
			},
			WebChannel: tvmaze.TVNetwork{
				Name:      "Max",
				Country:   "United States",
				Logo:      "https://img.example/web.png",
				LogoSmall: "https://img.example/web-small.png",
			},
			Image: tvmaze.Image{
				Original: "https://img.example/poster.jpg",
				Medium:   "https://img.example/poster-medium.jpg",
			},
			Externals: tvmaze.Externals{IMDB: "tt1234567", TVDB: 9988},
		}},
	}

	mapped := mapTVmazeMetadata(result)
	if mapped == nil {
		t.Fatalf("expected mapped metadata")
	}
	if mapped.Name != "Example Show" || mapped.Type != "Scripted" || mapped.Status != "Ended" {
		t.Fatalf("unexpected identity fields: %#v", mapped)
	}
	if mapped.IMDBID != 1234567 || mapped.TVDBID != 9988 {
		t.Fatalf("expected imdb/tvdb fallback mapping, got imdb=%d tvdb=%d", mapped.IMDBID, mapped.TVDBID)
	}
	if mapped.Genres != "Drama, Mystery" {
		t.Fatalf("expected joined genres, got %q", mapped.Genres)
	}
	if mapped.Poster == "" || mapped.Backdrop == "" || mapped.NetworkLogo == "" || mapped.WebLogo == "" {
		t.Fatalf("expected media/logo fields populated: %#v", mapped)
	}
	if mapped.Runtime != 60 || mapped.AverageRuntime != 58 || mapped.Rating != 8.3 || mapped.Weight != 92 {
		t.Fatalf("unexpected runtime/rating fields: %#v", mapped)
	}
}

func TestApplyTVEpisodeMetadataTVDBAliasYearApplied(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{specificAlias: "Le Bureau (2018)"}

	meta := api.PreparedMetadata{
		SourcePath: "/media/Le.Bureau.S01E01.mkv",
	}
	ids := &api.ExternalIDs{
		TMDBID:   100,
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.ExternalMetadata{
		TMDB: &api.TMDBMetadata{OriginalLanguage: "fr"},
		TVDB: &api.TVDBMetadata{TVDBID: 200},
	}

	_ = svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if external.TVDB.Name != "Le Bureau" {
		t.Fatalf("expected cleaned alias title, got %q", external.TVDB.Name)
	}
	if external.TVDB.Year != 2018 {
		t.Fatalf("expected alias year 2018, got %d", external.TVDB.Year)
	}
}

func TestApplyTVEpisodeMetadataTVDBSlugFallbackAliasYearApplied(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{specificAlias: "Cats Eye (2025)"}

	meta := api.PreparedMetadata{
		SourcePath: "/media/Cats.Eye.S01E01.mkv",
	}
	ids := &api.ExternalIDs{
		TMDBID:   100,
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.ExternalMetadata{
		TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		TVDB: &api.TVDBMetadata{TVDBID: 200},
	}

	_ = svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if external.TVDB.Name != "Cats Eye" {
		t.Fatalf("expected cleaned slug-fallback alias title, got %q", external.TVDB.Name)
	}
	if external.TVDB.Year != 2025 {
		t.Fatalf("expected alias year 2025, got %d", external.TVDB.Year)
	}
}

func TestApplyTVEpisodeMetadataTVDBAliasAppliedForEnglish(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{specificAlias: "English Show (2021)"}

	meta := api.PreparedMetadata{
		SourcePath: "/media/English.Show.S01E01.mkv",
	}
	ids := &api.ExternalIDs{
		TMDBID:   100,
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.ExternalMetadata{
		TMDB: &api.TMDBMetadata{OriginalLanguage: "en"},
		TVDB: &api.TVDBMetadata{TVDBID: 200, Name: "Native Name", Year: 2015},
	}

	_ = svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if external.TVDB.Name != "English Show" {
		t.Fatalf("expected alias-based title override, got %q", external.TVDB.Name)
	}
	if external.TVDB.Year != 2021 {
		t.Fatalf("expected alias-based year override, got %d", external.TVDB.Year)
	}
}

func TestApplyTVEpisodeMetadataTVDBEpisodeTranslationApplied(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{
		episodes: tvdb.EpisodesData{Episodes: []tvdb.Episode{{
			ID:           101,
			SeasonNumber: 1,
			Number:       2,
			Name:         "第2話 / Episode 2",
			Overview:     "日本語概要",
		}}},
		episodeTranslate: tvdb.EpisodeTranslation{
			Name:     "Episode 2",
			Overview: "English episode overview",
		},
	}

	meta := api.PreparedMetadata{
		SourcePath: "/media/Example.Show.S01E02.mkv",
		SeasonInt:  1,
		EpisodeInt: 2,
	}
	ids := &api.ExternalIDs{
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.ExternalMetadata{
		TVDB: &api.TVDBMetadata{TVDBID: 200, OriginalLanguage: "jpn"},
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if len(tvdbClient.episodeTransCalls) != 1 || tvdbClient.episodeTransCalls[0] != 101 {
		t.Fatalf("expected one episode translation call for episode id 101, got %v", tvdbClient.episodeTransCalls)
	}
	if external.TVDB.EpisodeNameEnglish != "Episode 2" {
		t.Fatalf("expected english episode name from translation, got %q", external.TVDB.EpisodeNameEnglish)
	}
	if external.TVDB.EpisodeOverviewEnglish != "English episode overview" {
		t.Fatalf("expected english episode overview from translation, got %q", external.TVDB.EpisodeOverviewEnglish)
	}
	if !external.TVDB.HasEnglish {
		t.Fatalf("expected HasEnglish true when english episode fields are populated")
	}
	if updated.EpisodeTitle != "" {
		t.Fatalf("expected episode title blanked when title contains episode/tba, got %q", updated.EpisodeTitle)
	}
}

func TestApplyTVEpisodeMetadataTVDBEpisodeTitleFallsBackToOriginalWhenEnglishMissing(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{
		episodes: tvdb.EpisodesData{Episodes: []tvdb.Episode{{
			ID:           102,
			SeasonNumber: 1,
			Number:       3,
			Name:         "Native Title 3",
			Overview:     "Native overview",
		}}},
		episodeTransErr: errors.New("no english translation"),
	}

	meta := api.PreparedMetadata{
		SourcePath: "/media/Example.Show.S01E03.mkv",
		SeasonInt:  1,
		EpisodeInt: 3,
	}
	ids := &api.ExternalIDs{
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.ExternalMetadata{
		TVDB: &api.TVDBMetadata{TVDBID: 200, OriginalLanguage: "jpn"},
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if updated.EpisodeTitle != "Native Title 3" {
		t.Fatalf("expected original episode title fallback, got %q", updated.EpisodeTitle)
	}
}

func TestApplyTVEpisodeMetadataEpisodeTitleBlankedWhenTBA(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{
		episodes: tvdb.EpisodesData{Episodes: []tvdb.Episode{{
			ID:           103,
			SeasonNumber: 1,
			Number:       4,
			Name:         "TBA",
			Overview:     "Overview",
		}}},
	}

	meta := api.PreparedMetadata{
		SourcePath: "/media/Example.Show.S01E04.mkv",
		SeasonInt:  1,
		EpisodeInt: 4,
	}
	ids := &api.ExternalIDs{
		TVDBID:   200,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, tvdbClient, &stubTVmaze{})

	if updated.EpisodeTitle != "" {
		t.Fatalf("expected episode title blank when tba, got %q", updated.EpisodeTitle)
	}
}

func TestResolveExternalIDsTVDBNoEnglishRefetch(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{
		seriesMetadata: tvdb.SeriesMetadata{
			TVDBID:           200,
			Name:             "アークナイツ",
			NameEnglish:      "Arknights: Prelude to Dawn",
			OriginalLanguage: "jpn",
			HasEnglish:       true,
		},
		episodes: tvdb.EpisodesData{
			Episodes: []tvdb.Episode{{
				ID:           1,
				SeasonNumber: 1,
				Number:       1,
				Name:         "黎明",
				Overview:     "日本語概要",
			}},
		},
	}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(&stubTVmaze{}),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Arknights.S01E01.mkv",
		MediaInfoCategory: "TV",
		SeasonInt:         1,
		EpisodeInt:        1,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID: intPtr(200),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if containsString(tvdbClient.seriesLangCalls, "eng") {
		t.Fatalf("expected no eng series refetch, calls=%v", tvdbClient.seriesLangCalls)
	}
	if containsString(tvdbClient.episodeLangCalls, "eng") {
		t.Fatalf("expected no eng episode refetch, calls=%v", tvdbClient.episodeLangCalls)
	}
	if result.ExternalMetadata.TVDB == nil {
		t.Fatalf("expected tvdb metadata")
	}
	if !result.ExternalMetadata.TVDB.HasEnglish {
		t.Fatalf("expected HasEnglish true when english fields are populated")
	}
}

func TestResolveExternalIDsTVDBSlugYearUsedForNamingYear(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{
		seriesMetadata: tvdb.SeriesMetadata{
			TVDBID:           200,
			Name:             "Cats Eye",
			NameEnglish:      "Cats Eye",
			Slug:             "cats-eye-2025",
			FirstAired:       "2010-10-01",
			OriginalLanguage: "jpn",
			HasEnglish:       true,
		},
	}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(&stubTVmaze{}),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Cats.Eye.S01E01.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID: intPtr(200),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalMetadata.TVDB == nil {
		t.Fatalf("expected tvdb metadata")
	}
	if result.ExternalMetadata.TVDB.Year != 2025 {
		t.Fatalf("expected slug-derived tvdb year 2025, got %d", result.ExternalMetadata.TVDB.Year)
	}
	if !result.ExternalMetadata.TVDB.YearFromAlias {
		t.Fatalf("expected YearFromAlias true for slug-derived year")
	}
}

func TestResolveExternalIDsAppliesMALOverride(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		metadata: tmdb.MetadataResult{
			TMDBType: "tv",
			MALID:    111,
			Anime:    true,
		},
	}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(&stubTVmaze{}),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Example.Anime.S01E01.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: intPtr(101),
			MALID:  intPtr(999),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.MALID != 999 {
		t.Fatalf("expected mal override 999, got %d", result.MALID)
	}
}

func TestResolveExternalIDsAppliesOriginalLanguageOverride(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		metadata: tmdb.MetadataResult{
			TMDBType:         "tv",
			OriginalLanguage: "en",
		},
	}
	imdbClient := &stubIMDB{
		info: imdb.Info{IMDbID: "tt0000202", OriginalLanguage: "en"},
	}
	tvdbClient := &stubTVDB{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(&stubTVmaze{}),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Example.Show.S01E01.mkv",
		MediaInfoCategory: "TV",
		MetadataOverrides: api.MetadataOverrides{
			OriginalLanguage: stringPtr("ja"),
		},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: intPtr(101),
			IMDBID: intPtr(202),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(tmdbClient.metaInputs) == 0 || tmdbClient.metaInputs[0].ManualLanguage != "ja" {
		t.Fatalf("expected tmdb fetch manual language ja, got %#v", tmdbClient.metaInputs)
	}
	if imdbClient.lastManualLanguage != "ja" {
		t.Fatalf("expected imdb manual language ja, got %q", imdbClient.lastManualLanguage)
	}
	if result.ExternalMetadata.TMDB == nil || result.ExternalMetadata.TMDB.OriginalLanguage != "ja" {
		t.Fatalf("expected tmdb original language override, got %#v", result.ExternalMetadata.TMDB)
	}
	if result.ExternalMetadata.IMDB == nil || result.ExternalMetadata.IMDB.OriginalLanguage != "ja" {
		t.Fatalf("expected imdb original language override, got %#v", result.ExternalMetadata.IMDB)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

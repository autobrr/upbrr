// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	"github.com/autobrr/upbrr/pkg/api"
)

type fakeRepo struct {
	ids                 api.ExternalIDs
	meta                api.ExternalMetadata
	fileMetadata        api.FileMetadata
	trackerMetadata     []api.TrackerMetadata
	trackerTimestamps   []api.TrackerTimestamp
	trackerRuleFailures []api.TrackerRuleFailure
}

func (f *fakeRepo) GetByPath(_ context.Context, path string) (api.FileMetadata, error) {
	if strings.EqualFold(strings.TrimSpace(f.fileMetadata.Path), strings.TrimSpace(path)) {
		return f.fileMetadata, nil
	}
	return api.FileMetadata{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) Save(_ context.Context, metadata api.FileMetadata) error {
	f.fileMetadata = metadata
	return nil
}

func (f *fakeRepo) GetExternalIDs(_ context.Context, _ string) (api.ExternalIDs, error) {
	return api.ExternalIDs{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveExternalIDs(_ context.Context, ids api.ExternalIDs) error {
	f.ids = ids
	return nil
}

func (f *fakeRepo) GetExternalMetadata(_ context.Context, _ string) (api.ExternalMetadata, error) {
	return api.ExternalMetadata{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveExternalMetadata(_ context.Context, metadata api.ExternalMetadata) error {
	f.meta = metadata
	return nil
}

func (f *fakeRepo) GetDVDMediaInfo(_ context.Context, _ string) (api.DVDMediaInfo, error) {
	return api.DVDMediaInfo{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveDVDMediaInfo(_ context.Context, _ api.DVDMediaInfo) error {
	return nil
}

func (f *fakeRepo) GetReleaseNameOverrides(_ context.Context, _ string) (api.ReleaseNameOverrides, error) {
	return api.ReleaseNameOverrides{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveReleaseNameOverrides(_ context.Context, _ string, _ api.ReleaseNameOverrides) error {
	return nil
}

func (f *fakeRepo) DeleteReleaseNameOverrides(_ context.Context, _ string) error {
	return nil
}

func (f *fakeRepo) ListHistoryEntries(_ context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (f *fakeRepo) ListUploadHistoryByPath(_ context.Context, _ string) ([]api.UploadRecord, error) {
	return nil, nil
}

func (f *fakeRepo) ListPendingUploads(_ context.Context) ([]api.UploadRecord, error) {
	return nil, nil
}

func (f *fakeRepo) CreateUploadRecord(_ context.Context, _ api.UploadRecord) error {
	return nil
}

func (f *fakeRepo) UpdateLatestUploadRecordStatus(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (f *fakeRepo) SaveTrackerRuleFailures(_ context.Context, _ string, _ string, failures []api.TrackerRuleFailure) error {
	f.trackerRuleFailures = append([]api.TrackerRuleFailure{}, failures...)
	return nil
}

func (f *fakeRepo) ListTrackerRuleFailuresByPath(_ context.Context, _ string) ([]api.TrackerRuleFailure, error) {
	return nil, nil
}

func (f *fakeRepo) GetTrackerTimestamp(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveTrackerTimestamp(_ context.Context, timestamp api.TrackerTimestamp) error {
	f.trackerTimestamps = append(f.trackerTimestamps, timestamp)
	return nil
}

func (f *fakeRepo) SaveTrackerMetadata(_ context.Context, metadata api.TrackerMetadata) error {
	f.trackerMetadata = append(f.trackerMetadata, metadata)
	return nil
}

func (f *fakeRepo) ListTrackerMetadataByPath(_ context.Context, _ string) ([]api.TrackerMetadata, error) {
	return nil, nil
}

func (f *fakeRepo) SaveScreenshot(_ context.Context, _ api.Screenshot) error {
	return nil
}

func (f *fakeRepo) ListScreenshotsByPath(_ context.Context, _ string) ([]api.Screenshot, error) {
	return nil, nil
}

func (f *fakeRepo) DeleteScreenshot(_ context.Context, _ string) error {
	return nil
}

func (f *fakeRepo) SaveFinalSelections(_ context.Context, _ string, _ []api.ScreenshotFinalSelection) error {
	return nil
}

func (f *fakeRepo) ListFinalSelections(_ context.Context, _ string) ([]api.ScreenshotFinalSelection, error) {
	return nil, nil
}

func (f *fakeRepo) DeleteFinalSelection(_ context.Context, _ string) error {
	return nil
}
func (f *fakeRepo) ReplaceScreenshotSlots(_ context.Context, _ string, _ []api.ScreenshotSlot) error {
	return nil
}
func (f *fakeRepo) ListScreenshotSlotsByPath(_ context.Context, _ string) ([]api.ScreenshotSlot, error) {
	return nil, nil
}
func (f *fakeRepo) UpsertScreenshotSlotVariants(_ context.Context, _ string, _ []api.ScreenshotSlotVariant) error {
	return nil
}

func (f *fakeRepo) SaveUploadedImages(_ context.Context, _ string, _ string, _ []api.UploadedImageLink) error {
	return nil
}

func (f *fakeRepo) ListUploadedImagesByPath(_ context.Context, _ string) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (f *fakeRepo) DeleteUploadedImage(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (f *fakeRepo) GetDescriptionOverride(_ context.Context, _ string, _ string) (api.DescriptionOverride, error) {
	return api.DescriptionOverride{}, internalerrors.ErrNotFound
}
func (f *fakeRepo) ListDescriptionOverridesByPath(_ context.Context, _ string) ([]api.DescriptionOverride, error) {
	return nil, nil
}

func (f *fakeRepo) SaveDescriptionOverride(_ context.Context, _ api.DescriptionOverride) error {
	return nil
}

func (f *fakeRepo) DeleteDescriptionOverride(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *fakeRepo) GetPlaylistSelection(_ context.Context, _ string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SavePlaylistSelection(_ context.Context, _ string, _ []string, _ bool) error {
	return nil
}

func (f *fakeRepo) DeletePlaylistSelection(_ context.Context, _ string) error {
	return nil
}

func (f *fakeRepo) ListStoredReleasePaths(_ context.Context) ([]string, error) {
	return nil, nil
}

func (f *fakeRepo) PurgeContentData(_ context.Context, _ string) error {
	return nil
}

type stubTMDB struct {
	searchOutcome   tmdb.SearchOutcome
	findResult      tmdb.FindResult
	metadata        tmdb.MetadataResult
	metadataErr     error
	searchFn        func(tmdb.SearchInput) (tmdb.SearchOutcome, error)
	dailySeason     int
	dailyEpisode    int
	dailyErr        error
	localizedData   map[string]any
	localizedByType map[string]map[string]any
	localizedErr    error
	searchErr       error
	findErr         error
	searchCalls     int
	findCalls       int
	metaCalls       int
	localizedInputs []tmdb.LocalizedDataInput
	searchInputs    []tmdb.SearchInput
	findInputs      []tmdb.FindInput
	metaInputs      []tmdb.MetadataInput
}

func (s *stubTMDB) FindByExternalID(_ context.Context, input tmdb.FindInput) (tmdb.FindResult, error) {
	s.findCalls++
	s.findInputs = append(s.findInputs, input)
	if s.findErr != nil {
		return tmdb.FindResult{}, s.findErr
	}
	return s.findResult, nil
}

func (s *stubTMDB) SearchID(_ context.Context, input tmdb.SearchInput) (tmdb.SearchOutcome, error) {
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

func (s *stubTMDB) FetchMetadata(_ context.Context, input tmdb.MetadataInput) (tmdb.MetadataResult, error) {
	s.metaCalls++
	s.metaInputs = append(s.metaInputs, input)
	if s.metadataErr != nil {
		return tmdb.MetadataResult{}, s.metadataErr
	}
	return s.metadata, nil
}

func (s *stubTMDB) GetEpisodeDetails(_ context.Context, _, _, _ int) (tmdb.EpisodeDetails, error) {
	return tmdb.EpisodeDetails{}, nil
}

func (s *stubTMDB) GetSeasonDetails(_ context.Context, _, _ int) (tmdb.SeasonDetails, error) {
	return tmdb.SeasonDetails{}, nil
}

func (s *stubTMDB) DailyToSeasonEpisode(_ context.Context, _ int, _ time.Time) (int, int, error) {
	return s.dailySeason, s.dailyEpisode, s.dailyErr
}

func (s *stubTMDB) GetLocalizedData(_ context.Context, input tmdb.LocalizedDataInput) (map[string]any, error) {
	s.localizedInputs = append(s.localizedInputs, input)
	if s.localizedByType != nil {
		return s.localizedByType[input.DataType], s.localizedErr
	}
	return s.localizedData, s.localizedErr
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

func (s *stubIMDB) Search(_ context.Context, input imdb.SearchInput) (imdb.SearchResult, error) {
	s.searchCalls++
	s.searchInputs = append(s.searchInputs, input)
	if s.searchFn != nil {
		return s.searchFn(input)
	}
	return s.searchResult, nil
}

func (s *stubIMDB) GetInfo(_ context.Context, _ string, manualLanguage string, _ bool) (imdb.Info, error) {
	s.infoCalls++
	s.lastManualLanguage = manualLanguage
	return s.info, nil
}

type stubTVDB struct {
	id                int
	name              string
	calls             int
	tvMovieCalls      []bool
	idWhenTVMovie     int
	nameWhenTVMovie   string
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

func (s *stubTVDB) GetByExternalID(_ context.Context, _, _ string, tvMovie bool) (int, string, error) {
	s.calls++
	s.tvMovieCalls = append(s.tvMovieCalls, tvMovie)
	if tvMovie && s.idWhenTVMovie != 0 {
		return s.idWhenTVMovie, s.nameWhenTVMovie, nil
	}
	return s.id, s.name, nil
}

func (s *stubTVDB) GetSeriesMetadata(_ context.Context, seriesID int) (tvdb.SeriesMetadata, error) {
	if s.seriesMetadata.TVDBID != 0 || s.seriesMetadata.Name != "" {
		return s.seriesMetadata, nil
	}
	return tvdb.SeriesMetadata{TVDBID: seriesID, Name: s.name}, nil
}

func (s *stubTVDB) GetSeriesMetadataWithLanguage(ctx context.Context, seriesID int, language string) (tvdb.SeriesMetadata, error) {
	s.seriesLangCalls = append(s.seriesLangCalls, language)
	return s.GetSeriesMetadata(ctx, seriesID)
}

func (s *stubTVDB) GetEpisodes(_ context.Context, _ int, query tvdb.EpisodeQuery) (tvdb.EpisodesData, string, error) {
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

func (s *stubTVDB) GetEpisodeTranslation(_ context.Context, episodeID int, _ string) (tvdb.EpisodeTranslation, error) {
	s.episodeTransCalls = append(s.episodeTransCalls, episodeID)
	if s.episodeTransErr != nil {
		return tvdb.EpisodeTranslation{}, s.episodeTransErr
	}
	return s.episodeTranslate, nil
}

type stubTVmaze struct {
	result      tvmaze.SearchResult
	episodeData *tvmaze.EpisodeData
	calls       int
	inputs      []tvmaze.SearchInput
}

func (s *stubTVmaze) Search(_ context.Context, input tvmaze.SearchInput) (tvmaze.SearchResult, error) {
	s.calls++
	s.inputs = append(s.inputs, input)
	return s.result, nil
}

func (s *stubTVmaze) GetEpisodeByNumber(_ context.Context, _, _, _ int, _ tvmaze.EpisodeLookupContext) (*tvmaze.EpisodeData, error) {
	return s.episodeData, nil
}

func (s *stubTVmaze) GetEpisodeByDate(_ context.Context, _ int, _ string) (*tvmaze.EpisodeData, error) {
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
		MediaInfoCategory: "TV",
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

func TestMapTMDBMetadataClonesLocalizedTitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		localizedTitles map[string]string
		want            map[string]string
	}{
		{
			name: "nil",
			want: map[string]string{},
		},
		{
			name:            "empty",
			localizedTitles: map[string]string{},
			want:            map[string]string{},
		},
		{
			name:            "preserves keys",
			localizedTitles: map[string]string{"de": "Titel"},
			want:            map[string]string{"de": "Titel"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tmdb.MetadataResult{LocalizedTitles: tt.localizedTitles}

			mapped := mapTMDBMetadata(api.ExternalIDs{TMDBID: 123}, result)
			if mapped == nil {
				t.Fatal("expected mapped metadata")
			}
			if mapped.LocalizedTitles == nil {
				t.Fatal("expected nonnil localized titles")
			}
			if len(mapped.LocalizedTitles) != len(tt.want) {
				t.Fatalf("localized titles len = %d, want %d", len(mapped.LocalizedTitles), len(tt.want))
			}
			for key, want := range tt.want {
				if got := mapped.LocalizedTitles[key]; got != want {
					t.Fatalf("localized title %q = %q, want %q", key, got, want)
				}
			}

			mapped.LocalizedTitles["fr"] = "Titre"
			if tt.localizedTitles != nil {
				if _, ok := tt.localizedTitles["fr"]; ok {
					t.Fatalf("expected cloned localized titles to ignore mapped mutation, got %#v", tt.localizedTitles)
				}
				tt.localizedTitles["de"] = "Changed"
				if got, ok := mapped.LocalizedTitles["de"]; ok && got == "Changed" {
					t.Fatalf("expected cloned localized title to ignore source mutation, got %#v", mapped.LocalizedTitles)
				}
			}
		})
	}
}

func TestCloneStringMapReturnsDetachedEmptyMapForNil(t *testing.T) {
	t.Parallel()

	cloned := cloneStringMap(nil)
	if cloned == nil {
		t.Fatal("expected nonnil empty map")
	}
	cloned["de"] = "Titel"
	if got := cloned["de"]; got != "Titel" {
		t.Fatalf("localized title = %q, want Titel", got)
	}
}

func TestResolveExternalIDsSkipAutoTorrentIgnoresTrackerSourcedIDs(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2024}}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt0000888", Title: "Example", Year: 2024}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		Options:         api.UploadOptions{SkipAutoTorrent: true},
		ExternalIDs: api.ExternalIDs{
			SourcePath: "/media/file.mkv",
			TMDBID:     1,
			SourceTMDB: "tracker",
			IMDBID:     2,
			SourceIMDB: "tracker",
			TVDBID:     3,
			SourceTVDB: "tracker",
			Category:   "MOVIE",
		},
		MediaInfoTMDBID:   999,
		MediaInfoIMDBID:   888,
		MediaInfoTVDBID:   777,
		MediaInfoCategory: "movie",
		TrackerData: []api.TrackerMetadata{{
			TMDBID: 4,
			IMDBID: 5,
			TVDBID: 6,
		}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 999 || result.ExternalIDs.SourceTMDB != "mediainfo" {
		t.Fatalf("expected tmdb from mediainfo, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.IMDBID != 888 || result.ExternalIDs.SourceIMDB != "mediainfo" {
		t.Fatalf("expected imdb from mediainfo, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.TVDBID != 0 || result.ExternalIDs.SourceTVDB != "" {
		t.Fatalf("expected tvdb cleared for movie category, got %#v", result.ExternalIDs)
	}
	if tmdbClient.findCalls != 0 || tmdbClient.searchCalls != 0 || tmdbClient.metaCalls != 1 {
		t.Fatalf("expected one tmdb metadata fetch only, got find=%d search=%d metadata=%d", tmdbClient.findCalls, tmdbClient.searchCalls, tmdbClient.metaCalls)
	}
	if imdbClient.searchCalls != 0 || imdbClient.infoCalls != 1 {
		t.Fatalf("expected one imdb info fetch only, got search=%d info=%d", imdbClient.searchCalls, imdbClient.infoCalls)
	}
	if tvdbClient.calls != 0 {
		t.Fatalf("expected tvdb external lookup skipped, got %d", tvdbClient.calls)
	}
	if len(tvdbClient.seriesLangCalls) != 0 {
		t.Fatalf("expected tvdb metadata fetch skipped for movie category, got %d", len(tvdbClient.seriesLangCalls))
	}
	if tvmazeClient.calls != 0 {
		t.Fatalf("expected tvmaze lookup skipped, got %d", tvmazeClient.calls)
	}

	reuseTMDBClient := &stubTMDB{}
	reuseIMDBClient := &stubIMDB{}
	reuseTVDBClient := &stubTVDB{}
	reuseTVmazeClient := &stubTVmaze{}
	reuseSvc := NewService(repo,
		WithTMDBClient(reuseTMDBClient),
		WithIMDBClient(reuseIMDBClient),
		WithTVDBClient(reuseTVDBClient),
		WithTVmazeClient(reuseTVmazeClient),
	)

	reused, err := reuseSvc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		Options:         api.UploadOptions{SkipAutoTorrent: true},
		ExternalIDs: api.ExternalIDs{
			SourcePath: "/media/file.mkv",
			TMDBID:     1,
			SourceTMDB: "tracker",
			IMDBID:     2,
			SourceIMDB: "tracker",
			TVDBID:     3,
			SourceTVDB: "tracker",
			Category:   "MOVIE",
		},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "/media/file.mkv",
			TMDB:       &api.TMDBMetadata{TMDBID: 999, Title: "Stored TMDB"},
			IMDB:       &api.IMDBMetadata{IMDBID: 888, Title: "Stored IMDb"},
			TVDB:       &api.TVDBMetadata{TVDBID: 777, Name: "Stored TVDB"},
		},
		MediaInfoTMDBID:   999,
		MediaInfoIMDBID:   888,
		MediaInfoTVDBID:   777,
		MediaInfoCategory: "movie",
		TrackerData: []api.TrackerMetadata{{
			TMDBID: 4,
			IMDBID: 5,
			TVDBID: 6,
		}},
	})
	if err != nil {
		t.Fatalf("resolve reused metadata: %v", err)
	}
	if reused.ExternalMetadata.TMDB == nil || reused.ExternalMetadata.TMDB.Title != "Stored TMDB" {
		t.Fatalf("expected stored tmdb metadata reused, got %#v", reused.ExternalMetadata.TMDB)
	}
	if reused.ExternalMetadata.IMDB == nil || reused.ExternalMetadata.IMDB.Title != "Stored IMDb" {
		t.Fatalf("expected stored imdb metadata reused, got %#v", reused.ExternalMetadata.IMDB)
	}
	if reused.ExternalMetadata.TVDB != nil {
		t.Fatalf("expected stored tvdb metadata cleared for movie category, got %#v", reused.ExternalMetadata.TVDB)
	}
	if reuseTMDBClient.findCalls != 0 || reuseTMDBClient.searchCalls != 0 || reuseTMDBClient.metaCalls != 0 {
		t.Fatalf("expected tmdb calls skipped for stored metadata, got find=%d search=%d metadata=%d", reuseTMDBClient.findCalls, reuseTMDBClient.searchCalls, reuseTMDBClient.metaCalls)
	}
	if reuseIMDBClient.searchCalls != 0 || reuseIMDBClient.infoCalls != 0 {
		t.Fatalf("expected imdb calls skipped for stored metadata, got search=%d info=%d", reuseIMDBClient.searchCalls, reuseIMDBClient.infoCalls)
	}
	if reuseTVDBClient.calls != 0 || len(reuseTVDBClient.seriesLangCalls) != 0 {
		t.Fatalf("expected tvdb calls skipped for stored metadata, got external=%d metadata=%d", reuseTVDBClient.calls, len(reuseTVDBClient.seriesLangCalls))
	}
	if reuseTVmazeClient.calls != 0 {
		t.Fatalf("expected tvmaze calls skipped for stored metadata, got %d", reuseTVmazeClient.calls)
	}
}

func TestResolveExternalIDsMovieCategoryVetoesEarlierTVCandidate(t *testing.T) {
	repo := &fakeRepo{
		fileMetadata: api.FileMetadata{
			Path:     "/media/file.mkv",
			Category: "MOVIE",
			Title:    "Example",
			Year:     2024,
		},
	}
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

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:  "/media/file.mkv",
		Release:     api.ReleaseInfo{Category: "MOVIE", Title: "Example", Year: 2024},
		TrackerData: []api.TrackerMetadata{{Category: "TV", TVDBID: 12345}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.Category != "MOVIE" {
		t.Fatalf("expected external IDs category MOVIE, got %q", result.ExternalIDs.Category)
	}
	if result.ExternalIDs.TVDBID != 0 {
		t.Fatalf("expected tvdb cleared for movie category, got %d", result.ExternalIDs.TVDBID)
	}
	if result.Release.Category != "MOVIE" {
		t.Fatalf("expected release category MOVIE, got %q", result.Release.Category)
	}
	if repo.fileMetadata.Category != "MOVIE" {
		t.Fatalf("expected persisted release category MOVIE, got %q", repo.fileMetadata.Category)
	}
	if repo.ids.Category != "MOVIE" {
		t.Fatalf("expected persisted external IDs category MOVIE, got %q", repo.ids.Category)
	}
	if len(tmdbClient.searchInputs) == 0 || tmdbClient.searchInputs[0].Category != "MOVIE" {
		t.Fatalf("expected MOVIE to be passed as TMDB preference, got %#v", tmdbClient.searchInputs)
	}
}

func TestResolveExternalIDsIgnoresUnsupportedTrackerCategory(t *testing.T) {
	repo := &fakeRepo{
		fileMetadata: api.FileMetadata{
			Path:     "/media/file.mkv",
			Category: "MOVIE",
			Title:    "Example",
			Year:     2024,
		},
	}
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

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:  "/media/file.mkv",
		Release:     api.ReleaseInfo{Category: "MOVIE", Title: "Example", Year: 2024},
		TrackerData: []api.TrackerMetadata{{Category: "Music"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.Category != "MOVIE" {
		t.Fatalf("expected external IDs category MOVIE, got %q", result.ExternalIDs.Category)
	}
	if result.Release.Category != "MOVIE" {
		t.Fatalf("expected release category MOVIE, got %q", result.Release.Category)
	}
	if repo.fileMetadata.Category != "MOVIE" {
		t.Fatalf("expected persisted release category MOVIE, got %q", repo.fileMetadata.Category)
	}
	if repo.ids.Category != "MOVIE" {
		t.Fatalf("expected persisted external IDs category MOVIE, got %q", repo.ids.Category)
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

func TestResolveExternalIDsPassesLogoSettingsToTMDB(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "MOVIE"},
		metadata:      tmdb.MetadataResult{Title: "Example", Year: 2024, TMDBType: "Movie"},
	}
	svc := NewService(repo,
		WithConfig(config.Config{Description: config.DescriptionSettingsConfig{AddLogo: true, LogoLanguage: "ja,en"}}),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	_, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/media/file.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(tmdbClient.metaInputs) != 1 {
		t.Fatalf("expected one metadata fetch, got %d", len(tmdbClient.metaInputs))
	}
	input := tmdbClient.metaInputs[0]
	if !input.AddLogo {
		t.Fatalf("expected AddLogo to be passed")
	}
	if strings.Join(input.LogoLanguages, ",") != "ja,en" {
		t.Fatalf("expected logo languages ja,en, got %#v", input.LogoLanguages)
	}
}

func TestResolveExternalIDsRefetchesMissingTMDBLogo(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		metadata: tmdb.MetadataResult{
			Title:    "Example",
			Year:     2024,
			TMDBType: "Movie",
			Logo:     "https://image.tmdb.org/t/p/original/logo.png",
			TMDBLogo: "logo.png",
		},
	}
	svc := NewService(repo,
		WithConfig(config.Config{Description: config.DescriptionSettingsConfig{AddLogo: true, LogoLanguage: "en"}}),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		ExternalIDs: api.ExternalIDs{
			SourcePath: "/media/file.mkv",
			TMDBID:     42,
			Category:   "MOVIE",
		},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "/media/file.mkv",
			TMDB:       &api.TMDBMetadata{TMDBID: 42, Title: "Cached without logo"},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tmdbClient.metaCalls != 1 {
		t.Fatalf("expected one metadata refetch for logo, got %d", tmdbClient.metaCalls)
	}
	if result.ExternalMetadata.TMDB == nil || result.ExternalMetadata.TMDB.Logo == "" {
		t.Fatalf("expected logo to be refreshed, got %#v", result.ExternalMetadata.TMDB)
	}
}

func TestResolveExternalIDsUsesSceneTVmazeToResolveIMDb(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "From", Year: 2022}}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt9813792", Title: "From", Year: 2022}}
	tvdbClient := &stubTVDB{id: 401003, name: "FROM"}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{SelectedID: 54723, IMDBID: 9813792, TVDBID: 401003}}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:        "/media/From.S04E01.2160p.WEB.h265-ETHEL.mkv",
		SceneTVmazeID:     54723,
		Release:           api.ReleaseInfo{Category: "TV", Title: "From"},
		MediaInfoCategory: "TV",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TVmazeID != 54723 || result.ExternalIDs.SourceTVmaze != "scene" {
		t.Fatalf("expected scene tvmaze id, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.IMDBID != 9813792 || result.ExternalIDs.SourceIMDB != "tvmaze" {
		t.Fatalf("expected imdb from tvmaze, got %#v", result.ExternalIDs)
	}
	if tvmazeClient.calls == 0 || len(tvmazeClient.inputs) == 0 || tvmazeClient.inputs[0].ManualID != 54723 {
		t.Fatalf("expected tvmaze manual lookup, got calls=%d inputs=%#v", tvmazeClient.calls, tvmazeClient.inputs)
	}
	if repo.ids.IMDBID != 9813792 || repo.ids.TVmazeID != 54723 {
		t.Fatalf("expected persisted ids from tvmaze, got %#v", repo.ids)
	}
}

func TestResolveExternalIDsUsesSceneNFOIDs(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2024, TMDBType: "TV", MALID: 999}}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt0123456", Title: "Example", Year: 2024}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:    "/media/example.mkv",
		SceneTMDBID:   42,
		SceneIMDB:     123456,
		SceneTVDBID:   333,
		SceneTVmazeID: 444,
		SceneMALID:    555,
		Release:       api.ReleaseInfo{Category: "TV", Title: "Example"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TMDBID != 42 || result.ExternalIDs.SourceTMDB != "scene" {
		t.Fatalf("expected scene tmdb id, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.IMDBID != 123456 || result.ExternalIDs.SourceIMDB != "scene" {
		t.Fatalf("expected scene imdb id, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.TVDBID != 333 || result.ExternalIDs.SourceTVDB != "scene" {
		t.Fatalf("expected scene tvdb id, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.TVmazeID != 444 || result.ExternalIDs.SourceTVmaze != "scene" {
		t.Fatalf("expected scene tvmaze id, got %#v", result.ExternalIDs)
	}
	if result.MALID != 999 {
		t.Fatalf("expected tmdb mal to take precedence after metadata fetch, got %d", result.MALID)
	}
	if repo.ids.TMDBID != 42 || repo.ids.IMDBID != 123456 || repo.ids.TVDBID != 333 || repo.ids.TVmazeID != 444 {
		t.Fatalf("expected persisted scene ids, got %#v", repo.ids)
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
	tmdbClient := &stubTMDB{searchFn: func(_ tmdb.SearchInput) (tmdb.SearchOutcome, error) {
		return tmdb.SearchOutcome{TMDBID: 4242}, nil
	}}
	imdbClient := &stubIMDB{searchFn: func(_ imdb.SearchInput) (imdb.SearchResult, error) {
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

func TestResolveExternalIDsDoesNotTreatTMDBMovieWithoutIMDbAsTVMovie(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 1234, Category: "Movie"},
		metadata:      tmdb.MetadataResult{Title: "Example Movie", TMDBType: "Movie"},
	}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{idWhenTVMovie: 55, nameWhenTVMovie: "Example TV Movie"}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Example.Movie.2024.2160p.WEB-DL.mkv",
		MediaInfoCategory: "movie",
		Release:           api.ReleaseInfo{Title: "Example Movie", Year: 2024},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TVDBID != 0 {
		t.Fatalf("expected no tvdb id without imdb tv movie metadata, got %d", result.ExternalIDs.TVDBID)
	}
	if len(tvdbClient.tvMovieCalls) != 0 {
		t.Fatalf("expected no tvdb lookup for movie category, got calls %#v", tvdbClient.tvMovieCalls)
	}
}

func TestResolveExternalIDsDoesNotApplyTVDBForMovieCategory(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 1234, Category: "Movie"},
		metadata:      tmdb.MetadataResult{Title: "Example TV Movie", TMDBType: "Movie", TVDBID: 55},
	}
	imdbClient := &stubIMDB{
		searchResult: imdb.SearchResult{IMDbID: 9876},
		info:         imdb.Info{IMDbID: "tt0009876", Title: "Example TV Movie", Type: "tvMovie"},
	}
	tvdbClient := &stubTVDB{idWhenTVMovie: 55, nameWhenTVMovie: "Example TV Movie"}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := api.PreparedMetadata{
		SourcePath:        "/media/Example.TV.Movie.2024.1080p.WEB-DL.mkv",
		MediaInfoCategory: "movie",
		Release:           api.ReleaseInfo{Title: "Example TV Movie", Year: 2024},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalIDs.TVDBID != 0 {
		t.Fatalf("expected no tvdb id for movie category, got %d", result.ExternalIDs.TVDBID)
	}
	if result.ExternalMetadata.TVDB != nil {
		t.Fatalf("expected no tvdb metadata for movie category, got %#v", result.ExternalMetadata.TVDB)
	}
	if len(tvdbClient.tvMovieCalls) != 0 {
		t.Fatalf("expected no tvdb lookup for movie category, got %#v", tvdbClient.tvMovieCalls)
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
			TMDBID: new(999),
			IMDBID: new(111),
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
			TMDBID: new(999),
			IMDBID: new(0),
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
			TMDBID: new(0),
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

func TestApplyTVEpisodeMetadataTVDBAliasYearPreservesSource(t *testing.T) {
	tests := []struct {
		name       string
		yearSource string
		confidence string
	}{
		{name: "translation name", yearSource: "translation_name", confidence: "high"},
		{name: "translation alias", yearSource: "translation_alias", confidence: "high"},
		{name: "extended alias", yearSource: "extended_alias", confidence: "high"},
		{name: "slug", yearSource: "slug", confidence: "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeRepo{})
			tmdbClient := &stubTMDB{}
			tvdbClient := &stubTVDB{
				episodes: tvdb.EpisodesData{
					SeriesYear:           2011,
					SeriesYearSource:     tt.yearSource,
					SeriesYearConfidence: tt.confidence,
				},
				specificAlias: "Hunter x Hunter (2011)",
			}

			meta := api.PreparedMetadata{
				SourcePath: "/media/Hunter.x.Hunter.S01E01.mkv",
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

			if external.TVDB.Year != 2011 {
				t.Fatalf("expected alias year 2011, got %d", external.TVDB.Year)
			}
			if external.TVDB.YearSource != tt.yearSource {
				t.Fatalf("expected preserved year source %q, got %q", tt.yearSource, external.TVDB.YearSource)
			}
			if external.TVDB.YearConfidence != tt.confidence {
				t.Fatalf("expected preserved year confidence %q, got %q", tt.confidence, external.TVDB.YearConfidence)
			}
		})
	}
}

func TestApplyTVEpisodeMetadataTVDBTitleOnlyAliasAppliedWithoutYear(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{specificAlias: "Cats Eye"}

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
		t.Fatalf("expected title-only alias, got %q", external.TVDB.Name)
	}
	if external.TVDB.Year != 0 {
		t.Fatalf("expected no alias year, got %d", external.TVDB.Year)
	}
	if external.TVDB.YearFromAlias {
		t.Fatalf("expected title-only alias not to mark YearFromAlias")
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

func TestApplyTVEpisodeMetadataTVDBEpisodeTitleSkipsOriginalWhenEnglishMissing(t *testing.T) {
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

	if updated.EpisodeTitle != "" {
		t.Fatalf("expected non-english original episode title to be skipped, got %q", updated.EpisodeTitle)
	}
}

func TestApplyTVEpisodeMetadataDiscardsSeriesTitleAsEpisodeTitle(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvmazeClient := &stubTVmaze{
		episodeData: &tvmaze.EpisodeData{
			EpisodeName:   "The Long Road",
			SeasonNumber:  4,
			EpisodeNumber: 11,
		},
	}

	meta := api.PreparedMetadata{
		SourcePath:   "/media/Re.ZERO.S04E11.mkv",
		SeasonInt:    4,
		EpisodeInt:   11,
		EpisodeTitle: "Re:ZERO -Starting Life in Another World-",
		ExternalIDs:  api.ExternalIDs{Category: "TV"},
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{NameEnglish: "Re: ZERO, Starting Life in Another World"},
		},
	}
	ids := &api.ExternalIDs{
		TVDBID:   200,
		TVmazeID: 300,
		Category: "TV",
	}
	external := &api.ExternalMetadata{
		TVDB: &api.TVDBMetadata{TVDBID: 200, NameEnglish: "Re: ZERO, Starting Life in Another World"},
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, &stubTVDB{}, tvmazeClient)

	if updated.EpisodeTitle != "The Long Road" {
		t.Fatalf("expected provider episode title to replace duplicate series title, got %q", updated.EpisodeTitle)
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
			TVDBID: new(200),
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

func TestResolveExternalIDsTVDBSlugYearNotUsedForNamingYear(t *testing.T) {
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
			TVDBID: new(200),
		},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalMetadata.TVDB == nil {
		t.Fatalf("expected tvdb metadata")
	}
	if result.ExternalMetadata.TVDB.Year != 2010 {
		t.Fatalf("expected first-aired tvdb year 2010, got %d", result.ExternalMetadata.TVDB.Year)
	}
	if result.ExternalMetadata.TVDB.YearFromAlias {
		t.Fatalf("expected slug-derived year not to mark YearFromAlias")
	}
}

func TestMapTVDBMetadataAPIYearDoesNotBecomeNamingYear(t *testing.T) {
	mapped := mapTVDBMetadata(402296, "", tvdb.SeriesMetadata{
		TVDBID:           402296,
		Name:             "A Spy Among Friends",
		NameEnglish:      "A Spy Among Friends",
		SeriesYear:       2022,
		FirstAired:       "2022-12-08",
		OriginalLanguage: "eng",
		HasEnglish:       true,
	})

	if mapped == nil {
		t.Fatalf("expected mapped metadata")
	}
	if mapped.Year != 2022 {
		t.Fatalf("expected first-aired tvdb year 2022, got %d", mapped.Year)
	}
	if mapped.YearFromAlias {
		t.Fatalf("expected api year not to mark YearFromAlias")
	}
	if mapped.YearSource != "first_aired" {
		t.Fatalf("expected first_aired year source, got %q", mapped.YearSource)
	}
}

func TestMergeTVDBMetadataClearsStaleAliasYear(t *testing.T) {
	target := &api.TVDBMetadata{
		TVDBID:         402296,
		Name:           "A Spy Among Friends",
		Year:           2025,
		YearFromAlias:  true,
		YearSource:     "translation_alias",
		YearConfidence: "high",
	}
	incoming := &api.TVDBMetadata{
		TVDBID:     402296,
		Name:       "A Spy Among Friends",
		FirstAired: "2022-12-08",
		Year:       2022,
		YearSource: "first_aired",
	}

	mergeTVDBMetadata(target, incoming)

	if target.Year != 2022 {
		t.Fatalf("expected stale alias year replaced with incoming first-aired year, got %d", target.Year)
	}
	if target.YearFromAlias {
		t.Fatalf("expected incoming non-alias metadata to clear YearFromAlias")
	}
	if target.YearSource != "first_aired" {
		t.Fatalf("expected incoming non-alias year source, got %q", target.YearSource)
	}
	if target.YearConfidence != "" {
		t.Fatalf("expected alias confidence cleared, got %q", target.YearConfidence)
	}
}

func TestMergeTVDBMetadataRefreshesAliasYear(t *testing.T) {
	target := &api.TVDBMetadata{
		TVDBID:         200,
		Name:           "Cats Eye",
		Year:           2024,
		YearFromAlias:  true,
		YearSource:     "extended_alias",
		YearConfidence: "medium",
	}
	incoming := &api.TVDBMetadata{
		TVDBID:         200,
		Name:           "Cats Eye",
		Year:           2025,
		YearFromAlias:  true,
		YearSource:     "translation_alias",
		YearConfidence: "high",
	}

	mergeTVDBMetadata(target, incoming)

	if target.Year != 2025 {
		t.Fatalf("expected alias-derived year refreshed from incoming metadata, got %d", target.Year)
	}
	if !target.YearFromAlias {
		t.Fatalf("expected alias-derived year provenance to remain set")
	}
	if target.YearSource != "translation_alias" {
		t.Fatalf("expected incoming alias year source, got %q", target.YearSource)
	}
	if target.YearConfidence != "high" {
		t.Fatalf("expected incoming alias confidence, got %q", target.YearConfidence)
	}
}

func TestResolveExternalIDsTVDBExplicitSeriesYearUsedForNamingYear(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{
		seriesMetadata: tvdb.SeriesMetadata{
			TVDBID:               200,
			Name:                 "Cats Eye",
			NameEnglish:          "Cats Eye",
			SeriesYear:           2025,
			SeriesYearSource:     "translation_alias",
			SeriesYearConfidence: "high",
			Slug:                 "cats-eye-2025",
			FirstAired:           "2010-10-01",
			OriginalLanguage:     "jpn",
			HasEnglish:           true,
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
			TVDBID: new(200),
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
		t.Fatalf("expected explicit tvdb series year 2025, got %d", result.ExternalMetadata.TVDB.Year)
	}
	if !result.ExternalMetadata.TVDB.YearFromAlias {
		t.Fatalf("expected explicit series year to mark YearFromAlias")
	}
	if result.ExternalMetadata.TVDB.YearSource != "translation_alias" {
		t.Fatalf("expected translation alias year source, got %q", result.ExternalMetadata.TVDB.YearSource)
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
			TMDBID: new(101),
			MALID:  new(999),
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
			OriginalLanguage: new("ja"),
		},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: new(101),
			IMDBID: new(202),
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

func TestResolveExternalIDsLocalizedFetchSucceedsWhenTMDBMetadataIsNil(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "MOVIE"},
		metadataErr:   errors.New("tmdb metadata fetch failed"),
		localizedData: map[string]any{
			"title":    "Título Localizado",
			"overview": "Sinopse Localizada",
		},
	}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	// Make sure BJS/BT/ASC is in Trackers list so needsPTBR is true
	meta := api.PreparedMetadata{
		SourcePath: "/media/file.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
		Trackers:   []string{"BJS"},
	}

	result, err := svc.ResolveExternalIDs(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ExternalMetadata.TMDB == nil {
		t.Fatalf("expected TMDB metadata to be initialized with localized info")
	}
	if result.ExternalMetadata.TMDB.Localized == nil {
		t.Fatalf("expected Localized map to be populated")
	}
	ptBR, ok := result.ExternalMetadata.TMDB.Localized["pt-BR"]
	if !ok || ptBR.Title != "Título Localizado" {
		t.Fatalf("expected localized title, got %#v", ptBR)
	}
	if len(tmdbClient.localizedInputs) != 1 || tmdbClient.localizedInputs[0].AppendToResponse != "credits,videos,release_dates" {
		t.Fatalf("expected movie localized fetch to append release_dates, got %#v", tmdbClient.localizedInputs)
	}
}

func TestResolveExternalIDsSkipsIncompleteLocalizedPTBR(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "MOVIE"},
		metadataErr:   errors.New("tmdb metadata fetch failed"),
		localizedData: map[string]any{
			"overview": "Sinopse sem titulo",
		},
	}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/media/file.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
		Trackers:   []string{"BJS"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.ExternalMetadata.TMDB != nil && result.ExternalMetadata.TMDB.Localized != nil {
		if got, ok := result.ExternalMetadata.TMDB.Localized["pt-BR"]; ok {
			t.Fatalf("expected incomplete localized data to stay unstored, got %#v", got)
		}
	}
}

func TestResolveExternalIDsStoresEpisodeOverviewWithoutEpisodeTitle(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "TV"},
		metadataErr:   errors.New("tmdb metadata fetch failed"),
		localizedByType: map[string]map[string]any{
			"main": {
				"name": "Serie localizada",
				"genres": []any{
					map[string]any{"name": "Drama"},
				},
			},
			"episode": {
				"overview": "Resumo do episodio",
			},
		},
	}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/media/show.s01e02.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
		SeasonInt:  1,
		EpisodeInt: 2,
		Trackers:   []string{"BJS"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	got := result.ExternalMetadata.TMDB.Localized["pt-BR"]
	if got.Title != "Serie localizada" || got.EpisodeOverview != "Resumo do episodio" {
		t.Fatalf("expected localized episode overview to be stored, got %#v", got)
	}
	if got.EpisodeTitle != "" {
		t.Fatalf("expected blank episode title to stay blank, got %q", got.EpisodeTitle)
	}
}

func TestResolveExternalIDsSkipsEpisodeLocalizedPTBRWithoutScopedOverview(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "TV"},
		metadataErr:   errors.New("tmdb metadata fetch failed"),
		localizedByType: map[string]map[string]any{
			"main": {
				"name":     "Serie localizada",
				"overview": "Resumo da serie",
			},
		},
	}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/media/show.s01e02.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
		SeasonInt:  1,
		EpisodeInt: 2,
		Trackers:   []string{"BJS"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.ExternalMetadata.TMDB != nil && result.ExternalMetadata.TMDB.Localized != nil {
		if got, ok := result.ExternalMetadata.TMDB.Localized["pt-BR"]; ok {
			t.Fatalf("expected episode localized data without scoped overview to stay unstored, got %#v", got)
		}
	}
}

func TestResolveExternalIDsMergesLocalizedPTBRWithoutBlankOverwrite(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "MOVIE"},
		metadataErr:   errors.New("tmdb metadata fetch failed"),
		localizedData: map[string]any{
			"title": "Titulo novo",
		},
	}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)
	existing := api.TMDBLocalizedData{
		Title:    "Titulo antigo",
		Overview: "Sinopse existente",
		Genres:   "Drama",
	}

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		Release:         api.ReleaseInfo{Title: "Example", Year: 2024},
		Trackers:        []string{"BJS"},
		ExternalIDs: api.ExternalIDs{
			SourcePath: "/media/file.mkv",
			TMDBID:     42,
			Category:   "MOVIE",
		},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "/media/file.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID: 42,
				Localized: map[string]api.TMDBLocalizedData{
					"pt-BR": existing,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := result.ExternalMetadata.TMDB.Localized["pt-BR"]
	if got.Title != "Titulo novo" {
		t.Fatalf("expected new title merged, got %#v", got)
	}
	if got.Overview != existing.Overview || got.Genres != existing.Genres {
		t.Fatalf("expected existing fields preserved, got %#v", got)
	}
}

func TestResolveExternalIDsPreservesExistingLocalizedPTBRWhenEpisodeFetchFails(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "TV"},
		metadataErr:   errors.New("tmdb metadata fetch failed"),
		localizedErr:  errors.New("localized fetch failed"),
		localizedByType: map[string]map[string]any{
			"main": {
				"name": "Serie nova",
				"genres": []any{
					map[string]any{"name": "Drama"},
				},
			},
		},
	}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)
	existing := api.TMDBLocalizedData{
		Title:           "Serie antiga",
		Overview:        "Resumo antigo",
		EpisodeOverview: "Resumo antigo do episodio",
		Genres:          "Acao",
	}

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath:      "/media/show.s01e02.mkv",
		StoredDataFresh: true,
		Release:         api.ReleaseInfo{Title: "Example", Year: 2024},
		SeasonInt:       1,
		EpisodeInt:      2,
		Trackers:        []string{"BJS"},
		ExternalIDs: api.ExternalIDs{
			SourcePath: "/media/show.s01e02.mkv",
			TMDBID:     42,
			Category:   "TV",
		},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "/media/show.s01e02.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID: 42,
				Localized: map[string]api.TMDBLocalizedData{
					"pt-BR": existing,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(tmdbClient.localizedInputs) != 3 {
		t.Fatalf("expected main, season, episode localized fetch attempts, got %#v", tmdbClient.localizedInputs)
	}
	got := result.ExternalMetadata.TMDB.Localized["pt-BR"]
	if got.Title != "Serie nova" || got.Genres != "Drama" {
		t.Fatalf("expected nonblank main localized fields to merge, got %#v", got)
	}
	if got.Overview != existing.Overview || got.EpisodeOverview != existing.EpisodeOverview {
		t.Fatalf("expected failed scoped fetch to preserve existing localized text, got %#v", got)
	}
}

func TestResolveExternalIDsLocalizedFetchUsesVariantCachePaths(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "TV"},
		metadataErr:   errors.New("tmdb metadata fetch failed"),
		localizedData: map[string]any{
			"name":     "Serie",
			"overview": "Sinopse",
		},
	}
	svc := NewService(repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	_, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/media/show.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
		Trackers:   []string{"ASC"},
		SeasonInt:  1,
		EpisodeInt: 2,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(tmdbClient.localizedInputs) != 3 {
		t.Fatalf("expected main, season, episode localized fetches, got %#v", tmdbClient.localizedInputs)
	}
	seen := make(map[string]struct{}, len(tmdbClient.localizedInputs))
	for _, input := range tmdbClient.localizedInputs {
		if strings.TrimSpace(input.CachePath) == "" {
			t.Fatalf("expected cache path for input %#v", input)
		}
		if _, ok := seen[input.CachePath]; ok {
			t.Fatalf("expected distinct cache paths, got duplicate %q in %#v", input.CachePath, tmdbClient.localizedInputs)
		}
		seen[input.CachePath] = struct{}{}
	}
	if tmdbClient.localizedInputs[0].AppendToResponse != "credits,videos,content_ratings" {
		t.Fatalf("expected TV content_ratings append, got %q", tmdbClient.localizedInputs[0].AppendToResponse)
	}
}

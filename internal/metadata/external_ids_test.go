// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type localizedMetadataTestDefinition struct{}

func (localizedMetadataTestDefinition) Name() string { return "BJS" }

func (localizedMetadataTestDefinition) UploadContentMode() trackers.UploadContentMode {
	return trackers.UploadContentModeDescription
}

func (localizedMetadataTestDefinition) DefaultBaseURL() string {
	return "https://tracker.example.invalid"
}

func (localizedMetadataTestDefinition) LocalizedMetadataLocale() string { return "pt-BR" }

func (localizedMetadataTestDefinition) Prepare(context.Context, trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.TrackerPlan{}, nil
}

func localizedMetadataTestRegistry(t *testing.T) *trackers.Registry {
	t.Helper()
	registry := trackers.NewRegistry()
	if err := registry.Register(localizedMetadataTestDefinition{}); err != nil {
		t.Fatalf("register localized metadata test tracker: %v", err)
	}
	return registry
}

type fakeRepo struct {
	ids                 api.ExternalIdentity
	meta                api.SourceScopedMetadata
	fileMetadata        api.FileMetadata
	trackerMetadata     []api.TrackerMetadata
	trackerTimestamps   []api.TrackerTimestamp
	trackerRuleFailures []api.TrackerRuleFailure
	externalMetaSaves   int
}

type recordingEvidencePipeline struct {
	service *Service
	state   preparationstate.State
}

type staticCandidateSource struct {
	state preparationstate.State
}

func (s staticCandidateSource) ResolveIdentityCandidate(
	context.Context,
	externalidentity.Request,
) (externalidentity.CandidateEvidence, error) {
	return externalidentity.CandidateEvidence{
		Identity:   s.state.Identity,
		Metadata:   s.state.ProviderMetadata,
		Candidates: s.state.ExternalIdentityCandidates,
	}, nil
}

func (p *recordingEvidencePipeline) CollectPreparationEvidence(
	ctx context.Context,
	request preparationstate.Request,
) (preparationstate.State, error) {
	state, err := p.service.CollectPreparationEvidence(ctx, request)
	p.state = state
	return state, err
}

// resolveExternalIdentity keeps provider-adapter tests focused on the candidate
// returned to canonical preparation; production collection never publishes it.
func (s *Service) resolveExternalIdentity(ctx context.Context, meta preparationstate.State) (preparationstate.State, error) {
	return s.collectExternalIdentityEvidence(ctx, meta)
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

func (f *fakeRepo) GetExternalIdentity(_ context.Context, path string) (api.ExternalIdentity, error) {
	if strings.EqualFold(strings.TrimSpace(f.ids.SourcePath), strings.TrimSpace(path)) {
		return f.ids, nil
	}
	return api.ExternalIdentity{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveExternalIdentity(_ context.Context, ids api.ExternalIdentity) error {
	f.ids = ids
	return nil
}

func TestResolveExternalIDsCandidateDoesNotPersist(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithIMDBClient(&stubIMDB{}))
	tmdbID := 1234567

	result, err := svc.collectExternalIdentityEvidence(context.Background(), preparationstate.State{
		SourcePath: "/media/Example.Release.2026.1080p-GRP.mkv",
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Title:    "Example Release",
			Year:     2026,
		},
		ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: &tmdbID},
	})
	if err != nil {
		t.Fatalf("collectExternalIdentityEvidence() error = %v", err)
	}
	if result.Identity.TMDBID != tmdbID {
		t.Fatalf("candidate TMDB ID = %d, want %d", result.Identity.TMDBID, tmdbID)
	}
	if repo.ids.TMDBID != 0 || repo.externalMetaSaves != 0 || repo.fileMetadata.Category != "" {
		t.Fatalf("candidate resolution persisted state ids=%#v metadata_saves=%d file=%#v", repo.ids, repo.externalMetaSaves, repo.fileMetadata)
	}
}

func (f *fakeRepo) GetExternalMetadata(_ context.Context, path string) (api.SourceScopedMetadata, error) {
	if strings.EqualFold(strings.TrimSpace(f.meta.SourcePath), strings.TrimSpace(path)) {
		return f.meta, nil
	}
	return api.SourceScopedMetadata{}, internalerrors.ErrNotFound
}

func (f *fakeRepo) SaveExternalMetadata(_ context.Context, metadata api.SourceScopedMetadata) error {
	f.externalMetaSaves++
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

func (f *fakeRepo) SavePlaylistSelection(_ context.Context, _ string, _ string, _ []string, _ bool) error {
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
	anilistMetadata tmdb.AniListMetadataResult
	metadataErr     error
	anilistErr      error
	searchFn        func(tmdb.SearchInput) (tmdb.SearchOutcome, error)
	findFn          func(tmdb.FindInput) (tmdb.FindResult, error)
	metadataFn      func(tmdb.MetadataInput) (tmdb.MetadataResult, error)
	dailySeason     int
	dailyEpisode    int
	dailyErr        error
	episodeDetails  tmdb.EpisodeDetails
	seasonDetails   tmdb.SeasonDetails
	localizedData   map[string]any
	localizedByType map[string]map[string]any
	localizedErr    error
	searchErr       error
	findErr         error
	searchCalls     int
	findCalls       int
	metaCalls       int
	anilistCalls    int
	dailyCalls      int
	episodeCalls    int
	seasonCalls     int
	localizedInputs []tmdb.LocalizedDataInput
	searchInputs    []tmdb.SearchInput
	findInputs      []tmdb.FindInput
	metaInputs      []tmdb.MetadataInput
	anilistInputs   []int
}

func (s *stubTMDB) FindByExternalID(_ context.Context, input tmdb.FindInput) (tmdb.FindResult, error) {
	s.findCalls++
	s.findInputs = append(s.findInputs, input)
	if s.findFn != nil {
		return s.findFn(input)
	}
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
	if s.metadataFn != nil {
		return s.metadataFn(input)
	}
	if s.metadataErr != nil {
		return tmdb.MetadataResult{}, s.metadataErr
	}
	return s.metadata, nil
}

func (s *stubTMDB) FetchAniListMetadata(_ context.Context, malID int) (tmdb.AniListMetadataResult, error) {
	s.anilistCalls++
	s.anilistInputs = append(s.anilistInputs, malID)
	if s.anilistErr != nil {
		return tmdb.AniListMetadataResult{}, s.anilistErr
	}
	return s.anilistMetadata, nil
}

func (s *stubTMDB) GetEpisodeDetails(_ context.Context, _, _, _ int) (tmdb.EpisodeDetails, error) {
	s.episodeCalls++
	return s.episodeDetails, nil
}

func (s *stubTMDB) GetSeasonDetails(_ context.Context, _, _ int) (tmdb.SeasonDetails, error) {
	s.seasonCalls++
	return s.seasonDetails, nil
}

func (s *stubTMDB) DailyToSeasonEpisode(_ context.Context, _ int, _ time.Time) (int, int, error) {
	s.dailyCalls++
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
	infoFn             func(string) imdb.Info
	episodeLookup      imdb.EpisodeLookup
	episodeLookupCalls int
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

func (s *stubIMDB) GetInfo(_ context.Context, imdbID string, manualLanguage string, _ bool) (imdb.Info, error) {
	s.infoCalls++
	s.lastManualLanguage = manualLanguage
	if s.infoFn != nil {
		return s.infoFn(imdbID), nil
	}
	return s.info, nil
}

func (s *stubIMDB) GetEpisodeInfo(_ context.Context, _ string, _ bool) (imdb.EpisodeLookup, error) {
	s.episodeLookupCalls++
	return s.episodeLookup, nil
}

type stubTVDB struct {
	id                       int
	name                     string
	calls                    int
	tvMovieCalls             []bool
	idWhenTVMovie            int
	nameWhenTVMovie          string
	episodes                 tvdb.EpisodesData
	specificAlias            string
	episodeErr               error
	episodeTranslate         tvdb.EpisodeTranslation
	episodeTransErr          error
	seriesMetadata           tvdb.SeriesMetadata
	nameDisambiguation       tvdb.NameDisambiguation
	episodeCalls             int
	seriesMetadataCalls      int
	seriesLangCalls          []string
	nameDisambiguationInputs []tvdb.NameDisambiguationInput
	episodeLangCalls         []string
	episodeTransCalls        []int
	lastEpisodeQuery         tvdb.EpisodeQuery
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
	s.seriesMetadataCalls++
	result := s.seriesMetadata
	if s.seriesMetadata.TVDBID != 0 || s.seriesMetadata.Name != "" {
		if result.NameDisambiguation.Status == "" {
			seriesYear := result.Year
			if seriesYear == 0 {
				seriesYear = result.SeriesYear
			}
			result.NameDisambiguation = tvdb.NameDisambiguation{
				CanonicalName: result.NameEnglish,
				SeriesYear:    seriesYear,
				Status:        api.MetadataEvidenceStatusUnavailable,
				Source:        "test",
			}
		}
		return result, nil
	}
	return tvdb.SeriesMetadata{
		TVDBID: seriesID,
		Name:   s.name,
		NameDisambiguation: tvdb.NameDisambiguation{
			CanonicalName: s.name,
			Status:        api.MetadataEvidenceStatusUnavailable,
			Source:        "test",
		},
	}, nil
}

func (s *stubTVDB) GetSeriesMetadataWithLanguage(ctx context.Context, seriesID int, language string) (tvdb.SeriesMetadata, error) {
	s.seriesLangCalls = append(s.seriesLangCalls, language)
	return s.GetSeriesMetadata(ctx, seriesID)
}

func (s *stubTVDB) GetNameDisambiguation(_ context.Context, input tvdb.NameDisambiguationInput) tvdb.NameDisambiguation {
	s.nameDisambiguationInputs = append(s.nameDisambiguationInputs, input)
	return s.nameDisambiguation
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
	result             tvmaze.SearchResult
	episodeData        *tvmaze.EpisodeData
	calls              int
	episodeNumberCalls int
	episodeDateCalls   int
	inputs             []tvmaze.SearchInput
	lastSeason         int
	lastEpisode        int
}

func (s *stubTVmaze) Search(_ context.Context, input tvmaze.SearchInput) (tvmaze.SearchResult, error) {
	s.calls++
	s.inputs = append(s.inputs, input)
	return s.result, nil
}

func (s *stubTVmaze) GetEpisodeByNumber(_ context.Context, _, season, episode int, _ tvmaze.EpisodeLookupContext) (*tvmaze.EpisodeData, error) {
	s.episodeNumberCalls++
	s.lastSeason = season
	s.lastEpisode = episode
	return s.episodeData, nil
}

func (s *stubTVmaze) GetEpisodeByDate(_ context.Context, _ int, _ string) (*tvmaze.EpisodeData, error) {
	s.episodeDateCalls++
	return nil, nil
}

func TestResolveExternalIDsPrecedence(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2024}}
	imdbClient := &stubIMDB{info: imdb.Info{
		IMDbID: "tt0000001",
		Title:  "Example",
		Year:   2024,
	}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := preparationstate.State{
		SourcePath:      "/media/file.mkv",
		MediaInfoTMDBID: 999,
		MediaInfoIMDBID: 888,
		MediaInfoTVDBID: 777,
		SceneIMDB:       666,
		TrackerData: []api.TrackerMetadata{{
			TMDBID: 1,
			IMDBID: 2,
			TVDBID: 3,
		}},
		MediaInfoCategory: "TV",
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 1 || result.Identity.IMDBID != 2 || result.Identity.TVDBID != 3 {
		t.Fatalf("unexpected resolved ids: %#v", result.Identity)
	}
	if result.Identity.Provenance.TMDB != "tracker" || result.Identity.Provenance.IMDB != "tracker" || result.Identity.Provenance.TVDB != "tracker" {
		t.Fatalf("unexpected sources: %#v", result.Identity)
	}
	if repo.ids.SourcePath != "" || repo.externalMetaSaves != 0 {
		t.Fatal("provider candidate must not publish identity or metadata")
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.IMDB == nil {
		t.Fatalf("expected metadata results")
	}
}

func TestResolveExternalIDsWithoutTMDBAPIKey(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo,
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Release.2026.1080p-GRP.mkv",
		MediaInfoCategory: "MOVIE",
		TrackerData:       []api.TrackerMetadata{{TMDBID: 123456}},
	})
	if err != nil {
		t.Fatalf("resolve without TMDB API key: %v", err)
	}
	if result.Identity.TMDBID != 123456 || result.Identity.Provenance.TMDB != "tracker" {
		t.Fatalf("expected tracker TMDB ID without enrichment, got %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB != nil {
		t.Fatalf("expected TMDB enrichment to remain unavailable, got %#v", result.ProviderMetadata.TMDB)
	}
}

func TestResolveExternalIDsPreservesExplicitEpisodeIMDbID(t *testing.T) {
	repo := &fakeRepo{}
	imdbClient := &stubIMDB{
		infoFn: func(imdbID string) imdb.Info {
			if imdbID == "tt7654321" {
				return imdb.Info{
					IMDbID: imdbID,
					Title:  "Example Episode",
					Type:   "tvEpisode",
				}
			}
			return imdb.Info{
				IMDbID: imdbID,
				Title:  "Example Series",
				Type:   "tvSeries",
			}
		},
		episodeLookup: imdb.EpisodeLookup{Series: imdb.SeriesInfo{SeriesID: "tt1234567", SeriesTitle: "Example Series"}},
	}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 55,
		IMDBID:     1234567,
		Candidates: []tvmaze.Candidate{{
			ID:        55,
			Name:      "Example Series",
			Externals: tvmaze.Externals{IMDB: "tt1234567"},
		}},
	}}
	imdbOverride := 7654321
	svc := NewService(repo,
		WithIMDBClient(imdbClient),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Series.-.02.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			IMDBID: &imdbOverride,
		},
	})
	if err != nil {
		t.Fatalf("resolve episode IMDb ID: %v", err)
	}

	if result.Identity.IMDBID != 7654321 || result.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit {
		t.Fatalf("expected explicit episode IMDb ID, got %#v", result.Identity)
	}
	if result.ProviderMetadata.IMDB == nil || result.ProviderMetadata.IMDB.Title != "Example Episode" || result.ProviderMetadata.IMDB.Type != "tvEpisode" {
		t.Fatalf("expected explicit episode IMDb metadata, got %#v", result.ProviderMetadata.IMDB)
	}
	if imdbClient.episodeLookupCalls != 0 || imdbClient.infoCalls != 1 {
		t.Fatalf("explicit IMDb ID was rewritten: lookups=%d info=%d", imdbClient.episodeLookupCalls, imdbClient.infoCalls)
	}
}

func TestEnsureExternalClientsInitializesKeylessAniListWithoutTMDBAPIKey(t *testing.T) {
	svc := NewService(&fakeRepo{})

	tmdbClient, anilistClient, _, _, _ := svc.ensureExternalClients()
	if tmdbClient != nil {
		t.Fatal("expected TMDB client to remain unavailable without API key")
	}
	if anilistClient == nil {
		t.Fatal("expected keyless AniList client without TMDB API key")
	}
}

func TestResolveExternalIDsRefreshesAniListWithoutTMDBAPIKey(t *testing.T) {
	sourcePath := "Example.Anime.S01E01.2026.1080p-GRP.mkv"
	repo := &fakeRepo{
		meta: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			AniList: &api.AniListMetadata{
				AniListID:   100,
				MALID:       200,
				TitleRomaji: "Stale Anime",
			},
		},
	}
	anilistClient := &stubTMDB{
		anilistMetadata: tmdb.AniListMetadataResult{
			AniListID:   300,
			MALID:       400,
			TitleRomaji: "Current Anime",
		},
	}
	svc := NewService(repo,
		WithAniListClient(anilistClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: sourcePath,
		MALID:      400,
	})
	if err != nil {
		t.Fatalf("resolve without TMDB API key: %v", err)
	}
	if svc.tmdb != nil {
		t.Fatal("expected TMDB client to remain unavailable without API key")
	}
	if anilistClient.anilistCalls != 1 || len(anilistClient.anilistInputs) != 1 || anilistClient.anilistInputs[0] != 400 {
		t.Fatalf("expected one keyless AniList fetch for MAL 400, got calls=%d inputs=%v", anilistClient.anilistCalls, anilistClient.anilistInputs)
	}
	if result.ProviderMetadata.TMDB != nil {
		t.Fatalf("expected TMDB enrichment to remain unavailable, got %#v", result.ProviderMetadata.TMDB)
	}
	if result.ProviderMetadata.AniList == nil || result.ProviderMetadata.AniList.MALID != 400 || result.ProviderMetadata.AniList.TitleRomaji != "Current Anime" {
		t.Fatalf("expected refreshed AniList metadata, got %#v", result.ProviderMetadata.AniList)
	}
	if repo.externalMetaSaves != 0 {
		t.Fatal("provider candidate must not publish refreshed AniList metadata")
	}
}

func TestResolveExternalIDsDoesNotCreateTVmazePlaceholder(t *testing.T) {
	repo := &fakeRepo{}
	tvmazeClient := &stubTVmaze{}
	svc := NewService(repo,
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Release.2026.S01.1080p.WEB-DL-GRP",
		MediaInfoCategory: "TV",
		SceneTVmazeID:     55,
	})
	if err != nil {
		t.Fatalf("resolve unresolved TVmaze ID: %v", err)
	}
	if result.Identity.TVmazeID != 55 {
		t.Fatalf("expected canonical TVmaze ID to remain, got %#v", result.Identity)
	}
	if result.ProviderMetadata.TVmaze != nil {
		t.Fatalf("expected no placeholder TVmaze snapshot, got %#v", result.ProviderMetadata.TVmaze)
	}
	if tvmazeClient.calls == 0 {
		t.Fatal("expected TVmaze fetch attempt")
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

			mapped := mapTMDBMetadata(api.ExternalIdentity{TMDBID: 123}, result)
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

func TestResolveExternalIDsPreservesCanonicalTrackerIdentity(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2024}}
	imdbClient := &stubIMDB{info: imdb.Info{
		IMDbID: "tt0000002",
		Title:  "Example",
		Year:   2024,
	}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: "/media/file.mkv",
			TMDBID:     1,
			IMDBID:     2,
			TVDBID:     3,
			Category:   api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{
				TMDB: api.IdentityProvenanceTracker,
				IMDB: api.IdentityProvenanceTracker,
				TVDB: api.IdentityProvenanceTracker,
			},
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

	if result.Identity.TMDBID != 1 || result.Identity.Provenance.TMDB != api.IdentityProvenanceTracker {
		t.Fatalf("expected canonical tracker tmdb identity, got %#v", result.Identity)
	}
	if result.Identity.IMDBID != 2 || result.Identity.Provenance.IMDB != api.IdentityProvenanceTracker {
		t.Fatalf("expected canonical tracker imdb identity, got %#v", result.Identity)
	}
	if result.Identity.TVDBID != 0 || result.Identity.Provenance.TVDB != "" {
		t.Fatalf("expected tvdb cleared for movie category, got %#v", result.Identity)
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

	reused, err := reuseSvc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: "/media/file.mkv",
			TMDBID:     1,
			IMDBID:     2,
			TVDBID:     3,
			Category:   api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{
				TMDB: api.IdentityProvenanceTracker,
				IMDB: api.IdentityProvenanceTracker,
				TVDB: api.IdentityProvenanceTracker,
			},
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "/media/file.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID:   1,
				Category: "movie",
				Title:    "Stored TMDB",
			},
			IMDB: &api.IMDBMetadata{IMDBID: 2, Title: "Stored IMDb"},
			TVDB: &api.TVDBMetadata{TVDBID: 3, Name: "Stored TVDB"},
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
	if reused.ProviderMetadata.TMDB == nil || reused.ProviderMetadata.TMDB.Title != "Stored TMDB" {
		t.Fatalf("expected stored tmdb metadata reused, got %#v", reused.ProviderMetadata.TMDB)
	}
	if reused.ProviderMetadata.IMDB == nil || reused.ProviderMetadata.IMDB.Title != "Stored IMDb" {
		t.Fatalf("expected stored imdb metadata reused, got %#v", reused.ProviderMetadata.IMDB)
	}
	if reused.ProviderMetadata.TVDB != nil {
		t.Fatalf("expected stored tvdb metadata cleared for movie category, got %#v", reused.ProviderMetadata.TVDB)
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

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: "/media/file.mkv",
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Title:    "Example",
			Year:     2024,
		},
		TrackerData: []api.TrackerMetadata{{Category: "TV", TVDBID: 12345}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.Category != api.CanonicalCategoryMovie {
		t.Fatalf("expected external IDs category MOVIE, got %q", result.Identity.Category)
	}
	if result.Identity.TVDBID != 0 {
		t.Fatalf("expected tvdb cleared for movie category, got %d", result.Identity.TVDBID)
	}
	if result.Release.Category != "MOVIE" {
		t.Fatalf("expected release category MOVIE, got %q", result.Release.Category)
	}
	if repo.fileMetadata.Category != "MOVIE" || repo.ids.SourcePath != "" {
		t.Fatal("provider candidate must not publish category state")
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

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: "/media/file.mkv",
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Title:    "Example",
			Year:     2024,
		},
		TrackerData: []api.TrackerMetadata{{Category: "Music"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.Category != api.CanonicalCategoryMovie {
		t.Fatalf("expected external IDs category MOVIE, got %q", result.Identity.Category)
	}
	if result.Release.Category != "MOVIE" {
		t.Fatalf("expected release category MOVIE, got %q", result.Release.Category)
	}
	if repo.fileMetadata.Category != "MOVIE" || repo.ids.SourcePath != "" {
		t.Fatal("provider candidate must not publish category state")
	}
}

func TestResolveExternalIDsSearchAndMetadata(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 42, Category: "MOVIE"},
		metadata: tmdb.MetadataResult{
			Title:    "Example",
			Year:     2024,
			TMDBType: "Movie",
		},
	}
	imdbClient := &stubIMDB{searchResult: imdb.SearchResult{IMDbID: 24}, info: imdb.Info{IMDbID: "tt0000024", Title: "Example"}}
	tvdbClient := &stubTVDB{id: 12, name: "Example"}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 55,
		IMDBID:     24,
		TVDBID:     12,
	}}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := preparationstate.State{
		SourcePath: "/media/file.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 42 || result.Identity.IMDBID != 24 {
		t.Fatalf("unexpected resolved ids: %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.IMDB == nil {
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
		metadata: tmdb.MetadataResult{
			Title:    "Example",
			Year:     2024,
			TMDBType: "Movie",
		},
	}
	svc := NewService(repo,
		WithConfig(config.Config{Description: config.DescriptionSettingsConfig{AddLogo: true, LogoLanguage: "ja,en"}}),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	_, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
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

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: "/media/file.mkv",
			TMDBID:     42,
			Category:   "MOVIE",
		},
		ProviderMetadata: api.SourceScopedMetadata{
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
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TMDB.Logo == "" {
		t.Fatalf("expected logo to be refreshed, got %#v", result.ProviderMetadata.TMDB)
	}
}

func TestResolveExternalIDsUsesSceneTVmazeToResolveIMDb(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example Show", Year: 2026}}
	imdbClient := &stubIMDB{info: imdb.Info{
		IMDbID: "tt1234567",
		Title:  "Example Show",
		Year:   2026,
	}}
	tvdbClient := &stubTVDB{id: 456789, name: "Example Show"}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 12345,
		IMDBID:     1234567,
		TVDBID:     456789,
	}}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Show.S04E01.2160p.WEB.h265-GRP.mkv",
		SceneTVmazeID:     12345,
		Release:           api.ReleaseInfo{Category: "TV", Title: "Example Show"},
		MediaInfoCategory: "TV",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TVmazeID != 12345 || result.Identity.Provenance.TVmaze != "scene" {
		t.Fatalf("expected scene tvmaze id, got %#v", result.Identity)
	}
	if result.Identity.IMDBID != 1234567 || result.Identity.Provenance.IMDB != api.IdentityProvenanceProvider {
		t.Fatalf("expected imdb from tvmaze, got %#v", result.Identity)
	}
	if tvmazeClient.calls == 0 || len(tvmazeClient.inputs) == 0 || tvmazeClient.inputs[0].ManualID != 12345 {
		t.Fatalf("expected tvmaze manual lookup, got calls=%d inputs=%#v", tvmazeClient.calls, tvmazeClient.inputs)
	}
	if repo.ids.SourcePath != "" {
		t.Fatal("provider candidate must not publish TVmaze identity")
	}
}

func TestResolveExternalIDsUsesSceneNFOIDs(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:    "Example",
		Year:     2024,
		TMDBType: "TV",
		MALID:    999,
	}}
	imdbClient := &stubIMDB{info: imdb.Info{
		IMDbID: "tt0123456",
		Title:  "Example",
		Year:   2024,
	}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
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

	if result.Identity.TMDBID != 42 || result.Identity.Provenance.TMDB != "scene" {
		t.Fatalf("expected scene tmdb id, got %#v", result.Identity)
	}
	if result.Identity.IMDBID != 123456 || result.Identity.Provenance.IMDB != "scene" {
		t.Fatalf("expected scene imdb id, got %#v", result.Identity)
	}
	if result.Identity.TVDBID != 333 || result.Identity.Provenance.TVDB != "scene" {
		t.Fatalf("expected scene tvdb id, got %#v", result.Identity)
	}
	if result.Identity.TVmazeID != 444 || result.Identity.Provenance.TVmaze != "scene" {
		t.Fatalf("expected scene tvmaze id, got %#v", result.Identity)
	}
	if result.Identity.MALID != 555 || result.Identity.Provenance.MAL != "scene" {
		t.Fatalf("expected scene mal id, got %#v", result.Identity)
	}
	if result.MALID != 555 {
		t.Fatalf("expected canonical scene mal mirror, got %d", result.MALID)
	}
	if repo.ids.SourcePath != "" {
		t.Fatal("provider candidate must not publish scene identity")
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

	meta := preparationstate.State{
		SourcePath:      "/media/file.mkv",
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: "/media/file.mkv",
			TMDBID:     42,
			IMDBID:     24,
			TVDBID:     12,
			TVmazeID:   55,
			Category:   api.CanonicalCategoryTV,
			Provenance: api.IdentityProvenanceSet{
				TMDB:   api.IdentityProvenanceLegacy,
				IMDB:   api.IdentityProvenanceLegacy,
				TVDB:   api.IdentityProvenanceLegacy,
				TVmaze: api.IdentityProvenanceLegacy,
			},
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "/media/file.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID:   42,
				Category: "tv",
				Title:    "Example",
			},
			IMDB: &api.IMDBMetadata{IMDBID: 24, Title: "Example"},
			TVDB: &api.TVDBMetadata{
				TVDBID: 12,
				Name:   "Example",
				NameDisambiguation: api.TVDBNameDisambiguation{
					CanonicalName: "Example",
					Status:        api.MetadataEvidenceStatusPartial,
					Source:        "test",
				},
			},
			TVmaze: &api.TVmazeMetadata{TVmazeID: 55, Name: "Example"},
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 42 || result.Identity.IMDBID != 24 || result.Identity.TVDBID != 12 || result.Identity.TVmazeID != 55 {
		t.Fatalf("unexpected resolved ids: %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.IMDB == nil || result.ProviderMetadata.TVDB == nil || result.ProviderMetadata.TVmaze == nil {
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

func TestResolveExternalIDsClearsNonExplicitSiblingBeforeAnchoredResolution(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provenance api.IdentityProvenance
		source     string
	}{
		{name: "fresh stored blank provenance", source: "stored"},
		{
			name:       "fresh stored tracker provenance",
			provenance: api.IdentityProvenanceTracker,
			source:     "stored",
		},
		{name: "tracker evidence", source: "tracker"},
		{name: "MediaInfo evidence", source: "mediainfo"},
		{name: "scene evidence", source: "scene"},
		{name: "Arr evidence", source: "arr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imdbID := 1234567
			state := preparationstate.State{
				SourcePath:        "/media/Example.Movie.2026.1080p-GRP.mkv",
				MediaInfoCategory: "MOVIE",
				ExternalIDOverrides: api.ExternalIDOverrides{
					IMDBID: &imdbID,
				},
			}
			switch tc.source {
			case "stored":
				state.StoredDataFresh = true
				state.Identity = api.ExternalIdentity{
					SourcePath: state.SourcePath,
					TMDBID:     999888,
					Provenance: api.IdentityProvenanceSet{TMDB: tc.provenance},
				}
				state.ProviderMetadata = api.SourceScopedMetadata{
					SourcePath: state.SourcePath,
					TMDB:       &api.TMDBMetadata{TMDBID: 999888, Title: "Stale Metadata"},
				}
			case "tracker":
				state.TrackerData = []api.TrackerMetadata{{TMDBID: 999888}}
			case "mediainfo":
				state.MediaInfoTMDBID = 999888
			case "scene":
				state.SceneTMDBID = 999888
			case "arr":
				state.ArrTMDBID = 999888
			}

			svc := NewService(&fakeRepo{},
				WithTMDBClient(&stubTMDB{}),
				WithIMDBClient(&stubIMDB{info: imdb.Info{
					IMDbID: "tt1234567",
					Title:  "Example Movie",
					Type:   "movie",
				}}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(&stubTVmaze{}),
			)
			result, err := svc.resolveExternalIdentity(context.Background(), state)
			if err != nil {
				t.Fatalf("resolve anchored identity: %v", err)
			}
			if result.Identity.IMDBID != imdbID || result.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit {
				t.Fatalf("explicit IMDb identity = %#v", result.Identity)
			}
			if result.Identity.TMDBID != 0 || result.Identity.Provenance.TMDB != api.IdentityProvenanceUnknown {
				t.Fatalf("non-explicit TMDB sibling retained: %#v", result.Identity)
			}
			if result.ProviderMetadata.TMDB != nil {
				t.Fatalf("dependent cached TMDB metadata retained: %#v", result.ProviderMetadata.TMDB)
			}
		})
	}
}

func TestResolveExternalIDsRefreshesTVDBDisambiguationWithoutRefetchingSeries(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
	tvdbClient := &stubTVDB{
		nameDisambiguation: tvdb.NameDisambiguation{
			CanonicalName:  "Example Series",
			SeriesYear:     2026,
			SameNameSeries: 1,
			IncludeYear:    true,
			Status:         api.MetadataEvidenceStatusPartial,
			Source:         "tvdb_v4_search_unpaged",
		},
	}
	svc := NewService(&fakeRepo{},
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:      sourcePath,
		StoredDataFresh: true,
		Release:         api.ReleaseInfo{Category: "TV", Title: "Example Series"},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			TVDBID:     987650001,
			TVmazeID:   987650002,
			Category:   api.CanonicalCategoryTV,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			TVDB: &api.TVDBMetadata{
				TVDBID:          987650001,
				Name:            "Example Native Series",
				NameEnglish:     "Example Series",
				Year:            2026,
				OriginalCountry: "jpn",
				NameDisambiguation: api.TVDBNameDisambiguation{
					CanonicalName: "Example Series",
					SeriesYear:    2026,
					Status:        api.MetadataEvidenceStatusUnavailable,
					Source:        "tvdb_v4_search_unpaged",
				},
			},
			TVmaze: &api.TVmazeMetadata{TVmazeID: 987650002, Name: "Example Series"},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tvdbClient.seriesMetadataCalls != 0 || len(tvdbClient.seriesLangCalls) != 0 {
		t.Fatalf(
			"selected series metadata was refetched: metadata=%d language_calls=%d",
			tvdbClient.seriesMetadataCalls,
			len(tvdbClient.seriesLangCalls),
		)
	}
	if len(tvdbClient.nameDisambiguationInputs) != 1 {
		t.Fatalf("name disambiguation calls = %d, want 1", len(tvdbClient.nameDisambiguationInputs))
	}
	input := tvdbClient.nameDisambiguationInputs[0]
	if input.TVDBID != 987650001 || input.NameEnglish != "Example Series" ||
		input.SeriesYear != 2026 || input.OriginalCountry != "jpn" || input.ExplicitNamingYear {
		t.Fatalf("name disambiguation input = %+v", input)
	}
	if result.ProviderMetadata.TVDB == nil ||
		result.ProviderMetadata.TVDB.NameDisambiguation.Status != api.MetadataEvidenceStatusPartial ||
		!result.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear {
		t.Fatalf("refreshed TVDB disambiguation = %+v", result.ProviderMetadata.TVDB)
	}
}

func TestResolveExternalIDsPrefersTMDBFromIMDbBeforeSearch(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 765432, Category: "TV"},
		findResult:    tmdb.FindResult{TMDBID: 456789, Category: "TV"},
		metadata:      tmdb.MetadataResult{Title: "Example Quiz", TMDBType: "Scripted"},
	}
	imdbClient := &stubIMDB{info: imdb.Info{
		IMDbID: "tt1234567",
		Title:  "Example Quiz",
		Type:   "tvSeries",
		Year:   2026,
	}}
	tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
		TVDBID:     456789,
		Name:       "Example Quiz",
		FirstAired: "2026-09-10",
	}}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := preparationstate.State{
		SourcePath:        `D:\temp\Example.Quiz.2026.11.10.1080p.WEB-DL.AAC2.0.H.264-GRP.mkv`,
		MediaInfoCategory: "TV",
		Release: api.ReleaseInfo{
			Title: "Example Quiz",
			Year:  2026,
			Type:  "episode",
		},
		TrackerData: []api.TrackerMetadata{{
			IMDBID:   1234567,
			TVDBID:   456789,
			Category: "TV",
		}},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 456789 {
		t.Fatalf("expected tmdb id from imdb external lookup, got %d", result.Identity.TMDBID)
	}
	if result.Identity.Provenance.TMDB != api.IdentityProvenanceProvider {
		t.Fatalf("expected tmdb source tmdb_external, got %q", result.Identity.Provenance.TMDB)
	}
	if tmdbClient.findCalls != 1 {
		t.Fatalf("expected one tmdb external lookup call, got %d", tmdbClient.findCalls)
	}
	if tmdbClient.searchCalls != 0 {
		t.Fatalf("expected no tmdb filename search when imdb lookup resolves tmdb, got %d", tmdbClient.searchCalls)
	}
	if len(tmdbClient.findInputs) != 1 || tmdbClient.findInputs[0].CategoryPreference != "TV" || tmdbClient.findInputs[0].RequireExternalIDAgreement {
		t.Fatalf("expected tmdb external lookup category preference TV, got %#v", tmdbClient.findInputs)
	}
}

func TestResolveExternalIDsKeepsAnchoredFilenameMatchAsCandidate(t *testing.T) {
	imdbID := 1234567
	tmdbClient := &stubTMDB{
		findResult: tmdb.FindResult{
			TMDBID:         253,
			Category:       "TV",
			FilenameSearch: true,
			Candidates: []tmdb.Candidate{{
				TMDBID: 253,
				Title:  "Example Candidate",
				Year:   2026,
			}},
		},
		searchOutcome: tmdb.SearchOutcome{TMDBID: 253, Category: "TV"},
	}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Anchor",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Anchor.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			IMDBID: &imdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve anchored identity: %v", err)
	}
	if result.Identity.IMDBID != imdbID || result.Identity.TMDBID != 0 || result.ProviderMetadata.TMDB != nil {
		t.Fatalf("filename match became canonical: identity=%#v metadata=%#v", result.Identity, result.ProviderMetadata.TMDB)
	}
	if tmdbClient.searchCalls != 0 || tmdbClient.metaCalls != 0 {
		t.Fatalf("anchored filename match triggered automatic TMDB selection: search=%d metadata=%d", tmdbClient.searchCalls, tmdbClient.metaCalls)
	}
	if len(result.ExternalIdentityCandidates) != 1 || result.ExternalIdentityCandidates[0].ID != 253 {
		t.Fatalf("filename candidates = %#v", result.ExternalIdentityCandidates)
	}
}

func TestResolveExternalIDsRejectsTMDBMetadataConflictingWithExplicitIMDb(t *testing.T) {
	for _, tc := range []struct {
		name         string
		explicitTMDB bool
		trackerTMDB  bool
		wantTMDB     int
	}{
		{name: "clear inferred TMDB", wantTMDB: 0},
		{
			name:        "clear conflicting tracker TMDB",
			trackerTMDB: true,
			wantTMDB:    0,
		},
		{
			name:         "preserve explicit TMDB",
			explicitTMDB: true,
			wantTMDB:     456789,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imdbID := 1234567
			tmdbID := 456789
			overrides := api.ExternalIDOverrides{IMDBID: &imdbID}
			if tc.explicitTMDB {
				overrides.TMDBID = &tmdbID
			}
			tmdbClient := &stubTMDB{
				findResult: tmdb.FindResult{TMDBID: tmdbID, Category: "MOVIE"},
				metadata: tmdb.MetadataResult{
					Title:          "Conflicting Metadata",
					TMDBType:       "Movie",
					IMDbID:         imdbID,
					ExternalIMDbID: 7654321,
				},
			}
			svc := NewService(&fakeRepo{},
				WithTMDBClient(tmdbClient),
				WithIMDBClient(&stubIMDB{info: imdb.Info{IMDbID: "tt1234567", Title: "Example Anchor"}}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(&stubTVmaze{}),
			)

			state := preparationstate.State{
				SourcePath:          "Example.Anchor.2026.1080p-GRP.mkv",
				MediaInfoCategory:   "MOVIE",
				ExternalIDOverrides: overrides,
			}
			if tc.trackerTMDB {
				state.TrackerData = []api.TrackerMetadata{{TMDBID: tmdbID}}
			}
			result, err := svc.resolveExternalIdentity(context.Background(), state)
			if err != nil {
				t.Fatalf("resolve conflicting identity: %v", err)
			}
			if result.Identity.IMDBID != imdbID || result.Identity.TMDBID != tc.wantTMDB {
				t.Fatalf("explicit identity changed: %#v", result.Identity)
			}
			if result.ProviderMetadata.TMDB != nil {
				t.Fatalf("conflicting TMDB metadata retained: %#v", result.ProviderMetadata.TMDB)
			}
			if len(result.LookupWarnings) != 1 || !strings.Contains(result.LookupWarnings[0], "Correct one of the supplied provider IDs") {
				t.Fatalf("correction warnings = %#v", result.LookupWarnings)
			}
		})
	}
}

func TestResolveExternalIDsGatesRejectedTMDBFromDownstreamMetadata(t *testing.T) {
	for _, tc := range []struct {
		name               string
		tmdbIMDbID         int
		tvPack             bool
		dailyDate          string
		season             int
		wantDailyCalls     int
		wantEpisodeCalls   int
		wantSeasonCalls    int
		wantLocalizedCalls int
		wantSeason         int
		wantEpisode        int
		wantEpisodeTitle   string
		wantTMDBMetadata   bool
	}{
		{
			name:       "conflicting explicit IDs daily episode",
			tmdbIMDbID: 7654321,
			dailyDate:  "2026-09-08",
		},
		{
			name:               "matching explicit IDs daily episode",
			tmdbIMDbID:         1234567,
			dailyDate:          "2026-09-08",
			wantDailyCalls:     1,
			wantEpisodeCalls:   1,
			wantLocalizedCalls: 3,
			wantSeason:         2,
			wantEpisode:        7,
			wantEpisodeTitle:   "Matched Episode",
			wantTMDBMetadata:   true,
		},
		{
			name:       "conflicting explicit IDs season pack",
			tmdbIMDbID: 7654321,
			tvPack:     true,
			season:     3,
			wantSeason: 3,
		},
		{
			name:               "matching explicit IDs season pack",
			tmdbIMDbID:         1234567,
			tvPack:             true,
			season:             3,
			wantSeasonCalls:    1,
			wantLocalizedCalls: 2,
			wantSeason:         3,
			wantEpisodeTitle:   "Matched Season",
			wantTMDBMetadata:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imdbID := 1234567
			tmdbID := 456789
			tmdbClient := &stubTMDB{
				metadata: tmdb.MetadataResult{
					Title:          "Example Series",
					TMDBType:       "Scripted",
					IMDbID:         imdbID,
					ExternalIMDbID: tc.tmdbIMDbID,
				},
				dailySeason:  2,
				dailyEpisode: 7,
				episodeDetails: tmdb.EpisodeDetails{
					Name:          "Matched Episode",
					SeasonNumber:  2,
					EpisodeNumber: 7,
				},
				seasonDetails: tmdb.SeasonDetails{Name: "Matched Season", SeasonNumber: 3},
				localizedByType: map[string]map[string]any{
					"main":    {"name": "Série Localizada", "overview": "Sinopse localizada"},
					"season":  {"name": "Temporada Localizada", "overview": "Sinopse da temporada"},
					"episode": {"name": "Episódio Localizado", "overview": "Sinopse do episódio"},
				},
			}
			svc := NewService(&fakeRepo{},
				WithTrackerRegistry(localizedMetadataTestRegistry(t)),
				WithTMDBClient(tmdbClient),
				WithIMDBClient(&stubIMDB{info: imdb.Info{
					IMDbID: "tt1234567",
					Title:  "Example Series",
					Type:   "tvSeries",
				}}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(&stubTVmaze{}),
			)

			result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
				SourcePath:          "/media/Example.Series.2026-09-08.1080p-GRP.mkv",
				MediaInfoCategory:   "TV",
				DailyEpisodeDate:    tc.dailyDate,
				SeasonInt:           tc.season,
				TVPack:              tc.tvPack,
				EvidenceTrackers:    []string{"BJS"},
				ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: &tmdbID, IMDBID: &imdbID},
			})
			if err != nil {
				t.Fatalf("resolve downstream TMDB metadata: %v", err)
			}
			if result.Identity.TMDBID != tmdbID || result.Identity.IMDBID != imdbID {
				t.Fatalf("explicit identity changed: %#v", result.Identity)
			}
			if tmdbClient.dailyCalls != tc.wantDailyCalls || tmdbClient.episodeCalls != tc.wantEpisodeCalls ||
				len(tmdbClient.localizedInputs) != tc.wantLocalizedCalls {
				t.Fatalf(
					"downstream TMDB calls = daily:%d episode:%d localized:%d",
					tmdbClient.dailyCalls,
					tmdbClient.episodeCalls,
					len(tmdbClient.localizedInputs),
				)
			}
			if tmdbClient.seasonCalls != tc.wantSeasonCalls {
				t.Fatalf("TMDB season calls = %d, want %d", tmdbClient.seasonCalls, tc.wantSeasonCalls)
			}
			if result.SeasonInt != tc.wantSeason || result.EpisodeInt != tc.wantEpisode || result.EpisodeTitle != tc.wantEpisodeTitle {
				t.Fatalf("episode metadata = season:%d episode:%d title:%q", result.SeasonInt, result.EpisodeInt, result.EpisodeTitle)
			}
			if (result.ProviderMetadata.TMDB != nil) != tc.wantTMDBMetadata {
				t.Fatalf("TMDB metadata = %#v", result.ProviderMetadata.TMDB)
			}
		})
	}
}

func TestResolveExternalIDsGatesRejectedTVDBFromEpisodeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name             string
		metadataTVDBID   int
		wantEpisodeCalls int
		wantEpisodeTitle string
		wantMetadata     bool
	}{
		{name: "conflicting explicit TVDB", metadataTVDBID: 999},
		{
			name:             "matching explicit TVDB",
			metadataTVDBID:   200,
			wantEpisodeCalls: 1,
			wantEpisodeTitle: "Matched TVDB Episode",
			wantMetadata:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tvdbID := 200
			tvdbClient := &stubTVDB{
				seriesMetadata: tvdb.SeriesMetadata{TVDBID: tc.metadataTVDBID, Name: "Example Series"},
				episodes: tvdb.EpisodesData{Episodes: []tvdb.Episode{{
					ID:           201,
					SeasonNumber: 1,
					Number:       1,
					Name:         "Matched TVDB Episode",
				}}},
			}
			svc := NewService(&fakeRepo{},
				WithTMDBClient(&stubTMDB{}),
				WithIMDBClient(&stubIMDB{}),
				WithTVDBClient(tvdbClient),
				WithTVmazeClient(&stubTVmaze{}),
			)

			result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
				SourcePath:        "/media/Example.Series.S01E01.1080p-GRP.mkv",
				MediaInfoCategory: "TV",
				SeasonInt:         1,
				EpisodeInt:        1,
				ExternalIDOverrides: api.ExternalIDOverrides{
					TVDBID: &tvdbID,
				},
			})
			if err != nil {
				t.Fatalf("resolve TVDB identity: %v", err)
			}
			if result.Identity.TVDBID != tvdbID || result.Identity.Provenance.TVDB != api.IdentityProvenanceExplicit {
				t.Fatalf("explicit TVDB identity changed: %#v", result.Identity)
			}
			if tvdbClient.episodeCalls != tc.wantEpisodeCalls || result.EpisodeTitle != tc.wantEpisodeTitle {
				t.Fatalf("TVDB downstream metadata = calls:%d title:%q", tvdbClient.episodeCalls, result.EpisodeTitle)
			}
			if (result.ProviderMetadata.TVDB != nil) != tc.wantMetadata {
				t.Fatalf("TVDB metadata = %#v", result.ProviderMetadata.TVDB)
			}
		})
	}
}

func TestResolveExternalIDsUsesStaleStoredExplicitPinBeforeProviderFacts(t *testing.T) {
	const sourcePath = "/media/Example.Series.2026-09-08.1080p-GRP.mkv"
	const storedIMDBID = 1234567
	tmdbID := 456789
	repo := &fakeRepo{ids: api.ExternalIdentity{
		SourcePath: sourcePath,
		IMDBID:     storedIMDBID,
		Provenance: api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceExplicit},
	}}
	tmdbClient := &stubTMDB{
		metadata: tmdb.MetadataResult{
			Title:          "Conflicting Series",
			TMDBType:       "Scripted",
			IMDbID:         7654321,
			ExternalIMDbID: 7654321,
		},
		dailySeason:  2,
		dailyEpisode: 7,
		episodeDetails: tmdb.EpisodeDetails{
			Name:          "Conflicting Episode",
			SeasonNumber:  2,
			EpisodeNumber: 7,
		},
		localizedData: map[string]any{"name": "Conflicting Localized Series"},
	}
	svc := NewService(repo,
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Stored Pin Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        sourcePath,
		StoredDataFresh:   false,
		MediaInfoCategory: "TV",
		DailyEpisodeDate:  "2026-09-08",
		EvidenceTrackers:  []string{"BJS"},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve stale stored pin: %v", err)
	}
	if result.Identity.IMDBID != storedIMDBID || result.Identity.Overrides.IMDB != api.OverrideStateValue || result.Identity.TMDBID != tmdbID {
		t.Fatalf("stored pin identity = %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB != nil {
		t.Fatalf("conflicting TMDB metadata reached candidate facts: %#v", result.ProviderMetadata.TMDB)
	}
	if tmdbClient.dailyCalls != 0 || tmdbClient.episodeCalls != 0 || tmdbClient.seasonCalls != 0 || len(tmdbClient.localizedInputs) != 0 {
		t.Fatalf("conflicting TMDB reached downstream calls: daily=%d episode=%d season=%d localized=%d", tmdbClient.dailyCalls, tmdbClient.episodeCalls, tmdbClient.seasonCalls, len(tmdbClient.localizedInputs))
	}
	if result.SeasonInt != 0 || result.EpisodeInt != 0 || result.EpisodeTitle != "" {
		t.Fatalf("conflicting TMDB changed episode facts: season=%d episode=%d title=%q", result.SeasonInt, result.EpisodeInt, result.EpisodeTitle)
	}
}

func TestResolveExternalIDsResetStoredTMDBPinAllowsAutomaticLookup(t *testing.T) {
	const sourcePath = "/media/Example.Movie.2026.1080p-GRP.mkv"
	const storedTMDBID = 111
	const storedIMDBID = 222
	const resolvedTMDBID = 333

	for _, test := range []struct {
		name            string
		storedDataFresh bool
		storedOverride  api.OverrideState
		storedTMDBID    int
	}{
		{
			name:            "fresh stored value",
			storedDataFresh: true,
			storedOverride:  api.OverrideStateValue,
			storedTMDBID:    storedTMDBID,
		},
		{
			name:            "stale stored value",
			storedDataFresh: false,
			storedOverride:  api.OverrideStateValue,
			storedTMDBID:    storedTMDBID,
		},
		{
			name:            "fresh stored clear",
			storedDataFresh: true,
			storedOverride:  api.OverrideStateClear,
		},
		{
			name:            "stale stored clear",
			storedDataFresh: false,
			storedOverride:  api.OverrideStateClear,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stored := api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryMovie,
				TMDBID:     test.storedTMDBID,
				IMDBID:     storedIMDBID,
				Provenance: api.IdentityProvenanceSet{
					Category: api.IdentityProvenanceExplicit,
					TMDB:     api.IdentityProvenanceExplicit,
					IMDB:     api.IdentityProvenanceExplicit,
				},
				Overrides: api.IdentityOverrideState{
					Category: api.OverrideStateValue,
					TMDB:     test.storedOverride,
					IMDB:     api.OverrideStateValue,
				},
			}
			tmdbClient := &stubTMDB{
				findResult: tmdb.FindResult{TMDBID: resolvedTMDBID, Category: "MOVIE"},
				metadata: tmdb.MetadataResult{
					TMDBType:       "Movie",
					Title:          "Current Movie",
					IMDbID:         storedIMDBID,
					ExternalIMDbID: storedIMDBID,
				},
			}
			repo := &fakeRepo{
				ids: stored,
				meta: api.SourceScopedMetadata{
					SourcePath: sourcePath,
					TMDB: &api.TMDBMetadata{
						TMDBID:   storedTMDBID,
						Category: "movie",
						Title:    "Stored Movie",
					},
				},
			}
			svc := NewService(repo,
				WithTMDBClient(tmdbClient),
				WithIMDBClient(&stubIMDB{info: imdb.Info{
					IMDbID: formatIMDbID(storedIMDBID),
					Title:  "Current Movie",
					Type:   "movie",
				}}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(&stubTVmaze{}),
			)
			state := preparationstate.State{
				SourcePath:          sourcePath,
				StoredDataFresh:     test.storedDataFresh,
				Release:             api.ReleaseInfo{Title: "Example Movie", Category: "MOVIE"},
				IdentityResetFields: []api.CorrectionField{api.CorrectionFieldIdentityTMDB},
			}
			if test.storedDataFresh {
				state.Identity = stored
				state.ProviderMetadata = repo.meta
			}
			result, err := svc.resolveExternalIdentity(t.Context(), state)
			if err != nil {
				t.Fatalf("resolve reset identity: %v", err)
			}
			if result.Identity.TMDBID != resolvedTMDBID || result.Identity.Provenance.TMDB == api.IdentityProvenanceExplicit ||
				result.Identity.Overrides.TMDB == api.OverrideStateValue || result.Identity.Overrides.TMDB == api.OverrideStateClear {
				t.Fatalf("reset TMDB identity = %#v", result.Identity)
			}
			if result.Identity.IMDBID != storedIMDBID || result.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit ||
				result.Identity.Overrides.IMDB != api.OverrideStateValue || result.Identity.Category != api.CanonicalCategoryMovie {
				t.Fatalf("unreset stored identity changed: %#v", result.Identity)
			}
			if tmdbClient.findCalls != 1 || tmdbClient.metaCalls != 1 || result.ProviderMetadata.TMDB == nil ||
				result.ProviderMetadata.TMDB.TMDBID != resolvedTMDBID {
				t.Fatalf("reset TMDB lookup = calls:%d/%d metadata:%#v", tmdbClient.findCalls, tmdbClient.metaCalls, result.ProviderMetadata.TMDB)
			}
		})
	}
}

func TestResolveExternalIDsCategoryResetUsesParsedMovieCategory(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP.mkv")

	for _, test := range []struct {
		name                    string
		resetCategory           bool
		wantCandidateProvenance api.IdentityProvenance
		wantCanonicalProvenance api.IdentityProvenance
	}{
		{
			name:                    "reset stored category",
			resetCategory:           true,
			wantCandidateProvenance: api.IdentityProvenanceUnknown,
			wantCanonicalProvenance: api.IdentityProvenanceProvider,
		},
		{
			name:                    "preserves unmarked category",
			wantCandidateProvenance: api.IdentityProvenanceExplicit,
			wantCanonicalProvenance: api.IdentityProvenanceExplicit,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stored := api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryTV,
				Provenance: api.IdentityProvenanceSet{Category: api.IdentityProvenanceExplicit},
				Overrides:  api.IdentityOverrideState{Category: api.OverrideStateValue},
			}
			tmdbClient := &stubTMDB{
				searchOutcome: tmdb.SearchOutcome{TMDBID: 333, Category: "MOVIE"},
				metadata:      tmdb.MetadataResult{TMDBType: "Movie", Title: "Example Movie"},
			}
			repo := &fakeRepo{ids: stored}
			service := NewService(repo,
				WithTMDBClient(tmdbClient),
				WithIMDBClient(&stubIMDB{}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(&stubTVmaze{}),
			)
			state := preparationstate.State{
				SourcePath:      sourcePath,
				StoredDataFresh: true,
				Identity:        stored,
				Release:         api.ReleaseInfo{Title: "Example Movie", Category: "MOVIE"},
			}
			if test.resetCategory {
				state.IdentityResetFields = []api.CorrectionField{api.CorrectionFieldReleaseNameCategory}
			}

			candidate, err := service.resolveExternalIdentity(t.Context(), state)
			if err != nil {
				t.Fatalf("resolve provider candidate: %v", err)
			}
			if len(tmdbClient.searchInputs) != 1 || tmdbClient.searchInputs[0].Category != "MOVIE" {
				t.Fatalf("TMDB search inputs = %#v", tmdbClient.searchInputs)
			}
			if candidate.Identity.Category != api.CanonicalCategoryMovie ||
				candidate.Identity.Provenance.Category != test.wantCandidateProvenance {
				t.Fatalf("provider candidate category = %#v", candidate.Identity)
			}

			resolver, err := externalidentity.NewWithCandidateSource(repo, staticCandidateSource{state: candidate})
			if err != nil {
				t.Fatalf("new identity resolver: %v", err)
			}
			resolved, err := resolver.Resolve(t.Context(), externalidentity.Request{
				SourcePath:        sourcePath,
				SourceFingerprint: "category-reset-source",
				Generation:        1,
				Intent: externalidentity.ResolutionIntent{
					IdentityResetFields: state.IdentityResetFields,
				},
			})
			if err != nil {
				t.Fatalf("resolve canonical identity: %v", err)
			}
			if resolved.Identity.Category != api.CanonicalCategoryMovie ||
				resolved.Identity.Provenance.Category != test.wantCanonicalProvenance {
				t.Fatalf("canonical category = %#v", resolved.Identity)
			}
		})
	}
}

func TestResolveExternalIDsGatesRejectedTVmazeFromEpisodeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name             string
		metadataIMDbID   int
		wantEpisodeCalls int
		wantEpisodeTitle string
		wantMetadata     bool
	}{
		{name: "conflicting explicit TVmaze", metadataIMDbID: 7654321},
		{
			name:             "matching explicit TVmaze",
			metadataIMDbID:   1234567,
			wantEpisodeCalls: 1,
			wantEpisodeTitle: "Matched TVmaze Episode",
			wantMetadata:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imdbID := 1234567
			tvmazeID := 55
			tvmazeClient := &stubTVmaze{
				result: tvmaze.SearchResult{
					SelectedID: tvmazeID,
					Candidates: []tvmaze.Candidate{{
						ID:        tvmazeID,
						Name:      "Example Series",
						Externals: tvmaze.Externals{IMDB: formatIMDbID(tc.metadataIMDbID)},
					}},
				},
				episodeData: &tvmaze.EpisodeData{
					EpisodeName:   "Matched TVmaze Episode",
					SeasonNumber:  1,
					EpisodeNumber: 1,
				},
			}
			svc := NewService(&fakeRepo{},
				WithTMDBClient(&stubTMDB{}),
				WithIMDBClient(&stubIMDB{info: imdb.Info{
					IMDbID: "tt1234567",
					Title:  "Example Series",
					Type:   "tvSeries",
				}}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(tvmazeClient),
			)

			result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
				SourcePath:        "/media/Example.Series.S01E01.1080p-GRP.mkv",
				MediaInfoCategory: "TV",
				SeasonInt:         1,
				EpisodeInt:        1,
				ExternalIDOverrides: api.ExternalIDOverrides{
					IMDBID:   &imdbID,
					TVmazeID: &tvmazeID,
				},
			})
			if err != nil {
				t.Fatalf("resolve TVmaze identity: %v", err)
			}
			if result.Identity.TVmazeID != tvmazeID || result.Identity.Provenance.TVmaze != api.IdentityProvenanceExplicit {
				t.Fatalf("explicit TVmaze identity changed: %#v", result.Identity)
			}
			if tvmazeClient.episodeNumberCalls != tc.wantEpisodeCalls || result.EpisodeTitle != tc.wantEpisodeTitle {
				t.Fatalf("TVmaze downstream metadata = calls:%d title:%q", tvmazeClient.episodeNumberCalls, result.EpisodeTitle)
			}
			if (result.ProviderMetadata.TVmaze != nil) != tc.wantMetadata {
				t.Fatalf("TVmaze metadata = %#v", result.ProviderMetadata.TVmaze)
			}
		})
	}
}

func TestResolveExternalIDsAllowsDownstreamTMDBAfterConflictingInferredIDIsReplaced(t *testing.T) {
	imdbID := 1234567
	tmdbClient := &stubTMDB{
		findResult: tmdb.FindResult{TMDBID: 222, Category: "TV"},
		metadataFn: func(input tmdb.MetadataInput) (tmdb.MetadataResult, error) {
			if input.TMDBID != 222 {
				t.Errorf("metadata lookup TMDB ID = %d, want replacement 222", input.TMDBID)
			}
			return tmdb.MetadataResult{}, errors.New("replacement metadata unavailable")
		},
		dailySeason:  2,
		dailyEpisode: 7,
		localizedData: map[string]any{
			"name":     "Série Localizada",
			"overview": "Sinopse localizada",
		},
	}
	svc := NewService(&fakeRepo{},
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Series.2026-09-08.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		DailyEpisodeDate:  "2026-09-08",
		EvidenceTrackers:  []string{"BJS"},
		TrackerData:       []api.TrackerMetadata{{TMDBID: 111}},
		ExternalIDOverrides: api.ExternalIDOverrides{
			IMDBID: &imdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve replacement TMDB identity: %v", err)
	}
	if result.Identity.TMDBID != 222 || result.Identity.Provenance.TMDB != api.IdentityProvenanceProvider {
		t.Fatalf("replacement TMDB identity = %#v", result.Identity)
	}
	if tmdbClient.dailyCalls != 1 || tmdbClient.episodeCalls != 1 || len(tmdbClient.localizedInputs) != 3 {
		t.Fatalf(
			"replacement downstream calls = daily:%d episode:%d localized:%d",
			tmdbClient.dailyCalls,
			tmdbClient.episodeCalls,
			len(tmdbClient.localizedInputs),
		)
	}
}

func TestResolveExternalIDsReconcilesExplicitTMDBAndTVmazeLinks(t *testing.T) {
	for _, tc := range []struct {
		name           string
		tvmazeIMDBID   int
		tvmazeTVDBID   int
		wantSiblingIDs bool
		wantMetadata   bool
		wantCorrection bool
	}{
		{
			name:           "conflicting links",
			tvmazeIMDBID:   7654321,
			tvmazeTVDBID:   999888,
			wantCorrection: true,
		},
		{
			name:           "matching links",
			tvmazeIMDBID:   1234567,
			tvmazeTVDBID:   222333,
			wantSiblingIDs: true,
			wantMetadata:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmdbID := 444555
			tvmazeID := 55
			tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
				Title:          "Example Series",
				TMDBType:       "Scripted",
				IMDbID:         1234567,
				TVDBID:         222333,
				ExternalIMDbID: 1234567,
				ExternalTVDBID: 222333,
			}}
			tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
				SelectedID: tvmazeID,
				IMDBID:     tc.tvmazeIMDBID,
				TVDBID:     tc.tvmazeTVDBID,
				Candidates: []tvmaze.Candidate{{
					ID:        tvmazeID,
					Name:      "Example Series",
					Externals: tvmaze.Externals{IMDB: formatIMDbID(tc.tvmazeIMDBID), TVDB: tc.tvmazeTVDBID},
				}},
			}}
			svc := NewService(&fakeRepo{},
				WithTMDBClient(tmdbClient),
				WithIMDBClient(&stubIMDB{info: imdb.Info{
					IMDbID: "tt1234567",
					Title:  "Example Series",
					Type:   "tvSeries",
				}}),
				WithTVDBClient(&stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 222333, Name: "Example Series"}}),
				WithTVmazeClient(tvmazeClient),
			)

			result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
				SourcePath:        "Example.Series.S01E01.1080p-GRP.mkv",
				MediaInfoCategory: "TV",
				ExternalIDOverrides: api.ExternalIDOverrides{
					TMDBID:   &tmdbID,
					TVmazeID: &tvmazeID,
				},
			})
			if err != nil {
				t.Fatalf("resolve explicit anchors: %v", err)
			}
			if result.Identity.TMDBID != tmdbID || result.Identity.TVmazeID != tvmazeID {
				t.Fatalf("explicit anchors changed: %#v", result.Identity)
			}
			if got := result.Identity.IMDBID != 0 && result.Identity.TVDBID != 0; got != tc.wantSiblingIDs {
				t.Fatalf("sibling identity = %#v", result.Identity)
			}
			if got := result.ProviderMetadata.TMDB != nil && result.ProviderMetadata.TVmaze != nil; got != tc.wantMetadata {
				t.Fatalf("provider metadata = %#v", result.ProviderMetadata)
			}
			if got := len(result.LookupWarnings) > 0; got != tc.wantCorrection {
				t.Fatalf("lookup warnings = %#v", result.LookupWarnings)
			}
		})
	}
}

func TestResolveExternalIDsReverifiesFreshStoredExplicitTMDBAnchor(t *testing.T) {
	const sourcePath = "/media/Example.Movie.2026.1080p-GRP.mkv"
	tmdbID := 444555
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:          "Example Movie",
		TMDBType:       "Movie",
		IMDbID:         1234567,
		ExternalIMDbID: 1234567,
	}}
	repo := &fakeRepo{}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Movie",
			Type:   "movie",
		}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	first, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        sourcePath,
		MediaInfoCategory: "MOVIE",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve first preparation: %v", err)
	}
	if first.Identity.IMDBID != 1234567 || first.Identity.Provenance.TMDB != api.IdentityProvenanceExplicit ||
		first.Identity.Overrides.TMDB != api.OverrideStateValue || first.ProviderMetadata.TMDB == nil {
		t.Fatalf("first preparation did not resolve TMDB siblings: identity=%#v metadata=%#v", first.Identity, first.ProviderMetadata.TMDB)
	}

	repo.ids = first.Identity
	repo.meta = first.ProviderMetadata
	fresh := preparationstate.State{
		SourcePath:       sourcePath,
		StoredDataFresh:  true,
		Identity:         repo.ids,
		ProviderMetadata: repo.meta,
	}
	second, err := svc.resolveExternalIdentity(context.Background(), fresh)
	if err != nil {
		t.Fatalf("resolve fresh preparation: %v", err)
	}
	if tmdbClient.metaCalls != 2 {
		t.Fatalf("TMDB metadata calls = %d, want one per preparation", tmdbClient.metaCalls)
	}
	if second.Identity.TMDBID != tmdbID || second.Identity.IMDBID != 1234567 {
		t.Fatalf("fresh preparation identity = %#v", second.Identity)
	}
	if second.ProviderMetadata.TMDB == nil || second.ProviderMetadata.TMDB.TMDBID != tmdbID {
		t.Fatalf("fresh TMDB metadata = %#v", second.ProviderMetadata.TMDB)
	}
}

func TestResolveExternalIDsReverifiesFreshStoredExplicitTVmazeAnchor(t *testing.T) {
	const sourcePath = "/media/Example.Series.S01E01.1080p-GRP.mkv"
	tvmazeID := 55
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: tvmazeID,
		IMDBID:     1234567,
		Candidates: []tvmaze.Candidate{{
			ID:        tvmazeID,
			Name:      "Example Series",
			Externals: tvmaze.Externals{IMDB: "tt1234567"},
		}},
	}}
	repo := &fakeRepo{}
	svc := NewService(repo,
		WithTMDBClient(&stubTMDB{}),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(tvmazeClient),
	)

	first, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        sourcePath,
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVmazeID: &tvmazeID,
		},
	})
	if err != nil {
		t.Fatalf("resolve first preparation: %v", err)
	}
	if first.Identity.IMDBID != 1234567 || first.Identity.Provenance.TVmaze != api.IdentityProvenanceExplicit ||
		first.Identity.Overrides.TVmaze != api.OverrideStateValue || first.ProviderMetadata.TVmaze == nil {
		t.Fatalf("first preparation did not resolve TVmaze siblings: identity=%#v metadata=%#v", first.Identity, first.ProviderMetadata.TVmaze)
	}

	repo.ids = first.Identity
	repo.meta = first.ProviderMetadata
	fresh := preparationstate.State{
		SourcePath:       sourcePath,
		StoredDataFresh:  true,
		Identity:         repo.ids,
		ProviderMetadata: repo.meta,
	}
	second, err := svc.resolveExternalIdentity(context.Background(), fresh)
	if err != nil {
		t.Fatalf("resolve fresh preparation: %v", err)
	}
	if tvmazeClient.calls != 2 {
		t.Fatalf("TVmaze metadata calls = %d, want one per preparation", tvmazeClient.calls)
	}
	if second.Identity.TVmazeID != tvmazeID || second.Identity.IMDBID != 1234567 {
		t.Fatalf("fresh preparation identity = %#v", second.Identity)
	}
	if second.ProviderMetadata.TVmaze == nil || second.ProviderMetadata.TVmaze.TVmazeID != tvmazeID {
		t.Fatalf("fresh TVmaze metadata = %#v", second.ProviderMetadata.TVmaze)
	}
}

func TestResolveExternalIDsReconcilesFreshStoredExplicitAnchorLinks(t *testing.T) {
	const sourcePath = "/media/Example.Series.S01E01.1080p-GRP.mkv"
	tmdbID := 444555
	tvmazeID := 55
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:          "Example Series",
		TMDBType:       "Scripted",
		IMDbID:         1234567,
		TVDBID:         222333,
		ExternalIMDbID: 1234567,
		ExternalTVDBID: 222333,
	}}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: tvmazeID,
		IMDBID:     1234567,
		TVDBID:     222333,
		Candidates: []tvmaze.Candidate{{
			ID:        tvmazeID,
			Name:      "Example Series",
			Externals: tvmaze.Externals{IMDB: "tt1234567", TVDB: 222333},
		}},
	}}
	repo := &fakeRepo{}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 222333, Name: "Example Series"}}),
		WithTVmazeClient(tvmazeClient),
	)

	first, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        sourcePath,
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID:   &tmdbID,
			TVmazeID: &tvmazeID,
		},
	})
	if err != nil {
		t.Fatalf("resolve first preparation: %v", err)
	}
	if first.ProviderMetadata.TMDB == nil || first.ProviderMetadata.TVmaze == nil {
		t.Fatalf("first preparation metadata = %#v", first.ProviderMetadata)
	}

	tvmazeClient.result.IMDBID = 7654321
	tvmazeClient.result.TVDBID = 999888
	tvmazeClient.result.Candidates[0].Externals = tvmaze.Externals{IMDB: "tt7654321", TVDB: 999888}
	repo.ids = first.Identity
	repo.meta = first.ProviderMetadata
	fresh := preparationstate.State{
		SourcePath:       sourcePath,
		StoredDataFresh:  true,
		Identity:         repo.ids,
		ProviderMetadata: repo.meta,
	}
	second, err := svc.resolveExternalIdentity(context.Background(), fresh)
	if err != nil {
		t.Fatalf("resolve fresh preparation: %v", err)
	}
	if tmdbClient.metaCalls != 2 || tvmazeClient.calls != 2 {
		t.Fatalf("fresh anchor verification calls = TMDB:%d TVmaze:%d", tmdbClient.metaCalls, tvmazeClient.calls)
	}
	if second.Identity.TMDBID != tmdbID || second.Identity.TVmazeID != tvmazeID || second.Identity.IMDBID != 0 || second.Identity.TVDBID != 0 {
		t.Fatalf("reconciled fresh identity = %#v", second.Identity)
	}
	if second.ProviderMetadata.TMDB != nil || second.ProviderMetadata.TVmaze != nil || len(second.LookupWarnings) == 0 {
		t.Fatalf("reconciled fresh metadata=%#v warnings=%#v", second.ProviderMetadata, second.LookupWarnings)
	}
}

func TestResolveExternalIDsPreservesUnanchoredProviderResultsWithConflictingLinks(t *testing.T) {
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:          "Example Series",
		TMDBType:       "Scripted",
		IMDbID:         1234567,
		TVDBID:         222333,
		ExternalIMDbID: 1234567,
		ExternalTVDBID: 222333,
	}}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 55,
		IMDBID:     7654321,
		TVDBID:     999888,
		Candidates: []tvmaze.Candidate{{
			ID:        55,
			Name:      "Example Series",
			Externals: tvmaze.Externals{IMDB: "tt7654321", TVDB: 999888},
		}},
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 222333, Name: "Example Series"}}),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Series.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		TrackerData: []api.TrackerMetadata{{
			TMDBID: 444555,
			IMDBID: 1234567,
			TVDBID: 222333,
		}},
		SceneTVmazeID: 55,
	})
	if err != nil {
		t.Fatalf("resolve unanchored identity: %v", err)
	}
	if result.Identity.TMDBID != 444555 || result.Identity.TVmazeID != 55 || result.Identity.IMDBID != 1234567 || result.Identity.TVDBID != 222333 {
		t.Fatalf("unanchored identity = %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TVmaze == nil || len(result.LookupWarnings) != 0 {
		t.Fatalf("unanchored metadata=%#v warnings=%#v", result.ProviderMetadata, result.LookupWarnings)
	}
}

func TestResolveExternalIDsSQLiteFreshProvenanceOnlyAnchorClearsStoredGuess(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	sourcePath := filepath.Join(base, "Example.Movie.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dbPath := filepath.Join(base, "metadata.sqlite")
	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}

	service := NewService(repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		WithMediaInfoExporter(&stubMediaInfo{}),
		WithSceneDetector(stubSceneDetector{}),
		WithTMDBClient(&stubTMDB{}),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)
	request := testCollectionRequest(t, api.Request{SourcePath: sourcePath})
	request.Manifest.SourcePath = sourcePath
	if _, err := service.CollectPreparationEvidence(ctx, request); err != nil {
		t.Fatalf("seed source fingerprint: %v", err)
	}
	if err := repo.SaveExternalIdentity(ctx, api.ExternalIdentity{
		SourcePath: sourcePath,
		TMDBID:     999888,
		IMDBID:     1234567,
		Category:   api.CanonicalCategoryMovie,
		Provenance: api.IdentityProvenanceSet{
			TMDB:     api.IdentityProvenanceProvider,
			IMDB:     api.IdentityProvenanceExplicit,
			Category: api.IdentityProvenanceProvider,
		},
	}); err != nil {
		t.Fatalf("save stored identity: %v", err)
	}
	if err := repo.SaveExternalMetadata(ctx, api.SourceScopedMetadata{
		SourcePath: sourcePath,
		TMDB:       &api.TMDBMetadata{TMDBID: 999888, Title: "Stale Provider Guess"},
	}); err != nil {
		t.Fatalf("save stored metadata: %v", err)
	}
	loaded, err := repo.GetExternalIdentity(ctx, sourcePath)
	if err != nil {
		t.Fatalf("load stored identity: %v", err)
	}
	if loaded.Provenance.IMDB != api.IdentityProvenanceExplicit || loaded.Overrides != (api.IdentityOverrideState{}) {
		t.Fatalf("SQLite identity shape = %#v", loaded)
	}

	pipeline := &recordingEvidencePipeline{service: service}
	collector, err := preparedrelease.NewEvidenceCollector(pipeline)
	if err != nil {
		t.Fatalf("new evidence collector: %v", err)
	}
	if _, err := collector.Collect(ctx, request); err != nil {
		t.Fatalf("collect fresh preparation: %v", err)
	}
	if !pipeline.state.StoredDataFresh {
		t.Fatal("expected SQLite-backed source metadata to load as fresh")
	}
	if pipeline.state.Identity.IMDBID != 1234567 || pipeline.state.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit ||
		pipeline.state.Identity.Overrides != (api.IdentityOverrideState{}) || pipeline.state.Identity.TMDBID != 0 || pipeline.state.ProviderMetadata.TMDB != nil {
		t.Fatalf("collector candidate retained stored guess: identity=%#v metadata=%#v", pipeline.state.Identity, pipeline.state.ProviderMetadata)
	}

	resolver, err := externalidentity.NewWithCandidateSource(repo, collector)
	if err != nil {
		t.Fatalf("new identity resolver: %v", err)
	}
	resolved, err := resolver.Resolve(ctx, externalidentity.Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "sqlite-fresh-source",
		Generation:        1,
	})
	if err != nil {
		t.Fatalf("resolve fresh identity: %v", err)
	}
	if resolved.Identity.IMDBID != 1234567 || resolved.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit || resolved.Identity.TMDBID != 0 {
		t.Fatalf("resolved identity restored stored guess: %#v", resolved.Identity)
	}
	if resolved.ProviderMetadata.TMDB != nil {
		t.Fatalf("resolved metadata restored rejected snapshot: %#v", resolved.ProviderMetadata)
	}
}

func TestResolveExternalIDsSQLiteStaleProvenanceOnlyClearBlocksSiblingPromotion(t *testing.T) {
	ctx := context.Background()
	repo, err := db.Open(filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	const sourcePath = "/media/Example.Movie.2026.1080p-GRP.mkv"
	if err := repo.SaveExternalIdentity(ctx, api.ExternalIdentity{
		SourcePath: sourcePath,
		TMDBID:     444555,
		Category:   api.CanonicalCategoryMovie,
		Provenance: api.IdentityProvenanceSet{
			TMDB: api.IdentityProvenanceExplicit,
			IMDB: api.IdentityProvenanceExplicit,
		},
	}); err != nil {
		t.Fatalf("save stored identity: %v", err)
	}
	loaded, err := repo.GetExternalIdentity(ctx, sourcePath)
	if err != nil {
		t.Fatalf("load stored identity: %v", err)
	}
	if loaded.IMDBID != 0 || loaded.Provenance.IMDB != api.IdentityProvenanceExplicit || loaded.Overrides != (api.IdentityOverrideState{}) {
		t.Fatalf("SQLite clear shape = %#v", loaded)
	}

	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:          "Example Movie",
		TMDBType:       "Movie",
		IMDbID:         7654321,
		ExternalIMDbID: 7654321,
	}}
	imdbClient := &stubIMDB{}
	service := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)
	result, err := service.collectExternalIdentityEvidence(ctx, preparationstate.State{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("collect stale identity: %v", err)
	}
	if result.Identity.TMDBID != 444555 || result.Identity.IMDBID != 0 || result.Identity.Overrides.IMDB != api.OverrideStateClear {
		t.Fatalf("stored clear was not preserved: %#v", result.Identity)
	}
	if imdbClient.searchCalls != 0 || imdbClient.infoCalls != 0 {
		t.Fatalf("stored clear triggered IMDb lookup: search=%d info=%d", imdbClient.searchCalls, imdbClient.infoCalls)
	}
	if tmdbClient.metaCalls != 1 || result.ProviderMetadata.TMDB == nil {
		t.Fatalf("TMDB anchor metadata = calls:%d metadata:%#v", tmdbClient.metaCalls, result.ProviderMetadata.TMDB)
	}
}

func TestResolveExternalIDsRejectsInferredTVmazeConflictingWithExplicitTMDB(t *testing.T) {
	tmdbID := 444555
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:          "Example Series",
		TMDBType:       "Scripted",
		IMDbID:         1234567,
		TVDBID:         222333,
		ExternalIMDbID: 1234567,
		ExternalTVDBID: 222333,
	}}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 55,
		Candidates: []tvmaze.Candidate{{
			ID:        55,
			Name:      "Conflicting Series",
			Externals: tvmaze.Externals{IMDB: "tt7654321", TVDB: 999888},
		}},
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 222333, Name: "Example Series"}}),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Series.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve explicit TMDB anchor: %v", err)
	}
	if result.Identity.TMDBID != tmdbID || result.Identity.IMDBID != 1234567 || result.Identity.TVDBID != 222333 || result.Identity.TVmazeID != 0 {
		t.Fatalf("reconciled identity = %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TVmaze != nil || len(result.LookupWarnings) == 0 {
		t.Fatalf("reconciled metadata=%#v warnings=%#v", result.ProviderMetadata, result.LookupWarnings)
	}
}

func TestResolveExternalIDsUsesExplicitTVmazeCrossReferences(t *testing.T) {
	tvmazeID := 55
	tmdbClient := &stubTMDB{
		findFn: func(input tmdb.FindInput) (tmdb.FindResult, error) {
			if input.IMDbID != "tt1234567" || input.TVDBID != 222333 {
				return tmdb.FindResult{}, nil
			}
			return tmdb.FindResult{TMDBID: 444555, Category: "TV"}, nil
		},
		metadata: tmdb.MetadataResult{
			Title:          "Example Series",
			TMDBType:       "Scripted",
			IMDbID:         1234567,
			TVDBID:         222333,
			ExternalIMDbID: 1234567,
			ExternalTVDBID: 222333,
		},
	}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 55,
		IMDBID:     1234567,
		TVDBID:     222333,
		Candidates: []tvmaze.Candidate{{
			ID:        55,
			Name:      "Example Series",
			Externals: tvmaze.Externals{IMDB: "tt1234567", TVDB: 222333},
		}},
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 222333, Name: "Example Series"}}),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Series.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVmazeID: &tvmazeID,
		},
	})
	if err != nil {
		t.Fatalf("resolve TVmaze identity: %v", err)
	}
	if result.Identity.TVmazeID != 55 || result.Identity.IMDBID != 1234567 || result.Identity.TVDBID != 222333 || result.Identity.TMDBID != 444555 {
		t.Fatalf("TVmaze cross-reference identity = %#v", result.Identity)
	}
	if tmdbClient.findCalls != 1 || tmdbClient.searchCalls != 0 || tmdbClient.metaCalls != 1 {
		t.Fatalf("TMDB calls = find:%d search:%d metadata:%d", tmdbClient.findCalls, tmdbClient.searchCalls, tmdbClient.metaCalls)
	}
	if len(tmdbClient.findInputs) != 1 || !tmdbClient.findInputs[0].RequireExternalIDAgreement {
		t.Fatalf("anchored TMDB lookup did not require external ID agreement: %#v", tmdbClient.findInputs)
	}
}

func TestResolveExternalIDsRejectsInferredTMDBConflictingWithExplicitTVmaze(t *testing.T) {
	tvmazeID := 55
	tmdbClient := &stubTMDB{
		findResult: tmdb.FindResult{TMDBID: 444555, Category: "TV"},
		metadata: tmdb.MetadataResult{
			Title:          "Conflicting Series",
			TMDBType:       "Scripted",
			IMDbID:         7654321,
			TVDBID:         222333,
			ExternalIMDbID: 7654321,
			ExternalTVDBID: 222333,
		},
	}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: tvmazeID,
		IMDBID:     1234567,
		TVDBID:     222333,
		Candidates: []tvmaze.Candidate{{
			ID:        tvmazeID,
			Name:      "Example Series",
			Externals: tvmaze.Externals{IMDB: "tt1234567", TVDB: 222333},
		}},
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Series",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 222333, Name: "Example Series"}}),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Series.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVmazeID: &tvmazeID,
		},
	})
	if err != nil {
		t.Fatalf("resolve explicit TVmaze anchor: %v", err)
	}
	if result.Identity.TVmazeID != tvmazeID || result.Identity.IMDBID != 1234567 || result.Identity.TVDBID != 222333 || result.Identity.TMDBID != 0 {
		t.Fatalf("reconciled identity = %#v", result.Identity)
	}
	if result.ProviderMetadata.TVmaze == nil || result.ProviderMetadata.TMDB != nil || len(result.LookupWarnings) == 0 {
		t.Fatalf("reconciled metadata=%#v warnings=%#v", result.ProviderMetadata, result.LookupWarnings)
	}
}

func TestResolveExternalIDsRejectsUnverifiedTVmazeTitleMatch(t *testing.T) {
	imdbID := 1234567
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 55,
		Candidates: []tvmaze.Candidate{{ID: 55, Name: "Unrelated Title Match"}},
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(&stubTMDB{findResult: tmdb.FindResult{FilenameSearch: true}}),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt1234567",
			Title:  "Example Anchor",
			Type:   "tvSeries",
		}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Anchor.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			IMDBID: &imdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve TVmaze title match: %v", err)
	}
	if result.Identity.TVmazeID != 0 || result.ProviderMetadata.TVmaze != nil {
		t.Fatalf("unverified TVmaze title match became canonical: identity=%#v metadata=%#v", result.Identity, result.ProviderMetadata.TVmaze)
	}
	if len(result.LookupWarnings) != 1 || !strings.Contains(result.LookupWarnings[0], "did not verify the supplied IMDb ID") {
		t.Fatalf("verification warnings = %#v", result.LookupWarnings)
	}
}

func TestResolveExternalIDsTMDBAnchorSuppressesTVmazeNameFallback(t *testing.T) {
	tmdbID := 123
	tvmazeClient := &stubTVmaze{}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{
			Title:          "Example Anchor",
			TMDBType:       "TV",
			IMDbID:         1234567,
			ExternalIMDbID: 1234567,
		}}),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(tvmazeClient),
	)
	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:          "Example.Anchor.S01E01.1080p-GRP.mkv",
		MediaInfoCategory:   "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: &tmdbID},
	})
	if err != nil {
		t.Fatalf("resolve TMDB anchor: %v", err)
	}
	if len(tvmazeClient.inputs) == 0 {
		t.Fatal("expected TVmaze lookup attempts")
	}
	for _, input := range tvmazeClient.inputs {
		if input.AllowNameFallback || !input.StrictIDOnly {
			t.Fatalf("anchored TVmaze lookup permits title fallback: %#v", input)
		}
	}
	if result.Identity.TVmazeID != 0 || result.ProviderMetadata.TVmaze != nil {
		t.Fatalf("unverified TVmaze identity: %#v", result.Identity)
	}
}

func TestResolveExternalIDsMALOnlySuppressesTitleSelection(t *testing.T) {
	malID := 999
	tmdbClient := &stubTMDB{
		searchOutcome:   tmdb.SearchOutcome{TMDBID: 123, Category: "TV"},
		anilistMetadata: tmdb.AniListMetadataResult{MALID: malID, TitleRomaji: "Example Anime"},
	}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{searchResult: imdb.SearchResult{IMDbID: 1234567}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Anime.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			MALID: &malID,
		},
	})
	if err != nil {
		t.Fatalf("resolve MAL identity: %v", err)
	}
	if result.Identity.MALID != malID || result.Identity.TMDBID != 0 || result.Identity.IMDBID != 0 {
		t.Fatalf("MAL-only identity = %#v", result.Identity)
	}
	if tmdbClient.searchCalls != 0 || tmdbClient.anilistCalls != 1 {
		t.Fatalf("provider calls = search:%d anilist:%d", tmdbClient.searchCalls, tmdbClient.anilistCalls)
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

	meta := preparationstate.State{
		SourcePath: `/media/Example.Show.2025.11.10.1080p.WEB-DL.mkv`,
		Release: api.ReleaseInfo{
			Title:    "Example Show",
			Year:     2025,
			Category: "TV",
			Type:     "WEB-DL",
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.Category != api.CanonicalCategoryTV {
		t.Fatalf("expected category TV from release type episode, got %q", result.Identity.Category)
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

	meta := preparationstate.State{
		SourcePath: "/media/Example.Series.2025.1080p.WEB-DL.mkv",
		Policy:     preparationstate.CollectionPolicy{InteractionMode: api.InteractionModeUnattended},
		Release: api.ReleaseInfo{
			Title: "Example Series",
			Year:  2025,
			Type:  "episode",
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 4242 {
		t.Fatalf("expected staged tmdb id 4242, got %d", result.Identity.TMDBID)
	}
	if result.Identity.IMDBID != 2424 {
		t.Fatalf("expected staged imdb id 2424, got %d", result.Identity.IMDBID)
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

	_, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: "/media/Example.Series.2025.1080p.WEB-DL.mkv",
		Policy:     preparationstate.CollectionPolicy{InteractionMode: api.InteractionModeInteractive},
		Release: api.ReleaseInfo{
			Title: "Example Series",
			Year:  2025,
			Type:  "episode",
		},
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

	meta := preparationstate.State{
		SourcePath:       "/media/Example.Quiz.2026.01.12.720p.HDTV.x264.mkv",
		DailyEpisodeDate: "2026-01-12",
		Release: api.ReleaseInfo{
			Title: "Example Quiz",
			Year:  2026,
			Type:  "episode",
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 4242 || result.Identity.IMDBID != 2424 {
		t.Fatalf("unexpected resolved ids: %#v", result.Identity)
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
		metadata: tmdb.MetadataResult{
			Title:    "Example Show",
			TMDBType: "Scripted",
			IMDbID:   1234567,
		},
	}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt1234567", Title: "Example Show"}}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := preparationstate.State{
		SourcePath:        "/media/Example.Show.S01E01.2160p.WEB-DL.mkv",
		MediaInfoCategory: "TV",
		Release:           api.ReleaseInfo{Title: "Example Show"},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.IMDBID != 1234567 {
		t.Fatalf("expected imdb id resolved from tmdb metadata, got %d", result.Identity.IMDBID)
	}
	if imdbClient.infoCalls == 0 {
		t.Fatalf("expected imdb metadata fetch after tmdb-imdb enrichment")
	}
	if result.ProviderMetadata.IMDB == nil {
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

	meta := preparationstate.State{
		SourcePath:        "/media/Example.Movie.2024.2160p.WEB-DL.mkv",
		MediaInfoCategory: "movie",
		Release:           api.ReleaseInfo{Title: "Example Movie", Year: 2024},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TVDBID != 0 {
		t.Fatalf("expected no tvdb id without imdb tv movie metadata, got %d", result.Identity.TVDBID)
	}
	if len(tvdbClient.tvMovieCalls) != 0 {
		t.Fatalf("expected no tvdb lookup for movie category, got calls %#v", tvdbClient.tvMovieCalls)
	}
}

func TestResolveExternalIDsDoesNotApplyTVDBForMovieCategory(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 1234, Category: "Movie"},
		metadata: tmdb.MetadataResult{
			Title:    "Example TV Movie",
			TMDBType: "Movie",
			TVDBID:   55,
		},
	}
	imdbClient := &stubIMDB{
		searchResult: imdb.SearchResult{IMDbID: 9876},
		info: imdb.Info{
			IMDbID: "tt0009876",
			Title:  "Example TV Movie",
			Type:   "tvMovie",
		},
	}
	tvdbClient := &stubTVDB{idWhenTVMovie: 55, nameWhenTVMovie: "Example TV Movie"}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := preparationstate.State{
		SourcePath:        "/media/Example.TV.Movie.2024.1080p.WEB-DL.mkv",
		MediaInfoCategory: "movie",
		Release:           api.ReleaseInfo{Title: "Example TV Movie", Year: 2024},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TVDBID != 0 {
		t.Fatalf("expected no tvdb id for movie category, got %d", result.Identity.TVDBID)
	}
	if result.ProviderMetadata.TVDB != nil {
		t.Fatalf("expected no tvdb metadata for movie category, got %#v", result.ProviderMetadata.TVDB)
	}
	if len(tvdbClient.tvMovieCalls) != 0 {
		t.Fatalf("expected no tvdb lookup for movie category, got %#v", tvdbClient.tvMovieCalls)
	}
}

func TestResolveExternalIDsTVmazeWaitsForIDThenFallsBack(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 224372, Category: "TV"},
		metadata: tmdb.MetadataResult{
			Title:    "Example Show",
			TMDBType: "Scripted",
			TVDBID:   433631,
		},
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

	meta := preparationstate.State{
		SourcePath:        "/media/Example.Show.S01E01.2160p.WEB-DL.mkv",
		MediaInfoCategory: "TV",
		Release:           api.ReleaseInfo{Title: "Example Show"},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
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
	if result.ProviderMetadata.TVmaze == nil || result.ProviderMetadata.TVmaze.TVmazeID != 55 {
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

	meta := preparationstate.State{
		SourcePath: "/media/file.mkv",
		TrackerData: []api.TrackerMetadata{{
			TMDBID: 1,
			IMDBID: 2,
			TVDBID: 3,
		}},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: new(999),
			IMDBID: new(111),
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 999 || result.Identity.IMDBID != 111 {
		t.Fatalf("unexpected override ids: %#v", result.Identity)
	}
	if result.Identity.Provenance.TMDB != api.IdentityProvenanceExplicit || result.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit ||
		result.Identity.Overrides.TMDB != api.OverrideStateValue || result.Identity.Overrides.IMDB != api.OverrideStateValue {
		t.Fatalf("unexpected override sources: %#v", result.Identity)
	}
}

func TestResolveExternalIDsPreservesExplicitTVDBIDForMovieConflict(t *testing.T) {
	tvdbID := 345678
	svc := NewService(&fakeRepo{},
		WithTMDBClient(&stubTMDB{}),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "Example.Movie.2026.1080p-GRP.mkv",
		MediaInfoCategory: "MOVIE",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID: &tvdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve movie conflict: %v", err)
	}
	if result.Identity.TVDBID != tvdbID || result.Identity.Provenance.TVDB != api.IdentityProvenanceExplicit {
		t.Fatalf("explicit TVDB ID changed: %#v", result.Identity)
	}
	if result.ProviderMetadata.TVDB != nil {
		t.Fatalf("movie retained TVDB metadata: %#v", result.ProviderMetadata.TVDB)
	}
	if len(result.LookupWarnings) != 1 || !strings.Contains(result.LookupWarnings[0], "Correct the TVDB ID or category") {
		t.Fatalf("correction warnings = %#v", result.LookupWarnings)
	}
}

func TestResolveExternalIDsRefetchesProviderSnapshotsAfterIDOverride(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Current TMDB", TMDBType: "Movie"}}
	imdbClient := &stubIMDB{info: imdb.Info{IMDbID: "tt0000111", Title: "Current IMDb"}}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:      "Example.Release.2026.1080p-GRP.mkv",
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: "Example.Release.2026.1080p-GRP.mkv",
			Category:   "movie",
			TMDBID:     1,
			IMDBID:     2,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "Example.Release.2026.1080p-GRP.mkv",
			TMDB:       &api.TMDBMetadata{TMDBID: 1, Title: "Stale TMDB"},
			IMDB:       &api.IMDBMetadata{IMDBID: 2, Title: "Stale IMDb"},
		},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: new(999),
			IMDBID: new(111),
		},
	})
	if err != nil {
		t.Fatalf("resolve changed provider IDs: %v", err)
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TMDB.TMDBID != 999 || result.ProviderMetadata.TMDB.Title != "Current TMDB" {
		t.Fatalf("expected refetched TMDB snapshot, got %#v", result.ProviderMetadata.TMDB)
	}
	if result.ProviderMetadata.IMDB == nil || result.ProviderMetadata.IMDB.IMDBID != 111 || result.ProviderMetadata.IMDB.Title != "Current IMDb" {
		t.Fatalf("expected refetched IMDb snapshot, got %#v", result.ProviderMetadata.IMDB)
	}
	if tmdbClient.metaCalls != 1 || imdbClient.infoCalls != 1 {
		t.Fatalf("expected one provider refetch each, got tmdb=%d imdb=%d", tmdbClient.metaCalls, imdbClient.infoCalls)
	}
}

func TestResolveExternalIDsRefetchesTMDBMetadataWhenCategoryChangesWithSameID(t *testing.T) {
	for _, test := range []struct {
		name              string
		storedCategory    api.CanonicalCategory
		requestedCategory string
		freshTitle        string
		freshGenres       string
		freshType         string
	}{
		{
			name:              "movie to tv",
			storedCategory:    api.CanonicalCategoryMovie,
			requestedCategory: "TV",
			freshTitle:        "Current Series",
			freshGenres:       "Science Fiction",
			freshType:         "Scripted",
		},
		{
			name:              "tv to movie",
			storedCategory:    api.CanonicalCategoryTV,
			requestedCategory: "MOVIE",
			freshTitle:        "Current Film",
			freshGenres:       "Thriller",
			freshType:         "Movie",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			category := test.requestedCategory
			tmdbClient := &stubTMDB{metadataFn: func(input tmdb.MetadataInput) (tmdb.MetadataResult, error) {
				if input.Category != strings.ToLower(test.requestedCategory) {
					t.Errorf("TMDB category = %q, want %q", input.Category, test.requestedCategory)
				}
				return tmdb.MetadataResult{
					Title:    test.freshTitle,
					Genres:   test.freshGenres,
					TMDBType: test.freshType,
				}, nil
			}}
			svc := NewService(&fakeRepo{},
				WithTMDBClient(tmdbClient),
				WithIMDBClient(&stubIMDB{}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(&stubTVmaze{}),
			)

			result, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
				SourcePath:      "/media/Example.Release.2026.1080p-GRP.mkv",
				StoredDataFresh: true,
				Identity: api.ExternalIdentity{
					SourcePath: "/media/Example.Release.2026.1080p-GRP.mkv",
					Category:   test.storedCategory,
					TMDBID:     12345,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: "/media/Example.Release.2026.1080p-GRP.mkv",
					TMDB: &api.TMDBMetadata{
						TMDBID:   12345,
						Category: string(test.storedCategory),
						Title:    "Stale title",
						Genres:   "Stale genre",
					},
				},
				ReleaseNameOverrides: api.ReleaseNameOverrides{Category: &category},
			})
			if err != nil {
				t.Fatalf("resolve external identity: %v", err)
			}
			if tmdbClient.metaCalls != 1 {
				t.Fatalf("TMDB metadata calls = %d, want 1", tmdbClient.metaCalls)
			}
			if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TMDB.Title != test.freshTitle ||
				result.ProviderMetadata.TMDB.Genres != test.freshGenres || result.ProviderMetadata.TMDB.Category != strings.ToLower(test.requestedCategory) {
				t.Fatalf("TMDB metadata = %#v", result.ProviderMetadata.TMDB)
			}
		})
	}
}

func TestResolveExternalIDsClearIMDBSuppressesTMDBSiblingEnrichment(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		IMDbID:   777,
		TMDBType: "Movie",
		Title:    "Example",
	}}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{}
	tvmazeClient := &stubTVmaze{}

	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	meta := preparationstate.State{
		SourcePath:  "/media/file.mkv",
		TrackerData: []api.TrackerMetadata{{IMDBID: 2}},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: new(999),
			IMDBID: new(0),
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Identity.TMDBID != 999 {
		t.Fatalf("expected tmdb override 999, got %d", result.Identity.TMDBID)
	}
	if result.Identity.IMDBID != 0 || result.Identity.Overrides.IMDB != api.OverrideStateClear {
		t.Fatalf("expected imdb clear to remain authoritative, got %#v", result.Identity)
	}
	if imdbClient.infoCalls != 0 {
		t.Fatalf("expected imdb lookup suppressed, got %d calls", imdbClient.infoCalls)
	}
}

func TestResolveExternalIDsClearTMDBSuppressesRetainedIMDBLookup(t *testing.T) {
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

	meta := preparationstate.State{
		SourcePath:  "/media/file.mkv",
		TrackerData: []api.TrackerMetadata{{IMDBID: 159881, TMDBID: 1}},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: new(0),
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if tmdbClient.findCalls != 0 || tmdbClient.searchCalls != 0 || tmdbClient.metaCalls != 0 {
		t.Fatalf("expected tmdb lookups suppressed, got find=%d search=%d metadata=%d", tmdbClient.findCalls, tmdbClient.searchCalls, tmdbClient.metaCalls)
	}
	if result.Identity.TMDBID != 0 || result.Identity.Overrides.TMDB != api.OverrideStateClear {
		t.Fatalf("expected tmdb clear to remain authoritative, got %#v", result.Identity)
	}
}

func TestResolveExternalIDsClearTVDBAndTVmazeSuppressesSiblingResolution(t *testing.T) {
	tmdbID := 999
	clearedID := 0
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:          "Example Series",
		TMDBType:       "TV",
		IMDbID:         777,
		TVDBID:         888,
		ExternalIMDbID: 777,
		ExternalTVDBID: 888,
	}}
	tvdbClient := &stubTVDB{id: 888}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{SelectedID: 55}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(tvmazeClient),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Series.S01E01.1080p-GRP.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID:   &tmdbID,
			TVDBID:   &clearedID,
			TVmazeID: &clearedID,
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Identity.IMDBID != 777 {
		t.Fatalf("expected uncleared imdb sibling enrichment, got %#v", result.Identity)
	}
	if result.Identity.TVDBID != 0 || result.Identity.Overrides.TVDB != api.OverrideStateClear ||
		result.Identity.TVmazeID != 0 || result.Identity.Overrides.TVmaze != api.OverrideStateClear {
		t.Fatalf("expected tvdb and tvmaze clears to remain authoritative, got %#v", result.Identity)
	}
	if tvdbClient.calls != 0 || tvmazeClient.calls != 0 {
		t.Fatalf("expected cleared provider lookups suppressed, got tvdb=%d tvmaze=%d", tvdbClient.calls, tvmazeClient.calls)
	}
}

func TestResolveExternalIDsMissingRepo(t *testing.T) {
	svc := NewService(nil)
	_, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{SourcePath: "/media/file.mkv"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSearchTitleYearFromPathFallback(t *testing.T) {
	meta := preparationstate.State{
		SourcePath: `D:\Movies\2026 - Example Movie [DVD9.PAL]`,
	}

	title, secondary := resolveSearchTitles(meta)
	year := resolveSearchYear(meta)

	if title != "Example Movie" {
		t.Fatalf("expected normalized title, got %q", title)
	}
	if secondary != "" {
		t.Fatalf("expected empty secondary title, got %q", secondary)
	}
	if year != 2026 {
		t.Fatalf("expected inferred year 2026, got %d", year)
	}
}

func TestApplyTVEpisodeMetadataDailyMappingMatch(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{dailySeason: 2, dailyEpisode: 7}

	meta := preparationstate.State{
		SourcePath:       "/media/Show.2024-01-15.mkv",
		DailyEpisodeDate: "2024-01-15",
	}
	ids := &api.ExternalIdentity{
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

	meta := preparationstate.State{
		SourcePath:       "/media/Show.2024-01-15.mkv",
		DailyEpisodeDate: "2024-01-15",
	}
	ids := &api.ExternalIdentity{
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

func TestApplyTVEpisodeMetadataDailyMappingNoMatchDoesNotPersistParsedFallback(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}

	meta := preparationstate.State{
		SourcePath:       "/media/Daily.Show.2024-01-15.mkv",
		DailyEpisodeDate: "2024-01-15",
		Release: api.ReleaseInfo{
			Category: "TV",
			Season:   2024,
			Episode:  115,
		},
	}
	ids := &api.ExternalIdentity{
		TMDBID:   100,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, &stubTVDB{}, &stubTVmaze{})
	if updated.SeasonInt != 0 || updated.EpisodeInt != 0 {
		t.Fatalf("expected parsed fallback not to persist as canonical season/episode, got %d/%d", updated.SeasonInt, updated.EpisodeInt)
	}
	if updated.SeasonStr != "" || updated.EpisodeStr != "" {
		t.Fatalf("expected empty formatted season/episode, got %q/%q", updated.SeasonStr, updated.EpisodeStr)
	}
	fallbackSeason, fallbackEpisode := updated.SeasonEpisodeWithParsedFallback()
	if fallbackSeason != 2024 || fallbackEpisode != 115 {
		t.Fatalf("expected parsed fallback 2024/115 to remain available, got %d/%d", fallbackSeason, fallbackEpisode)
	}
}

func TestApplyTVEpisodeMetadataUseSeasonEpisodePrefersTMDBDateMapping(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{dailySeason: 2, dailyEpisode: 7}
	useSeasonEpisode := true

	meta := preparationstate.State{
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
	ids := &api.ExternalIdentity{
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

func TestApplyTVEpisodeMetadataManualSeasonEpisodeInstructionsBecomeCanonical(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{dailySeason: 9, dailyEpisode: 9}
	tvmazeClient := &stubTVmaze{episodeData: &tvmaze.EpisodeData{SeasonNumber: 7, EpisodeNumber: 8}}

	meta := preparationstate.State{
		SourcePath:       "/media/Show.2024-01-15.mkv",
		DailyEpisodeDate: "2024-01-15",
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			Season:  new("S02"),
			Episode: new("5"),
		},
	}
	ids := &api.ExternalIdentity{
		TMDBID:   100,
		TVmazeID: 200,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, &stubTVDB{}, tvmazeClient)
	if updated.SeasonInt != 2 || updated.EpisodeInt != 5 {
		t.Fatalf("expected manual season/episode 2/5 to stay canonical over date mapping, got %d/%d", updated.SeasonInt, updated.EpisodeInt)
	}
	if updated.SeasonStr != "S02" || updated.EpisodeStr != "E05" {
		t.Fatalf("expected formatted manual season/episode S02/E05, got %q/%q", updated.SeasonStr, updated.EpisodeStr)
	}
}

func TestApplyTVEpisodeMetadataManualDateInstructionWinsOverParsedDate(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{dailySeason: 2, dailyEpisode: 7}

	meta := preparationstate.State{
		SourcePath:       "/media/Show.2024-01-15.mkv",
		DailyEpisodeDate: "2024-01-15",
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			ManualDate: new("2024-02-20"),
		},
	}
	ids := &api.ExternalIdentity{
		TMDBID:   100,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, &stubTVDB{}, &stubTVmaze{})
	if updated.DailyEpisodeDate != "2024-02-20" {
		t.Fatalf("expected manual daily date to become canonical, got %q", updated.DailyEpisodeDate)
	}
	if updated.SeasonInt != 2 || updated.EpisodeInt != 7 {
		t.Fatalf("expected date-mapped season/episode 2/7, got %d/%d", updated.SeasonInt, updated.EpisodeInt)
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

	meta := preparationstate.State{
		SourcePath: "/media/Le.Bureau.S01E01.mkv",
	}
	ids := &api.ExternalIdentity{
		TMDBID:   100,
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
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

func TestApplyTVEpisodeMetadataTVDBAliasYearUsesLastYear(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{specificAlias: "Hunter x Hunter (1999) (2011)"}

	meta := preparationstate.State{
		SourcePath: "/media/Hunter.x.Hunter.S01E01.mkv",
	}
	ids := &api.ExternalIdentity{
		TMDBID:   100,
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		TVDB: &api.TVDBMetadata{TVDBID: 200},
	}

	_ = svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if external.TVDB.Name != "Hunter x Hunter" {
		t.Fatalf("expected cleaned alias title, got %q", external.TVDB.Name)
	}
	if external.TVDB.Year != 2011 {
		t.Fatalf("expected last alias year 2011, got %d", external.TVDB.Year)
	}
	if !external.TVDB.YearFromAlias {
		t.Fatal("expected multi-year alias to mark YearFromAlias")
	}
}

func TestApplyTVEpisodeMetadataTVDBAliasYearPreservesSource(t *testing.T) {
	tests := []struct {
		name       string
		yearSource string
		confidence string
	}{
		{
			name:       "translation name",
			yearSource: "translation_name",
			confidence: "high",
		},
		{
			name:       "translation alias",
			yearSource: "translation_alias",
			confidence: "high",
		},
		{
			name:       "extended alias",
			yearSource: "extended_alias",
			confidence: "high",
		},
		{
			name:       "slug",
			yearSource: "slug",
			confidence: "low",
		},
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

			meta := preparationstate.State{
				SourcePath: "/media/Hunter.x.Hunter.S01E01.mkv",
			}
			ids := &api.ExternalIdentity{
				TMDBID:   100,
				TVDBID:   200,
				Category: "TV",
			}
			external := &api.SourceScopedMetadata{
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

	meta := preparationstate.State{
		SourcePath: "/media/Cats.Eye.S01E01.mkv",
	}
	ids := &api.ExternalIdentity{
		TMDBID:   100,
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
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

func TestApplyTVEpisodeMetadataTVDBAliasPreservesNativeTitle(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{specificAlias: "English Show (2021)"}

	meta := preparationstate.State{
		SourcePath: "/media/English.Show.S01E01.mkv",
	}
	ids := &api.ExternalIdentity{
		TMDBID:   100,
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{OriginalLanguage: "en"},
		TVDB: &api.TVDBMetadata{
			TVDBID:      200,
			Name:        "Native Name",
			NameEnglish: "Earlier English Name",
			Year:        2015,
		},
	}

	_ = svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if external.TVDB.Name != "Native Name" {
		t.Fatalf("expected native title preserved, got %q", external.TVDB.Name)
	}
	if external.TVDB.NameEnglish != "English Show" {
		t.Fatalf("expected English title preserved, got %q", external.TVDB.NameEnglish)
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
			Aired:        "2026-01-02",
			Image:        "https://img.example/tvdb-episode-2.jpg",
		}}},
		episodeTranslate: tvdb.EpisodeTranslation{
			Name:     "Episode 2",
			Overview: "English episode overview",
		},
	}

	meta := preparationstate.State{
		SourcePath: "/media/Example.Show.S01E02.mkv",
		SeasonInt:  1,
		EpisodeInt: 2,
	}
	ids := &api.ExternalIdentity{
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
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
	if external.TVDB.EpisodeImage != "https://img.example/tvdb-episode-2.jpg" {
		t.Fatalf("expected episode image from TVDB, got %q", external.TVDB.EpisodeImage)
	}
	if len(external.TVDB.Episodes) != 1 {
		t.Fatalf("expected one stored TVDB episode, got %#v", external.TVDB.Episodes)
	}
	storedEpisode := external.TVDB.Episodes[0]
	if storedEpisode.EpisodeNameEnglish != "Episode 2" || storedEpisode.EpisodeOverviewEnglish != "English episode overview" || storedEpisode.EpisodeImage != "https://img.example/tvdb-episode-2.jpg" {
		t.Fatalf("expected stored TVDB episode to include translated text and image, got %#v", storedEpisode)
	}
	if !external.TVDB.HasEnglish {
		t.Fatalf("expected HasEnglish true when english episode fields are populated")
	}
	if updated.EpisodeTitle != "" {
		t.Fatalf("expected episode title blanked when title contains episode/tba, got %q", updated.EpisodeTitle)
	}
	if updated.EpisodeOverview != "English episode overview" {
		t.Fatalf("expected english episode overview, got %q", updated.EpisodeOverview)
	}
}

func TestApplyTVEpisodeMetadataTVDBMapsSeasonlessAbsoluteWithoutTMDB(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tvdbClient := &stubTVDB{
		episodes: tvdb.EpisodesData{Episodes: []tvdb.Episode{{
			ID:             112,
			SeasonNumber:   1,
			Number:         12,
			AbsoluteNumber: 12,
			Name:           "第12話",
		}}},
		episodeTranslate: tvdb.EpisodeTranslation{Name: "Take the Example Path"},
	}
	meta := preparationstate.State{
		SourcePath: "Example.Release.-.12.1080p-GRP.mkv",
		EpisodeInt: 12,
	}
	ids := &api.ExternalIdentity{TVDBID: 200, Category: "TV"}
	external := &api.SourceScopedMetadata{
		TVDB: &api.TVDBMetadata{TVDBID: 200, OriginalLanguage: "jpn"},
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, nil, tvdbClient, &stubTVmaze{})

	if updated.SeasonInt != 1 || updated.EpisodeInt != 12 || updated.SeasonStr != "S01" || updated.EpisodeStr != "E12" {
		t.Fatalf("expected TVDB absolute mapping to S01E12, got season=%d episode=%d strings=%q/%q", updated.SeasonInt, updated.EpisodeInt, updated.SeasonStr, updated.EpisodeStr)
	}
	if updated.EpisodeTitle != "Take the Example Path" {
		t.Fatalf("expected translated TVDB episode title, got %q", updated.EpisodeTitle)
	}
	if tvdbClient.lastEpisodeQuery.Season != 0 || tvdbClient.lastEpisodeQuery.Episode != 12 || tvdbClient.lastEpisodeQuery.Absolute != 0 {
		t.Fatalf("expected seasonless TVDB query independent of TMDB anime metadata, got %#v", tvdbClient.lastEpisodeQuery)
	}
}

func TestApplyTVEpisodeMetadataIMDbMapsSeasonlessAbsoluteThenUsesTVmaze(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tvmazeClient := &stubTVmaze{episodeData: &tvmaze.EpisodeData{
		EpisodeName:   "TVmaze Episode Three",
		Overview:      "TVmaze overview.",
		SeasonNumber:  2,
		EpisodeNumber: 1,
		AirDate:       "2026-04-24",
	}}
	meta := preparationstate.State{
		SourcePath: "Example.Series.-.03.1080p-GRP.mkv",
		EpisodeInt: 3,
	}
	ids := &api.ExternalIdentity{
		IMDBID:   1234567,
		TVmazeID: 55,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
		IMDBID: 1234567,
		Title:  "Example Series",
		Type:   "tvSeries",
		Episodes: []api.IMDBEpisode{
			{
				ID:          "tt1000001",
				Title:       "Episode One",
				Season:      1,
				EpisodeText: "1",
			},
			{
				ID:          "tt1000002",
				Title:       "Episode Two",
				Season:      1,
				EpisodeText: "2",
			},
			{
				ID:          "tt1000003",
				Title:       "IMDb Episode Three",
				Season:      2,
				EpisodeText: "1",
				ReleaseYear: 2026,
			},
		},
	}}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, nil, &stubTVDB{}, tvmazeClient)

	if updated.SeasonInt != 2 || updated.EpisodeInt != 1 || updated.SeasonStr != "S02" || updated.EpisodeStr != "E01" {
		t.Fatalf("expected IMDb absolute mapping to S02E01, got season=%d episode=%d strings=%q/%q", updated.SeasonInt, updated.EpisodeInt, updated.SeasonStr, updated.EpisodeStr)
	}
	if tvmazeClient.lastSeason != 2 || tvmazeClient.lastEpisode != 1 {
		t.Fatalf("expected mapped S02E01 TVmaze lookup, got S%02dE%02d", tvmazeClient.lastSeason, tvmazeClient.lastEpisode)
	}
	if updated.EpisodeTitle != "TVmaze Episode Three" || updated.EpisodeOverview != "TVmaze overview." || updated.EpisodeYear != 2026 {
		t.Fatalf("expected TVmaze details with IMDb year fallback, got title=%q overview=%q year=%d", updated.EpisodeTitle, updated.EpisodeOverview, updated.EpisodeYear)
	}
}

func TestApplyTVEpisodeMetadataIMDbTitleFallbackWithoutTVmaze(t *testing.T) {
	svc := NewService(&fakeRepo{})
	meta := preparationstate.State{
		SourcePath: "Example.Series.S01E02.1080p-GRP.mkv",
		SeasonInt:  1,
		EpisodeInt: 2,
	}
	ids := &api.ExternalIdentity{IMDBID: 1234567, Category: "TV"}
	external := &api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
		IMDBID: 1234567,
		Title:  "Example Series",
		Episodes: []api.IMDBEpisode{{
			ID:          "tt1000002",
			Title:       "The Example Path",
			Season:      1,
			EpisodeText: "2",
			ReleaseYear: 2026,
		}},
	}}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, nil, &stubTVDB{}, &stubTVmaze{})

	if updated.EpisodeTitle != "The Example Path" || updated.EpisodeYear != 2026 {
		t.Fatalf("expected IMDb episode fallback, got title=%q year=%d", updated.EpisodeTitle, updated.EpisodeYear)
	}
}

func TestApplyTVEpisodeMetadataAnimeTVPackKeepsEpisodeEmpty(t *testing.T) {
	svc := NewService(&fakeRepo{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	meta := preparationstate.State{
		SourcePath: "[GRP] Example Release - Season 1 (BD 1080p x265 10-bit)",
		VideoPath:  "[GRP] Example Release - S01E01 (BD 1080p x265 10-bit).mkv",
		FileList: []string{
			"[GRP] Example Release - S01E01 (BD 1080p x265 10-bit).mkv",
			"[GRP] Example Release - S01E02 (BD 1080p x265 10-bit).mkv",
		},
		SeasonInt: 1,
		SeasonStr: "S01",
		TVPack:    true,
		Anime:     true,
	}
	ids := &api.ExternalIdentity{TVDBID: 200, Category: "TV"}

	updated := svc.applyTVEpisodeMetadata(ctx, meta, ids, nil, nil, &stubTVDB{}, &stubTVmaze{})

	if updated.SeasonInt != 1 || updated.SeasonStr != "S01" || updated.EpisodeInt != 0 || updated.EpisodeStr != "" {
		t.Fatalf("expected anime TV pack to remain S01, got season=%d/%q episode=%d/%q", updated.SeasonInt, updated.SeasonStr, updated.EpisodeInt, updated.EpisodeStr)
	}
}

func TestApplyTVEpisodeMetadataStoresTVDBSeasonEpisodesForPack(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tmdbClient := &stubTMDB{}
	tvdbClient := &stubTVDB{
		episodes: tvdb.EpisodesData{Episodes: []tvdb.Episode{
			{
				ID:           201,
				SeasonNumber: 2,
				Number:       1,
				Name:         "Example Episode One",
				Overview:     "Example overview one.",
				Aired:        "2026-04-23",
				Image:        "https://img.example/tvdb-episode-1.jpg",
			},
			{
				ID:           202,
				SeasonNumber: 2,
				Number:       2,
				Name:         "Example Episode Two",
				Overview:     "Example overview two.",
				Aired:        "2026-04-30",
				Image:        "https://img.example/tvdb-episode-2.jpg",
			},
		}},
	}

	meta := preparationstate.State{
		SourcePath: "/media/Example.Show.S02.mkv",
		SeasonInt:  2,
		TVPack:     true,
	}
	ids := &api.ExternalIdentity{
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
		TVDB: &api.TVDBMetadata{TVDBID: 200, OriginalLanguage: "eng"},
	}

	_ = svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if len(external.TVDB.Episodes) != 2 {
		t.Fatalf("expected stored TVDB season episodes, got %#v", external.TVDB.Episodes)
	}
	if external.TVDB.Episodes[0].EpisodeImage != "https://img.example/tvdb-episode-1.jpg" || external.TVDB.Episodes[1].EpisodeAired != "2026-04-30" {
		t.Fatalf("unexpected stored TVDB season episodes: %#v", external.TVDB.Episodes)
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

	meta := preparationstate.State{
		SourcePath: "/media/Example.Show.S01E03.mkv",
		SeasonInt:  1,
		EpisodeInt: 3,
	}
	ids := &api.ExternalIdentity{
		TVDBID:   200,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
		TVDB: &api.TVDBMetadata{TVDBID: 200, OriginalLanguage: "jpn"},
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, external, tmdbClient, tvdbClient, &stubTVmaze{})

	if updated.EpisodeTitle != "" {
		t.Fatalf("expected non-english original episode title to be skipped, got %q", updated.EpisodeTitle)
	}
	if updated.EpisodeOverview != "" {
		t.Fatalf("expected non-english original episode overview to be skipped, got %q", updated.EpisodeOverview)
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

	meta := preparationstate.State{
		SourcePath:   "/media/Re.ZERO.S04E11.mkv",
		SeasonInt:    4,
		EpisodeInt:   11,
		EpisodeTitle: "Re:ZERO -Starting Life in Another World-",
		Identity:     api.ExternalIdentity{Category: "TV"},
		ProviderMetadata: api.SourceScopedMetadata{
			TVDB: &api.TVDBMetadata{NameEnglish: "Re: ZERO, Starting Life in Another World"},
		},
	}
	ids := &api.ExternalIdentity{
		TVDBID:   200,
		TVmazeID: 300,
		Category: "TV",
	}
	external := &api.SourceScopedMetadata{
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

	meta := preparationstate.State{
		SourcePath: "/media/Example.Show.S01E04.mkv",
		SeasonInt:  1,
		EpisodeInt: 4,
	}
	ids := &api.ExternalIdentity{
		TVDBID:   200,
		Category: "TV",
	}

	updated := svc.applyTVEpisodeMetadata(context.Background(), meta, ids, nil, tmdbClient, tvdbClient, &stubTVmaze{})

	if updated.EpisodeTitle != "" {
		t.Fatalf("expected episode title blank when tba, got %q", updated.EpisodeTitle)
	}
}

func TestSanitizeEpisodeTitleSkipsGenericAndPlaceholderTitles(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "numeric episode",
			input: "Episode 1",
			want:  "",
		},
		{
			name:  "hash episode",
			input: "Episode #12",
			want:  "",
		},
		{
			name:  "word episode",
			input: "Episode One",
			want:  "",
		},
		{
			name:  "tba",
			input: "TBA",
			want:  "",
		},
		{
			name:  "tbd",
			input: "TBD",
			want:  "",
		},
		{
			name:  "tbc",
			input: "TBC",
			want:  "",
		},
		{
			name:  "tdc",
			input: "TDC",
			want:  "",
		},
		{
			name:  "real title containing episode",
			input: "The Episode Problem",
			want:  "The Episode Problem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeEpisodeTitle(tt.input); got != tt.want {
				t.Fatalf("sanitizeEpisodeTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
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

	meta := preparationstate.State{
		SourcePath:        "/media/Arknights.S01E01.mkv",
		MediaInfoCategory: "TV",
		SeasonInt:         1,
		EpisodeInt:        1,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID: new(200),
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if containsString(tvdbClient.seriesLangCalls, "eng") {
		t.Fatalf("expected no eng series refetch, calls=%v", tvdbClient.seriesLangCalls)
	}
	if containsString(tvdbClient.episodeLangCalls, "eng") {
		t.Fatalf("expected no eng episode refetch, calls=%v", tvdbClient.episodeLangCalls)
	}
	if len(tvdbClient.nameDisambiguationInputs) != 0 {
		t.Fatalf("expected direct metadata fetch to own disambiguation, calls=%v", tvdbClient.nameDisambiguationInputs)
	}
	if result.ProviderMetadata.TVDB == nil {
		t.Fatalf("expected tvdb metadata")
	}
	if !result.ProviderMetadata.TVDB.HasEnglish {
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

	meta := preparationstate.State{
		SourcePath:        "/media/Cats.Eye.S01E01.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID: new(200),
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ProviderMetadata.TVDB == nil {
		t.Fatalf("expected tvdb metadata")
	}
	if result.ProviderMetadata.TVDB.Year != 2010 {
		t.Fatalf("expected first-aired tvdb year 2010, got %d", result.ProviderMetadata.TVDB.Year)
	}
	if result.ProviderMetadata.TVDB.YearFromAlias {
		t.Fatalf("expected slug-derived year not to mark YearFromAlias")
	}
}

func TestMapTVDBMetadataAPIYearDoesNotBecomeNamingYear(t *testing.T) {
	mapped := mapTVDBMetadata(402296, "", tvdb.SeriesMetadata{
		TVDBID:           402296,
		Name:             "Example Spy Show",
		NameEnglish:      "Example Spy Show",
		SeriesYear:       2026,
		FirstAired:       "2026-12-08",
		OriginalLanguage: "eng",
		HasEnglish:       true,
	})

	if mapped == nil {
		t.Fatalf("expected mapped metadata")
	}
	if mapped.Year != 2026 {
		t.Fatalf("expected first-aired tvdb year 2026, got %d", mapped.Year)
	}
	if mapped.YearFromAlias {
		t.Fatalf("expected api year not to mark YearFromAlias")
	}
	if mapped.YearSource != "first_aired" {
		t.Fatalf("expected first_aired year source, got %q", mapped.YearSource)
	}
}

func TestMapTVDBMetadataCarriesNameDisambiguation(t *testing.T) {
	mapped := mapTVDBMetadata(987650001, "", tvdb.SeriesMetadata{
		TVDBID:      987650001,
		Name:        "Example Series",
		NameEnglish: "Example Series",
		Year:        2026,
		NameDisambiguation: tvdb.NameDisambiguation{
			CanonicalName:         "Example Series",
			SeriesYear:            2026,
			Locale:                "US",
			SameNameSeries:        2,
			SameNameAndYearSeries: 1,
			IncludeYear:           true,
			IncludeLocale:         true,
			Status:                api.MetadataEvidenceStatusPartial,
			Source:                "tvdb_v4_search_unpaged",
		},
	})

	if mapped == nil {
		t.Fatal("expected mapped metadata")
	}
	if mapped.Year != 2026 || mapped.YearFromAlias {
		t.Fatalf("year compatibility = year=%d alias=%t", mapped.Year, mapped.YearFromAlias)
	}
	if mapped.NameDisambiguation.Status != api.MetadataEvidenceStatusPartial ||
		mapped.NameDisambiguation.SameNameSeries != 2 ||
		mapped.NameDisambiguation.SameNameAndYearSeries != 1 ||
		!mapped.NameDisambiguation.IncludeYear ||
		!mapped.NameDisambiguation.IncludeLocale ||
		mapped.NameDisambiguation.Locale != "US" {
		t.Fatalf("unexpected mapped disambiguation: %+v", mapped.NameDisambiguation)
	}
}

func TestMergeTVDBMetadataDoesNotInventLegacyDisambiguation(t *testing.T) {
	target := &api.TVDBMetadata{
		TVDBID: 987650001,
		Name:   "Stored Example Series",
		Year:   2026,
	}
	incoming := &api.TVDBMetadata{
		TVDBID:      987650001,
		NameEnglish: "Example Series",
		Year:        2026,
	}

	mergeTVDBMetadata(target, incoming)

	if target.NameDisambiguation.Status != "" ||
		target.NameDisambiguation.IncludeYear ||
		target.NameDisambiguation.IncludeLocale {
		t.Fatalf("legacy merge invented disambiguation: %+v", target.NameDisambiguation)
	}
	if !usableTVDBMetadata(target, 987650001) {
		t.Fatal("direct TVDB metadata should remain reusable without collision evidence")
	}
	if reusableTVDBNameDisambiguation(target) {
		t.Fatal("legacy metadata without explicit evidence should refresh only collision evidence")
	}
}

func TestMergeTVDBMetadataReplacesDisambiguationAtomically(t *testing.T) {
	target := &api.TVDBMetadata{
		TVDBID: 987650001,
		Name:   "Example Series",
		NameDisambiguation: api.TVDBNameDisambiguation{
			CanonicalName: "Old Example",
			IncludeYear:   true,
			Status:        api.MetadataEvidenceStatusUnavailable,
			Source:        "old",
		},
	}
	incoming := &api.TVDBMetadata{
		TVDBID: 987650001,
		NameDisambiguation: api.TVDBNameDisambiguation{
			CanonicalName:         "Example Series",
			SeriesYear:            2026,
			Locale:                "JP",
			SameNameSeries:        1,
			SameNameAndYearSeries: 1,
			IncludeYear:           true,
			IncludeLocale:         true,
			Status:                api.MetadataEvidenceStatusPartial,
			Source:                "tvdb_v4_search_unpaged",
		},
	}

	mergeTVDBMetadata(target, incoming)

	if target.NameDisambiguation != incoming.NameDisambiguation {
		t.Fatalf("disambiguation = %+v, want %+v", target.NameDisambiguation, incoming.NameDisambiguation)
	}
	if !usableTVDBMetadata(target, 987650001) {
		t.Fatal("direct metadata should be reusable")
	}
	if !reusableTVDBNameDisambiguation(target) {
		t.Fatal("explicit collision evidence should be reusable")
	}
}

func TestTVDBMetadataAndDisambiguationReuseAreIndependent(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		status             api.MetadataEvidenceStatus
		wantDisambiguation bool
	}{
		{status: api.MetadataEvidenceStatusComplete, wantDisambiguation: true},
		{status: api.MetadataEvidenceStatusPartial, wantDisambiguation: true},
		{status: api.MetadataEvidenceStatusUnavailable},
		{status: api.MetadataEvidenceStatusContradictory},
		{},
	} {
		metadata := &api.TVDBMetadata{
			TVDBID:      987650001,
			NameEnglish: "Example Series",
			NameDisambiguation: api.TVDBNameDisambiguation{
				Status: test.status,
			},
		}
		if !usableTVDBMetadata(metadata, metadata.TVDBID) {
			t.Fatalf("direct metadata unexpectedly unusable for evidence status %q", test.status)
		}
		if got := reusableTVDBNameDisambiguation(metadata); got != test.wantDisambiguation {
			t.Fatalf("reusable disambiguation status %q = %t, want %t", test.status, got, test.wantDisambiguation)
		}
	}
}

func TestMergeTVDBMetadataClearsStaleAliasYear(t *testing.T) {
	target := &api.TVDBMetadata{
		TVDBID:         402296,
		Name:           "Example Spy Show",
		Year:           2025,
		YearFromAlias:  true,
		YearSource:     "translation_alias",
		YearConfidence: "high",
	}
	incoming := &api.TVDBMetadata{
		TVDBID:     402296,
		Name:       "Example Spy Show",
		FirstAired: "2026-12-08",
		Year:       2026,
		YearSource: "first_aired",
	}

	mergeTVDBMetadata(target, incoming)

	if target.Year != 2026 {
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

func TestMergeTVDBMetadataPreservesAliasYearWhenIncomingHasNoYearSource(t *testing.T) {
	target := &api.TVDBMetadata{
		TVDBID:         200,
		Name:           "Cats Eye",
		Year:           2025,
		YearFromAlias:  true,
		YearSource:     "translation_alias",
		YearConfidence: "high",
	}
	incoming := &api.TVDBMetadata{
		TVDBID:      200,
		NameEnglish: "Cat's Eye",
	}

	mergeTVDBMetadata(target, incoming)

	if target.Year != 2025 {
		t.Fatalf("expected alias year preserved, got %d", target.Year)
	}
	if !target.YearFromAlias {
		t.Fatal("expected YearFromAlias preserved")
	}
	if target.YearSource != "translation_alias" {
		t.Fatalf("expected alias year source preserved, got %q", target.YearSource)
	}
	if target.YearConfidence != "high" {
		t.Fatalf("expected alias confidence preserved, got %q", target.YearConfidence)
	}
	if target.NameEnglish != "Cat's Eye" {
		t.Fatalf("expected unrelated missing fields to merge, got %q", target.NameEnglish)
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

	meta := preparationstate.State{
		SourcePath:        "/media/Cats.Eye.S01E01.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID: new(200),
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ProviderMetadata.TVDB == nil {
		t.Fatalf("expected tvdb metadata")
	}
	if result.ProviderMetadata.TVDB.Year != 2025 {
		t.Fatalf("expected explicit tvdb series year 2025, got %d", result.ProviderMetadata.TVDB.Year)
	}
	if !result.ProviderMetadata.TVDB.YearFromAlias {
		t.Fatalf("expected explicit series year to mark YearFromAlias")
	}
	if result.ProviderMetadata.TVDB.YearSource != "translation_alias" {
		t.Fatalf("expected translation alias year source, got %q", result.ProviderMetadata.TVDB.YearSource)
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

	meta := preparationstate.State{
		SourcePath:        "/media/Example.Anime.S01E01.mkv",
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: new(101),
			MALID:  new(999),
		},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.MALID != 999 {
		t.Fatalf("expected mal override 999, got %d", result.MALID)
	}
	if result.Identity.MALID != 999 || result.Identity.Provenance.MAL != api.IdentityProvenanceExplicit || result.Identity.Overrides.MAL != api.OverrideStateValue {
		t.Fatalf("expected canonical mal override, got %#v", result.Identity)
	}
}

func TestResolveExternalIDsAnchoredTMDBSuppressesUnverifiedMAL(t *testing.T) {
	tmdbID := 101
	tmdbClient := &stubTMDB{
		metadata: tmdb.MetadataResult{
			TMDBType: "tv",
			MALID:    444,
			Anime:    true,
		},
	}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Anime.S01E01.mkv",
		MediaInfoCategory: "TV",
		TrackerData:       []api.TrackerMetadata{{MALID: 111}},
		SceneMALID:        222,
		MALID:             333,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Identity.MALID != 0 || result.MALID != 0 {
		t.Fatalf("expected anchored TMDB to suppress unverified MAL, got %#v", result.Identity)
	}
	for _, input := range tmdbClient.metaInputs {
		if !input.SkipAnimeLookup {
			t.Fatalf("anchored TMDB metadata input did not suppress anime lookup: %#v", input)
		}
	}
}

func TestResolveExternalIDsPreservesStoredAniListForSameMALID(t *testing.T) {
	sourcePath := "/media/Example.Anime.S01E01.mkv"
	repo := &fakeRepo{
		meta: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			AniList: &api.AniListMetadata{
				AniListID:   100,
				MALID:       200,
				TitleRomaji: "Stored Anime",
			},
		},
	}
	tmdbClient := &stubTMDB{}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: sourcePath,
		MALID:      200,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tmdbClient.anilistCalls != 0 {
		t.Fatalf("expected stored anilist reuse without fetch, got %d calls", tmdbClient.anilistCalls)
	}
	if result.ProviderMetadata.AniList == nil || result.ProviderMetadata.AniList.TitleRomaji != "Stored Anime" {
		t.Fatalf("expected stored anilist metadata preserved, got %#v", result.ProviderMetadata.AniList)
	}
	if repo.meta.AniList == nil || repo.meta.AniList.MALID != 200 {
		t.Fatalf("expected persisted anilist metadata preserved, got %#v", repo.meta.AniList)
	}
}

func TestResolveExternalIDsRefetchesAniListWhenMALIDChanges(t *testing.T) {
	sourcePath := "/media/Example.Anime.S01E02.mkv"
	staleUpdatedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	repo := &fakeRepo{
		meta: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			AniList: &api.AniListMetadata{
				AniListID:   100,
				MALID:       200,
				TitleRomaji: "Stale Anime",
			},
			UpdatedAt: staleUpdatedAt,
		},
	}
	tmdbClient := &stubTMDB{
		anilistMetadata: tmdb.AniListMetadataResult{
			AniListID:   300,
			MALID:       400,
			TitleRomaji: "Current Anime",
		},
	}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: sourcePath,
		MALID:      400,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tmdbClient.anilistCalls != 1 || len(tmdbClient.anilistInputs) != 1 || tmdbClient.anilistInputs[0] != 400 {
		t.Fatalf("expected one anilist refetch for MAL 400, got calls=%d inputs=%v", tmdbClient.anilistCalls, tmdbClient.anilistInputs)
	}
	if result.ProviderMetadata.AniList == nil || result.ProviderMetadata.AniList.MALID != 400 || result.ProviderMetadata.AniList.TitleRomaji != "Current Anime" {
		t.Fatalf("expected current anilist metadata, got %#v", result.ProviderMetadata.AniList)
	}
	if repo.externalMetaSaves != 0 {
		t.Fatal("provider candidate must not publish current AniList metadata")
	}
	if !result.ProviderMetadata.UpdatedAt.After(staleUpdatedAt) {
		t.Fatalf("expected metadata timestamp refreshed after anilist side effect, got %s", result.ProviderMetadata.UpdatedAt)
	}
	if !result.ProviderMetadata.UpdatedAt.Equal(result.Identity.ResolvedAt) {
		t.Fatalf("expected metadata and id timestamps to match, got metadata=%s ids=%s", result.ProviderMetadata.UpdatedAt, result.Identity.ResolvedAt)
	}
}

func TestResolveExternalIDsClearsStaleAniListWhenChangedMALIDHasNoResult(t *testing.T) {
	sourcePath := "/media/Example.Anime.S01E03.mkv"
	repo := &fakeRepo{
		meta: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			AniList: &api.AniListMetadata{
				AniListID:   100,
				MALID:       200,
				TitleRomaji: "Stale Anime",
			},
		},
	}
	tmdbClient := &stubTMDB{}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: sourcePath,
		MALID:      400,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tmdbClient.anilistCalls != 1 || len(tmdbClient.anilistInputs) != 1 || tmdbClient.anilistInputs[0] != 400 {
		t.Fatalf("expected one anilist refetch for MAL 400, got calls=%d inputs=%v", tmdbClient.anilistCalls, tmdbClient.anilistInputs)
	}
	if result.ProviderMetadata.AniList != nil {
		t.Fatalf("expected stale anilist metadata cleared from result, got %#v", result.ProviderMetadata.AniList)
	}
	if repo.meta.AniList == nil || repo.meta.AniList.MALID != 200 || repo.externalMetaSaves != 0 {
		t.Fatal("provider candidate must leave stored AniList metadata unchanged")
	}
}

func TestResolveExternalIDsAniListFetchErrorDoesNotPersistEmptyMetadata(t *testing.T) {
	sourcePath := "/media/Example.Anime.S01E04.mkv"
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{anilistErr: errors.New("graphql metadata error")}
	svc := NewService(repo,
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: sourcePath,
		MALID:      400,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tmdbClient.anilistCalls != 1 || len(tmdbClient.anilistInputs) != 1 || tmdbClient.anilistInputs[0] != 400 {
		t.Fatalf("expected one anilist fetch for MAL 400, got calls=%d inputs=%v", tmdbClient.anilistCalls, tmdbClient.anilistInputs)
	}
	if result.Identity.MALID != 400 || result.Identity.Provenance.MAL != api.IdentityProvenanceProvider {
		t.Fatalf("expected MAL ID retained after anilist fetch error, got %#v", result.Identity)
	}
	if result.ProviderMetadata.AniList != nil {
		t.Fatalf("expected no empty anilist metadata after fetch error, got %#v", result.ProviderMetadata.AniList)
	}
	if repo.externalMetaSaves != 0 {
		t.Fatalf("expected no empty external metadata persistence after anilist fetch error, got %d saves", repo.externalMetaSaves)
	}
}

func TestResolveExternalIDsMALFallbacksAndClear(t *testing.T) {
	tmdbID := 101
	clearMAL := 0
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		TMDBType: "tv",
		MALID:    444,
		Anime:    true,
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Anime.S01E02.mkv",
		MediaInfoCategory: "TV",
		SceneMALID:        222,
		MALID:             333,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err != nil {
		t.Fatalf("resolve scene: %v", err)
	}
	if result.Identity.MALID != 0 || len(tmdbClient.metaInputs) == 0 {
		t.Fatalf("anchored TMDB anime enrichment = identity:%#v inputs:%#v", result.Identity, tmdbClient.metaInputs)
	}
	for _, input := range tmdbClient.metaInputs {
		if !input.SkipAnimeLookup {
			t.Fatalf("anchored TMDB anime enrichment = identity:%#v inputs:%#v", result.Identity, tmdbClient.metaInputs)
		}
	}
	anchoredInputCount := len(tmdbClient.metaInputs)

	cleared, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Anime.S01E03.mkv",
		MediaInfoCategory: "TV",
		TrackerData:       []api.TrackerMetadata{{MALID: 111}},
		SceneMALID:        222,
		MALID:             333,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
			MALID:  &clearMAL,
		},
	})
	if err != nil {
		t.Fatalf("resolve clear: %v", err)
	}
	if cleared.Identity.MALID != 0 || cleared.Identity.Provenance.MAL != api.IdentityProvenanceExplicit || cleared.Identity.Overrides.MAL != api.OverrideStateClear {
		t.Fatalf("expected cleared mal lock, got %#v", cleared.Identity)
	}
	if cleared.MALID != 0 {
		t.Fatalf("expected prepared mal mirror cleared, got %d", cleared.MALID)
	}
	for _, input := range tmdbClient.metaInputs[anchoredInputCount:] {
		if !input.SkipAnimeLookup {
			t.Fatalf("cleared MAL anime enrichment inputs = %#v", tmdbClient.metaInputs)
		}
	}
	clearedInputCount := len(tmdbClient.metaInputs)

	legacy, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:        "/media/Example.Anime.S01E04.mkv",
		MediaInfoCategory: "TV",
		TrackerData:       []api.TrackerMetadata{{TMDBID: tmdbID}},
	})
	if err != nil {
		t.Fatalf("resolve legacy fallback: %v", err)
	}
	if legacy.Identity.MALID != 444 || legacy.Identity.Provenance.MAL != api.IdentityProvenanceProvider {
		t.Fatalf("legacy TMDB MAL enrichment = %#v", legacy.Identity)
	}
	for _, input := range tmdbClient.metaInputs[clearedInputCount:] {
		if input.SkipAnimeLookup {
			t.Fatalf("legacy TMDB anime enrichment inputs = %#v", tmdbClient.metaInputs)
		}
	}
}

func TestResolveExternalIDsPreservesProviderEvidenceWithOriginalLanguageOverride(t *testing.T) {
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

	meta := preparationstate.State{
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

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(tmdbClient.metaInputs) == 0 || tmdbClient.metaInputs[0].ManualLanguage != "" {
		t.Fatalf("manual correction changed TMDB evidence request: %#v", tmdbClient.metaInputs)
	}
	if imdbClient.lastManualLanguage != "" {
		t.Fatalf("manual correction changed IMDb evidence request: %q", imdbClient.lastManualLanguage)
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TMDB.OriginalLanguage != "en" {
		t.Fatalf("TMDB evidence changed: %#v", result.ProviderMetadata.TMDB)
	}
	if result.ProviderMetadata.IMDB == nil || result.ProviderMetadata.IMDB.OriginalLanguage != "en" {
		t.Fatalf("IMDb evidence changed: %#v", result.ProviderMetadata.IMDB)
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

func TestResolveExternalIDsSelectedDemandsShareLocalizedFetchWhenTMDBMetadataIsNil(t *testing.T) {
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
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	// Multiple selected trackers demand the same provider fact without supplying
	// tracker evidence or leaking tracker selection into source instructions.
	meta := preparationstate.State{
		SourcePath: "/media/file.mkv",
		Release:    api.ReleaseInfo{Title: "Example", Year: 2024},
		MetadataRequirements: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{
			{Scope: api.MetadataRequirementScopeAny, AnyOf: []api.MetadataRequirementField{"tmdb_localized_pt_br"}},
			{Scope: api.MetadataRequirementScopeMovie, AnyOf: []api.MetadataRequirementField{"tmdb_localized_pt_br"}},
		}},
	}

	result, err := svc.resolveExternalIdentity(context.Background(), meta)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.ProviderMetadata.TMDB == nil {
		t.Fatalf("expected TMDB metadata to be initialized with localized info")
	}
	if result.ProviderMetadata.TMDB.Localized == nil {
		t.Fatalf("expected Localized map to be populated")
	}
	ptBR, ok := result.ProviderMetadata.TMDB.Localized["pt-BR"]
	if !ok || ptBR.Title != "Título Localizado" {
		t.Fatalf("expected localized title, got %#v", ptBR)
	}
	if len(tmdbClient.localizedInputs) != 1 || tmdbClient.localizedInputs[0].AppendToResponse != "credits,videos,release_dates" {
		t.Fatalf("expected movie localized fetch to append release_dates, got %#v", tmdbClient.localizedInputs)
	}
}

func TestResolveExternalIDsRefreshesRetainedTMDBForSelectedDemands(t *testing.T) {
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:            "Refreshed Example",
		TMDBType:         "Movie",
		OriginCountry:    []string{"US"},
		Genres:           "Drama",
		OriginalLanguage: "en",
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:      "/media/Example.Movie.2026.1080p-GRP.mkv",
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: "/media/Example.Movie.2026.1080p-GRP.mkv",
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     42,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "/media/Example.Movie.2026.1080p-GRP.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID:   42,
				Category: "movie",
				Title:    "Retained Example",
			},
		},
		MetadataRequirements: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{
			{Scope: api.MetadataRequirementScopeAny, AnyOf: []api.MetadataRequirementField{"tmdb_origin_countries"}},
			{Scope: api.MetadataRequirementScopeMovie, AnyOf: []api.MetadataRequirementField{"tmdb_origin_countries"}},
			{Scope: api.MetadataRequirementScopeAny, AnyOf: []api.MetadataRequirementField{"genres", "original_language"}},
		}},
	})
	if err != nil {
		t.Fatalf("resolve external identity: %v", err)
	}
	if tmdbClient.metaCalls != 1 {
		t.Fatalf("TMDB metadata calls = %d, want 1", tmdbClient.metaCalls)
	}
	if result.ProviderMetadata.TMDB == nil || !containsString(result.ProviderMetadata.TMDB.OriginCountry, "US") ||
		result.ProviderMetadata.TMDB.Genres != "Drama" || result.ProviderMetadata.TMDB.OriginalLanguage != "en" {
		t.Fatalf("refreshed TMDB metadata = %#v", result.ProviderMetadata.TMDB)
	}
}

func TestResolveExternalIDsRefreshesRetainedTVDBYearForSelectedDemand(t *testing.T) {
	tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
		TVDBID: 7,
		Name:   "Refreshed Series",
		Year:   2024,
	}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(&stubTMDB{}),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(tvdbClient),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:        "/media/Example.Show.S01E01.1080p-GRP.mkv",
		StoredDataFresh:   true,
		MediaInfoCategory: "TV",
		Identity: api.ExternalIdentity{
			SourcePath: "/media/Example.Show.S01E01.1080p-GRP.mkv",
			Category:   api.CanonicalCategoryTV,
			TVDBID:     7,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "/media/Example.Show.S01E01.1080p-GRP.mkv",
			TVDB:       &api.TVDBMetadata{TVDBID: 7, Name: "Retained Series"},
		},
		MetadataRequirements: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
			Scope: api.MetadataRequirementScopeTV,
			AnyOf: []api.MetadataRequirementField{"tvdb_year"},
		}}},
	})
	if err != nil {
		t.Fatalf("resolve external identity: %v", err)
	}
	if tvdbClient.seriesMetadataCalls != 1 {
		t.Fatalf("TVDB series metadata calls = %d, want 1", tvdbClient.seriesMetadataCalls)
	}
	if result.ProviderMetadata.TVDB == nil || result.ProviderMetadata.TVDB.Year != 2024 {
		t.Fatalf("refreshed TVDB metadata = %#v", result.ProviderMetadata.TVDB)
	}
}

func TestResolveExternalIDsSkipsSelectedDemandRefreshForCompleteOrManualFacts(t *testing.T) {
	genres := []string{"Drama"}
	originalLanguage := "ja"
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Unexpected refresh", TMDBType: "Movie"}}
	svc := NewService(&fakeRepo{},
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	_, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:      "/media/Example.Movie.2026.1080p-GRP.mkv",
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: "/media/Example.Movie.2026.1080p-GRP.mkv",
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     42,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "/media/Example.Movie.2026.1080p-GRP.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID:           42,
				Category:         "movie",
				Title:            "Retained Example",
				OriginCountry:    []string{"US"},
				OriginalLanguage: "",
			},
		},
		MetadataOverrides: api.MetadataOverrides{
			Genres:           &genres,
			OriginalLanguage: &originalLanguage,
		},
		MetadataRequirements: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{
			{Scope: api.MetadataRequirementScopeAny, AnyOf: []api.MetadataRequirementField{"tmdb_origin_countries"}},
			{Scope: api.MetadataRequirementScopeAny, AnyOf: []api.MetadataRequirementField{"genres"}},
			{Scope: api.MetadataRequirementScopeAny, AnyOf: []api.MetadataRequirementField{"original_language"}},
		}},
	})
	if err != nil {
		t.Fatalf("resolve external identity: %v", err)
	}
	if tmdbClient.metaCalls != 0 {
		t.Fatalf("TMDB metadata calls = %d, want 0", tmdbClient.metaCalls)
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
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:       "/media/file.mkv",
		Release:          api.ReleaseInfo{Title: "Example", Year: 2024},
		EvidenceTrackers: []string{"BJS"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.ProviderMetadata.TMDB != nil && result.ProviderMetadata.TMDB.Localized != nil {
		if got, ok := result.ProviderMetadata.TMDB.Localized["pt-BR"]; ok {
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
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:       "/media/show.s01e02.mkv",
		Release:          api.ReleaseInfo{Title: "Example", Year: 2024},
		SeasonInt:        1,
		EpisodeInt:       2,
		EvidenceTrackers: []string{"BJS"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	got := result.ProviderMetadata.TMDB.Localized["pt-BR"]
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
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:       "/media/show.s01e02.mkv",
		Release:          api.ReleaseInfo{Title: "Example", Year: 2024},
		SeasonInt:        1,
		EpisodeInt:       2,
		EvidenceTrackers: []string{"BJS"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.ProviderMetadata.TMDB != nil && result.ProviderMetadata.TMDB.Localized != nil {
		if got, ok := result.ProviderMetadata.TMDB.Localized["pt-BR"]; ok {
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
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
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

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:       "/media/file.mkv",
		StoredDataFresh:  true,
		Release:          api.ReleaseInfo{Title: "Example", Year: 2024},
		EvidenceTrackers: []string{"BJS"},
		Identity: api.ExternalIdentity{
			SourcePath: "/media/file.mkv",
			TMDBID:     42,
			Category:   "MOVIE",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "/media/file.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID:   42,
				Category: "movie",
				Localized: map[string]api.TMDBLocalizedData{
					"pt-BR": existing,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := result.ProviderMetadata.TMDB.Localized["pt-BR"]
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
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
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

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:       "/media/show.s01e02.mkv",
		StoredDataFresh:  true,
		Release:          api.ReleaseInfo{Title: "Example", Year: 2024},
		SeasonInt:        1,
		EpisodeInt:       2,
		EvidenceTrackers: []string{"BJS"},
		Identity: api.ExternalIdentity{
			SourcePath: "/media/show.s01e02.mkv",
			TMDBID:     42,
			Category:   "TV",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "/media/show.s01e02.mkv",
			TMDB: &api.TMDBMetadata{
				TMDBID:   42,
				Category: "tv",
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
	got := result.ProviderMetadata.TMDB.Localized["pt-BR"]
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
		WithTrackerRegistry(localizedMetadataTestRegistry(t)),
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	_, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath:       "/media/show.mkv",
		Release:          api.ReleaseInfo{Title: "Example", Year: 2024},
		EvidenceTrackers: []string{"BJS"},
		SeasonInt:        1,
		EpisodeInt:       2,
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

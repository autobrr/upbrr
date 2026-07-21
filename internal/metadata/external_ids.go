// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"golang.org/x/sync/errgroup"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/metadata/thexem"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	searchYearPattern      = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	leadingSearchYearTitle = regexp.MustCompile(`^\s*(19\d{2}|20\d{2})\s*[-:._]\s*(.+?)\s*$`)
	searchBracketNoise     = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
	searchInnerWhitespace  = regexp.MustCompile(`\s+`)
	tvdbAliasYearPattern   = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	tvdbAliasYearCleanup   = regexp.MustCompile(`\s*\(?\b(?:19\d{2}|20\d{2})\b\)?\s*`)
	tvPathHintPattern      = regexp.MustCompile(`(?i)[\\/](tv|tvshows?|series)[\\/]`)
	tvNameHintPattern      = regexp.MustCompile(
		`(?i)\bS\d{1,2}(?:E\d{1,3})?\b|\b\d{1,2}x\d{2,3}\b|\b(?:season|series)\s*\d+\b|\b(19\d{2}|20\d{2})[.-]\d{2}[.-]\d{2}\b`,
	)
	subsPleaseHintPattern = regexp.MustCompile(`(?i)subsplease`)
	animeEpisodeHint      = regexp.MustCompile(`(?i)-\s*\d{1,3}\s*\(1080p\)`)
	genericEpisodePattern = regexp.MustCompile(
		`(?i)^episode\s*#?\s*(?:\d+|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty)\s*$`,
	)
	placeholderTitlePattern = regexp.MustCompile(`(?i)^(?:tba|tbd|tbc|tdc)$`)
)

// TMDBClient is the TMDB metadata surface used by external-ID resolution.
type TMDBClient interface {
	FindByExternalID(ctx context.Context, input tmdb.FindInput) (tmdb.FindResult, error)
	SearchID(ctx context.Context, input tmdb.SearchInput) (tmdb.SearchOutcome, error)
	FetchMetadata(ctx context.Context, input tmdb.MetadataInput) (tmdb.MetadataResult, error)
	GetEpisodeDetails(ctx context.Context, tmdbID, season, episode int) (tmdb.EpisodeDetails, error)
	GetSeasonDetails(ctx context.Context, tmdbID, season int) (tmdb.SeasonDetails, error)
	DailyToSeasonEpisode(ctx context.Context, tmdbID int, date time.Time) (int, int, error)
	GetLocalizedData(ctx context.Context, input tmdb.LocalizedDataInput) (map[string]any, error)
}

// AniListClient is the keyless AniList metadata surface used by external-ID resolution.
type AniListClient interface {
	FetchAniListMetadata(ctx context.Context, malID int) (tmdb.AniListMetadataResult, error)
}

// IMDBClient is the IMDb metadata surface used by external-ID resolution.
type IMDBClient interface {
	Search(ctx context.Context, input imdb.SearchInput) (imdb.SearchResult, error)
	GetInfo(ctx context.Context, imdbID string, manualLanguage string, debug bool) (imdb.Info, error)
}

// imdbEpisodeClient resolves an episode-level IMDb title to its parent series.
type imdbEpisodeClient interface {
	GetEpisodeInfo(ctx context.Context, imdbID string, debug bool) (imdb.EpisodeLookup, error)
}

// TVDBClient is the TVDB metadata surface used by external-ID resolution.
type TVDBClient interface {
	GetByExternalID(ctx context.Context, imdbID, tmdbID string, tvMovie bool) (int, string, error)
	GetSeriesMetadata(ctx context.Context, seriesID int) (tvdb.SeriesMetadata, error)
	GetSeriesMetadataWithLanguage(ctx context.Context, seriesID int, language string) (tvdb.SeriesMetadata, error)
	GetEpisodes(ctx context.Context, seriesID int, query tvdb.EpisodeQuery) (tvdb.EpisodesData, string, error)
	GetEpisodesWithLanguage(ctx context.Context, seriesID int, query tvdb.EpisodeQuery, language string) (tvdb.EpisodesData, string, error)
	GetEpisodeTranslation(ctx context.Context, episodeID int, language string) (tvdb.EpisodeTranslation, error)
}

// TVmazeClient is the TVmaze metadata surface used by external-ID resolution.
type TVmazeClient interface {
	Search(ctx context.Context, input tvmaze.SearchInput) (tvmaze.SearchResult, error)
	GetEpisodeByNumber(ctx context.Context, tvmazeID, season, episode int, lookup tvmaze.EpisodeLookupContext) (*tvmaze.EpisodeData, error)
	GetEpisodeByDate(ctx context.Context, tvmazeID int, airdate string) (*tvmaze.EpisodeData, error)
}

type externalIdentityCandidateEvidence struct {
	TMDB []api.ExternalIdentityCandidate
	IMDB []api.ExternalIdentityCandidate
}

// collectExternalIdentityEvidence resolves provider evidence without publishing
// identity or provider metadata. The prepared-release owner commits the
// candidate with the complete generation.
func (s *Service) collectExternalIdentityEvidence(ctx context.Context, meta preparationstate.State) (preparationstate.State, error) {
	return s.collectProviderIdentityCandidate(ctx, meta)
}

func (s *Service) collectProviderIdentityCandidate(ctx context.Context, meta preparationstate.State) (preparationstate.State, error) {
	select {
	case <-ctx.Done():
		return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if s.repo == nil {
		return preparationstate.State{}, internalerrors.ErrInvalidInput
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return preparationstate.State{}, internalerrors.ErrInvalidInput
	}

	ids := api.ExternalIdentity{SourcePath: meta.SourcePath}
	if meta.StoredDataFresh && sourceScopedMetadataMatches(meta.Identity.SourcePath, meta.SourcePath) {
		ids = meta.Identity
		if strings.TrimSpace(ids.SourcePath) == "" {
			ids.SourcePath = meta.SourcePath
		}
	}
	metadata := api.SourceScopedMetadata{SourcePath: meta.SourcePath}
	if meta.StoredDataFresh && sourceScopedMetadataMatches(meta.ProviderMetadata.SourcePath, meta.SourcePath) {
		metadata = meta.ProviderMetadata
		if strings.TrimSpace(metadata.SourcePath) == "" {
			metadata.SourcePath = meta.SourcePath
		}
	} else {
		storedMeta, err := s.repo.GetExternalMetadata(ctx, meta.SourcePath)
		if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
			return preparationstate.State{}, fmt.Errorf("metadata: load stored external metadata: %w", err)
		}
		if err == nil {
			metadata.Bluray = storedMeta.Bluray
			metadata.AniList = storedMeta.AniList
		}
	}
	candidates := externalIdentityCandidateEvidence{}
	categoryPref := resolveCategoryPreference(meta)
	if categoryPref != "" {
		ids.Category, _ = api.NormalizeCanonicalCategory(categoryPref)
	}
	tmdbCategoryPref := normalizeCategory(string(ids.Category))
	if s.logger != nil {
		s.logger.Debugf("metadata: external ids start path=%q category=%q", meta.SourcePath, ids.Category)
	}

	overrideTMDB, clearedTMDB := applyOverrideID(&ids.TMDBID, &ids.Provenance.TMDB, &ids.Overrides.TMDB, meta.ExternalIDOverrides.TMDBID)
	overrideIMDB, clearedIMDB := applyOverrideID(&ids.IMDBID, &ids.Provenance.IMDB, &ids.Overrides.IMDB, meta.ExternalIDOverrides.IMDBID)
	overrideTVDB, clearedTVDB := applyOverrideID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Overrides.TVDB, meta.ExternalIDOverrides.TVDBID)
	overrideTVmaze, _ := applyOverrideID(&ids.TVmazeID, &ids.Provenance.TVmaze, &ids.Overrides.TVmaze, meta.ExternalIDOverrides.TVmazeID)
	overrideMAL, clearedMAL := applyOverrideID(&ids.MALID, &ids.Provenance.MAL, &ids.Overrides.MAL, meta.ExternalIDOverrides.MALID)

	trackerTMDB, trackerIMDB, trackerTVDB, trackerMAL := resolveTrackerIDs(meta.TrackerData)
	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.Provenance.TMDB, trackerTMDB, "tracker")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, trackerIMDB, "tracker")
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.Provenance.TVDB, trackerTVDB, "tracker")
	}
	if !overrideMAL && !clearedMAL {
		applyResolvedID(&ids.MALID, &ids.Provenance.MAL, trackerMAL, "tracker")
	}

	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.Provenance.TMDB, meta.MediaInfoTMDBID, "mediainfo")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, meta.MediaInfoIMDBID, "mediainfo")
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.Provenance.TVDB, meta.MediaInfoTVDBID, "mediainfo")
	}

	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.Provenance.TMDB, meta.SceneTMDBID, "scene")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, meta.SceneIMDB, "scene")
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.Provenance.TVDB, meta.SceneTVDBID, "scene")
	}
	if !overrideTVmaze {
		applyResolvedID(&ids.TVmazeID, &ids.Provenance.TVmaze, meta.SceneTVmazeID, "scene")
	}
	if !overrideMAL && !clearedMAL {
		applyResolvedID(&ids.MALID, &ids.Provenance.MAL, meta.SceneMALID, "scene")
	}
	if !overrideMAL && !clearedMAL {
		applyResolvedID(&ids.MALID, &ids.Provenance.MAL, meta.MALID, "prepared")
	}
	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.Provenance.TMDB, meta.ArrTMDBID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, meta.ArrIMDBID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.Provenance.TVDB, meta.ArrTVDBID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if !overrideTVmaze {
		applyResolvedID(&ids.TVmazeID, &ids.Provenance.TVmaze, meta.ArrTVmazeID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if s.logger != nil {
		s.logger.Debugf(
			"metadata: external ids initial tmdb=%d(%s) imdb=%d(%s) tvdb=%d(%s)",
			ids.TMDBID,
			ids.Provenance.TMDB,
			ids.IMDBID,
			ids.Provenance.IMDB,
			ids.TVDBID,
			ids.Provenance.TVDB,
		)
	}
	invalidateMismatchedProviderMetadata(&metadata, ids)

	tmdbClient, anilistClient, imdbClient, tvdbClient, tvmazeClient := s.ensureExternalClients()

	filename, secondary := resolveSearchTitles(meta)
	year := resolveSearchYear(meta)
	manualLanguage := ""
	if meta.MetadataOverrides.OriginalLanguage != nil {
		manualLanguage = strings.TrimSpace(*meta.MetadataOverrides.OriginalLanguage)
	}
	unattendedSearch := isUnattendedMetadataSearch(meta)
	if tmdbClient != nil && !overrideTMDB && ids.TMDBID == 0 && ids.IMDBID != 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: external ids lookup tmdb from imdb=%d", ids.IMDBID)
		}
		result, err := tmdbClient.FindByExternalID(ctx, tmdb.FindInput{
			IMDbID:             formatIMDbID(ids.IMDBID),
			TVDBID:             ids.TVDBID,
			SearchYear:         year,
			Filename:           filename,
			CategoryPreference: tmdbCategoryPref,
			IMDbInfo: &tmdb.IMDbInfo{
				Title:         filename,
				OriginalTitle: secondary,
				Year:          year,
			},
			Unattended: unattendedSearch,
			Debug:      false,
		})
		if err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: tmdb external lookup failed: %v", err)
			}
		} else if result.TMDBID != 0 {
			applyResolvedID(&ids.TMDBID, &ids.Provenance.TMDB, result.TMDBID, "tmdb_external")
			if ids.Category == "" && result.Category != "" {
				ids.Category, _ = api.NormalizeCanonicalCategory(result.Category)
			}
		}
		if len(result.Candidates) > 0 && len(candidates.TMDB) == 0 {
			candidates.TMDB = mapTMDBCandidates(result.Candidates, result.Category)
		}
	}

	needsTMDBSearch := tmdbClient != nil && !overrideTMDB && ids.TMDBID == 0
	needsIMDBSearch := !overrideIMDB && ids.IMDBID == 0

	if needsTMDBSearch || needsIMDBSearch {
		searchYears := buildSearchYears(year)
		if s.logger != nil {
			s.logger.Debugf(
				"metadata: external ids search filename=%q year=%d stages=%v unattended=%t category=%q tmdb=%t imdb=%t",
				filename,
				year,
				searchYears,
				unattendedSearch,
				tmdbCategoryPref,
				needsTMDBSearch,
				needsIMDBSearch,
			)
		}

		for _, stageYear := range searchYears {
			if ids.TMDBID != 0 && ids.IMDBID != 0 {
				break
			}

			stageNeedsTMDB := tmdbClient != nil && !overrideTMDB && ids.TMDBID == 0
			stageNeedsIMDB := !overrideIMDB && ids.IMDBID == 0
			if !stageNeedsTMDB && !stageNeedsIMDB {
				break
			}

			if s.logger != nil {
				s.logger.Debugf(
					"metadata: external ids search stage year=%d tmdb_needed=%t imdb_needed=%t",
					stageYear,
					stageNeedsTMDB,
					stageNeedsIMDB,
				)
			}

			var tmdbOutcome tmdb.SearchOutcome
			var imdbResult imdb.SearchResult
			var tmdbErr error
			var imdbErr error
			var mu sync.Mutex

			group, gctx := errgroup.WithContext(ctx)
			if stageNeedsTMDB {
				group.Go(func() error {
					outcome, err := tmdbClient.SearchID(gctx, tmdb.SearchInput{
						Filename:       filename,
						SecondaryTitle: secondary,
						SearchYear:     stageYear,
						Category:       tmdbCategoryPref,
						Unattended:     unattendedSearch,
						DontSwitch:     strings.EqualFold(tmdbCategoryPref, "TV"),
						Debug:          false,
					})
					if err != nil {
						mu.Lock()
						tmdbErr = err
						mu.Unlock()
						return nil
					}
					mu.Lock()
					tmdbOutcome = outcome
					mu.Unlock()
					return nil
				})
			}
			if stageNeedsIMDB {
				group.Go(func() error {
					result, err := imdbClient.Search(gctx, imdb.SearchInput{
						Filename:          filename,
						SecondaryTitle:    secondary,
						UntouchedFilename: pathutil.Base(meta.SourcePath),
						SearchYear:        stageYear,
						Category:          string(ids.Category),
						Unattended:        unattendedSearch,
						Debug:             false,
					})
					if err != nil {
						mu.Lock()
						imdbErr = err
						mu.Unlock()
						return nil
					}
					mu.Lock()
					imdbResult = result
					mu.Unlock()
					return nil
				})
			}
			_ = group.Wait()

			if tmdbErr != nil && s.logger != nil {
				s.logger.Warnf("metadata: tmdb search failed (year=%d): %v", stageYear, tmdbErr)
			}
			if imdbErr != nil && s.logger != nil {
				s.logger.Warnf("metadata: imdb search failed (year=%d): %v", stageYear, imdbErr)
			}

			if stageNeedsTMDB && tmdbOutcome.TMDBID != 0 {
				applyResolvedID(&ids.TMDBID, &ids.Provenance.TMDB, tmdbOutcome.TMDBID, "tmdb_search")
				if ids.Category == "" && tmdbOutcome.Category != "" {
					ids.Category, _ = api.NormalizeCanonicalCategory(tmdbOutcome.Category)
				}
			}
			if stageNeedsTMDB && len(tmdbOutcome.Candidates) > 0 && len(candidates.TMDB) == 0 {
				candidates.TMDB = mapTMDBCandidates(tmdbOutcome.Candidates, tmdbOutcome.Category)
			}
			if stageNeedsIMDB && imdbResult.IMDbID != 0 {
				applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, imdbResult.IMDbID, "imdb_search")
			}
			if stageNeedsIMDB && len(imdbResult.Candidates) > 0 && len(candidates.IMDB) == 0 {
				candidates.IMDB = mapIMDBCandidates(imdbResult.Candidates)
			}
			if s.logger != nil {
				s.logger.Debugf(
					"metadata: external ids search stage result year=%d tmdb=%d imdb=%d",
					stageYear,
					tmdbOutcome.TMDBID,
					imdbResult.IMDbID,
				)
			}
		}
	}

	var tmdbErr error
	var imdbErr error
	var tvdbErr error
	var tvmazeErr error
	var anilistErr error
	tvdbName := ""

	isTVForTVmaze := func() bool {
		return shouldUseTVDBForCategory(meta, ids)
	}

	tmdbLogoFetchAttempted := false
	anilistFetchAttempted := false
	shouldFetchTMDBMetadata := func() bool {
		if tmdbClient == nil || ids.TMDBID == 0 {
			return false
		}
		if !usableTMDBMetadata(metadata.TMDB, ids.TMDBID) {
			return true
		}
		return s.cfg.Description.AddLogo && strings.TrimSpace(metadata.TMDB.Logo) == "" && !tmdbLogoFetchAttempted
	}
	shouldFetchIMDBMetadata := func() bool {
		return imdbClient != nil && ids.IMDBID != 0 && !usableIMDBMetadata(metadata.IMDB, ids.IMDBID)
	}
	shouldFetchTVDBMetadata := func() bool {
		return tvdbClient != nil && shouldUseTVDBForCategory(meta, ids) && ids.TVDBID != 0 && !usableTVDBMetadata(metadata.TVDB, ids.TVDBID)
	}
	shouldFetchTVmazeMetadata := func() bool {
		return tvmazeClient != nil && isTVForTVmaze() && ids.TVmazeID != 0 && !usableTVmazeMetadata(metadata.TVmaze, ids.TVmazeID)
	}

	shouldRunFetchPass := func() bool {
		if shouldFetchTMDBMetadata() {
			return true
		}
		if anilistClient != nil && shouldFetchAniListMetadata(ids.MALID, metadata.AniList) && !anilistFetchAttempted {
			return true
		}
		if shouldFetchIMDBMetadata() || shouldFetchTVDBMetadata() || shouldFetchTVmazeMetadata() {
			return true
		}
		if shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && (ids.IMDBID != 0 || ids.TMDBID != 0) {
			return true
		}
		if isTVForTVmaze() && (shouldFetchTVmazeMetadata() || (!overrideTVmaze && ids.TVmazeID == 0 && (ids.IMDBID != 0 || ids.TVDBID != 0))) {
			return true
		}
		return false
	}

	runFetchPass := func(allowTVmazeNameFallback bool) {
		fetchTMDB := shouldFetchTMDBMetadata()
		if fetchTMDB && s.cfg.Description.AddLogo {
			tmdbLogoFetchAttempted = true
		}
		fetchAniList := anilistClient != nil && shouldFetchAniListMetadata(ids.MALID, metadata.AniList) && !anilistFetchAttempted
		if fetchAniList {
			anilistFetchAttempted = true
		}
		fetchIMDB := shouldFetchIMDBMetadata()
		lookupTVDB := shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && (ids.IMDBID != 0 || ids.TMDBID != 0)
		lookupTVmaze := isTVForTVmaze() && (shouldFetchTVmazeMetadata() || (!overrideTVmaze && ids.TVmazeID == 0 && (ids.IMDBID != 0 || ids.TVDBID != 0)))

		if s.logger != nil {
			s.logger.Debugf(
				"metadata: external ids fetch tmdb=%t imdb=%t tvdb=%t tvmaze=%t anilist=%t tvmaze_name_fallback=%t",
				fetchTMDB,
				fetchIMDB,
				lookupTVDB,
				lookupTVmaze,
				fetchAniList,
				allowTVmazeNameFallback,
			)
		}

		var tmdbResult *tmdb.MetadataResult
		var anilistResult *tmdb.AniListMetadataResult
		var imdbInfo *imdb.Info
		var fetchedTVDBID int
		var fetchedTVDBName string
		var tvmazeResult *tvmaze.SearchResult
		var mu sync.Mutex

		group, gctx := errgroup.WithContext(ctx)
		if fetchTMDB {
			group.Go(func() error {
				result, err := tmdbClient.FetchMetadata(gctx, tmdb.MetadataInput{
					TMDBID:         ids.TMDBID,
					Category:       string(ids.Category),
					SearchYear:     year,
					IMDbID:         ids.IMDBID,
					TVDBID:         ids.TVDBID,
					ManualLanguage: manualLanguage,
					AddLogo:        s.cfg.Description.AddLogo,
					LogoLanguages:  descriptionLogoLanguages(s.cfg.Description.LogoLanguage),
					Filename:       filename,
					Debug:          false,
				})
				if err != nil {
					mu.Lock()
					if tmdbErr == nil {
						tmdbErr = err
					}
					mu.Unlock()
					return nil
				}
				mu.Lock()
				tmp := result
				tmdbResult = &tmp
				mu.Unlock()
				return nil
			})
		}

		if fetchAniList {
			group.Go(func() error {
				result, err := anilistClient.FetchAniListMetadata(gctx, ids.MALID)
				if err != nil {
					mu.Lock()
					if anilistErr == nil {
						anilistErr = err
					}
					mu.Unlock()
					return nil
				}
				if result.MALID != 0 || result.AniListID != 0 {
					mu.Lock()
					tmp := result
					anilistResult = &tmp
					mu.Unlock()
				}
				return nil
			})
		}

		if fetchIMDB {
			group.Go(func() error {
				result, err := imdbClient.GetInfo(gctx, formatIMDbID(ids.IMDBID), manualLanguage, false)
				if err != nil {
					mu.Lock()
					if imdbErr == nil {
						imdbErr = err
					}
					mu.Unlock()
					return nil
				}
				mu.Lock()
				imdbInfo = &result
				mu.Unlock()
				return nil
			})
		}

		if lookupTVDB {
			group.Go(func() error {
				tvMovie := isIMDbTVMovie(ids, metadata)
				id, name, err := tvdbClient.GetByExternalID(gctx, formatIMDbID(ids.IMDBID), formatOptionalInt(ids.TMDBID), tvMovie)
				if err != nil {
					mu.Lock()
					if tvdbErr == nil {
						tvdbErr = err
					}
					mu.Unlock()
					return nil
				}
				if id != 0 {
					mu.Lock()
					fetchedTVDBID = id
					fetchedTVDBName = name
					mu.Unlock()
				}
				return nil
			})
		} else if s.logger != nil && ids.TVDBID != 0 {
			s.logger.Debugf("metadata: external ids tvdb lookup skipped id=%d source=%s", ids.TVDBID, ids.Provenance.TVDB)
		}

		if lookupTVmaze {
			group.Go(func() error {
				yearText := ""
				if year > 0 {
					yearText = strconv.Itoa(year)
				}
				manualDate := ""
				if meta.ReleaseNameOverrides.ManualDate != nil {
					manualDate = strings.TrimSpace(*meta.ReleaseNameOverrides.ManualDate)
				}
				result, err := tvmazeClient.Search(gctx, tvmaze.SearchInput{
					Filename:          filename,
					Year:              yearText,
					ImdbID:            formatIMDbID(ids.IMDBID),
					TVDBID:            formatOptionalInt(ids.TVDBID),
					ManualID:          ids.TVmazeID,
					ManualDate:        manualDate,
					StrictIDOnly:      !allowTVmazeNameFallback,
					AllowNameFallback: allowTVmazeNameFallback,
					Debug:             false,
				})
				if err != nil {
					mu.Lock()
					if tvmazeErr == nil {
						tvmazeErr = err
					}
					mu.Unlock()
					return nil
				}
				if result.SelectedID != 0 {
					mu.Lock()
					tmp := result
					tvmazeResult = &tmp
					mu.Unlock()
				}
				return nil
			})
		} else if s.logger != nil && ids.TVmazeID != 0 {
			s.logger.Debugf("metadata: external ids tvmaze lookup skipped id=%d source=%s", ids.TVmazeID, ids.Provenance.TVmaze)
		}

		_ = group.Wait()

		if tmdbResult != nil {
			metadata.TMDB = mapTMDBMetadata(ids, *tmdbResult)
			if !overrideIMDB && ids.IMDBID == 0 && tmdbResult.IMDbID != 0 {
				applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, tmdbResult.IMDbID, "tmdb")
			}
			if ids.Category == "" && tmdbResult.TMDBType != "" {
				ids.Category, _ = api.NormalizeCanonicalCategory(tmdbResult.TMDBType)
			}
			if shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && tmdbResult.TVDBID != 0 {
				applyResolvedID(&ids.TVDBID, &ids.Provenance.TVDB, tmdbResult.TVDBID, "tmdb")
			}
			if !overrideMAL && !clearedMAL && ids.MALID == 0 && tmdbResult.MALID != 0 {
				applyResolvedID(&ids.MALID, &ids.Provenance.MAL, tmdbResult.MALID, "tmdb")
			}
		}

		if anilistResult != nil {
			metadata.AniList = mapAniListMetadata(*anilistResult)
		} else if fetchAniList {
			if metadata.AniList != nil {
				metadata.AniList = nil
			}
		}

		if imdbInfo != nil {
			metadata.IMDB = mapIMDBMetadata(*imdbInfo)
		}

		if fetchedTVDBID != 0 {
			if !overrideTVDB {
				applyResolvedID(&ids.TVDBID, &ids.Provenance.TVDB, fetchedTVDBID, "tvdb")
			}
			if metadata.TVDB == nil || metadata.TVDB.TVDBID == 0 {
				metadata.TVDB = &api.TVDBMetadata{TVDBID: fetchedTVDBID, Name: fetchedTVDBName}
			}
			if strings.TrimSpace(tvdbName) == "" {
				tvdbName = fetchedTVDBName
			}
		}

		if shouldUseTVDBForCategory(meta, ids) && ids.TVDBID != 0 {
			tvdbSeries, err := tvdbClient.GetSeriesMetadataWithLanguage(ctx, ids.TVDBID, "")
			if err != nil {
				tvdbSeries, err = tvdbClient.GetSeriesMetadata(ctx, ids.TVDBID)
				if err != nil {
					if tvdbErr == nil {
						tvdbErr = err
					}
				}
			}
			if err == nil {
				mapped := mapTVDBMetadata(ids.TVDBID, fetchedTVDBName, tvdbSeries)
				if metadata.TVDB == nil {
					metadata.TVDB = mapped
				} else {
					mergeTVDBMetadata(metadata.TVDB, mapped)
				}
				if s.logger != nil && metadata.TVDB != nil {
					namingYear := 0
					if metadata.TVDB.YearFromAlias {
						namingYear = metadata.TVDB.Year
					}
					s.logger.Tracef(
						"metadata: tvdb year resolved id=%d first_aired_year=%d naming_year=%d naming_year_source=%q naming_year_confidence=%q naming_eligible=%t",
						metadata.TVDB.TVDBID,
						parseYearFromDate(metadata.TVDB.FirstAired),
						namingYear,
						metadata.TVDB.YearSource,
						metadata.TVDB.YearConfidence,
						metadata.TVDB.YearFromAlias,
					)
				}
				if strings.TrimSpace(tvdbName) == "" {
					tvdbName = strings.TrimSpace(mapped.Name)
				}
			} else if tvdbErr == nil {
				tvdbErr = err
			}
		}

		if tvmazeResult != nil {
			if !overrideTVmaze {
				applyResolvedID(&ids.TVmazeID, &ids.Provenance.TVmaze, tvmazeResult.SelectedID, "tvmaze")
			}
			if !overrideIMDB && ids.IMDBID == 0 && tvmazeResult.IMDBID != 0 {
				applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, tvmazeResult.IMDBID, "tvmaze")
			}
			if shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && tvmazeResult.TVDBID != 0 {
				applyResolvedID(&ids.TVDBID, &ids.Provenance.TVDB, tvmazeResult.TVDBID, "tvmaze")
			}
			metadata.TVmaze = mapTVmazeMetadata(*tvmazeResult)
		}

		if manualLanguage != "" {
			if metadata.TMDB != nil {
				metadata.TMDB.OriginalLanguage = manualLanguage
			}
			if metadata.IMDB != nil {
				metadata.IMDB.OriginalLanguage = manualLanguage
			}
			if metadata.TVDB != nil {
				metadata.TVDB.OriginalLanguage = manualLanguage
			}
			if metadata.TVmaze != nil {
				metadata.TVmaze.Language = manualLanguage
			}
		}
	}

	if shouldRunFetchPass() {
		runFetchPass(false)
	}
	// Tracker and media metadata can supply an episode IMDb ID where downstream
	// providers require the parent series ID. Clear the episode snapshot so the
	// second fetch pass refreshes series metadata after a successful adjustment.
	if episodeClient, ok := imdbClient.(imdbEpisodeClient); ok && shouldUseTVDBForCategory(meta, ids) && metadata.IMDB != nil &&
		strings.EqualFold(strings.TrimSpace(metadata.IMDB.Type), "tvEpisode") {
		lookup, err := episodeClient.GetEpisodeInfo(ctx, formatIMDbID(ids.IMDBID), false)
		if err != nil {
			if s.logger != nil {
				s.logger.Debugf("metadata: imdb episode parent lookup failed")
			}
		} else if parentID := metautil.ParseIMDbNumeric(lookup.Series.SeriesID); parentID != 0 && parentID != ids.IMDBID {
			ids.IMDBID = parentID
			ids.Provenance.IMDB = api.IdentityProvenanceProvider
			metadata.IMDB = nil
			if s.logger != nil {
				s.logger.Debugf("metadata: imdb episode id adjusted to parent series id=%d", parentID)
			}
		}
	}
	if shouldRunFetchPass() {
		runFetchPass(true)
	}

	clearTVDBForNonTVCategory(meta, &ids, &metadata)
	meta = s.applyTVEpisodeMetadata(ctx, meta, &ids, &metadata, tmdbClient, tvdbClient, tvmazeClient)

	needsPTBR := s.registry.NeedsLocalizedMetadata(meta.EvidenceTrackers, "pt-BR") || s.registry.NeedsLocalizedMetadata(meta.MatchedEvidenceTrackers, "pt-BR")

	if tmdbClient != nil && needsPTBR && ids.TMDBID != 0 {
		var mainData, seasonData, episodeData map[string]any
		var localizedErr error
		isTV := strings.EqualFold(string(ids.Category), "TV")
		mainInput := tmdb.LocalizedDataInput{
			TMDBID:           ids.TMDBID,
			Category:         string(ids.Category),
			DataType:         "main",
			Language:         "pt-BR",
			AppendToResponse: localizedMainAppendToResponse(isTV),
		}
		mainInput.CachePath = localizedTMDBCachePath(s.cfg.MainSettings.DBPath, mainInput)
		mainData, localizedErr = tmdbClient.GetLocalizedData(ctx, mainInput)
		if localizedErr != nil && s.logger != nil {
			s.logger.Debugf("metadata: pt-BR main localized data fetch failed: %v", localizedErr)
		}

		if isTV && meta.SeasonInt > 0 {
			seasonInput := tmdb.LocalizedDataInput{
				TMDBID:           ids.TMDBID,
				Season:           meta.SeasonInt,
				Category:         "TV",
				DataType:         "season",
				Language:         "pt-BR",
				AppendToResponse: "credits",
			}
			seasonInput.CachePath = localizedTMDBCachePath(s.cfg.MainSettings.DBPath, seasonInput)
			seasonData, localizedErr = tmdbClient.GetLocalizedData(ctx, seasonInput)
			if localizedErr != nil && s.logger != nil {
				s.logger.Debugf("metadata: pt-BR season localized data fetch failed: %v", localizedErr)
			}
			if meta.EpisodeInt > 0 {
				episodeInput := tmdb.LocalizedDataInput{
					TMDBID:           ids.TMDBID,
					Season:           meta.SeasonInt,
					Episode:          meta.EpisodeInt,
					Category:         "TV",
					DataType:         "episode",
					Language:         "pt-BR",
					AppendToResponse: "credits",
				}
				episodeInput.CachePath = localizedTMDBCachePath(s.cfg.MainSettings.DBPath, episodeInput)
				episodeData, localizedErr = tmdbClient.GetLocalizedData(ctx, episodeInput)
				if localizedErr != nil && s.logger != nil {
					s.logger.Debugf("metadata: pt-BR episode localized data fetch failed: %v", localizedErr)
				}
			}
		}

		if mainData != nil || seasonData != nil || episodeData != nil {
			localized := parseTMDBLocalizedData(mainData, seasonData, episodeData)
			if metadata.TMDB == nil {
				metadata.TMDB = &api.TMDBMetadata{TMDBID: ids.TMDBID}
			}
			if metadata.TMDB.Localized == nil {
				metadata.TMDB.Localized = make(map[string]api.TMDBLocalizedData)
			}
			merged := mergeLocalizedPTBR(metadata.TMDB.Localized["pt-BR"], localized)
			if localizedPTBRComplete(merged, isTV && meta.SeasonInt > 0) {
				metadata.TMDB.Localized["pt-BR"] = merged
			}
		}
	}

	if tmdbErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: tmdb metadata lookup failed: %v", tmdbErr)
	}
	if imdbErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: imdb metadata lookup failed: %v", imdbErr)
	}
	if tvdbErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: tvdb lookup failed: %v", tvdbErr)
	}
	if tvmazeErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: tvmaze lookup failed: %v", tvmazeErr)
	}
	if anilistErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: anilist lookup failed: %v", anilistErr)
	}
	if s.logger != nil {
		s.logger.Debugf(
			"metadata: external ids resolved tmdb=%d(%s) imdb=%d(%s) tvdb=%d(%s) tvmaze=%d(%s) mal=%d(%s)",
			ids.TMDBID,
			ids.Provenance.TMDB,
			ids.IMDBID,
			ids.Provenance.IMDB,
			ids.TVDBID,
			ids.Provenance.TVDB,
			ids.TVmazeID,
			ids.Provenance.TVmaze,
			ids.MALID,
			ids.Provenance.MAL,
		)
		s.logger.Debugf(
			"metadata: external metadata fetched tmdb=%t imdb=%t tvdb=%t tvmaze=%t anilist=%t bluray=%t",
			metadata.TMDB != nil,
			metadata.IMDB != nil,
			metadata.TVDB != nil,
			metadata.TVmaze != nil,
			metadata.AniList != nil,
			metadata.Bluray != nil,
		)
	}

	ids.ResolvedAt = time.Now().UTC()
	metadata.UpdatedAt = ids.ResolvedAt

	meta.Identity = ids
	meta.MALID = ids.MALID
	meta.ExternalIdentityCandidates = append(append([]api.ExternalIdentityCandidate(nil), candidates.TMDB...), candidates.IMDB...)
	meta.ProviderMetadata = metadata
	return meta, nil
}

func sourceScopedMetadataMatches(storedSourcePath string, currentSourcePath string) bool {
	trimmedStored := strings.TrimSpace(storedSourcePath)
	if trimmedStored == "" {
		return true
	}
	return strings.EqualFold(trimmedStored, strings.TrimSpace(currentSourcePath))
}

func invalidateMismatchedProviderMetadata(metadata *api.SourceScopedMetadata, ids api.ExternalIdentity) bool {
	if metadata == nil {
		return false
	}
	changed := false
	if metadata.TMDB != nil && (ids.TMDBID == 0 || metadata.TMDB.TMDBID != ids.TMDBID) {
		metadata.TMDB = nil
		changed = true
	}
	if metadata.IMDB != nil && (ids.IMDBID == 0 || metadata.IMDB.IMDBID != ids.IMDBID) {
		metadata.IMDB = nil
		changed = true
	}
	if metadata.TVDB != nil && (ids.TVDBID == 0 || metadata.TVDB.TVDBID != ids.TVDBID) {
		metadata.TVDB = nil
		changed = true
	}
	if metadata.TVmaze != nil && (ids.TVmazeID == 0 || metadata.TVmaze.TVmazeID != ids.TVmazeID) {
		metadata.TVmaze = nil
		changed = true
	}
	return changed
}

func usableTMDBMetadata(metadata *api.TMDBMetadata, tmdbID int) bool {
	return metadata != nil && tmdbID > 0 && metadata.TMDBID == tmdbID && strings.TrimSpace(metadata.Title) != ""
}

func usableIMDBMetadata(metadata *api.IMDBMetadata, imdbID int) bool {
	return metadata != nil && imdbID > 0 && metadata.IMDBID == imdbID && strings.TrimSpace(metadata.Title) != ""
}

func usableTVDBMetadata(metadata *api.TVDBMetadata, tvdbID int) bool {
	return metadata != nil && tvdbID > 0 && metadata.TVDBID == tvdbID &&
		(strings.TrimSpace(metadata.NameEnglish) != "" || strings.TrimSpace(metadata.Name) != "")
}

func usableTVmazeMetadata(metadata *api.TVmazeMetadata, tvmazeID int) bool {
	return metadata != nil && tvmazeID > 0 && metadata.TVmazeID == tvmazeID && strings.TrimSpace(metadata.Name) != ""
}

// shouldFetchAniListMetadata returns true when the current canonical MAL ID has
// no matching rich AniList snapshot. A MAL ID change must invalidate the stored
// snapshot so previews cannot reuse stale anime metadata.
func shouldFetchAniListMetadata(malID int, metadata *api.AniListMetadata) bool {
	if malID == 0 {
		return false
	}
	return metadata == nil || metadata.MALID != malID
}

// shouldUseTVDBForCategory reports whether TVDB data may be used for the resolved media category.
// Any explicit MOVIE category is authoritative over TV hints from MediaInfo, stored IDs, release data, or the filename.
func shouldUseTVDBForCategory(meta preparationstate.State, ids api.ExternalIdentity) bool {
	candidates := []string{string(ids.Category), string(meta.Identity.Category), meta.MediaInfoCategory, meta.Release.Category}
	if meta.ReleaseNameOverrides.Category != nil {
		candidates = append(candidates, *meta.ReleaseNameOverrides.Category)
	}
	for _, candidate := range candidates {
		if normalizeCategory(candidate) == "MOVIE" {
			return false
		}
	}
	for _, candidate := range candidates {
		if normalizeCategory(candidate) == "TV" {
			return true
		}
	}
	return isLikelyTV(meta)
}

// clearTVDBForNonTVCategory removes TVDB IDs and metadata when the resolved category no longer permits TVDB data.
func clearTVDBForNonTVCategory(meta preparationstate.State, ids *api.ExternalIdentity, metadata *api.SourceScopedMetadata) {
	if ids == nil {
		return
	}
	if shouldUseTVDBForCategory(meta, *ids) {
		return
	}
	ids.TVDBID = 0
	ids.Provenance.TVDB = ""
	if metadata != nil {
		metadata.TVDB = nil
	}
}

// localizedMainAppendToResponse selects the TMDB append list that exposes
// category-specific localized certification data.
func localizedMainAppendToResponse(isTV bool) string {
	if isTV {
		return "credits,videos,content_ratings"
	}
	return "credits,videos,release_dates"
}

// localizedTMDBCachePath returns the per-request cache file for pt-BR TMDB
// localized payloads, or empty when the configured DB path cannot host it.
func localizedTMDBCachePath(dbPath string, input tmdb.LocalizedDataInput) string {
	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	name := fmt.Sprintf(
		"tmdb_localized_%d_%s_%s_%s_s%d_e%d_%s.json",
		input.TMDBID,
		localizedCacheSlug(input.Category),
		localizedCacheSlug(input.DataType),
		localizedCacheSlug(input.Language),
		input.Season,
		input.Episode,
		localizedCacheSlug(input.AppendToResponse),
	)
	path, err := db.FileInSubdir(dbPath, "cache", name)
	if err != nil {
		return ""
	}
	return path
}

// localizedCacheSlug normalizes request components for stable cache filenames.
func localizedCacheSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "none"
	}
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	slug := strings.Trim(builder.String(), "_")
	if slug == "" {
		return "none"
	}
	return slug
}

// mergeLocalizedPTBR applies only non-empty incoming pt-BR fields so partial
// localized responses do not erase previously cached metadata.
func mergeLocalizedPTBR(existing, incoming api.TMDBLocalizedData) api.TMDBLocalizedData {
	merged := existing
	if strings.TrimSpace(incoming.Title) != "" {
		merged.Title = incoming.Title
	}
	if strings.TrimSpace(incoming.Overview) != "" {
		merged.Overview = incoming.Overview
	}
	if strings.TrimSpace(incoming.EpisodeTitle) != "" {
		merged.EpisodeTitle = incoming.EpisodeTitle
	}
	if strings.TrimSpace(incoming.EpisodeOverview) != "" {
		merged.EpisodeOverview = incoming.EpisodeOverview
	}
	if strings.TrimSpace(incoming.TrailerURL) != "" {
		merged.TrailerURL = incoming.TrailerURL
	}
	if strings.TrimSpace(incoming.Genres) != "" {
		merged.Genres = incoming.Genres
	}
	if strings.TrimSpace(incoming.ContentRating) != "" {
		merged.ContentRating = incoming.ContentRating
	}
	if strings.TrimSpace(incoming.Poster) != "" {
		merged.Poster = incoming.Poster
	}
	return merged
}

// localizedPTBRComplete reports whether localized metadata has the title-level
// fields required by current pt-BR trackers, plus scoped TV overview text when
// season or episode data is needed.
func localizedPTBRComplete(localized api.TMDBLocalizedData, episodeLike bool) bool {
	if strings.TrimSpace(localized.Title) == "" {
		return false
	}
	if episodeLike {
		return strings.TrimSpace(localized.EpisodeOverview) != ""
	}
	return strings.TrimSpace(localized.Overview) != ""
}

func mapTMDBCandidates(items []tmdb.Candidate, category string) []api.ExternalIdentityCandidate {
	if len(items) == 0 {
		return nil
	}
	normalizedCategory, _ := api.NormalizeCanonicalCategory(category)
	mapped := make([]api.ExternalIdentityCandidate, 0, len(items))
	for _, item := range items {
		if item.TMDBID == 0 {
			continue
		}
		posterURL := strings.TrimSpace(item.PosterPath)
		if posterURL != "" && !strings.HasPrefix(strings.ToLower(posterURL), "http://") && !strings.HasPrefix(strings.ToLower(posterURL), "https://") {
			posterURL = "https://image.tmdb.org/t/p/w342/" + strings.TrimPrefix(posterURL, "/")
		}
		mapped = append(mapped, api.ExternalIdentityCandidate{
			Provider:      api.IdentityProviderTMDB,
			ID:            item.TMDBID,
			Title:         strings.TrimSpace(item.Title),
			OriginalTitle: strings.TrimSpace(item.OriginalTitle),
			Year:          item.Year,
			Category:      normalizedCategory,
			MediaType:     string(normalizedCategory),
			Overview:      strings.TrimSpace(item.Overview),
			PosterURL:     posterURL,
			Similarity:    item.Similarity,
		})
	}
	if len(mapped) == 0 {
		return nil
	}
	return mapped
}

func mapIMDBCandidates(items []imdb.Candidate) []api.ExternalIdentityCandidate {
	if len(items) == 0 {
		return nil
	}
	mapped := make([]api.ExternalIdentityCandidate, 0, len(items))
	for _, item := range items {
		if item.IMDbID == 0 {
			continue
		}
		mapped = append(mapped, api.ExternalIdentityCandidate{
			Provider:   api.IdentityProviderIMDB,
			ID:         item.IMDbID,
			Title:      strings.TrimSpace(item.Title),
			Year:       item.Year,
			MediaType:  strings.TrimSpace(item.Type),
			Overview:   strings.TrimSpace(item.Plot),
			PosterURL:  strings.TrimSpace(item.PosterURL),
			Similarity: item.Similarity,
		})
	}
	if len(mapped) == 0 {
		return nil
	}
	return mapped
}

// ensureExternalClients initializes missing provider clients. It returns a nil
// TMDB client when no client was injected and no TMDB API key is configured;
// the keyless AniList and remaining provider clients are always initialized.
func (s *Service) ensureExternalClients() (TMDBClient, AniListClient, IMDBClient, TVDBClient, TVmazeClient) {
	if s.tmdb == nil {
		apiKey := strings.TrimSpace(s.cfg.MainSettings.TMDBAPI)
		if apiKey != "" {
			s.tmdb = tmdb.NewClient(nil, s.logger, apiKey)
		} else if s.logger != nil {
			s.logger.Debugf("metadata: tmdb client disabled reason=api_key_missing")
		}
	}
	if s.anilist == nil {
		if anilistClient, ok := s.tmdb.(AniListClient); ok {
			s.anilist = anilistClient
		} else {
			s.anilist = tmdb.NewClient(nil, s.logger, "")
		}
	}
	if s.imdb == nil {
		s.imdb = imdb.NewClient(nil, s.logger)
	}
	if s.tvdb == nil {
		tvdbCache := resolveTVDBCacheDir(s.cfg.MainSettings.DBPath)
		s.tvdb = tvdb.NewClient(
			nil,
			s.logger,
			"",
			tvdbCache,
		)
	}
	if s.tvmaze == nil {
		s.tvmaze = tvmaze.NewClient(nil, s.logger)
	}
	return s.tmdb, s.anilist, s.imdb, s.tvdb, s.tvmaze
}

func isIMDbTVMovie(ids api.ExternalIdentity, metadata api.SourceScopedMetadata) bool {
	if ids.IMDBID == 0 || metadata.IMDB == nil {
		return false
	}
	imdbType := strings.TrimSpace(metadata.IMDB.Type)
	if imdbType == "" {
		return false
	}
	for _, keyword := range []string{"tv movie", "tv special", "tvmovie"} {
		if imdbTypeContains(imdbType, keyword) {
			return true
		}
	}
	return false
}

func imdbTypeContains(value string, keyword string) bool {
	for part := range strings.SplitSeq(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), keyword) {
			return true
		}
	}
	return false
}

func resolveTVDBCacheDir(dbPath string) string {
	cacheRoot, err := db.Subdir(dbPath, "cache")
	if err != nil {
		return ""
	}
	cacheDir := filepath.Join(cacheRoot, "tvdb")
	_ = os.MkdirAll(cacheDir, 0o700)
	return cacheDir
}

func resolveSearchTitles(meta preparationstate.State) (string, string) {
	primary := normalizeSearchTitle(meta.Release.Title)
	secondary := strings.TrimSpace(meta.Release.Alt)
	if secondary == "" {
		secondary = normalizeSearchTitle(meta.Release.Subtitle)
	} else {
		secondary = normalizeSearchTitle(secondary)
	}
	if primary == "" {
		primary = normalizeSearchTitle(meta.Release.Subtitle)
	}
	if primary == "" {
		derivedTitle, _ := deriveSearchTitleYear(meta.SourcePath)
		primary = normalizeSearchTitle(derivedTitle)
	}
	if primary == "" {
		primary = normalizeSearchTitle(pathutil.Base(meta.SourcePath))
	}
	return primary, secondary
}

func resolveSearchYear(meta preparationstate.State) int {
	if meta.ReleaseNameOverrides.ManualYear != nil {
		if *meta.ReleaseNameOverrides.ManualYear > 0 {
			return *meta.ReleaseNameOverrides.ManualYear
		}
		return 0
	}
	if strings.TrimSpace(meta.DailyEpisodeDate) != "" {
		return 0
	}
	if meta.ArrYear != 0 {
		return meta.ArrYear
	}
	if meta.Release.Year != 0 {
		return meta.Release.Year
	}
	_, derivedYear := deriveSearchTitleYear(meta.SourcePath)
	if derivedYear > 0 {
		return derivedYear
	}
	return 0
}

func deriveSearchTitleYear(path string) (string, int) {
	base := strings.TrimSpace(pathutil.Base(path))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", 0
	}

	year := 0
	if match := searchYearPattern.FindString(base); match != "" {
		if parsed, err := strconv.Atoi(match); err == nil {
			year = parsed
		}
	}

	withoutNoise := searchBracketNoise.ReplaceAllString(base, " ")
	withoutNoise = strings.ReplaceAll(withoutNoise, ".", " ")
	withoutNoise = strings.ReplaceAll(withoutNoise, "_", " ")
	withoutNoise = searchInnerWhitespace.ReplaceAllString(withoutNoise, " ")
	withoutNoise = strings.TrimSpace(withoutNoise)
	if withoutNoise == "" {
		withoutNoise = base
	}

	if groups := leadingSearchYearTitle.FindStringSubmatch(withoutNoise); len(groups) == 3 {
		if parsed, err := strconv.Atoi(groups[1]); err == nil && year == 0 {
			year = parsed
		}
		return strings.TrimSpace(groups[2]), year
	}

	trimmed := searchYearPattern.ReplaceAllString(withoutNoise, " ")
	trimmed = strings.TrimSpace(searchInnerWhitespace.ReplaceAllString(trimmed, " "))
	if trimmed == "" {
		trimmed = withoutNoise
	}
	return trimmed, year
}

func normalizeSearchTitle(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	title, _ := deriveSearchTitleYear(trimmed)
	if title == "" {
		return trimmed
	}
	return title
}

func buildSearchYears(year int) []int {
	if year <= 0 {
		return []int{0}
	}
	return uniqueSearchYears([]int{year, year + 1, year - 1, 0})
}

func uniqueSearchYears(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if value < 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func isUnattendedMetadataSearch(meta preparationstate.State) bool {
	//nolint:exhaustive // Interactive modes are intentionally treated as attended.
	switch meta.Policy.InteractionMode {
	case api.InteractionModeUnattended, api.InteractionModeUnattendedConfirm:
		return true
	default:
		return false
	}
}

func resolveCategoryPreference(meta preparationstate.State) string {
	if meta.ReleaseNameOverrides.Category != nil {
		category := normalizeCategory(*meta.ReleaseNameOverrides.Category)
		if category != "" {
			return category
		}
	}
	// A known movie category wins before tracker or MediaInfo TV candidates can make the resolver keep TVDB state.
	for _, candidate := range []string{string(meta.Identity.Category), meta.Release.Category, meta.MediaInfoCategory} {
		if normalizeCategory(candidate) == "MOVIE" {
			return "MOVIE"
		}
	}
	for _, record := range meta.TrackerData {
		if normalized := normalizeCategory(string(record.Category)); normalized != "" {
			return normalized
		}
	}
	if normalized := normalizeCategory(string(meta.Identity.Category)); normalized != "" {
		return normalized
	}
	category := normalizeCategory(meta.MediaInfoCategory)
	if category != "" {
		return category
	}
	if normalized := normalizeCategory(meta.Release.Category); normalized != "" {
		return normalized
	}
	if isLikelyTV(meta) {
		return "TV"
	}
	return ""
}

func normalizeCategory(value string) string {
	category := api.NormalizeCategory(value)
	if category.IsValid() {
		return string(category.Canonical())
	}
	return ""
}

func applyOverrideID(target *int, source *api.IdentityProvenance, state *api.OverrideState, override *int) (bool, bool) {
	if target == nil || source == nil || state == nil || override == nil {
		return false, false
	}
	if *override == 0 {
		*target = 0
		*source = api.IdentityProvenanceExplicit
		*state = api.OverrideStateClear
		return false, true
	}
	*target = *override
	*source = api.IdentityProvenanceExplicit
	*state = api.OverrideStateValue
	return true, false
}

func applyResolvedID(target *int, source *api.IdentityProvenance, value int, origin string) {
	if value == 0 || target == nil || source == nil {
		return
	}
	if *target != 0 {
		return
	}
	*target = value
	*source = identityProvenance(origin)
}

func identityProvenance(origin string) api.IdentityProvenance {
	value := strings.ToLower(strings.TrimSpace(origin))
	switch {
	case strings.Contains(value, "mediainfo"):
		return api.IdentityProvenanceMediaInfo
	case strings.Contains(value, "scene") || strings.Contains(value, "nfo"):
		return api.IdentityProvenanceScene
	case strings.Contains(value, "arr") || strings.Contains(value, "sonarr") || strings.Contains(value, "radarr"):
		return api.IdentityProvenanceArr
	case strings.Contains(value, "tracker"):
		return api.IdentityProvenanceTracker
	case strings.Contains(value, "override"):
		return api.IdentityProvenanceExplicit
	default:
		return api.IdentityProvenanceProvider
	}
}

func formatIMDbID(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", value)
}

func formatOptionalInt(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func descriptionLogoLanguages(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
	languages := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			languages = append(languages, trimmed)
		}
	}
	return languages
}

func mapTMDBMetadata(ids api.ExternalIdentity, result tmdb.MetadataResult) *api.TMDBMetadata {
	if ids.TMDBID == 0 {
		return nil
	}
	return &api.TMDBMetadata{
		TMDBID:              ids.TMDBID,
		IMDBID:              result.IMDbID,
		TVDBID:              result.TVDBID,
		Category:            string(ids.Category),
		Title:               result.Title,
		OriginalTitle:       result.OriginalTitle,
		Year:                result.Year,
		ReleaseDate:         result.ReleaseDate,
		FirstAirDate:        result.FirstAirDate,
		LastAirDate:         result.LastAirDate,
		OriginCountry:       append([]string{}, result.OriginCountry...),
		OriginalLanguage:    result.OriginalLanguage,
		Overview:            result.Overview,
		Poster:              result.Poster,
		TMDBPosterPath:      result.TMDBPosterPath,
		Logo:                result.Logo,
		TMDBLogo:            result.TMDBLogo,
		Backdrop:            result.Backdrop,
		TMDBType:            result.TMDBType,
		Runtime:             result.Runtime,
		Genres:              result.Genres,
		GenreIDs:            result.GenreIDs,
		Creators:            append([]string{}, result.Creators...),
		Directors:           append([]string{}, result.Directors...),
		Cast:                append([]string{}, result.Cast...),
		MALID:               result.MALID,
		Anime:               result.Anime,
		Demographic:         result.Demographic,
		RetrievedAKA:        result.RetrievedAKA,
		Keywords:            result.Keywords,
		LocalizedTitles:     cloneStringMap(result.LocalizedTitles),
		YouTube:             result.YouTube,
		Certification:       result.Certification,
		ProductionCompanies: mapTMDBCompanies(result.ProductionCompanies),
		ProductionCountries: mapTMDBCountries(result.ProductionCountries),
		Networks:            mapTMDBNetworks(result.Networks),
		IMDbMismatch:        result.IMDbMismatch,
		MismatchedIMDbID:    result.MismatchedIMDbID,
	}
}

// mapAniListMetadata converts fetched AniList metadata into the persisted API
// contract, copying slice fields so repository callers do not share mutable
// TMDB-client backing arrays.
func mapAniListMetadata(result tmdb.AniListMetadataResult) *api.AniListMetadata {
	if result.MALID == 0 && result.AniListID == 0 {
		return nil
	}
	return &api.AniListMetadata{
		AniListID:          result.AniListID,
		MALID:              result.MALID,
		SiteURL:            result.SiteURL,
		TitleRomaji:        result.TitleRomaji,
		TitleEnglish:       result.TitleEnglish,
		TitleNative:        result.TitleNative,
		TitleUserPreferred: result.TitleUserPreferred,
		Description:        result.Description,
		Format:             result.Format,
		Status:             result.Status,
		StartDate:          result.StartDate,
		EndDate:            result.EndDate,
		Season:             result.Season,
		SeasonYear:         result.SeasonYear,
		Episodes:           result.Episodes,
		Duration:           result.Duration,
		CountryOfOrigin:    result.CountryOfOrigin,
		Source:             result.Source,
		CoverExtraLarge:    result.CoverExtraLarge,
		CoverLarge:         result.CoverLarge,
		CoverMedium:        result.CoverMedium,
		CoverColor:         result.CoverColor,
		BannerImage:        result.BannerImage,
		Genres:             append([]string{}, result.Genres...),
		Synonyms:           append([]string{}, result.Synonyms...),
		AverageScore:       result.AverageScore,
		MeanScore:          result.MeanScore,
		Popularity:         result.Popularity,
		Favourites:         result.Favourites,
		IsAdult:            result.IsAdult,
		Tags:               mapAniListTags(result.Tags),
		Studios:            mapAniListStudios(result.Studios),
		Trailer: api.AniListTrailer{
			ID:        result.Trailer.ID,
			Site:      result.Trailer.Site,
			Thumbnail: result.Trailer.Thumbnail,
		},
		NextAiringEpisode: api.AniListAiringEpisode{
			AiringAt:        result.NextAiringEpisode.AiringAt,
			TimeUntilAiring: result.NextAiringEpisode.TimeUntilAiring,
			Episode:         result.NextAiringEpisode.Episode,
		},
		ExternalLinks: mapAniListExternalLinks(result.ExternalLinks),
	}
}

// mapAniListTags converts AniList tag metadata into the API contract while
// preserving adult and spoiler flags for UI filtering.
func mapAniListTags(tags []tmdb.AniListTag) []api.AniListTag {
	result := make([]api.AniListTag, 0, len(tags))
	for _, tag := range tags {
		result = append(result, api.AniListTag{
			Name:             tag.Name,
			Rank:             tag.Rank,
			Category:         tag.Category,
			IsAdult:          tag.IsAdult,
			IsGeneralSpoiler: tag.IsGeneralSpoiler,
			IsMediaSpoiler:   tag.IsMediaSpoiler,
		})
	}
	return result
}

// mapAniListStudios converts main AniList studio nodes into the API contract.
func mapAniListStudios(studios []tmdb.AniListStudio) []api.AniListStudio {
	result := make([]api.AniListStudio, 0, len(studios))
	for _, studio := range studios {
		result = append(result, api.AniListStudio{
			ID:      studio.ID,
			Name:    studio.Name,
			SiteURL: studio.SiteURL,
		})
	}
	return result
}

// mapAniListExternalLinks converts AniList external links into the API
// contract used by frontend preview details.
func mapAniListExternalLinks(links []tmdb.AniListExternalLink) []api.AniListExternalLink {
	result := make([]api.AniListExternalLink, 0, len(links))
	for _, link := range links {
		result = append(result, api.AniListExternalLink{
			Site:     link.Site,
			URL:      link.URL,
			Type:     link.Type,
			Language: link.Language,
		})
	}
	return result
}

// cloneStringMap returns a detached copy of values, normalizing nil to an
// empty map so API callers observe the same object shape before JSON marshal.
func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func mapIMDBMetadata(info imdb.Info) *api.IMDBMetadata {
	imdbID := metautil.ParseIMDbNumeric(info.IMDbID)
	if imdbID == 0 {
		return nil
	}
	return &api.IMDBMetadata{
		IMDBID:           imdbID,
		IMDbIDText:       info.IMDbID,
		IMDbURL:          info.IMDbURL,
		Title:            info.Title,
		Year:             info.Year,
		EndYear:          info.EndYear,
		AKA:              info.AKA,
		Type:             info.Type,
		Plot:             info.Plot,
		Rating:           info.Rating,
		RatingCount:      info.RatingCount,
		RatingText:       info.RatingText,
		RuntimeMinutes:   info.RuntimeMinutes,
		RuntimeText:      info.RuntimeText,
		Genres:           info.Genres,
		Country:          info.Country,
		CountryList:      info.CountryList,
		Cover:            info.Cover,
		Directors:        mapIMDBPeople(info.Directors),
		Creators:         mapIMDBPeople(info.Creators),
		Writers:          mapIMDBPeople(info.Writers),
		Stars:            mapIMDBPeople(info.Stars),
		Editions:         append([]string{}, info.Editions...),
		EditionDetails:   mapIMDBEditionDetails(info.EditionDetails),
		Akas:             mapIMDBAkas(info.Akas),
		Episodes:         mapIMDBEpisodes(info.Episodes),
		SeasonsSummary:   mapIMDBSeasons(info.SeasonsSummary),
		SoundMixes:       append([]string{}, info.SoundMixes...),
		TVYear:           info.TVYear,
		OriginalLanguage: info.OriginalLanguage,
	}
}

func mapTMDBCompanies(values []tmdb.Company) []api.TMDBCompany {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.TMDBCompany, 0, len(values))
	for _, value := range values {
		result = append(result, api.TMDBCompany{
			ID:            value.ID,
			Name:          value.Name,
			LogoPath:      value.LogoPath,
			OriginCountry: value.OriginCountry,
		})
	}
	return result
}

func mapTMDBCountries(values []tmdb.Country) []api.TMDBCountry {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.TMDBCountry, 0, len(values))
	for _, value := range values {
		result = append(result, api.TMDBCountry{
			ISO3166: value.ISO3166,
			Name:    value.Name,
		})
	}
	return result
}

func mapTMDBNetworks(values []tmdb.Network) []api.TMDBNetwork {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.TMDBNetwork, 0, len(values))
	for _, value := range values {
		result = append(result, api.TMDBNetwork{
			ID:            value.ID,
			Name:          value.Name,
			LogoPath:      value.LogoPath,
			OriginCountry: value.OriginCountry,
		})
	}
	return result
}

func mapIMDBPeople(values []imdb.Person) []api.IMDBPerson {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.IMDBPerson, 0, len(values))
	for _, value := range values {
		result = append(result, api.IMDBPerson{ID: value.ID, Name: value.Name})
	}
	return result
}

func mapIMDBEditionDetails(values map[string]imdb.EditionDetail) map[string]api.IMDBEditionDetail {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]api.IMDBEditionDetail, len(values))
	for key, value := range values {
		result[key] = api.IMDBEditionDetail{
			DisplayName: value.DisplayName,
			Seconds:     value.Seconds,
			Minutes:     value.Minutes,
			Attributes:  append([]string{}, value.Attributes...),
		}
	}
	return result
}

func mapIMDBAkas(values []imdb.AKA) []api.IMDBAKA {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.IMDBAKA, 0, len(values))
	for _, value := range values {
		result = append(result, api.IMDBAKA{
			Title:      value.Title,
			Country:    value.Country,
			Language:   value.Language,
			Attributes: append([]string{}, value.Attributes...),
		})
	}
	return result
}

func mapIMDBEpisodes(values []imdb.Episode) []api.IMDBEpisode {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.IMDBEpisode, 0, len(values))
	for _, value := range values {
		result = append(result, api.IMDBEpisode{
			ID:          value.ID,
			Title:       value.Title,
			ReleaseYear: value.ReleaseYear,
			ReleaseDate: api.IMDBReleaseDate{
				Year:  value.ReleaseDate.Year,
				Month: value.ReleaseDate.Month,
				Day:   value.ReleaseDate.Day,
			},
			Season:      value.Season,
			EpisodeText: value.EpisodeText,
		})
	}
	return result
}

func mapIMDBSeasons(values []imdb.SeasonSummary) []api.IMDBSeasonSummary {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.IMDBSeasonSummary, 0, len(values))
	for _, value := range values {
		result = append(result, api.IMDBSeasonSummary{
			Season:    value.Season,
			Year:      value.Year,
			YearRange: value.YearRange,
		})
	}
	return result
}

func mapTVDBMetadata(tvdbID int, fallbackName string, details tvdb.SeriesMetadata) *api.TVDBMetadata {
	id := metautil.FirstInt(details.TVDBID, tvdbID)
	if id == 0 {
		return nil
	}
	year := parseYearFromDate(details.FirstAired)
	yearFromAlias := false
	yearSource := "first_aired"
	yearConfidence := ""
	if year == 0 {
		yearSource = ""
	}
	if details.SeriesYear > 0 && strings.TrimSpace(details.SeriesYearSource) != "" {
		year = details.SeriesYear
		yearFromAlias = true
		yearSource = strings.TrimSpace(details.SeriesYearSource)
		yearConfidence = strings.TrimSpace(details.SeriesYearConfidence)
	}
	name := metautil.FirstNonEmptyTrimmed(details.Name, fallbackName)
	aliases := make([]string, 0, len(details.Aliases))
	for _, alias := range details.Aliases {
		trimmed := strings.TrimSpace(alias.Name)
		if trimmed == "" {
			continue
		}
		aliases = append(aliases, trimmed)
	}
	if len(aliases) == 0 {
		aliases = nil
	}

	return &api.TVDBMetadata{
		TVDBID:           id,
		Name:             name,
		Overview:         strings.TrimSpace(details.Overview),
		NameEnglish:      strings.TrimSpace(details.NameEnglish),
		OverviewEnglish:  strings.TrimSpace(details.OverviewEnglish),
		FirstAired:       strings.TrimSpace(details.FirstAired),
		Year:             year,
		YearFromAlias:    yearFromAlias,
		YearSource:       yearSource,
		YearConfidence:   yearConfidence,
		Type:             strings.TrimSpace(details.Type),
		Status:           strings.TrimSpace(details.Status),
		Network:          strings.TrimSpace(details.Network),
		OriginalCountry:  strings.TrimSpace(details.OriginalCountry),
		OriginalLanguage: strings.TrimSpace(details.OriginalLanguage),
		HasEnglish:       strings.TrimSpace(details.NameEnglish) != "" || strings.TrimSpace(details.OverviewEnglish) != "",
		Genres:           strings.TrimSpace(strings.Join(details.Genres, ", ")),
		Poster:           strings.TrimSpace(details.Poster),
		Aliases:          aliases,
	}
}

// mapTVDBEpisodes copies TVDB client episode data into the persisted external
// metadata shape used by tracker payload builders.
func mapTVDBEpisodes(values []tvdb.Episode) []api.TVDBEpisodeMetadata {
	if len(values) == 0 {
		return nil
	}
	result := make([]api.TVDBEpisodeMetadata, 0, len(values))
	for _, value := range values {
		result = append(result, api.TVDBEpisodeMetadata{
			ID:              value.ID,
			SeasonNumber:    value.SeasonNumber,
			EpisodeNumber:   value.Number,
			EpisodeName:     strings.TrimSpace(value.Name),
			EpisodeOverview: strings.TrimSpace(value.Overview),
			EpisodeAired:    strings.TrimSpace(value.Aired),
			EpisodeImage:    strings.TrimSpace(value.Image),
		})
	}
	return result
}

// applySelectedTVDBEpisodeText mirrors translated selected-episode text back
// into the matching stored episode entry.
func applySelectedTVDBEpisodeText(metadata *api.TVDBMetadata, episodeID int) {
	if metadata == nil || episodeID == 0 {
		return
	}
	for idx := range metadata.Episodes {
		if metadata.Episodes[idx].ID != episodeID {
			continue
		}
		metadata.Episodes[idx].EpisodeNameEnglish = strings.TrimSpace(metadata.EpisodeNameEnglish)
		metadata.Episodes[idx].EpisodeOverviewEnglish = strings.TrimSpace(metadata.EpisodeOverviewEnglish)
		if strings.TrimSpace(metadata.Episodes[idx].EpisodeImage) == "" {
			metadata.Episodes[idx].EpisodeImage = strings.TrimSpace(metadata.EpisodeImage)
		}
		return
	}
}

// mergeTVDBMetadata fills missing stored TVDB fields from newer metadata and
// refreshes alias-year provenance when TVDB title evidence changes.
func mergeTVDBMetadata(target *api.TVDBMetadata, incoming *api.TVDBMetadata) {
	if target == nil || incoming == nil {
		return
	}
	if target.TVDBID == 0 {
		target.TVDBID = incoming.TVDBID
	}
	if strings.TrimSpace(target.Name) == "" {
		target.Name = incoming.Name
	}
	if strings.TrimSpace(target.Overview) == "" {
		target.Overview = incoming.Overview
	}
	if strings.TrimSpace(target.NameEnglish) == "" {
		target.NameEnglish = incoming.NameEnglish
	}
	if strings.TrimSpace(target.OverviewEnglish) == "" {
		target.OverviewEnglish = incoming.OverviewEnglish
	}
	if strings.TrimSpace(target.FirstAired) == "" {
		target.FirstAired = incoming.FirstAired
	}
	if target.Year == 0 {
		target.Year = incoming.Year
	}
	// Alias-derived years are naming-eligible, so refresh or clear the provenance when newer TVDB details change that status.
	switch {
	case incoming.YearFromAlias:
		target.Year = incoming.Year
		target.YearFromAlias = true
		target.YearSource = incoming.YearSource
		target.YearConfidence = incoming.YearConfidence
	case target.YearFromAlias && incoming.Year > 0 && strings.TrimSpace(incoming.YearSource) != "":
		// Only a sourced replacement year can clear alias provenance;
		// translation misses can return no usable year evidence.
		target.Year = incoming.Year
		target.YearFromAlias = false
		target.YearSource = incoming.YearSource
		target.YearConfidence = incoming.YearConfidence
	default:
		if strings.TrimSpace(target.YearSource) == "" {
			target.YearSource = incoming.YearSource
		}
		if strings.TrimSpace(target.YearConfidence) == "" {
			target.YearConfidence = incoming.YearConfidence
		}
	}
	if strings.TrimSpace(target.Type) == "" {
		target.Type = incoming.Type
	}
	if strings.TrimSpace(target.Status) == "" {
		target.Status = incoming.Status
	}
	if strings.TrimSpace(target.Network) == "" {
		target.Network = incoming.Network
	}
	if strings.TrimSpace(target.OriginalCountry) == "" {
		target.OriginalCountry = incoming.OriginalCountry
	}
	if strings.TrimSpace(target.OriginalLanguage) == "" {
		target.OriginalLanguage = incoming.OriginalLanguage
	}
	if strings.TrimSpace(target.Genres) == "" {
		target.Genres = incoming.Genres
	}
	if strings.TrimSpace(target.Poster) == "" {
		target.Poster = incoming.Poster
	}
	if len(target.Aliases) == 0 && len(incoming.Aliases) > 0 {
		target.Aliases = append([]string{}, incoming.Aliases...)
	}
	if len(target.Episodes) == 0 && len(incoming.Episodes) > 0 {
		target.Episodes = append([]api.TVDBEpisodeMetadata(nil), incoming.Episodes...)
	}
	target.HasEnglish = tvdbHasEnglishContent(target)
}

func mapTVmazeMetadata(result tvmaze.SearchResult) *api.TVmazeMetadata {
	if result.SelectedID == 0 {
		return nil
	}
	selected := tvmaze.Candidate{}
	for _, candidate := range result.Candidates {
		if candidate.ID == result.SelectedID {
			selected = candidate
			break
		}
	}
	imdbID := result.IMDBID
	if imdbID == 0 {
		imdbID = metautil.ParseIMDbNumeric(selected.Externals.IMDB)
	}
	tvdbID := result.TVDBID
	if tvdbID == 0 {
		tvdbID = selected.Externals.TVDB
	}
	genres := make([]string, 0, len(selected.Genres))
	for _, genre := range selected.Genres {
		trimmed := strings.TrimSpace(genre)
		if trimmed == "" {
			continue
		}
		genres = append(genres, trimmed)
	}
	poster := metautil.FirstNonEmptyTrimmed(selected.Image.Original, selected.Image.Medium)
	posterMedium := metautil.FirstNonEmptyTrimmed(selected.Image.Medium, selected.Image.Original)
	return &api.TVmazeMetadata{
		TVmazeID:       result.SelectedID,
		Name:           strings.TrimSpace(selected.Name),
		Premiered:      strings.TrimSpace(selected.Premiered),
		Ended:          strings.TrimSpace(selected.Ended),
		Summary:        strings.TrimSpace(selected.Summary),
		Status:         strings.TrimSpace(selected.Status),
		Type:           strings.TrimSpace(selected.Type),
		Language:       strings.TrimSpace(selected.Language),
		Genres:         strings.TrimSpace(strings.Join(genres, ", ")),
		Runtime:        selected.Runtime,
		AverageRuntime: selected.AverageRuntime,
		Rating:         selected.Rating,
		Weight:         selected.Weight,
		OfficialSite:   strings.TrimSpace(selected.OfficialSite),
		Country:        strings.TrimSpace(selected.Country),
		Network:        strings.TrimSpace(selected.Network.Name),
		NetworkCountry: strings.TrimSpace(selected.Network.Country),
		NetworkLogo:    metautil.FirstNonEmptyTrimmed(selected.Network.Logo, selected.Network.LogoSmall),
		WebChannel:     strings.TrimSpace(selected.WebChannel.Name),
		WebCountry:     strings.TrimSpace(selected.WebChannel.Country),
		WebLogo:        metautil.FirstNonEmptyTrimmed(selected.WebChannel.Logo, selected.WebChannel.LogoSmall),
		Poster:         poster,
		PosterMedium:   posterMedium,
		Backdrop:       poster,
		BackdropMedium: posterMedium,
		IMDBID:         imdbID,
		TVDBID:         tvdbID,
	}
}

// applyTVEpisodeMetadata enriches TV episode fields from provider data.
// Parsed release season/episode values may seed provider lookups, but only
// canonical metadata or provider-confirmed mappings are persisted back to
// SeasonInt and EpisodeInt. A nil TMDB client skips TMDB-specific mapping and
// detail lookups without disabling TVDB or TVmaze enrichment. TVDB episode
// matches take precedence; otherwise IMDb can map an exact, dated, or
// seasonless absolute episode before TVmaze and TMDB detail lookups run.
func (s *Service) applyTVEpisodeMetadata(
	ctx context.Context,
	meta preparationstate.State,
	ids *api.ExternalIdentity, external *api.SourceScopedMetadata,
	tmdbClient TMDBClient,
	tvdbClient TVDBClient,
	tvmazeClient TVmazeClient,
) preparationstate.State {
	if ids == nil {
		return meta
	}
	overrideMAL := meta.ExternalIDOverrides.MALID != nil
	if external != nil && external.TMDB != nil {
		meta.Anime = external.TMDB.Anime
	}
	meta.MALID = ids.MALID
	if overrideMAL {
		meta.MALID = metautil.FirstInt(ids.MALID, 0)
	}

	if !isLikelyTV(meta) && !strings.EqualFold(string(ids.Category), "TV") {
		return meta
	}
	if ids.Category == "" {
		ids.Category = api.CanonicalCategoryTV
	}

	fallbackSeason, fallbackEpisode := meta.SeasonEpisodeWithParsedFallback()
	season, episode := meta.CanonicalSeasonEpisode()
	initialSeason := season
	initialEpisode := episode
	initialSeasonStr := strings.TrimSpace(meta.SeasonStr)
	initialEpisodeStr := strings.TrimSpace(meta.EpisodeStr)
	dailyDate := strings.TrimSpace(meta.DailyEpisodeDate)
	if dailyDate == "" && meta.ReleaseNameOverrides.ManualDate != nil {
		dailyDate = strings.TrimSpace(*meta.ReleaseNameOverrides.ManualDate)
	}
	wantsSeasonEpisode := wantsSeasonEpisodeNaming(meta.ReleaseNameOverrides)
	hasManualSeasonEpisode := hasManualSeasonEpisodeOverrides(meta.ReleaseNameOverrides)
	tmdbDateMatch := false

	if tmdbClient != nil && dailyDate != "" && ids.TMDBID != 0 && ((season == 0 || episode == 0) || (wantsSeasonEpisode && !hasManualSeasonEpisode)) {
		if parsedDate, err := time.Parse("2006-01-02", dailyDate); err == nil {
			if mappedSeason, mappedEpisode, mapErr := tmdbClient.DailyToSeasonEpisode(ctx, ids.TMDBID, parsedDate); mapErr == nil {
				if mappedSeason > 0 && mappedEpisode > 0 {
					tmdbDateMatch = true
					if wantsSeasonEpisode && !hasManualSeasonEpisode {
						season = mappedSeason
						episode = mappedEpisode
					} else {
						season = metautil.FirstInt(mappedSeason, season)
						episode = metautil.FirstInt(mappedEpisode, episode)
					}
				} else if s.logger != nil {
					s.logger.Debugf("metadata: tmdb daily season/episode lookup returned no exact match")
				}
			} else if s.logger != nil {
				s.logger.Debugf("metadata: tmdb daily season/episode lookup failed: %v", mapErr)
			}
		}
	}

	if meta.Anime && ids.TVDBID != 0 {
		extracted := seasonep.Extract(meta.SourcePath, meta)
		absoluteEpisode := extracted.AbsoluteEpisode
		if absoluteEpisode > 0 {
			xemClient := thexem.NewClient(nil, s.logger)
			if mappedSeason, mappedEpisode, err := xemClient.MapAbsoluteEpisode(ctx, ids.TVDBID, absoluteEpisode); err == nil {
				season = metautil.FirstInt(mappedSeason, season)
				episode = metautil.FirstInt(mappedEpisode, episode)
			} else if !errors.Is(err, thexem.ErrUnavailable) && s.logger != nil {
				s.logger.Debugf("metadata: thexem absolute mapping failed: %v", err)
			}
			if season == 0 {
				title := resolveSeriesTitle(meta, external)
				if title != "" {
					if matchedSeason, err := xemClient.MatchSeasonByName(ctx, ids.TVDBID, title); err == nil && matchedSeason > 0 {
						season = matchedSeason
					}
				}
			}
			if episode == 0 {
				episode = absoluteEpisode
			}
		}
	}

	meta.SeasonInt = season
	meta.EpisodeInt = episode
	meta.SeasonStr = seasonep.FormatSeason(season)
	meta.EpisodeStr = seasonep.FormatEpisode(episode)
	meta.TMDBDateMatch = tmdbDateMatch
	if dailyDate != "" {
		meta.DailyEpisodeDate = dailyDate
	}

	episodeTitle := discardSeriesEpisodeTitle(strings.TrimSpace(meta.EpisodeTitle), meta, external)
	episodeOverview := strings.TrimSpace(meta.EpisodeOverview)
	episodeYear := meta.EpisodeYear

	tvdbEpisodeTitle := ""
	tvdbEpisodeOverview := ""
	tvmazeEpisodeTitle := ""
	tvmazeEpisodeOverview := ""
	tmdbEpisodeTitle := ""
	tmdbEpisodeOverview := ""
	imdbEpisodeTitle := ""
	tvdbEpisodeMatched := false

	if ids.TVDBID != 0 {
		querySeason := metautil.FirstInt(season, fallbackSeason)
		queryEpisode := metautil.FirstInt(episode, fallbackEpisode)
		query := tvdb.EpisodeQuery{
			Season:    querySeason,
			Episode:   queryEpisode,
			AiredDate: dailyDate,
			Debug:     false,
		}
		if meta.Anime {
			query.Absolute = seasonep.Extract(meta.SourcePath, meta).AbsoluteEpisode
		}
		if episodes, specificAlias, err := tvdbClient.GetEpisodesWithLanguage(ctx, ids.TVDBID, query, ""); err == nil {
			meta.TVDBAirsDays = append([]string{}, episodes.AirsDays...)
			meta.TVDBAirsTime = strings.TrimSpace(episodes.AirsTime)
			meta.TVDBAirsTimezone = strings.TrimSpace(episodes.AirsTimezone)
			meta.TVDBAirsTimezoneSource = strings.TrimSpace(episodes.AirsTimezoneSource)
			if external != nil {
				if external.TVDB == nil {
					external.TVDB = &api.TVDBMetadata{TVDBID: ids.TVDBID}
				}
				external.TVDB.Episodes = mapTVDBEpisodes(episodes.Episodes)
			}
			if external != nil && shouldApplyTVDBSpecificAlias(specificAlias) {
				if aliasName, aliasYear, ok := parseTVDBAliasNameYear(specificAlias); ok {
					if external.TVDB == nil {
						external.TVDB = &api.TVDBMetadata{TVDBID: ids.TVDBID}
					}
					external.TVDB.Name = aliasName
					if aliasYear > 0 {
						external.TVDB.Year = aliasYear
						external.TVDB.YearFromAlias = true
						// Preserve the series-level source when the alias string was synthesized from TVDB title metadata.
						yearSource := "extended_alias"
						yearConfidence := "high"
						if episodes.SeriesYear == aliasYear && strings.TrimSpace(episodes.SeriesYearSource) != "" {
							yearSource = strings.TrimSpace(episodes.SeriesYearSource)
							yearConfidence = strings.TrimSpace(episodes.SeriesYearConfidence)
						}
						external.TVDB.YearSource = yearSource
						external.TVDB.YearConfidence = yearConfidence
					}
				}
			}
			if !meta.TVPack {
				if match, ok := tvdb.GetSpecificEpisodeData(episodes, query); ok {
					tvdbEpisodeMatched = true
					meta.TVDBAiredDate = strings.TrimSpace(match.Aired)
					preferredTVDBEpisodeTitle := strings.TrimSpace(match.EpisodeName)
					englishTVDBEpisodeTitle := ""
					englishTVDBEpisodeOverview := ""
					// Only use original TVDB episode text when the series language is unknown or English.
					allowOriginalTVDBEpisodeText := true
					if external != nil {
						if external.TVDB == nil {
							external.TVDB = &api.TVDBMetadata{TVDBID: ids.TVDBID}
						}
						if strings.TrimSpace(external.TVDB.OriginalLanguage) != "" && !isEnglishLanguage(external.TVDB.OriginalLanguage) {
							allowOriginalTVDBEpisodeText = false
						}
						external.TVDB.EpisodeSeason = match.SeasonNumber
						external.TVDB.EpisodeNumber = match.EpisodeNumber
						external.TVDB.EpisodeName = strings.TrimSpace(match.EpisodeName)
						external.TVDB.EpisodeOverview = strings.TrimSpace(match.Overview)
						external.TVDB.EpisodeAired = metautil.FirstNonEmptyTrimmed(match.Aired, query.AiredDate)
						external.TVDB.EpisodeImage = strings.TrimSpace(match.Image)
						if isEnglishLanguage(external.TVDB.OriginalLanguage) {
							external.TVDB.EpisodeNameEnglish = strings.TrimSpace(match.EpisodeName)
							external.TVDB.EpisodeOverviewEnglish = strings.TrimSpace(match.Overview)
						} else if match.EpisodeID != 0 {
							translated, translationErr := tvdbClient.GetEpisodeTranslation(ctx, match.EpisodeID, "eng")
							if translationErr != nil {
								if s.logger != nil {
									s.logger.Debugf("metadata: tvdb episode english translation lookup failed: %v", translationErr)
								}
							} else {
								external.TVDB.EpisodeNameEnglish = metautil.FirstNonEmptyTrimmed(translated.Name, external.TVDB.EpisodeNameEnglish)
								external.TVDB.EpisodeOverviewEnglish = metautil.FirstNonEmptyTrimmed(translated.Overview, external.TVDB.EpisodeOverviewEnglish)
							}
						}
						applySelectedTVDBEpisodeText(external.TVDB, match.EpisodeID)
						external.TVDB.HasEnglish = tvdbHasEnglishContent(external.TVDB)
						englishTVDBEpisodeTitle = strings.TrimSpace(external.TVDB.EpisodeNameEnglish)
						englishTVDBEpisodeOverview = strings.TrimSpace(external.TVDB.EpisodeOverviewEnglish)
						preferredTVDBEpisodeTitle = metautil.FirstNonEmptyTrimmed(external.TVDB.EpisodeNameEnglish, preferredTVDBEpisodeTitle)
					}
					if englishTVDBEpisodeTitle != "" {
						tvdbEpisodeTitle = englishTVDBEpisodeTitle
					} else if allowOriginalTVDBEpisodeText && !isGenericEpisodeTitle(preferredTVDBEpisodeTitle) {
						tvdbEpisodeTitle = preferredTVDBEpisodeTitle
					}
					if englishTVDBEpisodeOverview != "" {
						tvdbEpisodeOverview = englishTVDBEpisodeOverview
					} else if allowOriginalTVDBEpisodeText {
						tvdbEpisodeOverview = strings.TrimSpace(match.Overview)
					}
					if episodeYear == 0 {
						episodeYear = match.Year
					}
					if match.SeasonNumber > 0 {
						season = match.SeasonNumber
					}
					if match.EpisodeNumber > 0 {
						episode = match.EpisodeNumber
					}
				}
			}
		} else if s.logger != nil {
			s.logger.Debugf("metadata: tvdb episode lookup failed: %v", err)
		}
	}

	if !meta.TVPack && !tvdbEpisodeMatched && external != nil && external.IMDB != nil {
		if match, ok := findIMDBEpisode(external.IMDB.Episodes, season, episode, dailyDate); ok {
			imdbEpisodeTitle = strings.TrimSpace(match.Title)
			if match.Season > 0 {
				season = match.Season
			}
			if matchedEpisode := parseIMDBEpisodeNumber(match.EpisodeText); matchedEpisode > 0 {
				episode = matchedEpisode
			}
			if episodeYear == 0 {
				episodeYear = metautil.FirstInt(match.ReleaseYear, match.ReleaseDate.Year)
			}
		}
	}

	if !meta.TVPack && ids.TVmazeID != 0 {
		var epData *tvmaze.EpisodeData
		var err error
		if dailyDate != "" {
			epData, err = tvmazeClient.GetEpisodeByDate(ctx, ids.TVmazeID, dailyDate)
		} else if lookupSeason, lookupEpisode := metautil.FirstInt(season, fallbackSeason), metautil.FirstInt(episode, fallbackEpisode); lookupSeason > 0 && lookupEpisode > 0 {
			epData, err = tvmazeClient.GetEpisodeByNumber(ctx, ids.TVmazeID, lookupSeason, lookupEpisode, tvmaze.EpisodeLookupContext{
				ManualDate: dailyDate,
				Debug:      false,
			})
		}
		if err != nil && s.logger != nil {
			s.logger.Debugf("metadata: tvmaze episode lookup failed: %v", err)
		}
		if epData != nil {
			tvmazeEpisodeTitle = strings.TrimSpace(epData.EpisodeName)
			tvmazeEpisodeOverview = strings.TrimSpace(epData.Overview)
			if epData.SeasonNumber > 0 {
				season = epData.SeasonNumber
			}
			if epData.EpisodeNumber > 0 {
				episode = epData.EpisodeNumber
			}
			if episodeYear == 0 {
				if parsedYear := parseYearFromDate(epData.AirDate); parsedYear > 0 {
					episodeYear = parsedYear
				}
			}
		}
	}

	if tmdbClient != nil && ids.TMDBID != 0 {
		lookupSeason := metautil.FirstInt(season, fallbackSeason)
		lookupEpisode := metautil.FirstInt(episode, fallbackEpisode)
		if !meta.TVPack && lookupSeason > 0 && lookupEpisode > 0 {
			if details, err := tmdbClient.GetEpisodeDetails(ctx, ids.TMDBID, lookupSeason, lookupEpisode); err == nil {
				tmdbEpisodeTitle = strings.TrimSpace(details.Name)
				tmdbEpisodeOverview = strings.TrimSpace(details.Overview)
				if details.SeasonNumber > 0 {
					season = details.SeasonNumber
				}
				if details.EpisodeNumber > 0 {
					episode = details.EpisodeNumber
				}
				if episodeYear == 0 {
					if parsedYear := parseYearFromDate(details.AirDate); parsedYear > 0 {
						episodeYear = parsedYear
					}
				}
			} else if s.logger != nil {
				s.logger.Debugf("metadata: tmdb episode details lookup failed: %v", err)
			}
		}
		if meta.TVPack && lookupSeason > 0 {
			if details, err := tmdbClient.GetSeasonDetails(ctx, ids.TMDBID, lookupSeason); err == nil {
				if details.SeasonNumber > 0 {
					season = details.SeasonNumber
				}
				if tmdbEpisodeTitle == "" {
					tmdbEpisodeTitle = strings.TrimSpace(details.Name)
				}
				if tmdbEpisodeOverview == "" {
					tmdbEpisodeOverview = strings.TrimSpace(details.Overview)
				}
			} else if s.logger != nil {
				s.logger.Debugf("metadata: tmdb season details lookup failed: %v", err)
			}
		}
	}

	meta.SeasonInt = season
	meta.EpisodeInt = episode
	meta.SeasonStr = seasonep.FormatSeason(season)
	meta.EpisodeStr = seasonep.FormatEpisode(episode)
	meta.EpisodeYear = metautil.FirstInt(episodeYear, meta.EpisodeYear)
	meta.EpisodeTitle = sanitizeEpisodeTitle(
		metautil.FirstNonEmptyTrimmed(episodeTitle, tvdbEpisodeTitle, tvmazeEpisodeTitle, tmdbEpisodeTitle, imdbEpisodeTitle),
	)
	meta.EpisodeOverview = metautil.FirstNonEmptyTrimmed(episodeOverview, tvdbEpisodeOverview, tvmazeEpisodeOverview, tmdbEpisodeOverview)

	if s.logger != nil && (initialSeason != season || initialEpisode != episode || initialSeasonStr != meta.SeasonStr || initialEpisodeStr != meta.EpisodeStr) {
		s.logger.Debugf(
			"metadata: tv episode metadata updated season=%q->%q episode=%q->%q daily_date=%q title=%q",
			initialSeasonStr,
			meta.SeasonStr,
			initialEpisodeStr,
			meta.EpisodeStr,
			strings.TrimSpace(meta.DailyEpisodeDate),
			strings.TrimSpace(meta.EpisodeTitle),
		)
	}

	if tmdbClient != nil && wantsSeasonEpisode && !hasManualSeasonEpisode && !tmdbDateMatch && strings.TrimSpace(meta.DailyEpisodeDate) != "" &&
		ids.TMDBID != 0 &&
		s.logger != nil {
		s.logger.Warnf(
			"metadata: season/episode naming requested but TMDB season/episode lookup failed for daily_date=%q tmdb_id=%d",
			strings.TrimSpace(meta.DailyEpisodeDate),
			ids.TMDBID,
		)
	}

	return meta
}

// findIMDBEpisode matches by air date, then canonical season/episode. When the
// season is unknown, episode is treated as a one-based absolute position among
// numbered, non-special episodes ordered by season and episode number.
func findIMDBEpisode(episodes []api.IMDBEpisode, season, episode int, airedDate string) (api.IMDBEpisode, bool) {
	if len(episodes) == 0 {
		return api.IMDBEpisode{}, false
	}
	if aired, err := time.Parse("2006-01-02", strings.TrimSpace(airedDate)); err == nil {
		for _, candidate := range episodes {
			if candidate.ReleaseDate.Year == aired.Year() && candidate.ReleaseDate.Month == int(aired.Month()) && candidate.ReleaseDate.Day == aired.Day() {
				return candidate, true
			}
		}
	}
	if season > 0 && episode > 0 {
		for _, candidate := range episodes {
			if candidate.Season == season && parseIMDBEpisodeNumber(candidate.EpisodeText) == episode {
				return candidate, true
			}
		}
	}
	if season != 0 || episode <= 0 {
		return api.IMDBEpisode{}, false
	}

	ordered := make([]api.IMDBEpisode, 0, len(episodes))
	for _, candidate := range episodes {
		if candidate.Season > 0 && parseIMDBEpisodeNumber(candidate.EpisodeText) > 0 {
			ordered = append(ordered, candidate)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Season != ordered[j].Season {
			return ordered[i].Season < ordered[j].Season
		}
		return parseIMDBEpisodeNumber(ordered[i].EpisodeText) < parseIMDBEpisodeNumber(ordered[j].EpisodeText)
	})
	if episode > len(ordered) {
		return api.IMDBEpisode{}, false
	}
	return ordered[episode-1], true
}

// parseIMDBEpisodeNumber returns a positive numeric IMDb episode label, or zero
// when the label is empty, non-numeric, or non-positive.
func parseIMDBEpisodeNumber(value string) int {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

func wantsSeasonEpisodeNaming(overrides api.ReleaseNameOverrides) bool {
	return overrides.UseSeasonEpisode != nil && *overrides.UseSeasonEpisode
}

func hasManualSeasonEpisodeOverrides(overrides api.ReleaseNameOverrides) bool {
	if overrides.Season != nil && strings.TrimSpace(*overrides.Season) != "" {
		return true
	}
	return overrides.Episode != nil && strings.TrimSpace(*overrides.Episode) != ""
}

func isEnglishLanguage(language string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(language))
	if trimmed == "" {
		return false
	}
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	if trimmed == "en" || trimmed == "eng" || trimmed == "english" {
		return true
	}
	return strings.HasPrefix(trimmed, "en-")
}

func tvdbHasEnglishContent(metadata *api.TVDBMetadata) bool {
	if metadata == nil {
		return false
	}
	return strings.TrimSpace(metadata.NameEnglish) != "" ||
		strings.TrimSpace(metadata.OverviewEnglish) != "" ||
		strings.TrimSpace(metadata.EpisodeNameEnglish) != "" ||
		strings.TrimSpace(metadata.EpisodeOverviewEnglish) != ""
}

func resolveSeriesTitle(meta preparationstate.State, external *api.SourceScopedMetadata) string {
	if external != nil {
		if external.TMDB != nil && strings.TrimSpace(external.TMDB.Title) != "" {
			return strings.TrimSpace(external.TMDB.Title)
		}
		if external.TVDB != nil && strings.TrimSpace(external.TVDB.Name) != "" {
			return strings.TrimSpace(external.TVDB.Name)
		}
		if external.TVmaze != nil && strings.TrimSpace(external.TVmaze.Name) != "" {
			return strings.TrimSpace(external.TVmaze.Name)
		}
	}
	if strings.TrimSpace(meta.Release.Title) != "" {
		return strings.TrimSpace(meta.Release.Title)
	}
	return strings.TrimSpace(meta.Release.Subtitle)
}

func parseYearFromDate(value string) int {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 4 {
		return 0
	}
	year, err := strconv.Atoi(trimmed[:4])
	if err != nil {
		return 0
	}
	return year
}

// isGenericEpisodeTitle reports whether value is provider placeholder text
// rather than a useful episode title.
func isGenericEpisodeTitle(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	return genericEpisodePattern.MatchString(trimmed) || placeholderTitlePattern.MatchString(trimmed)
}

// discardSeriesEpisodeTitle clears parsed episode titles that duplicate a known
// series title so provider episode titles can still populate the release name.
func discardSeriesEpisodeTitle(episodeTitle string, meta preparationstate.State, external *api.SourceScopedMetadata) string {
	if episodeTitle == "" || !episodeTitleMatchesSeriesTitle(episodeTitle, meta, external) {
		return episodeTitle
	}
	return ""
}

func episodeTitleMatchesSeriesTitle(episodeTitle string, meta preparationstate.State, external *api.SourceScopedMetadata) bool {
	episodeKey := titleIdentityKey(episodeTitle)
	if len(episodeKey) < 6 {
		return false
	}
	for _, title := range seriesTitleCandidates(meta, external) {
		if episodeKey == titleIdentityKey(title) {
			return true
		}
	}
	return false
}

func seriesTitleCandidates(meta preparationstate.State, external *api.SourceScopedMetadata) []string {
	candidates := []string{
		meta.Release.Title,
		meta.Release.Subtitle,
	}
	if external == nil {
		return candidates
	}
	if external.TMDB != nil {
		candidates = append(candidates, external.TMDB.Title, external.TMDB.OriginalTitle)
	}
	if external.IMDB != nil {
		candidates = append(candidates, external.IMDB.Title)
	}
	if external.TVDB != nil {
		candidates = append(candidates, external.TVDB.Name, external.TVDB.NameEnglish)
	}
	if external.TVmaze != nil {
		candidates = append(candidates, external.TVmaze.Name)
	}
	return candidates
}

// titleIdentityKey compares titles across punctuation and spacing differences.
func titleIdentityKey(value string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
		}
	}
	return builder.String()
}

// sanitizeEpisodeTitle returns an empty value for generic provider episode
// titles while preserving real titles that merely contain the word episode.
func sanitizeEpisodeTitle(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if isGenericEpisodeTitle(trimmed) {
		return ""
	}
	return trimmed
}

func shouldApplyTVDBSpecificAlias(specificAlias string) bool {
	return strings.TrimSpace(specificAlias) != ""
}

func parseTVDBAliasNameYear(alias string) (string, int, bool) {
	trimmed := strings.TrimSpace(alias)
	if trimmed == "" {
		return "", 0, false
	}
	name := trimmed
	year := 0
	// TVDB alias precedence uses the final year-bearing disambiguator while
	// cleanup removes every year token from the display name.
	matches := tvdbAliasYearPattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) > 0 {
		match := matches[len(matches)-1]
		parsed, err := strconv.Atoi(match[1])
		if err != nil {
			return "", 0, false
		}
		year = parsed
		name = tvdbAliasYearCleanup.ReplaceAllString(trimmed, " ")
		name = strings.ReplaceAll(name, "(", " ")
		name = strings.ReplaceAll(name, ")", " ")
	}

	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", 0, false
	}
	return name, year, true
}

func isLikelyTV(meta preparationstate.State) bool {
	if strings.EqualFold(meta.MediaInfoCategory, "TV") || strings.EqualFold(string(meta.Identity.Category), "TV") {
		return true
	}
	if meta.HasTVSeasonEpisodeSignal() {
		return true
	}
	if strings.TrimSpace(meta.DailyEpisodeDate) != "" {
		return true
	}
	path := filepath.ToSlash(strings.ToLower(strings.TrimSpace(meta.SourcePath)))
	if tvPathHintPattern.MatchString(path) {
		return true
	}
	base := strings.ToLower(pathutil.Base(meta.SourcePath))
	if tvNameHintPattern.MatchString(base) {
		return true
	}
	return subsPleaseHintPattern.MatchString(path) && animeEpisodeHint.MatchString(base)
}

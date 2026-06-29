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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/errgroup"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/metadata/thexem"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
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
	tvNameHintPattern      = regexp.MustCompile(`(?i)\bS\d{1,2}(?:E\d{1,3})?\b|\b\d{1,2}x\d{2,3}\b|\b(?:season|series)\s*\d+\b|\b(19\d{2}|20\d{2})[.-]\d{2}[.-]\d{2}\b`)
	subsPleaseHintPattern  = regexp.MustCompile(`(?i)subsplease`)
	animeEpisodeHint       = regexp.MustCompile(`(?i)-\s*\d{1,3}\s*\(1080p\)`)
	genericEpisodePattern  = regexp.MustCompile(`(?i)^episode\s*#?\s*\d+\s*$`)
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

// IMDBClient is the IMDb metadata surface used by external-ID resolution.
type IMDBClient interface {
	Search(ctx context.Context, input imdb.SearchInput) (imdb.SearchResult, error)
	GetInfo(ctx context.Context, imdbID string, manualLanguage string, debug bool) (imdb.Info, error)
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

// ResolveExternalIDs resolves and persists cross-provider IDs and metadata for
// a prepared item, honoring fresh stored data, overrides, scene IDs, and tracker
// matches before falling back to provider searches. Selected BJS, BT, and ASC
// targets trigger pt-BR TMDB localized metadata when a TMDB ID is known.
func (s *Service) ResolveExternalIDs(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if s.repo == nil {
		return api.PreparedMetadata{}, internalerrors.ErrInvalidInput
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return api.PreparedMetadata{}, internalerrors.ErrInvalidInput
	}

	ids := api.ExternalIDs{SourcePath: meta.SourcePath}
	if meta.StoredDataFresh && strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.SourcePath), strings.TrimSpace(meta.SourcePath)) {
		ids = meta.ExternalIDs
		if strings.TrimSpace(ids.SourcePath) == "" {
			ids.SourcePath = meta.SourcePath
		}
	}
	if meta.Options.SkipAutoTorrent {
		clearTrackerSourcedExternalIDs(&ids)
	}
	metadata := api.ExternalMetadata{SourcePath: meta.SourcePath}
	if meta.StoredDataFresh && strings.EqualFold(strings.TrimSpace(meta.ExternalMetadata.SourcePath), strings.TrimSpace(meta.SourcePath)) {
		metadata = meta.ExternalMetadata
		if strings.TrimSpace(metadata.SourcePath) == "" {
			metadata.SourcePath = meta.SourcePath
		}
	} else {
		storedMeta, err := s.repo.GetExternalMetadata(ctx, meta.SourcePath)
		if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: load stored external metadata: %w", err)
		}
		if err == nil {
			metadata.Bluray = storedMeta.Bluray
		}
	}
	candidates := api.ExternalIDCandidates{}
	categoryPref := resolveCategoryPreference(meta)
	if categoryPref != "" {
		ids.Category = categoryPref
	}
	tmdbCategoryPref := normalizeCategory(ids.Category)
	if s.logger != nil {
		s.logger.Debugf("metadata: external ids start path=%q category=%q", meta.SourcePath, ids.Category)
	}

	overrideTMDB, clearedTMDB := applyOverrideID(&ids.TMDBID, &ids.SourceTMDB, meta.ExternalIDOverrides.TMDBID)
	overrideIMDB, clearedIMDB := applyOverrideID(&ids.IMDBID, &ids.SourceIMDB, meta.ExternalIDOverrides.IMDBID)
	overrideTVDB, clearedTVDB := applyOverrideID(&ids.TVDBID, &ids.SourceTVDB, meta.ExternalIDOverrides.TVDBID)
	overrideTVmaze, _ := applyOverrideID(&ids.TVmazeID, &ids.SourceTVmaze, meta.ExternalIDOverrides.TVmazeID)

	trackerTMDB, trackerIMDB, trackerTVDB := 0, 0, 0
	if !meta.Options.SkipAutoTorrent || meta.SourceLookupActive {
		trackerTMDB, trackerIMDB, trackerTVDB = resolveTrackerIDs(meta.TrackerData)
	}
	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.SourceTMDB, trackerTMDB, "tracker")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.SourceIMDB, trackerIMDB, "tracker")
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.SourceTVDB, trackerTVDB, "tracker")
	}

	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.SourceTMDB, meta.MediaInfoTMDBID, "mediainfo")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.SourceIMDB, meta.MediaInfoIMDBID, "mediainfo")
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.SourceTVDB, meta.MediaInfoTVDBID, "mediainfo")
	}

	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.SourceTMDB, meta.SceneTMDBID, "scene")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.SourceIMDB, meta.SceneIMDB, "scene")
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.SourceTVDB, meta.SceneTVDBID, "scene")
	}
	if !overrideTVmaze {
		applyResolvedID(&ids.TVmazeID, &ids.SourceTVmaze, meta.SceneTVmazeID, "scene")
	}
	if !overrideTMDB && !clearedTMDB {
		applyResolvedID(&ids.TMDBID, &ids.SourceTMDB, meta.ArrTMDBID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.SourceIMDB, meta.ArrIMDBID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if !overrideTVDB && !clearedTVDB {
		applyResolvedID(&ids.TVDBID, &ids.SourceTVDB, meta.ArrTVDBID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if !overrideTVmaze {
		applyResolvedID(&ids.TVmazeID, &ids.SourceTVmaze, meta.ArrTVmazeID, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr"))
	}
	if s.logger != nil {
		s.logger.Debugf(
			"metadata: external ids initial tmdb=%d(%s) imdb=%d(%s) tvdb=%d(%s)",
			ids.TMDBID,
			ids.SourceTMDB,
			ids.IMDBID,
			ids.SourceIMDB,
			ids.TVDBID,
			ids.SourceTVDB,
		)
	}

	tmdbClient, imdbClient, tvdbClient, tvmazeClient, err := s.ensureExternalClients()
	if err != nil {
		return api.PreparedMetadata{}, err
	}

	filename, secondary := resolveSearchTitles(meta)
	year := resolveSearchYear(meta)
	manualLanguage := ""
	if meta.MetadataOverrides.OriginalLanguage != nil {
		manualLanguage = strings.TrimSpace(*meta.MetadataOverrides.OriginalLanguage)
	}
	unattendedSearch := isUnattendedMetadataSearch(meta)
	if !overrideTMDB && ids.TMDBID == 0 && ids.IMDBID != 0 {
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
			Debug:      meta.Options.Debug,
		})
		if err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: tmdb external lookup failed: %v", err)
			}
		} else if result.TMDBID != 0 {
			applyResolvedID(&ids.TMDBID, &ids.SourceTMDB, result.TMDBID, "tmdb_external")
			if ids.Category == "" && result.Category != "" {
				ids.Category = strings.ToUpper(strings.TrimSpace(result.Category))
			}
		}
		if len(result.Candidates) > 0 && len(candidates.TMDB) == 0 {
			candidates.TMDB = mapTMDBCandidates(result.Candidates, result.Category)
			candidates.TMDBAutoSelected = result.AutoSelected
		}
	}

	needsTMDBSearch := !overrideTMDB && ids.TMDBID == 0
	needsIMDBSearch := !overrideIMDB && ids.IMDBID == 0

	if needsTMDBSearch || needsIMDBSearch {
		searchYears := buildSearchYears(year)
		if s.logger != nil {
			s.logger.Debugf(
				"metadata: external ids search tmdb+imdb filename=%q year=%d stages=%v unattended=%t category=%q",
				filename,
				year,
				searchYears,
				unattendedSearch,
				tmdbCategoryPref,
			)
		}

		for _, stageYear := range searchYears {
			if ids.TMDBID != 0 && ids.IMDBID != 0 {
				break
			}

			stageNeedsTMDB := !overrideTMDB && ids.TMDBID == 0
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
						Debug:          meta.Options.Debug,
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
						Category:          ids.Category,
						Unattended:        unattendedSearch,
						Debug:             meta.Options.Debug,
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
				applyResolvedID(&ids.TMDBID, &ids.SourceTMDB, tmdbOutcome.TMDBID, "tmdb_search")
				if ids.Category == "" && tmdbOutcome.Category != "" {
					ids.Category = strings.ToUpper(strings.TrimSpace(tmdbOutcome.Category))
				}
			}
			if stageNeedsTMDB && len(tmdbOutcome.Candidates) > 0 && len(candidates.TMDB) == 0 {
				candidates.TMDB = mapTMDBCandidates(tmdbOutcome.Candidates, tmdbOutcome.Category)
				candidates.TMDBAutoSelected = tmdbOutcome.AutoSelected
			}
			if stageNeedsIMDB && imdbResult.IMDbID != 0 {
				applyResolvedID(&ids.IMDBID, &ids.SourceIMDB, imdbResult.IMDbID, "imdb_search")
			}
			if stageNeedsIMDB && len(imdbResult.Candidates) > 0 && len(candidates.IMDB) == 0 {
				candidates.IMDB = mapIMDBCandidates(imdbResult.Candidates)
				candidates.IMDBAutoSelected = imdbResult.AutoSelected
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
	tvdbName := ""

	isTVForTVmaze := func() bool {
		return shouldUseTVDBForCategory(meta, ids)
	}

	tmdbLogoFetchAttempted := false
	shouldFetchTMDBMetadata := func() bool {
		if ids.TMDBID == 0 {
			return false
		}
		if metadata.TMDB == nil {
			return true
		}
		return s.cfg.Description.AddLogo && strings.TrimSpace(metadata.TMDB.Logo) == "" && !tmdbLogoFetchAttempted
	}

	shouldRunFetchPass := func() bool {
		if shouldFetchTMDBMetadata() {
			return true
		}
		if ids.IMDBID != 0 && metadata.IMDB == nil {
			return true
		}
		if shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && (ids.IMDBID != 0 || ids.TMDBID != 0) {
			return true
		}
		if metadata.TVmaze == nil && isTVForTVmaze() && (ids.TVmazeID != 0 || (!overrideTVmaze && ids.TVmazeID == 0 && (ids.IMDBID != 0 || ids.TVDBID != 0))) {
			return true
		}
		return false
	}

	runFetchPass := func(allowTVmazeNameFallback bool) {
		fetchTMDB := shouldFetchTMDBMetadata()
		if fetchTMDB && s.cfg.Description.AddLogo {
			tmdbLogoFetchAttempted = true
		}
		fetchIMDB := ids.IMDBID != 0 && metadata.IMDB == nil
		lookupTVDB := shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && (ids.IMDBID != 0 || ids.TMDBID != 0)
		lookupTVmaze := metadata.TVmaze == nil && isTVForTVmaze() && (ids.TVmazeID != 0 || (!overrideTVmaze && ids.TVmazeID == 0 && (ids.IMDBID != 0 || ids.TVDBID != 0)))

		if s.logger != nil {
			s.logger.Debugf(
				"metadata: external ids fetch tmdb=%t imdb=%t tvdb=%t tvmaze=%t tvmaze_name_fallback=%t",
				fetchTMDB,
				fetchIMDB,
				lookupTVDB,
				lookupTVmaze,
				allowTVmazeNameFallback,
			)
		}

		var tmdbResult *tmdb.MetadataResult
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
					Category:       ids.Category,
					SearchYear:     year,
					IMDbID:         ids.IMDBID,
					TVDBID:         ids.TVDBID,
					ManualLanguage: manualLanguage,
					AddLogo:        s.cfg.Description.AddLogo,
					LogoLanguages:  descriptionLogoLanguages(s.cfg.Description.LogoLanguage),
					Filename:       filename,
					Debug:          meta.Options.Debug,
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

		if fetchIMDB {
			group.Go(func() error {
				result, err := imdbClient.GetInfo(gctx, formatIMDbID(ids.IMDBID), manualLanguage, meta.Options.Debug)
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
			s.logger.Debugf("metadata: external ids tvdb lookup skipped id=%d source=%s", ids.TVDBID, ids.SourceTVDB)
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
					Debug:             meta.Options.Debug,
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
			s.logger.Debugf("metadata: external ids tvmaze lookup skipped id=%d source=%s", ids.TVmazeID, ids.SourceTVmaze)
		}

		_ = group.Wait()

		if tmdbResult != nil {
			metadata.TMDB = mapTMDBMetadata(ids, *tmdbResult)
			if !overrideIMDB && ids.IMDBID == 0 && tmdbResult.IMDbID != 0 {
				applyResolvedID(&ids.IMDBID, &ids.SourceIMDB, tmdbResult.IMDbID, "tmdb")
			}
			if ids.Category == "" && tmdbResult.TMDBType != "" {
				ids.Category = normalizeCategory(tmdbResult.TMDBType)
			}
			if shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && tmdbResult.TVDBID != 0 {
				applyResolvedID(&ids.TVDBID, &ids.SourceTVDB, tmdbResult.TVDBID, "tmdb")
			}
		}

		if imdbInfo != nil {
			metadata.IMDB = mapIMDBMetadata(*imdbInfo)
		}

		if fetchedTVDBID != 0 {
			if !overrideTVDB {
				applyResolvedID(&ids.TVDBID, &ids.SourceTVDB, fetchedTVDBID, "tvdb")
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
				applyResolvedID(&ids.TVmazeID, &ids.SourceTVmaze, tvmazeResult.SelectedID, "tvmaze")
			}
			if !overrideIMDB && ids.IMDBID == 0 && tvmazeResult.IMDBID != 0 {
				applyResolvedID(&ids.IMDBID, &ids.SourceIMDB, tvmazeResult.IMDBID, "tvmaze")
			}
			if shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && ids.TVDBID == 0 && tvmazeResult.TVDBID != 0 {
				applyResolvedID(&ids.TVDBID, &ids.SourceTVDB, tvmazeResult.TVDBID, "tvmaze")
			}
			metadata.TVmaze = mapTVmazeMetadata(*tvmazeResult)
		}

		if shouldUseTVDBForCategory(meta, ids) && metadata.TVDB == nil && ids.TVDBID != 0 {
			metadata.TVDB = &api.TVDBMetadata{TVDBID: ids.TVDBID, Name: tvdbName}
		}
		if metadata.TVmaze == nil && ids.TVmazeID != 0 {
			metadata.TVmaze = &api.TVmazeMetadata{TVmazeID: ids.TVmazeID, IMDBID: ids.IMDBID, TVDBID: ids.TVDBID}
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
	if shouldRunFetchPass() {
		runFetchPass(true)
	}

	clearTVDBForNonTVCategory(meta, &ids, &metadata)
	meta = s.applyTVEpisodeMetadata(ctx, meta, &ids, &metadata, tmdbClient, tvdbClient, tvmazeClient)

	needsPTBR := trackers.AnyNeedsPTBRLocalizedMetadata(meta.Trackers) || trackers.AnyNeedsPTBRLocalizedMetadata(meta.MatchedTrackers)

	if needsPTBR && ids.TMDBID != 0 {
		var mainData, seasonData, episodeData map[string]any
		var localizedErr error
		isTV := strings.EqualFold(ids.Category, "TV")
		mainInput := tmdb.LocalizedDataInput{
			TMDBID:           ids.TMDBID,
			Category:         ids.Category,
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
	if s.logger != nil {
		s.logger.Debugf(
			"metadata: external ids resolved tmdb=%d(%s) imdb=%d(%s) tvdb=%d(%s) tvmaze=%d(%s)",
			ids.TMDBID,
			ids.SourceTMDB,
			ids.IMDBID,
			ids.SourceIMDB,
			ids.TVDBID,
			ids.SourceTVDB,
			ids.TVmazeID,
			ids.SourceTVmaze,
		)
		s.logger.Debugf(
			"metadata: external metadata fetched tmdb=%t imdb=%t tvdb=%t tvmaze=%t bluray=%t",
			metadata.TMDB != nil,
			metadata.IMDB != nil,
			metadata.TVDB != nil,
			metadata.TVmaze != nil,
			metadata.Bluray != nil,
		)
	}

	ids.UpdatedAt = time.Now().UTC()
	metadata.UpdatedAt = ids.UpdatedAt
	if ids.Category != "" {
		meta.Release.Category = ids.Category
		if err := s.persistResolvedReleaseCategory(ctx, meta.SourcePath, ids.Category, ids.UpdatedAt); err != nil {
			return api.PreparedMetadata{}, err
		}
	}

	if err := s.repo.SaveExternalIDs(ctx, ids); err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: save external ids: %w", err)
	}
	if metadata.TMDB != nil || metadata.IMDB != nil || metadata.TVDB != nil || metadata.TVmaze != nil || metadata.Bluray != nil {
		if err := s.repo.SaveExternalMetadata(ctx, metadata); err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: save external metadata: %w", err)
		}
	}

	meta.ExternalIDs = ids
	meta.ExternalIDCandidates = candidates
	meta.ExternalMetadata = metadata
	return meta, nil
}

func clearTrackerSourcedExternalIDs(ids *api.ExternalIDs) {
	if ids == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(ids.SourceTMDB), "tracker") {
		ids.TMDBID = 0
		ids.SourceTMDB = ""
	}
	if strings.EqualFold(strings.TrimSpace(ids.SourceIMDB), "tracker") {
		ids.IMDBID = 0
		ids.SourceIMDB = ""
	}
	if strings.EqualFold(strings.TrimSpace(ids.SourceTVDB), "tracker") {
		ids.TVDBID = 0
		ids.SourceTVDB = ""
	}
	if strings.EqualFold(strings.TrimSpace(ids.SourceTVmaze), "tracker") {
		ids.TVmazeID = 0
		ids.SourceTVmaze = ""
	}
}

// shouldUseTVDBForCategory reports whether TVDB data may be used for the resolved media category.
// Any explicit MOVIE category is authoritative over TV hints from MediaInfo, stored IDs, release data, or the filename.
func shouldUseTVDBForCategory(meta api.PreparedMetadata, ids api.ExternalIDs) bool {
	candidates := []string{ids.Category, meta.ExternalIDs.Category, meta.MediaInfoCategory, meta.Release.Category}
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
func clearTVDBForNonTVCategory(meta api.PreparedMetadata, ids *api.ExternalIDs, metadata *api.ExternalMetadata) {
	if ids == nil {
		return
	}
	if shouldUseTVDBForCategory(meta, *ids) {
		return
	}
	ids.TVDBID = 0
	ids.SourceTVDB = ""
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

func mapTMDBCandidates(items []tmdb.Candidate, category string) []api.ExternalIDCandidate {
	if len(items) == 0 {
		return nil
	}
	normalizedCategory := normalizeCategory(category)
	mapped := make([]api.ExternalIDCandidate, 0, len(items))
	for _, item := range items {
		if item.TMDBID == 0 {
			continue
		}
		posterURL := strings.TrimSpace(item.PosterPath)
		if posterURL != "" && !strings.HasPrefix(strings.ToLower(posterURL), "http://") && !strings.HasPrefix(strings.ToLower(posterURL), "https://") {
			posterURL = "https://image.tmdb.org/t/p/w342/" + strings.TrimPrefix(posterURL, "/")
		}
		mapped = append(mapped, api.ExternalIDCandidate{
			Provider:      "tmdb",
			ID:            item.TMDBID,
			Title:         strings.TrimSpace(item.Title),
			OriginalTitle: strings.TrimSpace(item.OriginalTitle),
			Year:          item.Year,
			Category:      normalizedCategory,
			MediaType:     normalizedCategory,
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

func mapIMDBCandidates(items []imdb.Candidate) []api.ExternalIDCandidate {
	if len(items) == 0 {
		return nil
	}
	mapped := make([]api.ExternalIDCandidate, 0, len(items))
	for _, item := range items {
		if item.IMDbID == 0 {
			continue
		}
		mapped = append(mapped, api.ExternalIDCandidate{
			Provider:   "imdb",
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

func (s *Service) ensureExternalClients() (TMDBClient, IMDBClient, TVDBClient, TVmazeClient, error) {
	if s.tmdb == nil {
		apiKey := strings.TrimSpace(s.cfg.MainSettings.TMDBAPI)
		if apiKey == "" {
			return nil, nil, nil, nil, errors.New("metadata: tmdb api key missing")
		}
		s.tmdb = tmdb.NewClient(nil, s.logger, apiKey)
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
	return s.tmdb, s.imdb, s.tvdb, s.tvmaze, nil
}

func isIMDbTVMovie(ids api.ExternalIDs, metadata api.ExternalMetadata) bool {
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

func resolveSearchTitles(meta api.PreparedMetadata) (string, string) {
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

func resolveSearchYear(meta api.PreparedMetadata) int {
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

func isUnattendedMetadataSearch(meta api.PreparedMetadata) bool {
	//nolint:exhaustive // Interactive modes are intentionally treated as attended.
	switch meta.Options.InteractionMode {
	case api.InteractionModeUnattended, api.InteractionModeUnattendedConfirm:
		return true
	default:
		return false
	}
}

func resolveCategoryPreference(meta api.PreparedMetadata) string {
	if meta.ReleaseNameOverrides.Category != nil {
		category := normalizeCategory(*meta.ReleaseNameOverrides.Category)
		if category != "" {
			return category
		}
	}
	// A known movie category wins before tracker or MediaInfo TV candidates can make the resolver keep TVDB state.
	for _, candidate := range []string{meta.ExternalIDs.Category, meta.Release.Category, meta.MediaInfoCategory} {
		if normalizeCategory(candidate) == "MOVIE" {
			return "MOVIE"
		}
	}
	for _, record := range meta.TrackerData {
		if normalized := normalizeCategory(string(record.Category)); normalized != "" {
			return normalized
		}
	}
	if normalized := normalizeCategory(meta.ExternalIDs.Category); normalized != "" {
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

func (s *Service) persistResolvedReleaseCategory(ctx context.Context, sourcePath string, category string, updatedAt time.Time) error {
	normalized := api.NormalizeCategory(category)
	if s.repo == nil || strings.TrimSpace(sourcePath) == "" || !normalized.IsValid() {
		return nil
	}

	stored, err := s.repo.GetByPath(ctx, sourcePath)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("metadata: load stored metadata for resolved category: %w", err)
	}
	if stored.Category.Canonical() == normalized {
		return nil
	}

	stored.Category = normalized
	stored.UpdatedAt = updatedAt
	if stored.UpdatedAt.IsZero() {
		stored.UpdatedAt = time.Now().UTC()
	}
	if err := s.repo.Save(ctx, stored); err != nil {
		return fmt.Errorf("metadata: persist resolved release category: %w", err)
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: persisted resolved release category path=%q category=%q", sourcePath, normalized)
	}
	return nil
}

func applyOverrideID(target *int, source *string, override *int) (bool, bool) {
	if target == nil || source == nil || override == nil {
		return false, false
	}
	if *override == 0 {
		*target = 0
		*source = "override_clear"
		return false, true
	}
	*target = *override
	*source = "override"
	return true, false
}

func applyResolvedID(target *int, source *string, value int, origin string) {
	if value == 0 || target == nil || source == nil {
		return
	}
	if *target != 0 {
		return
	}
	*target = value
	*source = origin
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

func mapTMDBMetadata(ids api.ExternalIDs, result tmdb.MetadataResult) *api.TMDBMetadata {
	if ids.TMDBID == 0 {
		return nil
	}
	return &api.TMDBMetadata{
		TMDBID:              ids.TMDBID,
		IMDBID:              result.IMDbID,
		TVDBID:              result.TVDBID,
		Category:            ids.Category,
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
			ReleaseDate: api.IMDBReleaseDate{Year: value.ReleaseDate.Year, Month: value.ReleaseDate.Month, Day: value.ReleaseDate.Day},
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

func (s *Service) applyTVEpisodeMetadata(
	ctx context.Context,
	meta api.PreparedMetadata,
	ids *api.ExternalIDs,
	external *api.ExternalMetadata,
	tmdbClient TMDBClient,
	tvdbClient TVDBClient,
	tvmazeClient TVmazeClient,
) api.PreparedMetadata {
	if ids == nil {
		return meta
	}
	overrideMAL := meta.ExternalIDOverrides.MALID != nil
	if external != nil && external.TMDB != nil {
		meta.Anime = external.TMDB.Anime
		meta.MALID = external.TMDB.MALID
	}
	if meta.MALID == 0 {
		meta.MALID = meta.SceneMALID
	}
	if overrideMAL {
		meta.MALID = metautil.FirstInt(*meta.ExternalIDOverrides.MALID, 0)
	}

	if !isLikelyTV(meta) && !strings.EqualFold(ids.Category, "TV") {
		return meta
	}
	if ids.Category == "" {
		ids.Category = "TV"
	}

	season := meta.SeasonInt
	if season == 0 {
		season = meta.Release.Season
	}
	episode := meta.EpisodeInt
	if episode == 0 {
		episode = meta.Release.Episode
	}
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

	if dailyDate != "" && ids.TMDBID != 0 && ((season == 0 || episode == 0) || (wantsSeasonEpisode && !hasManualSeasonEpisode)) {
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

	if ids.TVDBID != 0 {
		query := tvdb.EpisodeQuery{
			Season:    season,
			Episode:   episode,
			AiredDate: dailyDate,
			Debug:     meta.Options.Debug,
		}
		if meta.Anime {
			query.Absolute = seasonep.Extract(meta.SourcePath, meta).AbsoluteEpisode
		}
		if episodes, specificAlias, err := tvdbClient.GetEpisodesWithLanguage(ctx, ids.TVDBID, query, ""); err == nil {
			meta.TVDBAirsDays = append([]string{}, episodes.AirsDays...)
			meta.TVDBAirsTime = strings.TrimSpace(episodes.AirsTime)
			meta.TVDBAirsTimezone = strings.TrimSpace(episodes.AirsTimezone)
			meta.TVDBAirsTimezoneSource = strings.TrimSpace(episodes.AirsTimezoneSource)
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
					if season == 0 && match.SeasonNumber > 0 {
						season = match.SeasonNumber
					}
					if episode == 0 && match.EpisodeNumber > 0 {
						episode = match.EpisodeNumber
					}
				}
			}
		} else if s.logger != nil {
			s.logger.Debugf("metadata: tvdb episode lookup failed: %v", err)
		}
	}

	if !meta.TVPack && ids.TVmazeID != 0 {
		var epData *tvmaze.EpisodeData
		var err error
		if dailyDate != "" {
			epData, err = tvmazeClient.GetEpisodeByDate(ctx, ids.TVmazeID, dailyDate)
		} else if season > 0 && episode > 0 {
			epData, err = tvmazeClient.GetEpisodeByNumber(ctx, ids.TVmazeID, season, episode, tvmaze.EpisodeLookupContext{
				ManualDate: dailyDate,
				Debug:      meta.Options.Debug,
			})
		}
		if err != nil && s.logger != nil {
			s.logger.Debugf("metadata: tvmaze episode lookup failed: %v", err)
		}
		if epData != nil {
			tvmazeEpisodeTitle = strings.TrimSpace(epData.EpisodeName)
			tvmazeEpisodeOverview = strings.TrimSpace(epData.Overview)
			if season == 0 && epData.SeasonNumber > 0 {
				season = epData.SeasonNumber
			}
			if episode == 0 && epData.EpisodeNumber > 0 {
				episode = epData.EpisodeNumber
			}
			if episodeYear == 0 {
				if parsedYear := parseYearFromDate(epData.AirDate); parsedYear > 0 {
					episodeYear = parsedYear
				}
			}
		}
	}

	if ids.TMDBID != 0 {
		if !meta.TVPack && season > 0 && episode > 0 {
			if details, err := tmdbClient.GetEpisodeDetails(ctx, ids.TMDBID, season, episode); err == nil {
				tmdbEpisodeTitle = strings.TrimSpace(details.Name)
				tmdbEpisodeOverview = strings.TrimSpace(details.Overview)
				if episodeYear == 0 {
					if parsedYear := parseYearFromDate(details.AirDate); parsedYear > 0 {
						episodeYear = parsedYear
					}
				}
			} else if s.logger != nil {
				s.logger.Debugf("metadata: tmdb episode details lookup failed: %v", err)
			}
		}
		if meta.TVPack && season > 0 {
			if details, err := tmdbClient.GetSeasonDetails(ctx, ids.TMDBID, season); err == nil {
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
	meta.EpisodeTitle = sanitizeEpisodeTitle(metautil.FirstNonEmptyTrimmed(episodeTitle, tvdbEpisodeTitle, tvmazeEpisodeTitle, tmdbEpisodeTitle))
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

	if wantsSeasonEpisode && !hasManualSeasonEpisode && !tmdbDateMatch && strings.TrimSpace(meta.DailyEpisodeDate) != "" && ids.TMDBID != 0 && s.logger != nil {
		s.logger.Warnf("metadata: season/episode naming requested but TMDB season/episode lookup failed for daily_date=%q tmdb_id=%d", strings.TrimSpace(meta.DailyEpisodeDate), ids.TMDBID)
	}

	return meta
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

func resolveSeriesTitle(meta api.PreparedMetadata, external *api.ExternalMetadata) string {
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

func isGenericEpisodeTitle(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	return genericEpisodePattern.MatchString(trimmed)
}

// discardSeriesEpisodeTitle clears parsed episode titles that duplicate a known
// series title so provider episode titles can still populate the release name.
func discardSeriesEpisodeTitle(episodeTitle string, meta api.PreparedMetadata, external *api.ExternalMetadata) string {
	if episodeTitle == "" || !episodeTitleMatchesSeriesTitle(episodeTitle, meta, external) {
		return episodeTitle
	}
	return ""
}

func episodeTitleMatchesSeriesTitle(episodeTitle string, meta api.PreparedMetadata, external *api.ExternalMetadata) bool {
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

func seriesTitleCandidates(meta api.PreparedMetadata, external *api.ExternalMetadata) []string {
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

func sanitizeEpisodeTitle(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "episode") || strings.Contains(lower, "tba") {
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

func isLikelyTV(meta api.PreparedMetadata) bool {
	if strings.EqualFold(meta.MediaInfoCategory, "TV") || strings.EqualFold(meta.ExternalIDs.Category, "TV") {
		return true
	}
	if meta.SeasonInt > 0 || meta.EpisodeInt > 0 || meta.Release.Season > 0 || meta.Release.Episode > 0 {
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

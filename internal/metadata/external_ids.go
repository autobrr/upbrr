// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/logging"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"golang.org/x/sync/errgroup"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/metadata/thexem"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/redaction"
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
	genericEpisodePattern  = regexp.MustCompile(
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

type animeResolver interface {
	ResolveAnime(context.Context, string, tmdb.MetadataInput) (tmdb.AnimeLookupResult, error)
}

type logoArtworkClient interface {
	SelectLogo(context.Context, int, string, []string) (string, string, error)
}

type seriesExternalIDsClient interface {
	GetSeriesExternalIDs(context.Context, int) (int, int, error)
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
	GetSeriesRemoteLinks(context.Context, int) (api.TVDBRemoteLinks, error)
	SearchSeries(ctx context.Context, filename, year string) ([]tvdb.SeriesSearchResult, int, error)
	GetByExternalID(ctx context.Context, imdbID, tmdbID string, tvMovie bool) (int, string, error)
	GetSeriesMetadata(ctx context.Context, seriesID int) (tvdb.SeriesMetadata, error)
	GetSeriesMetadataWithLanguage(ctx context.Context, seriesID int, language string) (tvdb.SeriesMetadata, error)
	GetNameDisambiguation(ctx context.Context, input tvdb.NameDisambiguationInput) tvdb.NameDisambiguation
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
	logger := logging.FromContext(ctx, s.logger)

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
	sourceFingerprint := cmp.Or(meta.IdentitySourceFingerprint, meta.SourceFingerprint)
	previousMALID := meta.Identity.MALID
	meta.ProviderMetadata = externalidentity.StoredMetadataForSource(meta.ProviderMetadata, meta.Identity, meta.SourcePath, sourceFingerprint)
	meta.Identity = externalidentity.StoredIdentityForSource(
		meta.Identity.WithoutResetPins(meta.IdentityResetFields), meta.SourcePath, sourceFingerprint, meta.ExternalIDOverrides,
	).WithProviderRetention(meta.ExternalIDOverrides)
	if previousMALID != 0 && meta.MALID == previousMALID && meta.Identity.MALID == 0 {
		meta.MALID = 0
	}
	invalidateMismatchedProviderMetadata(&meta.ProviderMetadata, meta.Identity)

	refreshProviders := meta.ExternalFreshness.RequiresRefresh()
	previousIdentity := meta.Identity
	if refreshProviders {
		meta.Identity = api.ExternalIdentity{SourcePath: meta.SourcePath}
		if previousIdentity.HasRetainedProvider() {
			meta.Identity.Category = previousIdentity.Category
		}
		meta.MALID = 0
		meta.ExternalIdentityCandidates = nil
	}
	ids := api.ExternalIdentity{SourcePath: meta.SourcePath}
	storedIdentityVersion := previousIdentity.Resolution.ContractVersion
	if refreshProviders && sourceScopedMetadataMatches(previousIdentity.SourcePath, meta.SourcePath) {
		copyStoredProviderPins(&ids, previousIdentity)
	}
	reuseStoredIDs := !refreshProviders && meta.StoredDataFresh && sourceScopedMetadataMatches(meta.Identity.SourcePath, meta.SourcePath)
	if reuseStoredIDs {
		ids = meta.Identity
		if strings.TrimSpace(ids.SourcePath) == "" {
			ids.SourcePath = meta.SourcePath
		}
	} else if storedIDs, err := s.repo.GetExternalIdentity(ctx, meta.SourcePath); err == nil {
		storedIdentityVersion = storedIDs.Resolution.ContractVersion
		storedIDs = externalidentity.StoredIdentityForSource(
			storedIDs.WithoutResetPins(meta.IdentityResetFields), ids.SourcePath, sourceFingerprint, meta.ExternalIDOverrides,
		).WithProviderRetention(meta.ExternalIDOverrides)
		if sourceScopedMetadataMatches(storedIDs.SourcePath, ids.SourcePath) {
			// The resolver also reads this record. Keep its lineage available for
			// withdrawals. Reusing provider metadata still requires its own scope checks.
			previousIdentity = storedIDs
		}
		copyStoredProviderPins(&ids, storedIDs)
		if ids.HasRetainedProvider() {
			meta.Identity.Category = ids.Category
		}
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return preparationstate.State{}, fmt.Errorf("metadata: load stored external identity: %w", err)
	}
	meta.WithdrawnStoredIDs = nil
	recordWithdrawnStoredIDs := func(before, remaining api.ExternalIdentity) {
		for _, provider := range []api.IdentityProvider{
			api.IdentityProviderTMDB, api.IdentityProviderIMDB, api.IdentityProviderTVDB, api.IdentityProviderTVmaze, api.IdentityProviderMAL,
		} {
			id, hadID := previousIdentity.ProviderID(provider)
			previousID, _ := before.ProviderID(provider)
			if _, kept := remaining.ProviderID(provider); hadID && id == previousID && !kept {
				if meta.WithdrawnStoredIDs == nil {
					meta.WithdrawnStoredIDs = make(map[api.IdentityProvider]int)
				}
				meta.WithdrawnStoredIDs[provider] = id
			}
		}
	}
	if !reuseStoredIDs {
		// Collection starts from pins, but the resolver can still consider the
		// stored automatic IDs. Check their lineage within the stored scope.
		prior := previousIdentity
		if invalidateIdentityDependencies(&prior, false) > 0 {
			recordWithdrawnStoredIDs(previousIdentity, prior)
		}
	}
	invalidateCandidateDependencies := func(invalidateUnrecorded bool) int {
		before := ids
		invalidated := invalidateIdentityDependencies(&ids, invalidateUnrecorded)
		if invalidated > 0 {
			recordWithdrawnStoredIDs(before, ids)
		}
		return invalidated
	}
	metadata := api.SourceScopedMetadata{SourcePath: meta.SourcePath}
	retainedMetadata := meta.ProviderMetadata
	replayProviders := evidence.Enabled(ctx) && meta.ExternalFreshness == api.ExternalFreshnessLoad
	if replayProviders {
		metadata.Bluray = meta.ProviderMetadata.Bluray
	}
	if !refreshProviders && !replayProviders && meta.StoredDataFresh && sourceScopedMetadataMatches(meta.ProviderMetadata.SourcePath, meta.SourcePath) {
		metadata = meta.ProviderMetadata
		if strings.TrimSpace(metadata.SourcePath) == "" {
			metadata.SourcePath = meta.SourcePath
		}
	} else if !replayProviders && (!refreshProviders || retainedMetadata.TMDB == nil &&
		sourceFingerprint != "" && previousIdentity.Resolution.SourceFingerprint == sourceFingerprint) {
		storedMeta, err := s.repo.GetExternalMetadata(ctx, meta.SourcePath)
		if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
			return preparationstate.State{}, fmt.Errorf("metadata: load stored external metadata: %w", err)
		}
		if err == nil {
			storedMeta = externalidentity.StoredMetadataForSource(storedMeta, previousIdentity, ids.SourcePath, sourceFingerprint)
			if !refreshProviders {
				metadata.Bluray = storedMeta.Bluray
				metadata.AniList = storedMeta.AniList
			}
			retainedMetadata = storedMeta
		}
	}
	candidates := externalIdentityCandidateEvidence{}
	tentativeIMDb := false
	previousCategory := normalizeCategory(string(ids.Category))
	categoryPref := resolveCategoryPreference(meta)
	if categoryPref != "" {
		ids.Category, _ = api.NormalizeCanonicalCategory(categoryPref)
	}
	tmdbCategoryPref := normalizeCategory(string(ids.Category))
	logger.Debugf("metadata: external ids start path=%q category=%q", meta.SourcePath, ids.Category)

	applyOverrideID(&ids.TMDBID, &ids.Provenance.TMDB, &ids.Overrides.TMDB, meta.ExternalIDOverrides.TMDBID)
	applyOverrideID(&ids.IMDBID, &ids.Provenance.IMDB, &ids.Overrides.IMDB, meta.ExternalIDOverrides.IMDBID)
	applyOverrideID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Overrides.TVDB, meta.ExternalIDOverrides.TVDBID)
	applyOverrideID(&ids.TVmazeID, &ids.Provenance.TVmaze, &ids.Overrides.TVmaze, meta.ExternalIDOverrides.TVmazeID)
	applyOverrideID(&ids.MALID, &ids.Provenance.MAL, &ids.Overrides.MAL, meta.ExternalIDOverrides.MALID)
	previousMAL := ids.MALID
	if previousCategory != "" && categoryPref != "" && previousCategory != normalizeCategory(categoryPref) {
		clearPreviouslyResolvedProviderIDs(&ids, false)
	}
	effectiveOverrides := effectiveProviderOverrides(meta.ExternalIDOverrides, ids)
	overrideTMDB, clearedTMDB := positiveProviderOverride(effectiveOverrides.TMDBID), clearedProviderOverride(effectiveOverrides.TMDBID)
	overrideIMDB, clearedIMDB := positiveProviderOverride(effectiveOverrides.IMDBID), clearedProviderOverride(effectiveOverrides.IMDBID)
	overrideTVDB, clearedTVDB := positiveProviderOverride(effectiveOverrides.TVDBID), clearedProviderOverride(effectiveOverrides.TVDBID)
	overrideTVmaze, clearedTVmaze := positiveProviderOverride(effectiveOverrides.TVmazeID), clearedProviderOverride(effectiveOverrides.TVmazeID)
	overrideMAL, clearedMAL := positiveProviderOverride(effectiveOverrides.MALID), clearedProviderOverride(effectiveOverrides.MALID)
	if storedIdentityVersion != externalidentity.ContractVersion &&
		(storedIdentityVersion != "" || hasPositiveProviderOverride(effectiveProviderOverrides(api.ExternalIDOverrides{}, ids))) {
		if overrideIMDB && !keepProvider(effectiveOverrides.KeepIMDB) {
			metadata.IMDB = nil
		}
		if overrideTVDB && !keepProvider(effectiveOverrides.KeepTVDB) && metadata.TVDB != nil {
			metadata.TVDB = &api.TVDBMetadata{
				TVDBID:      metadata.TVDB.TVDBID,
				RemoteLinks: metadata.TVDB.RemoteLinks,
			}
		}
	}
	manualSeason, manualEpisode, manualSeasonSet := manualSeasonEpisodeInstructionValues(meta.ReleaseNameOverrides)
	tmdbManualSeason := ""
	if manualSeasonSet {
		tmdbManualSeason = seasonep.FormatSeason(manualSeason)
		if manualSeason == 0 {
			tmdbManualSeason = "S00"
		}
	}
	invalidated := invalidateCandidateDependencies(clearedTMDB || clearedIMDB || clearedTVDB || clearedTVmaze || clearedMAL)
	if invalidated > 0 {
		logger.Debugf("metadata: provider inputs changed decision=resolve_again count=%d", invalidated)
	}
	if previousMAL != 0 && ids.MALID == 0 && meta.MALID == previousMAL {
		meta.MALID = 0
	}

	trackerTMDB, trackerIMDB, trackerTVDB, trackerMAL := resolveTrackerIDs(meta.TrackerData)
	if !overrideTMDB && !clearedTMDB {
		applyDirectProviderID(&ids.TMDBID, &ids.Provenance.TMDB, &ids.Dependencies.TMDB, trackerTMDB, "tracker")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, trackerIMDB, "tracker")
	}
	if !overrideTVDB && !clearedTVDB {
		applyDirectProviderID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB, trackerTVDB, "tracker")
	}
	if !overrideMAL && !clearedMAL {
		applyResolvedID(&ids.MALID, &ids.Provenance.MAL, trackerMAL, "tracker")
	}

	if !overrideTMDB && !clearedTMDB {
		applyDirectProviderID(&ids.TMDBID, &ids.Provenance.TMDB, &ids.Dependencies.TMDB, meta.MediaInfoTMDBID, "mediainfo")
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, meta.MediaInfoIMDBID, "mediainfo")
	}
	if !overrideTVDB && !clearedTVDB {
		applyDirectProviderID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB, meta.MediaInfoTVDBID, "mediainfo")
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
	if !overrideTVmaze && !clearedTVmaze {
		applyResolvedID(&ids.TVmazeID, &ids.Provenance.TVmaze, meta.SceneTVmazeID, "scene")
	}
	if !overrideMAL && !clearedMAL {
		applyResolvedID(&ids.MALID, &ids.Provenance.MAL, meta.SceneMALID, "scene")
	}
	if !overrideMAL && !clearedMAL && applyResolvedID(&ids.MALID, &ids.Provenance.MAL, meta.MALID, "prepared") {
		ids.Dependencies.MAL = api.IdentityDependency{ID: ids.MALID}
	}
	arrSource := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ArrSource), "arr")
	if !overrideTMDB && !clearedTMDB {
		applyDirectProviderID(&ids.TMDBID, &ids.Provenance.TMDB, &ids.Dependencies.TMDB, meta.ArrTMDBID, arrSource)
	}
	if !overrideIMDB && !clearedIMDB {
		applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, meta.ArrIMDBID, arrSource)
	}
	if !overrideTVDB && !clearedTVDB {
		applyDirectProviderID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB, meta.ArrTVDBID, arrSource)
	}
	if !overrideTVmaze && !clearedTVmaze {
		applyDirectProviderID(&ids.TVmazeID, &ids.Provenance.TVmaze, &ids.Dependencies.TVmaze, meta.ArrTVmazeID, arrSource)
	}
	hasExplicitProviderAnchor := hasPositiveProviderOverride(effectiveOverrides)
	if hasExplicitProviderAnchor {
		clearPreviouslyResolvedProviderIDs(&ids, true)
	}
	if !refreshProviders {
		copyRetainedProviderMetadata(&metadata, retainedMetadata, ids, effectiveOverrides)
	}
	logger.Debugf(
		"metadata: external ids initial tmdb=%d(%s) imdb=%d(%s) tvdb=%d(%s)",
		ids.TMDBID,
		ids.Provenance.TMDB,
		ids.IMDBID,
		ids.Provenance.IMDB,
		ids.TVDBID,
		ids.Provenance.TVDB,
	)
	invalidateMismatchedProviderMetadata(&metadata, ids)
	invalidateProviderMetadataForExplicitReconciliation(&metadata, effectiveOverrides)

	tmdbClient, anilistClient, imdbClient, tvdbClient, tvmazeClient := s.ensureExternalClients(ctx)
	animeClient, deferAnimeLookup := anilistClient.(animeResolver)
	if !deferAnimeLookup {
		animeClient, deferAnimeLookup = tmdbClient.(animeResolver)
	}
	// Resolve series links before title searches can choose an unrelated sibling.
	// Saved failures remain suppressed until the source is removed from History.
	var initialTVDBErr error
	if tvdbClient != nil && ids.TVDBID > 0 && shouldUseTVDBForCategory(meta, ids) &&
		(metadata.TVDB == nil || metadata.TVDB.RemoteLinks.Status == "") {
		links, err := tvdbClient.GetSeriesRemoteLinks(ctx, ids.TVDBID)
		initialTVDBErr = err
		if metadata.TVDB == nil && err == nil {
			metadata.TVDB = &api.TVDBMetadata{TVDBID: ids.TVDBID}
		}
		if metadata.TVDB != nil {
			metadata.TVDB.RemoteLinks = links
			metadata.TVDB.RemoteLinks.Links = slices.Clone(links.Links)
		}
		if err := ctx.Err(); err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: TVDB remote links canceled: %w", err)
		}
	}

	var acceptedTMDBLinks providerExternalLinks
	var acceptedTMDBSeriesLinksVerified bool
	var acceptedTVmazeLinks providerExternalLinks
	acceptedTMDBID := 0
	acceptedTVmazeID := 0
	acceptedTMDBMALID := 0
	rejectedTMDBID := 0
	rejectedTVDBIMDbID := 0
	unverifiedTVDBIMDbID := 0
	inferredMALConflict := false
	meta.AuthoritativeMAL = false
	meta.AuthoritativeIMDB = false
	meta.AuthoritativeTVmaze = false
	markIMDbWithdrawal := func() {
		meta.AuthoritativeIMDB = true
		// The resolver may still have the stored TVmaze match even when
		// collection has already dropped the IMDb ID that supplied it.
		if previousIdentity.IMDBID > 0 && previousIdentity.Dependencies.TVmaze.IMDBID == previousIdentity.IMDBID {
			meta.AuthoritativeTVmaze = true
		}
	}
	replayedTVDBLinks := make(map[int]api.TVDBRemoteLinks)
	hasAcceptedTMDBLinks := false
	hasAcceptedTVmazeLinks := false
	keptTMDBLinksAttempted := false
	keptTVmazeLinksAttempted := false
	selectedMALAuthority := overrideTMDB || effectiveOverrides.MALID != nil
	compareSelectedSeriesMAL := overrideTMDB && overrideTVDB && effectiveOverrides.MALID == nil
	linksConflictWithTVDB := func(tmdbLinks, tvmazeLinks providerExternalLinks) bool {
		if ids.TVDBID == 0 || metadata.TVDB == nil || metadata.TVDB.TVDBID != ids.TVDBID {
			return false
		}
		if selectedMALAuthority && metadata.TVDB.RemoteLinks.Status == api.MetadataEvidenceStatusContradictory {
			return true
		}
		links := providerExternalLinks{TVDBID: ids.TVDBID}
		for _, link := range metadata.TVDB.RemoteLinks.Links {
			if link.State != "valid" {
				continue
			}
			if link.Provider == api.IdentityProviderMAL {
				if !providerIDMatchesExplicit(effectiveOverrides.MALID, link.ID) ||
					compareSelectedSeriesMAL &&
						acceptedTMDBID == ids.TMDBID && acceptedTMDBMALID > 0 && acceptedTMDBMALID != link.ID {
					return true
				}
			}
			if !overrideTVDB {
				continue
			}
			switch link.Provider {
			case api.IdentityProviderIMDB:
				if !providerIDMatchesExplicit(effectiveOverrides.IMDBID, link.ID) {
					return true
				}
				links.IMDBID = link.ID
			case api.IdentityProviderTMDB:
				if overrideTMDB && *effectiveOverrides.TMDBID != link.ID {
					return true
				}
			case api.IdentityProviderTVmaze:
				if overrideTVmaze && *effectiveOverrides.TVmazeID != link.ID {
					return true
				}
			case api.IdentityProviderTVDB, api.IdentityProviderMAL:
			}
		}
		return overrideTVDB && (overrideTMDB && providerExternalLinksConflict(tmdbLinks, links) ||
			overrideTVmaze && providerExternalLinksConflict(tvmazeLinks, links))
	}
	linksMatchSelectedIDs := func(links providerExternalLinks) bool {
		return providerIDMatchesExplicit(effectiveOverrides.IMDBID, links.IMDBID) &&
			providerIDMatchesExplicit(effectiveOverrides.TVDBID, links.TVDBID)
	}
	selectedSiblingLinksConflict := func(tmdbLinks, tvmazeLinks providerExternalLinks) bool {
		return overrideTMDB && !linksMatchSelectedIDs(tmdbLinks) ||
			overrideTVmaze && !linksMatchSelectedIDs(tvmazeLinks) ||
			overrideTMDB && overrideTVmaze && providerExternalLinksConflict(tmdbLinks, tvmazeLinks) ||
			linksConflictWithTVDB(tmdbLinks, tvmazeLinks)
	}
	reconcileTVDBMALLinks := func(tmdbID, malID int) {
		if ids.TVDBID == 0 || metadata.TVDB == nil || metadata.TVDB.TVDBID != ids.TVDBID || tmdbID != ids.TMDBID {
			return
		}
		links := metadata.TVDB.RemoteLinks
		if selectedMALAuthority {
			if !hasTVDBMALConflict(links) {
				return
			}
			malID = 0
			if effectiveOverrides.MALID != nil {
				malID = *effectiveOverrides.MALID
			}
		}
		if hasTVDBMALConflict(links) {
			// Persisted conflicting links have no ID. Replay the raw series evidence
			// and restore the MAL namespace if the new scope has no disagreement.
			raw, replayed := replayedTVDBLinks[ids.TVDBID]
			if !replayed && tvdbClient != nil {
				fetched, err := tvdbClient.GetSeriesRemoteLinks(ctx, ids.TVDBID)
				if err == nil {
					raw = fetched
				}
				replayedTVDBLinks[ids.TVDBID] = raw
			}
			links = restoreAgreeingTVDBMALLinks(links, raw, malID, effectiveOverrides.MALID != nil)
		}
		current := *metadata.TVDB
		current.RemoteLinks = links
		if selectedMALAuthority {
			metadata.TVDB = &current
			return
		}
		copiedLinks := false
		for _, link := range links.Links {
			if malID > 0 && link.Provider == api.IdentityProviderMAL && link.State == "valid" && link.ID != malID {
				if !copiedLinks {
					if !hasTVDBMALConflict(links) {
						replayedTVDBLinks[ids.TVDBID] = links
					}
					current.RemoteLinks.Links = slices.Clone(links.Links)
					copiedLinks = true
				}
				markTVDBLinkConflict(&current, api.IdentityProviderMAL, link.ID)
			}
		}
		metadata.TVDB = &current
		inferredMALConflict = hasTVDBMALConflict(current.RemoteLinks)
		if inferredMALConflict {
			withdrawAutomaticAnimeMAL(&meta, &ids)
			// A dependency carried into this preparation must pass the same
			// admission rule as a newly discovered TVDB sibling.
			if previousIdentity.TVDBID == ids.TVDBID || previousIdentity.Dependencies.IMDB.TVDBID == ids.TVDBID ||
				previousIdentity.Dependencies.TVmaze.TVDBID == ids.TVDBID {
				prior := previousIdentity
				withdrawConflictingTVDBSiblings(&prior, ids.TVDBID)
				recordWithdrawnStoredIDs(previousIdentity, prior)
			}
			withdrawConflictingTVDBSiblings(&ids, ids.TVDBID)
			invalidateMismatchedProviderMetadata(&metadata, ids)
		}
	}
	reconcileFetchedTMDBMALLinks := func() {
		if !selectedMALAuthority && hasAcceptedTMDBLinks && acceptedTMDBMALID > 0 {
			reconcileTVDBMALLinks(acceptedTMDBID, acceptedTMDBMALID)
		}
	}
	replayKeptSeriesLinks := func() {
		if !shouldUseTVDBForCategory(meta, ids) {
			return
		}
		needed := ids.IMDBID == 0 && effectiveOverrides.IMDBID == nil || ids.TVDBID == 0 && effectiveOverrides.TVDBID == nil ||
			ids.TMDBID == 0 && effectiveOverrides.TMDBID == nil || ids.TVmazeID == 0 && effectiveOverrides.TVmazeID == nil
		if metadata.TVDB != nil && metadata.TVDB.TVDBID == ids.TVDBID {
			targets := tvdbSiblingTargets(&ids, effectiveOverrides)
			for _, link := range metadata.TVDB.RemoteLinks.Links {
				if target, ok := targets[link.Provider]; ok && link.State == "valid" && link.ID > 0 && *target.id == 0 && target.override == nil {
					needed = true
				}
			}
		}
		providers := []struct {
			name       string
			client     any
			id         int
			usable     bool
			keep       *bool
			attempted  *bool
			accepted   *bool
			links      *providerExternalLinks
			acceptedID *int
			dependency api.IdentityDependency
			matches    func(*int, int) bool
		}{
			{
				"TMDB",
				tmdbClient,
				ids.TMDBID,
				usableTMDBMetadata(metadata.TMDB, ids.TMDBID, ids.Category),
				effectiveOverrides.KeepTMDB,
				&keptTMDBLinksAttempted,
				&hasAcceptedTMDBLinks,
				&acceptedTMDBLinks,
				&acceptedTMDBID,
				api.IdentityDependency{TMDBID: ids.TMDBID},
				providerIDMatchesExplicit,
			},
			{
				"TVmaze",
				tvmazeClient,
				ids.TVmazeID,
				usableTVmazeMetadata(metadata.TVmaze, ids.TVmazeID),
				effectiveOverrides.KeepTVmaze,
				&keptTVmazeLinksAttempted,
				&hasAcceptedTVmazeLinks,
				&acceptedTVmazeLinks,
				&acceptedTVmazeID,
				api.IdentityDependency{TVmazeID: ids.TVmazeID},
				providerIDVerifiesExplicit,
			},
		}
		for _, provider := range providers {
			if !needed || !provider.usable || !keepProvider(provider.keep) || *provider.attempted || *provider.accepted {
				continue
			}
			*provider.attempted = true
			reader, ok := provider.client.(seriesExternalIDsClient)
			if !ok {
				continue
			}
			imdbID, tvdbID, err := reader.GetSeriesExternalIDs(ctx, provider.id)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				appendIdentityCorrectionWarning(&meta, provider.name+" kept identity links are unavailable; select the missing sibling IDs manually.")
				continue
			}
			*provider.links = providerExternalLinks{IMDBID: imdbID, TVDBID: tvdbID}
			*provider.acceptedID = provider.id
			*provider.accepted = true
			if provider.name == "TMDB" && !deferAnimeLookup {
				acceptedTMDBMALID = metadata.TMDB.MALID
			}
		}
		if overrideTMDB && !hasAcceptedTMDBLinks || overrideTVmaze && !hasAcceptedTVmazeLinks ||
			selectedSiblingLinksConflict(acceptedTMDBLinks, acceptedTVmazeLinks) {
			return
		}
		for _, provider := range providers {
			if !keepProvider(provider.keep) || !*provider.accepted || provider.id <= 0 || provider.id != *provider.acceptedID {
				continue
			}
			if !provider.matches(effectiveOverrides.IMDBID, provider.links.IMDBID) || !provider.matches(effectiveOverrides.TVDBID, provider.links.TVDBID) {
				continue
			}
			if !tentativeIMDb && effectiveOverrides.IMDBID == nil && ids.IMDBID == 0 && provider.links.IMDBID > 0 &&
				provider.links.IMDBID != rejectedTVDBIMDbID &&
				provider.links.IMDBID != unverifiedTVDBIMDbID {
				applyLinkedProviderID(&ids.IMDBID, &ids.Provenance.IMDB, &ids.Dependencies.IMDB, provider.links.IMDBID, provider.dependency)
			}
			if effectiveOverrides.TVDBID == nil && ids.TVDBID == 0 && provider.links.TVDBID > 0 {
				applyLinkedProviderID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB, provider.links.TVDBID, provider.dependency)
			}
		}
	}
	withdrawSelectedConflictSiblings := func(identity *api.ExternalIdentity) {
		for _, provider := range []struct {
			id         *int
			provenance *api.IdentityProvenance
			dependency api.IdentityDependency
			override   *int
		}{
			{&identity.TMDBID, &identity.Provenance.TMDB, identity.Dependencies.TMDB, effectiveOverrides.TMDBID},
			{&identity.IMDBID, &identity.Provenance.IMDB, identity.Dependencies.IMDB, effectiveOverrides.IMDBID},
			{&identity.TVDBID, &identity.Provenance.TVDB, identity.Dependencies.TVDB, effectiveOverrides.TVDBID},
			{&identity.TVmazeID, &identity.Provenance.TVmaze, identity.Dependencies.TVmaze, effectiveOverrides.TVmazeID},
		} {
			if provider.override == nil && provider.dependency.ID > 0 && !independentlyResolved(*provider.id, provider.dependency) {
				clearInferredProviderID(provider.id, provider.provenance)
			}
		}
		if (!overrideTMDB || identity.Dependencies.MAL.TMDBID == 0) && effectiveOverrides.MALID == nil && identity.Dependencies.MAL.ID > 0 &&
			!independentlyResolved(identity.MALID, identity.Dependencies.MAL) {
			clearInferredProviderID(&identity.MALID, &identity.Provenance.MAL)
		}
		invalidateIdentityDependencies(identity, false)
	}
	reconcileSelectedSiblingConflict := func() {
		if !selectedSiblingLinksConflict(acceptedTMDBLinks, acceptedTVmazeLinks) {
			return
		}
		if linksConflictWithTVDB(acceptedTMDBLinks, acceptedTVmazeLinks) {
			appendIdentityCorrectionWarning(&meta, tvdbSiblingConflictWarning)
		}
		prior := previousIdentity
		withdrawSelectedConflictSiblings(&prior)
		recordWithdrawnStoredIDs(previousIdentity, prior)
		withdrawSelectedConflictSiblings(&ids)
		invalidateMismatchedProviderMetadata(&metadata, ids)
	}
	animeLookupFailed := false
	applyTVDBLinks := func(bootstrap bool) {
		markTVDBLinkConflict(metadata.TVDB, api.IdentityProviderTMDB, rejectedTMDBID)
		if selectedMALAuthority {
			reconcileTVDBMALLinks(ids.TMDBID, 0)
		} else {
			reconcileFetchedTMDBMALLinks()
		}
		replayKeptSeriesLinks()
		reconcileSelectedSiblingConflict()
		// Wait for selected resources' raw links before choosing an unspecified sibling.
		if overrideTMDB && !hasAcceptedTMDBLinks || overrideTVmaze && !hasAcceptedTVmazeLinks ||
			selectedSiblingLinksConflict(acceptedTMDBLinks, acceptedTVmazeLinks) {
			return
		}
		linkOverrides := effectiveOverrides
		if tentativeIMDb {
			linkOverrides.IMDBID = new(0)
		}
		if animeLookupFailed {
			linkOverrides.MALID = new(0)
		}
		if bootstrap && !selectedMALAuthority && deferAnimeLookup && tmdbClient != nil && !clearedTMDB &&
			metadata.TVDB != nil && slices.ContainsFunc(metadata.TVDB.RemoteLinks.Links, isUsableTVDBMALLink) {
			// A TVDB link can identify TMDB before title collection. The other
			// siblings wait until AniList can check the series' MAL evidence.
			if ids.TMDBID != 0 || metadata.TVDB.RemoteLinks.Status == api.MetadataEvidenceStatusContradictory {
				return
			}
			linkOverrides.IMDBID, linkOverrides.TVmazeID, linkOverrides.MALID = new(0), new(0), new(0)
		}
		applyTVDBSeriesLinks(&meta, &ids, metadata, linkOverrides, unverifiedTVDBIMDbID)
	}
	applyTVDBLinks(true)
	if err := ctx.Err(); err != nil {
		return preparationstate.State{}, fmt.Errorf("metadata: kept identity links canceled: %w", err)
	}

	filename, secondary := resolveSearchTitles(meta)
	year := resolveSearchYear(meta)
	// Provider confidence thresholds apply in every mode; the CLI owns required selection.
	explicitCategory := meta.ReleaseNameOverrides.Category != nil && normalizeCategory(*meta.ReleaseNameOverrides.Category) != "" ||
		ids.Overrides.Category == api.OverrideStateValue || ids.Provenance.Category == api.IdentityProvenanceExplicit
	imdbSearchCategory := ids.Category
	searchIMDb := func(stageYear int) imdb.SearchResult {
		imdbSearchCategory = ids.Category
		result, err := imdbClient.Search(ctx, imdb.SearchInput{
			Filename:          filename,
			AlternateTitle:    normalizeSearchTitle(meta.Release.Alt),
			Subtitle:          normalizeSearchTitle(meta.Release.Subtitle),
			UntouchedFilename: pathutil.Base(meta.SourcePath),
			SearchYear:        stageYear,
			Category:          string(ids.Category),
			Debug:             false,
		})
		if err != nil {
			logger.Warnf("metadata: imdb search failed (year=%d): %v", stageYear, err)
			return imdb.SearchResult{}
		}
		if result.IMDbID != 0 {
			applyResolvedID(&ids.IMDBID, &ids.Provenance.IMDB, result.IMDbID, "imdb_search")
			ids.Dependencies.IMDB = api.IdentityDependency{ID: ids.IMDBID}
		}
		if len(result.Candidates) > 0 && len(candidates.IMDB) == 0 {
			candidates.IMDB = mapIMDBCandidates(result.Candidates)
			if result.Tentative && len(candidates.IMDB) > 0 {
				tentativeIMDb = true
				for _, candidate := range result.Candidates {
					if candidate.IMDbID == candidates.IMDB[0].ID {
						candidates.IMDB[0].TentativeConfidence = new(candidate.Confidence)
						break
					}
				}
			}
		}
		return result
	}
	retryIMDbCategorySearch := func() {
		if imdbClient == nil || ids.IMDBID != 0 || len(candidates.IMDB) > 0 ||
			hasExplicitProviderAnchor || ids.TVDBID != 0 && shouldUseTVDBForCategory(meta, ids) || overrideIMDB || clearedIMDB ||
			imdbSearchCategory == ids.Category {
			return
		}
		for _, stageYear := range buildSearchYears(year) {
			searchIMDb(stageYear)
			if ids.IMDBID != 0 || len(candidates.IMDB) > 0 {
				break
			}
		}
	}
	acceptTMDBCategory := func(value string) bool {
		category := normalizeCategory(value)
		if category == "" {
			return true
		}
		if explicitCategory && category != tmdbCategoryPref {
			return false
		}
		if category != tmdbCategoryPref {
			logger.Debugf("metadata: category resolved previous=%s category=%s source=tmdb", tmdbCategoryPref, category)
			directIMDb := slices.Contains([]api.IdentityProvenance{
				api.IdentityProvenanceMediaInfo, api.IdentityProvenanceScene, api.IdentityProvenanceArr, api.IdentityProvenanceTracker,
			}, ids.Provenance.IMDB)
			if !overrideIMDB && !clearedIMDB && !directIMDb {
				clearInferredProviderID(&ids.IMDBID, &ids.Provenance.IMDB)
				ids.Dependencies.IMDB = api.IdentityDependency{}
				candidates.IMDB = nil
				tentativeIMDb = false
			}
			ids.Category, _ = api.NormalizeCanonicalCategory(category)
			ids.Provenance.Category = api.IdentityProvenanceProvider
			tmdbCategoryPref = category
			invalidateCandidateDependencies(false)
			invalidateMismatchedProviderMetadata(&metadata, ids)
		}
		return true
	}
	confirmedTentativeIMDb := false
	lastTMDBLookupIMDB := 0
	lastTMDBLookupTVDB := 0
	resolveTMDBFromProviderIDs := func() {
		if tmdbClient == nil || overrideTMDB || clearedTMDB || ids.TMDBID != 0 || ids.IMDBID == 0 && ids.TVDBID == 0 {
			return
		}
		if ids.IMDBID == lastTMDBLookupIMDB && ids.TVDBID == lastTMDBLookupTVDB {
			return
		}
		if selectedSiblingLinksConflict(acceptedTMDBLinks, acceptedTVmazeLinks) {
			return
		}
		if metadata.TVDB != nil && metadata.TVDB.TVDBID == ids.TVDBID && metadata.TVDB.RemoteLinks.Status == api.MetadataEvidenceStatusContradictory {
			return
		}
		lastTMDBLookupIMDB = ids.IMDBID
		lastTMDBLookupTVDB = ids.TVDBID
		logger.Debugf("metadata: external ids lookup tmdb from imdb=%d tvdb=%d", ids.IMDBID, ids.TVDBID)
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
			RequireExternalIDAgreement: hasExplicitProviderAnchor || ids.TVDBID != 0 && shouldUseTVDBForCategory(meta, ids),
			Debug:                      false,
		})
		if err != nil {
			logger.Warnf("metadata: tmdb external lookup failed: %v", err)
		} else if result.TMDBID != 0 &&
			(!result.FilenameSearch || !confirmedTentativeIMDb && !hasExplicitProviderAnchor && (ids.TVDBID == 0 || !shouldUseTVDBForCategory(meta, ids))) &&
			(!confirmedTentativeIMDb || normalizeCategory(result.Category) == normalizeCategory(string(ids.Category))) &&
			(confirmedTentativeIMDb || acceptTMDBCategory(result.Category)) {
			inputs := api.IdentityDependency{}
			if !result.FilenameSearch {
				inputs.IMDBID = ids.IMDBID
				if shouldUseTVDBForCategory(meta, ids) {
					inputs.TVDBID = ids.TVDBID
				}
			}
			applyLinkedProviderID(&ids.TMDBID, &ids.Provenance.TMDB, &ids.Dependencies.TMDB, result.TMDBID, inputs)
		}
		if result.ExternalIDConflict {
			appendIdentityCorrectionWarning(&meta, "TMDB could not reconcile the supplied IMDb and TVDB IDs. Correct one of the supplied provider IDs.")
		}
		if len(result.Candidates) > 0 && len(candidates.TMDB) == 0 {
			candidates.TMDB = mapTMDBCandidates(result.Candidates, result.Category)
		}
	}
	resolveTMDBFromProviderIDs()

	titleMatchedTVTMDBID := 0
	needsTMDBSearch := tmdbClient != nil && !overrideTMDB && !clearedTMDB && ids.TMDBID == 0 && !hasExplicitProviderAnchor &&
		(ids.TVDBID == 0 || !shouldUseTVDBForCategory(meta, ids))
	needsIMDBSearch := !overrideIMDB && !clearedIMDB && ids.IMDBID == 0 && !hasExplicitProviderAnchor &&
		(ids.TVDBID == 0 || !shouldUseTVDBForCategory(meta, ids))

	if needsTMDBSearch || needsIMDBSearch {
		searchYears := buildSearchYears(year)
		logger.Debugf(
			"metadata: external ids search filename=%q year=%d stages=%v category=%q tmdb=%t imdb=%t",
			filename,
			year,
			searchYears,
			tmdbCategoryPref,
			needsTMDBSearch,
			needsIMDBSearch,
		)

		for _, stageYear := range searchYears {
			if ids.TMDBID != 0 && ids.IMDBID != 0 {
				break
			}

			// Unresolved candidates require selection, not a different year's match.
			stageNeedsTMDB := tmdbClient != nil && !overrideTMDB && !clearedTMDB && ids.TMDBID == 0 && len(candidates.TMDB) == 0
			stageNeedsIMDB := !overrideIMDB && !clearedIMDB && ids.IMDBID == 0 && len(candidates.IMDB) == 0
			if !stageNeedsTMDB && !stageNeedsIMDB {
				break
			}

			logger.Debugf(
				"metadata: external ids search stage year=%d tmdb_needed=%t imdb_needed=%t",
				stageYear,
				stageNeedsTMDB,
				stageNeedsIMDB,
			)

			var tmdbOutcome tmdb.SearchOutcome
			var imdbResult imdb.SearchResult
			var tmdbErr error
			if stageNeedsTMDB {
				tmdbOutcome, tmdbErr = tmdbClient.SearchID(ctx, tmdb.SearchInput{
					Filename:       filename,
					SecondaryTitle: secondary,
					SearchYear:     stageYear,
					Category:       tmdbCategoryPref,
					DontSwitch:     explicitCategory || strings.EqualFold(tmdbCategoryPref, "TV"),
					Debug:          false,
				})
			}
			if tmdbErr != nil {
				logger.Warnf("metadata: tmdb search failed (year=%d): %v", stageYear, tmdbErr)
			}
			if stageNeedsTMDB && tmdbOutcome.TMDBID != 0 && acceptTMDBCategory(tmdbOutcome.Category) {
				applyResolvedID(&ids.TMDBID, &ids.Provenance.TMDB, tmdbOutcome.TMDBID, "tmdb_search")
				ids.Dependencies.TMDB = api.IdentityDependency{ID: ids.TMDBID}
				// A sole TMDB result can be selected even below the title-match threshold.
				if tmdbOutcome.AutoSelected && normalizeCategory(tmdbOutcome.Category) == "TV" && len(tmdbOutcome.Candidates) > 0 &&
					tmdbOutcome.Candidates[0].TMDBID == ids.TMDBID && tmdbOutcome.Candidates[0].Similarity >= 0.75 {
					titleMatchedTVTMDBID = ids.TMDBID
				}
			}
			if stageNeedsTMDB && len(tmdbOutcome.Candidates) > 0 && len(candidates.TMDB) == 0 {
				candidates.TMDB = mapTMDBCandidates(tmdbOutcome.Candidates, tmdbOutcome.Category)
			}
			// IMDb selection must use the category of the selected TMDB resource.
			stageNeedsIMDB = !overrideIMDB && !clearedIMDB && ids.IMDBID == 0 && len(candidates.IMDB) == 0
			if stageNeedsIMDB {
				imdbResult = searchIMDb(stageYear)
			}
			logger.Debugf(
				"metadata: external ids search stage result year=%d tmdb=%d imdb=%d",
				stageYear,
				tmdbOutcome.TMDBID,
				imdbResult.IMDbID,
			)
		}
	}
	resolveTMDBFromProviderIDs()
	retryIMDbCategorySearch()

	var tmdbErr error
	var imdbErr error
	tvdbErr := initialTVDBErr
	var tvmazeErr error
	var anilistErr error

	isTVForTVmaze := func() bool {
		return shouldUseTVDBForCategory(meta, ids)
	}

	logoLanguages, unsupportedLogoLanguages := config.ParseLogoLanguages(s.cfg.Description.LogoLanguage)
	logoPreference := strings.Join(logoLanguages, ",")
	if len(unsupportedLogoLanguages) > 0 {
		ignored := redaction.RedactValue(strings.Join(unsupportedLogoLanguages, ", "), nil)
		logger.Warnf("metadata: unsupported description_settings.logo_language entries ignored: %s", ignored)
	}
	logoClient, canSelectLogo := tmdbClient.(logoArtworkClient)
	tmdbLogoFetchAttempted := false
	tmdbMetadataFetchedID := 0
	imdbMetadataFetchedID := 0
	tmdbAnchorVerificationAttempted := false
	tmdbMetadataRejected := false
	tvdbMetadataRejected := false
	tvmazeMetadataRejected := false
	tvmazeAnchorVerificationAttempted := false
	tvmazeMetadataFetchAttempted := false
	tvdbMetadataFetchAttempted := false
	tvdbDisambiguationFetchAttempted := false
	episodeProviderIDs := func() api.ExternalIdentity {
		downstream := ids
		if tmdbMetadataRejected {
			downstream.TMDBID = 0
		}
		if tvdbMetadataRejected {
			downstream.TVDBID = 0
		}
		if tvmazeMetadataRejected {
			downstream.TVmazeID = 0
		}
		return downstream
	}
	rejectTMDBMetadata := func() {
		previousID := ids.TMDBID
		metadata.TMDB = nil
		clearInferredProviderID(&ids.TMDBID, &ids.Provenance.TMDB)
		tmdbMetadataRejected = ids.TMDBID != 0
		if previousID > 0 && ids.TMDBID == 0 {
			rejectedTMDBID = previousID
			markTVDBLinkConflict(metadata.TVDB, api.IdentityProviderTMDB, previousID)
		}
	}
	shouldFetchTMDBMetadata := func() bool {
		if tmdbClient == nil || ids.TMDBID == 0 {
			return false
		}
		if refreshProviders && tmdbMetadataFetchedID == ids.TMDBID {
			return false
		}
		if !refreshProviders && keepProvider(effectiveOverrides.KeepTMDB) && usableTMDBMetadata(metadata.TMDB, ids.TMDBID, ids.Category) &&
			!requiresProviderMetadataRefresh(meta.MetadataRequirements, ids.Category, meta, ids, metadata, api.IdentityProviderTMDB) {
			return false
		}
		if overrideTMDB {
			return !tmdbAnchorVerificationAttempted
		}
		if tentativeIMDb && tmdbMetadataFetchedID != ids.TMDBID {
			// Retained title fields may echo earlier inputs. Replay the raw link.
			return true
		}
		if !usableTMDBMetadata(metadata.TMDB, ids.TMDBID, ids.Category) {
			return true
		}
		if requiresProviderMetadataRefresh(meta.MetadataRequirements, ids.Category, meta, ids, metadata, api.IdentityProviderTMDB) {
			return true
		}
		return !canSelectLogo && strings.TrimSpace(metadata.TMDB.Logo) == "" && !metadata.TMDB.LogoLookupAttempted && !tmdbLogoFetchAttempted
	}
	imdbSpecialSeason, imdbSpecialEpisode := meta.HasExplicitSeasonZero(), meta.EpisodeInt
	if meta.ReleaseNameOverrides.Season != nil {
		imdbSpecialSeason = manualSeasonSet && manualSeason == 0
	}
	if meta.ReleaseNameOverrides.Episode != nil {
		imdbSpecialEpisode = manualEpisode
	}
	imdbSeasonReplayID := 0
	shouldFetchIMDBMetadata := func() bool {
		if imdbClient == nil || ids.IMDBID == 0 ||
			(refreshProviders || imdbSeasonReplayID == ids.IMDBID) && imdbMetadataFetchedID == ids.IMDBID {
			return false
		}
		if refreshProviders {
			return true
		}
		// Replay raw evidence once when a normalized snapshot cannot prove the
		// requested special's season. Reuse still suppresses empty/failed queries.
		if imdbSpecialSeason && imdbSpecialEpisode > 0 && !meta.TVPack && metadata.IMDB != nil &&
			slices.ContainsFunc(metadata.IMDB.Episodes, func(episode api.IMDBEpisode) bool {
				return !episode.SeasonKnown && episode.Season == 0 && parseIMDBEpisodeNumber(episode.EpisodeText) == imdbSpecialEpisode
			}) {
			imdbSeasonReplayID = ids.IMDBID
			return true
		}
		// Reopening a movie can reuse its IMDb identity, but older title
		// snapshots need the new runtime evidence before comparing editions.
		if meta.ExternalFreshness == api.ExternalFreshnessLoad && ids.Category == api.CanonicalCategoryMovie &&
			metadata.IMDB != nil && metadata.IMDB.EditionRuntimes == nil && imdbMetadataFetchedID != ids.IMDBID {
			return true
		}
		return !usableIMDBMetadata(metadata.IMDB, ids.IMDBID) ||
			requiresProviderMetadataRefresh(meta.MetadataRequirements, ids.Category, meta, ids, metadata, api.IdentityProviderIMDB)
	}
	shouldFetchTVDBMetadata := func() bool {
		if !refreshProviders && keepProvider(effectiveOverrides.KeepTVDB) && usableTVDBMetadata(metadata.TVDB, ids.TVDBID) &&
			!requiresProviderMetadataRefresh(meta.MetadataRequirements, ids.Category, meta, ids, metadata, api.IdentityProviderTVDB) {
			return false
		}
		return tvdbClient != nil && shouldUseTVDBForCategory(meta, ids) && ids.TVDBID != 0 &&
			!tvdbMetadataFetchAttempted &&
			(refreshProviders || !usableTVDBMetadata(metadata.TVDB, ids.TVDBID) ||
				requiresProviderMetadataRefresh(meta.MetadataRequirements, ids.Category, meta, ids, metadata, api.IdentityProviderTVDB) ||
				!metadata.TVDB.PosterThumbnailLookupAttempted)
	}
	shouldRefreshTVDBDisambiguation := func() bool {
		return tvdbClient != nil && shouldUseTVDBForCategory(meta, ids) && ids.TVDBID != 0 &&
			(refreshProviders || !keepProvider(effectiveOverrides.KeepTVDB)) &&
			!tvdbDisambiguationFetchAttempted && usableTVDBMetadata(metadata.TVDB, ids.TVDBID) &&
			!reusableTVDBNameDisambiguation(metadata.TVDB)
	}
	shouldFetchTVmazeMetadata := func() bool {
		if tvmazeMetadataFetchAttempted && metadata.TVmaze != nil && metadata.TVmaze.TVmazeID == ids.TVmazeID {
			return false
		}
		if tvmazeClient == nil || !isTVForTVmaze() || ids.TVmazeID == 0 {
			return false
		}
		if refreshProviders && tvmazeMetadataFetchAttempted {
			return false
		}
		if refreshProviders {
			return true
		}
		if keepProvider(effectiveOverrides.KeepTVmaze) && usableTVmazeMetadata(metadata.TVmaze, ids.TVmazeID) &&
			!requiresProviderMetadataRefresh(meta.MetadataRequirements, ids.Category, meta, ids, metadata, api.IdentityProviderTVmaze) {
			return false
		}
		if overrideTVmaze {
			return !tvmazeAnchorVerificationAttempted
		}
		return tentativeIMDb && !tvmazeMetadataFetchAttempted || !usableTVmazeMetadata(metadata.TVmaze, ids.TVmazeID) ||
			requiresProviderMetadataRefresh(meta.MetadataRequirements, ids.Category, meta, ids, metadata, api.IdentityProviderTVmaze) ||
			!metadata.TVmaze.BackdropLookupAttempted && !tvmazeMetadataFetchAttempted
	}

	shouldRunFetchPass := func() bool {
		if shouldFetchTMDBMetadata() {
			return true
		}
		if shouldFetchIMDBMetadata() || shouldFetchTVDBMetadata() || shouldRefreshTVDBDisambiguation() || shouldFetchTVmazeMetadata() {
			return true
		}
		if shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && !clearedTVDB && ids.TVDBID == 0 && (ids.IMDBID != 0 || ids.TMDBID != 0) {
			return true
		}
		if isTVForTVmaze() &&
			(shouldFetchTVmazeMetadata() || (!overrideTVmaze && !clearedTVmaze && ids.TVmazeID == 0 && (ids.IMDBID != 0 || ids.TVDBID != 0))) {
			return true
		}
		return false
	}

	tmdbInput := func() tmdb.MetadataInput {
		input := tmdb.MetadataInput{
			TMDBID:        ids.TMDBID,
			Category:      string(ids.Category),
			SearchYear:    year,
			IMDbID:        ids.IMDBID,
			TVDBID:        ids.TVDBID,
			AddLogo:       true,
			LogoLanguages: logoLanguages,
			Filename:      pathutil.Base(meta.SourcePath),
		}
		if ids.Category != api.CanonicalCategoryMovie {
			input.ManualSeason, input.Season = tmdbManualSeason, meta.SeasonStr
		}
		return input
	}

	type animeRankingKey struct {
		id, year     int
		title, scope string
	}
	var rankedAnime animeRankingKey
	var rankedTMDB *api.TMDBMetadata
	animeWarning := ""
	reconcileAnimeEvidence := func(season string) error {
		current := metadata.TMDB
		if current == nil || current.TMDBID != ids.TMDBID || !current.Anime || overrideMAL || clearedMAL ||
			ids.MALID > 0 && keepProvider(effectiveOverrides.KeepMAL) {
			return nil
		}
		scope := animeRankingScope(ids.Category, season)
		if !deferAnimeLookup {
			if tmdbMetadataFetchedID == ids.TMDBID {
				anime := *current
				anime.AnimeSeason = scope
				metadata.TMDB = &anime
			}
			return nil
		}
		key := animeRankingKey{
			id:    ids.TMDBID,
			year:  current.Year,
			title: current.Title,
			scope: scope,
		}
		if key != rankedAnime {
			rankedAnime = key
			animeWarning = ""
			anime := *current
			previous := retainedMetadata.TMDB
			sameScope := sourceScopedMetadataMatches(retainedMetadata.SourcePath, ids.SourcePath) &&
				retainedMetadata.UpdatedAt.Equal(previousIdentity.ResolvedAt) && previousIdentity.Resolution.SourceFingerprint == sourceFingerprint &&
				usableTMDBMetadata(previous, ids.TMDBID, ids.Category) && previous.Anime && previous.Title == anime.Title &&
				previous.Year == anime.Year && previous.AnimeSeason == scope
			resetMAL := ids.MALID == 0 && slices.Contains(meta.IdentityResetFields, api.CorrectionFieldIdentityMAL)
			// Collection replays raw search evidence for the current ranking scope.
			if sameScope && !previous.AnimeLookupPending && !refreshProviders && !resetMAL && !evidence.Enabled(ctx) {
				copyAnimeRanking(&anime, previous)
			} else {
				input := tmdbInput()
				if ids.Category != api.CanonicalCategoryMovie {
					input.Season = season
				}
				result, err := animeClient.ResolveAnime(ctx, anime.Title, input)
				if ctx.Err() != nil {
					return fmt.Errorf("metadata: anime resolution canceled: %w", ctx.Err())
				}
				animeLookupFailed = err != nil || result.LookupFailed
				switch {
				case !animeLookupFailed:
					anime.MALID, anime.AnimeSeason = result.MALID, scope
					anime.RetrievedAKA, anime.Demographic = "", ""
					if result.MALID > 0 {
						anime.RetrievedAKA = tmdb.FormatRetrievedAKA(anime.Title, result.Romaji, anime.Year)
						anime.Demographic = result.Demographic
					}
					if ids.MALID == 0 || ids.Dependencies.MAL.TMDBID == ids.TMDBID {
						withdrawAutomaticAnimeMAL(&meta, &ids)
					}
				case sameScope && !resetMAL:
					copyAnimeRanking(&anime, previous)
					animeWarning = "AniList title search failed; kept the previous anime metadata for this release and season."
					if ids.MALID == 0 && previousIdentity.MALID == previous.MALID {
						applyLinkedProviderID(&ids.MALID, &ids.Provenance.MAL, &ids.Dependencies.MAL, previous.MALID, previousIdentity.Dependencies.MAL)
					}
					if metadata.AniList == nil && retainedMetadata.AniList != nil && retainedMetadata.AniList.MALID == ids.MALID {
						metadata.AniList = retainedMetadata.AniList
					}
				default:
					anime.MALID, anime.AnimeSeason, anime.RetrievedAKA, anime.Demographic = 0, "", "", ""
					animeWarning = "AniList title search failed; automatic anime metadata is unavailable until the lookup succeeds."
					withdrawAutomaticAnimeMAL(&meta, &ids)
					invalidateMismatchedProviderMetadata(&metadata, ids)
				}
				anime.AnimeLookupPending = animeLookupFailed
			}
			rankedTMDB = &anime
			metadata.TMDB = rankedTMDB
		} else if current != rankedTMDB {
			anime := *current
			copyAnimeRanking(&anime, rankedTMDB)
			metadata.TMDB = &anime
			rankedTMDB = &anime
		}
		if !animeLookupFailed || metadata.TMDB.MALID > 0 {
			reconcileTVDBMALLinks(ids.TMDBID, metadata.TMDB.MALID)
		}
		if compareSelectedSeriesMAL && acceptedTMDBID == ids.TMDBID {
			acceptedTMDBMALID = metadata.TMDB.MALID
		}
		return nil
	}
	type episodeMappingKey struct {
		ids         [4]int
		imdb        *api.IMDBMetadata
		seriesTitle string
	}
	var mappedAnimeKey episodeMappingKey
	mappedAnimeSeason := ""
	mapAnimeSeasonForTVDBVote := func(preliminary bool) (string, bool) {
		if ids.Category != api.CanonicalCategoryTV || !deferAnimeLookup || manualSeasonSet || meta.HasExplicitSeasonZero() ||
			metadata.TMDB == nil || !metadata.TMDB.Anime || metadata.TVDB == nil || tvdbClient == nil || meta.TVPack {
			return "", false
		}
		links := metadata.TVDB.RemoteLinks
		if !slices.ContainsFunc(links.Links, isUsableTVDBMALLink) && !hasTVDBMALConflict(links) {
			return "", false
		}
		_, episode := meta.SeasonEpisodeWithParsedFallback()
		if episodeLookupDate(meta) == "" && episode == 0 && manualEpisode == 0 && seasonep.Extract(meta.SourcePath, meta).AbsoluteEpisode == 0 {
			return "", false
		}
		// A sibling supplied by TVDB cannot help select the MAL ID that checks TVDB's vote.
		episodeIDs := episodeProviderIDs()
		episodeMetadata := metadata
		tvdbID := episodeIDs.TVDBID
		if tvdbID == 0 {
			return "", false
		}
		imdbFromTVDB := episodeIDs.IMDBID != 0 && episodeIDs.Dependencies.IMDB.TVDBID == tvdbID
		tvmazeFromTVDB := episodeIDs.TVmazeID != 0 && (episodeIDs.Dependencies.TVmaze.TVDBID == tvdbID ||
			imdbFromTVDB && episodeIDs.Dependencies.TVmaze.IMDBID == episodeIDs.IMDBID)
		imdbFromTVmaze := tvmazeFromTVDB && episodeIDs.Dependencies.IMDB.TVmazeID == episodeIDs.TVmazeID
		tmdbFromTVDB := episodeIDs.TMDBID != 0 && (episodeIDs.Dependencies.TMDB.TVDBID == tvdbID ||
			episodeIDs.IMDBID != 0 && (imdbFromTVDB || imdbFromTVmaze) && episodeIDs.Dependencies.TMDB.IMDBID == episodeIDs.IMDBID ||
			tvmazeFromTVDB && episodeIDs.Dependencies.TMDB.TVmazeID == episodeIDs.TVmazeID)
		if !preliminary && !imdbFromTVDB && !tvmazeFromTVDB && !tmdbFromTVDB {
			return "", false
		}
		// The final pass can use an IMDb episode mapping to disqualify the TVDB
		// vote that corroborated IMDb, provided the mapping did not come via TVmaze.
		if preliminary && imdbFromTVDB || imdbFromTVmaze {
			episodeIDs.IMDBID = 0
			episodeMetadata.IMDB = nil
		}
		if tvmazeFromTVDB {
			episodeIDs.TVmazeID = 0
			episodeMetadata.TVmaze = nil
		}
		if tmdbFromTVDB {
			episodeIDs.TMDBID = 0
			episodeMetadata.TMDB = nil
		}
		// IMDb episode evidence can arrive in a later fetch pass for the same ID.
		key := episodeMappingKey{
			ids:         [4]int{episodeIDs.TMDBID, episodeIDs.IMDBID, tvdbID, episodeIDs.TVmazeID},
			imdb:        episodeMetadata.IMDB,
			seriesTitle: resolveSeriesTitle(meta, &episodeMetadata),
		}
		if key != mappedAnimeKey {
			episodeMetadata.TVDB = new(*metadata.TVDB)
			episodeInput := meta
			episodeInput.Anime = true
			mappedAnimeSeason = s.applyTVEpisodeMetadata(ctx, episodeInput, &episodeIDs, &episodeMetadata, tmdbClient, tvdbClient, tvmazeClient).SeasonStr
			mappedAnimeKey = key
		}
		return mappedAnimeSeason, true
	}
	initialAnimeSeason := func() string {
		if ids.Category != api.CanonicalCategoryTV {
			return ""
		}
		if manualSeasonSet {
			return tmdbManualSeason
		}
		season := metautil.FirstNonEmptyTrimmed(meta.SeasonStr, seasonep.FormatSeason(meta.SeasonInt))
		if !deferAnimeLookup || metadata.TMDB == nil || !metadata.TMDB.Anime || meta.HasExplicitSeasonZero() {
			return season
		}
		if mappedSeason, ok := mapAnimeSeasonForTVDBVote(true); ok {
			return mappedSeason
		}
		if season == "" && tmdbClient != nil && ids.TMDBID != 0 {
			if parsed, err := time.Parse("2006-01-02", episodeLookupDate(meta)); err == nil {
				mappedSeason, mappedEpisode, err := tmdbClient.DailyToSeasonEpisode(ctx, ids.TMDBID, parsed)
				if err == nil && mappedSeason > 0 && mappedEpisode > 0 {
					return seasonep.FormatSeason(mappedSeason)
				}
			}
		}
		return season
	}
	applyReconciledTVDBLinks := func() error {
		if err := reconcileAnimeEvidence(initialAnimeSeason()); err != nil {
			return err
		}
		applyTVDBLinks(false)
		return nil
	}

	var lastFetchIDs [4]int
	runFetchPass := func(allowProviderNameFallback bool) error {
		lastFetchIDs = [4]int{ids.TMDBID, ids.IMDBID, ids.TVDBID, ids.TVmazeID}
		allowProviderNameFallback = allowProviderNameFallback && !hasExplicitProviderAnchor
		fetchTMDB := shouldFetchTMDBMetadata()
		if fetchTMDB {
			if tmdbMetadataFetchedID != ids.TMDBID {
				tmdbErr = nil
			}
			tmdbMetadataFetchedID = ids.TMDBID
		}
		if fetchTMDB && overrideTMDB {
			tmdbAnchorVerificationAttempted = true
		}
		if fetchTMDB {
			tmdbLogoFetchAttempted = true
		}
		fetchIMDB := shouldFetchIMDBMetadata()
		if fetchIMDB {
			imdbMetadataFetchedID = ids.IMDBID
		}
		fetchTVDB := shouldFetchTVDBMetadata()
		refreshTVDBDisambiguation := shouldRefreshTVDBDisambiguation()
		if fetchTVDB {
			tvdbMetadataFetchAttempted = true
		}
		allowSiblingLookup := !selectedSiblingLinksConflict(acceptedTMDBLinks, acceptedTVmazeLinks)
		lookupTVDB := allowSiblingLookup && tvdbClient != nil && shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && !clearedTVDB && ids.TVDBID == 0 &&
			(ids.IMDBID != 0 || ids.TMDBID != 0 || allowProviderNameFallback)
		lookupTVmaze := isTVForTVmaze() &&
			(shouldFetchTVmazeMetadata() || (allowSiblingLookup && !overrideTVmaze && !clearedTVmaze && ids.TVmazeID == 0 && (ids.IMDBID != 0 || ids.TVDBID != 0)))
		if lookupTVmaze {
			tvmazeMetadataFetchAttempted = true
		}
		if lookupTVmaze && overrideTVmaze {
			tvmazeAnchorVerificationAttempted = true
		}

		logger.Debugf(
			"metadata: external ids fetch tmdb=%t imdb=%t tvdb=%t tvdb_disambiguation=%t tvmaze=%t tvmaze_name_fallback=%t",
			fetchTMDB,
			fetchIMDB,
			lookupTVDB,
			refreshTVDBDisambiguation,
			lookupTVmaze,
			allowProviderNameFallback,
		)

		var tmdbResult *tmdb.MetadataResult
		var imdbInfo *imdb.Info
		var fetchedTVDBID int
		var fetchedTVDBName string
		var fetchedTVDBInputs api.IdentityDependency
		var tvmazeResult *tvmaze.SearchResult
		var mu sync.Mutex

		group, gctx := errgroup.WithContext(ctx)
		if fetchTMDB {
			group.Go(func() error {
				input := tmdbInput()
				input.SkipAnimeLookup = deferAnimeLookup || overrideMAL || clearedMAL
				result, err := tmdbClient.FetchMetadata(gctx, input)
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
				result, err := imdbClient.GetInfo(gctx, formatIMDbID(ids.IMDBID), "", false)
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
				inputs := api.IdentityDependency{IMDBID: ids.IMDBID, TMDBID: ids.TMDBID}
				if err == nil && id == 0 && allowProviderNameFallback && strings.TrimSpace(filename) != "" {
					inputs = api.IdentityDependency{}
					var results []tvdb.SeriesSearchResult
					results, _, err = tvdbClient.SearchSeries(gctx, filename, formatOptionalInt(year))
					id = safeTVDBNameMatch(results, filename, year)
					for _, result := range results {
						if result.TVDBID == id {
							name = result.Name
							break
						}
					}
				}
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
					fetchedTVDBInputs = inputs
					mu.Unlock()
				}
				return nil
			})
		} else if ids.TVDBID != 0 {
			logger.Debugf("metadata: external ids tvdb lookup skipped id=%d source=%s", ids.TVDBID, ids.Provenance.TVDB)
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
					StrictIDOnly:      !allowProviderNameFallback,
					AllowNameFallback: allowProviderNameFallback,
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
		} else if ids.TVmazeID != 0 {
			logger.Debugf("metadata: external ids tvmaze lookup skipped id=%d source=%s", ids.TVmazeID, ids.Provenance.TVmaze)
		}

		_ = group.Wait()
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("metadata: external provider refresh canceled: %w", err)
		}

		tmdbAccepted := tmdbResult != nil && tmdbMetadataMatchesExplicitIDs(effectiveOverrides, *tmdbResult, &meta)
		if tmdbAccepted && confirmedTentativeIMDb && !overrideTMDB && !providerIDMatchesExplicit(&ids.IMDBID, tmdbResult.ExternalIMDbID) {
			appendIdentityCorrectionWarning(&meta, "TMDB metadata conflicts with the corroborated IMDb ID and was ignored.")
			tmdbAccepted = false
		}
		tvmazeAccepted := tvmazeResult != nil && tvmazeMetadataMatchesExplicitIDs(effectiveOverrides, *tvmazeResult, &meta)
		candidateTMDBLinks := acceptedTMDBLinks
		candidateTVmazeLinks := acceptedTVmazeLinks
		candidateHasTMDBLinks := hasAcceptedTMDBLinks
		candidateHasTVmazeLinks := hasAcceptedTVmazeLinks
		if tmdbAccepted {
			acceptedTMDBID = ids.TMDBID
			acceptedTMDBSeriesLinksVerified = tmdbResult.SeriesExternalIDsVerified
			acceptedTMDBMALID = tmdbResult.MALID
			candidateTMDBLinks = providerExternalLinks{IMDBID: tmdbResult.ExternalIMDbID, TVDBID: tmdbResult.ExternalTVDBID}
			candidateHasTMDBLinks = true
		}
		if tvmazeAccepted {
			links := tvmazeResultExternalLinks(*tvmazeResult)
			if confirmedTentativeIMDb && !overrideTVmaze && !providerIDMatchesExplicit(&ids.IMDBID, links.IMDBID) {
				appendIdentityCorrectionWarning(&meta, "TVmaze metadata conflicts with the corroborated IMDb ID and was ignored.")
				tvmazeAccepted = false
			} else {
				acceptedTVmazeID = tvmazeResult.SelectedID
				candidateTVmazeLinks = links
				candidateHasTVmazeLinks = true
			}
		}
		linksConflict := hasExplicitProviderAnchor && candidateHasTMDBLinks && candidateHasTVmazeLinks &&
			providerExternalLinksConflict(candidateTMDBLinks, candidateTVmazeLinks)
		// Independent manual selections retain metadata. Link disagreement blocks
		// sibling inference here; MAL follows the selected TMDB resource independently.
		allowSiblingInference := !selectedSiblingLinksConflict(candidateTMDBLinks, candidateTVmazeLinks)
		if linksConflict && allowSiblingInference {
			appendIdentityCorrectionWarning(&meta, "TMDB and TVmaze metadata reference different IMDb or TVDB IDs. Correct one of the supplied provider IDs.")
			switch {
			case overrideTMDB && !overrideTVmaze:
				tvmazeAccepted = false
				metadata.TVmaze = nil
				clearInferredProviderID(&ids.TVmazeID, &ids.Provenance.TVmaze)
				tvmazeMetadataRejected = ids.TVmazeID != 0
				candidateHasTVmazeLinks = false
			case overrideTVmaze && !overrideTMDB:
				tmdbAccepted = false
				rejectTMDBMetadata()
				candidateHasTMDBLinks = false
			default:
				tmdbAccepted = false
				tvmazeAccepted = false
				rejectTMDBMetadata()
				metadata.TVmaze = nil
				clearInferredProviderID(&ids.TVmazeID, &ids.Provenance.TVmaze)
				tvmazeMetadataRejected = ids.TVmazeID != 0
				candidateHasTMDBLinks = false
				candidateHasTVmazeLinks = false
			}
		}
		tmdbLinksAgree := allowSiblingInference && linksMatchSelectedIDs(candidateTMDBLinks)
		tvmazeLinksAgree := allowSiblingInference &&
			providerIDVerifiesExplicit(effectiveOverrides.IMDBID, candidateTVmazeLinks.IMDBID) &&
			providerIDVerifiesExplicit(effectiveOverrides.TVDBID, candidateTVmazeLinks.TVDBID)
		acceptedTMDBLinks = candidateTMDBLinks
		acceptedTVmazeLinks = candidateTVmazeLinks
		hasAcceptedTMDBLinks = candidateHasTMDBLinks
		hasAcceptedTVmazeLinks = candidateHasTVmazeLinks
		reconcileSelectedSiblingConflict()
		tmdbAccepted = tmdbAccepted && acceptedTMDBID == ids.TMDBID
		reconcileFetchedTMDBMALLinks()

		if tmdbResult != nil {
			if tmdbAccepted {
				tmdbMetadataRejected = false
				metadata.TMDB = mapTMDBMetadata(ids, *tmdbResult)
				// A verified non-anime resource supersedes any MAL inferred from TMDB.
				if ids.TMDBID != 0 && !tmdbResult.Anime &&
					(ids.Dependencies.MAL.TMDBID != 0 || ids.MALID == 0 && previousIdentity.Dependencies.MAL.TMDBID != 0) {
					withdrawAutomaticAnimeMAL(&meta, &ids)
				}
				if metadata.TMDB != nil {
					previous := retainedMetadata.TMDB
					metadata.TMDB.LogoLanguagePreference = logoPreference
					// This fetch always requests artwork; an empty successful result is reusable.
					metadata.TMDB.LogoLookupPending = canSelectLogo && !tmdbResult.LogoLookupAttempted
					if metadata.TMDB.LogoLookupPending {
						artworkCategory := ids.Category
						if artworkCategory == "" {
							artworkCategory = api.CanonicalCategoryMovie // TMDB defaults an unspecified category to movies.
						}
						if usableTMDBMetadata(previous, ids.TMDBID, artworkCategory) &&
							sourceScopedMetadataMatches(retainedMetadata.SourcePath, ids.SourcePath) {
							metadata.TMDB.Logo, metadata.TMDB.TMDBLogo = previous.Logo, previous.TMDBLogo
							metadata.TMDB.LogoLanguagePreference = previous.LogoLanguagePreference
						}
					}
				}
				if !tentativeIMDb && tmdbLinksAgree && !overrideIMDB && !clearedIMDB && ids.IMDBID == 0 && tmdbResult.IMDbID != 0 &&
					tmdbResult.IMDbID != rejectedTVDBIMDbID &&
					tmdbResult.IMDbID != unverifiedTVDBIMDbID {
					applyLinkedProviderID(&ids.IMDBID, &ids.Provenance.IMDB, &ids.Dependencies.IMDB, tmdbResult.IMDbID,
						api.IdentityDependency{TMDBID: ids.TMDBID})
				}
				if ids.Category == "" && tmdbResult.TMDBType != "" {
					ids.Category, _ = api.NormalizeCanonicalCategory(tmdbResult.TMDBType)
				}
				if tmdbLinksAgree && shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && !clearedTVDB && ids.TVDBID == 0 && tmdbResult.TVDBID != 0 {
					applyLinkedProviderID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB, tmdbResult.TVDBID,
						api.IdentityDependency{TMDBID: ids.TMDBID})
				}
				if !overrideMAL && !clearedMAL && !inferredMALConflict && ids.MALID == 0 && tmdbResult.MALID != 0 {
					applyLinkedProviderID(&ids.MALID, &ids.Provenance.MAL, &ids.Dependencies.MAL, tmdbResult.MALID,
						api.IdentityDependency{TMDBID: ids.TMDBID})
				}
			} else {
				rejectTMDBMetadata()
			}
		}

		if imdbInfo != nil && providerIDMatchesExplicit(effectiveOverrides.IMDBID, metautil.ParseIMDbNumeric(imdbInfo.IMDbID)) {
			previous := metadata.IMDB
			metadata.IMDB = mapIMDBMetadata(*imdbInfo)
			// An omitted type does not revoke the same ID's verified type during reuse.
			if !refreshProviders && metadata.IMDB != nil && metadata.IMDB.Type == "" && previous != nil && previous.IMDBID == metadata.IMDB.IMDBID {
				metadata.IMDB.Type = previous.Type
			}
			if metadata.IMDB != nil && metadata.IMDB.IMDBID == ids.IMDBID {
				imdbErr = nil
			}
		} else if imdbInfo != nil {
			appendProviderIDConflictWarning(
				&meta,
				"IMDb",
				"IMDb",
				*effectiveOverrides.IMDBID,
				metautil.ParseIMDbNumeric(imdbInfo.IMDbID),
			)
			metadata.IMDB = nil
			clearInferredProviderID(&ids.IMDBID, &ids.Provenance.IMDB)
		}

		if fetchedTVDBID != 0 && allowSiblingInference {
			if !overrideTVDB && !clearedTVDB {
				applyLinkedProviderID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB, fetchedTVDBID, fetchedTVDBInputs)
			}
			if metadata.TVDB == nil || metadata.TVDB.TVDBID == 0 {
				metadata.TVDB = &api.TVDBMetadata{TVDBID: fetchedTVDBID}
			}
		}

		if shouldUseTVDBForCategory(meta, ids) && ids.TVDBID != 0 && (fetchTVDB || fetchedTVDBID != 0) {
			tvdbMetadataFetchAttempted = true
			tvdbDisambiguationFetchAttempted = true
			tvdbSeries, err := tvdbClient.GetSeriesMetadataWithLanguage(ctx, ids.TVDBID, "")
			if err != nil {
				tvdbSeries, err = tvdbClient.GetSeriesMetadata(ctx, ids.TVDBID)
				if err != nil {
					if tvdbErr == nil {
						tvdbErr = err
					}
				}
			}
			switch {
			case err != nil:
				if metadata.TVDB != nil && metadata.TVDB.TVDBID == fetchedTVDBID && metadata.TVDB.DisplayName() == "" {
					if name := strings.TrimSpace(fetchedTVDBName); name != "" {
						metadata.TVDB.Aliases = []string{name}
					}
				}
				if tvdbErr == nil {
					tvdbErr = err
				}
			case providerIDMatchesExplicit(effectiveOverrides.TVDBID, tvdbSeries.TVDBID):
				tvdbMetadataRejected = false
				tvdbErr = nil
				mapped := mapTVDBMetadata(ids.TVDBID, fetchedTVDBName, tvdbSeries)
				if metadata.TVDB == nil || metadata.TVDB.TVDBID != mapped.TVDBID {
					metadata.TVDB = mapped
				} else {
					mergeTVDBMetadata(metadata.TVDB, mapped)
				}
				if metadata.TVDB != nil {
					namingYear := 0
					if metadata.TVDB.YearFromAlias {
						namingYear = metadata.TVDB.Year
					}
					logger.Tracef(
						"metadata: tvdb year resolved id=%d first_aired_year=%d naming_year=%d naming_year_source=%q naming_year_confidence=%q naming_eligible=%t",
						metadata.TVDB.TVDBID,
						parseYearFromDate(metadata.TVDB.FirstAired),
						namingYear,
						metadata.TVDB.YearSource,
						metadata.TVDB.YearConfidence,
						metadata.TVDB.YearFromAlias,
					)
				}
			default:
				appendProviderIDConflictWarning(&meta, "TVDB", "TVDB", *effectiveOverrides.TVDBID, tvdbSeries.TVDBID)
				metadata.TVDB = nil
				clearInferredProviderID(&ids.TVDBID, &ids.Provenance.TVDB)
				tvdbMetadataRejected = ids.TVDBID != 0
			}
		}

		if refreshTVDBDisambiguation && metadata.TVDB != nil {
			tvdbDisambiguationFetchAttempted = true
			refreshed := tvdbClient.GetNameDisambiguation(ctx, tvdbNameDisambiguationInput(metadata.TVDB))
			metadata.TVDB.NameDisambiguation = mapTVDBNameDisambiguation(refreshed)
		}
		if selectedMALAuthority {
			reconcileTVDBMALLinks(ids.TMDBID, 0)
		}
		reconcileSelectedSiblingConflict()

		if tvmazeResult != nil && tvmazeAccepted && (ids.TVmazeID > 0 ||
			allowSiblingInference && !selectedSiblingLinksConflict(acceptedTMDBLinks, acceptedTVmazeLinks)) {
			tvmazeMetadataRejected = false
			if !overrideTVmaze && !clearedTVmaze {
				applyLinkedProviderID(&ids.TVmazeID, &ids.Provenance.TVmaze, &ids.Dependencies.TVmaze, tvmazeResult.SelectedID, tvmazeResult.LookupDependency)
			}
			if !tentativeIMDb && tvmazeLinksAgree && !overrideIMDB && !clearedIMDB && ids.IMDBID == 0 && tvmazeResult.IMDBID != 0 &&
				tvmazeResult.IMDBID != rejectedTVDBIMDbID &&
				tvmazeResult.IMDBID != unverifiedTVDBIMDbID {
				applyLinkedProviderID(&ids.IMDBID, &ids.Provenance.IMDB, &ids.Dependencies.IMDB, tvmazeResult.IMDBID,
					api.IdentityDependency{TVmazeID: ids.TVmazeID})
			}
			if tvmazeLinksAgree && shouldUseTVDBForCategory(meta, ids) && !overrideTVDB && !clearedTVDB && ids.TVDBID == 0 && tvmazeResult.TVDBID != 0 {
				applyLinkedProviderID(&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB, tvmazeResult.TVDBID,
					api.IdentityDependency{TVmazeID: ids.TVmazeID})
			}
			metadata.TVmaze = mapTVmazeMetadata(*tvmazeResult)
		} else if tvmazeResult != nil {
			metadata.TVmaze = nil
			clearInferredProviderID(&ids.TVmazeID, &ids.Provenance.TVmaze)
			tvmazeMetadataRejected = ids.TVmazeID != 0
		}
		if rejected := rejectTVDBEpisodeLink(&meta, &ids, &metadata); rejected > 0 {
			rejectedTVDBIMDbID = rejected
			invalidateCandidateDependencies(false)
			invalidateMismatchedProviderMetadata(&metadata, ids)
		}
		if effectiveOverrides.IMDBID == nil && ids.IMDBID > 0 && ids.Dependencies.IMDB.TVDBID > 0 &&
			(metadata.IMDB == nil || metadata.IMDB.IMDBID != ids.IMDBID || strings.TrimSpace(metadata.IMDB.Type) == "") {
			unverifiedTVDBIMDbID = ids.IMDBID
			clearInferredProviderID(&ids.IMDBID, &ids.Provenance.IMDB)
			ids.Dependencies.IMDB = api.IdentityDependency{}
			metadata.IMDB = nil
			invalidateCandidateDependencies(false)
			invalidateMismatchedProviderMetadata(&metadata, ids)
			if imdbErr != nil {
				appendIdentityCorrectionWarning(&meta, "IMDb could not verify TVDB's remote link; the unverified link was ignored.")
			} else {
				appendIdentityCorrectionWarning(&meta, "TVDB's IMDb remote link has no verified title type and was ignored.")
			}
		}
		reconcileFetchedTMDBMALLinks()
		// Replay raw series evidence when a rejected input previously occupied IMDb.
		if !tentativeIMDb && tmdbLinksAgree && hasAcceptedTMDBLinks && ids.TMDBID > 0 && acceptedTMDBID == ids.TMDBID &&
			!selectedSiblingLinksConflict(acceptedTMDBLinks, acceptedTVmazeLinks) &&
			!overrideIMDB && !clearedIMDB && ids.IMDBID == 0 && acceptedTMDBLinks.IMDBID > 0 &&
			acceptedTMDBLinks.IMDBID != rejectedTVDBIMDbID && acceptedTMDBLinks.IMDBID != unverifiedTVDBIMDbID {
			applyLinkedProviderID(&ids.IMDBID, &ids.Provenance.IMDB, &ids.Dependencies.IMDB, acceptedTMDBLinks.IMDBID,
				api.IdentityDependency{TMDBID: ids.TMDBID})
		}
		return nil
	}

	if shouldRunFetchPass() {
		if err := runFetchPass(false); err != nil {
			return preparationstate.State{}, err
		}
	}
	if err := applyReconciledTVDBLinks(); err != nil {
		return preparationstate.State{}, err
	}
	resolveTMDBFromProviderIDs()
	if err := ctx.Err(); err != nil {
		return preparationstate.State{}, fmt.Errorf("metadata: external provider refresh canceled: %w", err)
	}
	// Tracker and media metadata can supply an episode IMDb ID where downstream
	// providers require the parent series ID. Clear the episode snapshot so the
	// second fetch pass refreshes series metadata after a successful adjustment.
	if episodeClient, ok := imdbClient.(imdbEpisodeClient); ok && !overrideIMDB && !clearedIMDB && shouldUseTVDBForCategory(meta, ids) &&
		metadata.IMDB != nil &&
		strings.EqualFold(strings.TrimSpace(metadata.IMDB.Type), "tvEpisode") {
		lookup, err := episodeClient.GetEpisodeInfo(ctx, formatIMDbID(ids.IMDBID), false)
		if err != nil {
			logger.Debugf("metadata: imdb episode parent lookup failed")
		} else if parentID := metautil.ParseIMDbNumeric(lookup.Series.SeriesID); parentID != 0 && parentID != ids.IMDBID {
			ids.IMDBID = parentID
			ids.Provenance.IMDB = api.IdentityProvenanceProvider
			ids.Dependencies.IMDB = api.IdentityDependency{ID: parentID}
			metadata.IMDB = nil
			if invalidateCandidateDependencies(false) > 0 {
				invalidateMismatchedProviderMetadata(&metadata, ids)
			}
			logger.Debugf("metadata: imdb episode id adjusted to parent series id=%d", parentID)
		}
	}
	resolveTMDBFromProviderIDs()
	retryIMDbCategorySearch()
	if shouldRunFetchPass() || tvdbClient != nil && shouldUseTVDBForCategory(meta, ids) && !hasExplicitProviderAnchor &&
		!clearedTVDB && ids.TVDBID == 0 && strings.TrimSpace(filename) != "" {
		if err := runFetchPass(true); err != nil {
			return preparationstate.State{}, err
		}
	}
	// A name fallback or reverse lookup can discover new sibling links. Bound the
	// follow-up work and stop when no new IDs need fetching.
	reconcileSiblingLinks := func() error {
		for range 3 {
			if err := applyReconciledTVDBLinks(); err != nil {
				return err
			}
			resolveTMDBFromProviderIDs()
			after := [4]int{ids.TMDBID, ids.IMDBID, ids.TVDBID, ids.TVmazeID}
			if lastFetchIDs == after || !shouldRunFetchPass() {
				break
			}
			if err := runFetchPass(false); err != nil {
				return err
			}
		}
		return nil
	}
	if err := reconcileSiblingLinks(); err != nil {
		return preparationstate.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return preparationstate.State{}, fmt.Errorf("metadata: provider reconciliation canceled: %w", err)
	}
	const tentativeIMDbWarning = "The IMDb match remains tentative. Confirm the intended IMDb title before continuing if an ID is required."
	tentativeIMDbRequiresTVDB := false
	if tentativeIMDb && ids.IMDBID == 0 {
		sceneInput := meta
		sceneInput.Identity = ids
		sceneInput.ArrIMDBID = 0 // A lookup keyed on IMDb cannot independently confirm IMDb.
		scene, err := s.detectScene(ctx, sceneInput)
		if err != nil {
			return preparationstate.State{}, err
		}
		if scene == nil {
			scene = new(SceneResult)
		}
		meta.CollectedScene = scene
		var linkedIMDb []imdbCorroboration
		// A dependent provider link can disagree, but normally cannot corroborate an IMDb ID.
		if hasAcceptedTMDBLinks && acceptedTMDBID == ids.TMDBID {
			linkedIMDb = append(linkedIMDb, imdbCorroboration{acceptedTMDBLinks.IMDBID, independentlyResolved(ids.TMDBID, ids.Dependencies.TMDB)})
		}
		if hasAcceptedTVmazeLinks && acceptedTVmazeID == ids.TVmazeID {
			linkedIMDb = append(linkedIMDb, imdbCorroboration{acceptedTVmazeLinks.IMDBID, independentlyResolved(ids.TVmazeID, ids.Dependencies.TVmaze)})
		}
		if scene.IsScene {
			linkedIMDb = append(linkedIMDb, imdbCorroboration{scene.IMDBID, true})
		}
		suggestion := candidates.IMDB[0].ID
		confirmedWithoutTVDB := corroboratedIMDb(suggestion, linkedIMDb)
		tvdbIMDbConflict := false
		tmdbAnchoredTVDBVote := false
		if ids.TVDBID > 0 && shouldUseTVDBForCategory(meta, ids) && metadata.TVDB != nil && metadata.TVDB.TVDBID == ids.TVDBID {
			links := metadata.TVDB.RemoteLinks
			// A sibling mismatch disqualifies TVDB from voting, but cannot erase its IMDb contradiction.
			tvdbIMDbConflict = slices.ContainsFunc(links.Links, func(link api.TVDBRemoteLink) bool {
				return link.Provider == api.IdentityProviderIMDB && link.State == "conflicting"
			})
			if links.Status == api.MetadataEvidenceStatusComplete || links.Status == api.MetadataEvidenceStatusContradictory {
				usableVote := links.Status == api.MetadataEvidenceStatusComplete &&
					!tvdbSiblingLinksMismatch(links, tvdbSiblingTargets(&ids, effectiveOverrides))
				independent := usableVote && independentlyResolved(ids.TVDBID, ids.Dependencies.TVDB)
				for _, link := range links.Links {
					if link.Provider == api.IdentityProviderIMDB && link.State == "valid" {
						// TMDB may locate the TVDB series, but cannot supply its IMDb vote.
						// Both verified series records must explicitly link to the replacement.
						tmdbAnchoredTVDBVote = usableVote && link.ID > 0 && link.ID != suggestion &&
							ids.Category == api.CanonicalCategoryTV && titleMatchedTVTMDBID == ids.TMDBID &&
							hasAcceptedTMDBLinks && acceptedTMDBSeriesLinksVerified && acceptedTMDBID == ids.TMDBID && acceptedTMDBLinks.IMDBID == link.ID &&
							independentlyResolved(ids.TMDBID, ids.Dependencies.TMDB) &&
							ids.Dependencies.TVDB == (api.IdentityDependency{ID: ids.TVDBID, TMDBID: ids.TMDBID})
						linkedIMDb = append(linkedIMDb, imdbCorroboration{link.ID, independent || tmdbAnchoredTVDBVote})
						break // Duplicate TVDB links do not supply extra votes.
					}
				}
			}
		}
		if confirmed := corroboratedIMDb(suggestion, linkedIMDb); confirmed != 0 && !tvdbIMDbConflict {
			info, err := imdbClient.GetInfo(ctx, formatIMDbID(confirmed), "", false)
			if ctx.Err() != nil {
				return preparationstate.State{}, fmt.Errorf("metadata: IMDb corroboration canceled: %w", ctx.Err())
			}
			category := ids.Category
			if normalizeCategory(string(category)) == "" {
				category = api.CanonicalCategoryMovie
			}
			if err == nil && metautil.ParseIMDbNumeric(info.IMDbID) == confirmed && verifiedIMDbCategory(info.Type, category) {
				ids.Category = category
				ids.IMDBID = confirmed
				ids.Provenance.IMDB = api.IdentityProvenanceProvider
				ids.Dependencies.IMDB = api.IdentityDependency{ID: confirmed}
				metadata.IMDB = mapIMDBMetadata(info)
				imdbMetadataFetchedID = confirmed
				imdbErr = nil
				confirmedTentativeIMDb = true
				tentativeIMDbRequiresTVDB = confirmedWithoutTVDB != confirmed
				if tentativeIMDbRequiresTVDB {
					ids.Dependencies.IMDB.TVDBID = ids.TVDBID
					if tmdbAnchoredTVDBVote {
						ids.Dependencies.IMDB.TMDBID = ids.TMDBID
					}
				}
				if ids.TMDBID == 0 {
					tmdbCategoryPref = normalizeCategory(string(category))
				}
				// New sibling IDs enrich the generation, but never vote on the IMDb ID that led to them.
				if err := reconcileSiblingLinks(); err != nil {
					return preparationstate.State{}, err
				}
			}
		}
		if ids.IMDBID == 0 {
			markIMDbWithdrawal()
			appendIdentityCorrectionWarning(&meta, tentativeIMDbWarning)
		}
	}
	if refreshProviders {
		clearUnverifiedRefreshProviderIDs(&ids, &metadata, tmdbErr, imdbErr, tvdbErr, tvmazeErr)
	}
	if positiveProviderOverride(effectiveOverrides.TVDBID) && !shouldUseTVDBForCategory(meta, ids) {
		appendIdentityCorrectionWarning(&meta, "The supplied TVDB ID conflicts with the resolved movie category. Correct the TVDB ID or category.")
	}
	clearTVProvidersForNonTVCategory(meta, &ids, &metadata)
	if invalidateCandidateDependencies(false) > 0 {
		invalidateMismatchedProviderMetadata(&metadata, ids)
	}
	if metadata.TVDB != nil && metadata.TVDB.RemoteLinks.Status == api.MetadataEvidenceStatusUnavailable {
		appendIdentityCorrectionWarning(
			&meta,
			"TVDB series remote links are unavailable; select missing sibling IDs manually. Saved fetch failures require removing the input from History before retrying.",
		)
	}
	downstreamIDs := episodeProviderIDs()
	// Retained title facts may carry a logo selected using earlier preferences.
	// Replay just the image evidence, without re-collecting the title or its siblings.
	if canSelectLogo && (tmdbMetadataFetchedID != downstreamIDs.TMDBID || tmdbErr != nil) &&
		usableTMDBMetadata(metadata.TMDB, downstreamIDs.TMDBID, downstreamIDs.Category) &&
		(metadata.TMDB.LogoLookupPending || metadata.TMDB.LogoLanguagePreference != logoPreference ||
			!keepProvider(effectiveOverrides.KeepTMDB) && metadata.TMDB.Logo == "" && !metadata.TMDB.LogoLookupAttempted) {
		logo, name, err := logoClient.SelectLogo(ctx, downstreamIDs.TMDBID, string(downstreamIDs.Category), logoLanguages)
		if ctx.Err() != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: logo selection canceled: %w", ctx.Err())
		}
		retained := *metadata.TMDB
		retained.LogoLookupPending = err != nil
		if err != nil {
			logger.Warnf("metadata: TMDB logo lookup failed: %s", redaction.RedactValue(err.Error(), nil))
		} else {
			retained.Logo, retained.TMDBLogo, retained.LogoLookupAttempted = logo, name, true
			retained.LogoLanguagePreference = logoPreference
		}
		metadata.TMDB = &retained
	}
	episodeInput := meta
	rankingSeason, hasFilteredSeason := mapAnimeSeasonForTVDBVote(false)
	if !hasFilteredSeason {
		meta = s.applyTVEpisodeMetadata(ctx, meta, &downstreamIDs, &metadata, tmdbClient, tvdbClient, tvmazeClient)
		ids.Category = downstreamIDs.Category
		rankingSeason = meta.SeasonStr
	}
	if err := reconcileAnimeEvidence(rankingSeason); err != nil {
		return preparationstate.State{}, err
	}
	if compareSelectedSeriesMAL {
		reconcileSelectedSiblingConflict()
	}
	if tentativeIMDbRequiresTVDB && inferredMALConflict {
		markIMDbWithdrawal()
		appendIdentityCorrectionWarning(&meta, tentativeIMDbWarning)
	}
	finalEpisodeIDs := episodeProviderIDs()
	if hasFilteredSeason || downstreamIDs.IMDBID != finalEpisodeIDs.IMDBID || downstreamIDs.TVmazeID != finalEpisodeIDs.TVmazeID {
		// Replay the original episode inputs with the reconciled identity decisions.
		downstreamIDs = finalEpisodeIDs
		episodeInput.LookupWarnings = meta.LookupWarnings
		episodeInput.AuthoritativeIMDB = meta.AuthoritativeIMDB
		episodeInput.AuthoritativeTVmaze = meta.AuthoritativeTVmaze
		episodeInput.AuthoritativeMAL = meta.AuthoritativeMAL
		episodeInput.WithdrawnStoredIDs = meta.WithdrawnStoredIDs
		meta = s.applyTVEpisodeMetadata(ctx, episodeInput, &downstreamIDs, &metadata, tmdbClient, tvdbClient, tvmazeClient)
		ids.Category = downstreamIDs.Category
	}
	if animeWarning != "" {
		appendIdentityCorrectionWarning(&meta, animeWarning)
	}
	if inferredMALConflict {
		appendIdentityCorrectionWarning(&meta, "TMDB and TVDB reference different MAL IDs. Select the intended MAL ID before resolving its metadata.")
	}
	if anime := metadata.TMDB; anime != nil && anime.TMDBID == downstreamIDs.TMDBID && !overrideMAL && !clearedMAL &&
		!inferredMALConflict && (deferAnimeLookup && rankedAnime.id == anime.TMDBID && anime.AnimeSeason != "" && !animeLookupFailed ||
		!deferAnimeLookup && keepProvider(effectiveOverrides.KeepTMDB)) {
		applyLinkedProviderID(&ids.MALID, &ids.Provenance.MAL, &ids.Dependencies.MAL, anime.MALID, api.IdentityDependency{TMDBID: ids.TMDBID})
	}
	if metadata.AniList != nil && (ids.MALID == 0 || metadata.AniList.MALID != ids.MALID) {
		invalidateMismatchedProviderMetadata(&metadata, ids)
	}
	if anilistClient != nil && shouldFetchAniListMetadata(ids.MALID, metadata.AniList) {
		var result tmdb.AniListMetadataResult
		result, anilistErr = anilistClient.FetchAniListMetadata(ctx, ids.MALID)
		if err := ctx.Err(); err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: AniList metadata canceled: %w", err)
		}
		if anilistErr == nil && result.MALID == ids.MALID {
			metadata.AniList = mapAniListMetadata(result)
		} else if anilistErr == nil && result.MALID > 0 && overrideMAL {
			appendProviderIDConflictWarning(&meta, "AniList", "MAL", *effectiveOverrides.MALID, result.MALID)
		}
	}
	if refreshProviders && anilistErr != nil && metadata.AniList == nil {
		withdrawAutomaticAnimeMAL(&meta, &ids)
	}
	downstreamIDs.MALID = ids.MALID
	downstreamIDs.Provenance.MAL = ids.Provenance.MAL
	downstreamIDs.Dependencies.MAL = ids.Dependencies.MAL

	needsPTBR := requiresTMDBLocalizedPTBRRefresh(meta.MetadataRequirements, ids.Category, meta, downstreamIDs, metadata) ||
		s.registry.NeedsLocalizedMetadata(meta.EvidenceTrackers, "pt-BR") || s.registry.NeedsLocalizedMetadata(meta.MatchedEvidenceTrackers, "pt-BR")

	if tmdbClient != nil && needsPTBR && downstreamIDs.TMDBID != 0 {
		var mainData, seasonData, episodeData map[string]any
		var localizedErr error
		isTV := strings.EqualFold(string(ids.Category), "TV")
		mainInput := tmdb.LocalizedDataInput{
			TMDBID:           downstreamIDs.TMDBID,
			Category:         string(ids.Category),
			DataType:         "main",
			Language:         "pt-BR",
			AppendToResponse: localizedMainAppendToResponse(isTV),
		}
		mainInput.CachePath = localizedTMDBCachePath(s.cfg.MainSettings.DBPath, mainInput)
		mainData, localizedErr = tmdbClient.GetLocalizedData(ctx, mainInput)
		if localizedErr != nil {
			logger.Debugf("metadata: pt-BR main localized data fetch failed: %v", localizedErr)
		}

		if isTV && meta.SeasonInt > 0 {
			seasonInput := tmdb.LocalizedDataInput{
				TMDBID:           downstreamIDs.TMDBID,
				Season:           meta.SeasonInt,
				Category:         "TV",
				DataType:         "season",
				Language:         "pt-BR",
				AppendToResponse: "credits",
			}
			seasonInput.CachePath = localizedTMDBCachePath(s.cfg.MainSettings.DBPath, seasonInput)
			seasonData, localizedErr = tmdbClient.GetLocalizedData(ctx, seasonInput)
			if localizedErr != nil {
				logger.Debugf("metadata: pt-BR season localized data fetch failed: %v", localizedErr)
			}
			if meta.EpisodeInt > 0 {
				episodeInput := tmdb.LocalizedDataInput{
					TMDBID:           downstreamIDs.TMDBID,
					Season:           meta.SeasonInt,
					Episode:          meta.EpisodeInt,
					Category:         "TV",
					DataType:         "episode",
					Language:         "pt-BR",
					AppendToResponse: "credits",
				}
				episodeInput.CachePath = localizedTMDBCachePath(s.cfg.MainSettings.DBPath, episodeInput)
				episodeData, localizedErr = tmdbClient.GetLocalizedData(ctx, episodeInput)
				if localizedErr != nil {
					logger.Debugf("metadata: pt-BR episode localized data fetch failed: %v", localizedErr)
				}
			}
		}

		if mainData != nil || seasonData != nil || episodeData != nil {
			localized := parseTMDBLocalizedData(mainData, seasonData, episodeData)
			if metadata.TMDB == nil {
				metadata.TMDB = &api.TMDBMetadata{TMDBID: downstreamIDs.TMDBID, Category: string(ids.Category)}
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

	if tmdbErr != nil {
		logger.Warnf("metadata: tmdb metadata lookup failed: %v", tmdbErr)
	}
	if imdbErr != nil {
		logger.Warnf("metadata: imdb metadata lookup failed: %v", imdbErr)
	}
	if tvdbErr != nil {
		logger.Warnf("metadata: tvdb lookup failed: %v", tvdbErr)
	}
	if tvmazeErr != nil {
		logger.Warnf("metadata: tvmaze lookup failed: %v", tvmazeErr)
	}
	if anilistErr != nil {
		logger.Warnf("metadata: anilist lookup failed: %v", anilistErr)
	}
	if refreshProviders {
		appendProviderRefreshWarning(&meta, "TMDB", tmdbErr)
		appendProviderRefreshWarning(&meta, "IMDb", imdbErr)
		appendProviderRefreshWarning(&meta, "TVDB", tvdbErr)
		appendProviderRefreshWarning(&meta, "TVmaze", tvmazeErr)
		appendProviderRefreshWarning(&meta, "AniList", anilistErr)
	}
	// Keep unresolved searches free to discover TV before applying the final default.
	if normalizeCategory(string(ids.Category)) == "" {
		ids.Category = api.CanonicalCategoryMovie
	}
	ids = ids.WithProviderRetention(meta.ExternalIDOverrides)
	if tmdb := metadata.TMDB; tmdb != nil && tmdb.RetrievedAKA != "" &&
		(ids.MALID <= 0 || tmdb.MALID != ids.MALID || ids.Overrides.MAL == api.OverrideStateClear) {
		current := *tmdb
		current.RetrievedAKA = ""
		metadata.TMDB = &current
	}
	logger.Tracef(
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
	logger.Tracef(
		"metadata: external metadata available tmdb=%t imdb=%t tvdb=%t tvmaze=%t anilist=%t bluray=%t",
		metadata.TMDB != nil,
		metadata.IMDB != nil,
		metadata.TVDB != nil,
		metadata.TVmaze != nil,
		metadata.AniList != nil,
		metadata.Bluray != nil,
	)

	ids.ResolvedAt = time.Now().UTC()
	metadata.UpdatedAt = ids.ResolvedAt

	meta.Identity = ids
	meta.MALID = ids.MALID
	meta.ExternalIdentityCandidates = append(append([]api.ExternalIdentityCandidate(nil), candidates.TMDB...), candidates.IMDB...)
	meta.ProviderMetadata = metadata
	return meta, nil
}

func independentlyResolved(id int, dependency api.IdentityDependency) bool {
	return id > 0 && dependency == (api.IdentityDependency{ID: id})
}

// animeRankingScope distinguishes a completed seasonless lookup from a pending lookup.
func animeRankingScope(category api.CanonicalCategory, season string) string {
	if category == api.CanonicalCategoryTV {
		return cmp.Or(season, "none")
	}
	return "movie"
}

func copyAnimeRanking(target, source *api.TMDBMetadata) {
	target.MALID, target.AnimeSeason = source.MALID, source.AnimeSeason
	target.RetrievedAKA, target.Demographic = source.RetrievedAKA, source.Demographic
	target.AnimeLookupPending = source.AnimeLookupPending
}

func withdrawAutomaticAnimeMAL(meta *preparationstate.State, ids *api.ExternalIdentity) {
	switch ids.Provenance.MAL {
	case "", api.IdentityProvenanceUnknown, api.IdentityProvenanceProvider, api.IdentityProvenancePersisted, api.IdentityProvenanceLegacy:
		clearInferredProviderID(&ids.MALID, &ids.Provenance.MAL)
		ids.Dependencies.MAL = api.IdentityDependency{}
		meta.AuthoritativeMAL = true
	case api.IdentityProvenanceExplicit, api.IdentityProvenanceRetained, api.IdentityProvenanceMediaInfo,
		api.IdentityProvenanceScene, api.IdentityProvenanceArr, api.IdentityProvenanceTracker:
	}
}

func withdrawConflictingTVDBSiblings(ids *api.ExternalIdentity, tvdbID int) {
	if ids.Dependencies.IMDB.TVDBID == tvdbID {
		clearInferredProviderID(&ids.IMDBID, &ids.Provenance.IMDB)
	}
	if ids.Dependencies.TVmaze.TVDBID == tvdbID {
		clearInferredProviderID(&ids.TVmazeID, &ids.Provenance.TVmaze)
	}
	invalidateIdentityDependencies(ids, false)
}

type imdbCorroboration struct {
	id     int
	counts bool
}

// corroboratedIMDb accepts one independent confirmation of the suggestion,
// or two agreeing qualifying links to a replacement. Conflicts stay unresolved.
func corroboratedIMDb(suggestion int, links []imdbCorroboration) int {
	var agreed, count int
	for _, link := range links {
		if link.id <= 0 {
			continue
		}
		if agreed != 0 && agreed != link.id {
			return 0
		}
		agreed = link.id
		if link.counts {
			count++
		}
	}
	if count >= 2 || count == 1 && agreed == suggestion {
		return agreed
	}
	return 0
}

func verifiedIMDbCategory(imdbType string, category api.CanonicalCategory) bool {
	switch strings.ToLower(strings.TrimSpace(imdbType)) {
	case "tvseries", "tvminiseries":
		return category == api.CanonicalCategoryTV
	case "movie", "tvmovie", "tvspecial", "short", "video", "tvshort":
		return category == api.CanonicalCategoryMovie
	default:
		return false
	}
}

func appendProviderRefreshWarning(meta *preparationstate.State, provider string, err error) {
	if meta == nil || err == nil {
		return
	}
	appendIdentityCorrectionWarning(meta, provider+" refresh failed; dependent metadata is unavailable until it succeeds.")
}

func clearUnverifiedRefreshProviderIDs(
	ids *api.ExternalIdentity,
	metadata *api.SourceScopedMetadata,
	tmdbErr error,
	imdbErr error,
	tvdbErr error,
	tvmazeErr error,
) {
	if ids == nil || metadata == nil {
		return
	}
	if tmdbErr != nil && metadata.TMDB == nil {
		clearInferredProviderID(&ids.TMDBID, &ids.Provenance.TMDB)
	}
	if imdbErr != nil && metadata.IMDB == nil {
		clearInferredProviderID(&ids.IMDBID, &ids.Provenance.IMDB)
	}
	if tvdbErr != nil && metadata.TVDB == nil {
		clearInferredProviderID(&ids.TVDBID, &ids.Provenance.TVDB)
	}
	if tvmazeErr != nil && metadata.TVmaze == nil {
		clearInferredProviderID(&ids.TVmazeID, &ids.Provenance.TVmaze)
	}
}

func sourceScopedMetadataMatches(storedSourcePath string, currentSourcePath string) bool {
	trimmedStored := strings.TrimSpace(storedSourcePath)
	if trimmedStored == "" {
		return true
	}
	return strings.EqualFold(trimmedStored, strings.TrimSpace(currentSourcePath))
}

func copyStoredProviderPins(target *api.ExternalIdentity, stored api.ExternalIdentity) {
	if target == nil || !sourceScopedMetadataMatches(stored.SourcePath, target.SourcePath) {
		return
	}
	copyStoredProviderPin(&target.TMDBID, &target.Provenance.TMDB, &target.Overrides.TMDB, stored.TMDBID, stored.Provenance.TMDB, stored.Overrides.TMDB)
	copyStoredProviderPin(&target.IMDBID, &target.Provenance.IMDB, &target.Overrides.IMDB, stored.IMDBID, stored.Provenance.IMDB, stored.Overrides.IMDB)
	copyStoredProviderPin(&target.TVDBID, &target.Provenance.TVDB, &target.Overrides.TVDB, stored.TVDBID, stored.Provenance.TVDB, stored.Overrides.TVDB)
	copyStoredProviderPin(
		&target.TVmazeID,
		&target.Provenance.TVmaze,
		&target.Overrides.TVmaze,
		stored.TVmazeID,
		stored.Provenance.TVmaze,
		stored.Overrides.TVmaze,
	)
	copyStoredProviderPin(&target.MALID, &target.Provenance.MAL, &target.Overrides.MAL, stored.MALID, stored.Provenance.MAL, stored.Overrides.MAL)
	for _, provider := range []struct {
		retained bool
		target   *api.IdentityDependency
		stored   api.IdentityDependency
	}{
		{target.Provenance.TMDB == api.IdentityProvenanceRetained, &target.Dependencies.TMDB, stored.Dependencies.TMDB},
		{target.Provenance.IMDB == api.IdentityProvenanceRetained, &target.Dependencies.IMDB, stored.Dependencies.IMDB},
		{target.Provenance.TVDB == api.IdentityProvenanceRetained, &target.Dependencies.TVDB, stored.Dependencies.TVDB},
		{target.Provenance.TVmaze == api.IdentityProvenanceRetained, &target.Dependencies.TVmaze, stored.Dependencies.TVmaze},
		{target.Provenance.MAL == api.IdentityProvenanceRetained, &target.Dependencies.MAL, stored.Dependencies.MAL},
	} {
		if provider.retained {
			*provider.target = provider.stored
		}
	}
	if target.HasRetainedProvider() {
		target.Category = stored.Category
	}
}

func copyStoredProviderPin(
	target *int,
	provenance *api.IdentityProvenance,
	override *api.OverrideState,
	storedID int,
	storedProvenance api.IdentityProvenance,
	storedOverride api.OverrideState,
) {
	switch {
	case storedOverride == api.OverrideStateClear || storedID == 0 && storedProvenance == api.IdentityProvenanceExplicit:
		*target = 0
		*provenance = api.IdentityProvenanceExplicit
		*override = api.OverrideStateClear
	case storedID > 0 && (storedOverride == api.OverrideStateValue || storedProvenance == api.IdentityProvenanceExplicit):
		*target = storedID
		*provenance = api.IdentityProvenanceExplicit
		*override = api.OverrideStateValue
	case storedID > 0 && storedProvenance == api.IdentityProvenanceRetained:
		*target = storedID
		*provenance = api.IdentityProvenanceRetained
		*override = api.OverrideStateUnset
	}
}

func hasPositiveProviderOverride(overrides api.ExternalIDOverrides) bool {
	return positiveProviderOverride(overrides.TMDBID) || positiveProviderOverride(overrides.IMDBID) ||
		positiveProviderOverride(overrides.TVDBID) || positiveProviderOverride(overrides.TVmazeID) || positiveProviderOverride(overrides.MALID)
}

func positiveProviderOverride(value *int) bool {
	return value != nil && *value > 0
}

func clearedProviderOverride(value *int) bool {
	return value != nil && *value == 0
}

func keepProvider(value *bool) bool {
	return value != nil && *value
}

func effectiveProviderOverrides(current api.ExternalIDOverrides, ids api.ExternalIdentity) api.ExternalIDOverrides {
	return api.ExternalIDOverrides{
		TMDBID:     effectiveProviderOverride(current.TMDBID, ids.TMDBID, ids.Provenance.TMDB, ids.Overrides.TMDB),
		IMDBID:     effectiveProviderOverride(current.IMDBID, ids.IMDBID, ids.Provenance.IMDB, ids.Overrides.IMDB),
		TVDBID:     effectiveProviderOverride(current.TVDBID, ids.TVDBID, ids.Provenance.TVDB, ids.Overrides.TVDB),
		TVmazeID:   effectiveProviderOverride(current.TVmazeID, ids.TVmazeID, ids.Provenance.TVmaze, ids.Overrides.TVmaze),
		MALID:      effectiveProviderOverride(current.MALID, ids.MALID, ids.Provenance.MAL, ids.Overrides.MAL),
		KeepTMDB:   current.KeepTMDB,
		KeepIMDB:   current.KeepIMDB,
		KeepTVDB:   current.KeepTVDB,
		KeepTVmaze: current.KeepTVmaze,
		KeepMAL:    current.KeepMAL,
	}
}

func effectiveProviderOverride(current *int, id int, provenance api.IdentityProvenance, state api.OverrideState) *int {
	if current != nil {
		return current
	}
	if state == api.OverrideStateClear {
		return new(0)
	}
	if state == api.OverrideStateValue || provenance == api.IdentityProvenanceExplicit || provenance == api.IdentityProvenanceRetained {
		return new(id)
	}
	return nil
}

type providerExternalLinks struct {
	IMDBID int
	TVDBID int
}

func tvmazeResultExternalLinks(result tvmaze.SearchResult) providerExternalLinks {
	for _, candidate := range result.Candidates {
		if candidate.ID == result.SelectedID {
			return providerExternalLinks{
				IMDBID: metautil.ParseIMDbNumeric(candidate.Externals.IMDB),
				TVDBID: candidate.Externals.TVDB,
			}
		}
	}
	return providerExternalLinks{}
}

func providerExternalLinksConflict(first, second providerExternalLinks) bool {
	return first.IMDBID != 0 && second.IMDBID != 0 && first.IMDBID != second.IMDBID ||
		first.TVDBID != 0 && second.TVDBID != 0 && first.TVDBID != second.TVDBID
}

func clearPreviouslyResolvedProviderIDs(ids *api.ExternalIdentity, preserveRetained bool) {
	if ids == nil {
		return
	}
	for _, provider := range []struct {
		id         *int
		provenance *api.IdentityProvenance
		dependency *api.IdentityDependency
	}{
		{&ids.TMDBID, &ids.Provenance.TMDB, &ids.Dependencies.TMDB}, {&ids.IMDBID, &ids.Provenance.IMDB, &ids.Dependencies.IMDB},
		{&ids.TVDBID, &ids.Provenance.TVDB, &ids.Dependencies.TVDB}, {&ids.TVmazeID, &ids.Provenance.TVmaze, &ids.Dependencies.TVmaze},
		{&ids.MALID, &ids.Provenance.MAL, &ids.Dependencies.MAL},
	} {
		if !preserveRetained && *provider.provenance == api.IdentityProvenanceRetained {
			*provider.provenance = api.IdentityProvenancePersisted
		}
		clearInferredProviderID(provider.id, provider.provenance)
		if *provider.provenance != api.IdentityProvenanceRetained {
			*provider.dependency = api.IdentityDependency{}
		}
	}
}

// invalidateIdentityDependencies drops lookup results whose inputs changed.
// It repeats until transitive dependents are cleared and returns the cleared count.
// Explicit corrections remain authoritative. When invalidateUnrecorded is true,
// legacy automatic IDs without recorded inputs are also cleared for re-resolution.
func invalidateIdentityDependencies(ids *api.ExternalIdentity, invalidateUnrecorded bool) int {
	invalidated := 0
	providers := []struct {
		id         *int
		provenance *api.IdentityProvenance
		override   api.OverrideState
		dependency *api.IdentityDependency
	}{
		{&ids.TMDBID, &ids.Provenance.TMDB, ids.Overrides.TMDB, &ids.Dependencies.TMDB},
		{&ids.IMDBID, &ids.Provenance.IMDB, ids.Overrides.IMDB, &ids.Dependencies.IMDB},
		{&ids.TVDBID, &ids.Provenance.TVDB, ids.Overrides.TVDB, &ids.Dependencies.TVDB},
		{&ids.TVmazeID, &ids.Provenance.TVmaze, ids.Overrides.TVmaze, &ids.Dependencies.TVmaze},
		{&ids.MALID, &ids.Provenance.MAL, ids.Overrides.MAL, &ids.Dependencies.MAL},
	}
	for changed := true; changed; {
		changed = false
		for _, provider := range providers {
			if *provider.id == 0 || *provider.provenance == api.IdentityProvenanceExplicit ||
				provider.override == api.OverrideStateValue || provider.override == api.OverrideStateClear {
				*provider.dependency = api.IdentityDependency{ID: *provider.id}
				continue
			}
			if *provider.provenance == api.IdentityProvenanceRetained {
				continue
			}
			dependency := *provider.dependency
			unrecorded := invalidateUnrecorded && dependency.ID == 0 &&
				(*provider.provenance == api.IdentityProvenanceProvider || *provider.provenance == api.IdentityProvenanceLegacy ||
					*provider.provenance == api.IdentityProvenancePersisted || *provider.provenance == api.IdentityProvenanceUnknown || *provider.provenance == "")
			stale := dependency.ID == *provider.id &&
				(dependency.TMDBID != 0 && dependency.TMDBID != ids.TMDBID ||
					dependency.IMDBID != 0 && dependency.IMDBID != ids.IMDBID ||
					dependency.TVDBID != 0 && dependency.TVDBID != ids.TVDBID ||
					dependency.TVmazeID != 0 && dependency.TVmazeID != ids.TVmazeID ||
					dependency.MALID != 0 && dependency.MALID != ids.MALID)
			if unrecorded || stale {
				clearInferredProviderID(provider.id, provider.provenance)
				*provider.dependency = api.IdentityDependency{}
				invalidated++
				changed = true
			} else if dependency.ID != 0 && dependency.ID != *provider.id {
				*provider.dependency = api.IdentityDependency{}
			}
		}
	}
	return invalidated
}

// applyDirectProviderID records the lineage of an independently supplied ID.
func applyDirectProviderID(target *int, provenance *api.IdentityProvenance, dependency *api.IdentityDependency, value int, origin string) {
	if applyResolvedID(target, provenance, value, origin) {
		*dependency = api.IdentityDependency{ID: value}
	}
}

// applyLinkedProviderID records lookup inputs only when value fills an empty ID.
// Existing IDs retain their provenance and dependencies.
func applyLinkedProviderID(target *int, provenance *api.IdentityProvenance, dependency *api.IdentityDependency, value int, inputs api.IdentityDependency) {
	if !applyResolvedID(target, provenance, value, "provider") {
		return
	}
	inputs.ID = value
	*dependency = inputs
}

func clearInferredProviderID(id *int, provenance *api.IdentityProvenance) {
	if id == nil || provenance == nil || *provenance == api.IdentityProvenanceExplicit || *provenance == api.IdentityProvenanceRetained {
		return
	}
	*id = 0
	*provenance = api.IdentityProvenanceUnknown
}

func providerIDMatchesExplicit(explicit *int, actual int) bool {
	return !positiveProviderOverride(explicit) || actual == 0 || actual == *explicit
}

func tmdbMetadataMatchesExplicitIDs(overrides api.ExternalIDOverrides, result tmdb.MetadataResult, meta *preparationstate.State) bool {
	if positiveProviderOverride(overrides.TMDBID) {
		return true
	}
	matches := true
	if !providerIDMatchesExplicit(overrides.IMDBID, result.ExternalIMDbID) {
		appendProviderIDConflictWarning(meta, "TMDB", "IMDb", *overrides.IMDBID, result.ExternalIMDbID)
		matches = false
	}
	if !providerIDMatchesExplicit(overrides.TVDBID, result.ExternalTVDBID) {
		appendProviderIDConflictWarning(meta, "TMDB", "TVDB", *overrides.TVDBID, result.ExternalTVDBID)
		matches = false
	}
	return matches
}

func tvmazeMetadataMatchesExplicitIDs(overrides api.ExternalIDOverrides, result tvmaze.SearchResult, meta *preparationstate.State) bool {
	if positiveProviderOverride(overrides.TVmazeID) {
		if !providerIDMatchesExplicit(overrides.TVmazeID, result.SelectedID) {
			appendProviderIDConflictWarning(meta, "TVmaze", "TVmaze", *overrides.TVmazeID, result.SelectedID)
			return false
		}
		return true
	}
	selected := tvmaze.Candidate{}
	for _, candidate := range result.Candidates {
		if candidate.ID == result.SelectedID {
			selected = candidate
			break
		}
	}
	actualIMDB := metautil.ParseIMDbNumeric(selected.Externals.IMDB)
	actualTVDB := selected.Externals.TVDB
	matches := true
	if !providerIDVerifiesExplicit(overrides.IMDBID, actualIMDB) {
		appendProviderIDVerificationWarning(meta, "TVmaze", "IMDb", *overrides.IMDBID, actualIMDB)
		matches = false
	}
	if !providerIDVerifiesExplicit(overrides.TVDBID, actualTVDB) {
		appendProviderIDVerificationWarning(meta, "TVmaze", "TVDB", *overrides.TVDBID, actualTVDB)
		matches = false
	}
	return matches
}

func providerIDVerifiesExplicit(explicit *int, actual int) bool {
	return !positiveProviderOverride(explicit) || actual == *explicit
}

func appendProviderIDVerificationWarning(meta *preparationstate.State, source, provider string, expected, actual int) {
	if actual == 0 {
		appendIdentityCorrectionWarning(
			meta,
			fmt.Sprintf("%s metadata did not verify the supplied %s ID %d. Review the supplied provider IDs before upload.", source, provider, expected),
		)
		return
	}
	appendProviderIDConflictWarning(meta, source, provider, expected, actual)
}

func appendProviderIDConflictWarning(meta *preparationstate.State, source, provider string, expected, actual int) {
	appendIdentityCorrectionWarning(
		meta,
		fmt.Sprintf(
			"%s metadata references %s ID %d, but the supplied %s ID is %d. Correct one of the supplied provider IDs.",
			source,
			provider,
			actual,
			provider,
			expected,
		),
	)
}

func appendIdentityCorrectionWarning(meta *preparationstate.State, warning string) {
	if meta == nil || strings.TrimSpace(warning) == "" {
		return
	}
	if slices.Contains(meta.LookupWarnings, warning) {
		return
	}
	meta.LookupWarnings = append(meta.LookupWarnings, warning)
}

func invalidateProviderMetadataForExplicitReconciliation(metadata *api.SourceScopedMetadata, overrides api.ExternalIDOverrides) {
	if metadata == nil {
		return
	}
	if !keepProvider(overrides.KeepTMDB) && positiveProviderOverride(overrides.TMDBID) &&
		(positiveProviderOverride(overrides.IMDBID) || positiveProviderOverride(overrides.TVDBID) || positiveProviderOverride(overrides.TVmazeID)) {
		metadata.TMDB = nil
	}
	if !keepProvider(overrides.KeepTVmaze) && positiveProviderOverride(overrides.TVmazeID) &&
		(positiveProviderOverride(overrides.IMDBID) || positiveProviderOverride(overrides.TVDBID) || positiveProviderOverride(overrides.TMDBID)) {
		metadata.TVmaze = nil
	}
}

func copyRetainedProviderMetadata(
	target *api.SourceScopedMetadata,
	previous api.SourceScopedMetadata,
	ids api.ExternalIdentity,
	overrides api.ExternalIDOverrides,
) {
	if !sourceScopedMetadataMatches(previous.SourcePath, ids.SourcePath) {
		return
	}
	if keepProvider(overrides.KeepTMDB) && usableTMDBMetadata(previous.TMDB, ids.TMDBID, ids.Category) {
		target.TMDB = previous.TMDB
	}
	if keepProvider(overrides.KeepIMDB) && usableIMDBMetadata(previous.IMDB, ids.IMDBID) {
		target.IMDB = previous.IMDB
	}
	if shouldUseTVDBForCategory(preparationstate.State{}, ids) {
		if keepProvider(overrides.KeepTVDB) && usableTVDBMetadata(previous.TVDB, ids.TVDBID) {
			// Keep freezes provider evidence. Repair a derived omission only when
			// the explicit alias year belongs to that stored series-year decision.
			retained := *previous.TVDB
			if tvdbNameDisambiguationInput(&retained).ExplicitNamingYear && retained.NameDisambiguation.SeriesYear == retained.Year {
				retained.NameDisambiguation.IncludeYear = true
			}
			target.TVDB = &retained
		}
		if keepProvider(overrides.KeepTVmaze) && usableTVmazeMetadata(previous.TVmaze, ids.TVmazeID) {
			target.TVmaze = previous.TVmaze
		}
	}
	if keepProvider(overrides.KeepMAL) && previous.AniList != nil && ids.MALID > 0 && previous.AniList.MALID == ids.MALID {
		target.AniList = previous.AniList
	}
}

func invalidateMismatchedProviderMetadata(metadata *api.SourceScopedMetadata, ids api.ExternalIdentity) {
	if metadata == nil {
		return
	}
	if metadata.TMDB != nil && (ids.TMDBID == 0 || metadata.TMDB.TMDBID != ids.TMDBID || !tmdbMetadataMatchesCategory(metadata.TMDB, ids.Category)) {
		metadata.TMDB = nil
	}
	if metadata.IMDB != nil && (ids.IMDBID == 0 || metadata.IMDB.IMDBID != ids.IMDBID) {
		metadata.IMDB = nil
	}
	if metadata.TVDB != nil && (ids.TVDBID == 0 || metadata.TVDB.TVDBID != ids.TVDBID) {
		metadata.TVDB = nil
	}
	if metadata.TVmaze != nil && (ids.TVmazeID == 0 || metadata.TVmaze.TVmazeID != ids.TVmazeID) {
		metadata.TVmaze = nil
	}
	if metadata.AniList != nil && (ids.MALID == 0 || metadata.AniList.MALID != ids.MALID) {
		metadata.AniList = nil
	}
}

func usableTMDBMetadata(metadata *api.TMDBMetadata, tmdbID int, category api.CanonicalCategory) bool {
	return metadata != nil && tmdbID > 0 && metadata.TMDBID == tmdbID && tmdbMetadataMatchesCategory(metadata, category) &&
		strings.TrimSpace(metadata.Title) != ""
}

func tmdbMetadataMatchesCategory(metadata *api.TMDBMetadata, category api.CanonicalCategory) bool {
	if metadata == nil {
		return false
	}
	expected := normalizeCategory(string(category))
	return expected == "" || normalizeCategory(metadata.Category) == expected
}

func usableIMDBMetadata(metadata *api.IMDBMetadata, imdbID int) bool {
	return metadata != nil && imdbID > 0 && metadata.IMDBID == imdbID && strings.TrimSpace(metadata.Title) != ""
}

func usableTVDBMetadata(metadata *api.TVDBMetadata, tvdbID int) bool {
	return metadata != nil && tvdbID > 0 && metadata.TVDBID == tvdbID && metadata.DisplayName() != ""
}

// reusableTVDBNameDisambiguation reports whether collision evidence can support
// site-specific naming without another TVDB search. Older negative decisions
// must be recomputed when the selected title or alias explicitly carries a year.
func reusableTVDBNameDisambiguation(metadata *api.TVDBMetadata) bool {
	if metadata == nil || tvdbNameDisambiguationInput(metadata).ExplicitNamingYear && !metadata.NameDisambiguation.IncludeYear {
		return false
	}
	status := metadata.NameDisambiguation.Status
	return status == api.MetadataEvidenceStatusComplete || status == api.MetadataEvidenceStatusPartial
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
// Explicit category corrections win over parsed and retained evidence. Without a
// correction, the resolved identity category wins over earlier source evidence.
func shouldUseTVDBForCategory(meta preparationstate.State, ids api.ExternalIdentity) bool {
	if meta.ReleaseNameOverrides.Category != nil {
		switch normalizeCategory(*meta.ReleaseNameOverrides.Category) {
		case "TV":
			return true
		case "MOVIE":
			return false
		}
	}
	if category := normalizeCategory(string(ids.Category)); category != "" {
		return category == "TV"
	}
	candidates := []string{string(ids.Category), string(meta.Identity.Category), meta.MediaInfoCategory, meta.Release.Category}
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

// clearTVProvidersForNonTVCategory removes automatic TV IDs and all TV provider
// metadata when the resolved category no longer permits their use.
func clearTVProvidersForNonTVCategory(meta preparationstate.State, ids *api.ExternalIdentity, metadata *api.SourceScopedMetadata) {
	if ids == nil {
		return
	}
	if shouldUseTVDBForCategory(meta, *ids) {
		return
	}
	for _, provider := range []struct {
		id         *int
		provenance *api.IdentityProvenance
		override   api.OverrideState
		dependency *api.IdentityDependency
	}{
		{&ids.TVDBID, &ids.Provenance.TVDB, ids.Overrides.TVDB, &ids.Dependencies.TVDB},
		{&ids.TVmazeID, &ids.Provenance.TVmaze, ids.Overrides.TVmaze, &ids.Dependencies.TVmaze},
	} {
		if provider.override != api.OverrideStateValue && *provider.provenance != api.IdentityProvenanceExplicit {
			*provider.id = 0
			*provider.provenance = ""
			*provider.dependency = api.IdentityDependency{}
		}
	}
	if metadata != nil {
		metadata.TVDB = nil
		metadata.TVmaze = nil
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
func (s *Service) ensureExternalClients(ctx context.Context) (TMDBClient, AniListClient, IMDBClient, TVDBClient, TVmazeClient) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if s.tmdb == nil {
		apiKey := strings.TrimSpace(s.cfg.MainSettings.TMDBAPI)
		if apiKey != "" {
			s.tmdb = tmdb.NewClient(nil, s.logger, apiKey)
		} else if s.logger != nil {
			logging.FromContext(ctx, s.logger).Debugf("metadata: tmdb client disabled reason=api_key_missing")
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
	if meta.ReleaseNameOverrides.ManualYear != nil && (resolveCategoryPreference(meta) != "TV" || !matchingTVDBMetadataForNaming(meta)) {
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

func resolveCategoryPreference(meta preparationstate.State) string {
	if meta.ReleaseNameOverrides.Category != nil {
		category := normalizeCategory(*meta.ReleaseNameOverrides.Category)
		if category != "" {
			return category
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
	for _, candidate := range []string{meta.Release.Category, meta.MediaInfoCategory} {
		if normalizeCategory(candidate) == "MOVIE" {
			return "MOVIE"
		}
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

func applyOverrideID(target *int, source *api.IdentityProvenance, state *api.OverrideState, override *int) {
	if target == nil || source == nil || state == nil || override == nil {
		return
	}
	if *override == 0 {
		*target = 0
		*source = api.IdentityProvenanceExplicit
		*state = api.OverrideStateClear
		return
	}
	*target = *override
	*source = api.IdentityProvenanceExplicit
	*state = api.OverrideStateValue
}

func applyResolvedID(target *int, source *api.IdentityProvenance, value int, origin string) bool {
	if value == 0 || target == nil || source == nil {
		return false
	}
	if *target != 0 {
		return false
	}
	*target = value
	*source = identityProvenance(origin)
	return true
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
	return providerid.IMDb(value).Prefixed()
}

func formatOptionalInt(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
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
		SpokenLanguages:     append([]string(nil), result.SpokenLanguages...),
		Overview:            result.Overview,
		Poster:              result.Poster,
		TMDBPosterPath:      result.TMDBPosterPath,
		Logo:                result.Logo,
		LogoLookupAttempted: result.LogoLookupAttempted,
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
	runtimes := make([]api.IMDBEditionDetail, len(info.EditionRuntimes))
	for i, row := range info.EditionRuntimes {
		runtimes[i] = api.IMDBEditionDetail{Seconds: row.Seconds, Attributes: slices.Clone(row.Attributes)}
	}
	return &api.IMDBMetadata{
		EditionRuntimes:  runtimes,
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
		SpokenLanguages:  slices.Clone(info.SpokenLanguages),
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
			SeasonKnown: value.SeasonKnown,
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
	year := metautil.FirstInt(details.Year, parseYearFromDate(details.FirstAired))
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
		if name := strings.TrimSpace(fallbackName); strings.TrimSpace(details.Name) == "" && name != "" {
			aliases = []string{name}
		}
	}

	return &api.TVDBMetadata{
		RemoteLinks: api.TVDBRemoteLinks{
			Status: details.RemoteLinks.Status,
			Links:  slices.Clone(details.RemoteLinks.Links),
		},
		TVDBID:                         id,
		Name:                           strings.TrimSpace(details.Name),
		Overview:                       strings.TrimSpace(details.Overview),
		NameEnglish:                    strings.TrimSpace(details.NameEnglish),
		OverviewEnglish:                strings.TrimSpace(details.OverviewEnglish),
		FirstAired:                     strings.TrimSpace(details.FirstAired),
		Year:                           year,
		YearFromAlias:                  yearFromAlias,
		YearSource:                     yearSource,
		YearConfidence:                 yearConfidence,
		NameDisambiguation:             mapTVDBNameDisambiguation(details.NameDisambiguation),
		Type:                           strings.TrimSpace(details.Type),
		Status:                         strings.TrimSpace(details.Status),
		Network:                        strings.TrimSpace(details.Network),
		OriginalCountry:                strings.TrimSpace(details.OriginalCountry),
		OriginalLanguage:               strings.TrimSpace(details.OriginalLanguage),
		HasEnglish:                     strings.TrimSpace(details.NameEnglish) != "" || strings.TrimSpace(details.OverviewEnglish) != "",
		Genres:                         strings.TrimSpace(strings.Join(details.Genres, ", ")),
		Poster:                         strings.TrimSpace(details.Poster),
		PosterThumbnail:                strings.TrimSpace(details.PosterThumbnail),
		PosterThumbnailLookupAttempted: details.PosterThumbnailLookupAttempted,
		Aliases:                        aliases,
	}
}

func tvdbNameDisambiguationInput(metadata *api.TVDBMetadata) tvdb.NameDisambiguationInput {
	return tvdb.NameDisambiguationInput{
		TVDBID:             metadata.TVDBID,
		NameEnglish:        strings.TrimSpace(metadata.NameEnglish),
		SeriesYear:         metadata.Year,
		OriginalCountry:    strings.TrimSpace(metadata.OriginalCountry),
		ExplicitNamingYear: metadata.Year > 0 && metadata.YearFromAlias && tvdb.IsExplicitSeriesYearSource(metadata.YearSource),
	}
}

func mapTVDBNameDisambiguation(details tvdb.NameDisambiguation) api.TVDBNameDisambiguation {
	return api.TVDBNameDisambiguation{
		CanonicalName:         strings.TrimSpace(details.CanonicalName),
		SeriesYear:            details.SeriesYear,
		Locale:                strings.TrimSpace(details.Locale),
		SameNameSeries:        details.SameNameSeries,
		SameNameAndYearSeries: details.SameNameAndYearSeries,
		IncludeYear:           details.IncludeYear,
		IncludeLocale:         details.IncludeLocale,
		Status:                details.Status,
		Source:                strings.TrimSpace(details.Source),
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
	if incoming.RemoteLinks.Status != "" {
		target.RemoteLinks = incoming.RemoteLinks
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
	if validMetadataEvidenceStatus(incoming.NameDisambiguation.Status) {
		target.NameDisambiguation = incoming.NameDisambiguation
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
	if strings.TrimSpace(target.Poster) == strings.TrimSpace(incoming.Poster) && strings.TrimSpace(incoming.PosterThumbnail) != "" {
		target.PosterThumbnail = incoming.PosterThumbnail
	}
	target.PosterThumbnailLookupAttempted = target.PosterThumbnailLookupAttempted || incoming.PosterThumbnailLookupAttempted
	if len(target.Aliases) == 0 && len(incoming.Aliases) > 0 {
		target.Aliases = append([]string{}, incoming.Aliases...)
	}
	if len(target.Episodes) == 0 && len(incoming.Episodes) > 0 {
		target.Episodes = append([]api.TVDBEpisodeMetadata(nil), incoming.Episodes...)
	}
	target.HasEnglish = tvdbHasEnglishContent(target)
}

func validMetadataEvidenceStatus(status api.MetadataEvidenceStatus) bool {
	switch status {
	case api.MetadataEvidenceStatusComplete,
		api.MetadataEvidenceStatusPartial,
		api.MetadataEvidenceStatusUnavailable,
		api.MetadataEvidenceStatusContradictory:
		return true
	default:
		return false
	}
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
	backdrop := metautil.FirstNonEmptyTrimmed(selected.Backdrop.Original, selected.Backdrop.Medium)
	backdropMedium := metautil.FirstNonEmptyTrimmed(selected.Backdrop.Medium, selected.Backdrop.Original)
	return &api.TVmazeMetadata{
		TVmazeID:                result.SelectedID,
		Name:                    strings.TrimSpace(selected.Name),
		Premiered:               strings.TrimSpace(selected.Premiered),
		Ended:                   strings.TrimSpace(selected.Ended),
		Summary:                 strings.TrimSpace(selected.Summary),
		Status:                  strings.TrimSpace(selected.Status),
		Type:                    strings.TrimSpace(selected.Type),
		Language:                strings.TrimSpace(selected.Language),
		Genres:                  strings.TrimSpace(strings.Join(genres, ", ")),
		Runtime:                 selected.Runtime,
		AverageRuntime:          selected.AverageRuntime,
		Rating:                  selected.Rating,
		Weight:                  selected.Weight,
		OfficialSite:            strings.TrimSpace(selected.OfficialSite),
		Country:                 strings.TrimSpace(selected.Country),
		Network:                 strings.TrimSpace(selected.Network.Name),
		NetworkCountry:          strings.TrimSpace(selected.Network.Country),
		NetworkLogo:             metautil.FirstNonEmptyTrimmed(selected.Network.Logo, selected.Network.LogoSmall),
		WebChannel:              strings.TrimSpace(selected.WebChannel.Name),
		WebCountry:              strings.TrimSpace(selected.WebChannel.Country),
		WebLogo:                 metautil.FirstNonEmptyTrimmed(selected.WebChannel.Logo, selected.WebChannel.LogoSmall),
		Poster:                  poster,
		PosterMedium:            posterMedium,
		Backdrop:                backdrop,
		BackdropMedium:          backdropMedium,
		BackdropLookupAttempted: selected.BackdropLookupAttempted,
		IMDBID:                  imdbID,
		TVDBID:                  tvdbID,
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
	logger := logging.FromContext(ctx, s.logger)

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

	if !shouldUseTVDBForCategory(meta, *ids) {
		return meta
	}
	if ids.Category == "" {
		ids.Category = api.CanonicalCategoryTV
	}

	fallbackSeason, fallbackEpisode := meta.SeasonEpisodeWithParsedFallback()
	season, episode := meta.CanonicalSeasonEpisode()
	// Explicit season/episode instructions seed the provider lookups below so
	// episode titles, overviews, and air dates match the corrected values.
	manualSeason, manualEpisode, manualSeasonSet := manualSeasonEpisodeInstructionValues(meta.ReleaseNameOverrides)
	explicitZero := meta.HasExplicitSeasonZero()
	if meta.ReleaseNameOverrides.Season != nil {
		explicitZero = manualSeasonSet && manualSeason == 0
	}
	if manualSeasonSet {
		season = manualSeason
	}
	if manualEpisode > 0 {
		episode = manualEpisode
	}
	initialSeason := season
	initialEpisode := episode
	initialSeasonStr := strings.TrimSpace(meta.SeasonStr)
	initialEpisodeStr := strings.TrimSpace(meta.EpisodeStr)
	dailyDate := episodeLookupDate(meta)
	wantsSeasonEpisode := wantsSeasonEpisodeNaming(meta.ReleaseNameOverrides)
	hasManualSeasonEpisode := hasManualSeasonEpisodeOverrides(meta.ReleaseNameOverrides)
	tmdbDateMatch := false

	if !explicitZero && tmdbClient != nil && dailyDate != "" && ids.TMDBID != 0 &&
		((season == 0 || episode == 0) || (wantsSeasonEpisode && !hasManualSeasonEpisode)) {
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
				} else {
					logger.Debugf("metadata: tmdb daily season/episode lookup returned no exact match")
				}
			} else {
				logger.Debugf("metadata: tmdb daily season/episode lookup failed: %v", mapErr)
			}
		}
	}

	if !explicitZero && meta.Anime && !meta.TVPack && ids.TVDBID != 0 {
		extracted := seasonep.Extract(meta.SourcePath, meta)
		absoluteEpisode := extracted.AbsoluteEpisode
		if absoluteEpisode > 0 {
			xemClient := thexem.NewClient(nil, logger)
			if mappedSeason, mappedEpisode, err := xemClient.MapAbsoluteEpisode(ctx, ids.TVDBID, absoluteEpisode); err == nil {
				season = metautil.FirstInt(mappedSeason, season)
				episode = metautil.FirstInt(mappedEpisode, episode)
			} else {
				logger.Debugf("metadata: thexem absolute mapping failed tvdb_id=%d error=%s", ids.TVDBID, redaction.RedactValue(err.Error(), nil))
			}
			if season == 0 {
				title := resolveSeriesTitle(meta, external)
				if title != "" {
					if matchedSeason, err := xemClient.MatchSeasonByName(ctx, ids.TVDBID, title); err == nil && matchedSeason > 0 {
						season = matchedSeason
					} else if err != nil {
						logger.Debugf("metadata: thexem season lookup failed tvdb_id=%d error=%s", ids.TVDBID, redaction.RedactValue(err.Error(), nil))
					}
				}
			}
			if episode == 0 {
				episode = absoluteEpisode
			}
		}
	}

	// Explicit instructions stay authoritative over daily-date and
	// absolute-episode remapping; the provider episode lookups below consume
	// the manual values.
	if manualSeasonSet {
		season = manualSeason
	}
	if manualEpisode > 0 {
		episode = manualEpisode
	}

	meta.SeasonInt = season
	meta.EpisodeInt = episode
	meta.SeasonStr = seasonep.FormatSeason(season)
	if explicitZero {
		meta.SeasonStr = "S00"
	}
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
		if explicitZero {
			querySeason = 0
		}
		queryEpisode := metautil.FirstInt(episode, fallbackEpisode)
		query := tvdb.EpisodeQuery{
			Season:      querySeason,
			SeasonKnown: explicitZero,
			Episode:     queryEpisode,
			AiredDate:   dailyDate,
			Debug:       false,
		}
		if meta.Anime && !explicitZero {
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
					external.TVDB.NameEnglish = aliasName
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
				if match, ok := tvdb.GetSpecificEpisodeData(
					episodes,
					query,
				); ok &&
					(!explicitZero || match.SeasonNumber == 0 && match.EpisodeNumber == episode) {
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
						if explicitZero && (!external.TVDB.EpisodeSeasonKnown || external.TVDB.EpisodeSeason != match.SeasonNumber ||
							external.TVDB.EpisodeNumber != match.EpisodeNumber) {
							external.TVDB.EpisodeNameEnglish = ""
							external.TVDB.EpisodeOverviewEnglish = ""
						}
						external.TVDB.EpisodeSeason = match.SeasonNumber
						external.TVDB.EpisodeSeasonKnown = match.SeasonKnown
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
								logger.Debugf("metadata: tvdb episode english translation lookup failed: %v", translationErr)
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
		} else {
			logger.Debugf("metadata: tvdb episode lookup failed: %v", err)
		}
	}

	if !meta.TVPack && !tvdbEpisodeMatched && external != nil && external.IMDB != nil {
		var match api.IMDBEpisode
		var ok bool
		if explicitZero {
			match, ok = findIMDBSpecialEpisode(external.IMDB.Episodes, episode)
		} else {
			match, ok = findIMDBEpisode(external.IMDB.Episodes, season, episode, dailyDate)
		}
		if ok {
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

	if !explicitZero && !meta.TVPack && ids.TVmazeID != 0 {
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
		if err != nil {
			logger.Debugf("metadata: tvmaze episode lookup failed: %v", err)
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
		if explicitZero {
			lookupSeason = 0
		}
		lookupEpisode := metautil.FirstInt(episode, fallbackEpisode)
		if !meta.TVPack && (lookupSeason > 0 || explicitZero) && lookupEpisode > 0 {
			if details, err := tmdbClient.GetEpisodeDetails(ctx, ids.TMDBID, lookupSeason, lookupEpisode); err == nil &&
				(!explicitZero || details.SeasonKnown && details.SeasonNumber == 0 && details.EpisodeNumber == episode) {
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
			} else if err != nil {
				logger.Debugf("metadata: tmdb episode details lookup failed: %v", err)
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
			} else {
				logger.Debugf("metadata: tmdb season details lookup failed: %v", err)
			}
		}
	}
	if manualSeasonSet {
		season = manualSeason
	}
	if manualEpisode > 0 {
		episode = manualEpisode
	}

	meta.SeasonInt = season
	meta.EpisodeInt = episode
	meta.SeasonStr = seasonep.FormatSeason(season)
	if explicitZero {
		meta.SeasonStr = "S00"
	}
	meta.EpisodeStr = seasonep.FormatEpisode(episode)
	meta.EpisodeYear = metautil.FirstInt(episodeYear, meta.EpisodeYear)
	meta.EpisodeTitle = sanitizeEpisodeTitle(
		metautil.FirstNonEmptyTrimmed(episodeTitle, tvdbEpisodeTitle, tvmazeEpisodeTitle, tmdbEpisodeTitle, imdbEpisodeTitle),
	)
	meta.EpisodeOverview = metautil.FirstNonEmptyTrimmed(episodeOverview, tvdbEpisodeOverview, tvmazeEpisodeOverview, tmdbEpisodeOverview)

	if initialSeason != season || initialEpisode != episode || initialSeasonStr != meta.SeasonStr || initialEpisodeStr != meta.EpisodeStr {
		logger.Debugf(
			"metadata: tv episode metadata updated season=%q->%q episode=%q->%q daily_date=%q title=%q",
			initialSeasonStr,
			meta.SeasonStr,
			initialEpisodeStr,
			meta.EpisodeStr,
			strings.TrimSpace(meta.DailyEpisodeDate),
			strings.TrimSpace(meta.EpisodeTitle),
		)
	}

	if !explicitZero && tmdbClient != nil && wantsSeasonEpisode && !hasManualSeasonEpisode && !tmdbDateMatch &&
		strings.TrimSpace(meta.DailyEpisodeDate) != "" &&
		ids.TMDBID != 0 {
		logger.Warnf(
			"metadata: season/episode naming requested but TMDB season/episode lookup failed for daily_date=%q tmdb_id=%d",
			strings.TrimSpace(meta.DailyEpisodeDate),
			ids.TMDBID,
		)
	}

	return meta
}

func episodeLookupDate(meta preparationstate.State) string {
	if meta.ReleaseNameOverrides.ManualDate != nil {
		if date := strings.TrimSpace(*meta.ReleaseNameOverrides.ManualDate); date != "" {
			return date
		}
	}
	return strings.TrimSpace(meta.DailyEpisodeDate)
}

// findIMDBSpecialEpisode excludes date and absolute fallbacks for explicit S00.
func findIMDBSpecialEpisode(episodes []api.IMDBEpisode, episode int) (api.IMDBEpisode, bool) {
	for _, candidate := range episodes {
		if candidate.SeasonKnown && candidate.Season == 0 && episode > 0 && parseIMDBEpisodeNumber(candidate.EpisodeText) == episode {
			return candidate, true
		}
	}
	return api.IMDBEpisode{}, false
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
		if external.TVDB != nil {
			if name := external.TVDB.DisplayName(); name != "" {
				return name
			}
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
	episodeKey := metautil.TitleIdentityKey(episodeTitle)
	if len(episodeKey) < 6 {
		return false
	}
	for _, title := range seriesTitleCandidates(meta, external) {
		if episodeKey == metautil.TitleIdentityKey(title) {
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
		candidates = append(candidates, external.TVDB.DisplayName(), external.TVDB.NameEnglish)
	}
	if external.TVmaze != nil {
		candidates = append(candidates, external.TVmaze.Name)
	}
	return candidates
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
	return sourceHasTVCategory(meta.SourcePath)
}

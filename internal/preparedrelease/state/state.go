// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package preparationstate contains mutable evidence while a prepared release
// is being collected. State is private to preparation and must never cross a
// transport, operation-module, or persistence boundary.
package preparationstate

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

// Request is the complete preparation-only handoff to the evidence pipeline.
// It contains no workflow request or tracker selection.
type Request struct {
	// Input contains normalized preparation choices for this evidence pass.
	Input api.PrepareInput
	// Manifest is the validated source snapshot bound to the request.
	Manifest api.SourceManifest
	// Layout exposes preparation-only derived resource roots for the same source.
	Layout sourcelayout.Layout
}

// ReleaseAssessments projects concrete source-derived validation facts from
// private collection state into their canonical typed form.
func (s State) ReleaseAssessments() api.ReleaseAssessments {
	uniqueID := api.UniqueIDStatusMissing
	if !s.requiresMediaInfoUniqueID() {
		uniqueID = api.UniqueIDStatusNotApplicable
	} else if s.MediaInfoUniqueIDPresent {
		uniqueID = api.UniqueIDStatusPresent
	}

	encodeSettings := api.EncodeSettingsStatusMissing
	if strings.EqualFold(s.DiscType, "BDMV") || !strings.EqualFold(s.Type, "ENCODE") || strings.EqualFold(s.VideoCodec, "AV1") {
		encodeSettings = api.EncodeSettingsStatusNotApplicable
	} else if s.MediaInfoEncodeSettingsPresent {
		encodeSettings = api.EncodeSettingsStatusPresent
	}

	return api.ReleaseAssessments{
		MediaInfoUniqueID:       uniqueID,
		MediaInfoEncodeSettings: encodeSettings,
	}
}

func (s State) requiresMediaInfoUniqueID() bool {
	if strings.TrimSpace(s.DiscType) != "" {
		return false
	}
	if len(s.FileList) == 0 {
		return strings.EqualFold(filepath.Ext(s.SourcePath), ".mkv")
	}
	for _, path := range s.FileList {
		if strings.EqualFold(filepath.Ext(path), ".mkv") {
			return true
		}
	}
	return false
}

// State is mutable, source-scoped collection evidence used only during
// preparation. It contains no upload outcome, tracker-selection result, client
// instruction, or transport state. Callers must project it into canonical fact
// groups or operation-owned subjects before it leaves this boundary.
type State struct {
	SourcePath              string
	SourceLookupURL         string
	SourceLookupActive      bool
	SourceLookupMode        string
	SourceLookupTracker     string
	SourceLookupTrackerID   string
	LookupWarnings          []string
	Paths                   []string
	DiscType                string
	VideoPath               string
	FileList                []string
	SourceSize              int64
	MediaInfoJSONPath       string
	MediaInfoTextPath       string
	DVDIFOPath              string
	DVDVOBPath              string
	DVDVOBSet               string
	DVDVOBMediaInfoJSON     string
	DVDVOBMediaInfoText     string
	MediaInfoUniqueID       string
	Scene                   bool
	SceneName               string
	SceneTMDBID             int
	SceneIMDB               int
	SceneTVDBID             int
	SceneTVmazeID           int
	SceneMALID              int
	SceneNFOPath            string
	SceneNFONew             bool
	SceneRenamed            bool
	SceneRenamedReason      string
	EvidenceTrackers        []string
	Policy                  CollectionPolicy
	MatchedEvidenceTrackers []string
	Tag                     string
	Release                 api.ReleaseInfo
	TagOverride             *api.TagOverride
	MetadataOverrides       api.MetadataOverrides
	PersonalRelease         bool
	InfoHash                string
	// ClientEvidence retains the complete detached preparation-owned client snapshot.
	ClientEvidence ClientEvidenceSnapshot
	// DiscoveredTorrentPath is a reusable local metainfo path found during client discovery.
	DiscoveredTorrentPath          string
	TrackerIDs                     map[string]string
	FoundTrackerMatch              bool
	TorrentComments                []api.TorrentMatch
	DescriptionTemplate            string
	PieceSizeConstraint            string
	FoundPreferredPiece            string
	StoredInfoHash                 string
	StoredUpdatedAt                time.Time
	StoredDataFresh                bool
	TrackerData                    []api.TrackerMetadata
	MediaInfoCategory              string
	MediaInfoTMDBID                int
	MediaInfoIMDBID                int
	MediaInfoTVDBID                int
	ArrSource                      string
	ArrTMDBID                      int
	ArrIMDBID                      int
	ArrTVDBID                      int
	ArrTVmazeID                    int
	ArrYear                        int
	ArrGenres                      []string
	ArrReleaseGroup                string
	MismatchedMediaInfoTMDBID      int
	MismatchedMediaInfoIMDBID      int
	MismatchedMediaInfoTVDBID      int
	ExternalIDOverrides            api.ExternalIDOverrides
	ReleaseNameOverrides           api.ReleaseNameOverrides
	SeasonInt                      int
	EpisodeInt                     int
	SeasonStr                      string
	EpisodeStr                     string
	TVDBAiredDate                  string
	TVDBAirsDays                   []string
	TVDBAirsTime                   string
	TVDBAirsTimezone               string
	TVDBAirsTimezoneSource         string
	TVPack                         bool
	DailyEpisodeDate               string
	TMDBDateMatch                  bool
	Anime                          bool
	MALID                          int
	EpisodeTitle                   string
	EpisodeOverview                string
	EpisodeYear                    int
	SelectedBDMVPlaylists          []api.PlaylistInfo
	Identity                       api.ExternalIdentity
	ExternalIdentityCandidates     []api.ExternalIdentityCandidate
	ProviderMetadata               api.SourceScopedMetadata
	AudioLanguages                 []string
	SubtitleLanguages              []string
	Container                      string
	Audio                          string
	Channels                       string
	HasCommentary                  bool
	Is3D                           string
	Source                         string
	Type                           string
	UHD                            string
	HDR                            string
	Distributor                    string
	Region                         string
	VideoCodec                     string
	VideoEncode                    string
	HasEncodeSettings              bool
	BitDepth                       string
	Edition                        string
	Repack                         string
	WebDV                          bool
	MediaInfoUniqueIDPresent       bool
	MediaInfoEncodeSettingsPresent bool
	StreamOptimized                int
	Service                        string
	ServiceLongName                string
	Filename                       string
	ReleaseName                    string
	ReleaseNameNoTag               string
	ReleaseNameClean               string
	ReleaseNameMissing             []string
	BDInfo                         map[string]any
}

// CollectionPolicy contains preparation-only controls used while gathering
// evidence. It excludes upload, tracker-selection, and client instructions.
type CollectionPolicy struct {
	OnlyID          bool
	KeepFolder      bool
	KeepImages      bool
	InteractionMode api.InteractionMode
}

// CanonicalSeasonEpisode returns the provider-resolved TV season/episode.
func (s State) CanonicalSeasonEpisode() (int, int) {
	return s.SeasonInt, s.EpisodeInt
}

// SeasonEpisodeWithParsedFallback returns canonical values with independent
// parsed-name fallback for classification and evidence lookup only.
func (s State) SeasonEpisodeWithParsedFallback() (int, int) {
	season, episode := s.CanonicalSeasonEpisode()
	if season <= 0 {
		season = s.Release.Season
	}
	if episode <= 0 {
		episode = s.Release.Episode
	}
	return season, episode
}

// HasTVSeasonEpisodeSignal reports whether canonical or parsed naming evidence
// carries a season or episode hint.
func (s State) HasTVSeasonEpisodeSignal() bool {
	season, episode := s.SeasonEpisodeWithParsedFallback()
	return season > 0 || episode > 0
}

// LocalizedPTBR returns source-scoped Brazilian Portuguese TMDB data.
func (s State) LocalizedPTBR() api.TMDBLocalizedData {
	if s.ProviderMetadata.TMDB != nil && s.ProviderMetadata.TMDB.Localized != nil {
		if value, ok := s.ProviderMetadata.TMDB.Localized["pt-BR"]; ok {
			return value
		}
	}
	return api.TMDBLocalizedData{}
}

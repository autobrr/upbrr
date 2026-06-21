// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"strings"
	"time"
)

type ServiceSet struct {
	Metadata    MetadataService
	Trackers    TrackerService
	Torrents    TorrentService
	Clients     ClientService
	Filesystem  FilesystemService
	Dupes       DupeService
	Screenshots ScreenshotService
	Images      ImageHostingService
}

type MetadataService interface {
	Prepare(ctx context.Context, req Request) (PreparedMetadata, error)
	RefreshPreparedMetadata(ctx context.Context, meta PreparedMetadata) (PreparedMetadata, error)
	EnrichTrackerData(ctx context.Context, meta PreparedMetadata) (PreparedMetadata, error)
	ApplyMediaInfoIDs(ctx context.Context, meta PreparedMetadata) (PreparedMetadata, error)
	ApplyArrData(ctx context.Context, meta PreparedMetadata) (PreparedMetadata, error)
	ResolveExternalIDs(ctx context.Context, meta PreparedMetadata) (PreparedMetadata, error)
	ApplyMediaDetails(ctx context.Context, meta PreparedMetadata) (PreparedMetadata, error)
}

type TrackerService interface {
	Upload(ctx context.Context, meta PreparedMetadata) (UploadSummary, error)
	BuildPreparation(ctx context.Context, meta PreparedMetadata, trackers []string) (PreparationPreview, error)
	BuildUploadDryRun(ctx context.Context, meta PreparedMetadata, trackers []string) ([]TrackerDryRunEntry, error)
}

type TorrentService interface {
	Create(ctx context.Context, meta PreparedMetadata) (TorrentResult, error)
}

type ClientService interface {
	Inject(ctx context.Context, meta PreparedMetadata, torrent TorrentResult) error
	SearchPathedTorrents(ctx context.Context, meta PreparedMetadata) (ClientSearchResult, error)
}

type FilesystemService interface {
	ValidatePaths(ctx context.Context, paths []string) ([]string, error)
}

type DupeService interface {
	Check(ctx context.Context, meta PreparedMetadata, trackers []string) (DupeCheckSummary, error)
}

type ScreenshotService interface {
	Plan(ctx context.Context, meta PreparedMetadata, count int) (ScreenshotPlan, error)
	Capture(ctx context.Context, meta PreparedMetadata, selections []ScreenshotSelection, purpose ScreenshotPurpose) (ScreenshotResult, error)
	PreviewFrame(ctx context.Context, meta PreparedMetadata, timestampSeconds float64) (ScreenshotPreview, error)
	Delete(ctx context.Context, meta PreparedMetadata, imagePath string) error
	SaveFinalSelections(ctx context.Context, meta PreparedMetadata, images []ScreenshotImage) error
}

type ImageHostingService interface {
	ListCandidates(ctx context.Context, meta PreparedMetadata) ([]ScreenshotImage, error)
	Upload(ctx context.Context, meta PreparedMetadata, host string, usageScope string, images []ScreenshotImage) ([]UploadedImageLink, error)
}

type TrackerBlockReason string

const (
	TrackerBlockReasonDupe  TrackerBlockReason = "dupe"
	TrackerBlockReasonClaim TrackerBlockReason = "claim"
	TrackerBlockReasonAudio TrackerBlockReason = "audio"
)

type PreparedMetadata struct {
	SourcePath                  string
	SourceLookupURL             string
	SourceLookupActive          bool
	SourceLookupMode            string
	SourceLookupTracker         string
	SourceLookupTrackerID       string
	LookupWarnings              []string
	Paths                       []string
	DiscType                    string
	VideoPath                   string
	FileList                    []string
	SourceSize                  int64
	MediaInfoJSONPath           string
	MediaInfoTextPath           string
	DVDIFOPath                  string
	DVDVOBPath                  string
	DVDVOBSet                   string
	DVDVOBMediaInfoJSON         string
	DVDVOBMediaInfoText         string
	MediaInfoUniqueID           string
	Scene                       bool
	SceneName                   string
	SceneTMDBID                 int
	SceneIMDB                   int
	SceneTVDBID                 int
	SceneTVmazeID               int
	SceneMALID                  int
	SceneNFOPath                string
	SceneNFONew                 bool
	Mode                        Mode
	DescriptionGroups           []DescriptionBuilderGroup
	Trackers                    []string
	Options                     UploadOptions
	TrackersRemove              []string
	MatchedTrackers             []string
	Tag                         string
	Release                     ReleaseInfo
	TagOverride                 *TagOverride
	DescriptionOverride         string
	MetadataOverrides           MetadataOverrides
	TrackerConfigOverrides      TrackerConfigOverrides
	TrackerSiteOverrides        TrackerSiteOverrides
	ClientOverrides             ClientOverrides
	ImageHostOverrides          ImageHostOverrides
	ScreenshotOverrides         ScreenshotOverrides
	TorrentOverrides            TorrentOverrides
	DescriptionTemplate         string
	PersonalRelease             bool
	InfoHash                    string
	TrackerIDs                  map[string]string
	FoundTrackerMatch           bool
	TorrentComments             []TorrentMatch
	PieceSizeConstraint         string
	FoundPreferredPiece         string
	StoredInfoHash              string
	StoredUpdatedAt             time.Time `ts_type:"string"`
	StoredDataFresh             bool
	TrackerData                 []TrackerMetadata
	CrossSeedTorrents           []UploadedTorrent
	ClientTorrentPath           string
	TorrentPath                 string
	MediaInfoCategory           string
	MediaInfoTMDBID             int
	MediaInfoIMDBID             int
	MediaInfoTVDBID             int
	ArrSource                   string
	ArrTMDBID                   int
	ArrIMDBID                   int
	ArrTVDBID                   int
	ArrTVmazeID                 int
	ArrYear                     int
	ArrGenres                   []string
	ArrReleaseGroup             string
	MismatchedMediaInfoTMDBID   int
	MismatchedMediaInfoIMDBID   int
	MismatchedMediaInfoTVDBID   int
	ExternalIDOverrides         ExternalIDOverrides
	ReleaseNameOverrides        ReleaseNameOverrides
	TrackerQuestionnaireAnswers map[string]map[string]string
	SeasonInt                   int
	EpisodeInt                  int
	SeasonStr                   string
	EpisodeStr                  string
	TVDBAiredDate               string
	TVDBAirsDays                []string
	TVDBAirsTime                string
	TVDBAirsTimezone            string
	TVDBAirsTimezoneSource      string
	TVPack                      bool
	DailyEpisodeDate            string
	TMDBDateMatch               bool
	Anime                       bool
	MALID                       int
	EpisodeTitle                string
	EpisodeOverview             string
	EpisodeYear                 int
	SelectedBDMVPlaylists       []PlaylistInfo
	ExternalIDs                 ExternalIDs
	ExternalIDCandidates        ExternalIDCandidates
	ExternalMetadata            ExternalMetadata
	AudioLanguages              []string
	SubtitleLanguages           []string
	Container                   string
	Audio                       string
	Channels                    string
	HasCommentary               bool
	Is3D                        string
	Source                      string
	Type                        string
	UHD                         string
	HDR                         string
	Distributor                 string
	Region                      string
	VideoCodec                  string
	VideoEncode                 string
	HasEncodeSettings           bool
	BitDepth                    string
	Edition                     string
	Repack                      string
	WebDV                       bool
	ValidMediaInfo              bool
	ValidMediaInfoSettings      bool
	StreamOptimized             int
	Service                     string
	ServiceLongName             string
	Filename                    string
	ReleaseName                 string
	ReleaseNameNoTag            string
	ReleaseNameClean            string
	ReleaseNameMissing          []string
	BlockedTrackers             map[string][]TrackerBlockReason
	IgnoreTrackerRuleFailures   bool
	TrackerRuleFailures         map[string][]RuleFailure
	BDInfo                      map[string]any
}

type MetadataOverrides struct {
	Distributor      *string
	OriginalLanguage *string
	PersonalRelease  *bool
	Commentary       *bool
	WebDV            *bool
	StreamOptimized  *bool
	Anime            *bool
}

type ClientOverrides struct {
	Client       *string
	QbitCategory *string
	QbitTag      *string
	ForceRecheck *bool
}

type ImageHostOverrides struct {
	PreferredHost *string
	SkipUpload    *bool
}

type TorrentOverrides struct {
	InfoHash        *string
	MaxPieceSizeMiB *int
	NoHash          *bool
	Rehash          *bool
}

type ExternalIDCandidates struct {
	TMDB             []ExternalIDCandidate
	IMDB             []ExternalIDCandidate
	TMDBAutoSelected bool
	IMDBAutoSelected bool
}

type ExternalIDCandidate struct {
	Provider      string
	ID            int
	Title         string
	OriginalTitle string
	Year          int
	Category      string
	MediaType     string
	Overview      string
	PosterURL     string
	Similarity    float64
}

type RuleFailure struct {
	Rule   string
	Reason string
}

type ExternalIDOverrides struct {
	TMDBID   *int
	IMDBID   *int
	TVDBID   *int
	TVmazeID *int
	MALID    *int
}

type ExternalIDs struct {
	SourcePath   string
	TMDBID       int
	IMDBID       int
	TVDBID       int
	TVmazeID     int
	Category     string
	SourceTMDB   string
	SourceIMDB   string
	SourceTVDB   string
	SourceTVmaze string
	UpdatedAt    time.Time `ts_type:"string"`
}

type ExternalMetadata struct {
	SourcePath string
	TMDB       *TMDBMetadata
	IMDB       *IMDBMetadata
	TVDB       *TVDBMetadata
	TVmaze     *TVmazeMetadata
	Bluray     *BlurayMetadata
	UpdatedAt  time.Time `ts_type:"string"`
}

type BlurayMetadata struct {
	SourcePath        string
	IMDBID            int
	SearchURL         string
	SelectedReleaseID string
	SelectedURL       string
	AutoSelected      bool
	SelectionReason   string
	BestScore         float64
	Threshold         float64
	Candidates        []BlurayReleaseCandidate
	UpdatedAt         time.Time `ts_type:"string"`
}

type BlurayReleaseCandidate struct {
	ReleaseID    string
	ProductID    string
	MovieTitle   string
	MovieYear    string
	Title        string
	URL          string
	Price        string
	Publisher    string
	Country      string
	Region       string
	Score        float64
	Accepted     bool
	Warnings     []string
	MatchNotes   []string
	Specs        BluraySpecs
	CoverImages  []BlurayImage
	GenericDisc  bool
	SpecsMissing bool
}

type BluraySpecs struct {
	Video     BlurayVideoSpec
	Audio     []string
	Subtitles []string
	Discs     BlurayDiscSpec
	Playback  BlurayPlaybackSpec
}

type BlurayVideoSpec struct {
	Codec      string
	Resolution string
}

type BlurayDiscSpec struct {
	Type   string
	Count  int
	Format string
}

type BlurayPlaybackSpec struct {
	Region      string
	RegionNotes string
}

type BlurayImage struct {
	Kind string
	URL  string
}

func (m *BlurayMetadata) CandidateByID(releaseID string) *BlurayReleaseCandidate {
	if m == nil {
		return nil
	}
	trimmedID := strings.TrimSpace(releaseID)
	if trimmedID == "" {
		return nil
	}
	for idx := range m.Candidates {
		if strings.EqualFold(strings.TrimSpace(m.Candidates[idx].ReleaseID), trimmedID) {
			return &m.Candidates[idx]
		}
	}
	return nil
}

func (m *BlurayMetadata) SelectedCandidate() *BlurayReleaseCandidate {
	if m == nil {
		return nil
	}
	return m.CandidateByID(m.SelectedReleaseID)
}

func (m *BlurayMetadata) SelectCandidate(releaseID string, auto bool, reason string) bool {
	if m == nil {
		return false
	}
	candidate := m.CandidateByID(releaseID)
	if candidate == nil {
		return false
	}
	m.SelectedReleaseID = strings.TrimSpace(candidate.ReleaseID)
	m.SelectedURL = strings.TrimSpace(candidate.URL)
	m.AutoSelected = auto
	m.SelectionReason = strings.TrimSpace(reason)
	for idx := range m.Candidates {
		trimmedCandidate := strings.TrimSpace(m.Candidates[idx].ReleaseID)
		m.Candidates[idx].Accepted = strings.EqualFold(trimmedCandidate, m.SelectedReleaseID)
	}
	return true
}

// TMDBMetadata is the shared TMDB metadata snapshot returned to CLI, Wails, and
// embedded web callers during upload preparation and review.
type TMDBMetadata struct {
	TMDBID           int
	IMDBID           int
	TVDBID           int
	Category         string
	Title            string
	OriginalTitle    string
	Year             int
	ReleaseDate      string
	FirstAirDate     string
	LastAirDate      string
	OriginCountry    []string
	OriginalLanguage string
	Overview         string
	Poster           string
	TMDBPosterPath   string
	Logo             string
	TMDBLogo         string
	Backdrop         string
	TMDBType         string
	Runtime          int
	Genres           string
	GenreIDs         string
	Creators         []string
	Directors        []string
	Cast             []string
	MALID            int
	Anime            bool
	Demographic      string
	RetrievedAKA     string
	Keywords         string
	// LocalizedTitles maps lowercase language codes and optional regional tags
	// such as "de" or "pt-BR" to TMDB translation titles.
	LocalizedTitles     map[string]string
	YouTube             string
	Certification       string
	ProductionCompanies []TMDBCompany
	ProductionCountries []TMDBCountry
	Networks            []TMDBNetwork
	IMDbMismatch        bool
	MismatchedIMDbID    int
}

type TMDBCompany struct {
	ID            int
	Name          string
	LogoPath      string
	OriginCountry string
}

type TMDBCountry struct {
	ISO3166 string
	Name    string
}

type TMDBNetwork struct {
	ID            int
	Name          string
	LogoPath      string
	OriginCountry string
}

type IMDBMetadata struct {
	IMDBID           int
	IMDbIDText       string
	IMDbURL          string
	Title            string
	Year             int
	EndYear          int
	AKA              string
	Type             string
	Plot             string
	Rating           float64
	RatingCount      int
	RatingText       string
	RuntimeMinutes   int
	RuntimeText      string
	Genres           string
	Country          string
	CountryList      string
	Cover            string
	Directors        []IMDBPerson
	Creators         []IMDBPerson
	Writers          []IMDBPerson
	Stars            []IMDBPerson
	Editions         []string
	EditionDetails   map[string]IMDBEditionDetail
	Akas             []IMDBAKA
	Episodes         []IMDBEpisode
	SeasonsSummary   []IMDBSeasonSummary
	SoundMixes       []string
	TVYear           int
	OriginalLanguage string
}

type IMDBPerson struct {
	ID   string
	Name string
}

type IMDBEditionDetail struct {
	DisplayName string
	Seconds     int
	Minutes     int
	Attributes  []string
}

type IMDBAKA struct {
	Title      string
	Country    string
	Language   string
	Attributes []string
}

type IMDBEpisode struct {
	ID          string
	Title       string
	ReleaseYear int
	ReleaseDate IMDBReleaseDate
	Season      int
	EpisodeText string
}

type IMDBReleaseDate struct {
	Year  int
	Month int
	Day   int
}

type IMDBSeasonSummary struct {
	Season    int
	Year      int
	YearRange string
}

type TVDBMetadata struct {
	TVDBID                 int
	Name                   string
	NameEnglish            string
	Overview               string
	OverviewEnglish        string
	FirstAired             string
	Year                   int
	YearFromAlias          bool
	Type                   string
	Status                 string
	Network                string
	OriginalCountry        string
	OriginalLanguage       string
	HasEnglish             bool
	Genres                 string
	Poster                 string
	Aliases                []string
	EpisodeSeason          int
	EpisodeNumber          int
	EpisodeName            string
	EpisodeNameEnglish     string
	EpisodeOverview        string
	EpisodeOverviewEnglish string
	EpisodeAired           string
}

type TVmazeMetadata struct {
	TVmazeID       int
	Name           string
	Premiered      string
	Ended          string
	Summary        string
	Status         string
	Type           string
	Language       string
	Genres         string
	Runtime        int
	AverageRuntime int
	Rating         float64
	Weight         int
	OfficialSite   string
	Country        string
	Network        string
	NetworkCountry string
	NetworkLogo    string
	WebChannel     string
	WebCountry     string
	WebLogo        string
	Poster         string
	PosterMedium   string
	Backdrop       string
	BackdropMedium string
	IMDBID         int
	TVDBID         int
}

type ClientSearchResult struct {
	InfoHash            string
	TrackerIDs          map[string]string
	FoundTrackerMatch   bool
	TorrentComments     []TorrentMatch
	PieceSizeConstraint string
	FoundPreferredPiece string
	MatchedTrackers     []string
	TorrentPath         string
}

type TorrentMatch struct {
	Hash              string
	Name              string
	SavePath          string
	ContentPath       string
	Size              int64
	Category          string
	Seeders           int64
	Tracker           string
	HasWorkingTracker bool
	Comment           string
	TrackerURLsRaw    []string
	TrackerURLs       []TrackerMatch
	HasTracker        bool
}

type TrackerMatch struct {
	ID        string
	TrackerID string
}

type ReleaseInfo struct {
	Category   string
	Type       string
	Artist     string
	Title      string
	Subtitle   string
	Alt        string
	Year       int
	Month      int
	Day        int
	Source     string
	Resolution string
	Codec      []string
	Audio      []string
	HDR        []string
	Ext        string
	Language   []string
	Site       string
	Genre      string
	Channels   string
	Collection string
	Region     string
	Size       string
	Group      string
	Disc       string
	Season     int
	Episode    int
	Edition    []string
	Other      []string
}

type TagOverride struct {
	Type            string
	Source          string
	Template        string
	PersonalRelease bool
}

type UploadSummary struct {
	Uploaded         int
	UploadedTorrents []UploadedTorrent
}

type UploadedTorrent struct {
	Tracker     string
	TorrentID   string
	DownloadURL string
	TorrentURL  string
	TorrentPath string
}

type TrackerQuestionnaire struct {
	Tracker string
	Fields  []TrackerQuestionnaireField
}

type TrackerQuestionnaireField struct {
	Key         string
	Label       string
	Kind        string
	Options     []string
	Value       string
	Placeholder string
	Help        string
	Required    bool
}

type TorrentResult struct {
	Path      string
	InfoHash  string
	URL       string
	Tracker   string
	CrossSeed bool
}

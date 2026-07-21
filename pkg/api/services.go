// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

type ServiceSet struct {
	// Metadata is an internal preparation collector override. Core validates the
	// private contract before use; no mutable preparation type is exported here.
	Metadata   any
	Trackers   TrackerService
	Torrents   TorrentService
	Clients    ClientService
	Filesystem FilesystemService
	Dupes      DupeService
	// TrackerAuth validates managed tracker sessions before duplicate checks.
	TrackerAuth TrackerAuthService
	Screenshots ScreenshotService
	// DVDMenus handles automatic capture and persisted disc-menu lifecycle operations.
	DVDMenus DVDMenuService
	Images   ImageHostingService
}

type TrackerService interface {
	Upload(ctx context.Context, subject UploadSubject) (UploadSummary, error)
	BuildPreparation(ctx context.Context, subject DescriptionSubject, trackers []string) (PreparationPreview, error)
	BuildUploadReview(ctx context.Context, subject UploadSubject, trackers []string) ([]TrackerDryRunEntry, error)
	BuildUploadDryRun(ctx context.Context, subject UploadSubject, trackers []string) ([]TrackerDryRunEntry, error)
}

type TorrentService interface {
	Create(ctx context.Context, subject TorrentSubject) (TorrentResult, error)
}

type ClientService interface {
	Inject(ctx context.Context, subject ClientSubject, torrent TorrentResult) error
	SearchPathedTorrents(ctx context.Context, subject ClientSubject) (ClientSearchResult, error)
}

type FilesystemService interface {
	ValidatePaths(ctx context.Context, paths []string) ([]string, error)
}

type DupeService interface {
	Check(ctx context.Context, subject DuplicateSubject, trackers []string) (DupeCheckSummary, error)
}

// DuplicateSubject is the duplicate module's source-scoped read model. It
// contains only facts, instructions, and prior workflow outcomes that affect
// duplicate search or authorization.
type DuplicateSubject struct {
	SourcePath           string
	SourceSize           int64
	VideoPath            string
	FileList             []string
	Filename             string
	SceneName            string
	ReleaseName          string
	Release              ReleaseInfo
	ReleaseNameOverrides ReleaseNameOverrides
	Identity             ExternalIdentity
	ProviderMetadata     SourceScopedMetadata
	TrackerIDs           map[string]string
	DiscType             string
	Type                 string
	Source               string
	Tag                  string
	HDR                  string
	UHD                  string
	VideoEncode          string
	VideoCodec           string
	HasEncodeSettings    bool
	SeasonInt            int
	EpisodeInt           int
	SeasonStr            string
	EpisodeStr           string
	DailyEpisodeDate     string
	TVPack               bool
	Anime                bool
	MatchedTrackers      []string
	TrackerRuleFailures  map[string][]RuleFailure
	BlockedTrackers      map[string][]TrackerBlockReason
	CrossSeedTorrents    []UploadedTorrent
}

// CanonicalSeasonEpisode returns the prepared TV season and episode.
func (s DuplicateSubject) CanonicalSeasonEpisode() (int, int) {
	return s.SeasonInt, s.EpisodeInt
}

// TrackerAuthService exposes the batch auth operations needed before WebUI and
// embedded-web duplicate checking.
type TrackerAuthService interface {
	// Capabilities returns the configured trackers whose auth workflows the
	// service can classify.
	Capabilities(ctx context.Context) ([]TrackerAuthCapability, error)
	// ValidateMany returns one status per tracker in input order. An error means
	// the batch has no usable status result.
	ValidateMany(ctx context.Context, trackerIDs []string) ([]TrackerAuthStatus, error)
}

type ScreenshotService interface {
	Plan(ctx context.Context, subject ScreenshotSubject, count int) (ScreenshotPlan, error)
	Capture(ctx context.Context, subject ScreenshotSubject, selections []ScreenshotSelection, purpose ScreenshotPurpose) (ScreenshotResult, error)
	PreviewFrame(ctx context.Context, subject ScreenshotSubject, timestampSeconds float64) (ScreenshotPreview, error)
	Delete(ctx context.Context, subject ScreenshotSubject, imagePath string) error
	SaveFinalSelections(ctx context.Context, subject ScreenshotSubject, images []ScreenshotImage) error
}

// DVDMenuService captures and manages persisted menu images for prepared DVD metadata.
type DVDMenuService interface {
	// Capture replaces automatic captures up to maxItems while preserving manual menus.
	Capture(ctx context.Context, subject DVDMenuSubject, maxItems int) (DVDMenuCaptureResult, error)
	// List returns persisted manual and automatic menu images in selection order.
	List(ctx context.Context, subject DVDMenuSubject) ([]ScreenshotImage, error)
	// Delete removes one managed menu image and its local repository references.
	Delete(ctx context.Context, subject DVDMenuSubject, imagePath string) error
	// Capability reports path-free engine and FFmpeg dvdvideo support.
	Capability(ctx context.Context) (DVDMenuEngineInfo, error)
}

type ImageHostingService interface {
	ListCandidates(ctx context.Context, subject ImageHostingSubject) ([]ScreenshotImage, error)
	Upload(ctx context.Context, subject ImageHostingSubject, host string, usageScope string, images []ScreenshotImage) ([]UploadedImageLink, error)
}

type TrackerBlockReason string

const (
	TrackerBlockReasonDupe  TrackerBlockReason = "dupe"
	TrackerBlockReasonClaim TrackerBlockReason = "claim"
	TrackerBlockReasonAudio TrackerBlockReason = "audio"
)

// TrackerSubject is the tracker module's operation-owned source, resource,
// instruction, and prerequisite view. It excludes preparation diagnostics,
// resolver evidence, cache freshness, and client-search implementation state.
type UploadSubject struct {
	SourcePath                  string
	Paths                       []string
	DiscType                    string
	VideoPath                   string
	FileList                    []string
	SourceSize                  int64
	MediaInfoJSONPath           string
	MediaInfoTextPath           string
	DVDVOBMediaInfoText         string
	Scene                       bool
	SceneName                   string
	SceneNFOPath                string
	SceneRenamed                bool
	SceneRenamedReason          string
	DescriptionGroups           []DescriptionBuilderGroup
	Trackers                    []string
	Options                     UploadOptions
	TrackersRemove              []string
	MatchedTrackers             []string
	Tag                         string
	Release                     ReleaseInfo
	DescriptionOverride         string
	TrackerConfigOverrides      TrackerConfigOverrides
	TrackerSiteOverrides        TrackerSiteOverrides
	ImageHostOverrides          ImageHostOverrides
	DescriptionTemplate         string
	PersonalRelease             bool
	InfoHash                    string
	TrackerIDs                  map[string]string
	TrackerData                 []TrackerMetadata
	CrossSeedTorrents           []UploadedTorrent
	ClientTorrentPath           string
	TorrentPath                 string
	ArrReleaseGroup             string
	ReleaseNameOverrides        ReleaseNameOverrides
	TrackerQuestionnaireAnswers map[string]map[string]string
	SeasonInt                   int
	EpisodeInt                  int
	SeasonStr                   string
	EpisodeStr                  string
	TVDBAiredDate               string
	TVDBAirsTime                string
	TVDBAirsTimezone            string
	TVPack                      bool
	DailyEpisodeDate            string
	Anime                       bool
	EpisodeTitle                string
	EpisodeOverview             string
	SelectedBDMVPlaylists       []PlaylistInfo
	Identity                    ExternalIdentity
	ProviderMetadata            SourceScopedMetadata
	Disc                        DiscFacts
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
	Assessments                 ReleaseAssessments
	StreamOptimized             int
	Service                     string
	ServiceLongName             string
	Filename                    string
	ReleaseName                 string
	ReleaseNameNoTag            string
	ReleaseNameClean            string
	BlockedTrackers             map[string][]TrackerBlockReason
	TrackerRuleFailures         map[string][]RuleFailure
}

// RuleSubject contains only stable facts used by generic and tracker-specific
// eligibility rules.
type RuleSubject struct {
	SourcePath          string
	VideoPath           string
	FileList            []string
	DiscType            string
	Scene               bool
	SceneNFOPath        string
	SceneRenamed        bool
	SceneRenamedReason  string
	PersonalRelease     bool
	Release             ReleaseInfo
	ReleaseName         string
	ReleaseNameNoTag    string
	Tag                 string
	Identity            ExternalIdentity
	ProviderMetadata    SourceScopedMetadata
	AudioLanguages      []string
	SubtitleLanguages   []string
	TVPack              bool
	Type                string
	Source              string
	Container           string
	BitDepth            string
	VideoCodec          string
	VideoEncode         string
	HDR                 string
	Region              string
	WebDV               bool
	Anime               bool
	Assessments         ReleaseAssessments
	DescriptionOverride string
}

// NewRuleSubject projects upload facts into the rule evaluator's read model.
func NewRuleSubject(subject UploadSubject) RuleSubject {
	return RuleSubject{
		SourcePath:          subject.SourcePath,
		VideoPath:           subject.VideoPath,
		FileList:            append([]string(nil), subject.FileList...),
		DiscType:            subject.DiscType,
		Scene:               subject.Scene,
		SceneNFOPath:        subject.SceneNFOPath,
		SceneRenamed:        subject.SceneRenamed,
		SceneRenamedReason:  subject.SceneRenamedReason,
		PersonalRelease:     subject.PersonalRelease,
		Release:             subject.Release,
		ReleaseName:         subject.ReleaseName,
		ReleaseNameNoTag:    subject.ReleaseNameNoTag,
		Tag:                 subject.Tag,
		Identity:            subject.Identity,
		ProviderMetadata:    subject.ProviderMetadata,
		AudioLanguages:      append([]string(nil), subject.AudioLanguages...),
		SubtitleLanguages:   append([]string(nil), subject.SubtitleLanguages...),
		TVPack:              subject.TVPack,
		Type:                subject.Type,
		Source:              subject.Source,
		Container:           subject.Container,
		BitDepth:            subject.BitDepth,
		VideoCodec:          subject.VideoCodec,
		VideoEncode:         subject.VideoEncode,
		HDR:                 subject.HDR,
		Region:              subject.Region,
		WebDV:               subject.WebDV,
		Anime:               subject.Anime,
		Assessments:         subject.Assessments,
		DescriptionOverride: subject.DescriptionOverride,
	}
}

// DescriptionSubject contains only facts, local resources, and rendering
// instructions consumed by tracker description builders.
type DescriptionSubject struct {
	SourcePath            string
	DiscType              string
	MediaInfoTextPath     string
	DVDVOBMediaInfoText   string
	DescriptionTemplate   string
	EpisodeOverview       string
	Options               UploadOptions
	Release               ReleaseInfo
	SelectedBDMVPlaylists []PlaylistInfo
	Tag                   string
	Identity              ExternalIdentity
	ProviderMetadata      SourceScopedMetadata
	SeasonInt             int
	EpisodeInt            int
	Filename              string
	ReleaseName           string
	ReleaseNameNoTag      string
	ServiceLongName       string
	Type                  string
	HDR                   string
	ArrReleaseGroup       string
	Trackers              []string
	TrackerConfig         TrackerConfigOverrides
	TrackerSite           TrackerSiteOverrides
	ImageHost             ImageHostOverrides
	TrackerData           []TrackerMetadata
}

// NewDescriptionSubject projects upload state into the description builder's
// read model and detaches mutable collections.
func NewDescriptionSubject(subject UploadSubject) DescriptionSubject {
	projected := DescriptionSubject{
		SourcePath:            subject.SourcePath,
		DiscType:              subject.DiscType,
		MediaInfoTextPath:     subject.MediaInfoTextPath,
		DVDVOBMediaInfoText:   subject.DVDVOBMediaInfoText,
		DescriptionTemplate:   subject.DescriptionTemplate,
		EpisodeOverview:       subject.EpisodeOverview,
		Options:               subject.Options,
		Release:               subject.Release,
		SelectedBDMVPlaylists: append([]PlaylistInfo(nil), subject.SelectedBDMVPlaylists...),
		Tag:                   subject.Tag,
		Identity:              subject.Identity,
		ProviderMetadata:      subject.ProviderMetadata,
		SeasonInt:             subject.SeasonInt,
		EpisodeInt:            subject.EpisodeInt,
		Filename:              subject.Filename,
		ReleaseName:           subject.ReleaseName,
		ReleaseNameNoTag:      subject.ReleaseNameNoTag,
		ServiceLongName:       subject.ServiceLongName,
		Type:                  subject.Type,
		HDR:                   subject.HDR,
		ArrReleaseGroup:       subject.ArrReleaseGroup,
		Trackers:              append([]string(nil), subject.Trackers...),
		TrackerConfig:         subject.TrackerConfigOverrides,
		TrackerSite:           subject.TrackerSiteOverrides,
		ImageHost:             subject.ImageHostOverrides,
		TrackerData:           append([]TrackerMetadata(nil), subject.TrackerData...),
	}
	cloned, err := clonePreparedValue(projected)
	if err != nil {
		panic(fmt.Sprintf("clone description subject: %v", err))
	}
	return cloned
}

// CanonicalSeasonEpisode returns the provider-resolved TV season/episode used
// by tracker operations.
func (s UploadSubject) CanonicalSeasonEpisode() (int, int) {
	return s.SeasonInt, s.EpisodeInt
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

// TorrentSubject contains only facts and instructions required to create or
// validate a torrent artifact for one prepared source.
type TorrentSubject struct {
	SourcePath        string
	SourceSize        int64
	FileList          []string
	DiscType          string
	ClientTorrentPath string
	Trackers          []string
	TorrentOverrides  TorrentOverrides
}

// ClientSubject contains only source facts and caller instructions required
// for torrent-client search, linking, and injection.
type ClientSubject struct {
	SourcePath      string
	FileList        []string
	DiscType        string
	ClientOverrides ClientOverrides
}

// RuleDisposition identifies how a failed tracker rule affects live upload.
type RuleDisposition string

const (
	// RuleDispositionAdvisory reports guidance without blocking live upload.
	RuleDispositionAdvisory RuleDisposition = "advisory"
	// RuleDispositionWaivable requires exact user authorization before live upload.
	RuleDispositionWaivable RuleDisposition = "waivable"
	// RuleDispositionStrict blocks live upload and cannot be authorized.
	RuleDispositionStrict RuleDisposition = "strict"
)

// RuleFailure describes one stable tracker-rule result.
type RuleFailure struct {
	Rule        string
	Reason      string
	Disposition RuleDisposition
}

// RuleAuthorization binds user consent to exact current rule keys for one tracker.
type RuleAuthorization struct {
	Tracker string
	Rules   []string
}

// NormalizeRuleDisposition maps legacy persisted values and fails closed for
// unknown values. Empty and legacy blocking results remain user-waivable;
// unknown non-empty values become strict.
func NormalizeRuleDisposition(disposition RuleDisposition) RuleDisposition {
	switch disposition {
	case RuleDispositionAdvisory, RuleDispositionWaivable, RuleDispositionStrict:
		return disposition
	case "warning":
		return RuleDispositionAdvisory
	case "", "blocking":
		return RuleDispositionWaivable
	default:
		return RuleDispositionStrict
	}
}

// IsBlockingRuleFailure reports whether a rule result blocks tracker work.
func IsBlockingRuleFailure(failure RuleFailure) bool {
	return NormalizeRuleDisposition(failure.Disposition) != RuleDispositionAdvisory
}

// IsStrictRuleFailure reports whether a rule result can never be authorized.
func IsStrictRuleFailure(failure RuleFailure) bool {
	return NormalizeRuleDisposition(failure.Disposition) == RuleDispositionStrict
}

// IsWaivableRuleFailure reports whether exact authorization may unblock a result.
func IsWaivableRuleFailure(failure RuleFailure) bool {
	return NormalizeRuleDisposition(failure.Disposition) == RuleDispositionWaivable
}

// BlockingRuleFailures returns an independent slice containing only blocking
// results. Legacy and unrecognized dispositions are included.
func BlockingRuleFailures(failures []RuleFailure) []RuleFailure {
	return filterRuleFailures(failures, true)
}

// AdvisoryRuleFailures returns an independent slice containing only advisory results.
func AdvisoryRuleFailures(failures []RuleFailure) []RuleFailure {
	return filterRuleFailures(failures, false)
}

// HasBlockingRuleFailures reports whether any rule result blocks tracker work.
func HasBlockingRuleFailures(failures []RuleFailure) bool {
	return slices.ContainsFunc(failures, IsBlockingRuleFailure)
}

// CountBlockingRuleFailures returns the number of rule results that block
// tracker work. Legacy and unrecognized dispositions are counted as blocking.
func CountBlockingRuleFailures(failures []TrackerRuleFailure) int {
	count := 0
	for _, failure := range failures {
		if NormalizeRuleDisposition(failure.Disposition) != RuleDispositionAdvisory {
			count++
		}
	}
	return count
}

// filterRuleFailures copies results whose normalized blocking state matches the
// requested state.
func filterRuleFailures(failures []RuleFailure, blocking bool) []RuleFailure {
	filtered := make([]RuleFailure, 0, len(failures))
	for _, failure := range failures {
		if IsBlockingRuleFailure(failure) == blocking {
			filtered = append(filtered, failure)
		}
	}
	return filtered
}

// ExternalIDOverrides carries caller-supplied ID intent into metadata
// resolution. Nil means the resolver may fill the provider; a positive value
// locks that provider to the supplied ID; zero locks an explicit clear for the
// current request.
type ExternalIDOverrides struct {
	TMDBID   *int
	IMDBID   *int
	TVDBID   *int
	TVmazeID *int
	// MALID carries caller intent for the canonical MAL/AniList-compatible
	// anime identifier. Nil leaves resolution unchanged; zero clears it.
	MALID *int
}

// AniListMetadata is the AniList media snapshot used for MAL/AniList preview.
//
// Date fields keep AniList fuzzy-date precision, score fields are percentages
// from 0 to 100, and AiringAt fields are Unix timestamps in seconds. Tags keep
// adult/spoiler flags so consumers can filter them before display.
type AniListMetadata struct {
	// AniListID is the AniList media ID used in AniList URLs.
	AniListID int
	// MALID is the MyAnimeList media ID used as upbrr's canonical anime ID.
	MALID int
	// SiteURL is the canonical AniList media page URL.
	SiteURL string
	// Title* fields preserve AniList's localized title variants.
	TitleRomaji        string
	TitleEnglish       string
	TitleNative        string
	TitleUserPreferred string
	// Description is AniList's plain-text media description.
	Description string
	// Format, Status, Season, and Source are AniList enum values.
	Format string
	Status string
	// StartDate is formatted as YYYY, YYYY-MM, or YYYY-MM-DD depending on AniList precision.
	StartDate string
	// EndDate is formatted as YYYY, YYYY-MM, or YYYY-MM-DD depending on AniList precision.
	EndDate    string
	Season     string
	SeasonYear int
	Episodes   int
	// Duration is AniList's average episode duration in minutes.
	Duration        int
	CountryOfOrigin string
	Source          string
	// Cover* and BannerImage are AniList image URLs or color metadata used by previews.
	CoverExtraLarge string
	CoverLarge      string
	CoverMedium     string
	CoverColor      string
	BannerImage     string
	Genres          []string
	Synonyms        []string
	// AverageScore and MeanScore are AniList percentage scores from 0 to 100.
	AverageScore      int
	MeanScore         int
	Popularity        int
	Favourites        int
	IsAdult           bool
	Tags              []AniListTag
	Studios           []AniListStudio
	Trailer           AniListTrailer
	NextAiringEpisode AniListAiringEpisode
	ExternalLinks     []AniListExternalLink
}

// AniListTag is a media tag returned by AniList for the selected anime.
type AniListTag struct {
	Name string
	// Rank is AniList's tag relevance percentage from 0 to 100.
	Rank     int
	Category string
	// IsAdult and Is*Spoiler let UI consumers omit sensitive tag labels.
	IsAdult          bool
	IsGeneralSpoiler bool
	IsMediaSpoiler   bool
}

// AniListStudio is a studio attached to an AniList media entry.
type AniListStudio struct {
	ID   int
	Name string
	// SiteURL is the AniList studio page URL.
	SiteURL string
}

// AniListTrailer identifies a media trailer from AniList.
type AniListTrailer struct {
	ID   string
	Site string
	// Thumbnail is the provider thumbnail URL when AniList supplies one.
	Thumbnail string
}

// AniListAiringEpisode describes the next scheduled episode for an airing anime.
type AniListAiringEpisode struct {
	// AiringAt is a Unix timestamp in seconds.
	AiringAt int
	// TimeUntilAiring is seconds from AniList's response time until AiringAt.
	TimeUntilAiring int
	Episode         int
}

// AniListExternalLink is a public provider or official link attached to AniList media.
type AniListExternalLink struct {
	Site     string
	URL      string
	Type     string
	Language string
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

// TMDBMetadata is the shared TMDB metadata snapshot returned to CLI and WebUI
// callers during upload preparation and review.
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
	// such as "de" or "pt-BR" to TMDB translation titles. Nil values marshal as
	// an empty JSON object for WebUI callers.
	LocalizedTitles     map[string]string
	YouTube             string
	Certification       string
	ProductionCompanies []TMDBCompany
	ProductionCountries []TMDBCountry
	Networks            []TMDBNetwork
	IMDbMismatch        bool
	MismatchedIMDbID    int
	Localized           map[string]TMDBLocalizedData
}

type TMDBLocalizedData struct {
	Title           string
	Overview        string
	EpisodeTitle    string
	EpisodeOverview string
	TrailerURL      string
	Genres          string
	ContentRating   string
	Poster          string
}

// ExtractTrackerLocalizedPTBR returns pt-BR provider data from a tracker-owned
// operation subject.
func ExtractTrackerLocalizedPTBR(subject UploadSubject) TMDBLocalizedData {
	if subject.ProviderMetadata.TMDB != nil && subject.ProviderMetadata.TMDB.Localized != nil {
		if value, ok := subject.ProviderMetadata.TMDB.Localized["pt-BR"]; ok {
			return value
		}
	}
	return TMDBLocalizedData{}
}

// MarshalJSON preserves the shared TMDBMetadata shape while emitting
// LocalizedTitles as an object instead of null.
func (m TMDBMetadata) MarshalJSON() ([]byte, error) {
	type tmdbMetadata TMDBMetadata
	payload := tmdbMetadata(m)
	if payload.LocalizedTitles == nil {
		payload.LocalizedTitles = map[string]string{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("api: marshal TMDB metadata: %w", err)
	}
	return data, nil
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

// TVDBEpisodeMetadata stores one TVDB episode entry for tracker payloads that
// need single-episode or season-pack episode descriptions.
type TVDBEpisodeMetadata struct {
	ID                     int
	SeasonNumber           int
	EpisodeNumber          int
	EpisodeName            string
	EpisodeNameEnglish     string
	EpisodeOverview        string
	EpisodeOverviewEnglish string
	// EpisodeAired is the TVDB air date string used in tracker descriptions.
	EpisodeAired string
	// EpisodeImage is the TVDB episode image URL when the API returned one.
	EpisodeImage string
}

// TVDBMetadata stores TVDB series metadata plus the selected episode and any
// episode list fetched for the selected season.
type TVDBMetadata struct {
	TVDBID          int
	Name            string
	NameEnglish     string
	Overview        string
	OverviewEnglish string
	FirstAired      string
	Year            int
	// YearFromAlias reports whether Year is naming-eligible for TV release names.
	YearFromAlias bool
	// YearSource identifies the TVDB source used for Year, such as first_aired, translation_name, translation_alias, extended_alias, or slug.
	YearSource string
	// YearConfidence is "high" for explicit TVDB title/alias years and "low" for guarded slug-derived naming years.
	YearConfidence         string
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
	// EpisodeImage is the selected episode image URL when the API returned one.
	EpisodeImage string
	// Episodes contains fetched TVDB episode entries, usually the season needed
	// by a season-pack upload.
	Episodes []TVDBEpisodeMetadata
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

// ReleaseInfo preserves release-name parser output before provider metadata can
// remap episode identity.
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

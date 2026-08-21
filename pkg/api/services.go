// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	Dupes      ProjectionDupeService
	// TrackerAuth validates tracker readiness at the shared upload-preflight boundary.
	TrackerAuth TrackerAuthService
	Screenshots ScreenshotService
	// DVDMenus handles automatic capture and persisted disc-menu lifecycle operations.
	DVDMenus DVDMenuService
	Images   ImageHostingService
}

type TrackerService interface {
	Upload(ctx context.Context, subject UploadSubject) (UploadSummary, error)
	BuildPreparation(ctx context.Context, subject DescriptionSubject, trackers []string) (PreparationPreview, error)
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

// ProjectionDupeCheckOptions controls projection-bound duplicate execution.
type ProjectionDupeCheckOptions struct {
	// SkipRemote limits evaluation to local/client evidence when true.
	SkipRemote bool
	// BypassBannedGroups allows debug-mode callers to bypass banned-group policy.
	BypassBannedGroups bool
}

// DupeAssessmentEvidence is private retained duplicate authority. Public
// workflow state receives only sanitized candidate projections.
type DupeAssessmentEvidence interface {
	Apply(*DuplicateSubject)
	MarshalBinary() ([]byte, error)
}

// ProjectionDupeService checks one exact tracker projection set and returns
// private retained evidence separately from its safe public summary.
type ProjectionDupeService interface {
	CheckProjectionSet(
		context.Context,
		DuplicateSubject,
		TrackerReleaseProjectionSet,
		ProjectionDupeCheckOptions,
	) (DupeCheckSummary, DupeAssessmentEvidence, error)
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
	HDRFacts             HDRFacts
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
	// Projection is the exact safe tracker-local interpretation used for this search.
	Projection *TrackerReleaseProjection
}

// CanonicalSeasonEpisode returns the prepared TV season and episode.
func (s DuplicateSubject) CanonicalSeasonEpisode() (int, int) {
	return s.SeasonInt, s.EpisodeInt
}

// TrackerAuthService exposes the batch auth operations needed by shared upload
// preflight. Interactive auth changes remain outside release workflows.
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

// ExactMediaAssets is one authoritative workflow-owned media revision.
// Screenshots and DVD menus are independent channels, as are their hosted
// variants.
type ExactMediaAssets struct {
	Screenshots       []ScreenshotImage
	DVDMenus          []DVDMenuCaptureImage
	ScreenshotUploads []UploadedImageLink
	DVDMenuUploads    []UploadedImageLink
}

// Clone returns a detached exact-media bundle while preserving nil slices.
func (a *ExactMediaAssets) Clone() *ExactMediaAssets {
	if a == nil {
		return nil
	}
	return &ExactMediaAssets{
		Screenshots:       cloneOptionalSlice(a.Screenshots),
		DVDMenus:          cloneOptionalSlice(a.DVDMenus),
		ScreenshotUploads: cloneOptionalSlice(a.ScreenshotUploads),
		DVDMenuUploads:    cloneOptionalSlice(a.DVDMenuUploads),
	}
}

// Validate enforces the normal-screenshot and DVD-menu channel boundary.
func (a *ExactMediaAssets) Validate() error {
	if a == nil {
		return nil
	}
	screenshotPaths := make(map[string]struct{}, len(a.Screenshots))
	for _, image := range a.Screenshots {
		if image.Purpose != ScreenshotPurposeFinal {
			return fmt.Errorf("exact media screenshot has invalid purpose %q", image.Purpose)
		}
		if imagePath := strings.TrimSpace(image.Path); imagePath != "" {
			screenshotPaths[imagePath] = struct{}{}
		}
	}
	menuPaths := make(map[string]struct{}, len(a.DVDMenus))
	for _, menu := range a.DVDMenus {
		if menu.Purpose != ScreenshotPurposeMenu {
			return fmt.Errorf("exact media DVD menu has invalid purpose %q", menu.Purpose)
		}
		if imagePath := strings.TrimSpace(menu.Path); imagePath != "" {
			menuPaths[imagePath] = struct{}{}
		}
	}
	if err := validateExactMediaUploads("screenshot", a.ScreenshotUploads, screenshotPaths); err != nil {
		return err
	}
	return validateExactMediaUploads("DVD menu", a.DVDMenuUploads, menuPaths)
}

func validateExactMediaUploads(channel string, uploads []UploadedImageLink, allowedPaths map[string]struct{}) error {
	for _, upload := range uploads {
		imagePath := strings.TrimSpace(upload.ImagePath)
		if imagePath == "" {
			return fmt.Errorf("exact media %s upload path is required", channel)
		}
		if _, ok := allowedPaths[imagePath]; !ok {
			return fmt.Errorf("exact media %s upload does not match its source channel", channel)
		}
	}
	return nil
}

// TrackerSubject is the tracker module's operation-owned source, resource,
// instruction, and prerequisite view. It excludes preparation diagnostics,
// resolver evidence, cache freshness, and client-search implementation state.
type UploadSubject struct {
	SourcePath          string
	Paths               []string
	DiscType            string
	VideoPath           string
	FileList            []string
	SourceSize          int64
	MediaInfoJSONPath   string
	MediaInfoTextPath   string
	DVDVOBMediaInfoText string
	Scene               bool
	SceneName           string
	SceneNFOPath        string
	SceneRenamed        bool
	SceneRenamedReason  string
	DescriptionGroups   []DescriptionBuilderGroup
	// DescriptionGroupsFinal distinguishes retained description output from
	// pre-description subjects whose content may still be generated.
	DescriptionGroupsFinal      bool
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
	NamePresentation            ReleaseNamePresentation
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
	HDRFacts                    HDRFacts
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
	// GeneratedReleaseNames contains canonical structural alternatives. Empty
	// variants mean ReleaseName must remain exact.
	GeneratedReleaseNames GeneratedReleaseNameVariants
	BlockedTrackers       map[string][]TrackerBlockReason
	TrackerRuleFailures   map[string][]RuleFailure
	// RehashedTrackers identifies uploads whose selected base torrent required
	// regeneration so tracker execution can queue them after reusable artifacts.
	RehashedTrackers []string
	// ExactMedia, when non-nil, constrains description/image preparation to the
	// retained workflow-owned revision instead of repository discovery.
	ExactMedia *ExactMediaAssets
}

// RuleSubject contains only stable facts used by generic and tracker-specific
// eligibility rules.
type RuleSubject struct {
	SourcePath           string
	VideoPath            string
	FileList             []string
	DiscType             string
	Scene                bool
	SceneNFOPath         string
	SceneRenamed         bool
	SceneRenamedReason   string
	PersonalRelease      bool
	Release              ReleaseInfo
	ReleaseName          string
	ReleaseNameNoTag     string
	Tag                  string
	Identity             ExternalIdentity
	ProviderMetadata     SourceScopedMetadata
	AudioLanguages       []string
	SubtitleLanguages    []string
	TVPack               bool
	Type                 string
	Source               string
	Container            string
	BitDepth             string
	VideoCodec           string
	VideoEncode          string
	HDR                  string
	Region               string
	WebDV                bool
	Anime                bool
	Assessments          ReleaseAssessments
	DescriptionOverride  string
	Disc                 DiscFacts
	MediaInfoJSONReady   bool
	MediaInfoTextReady   bool
	DVDVOBMediaInfoReady bool
}

// PackageFileKind classifies a known package entry without applying
// tracker-specific policy.
type PackageFileKind string

const (
	PackageFileKindMedia            PackageFileKind = "media"
	PackageFileKindArchive          PackageFileKind = "archive"
	PackageFileKindExternalSubtitle PackageFileKind = "external_subtitle"
	PackageFileKindSample           PackageFileKind = "sample"
	PackageFileKindProof            PackageFileKind = "proof"
	PackageFileKindNFO              PackageFileKind = "nfo"
	PackageFileKindChecksum         PackageFileKind = "checksum"
	PackageFileKindImage            PackageFileKind = "image"
	PackageFileKindText             PackageFileKind = "text"
	PackageFileKindExecutable       PackageFileKind = "executable"
	PackageFileKindOther            PackageFileKind = "other"
)

// SeasonEpisodeFacts records locally detected episode numbers for one season.
type SeasonEpisodeFacts struct {
	Season   int
	Episodes []int
}

// PackageFacts contains source-layout facts derived without filesystem I/O.
// Status remains partial until the caller supplies a complete all-file list.
type PackageFacts struct {
	Status                    MetadataEvidenceStatus
	KnownFileCount            int
	MediaFileCount            int
	ArchiveFileCount          int
	ExternalSubtitleFileCount int
	ExternalFileCount         int
	NestedFileCount           int
	Extensions                []string
	DetectedSeasons           []int
	DetectedEpisodes          []SeasonEpisodeFacts
	ExtraKinds                []PackageFileKind
	SingleFileFolder          bool
}

// MediaFileFact contains normalized technical and language facts for one
// media file. Empty values and zero track counts mean unknown.
type MediaFileFact struct {
	FileName          string
	Primary           bool
	Container         string
	Source            string
	Resolution        string
	VideoCodec        string
	VideoEncode       string
	BitDepth          string
	VideoTrackCount   int
	AudioLanguages    []string
	SubtitleLanguages []string
}

// MediaFileFacts contains all media facts available to shared validation.
// TechnicalStatus and LanguageStatus describe their respective projections.
type MediaFileFacts struct {
	Status            MetadataEvidenceStatus
	TechnicalStatus   MetadataEvidenceStatus
	LanguageStatus    MetadataEvidenceStatus
	ExpectedFileCount int
	OriginalLanguage  string
	Files             []MediaFileFact
}

// AssetEvidence records exact readiness for one prepared asset channel.
type AssetEvidence struct {
	Status MetadataEvidenceStatus
	Ready  bool
	Count  int
}

// AssetFacts contains prepared-resource readiness without exposing local paths.
type AssetFacts struct {
	Status            MetadataEvidenceStatus
	MediaInfoJSON     AssetEvidence
	MediaInfoText     AssetEvidence
	DVDVOBMediaInfo   AssetEvidence
	BDInfo            AssetEvidence
	NFO               AssetEvidence
	Screenshots       AssetEvidence
	HostedScreenshots AssetEvidence
	DVDMenus          AssetEvidence
	HostedDVDMenus    AssetEvidence
}

// AvailabilityFacts contains provider-entry evidence available to validation.
// The aggregate status remains partial because the current contract does not
// declare the complete provider lookup scope.
type AvailabilityFacts struct {
	// Status describes the completeness of Providers as a set, not the result
	// of any one provider lookup.
	Status    MetadataEvidenceStatus
	Providers []ProviderAvailabilityEvidence
}

// ProvenanceFacts records source/generation authority for identity and provider
// metadata consumed by validation.
type ProvenanceFacts struct {
	Status             MetadataEvidenceStatus
	IdentitySourcePath string
	IdentityGeneration PreparedGeneration
	MetadataSourcePath string
	MetadataGeneration PreparedGeneration
	Identity           IdentityProvenanceSet
}

// TrackerValidationSubject contains only immutable canonical facts, projected
// tracker answers, and prepared-resource readiness used by side-effect-free
// pre-duplicate validation.
type TrackerValidationSubject struct {
	Tracker                string
	SourcePath             string
	VideoPath              string
	FileList               []string
	SourceSize             int64
	DiscType               string
	Scene                  bool
	SceneNFOReady          bool
	SceneRenamed           bool
	SceneRenamedReason     string
	PersonalRelease        bool
	Release                ReleaseInfo
	ReleaseName            string
	ReleaseNameNoTag       string
	Tag                    string
	Identity               ExternalIdentity
	ProviderMetadata       SourceScopedMetadata
	AudioLanguages         []string
	SubtitleLanguages      []string
	SeasonInt              int
	EpisodeInt             int
	SeasonStr              string
	EpisodeStr             string
	TVPack                 bool
	DailyEpisodeDate       string
	Anime                  bool
	EpisodeTitle           string
	EpisodeOverview        string
	Disc                   DiscFacts
	Type                   string
	Source                 string
	Container              string
	Audio                  string
	Channels               string
	HasCommentary          bool
	Is3D                   string
	BitDepth               string
	VideoCodec             string
	VideoEncode            string
	HasEncodeSettings      bool
	HDR                    string
	UHD                    string
	Distributor            string
	Region                 string
	Edition                string
	Repack                 string
	WebDV                  bool
	Service                string
	ServiceLongName        string
	StreamOptimized        int
	Assessments            ReleaseAssessments
	QuestionnaireAnswers   map[string]string
	TrackerConfigOverrides TrackerConfigOverrides
	TrackerSiteOverrides   TrackerSiteOverrides
	ReleaseNameOverrides   ReleaseNameOverrides
	// DescriptionOverride is the description selected for Tracker.
	DescriptionOverride string
	// DescriptionGroupsFinal distinguishes pending description work from final
	// evidence that required manual content is absent.
	DescriptionGroupsFinal      bool
	MediaInfoJSONReady          bool
	MediaInfoTextReady          bool
	DVDVOBMediaInfoReady        bool
	BDInfoReady                 bool
	PreparedResourceFingerprint string
	PackageFacts                PackageFacts
	MediaFileFacts              MediaFileFacts
	AssetFacts                  AssetFacts
	AvailabilityFacts           AvailabilityFacts
	ProvenanceFacts             ProvenanceFacts
}

// NewTrackerValidationSubject projects an upload subject into the detached
// pre-duplicate validation contract for one tracker.
func NewTrackerValidationSubject(subject UploadSubject, tracker string) TrackerValidationSubject {
	tracker = strings.ToUpper(strings.TrimSpace(tracker))
	answers := make(map[string]string)
	for configuredTracker, values := range subject.TrackerQuestionnaireAnswers {
		if !strings.EqualFold(strings.TrimSpace(configuredTracker), tracker) {
			continue
		}
		maps.Copy(answers, values)
		break
	}
	descriptionOverride := trackerDescriptionOverride(subject, tracker)
	resourceFingerprint, _ := CanonicalWorkflowFingerprint(struct {
		MediaInfoJSON   bool
		MediaInfoText   bool
		DVDVOBMediaInfo bool
		BDInfo          bool
		SceneNFO        bool
	}{
		MediaInfoJSON:   strings.TrimSpace(subject.MediaInfoJSONPath) != "",
		MediaInfoText:   strings.TrimSpace(subject.MediaInfoTextPath) != "",
		DVDVOBMediaInfo: strings.TrimSpace(subject.DVDVOBMediaInfoText) != "",
		BDInfo:          strings.TrimSpace(subject.Disc.Summary) != "",
		SceneNFO:        strings.TrimSpace(subject.SceneNFOPath) != "",
	})
	packageFacts := deriveValidationPackageFacts(subject.SourcePath, subject.FileList)
	mediaFacts := deriveValidationMediaFileFacts(subject, packageFacts.MediaFileCount)
	assetFacts := deriveValidationAssetFacts(subject)
	availabilityFacts := deriveValidationAvailabilityFacts(subject.ProviderMetadata)
	provenanceFacts := deriveValidationProvenanceFacts(subject.Identity, subject.ProviderMetadata)
	return TrackerValidationSubject{
		Tracker:                     tracker,
		SourcePath:                  subject.SourcePath,
		VideoPath:                   subject.VideoPath,
		FileList:                    slices.Clone(subject.FileList),
		SourceSize:                  subject.SourceSize,
		DiscType:                    subject.DiscType,
		Scene:                       subject.Scene,
		SceneNFOReady:               strings.TrimSpace(subject.SceneNFOPath) != "",
		SceneRenamed:                subject.SceneRenamed,
		SceneRenamedReason:          subject.SceneRenamedReason,
		PersonalRelease:             subject.PersonalRelease,
		Release:                     cloneTrackerValidationValue(subject.Release),
		ReleaseName:                 subject.ReleaseName,
		ReleaseNameNoTag:            subject.ReleaseNameNoTag,
		Tag:                         subject.Tag,
		Identity:                    cloneTrackerValidationValue(subject.Identity),
		ProviderMetadata:            cloneTrackerValidationValue(subject.ProviderMetadata),
		AudioLanguages:              slices.Clone(subject.AudioLanguages),
		SubtitleLanguages:           slices.Clone(subject.SubtitleLanguages),
		SeasonInt:                   subject.SeasonInt,
		EpisodeInt:                  subject.EpisodeInt,
		SeasonStr:                   subject.SeasonStr,
		EpisodeStr:                  subject.EpisodeStr,
		TVPack:                      subject.TVPack,
		DailyEpisodeDate:            subject.DailyEpisodeDate,
		Anime:                       subject.Anime,
		EpisodeTitle:                subject.EpisodeTitle,
		EpisodeOverview:             subject.EpisodeOverview,
		Disc:                        cloneTrackerValidationValue(subject.Disc),
		Type:                        subject.Type,
		Source:                      subject.Source,
		Container:                   subject.Container,
		Audio:                       subject.Audio,
		Channels:                    subject.Channels,
		HasCommentary:               subject.HasCommentary,
		Is3D:                        subject.Is3D,
		BitDepth:                    subject.BitDepth,
		VideoCodec:                  subject.VideoCodec,
		VideoEncode:                 subject.VideoEncode,
		HasEncodeSettings:           subject.HasEncodeSettings,
		HDR:                         subject.HDR,
		UHD:                         subject.UHD,
		Distributor:                 subject.Distributor,
		Region:                      subject.Region,
		Edition:                     subject.Edition,
		Repack:                      subject.Repack,
		WebDV:                       subject.WebDV,
		Service:                     subject.Service,
		ServiceLongName:             subject.ServiceLongName,
		StreamOptimized:             subject.StreamOptimized,
		Assessments:                 cloneTrackerValidationValue(subject.Assessments),
		QuestionnaireAnswers:        answers,
		TrackerConfigOverrides:      cloneTrackerValidationValue(subject.TrackerConfigOverrides),
		TrackerSiteOverrides:        cloneTrackerValidationValue(subject.TrackerSiteOverrides),
		ReleaseNameOverrides:        cloneTrackerValidationValue(subject.ReleaseNameOverrides),
		DescriptionOverride:         descriptionOverride,
		DescriptionGroupsFinal:      subject.DescriptionGroupsFinal,
		MediaInfoJSONReady:          strings.TrimSpace(subject.MediaInfoJSONPath) != "",
		MediaInfoTextReady:          strings.TrimSpace(subject.MediaInfoTextPath) != "",
		DVDVOBMediaInfoReady:        strings.TrimSpace(subject.DVDVOBMediaInfoText) != "",
		BDInfoReady:                 strings.TrimSpace(subject.Disc.Summary) != "",
		PreparedResourceFingerprint: string(resourceFingerprint),
		PackageFacts:                packageFacts,
		MediaFileFacts:              mediaFacts,
		AssetFacts:                  assetFacts,
		AvailabilityFacts:           availabilityFacts,
		ProvenanceFacts:             provenanceFacts,
	}
}

// trackerDescriptionOverride prefers explicit direct content, then selects the
// first non-empty prepared description assigned to tracker.
func trackerDescriptionOverride(subject UploadSubject, tracker string) string {
	if description := strings.TrimSpace(subject.DescriptionOverride); description != "" {
		return description
	}
	for _, group := range subject.DescriptionGroups {
		matchesTracker := false
		for _, candidate := range group.Trackers {
			if strings.EqualFold(strings.TrimSpace(candidate), tracker) {
				matchesTracker = true
				break
			}
		}
		if !matchesTracker {
			continue
		}
		if description := strings.TrimSpace(group.Description); description != "" {
			return description
		}
		if description := strings.TrimSpace(group.RawDescription); description != "" {
			return description
		}
	}
	return ""
}

func cloneTrackerValidationValue[T any](value T) T {
	cloned, err := clonePreparedValue(value)
	if err != nil {
		return value
	}
	return cloned
}

var (
	validationSeasonPattern  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])S(\d{1,3})`)
	validationEpisodePattern = regexp.MustCompile(`(?i)E(\d{1,4})`)
	validationArchivePart    = regexp.MustCompile(`(?i)^\.r\d{2,3}$`)
)

var validationMediaExtensions = map[string]struct{}{
	".3gp":  {},
	".avi":  {},
	".flv":  {},
	".m2ts": {},
	".m2v":  {},
	".m4v":  {},
	".mkv":  {},
	".mov":  {},
	".mp4":  {},
	".mpeg": {},
	".mpg":  {},
	".mts":  {},
	".ts":   {},
	".vob":  {},
	".webm": {},
	".wmv":  {},
}

var validationArchiveExtensions = map[string]struct{}{
	".001": {},
	".7z":  {},
	".bz2": {},
	".gz":  {},
	".rar": {},
	".tar": {},
	".tbz": {},
	".tgz": {},
	".txz": {},
	".xz":  {},
	".zip": {},
}

var validationSubtitleExtensions = map[string]struct{}{
	".ass": {},
	".idx": {},
	".smi": {},
	".srt": {},
	".ssa": {},
	".sub": {},
	".sup": {},
	".vtt": {},
}

func deriveValidationPackageFacts(sourcePath string, fileList []string) PackageFacts {
	facts := PackageFacts{Status: MetadataEvidenceStatusUnavailable}
	extensions := make(map[string]struct{})
	extraKinds := make(map[PackageFileKind]struct{})
	detectedEpisodes := make(map[int]map[int]struct{})
	seenFiles := make(map[string]struct{})
	sourcePath = strings.TrimSpace(sourcePath)
	folderCandidate := false

	for _, rawFile := range fileList {
		fileName := strings.TrimSpace(rawFile)
		if fileName == "" {
			continue
		}
		cleanFileName := filepath.Clean(fileName)
		if _, seen := seenFiles[cleanFileName]; seen {
			continue
		}
		seenFiles[cleanFileName] = struct{}{}
		facts.KnownFileCount++

		extension := strings.ToLower(filepath.Ext(cleanFileName))
		if extension != "" {
			extensions[extension] = struct{}{}
		}
		isMedia := validationPackageFileIsMedia(extension)
		if isMedia {
			facts.MediaFileCount++
		}
		kind := validationPackageFileKind(cleanFileName, extension, isMedia)
		switch kind {
		case PackageFileKindArchive:
			facts.ArchiveFileCount++
		case PackageFileKindExternalSubtitle:
			facts.ExternalSubtitleFileCount++
		case PackageFileKindMedia:
		case PackageFileKindSample, PackageFileKindProof, PackageFileKindNFO, PackageFileKindChecksum,
			PackageFileKindImage, PackageFileKindText, PackageFileKindExecutable, PackageFileKindOther:
			extraKinds[kind] = struct{}{}
		}

		nested, external, insideFolder := validationPackagePathFacts(sourcePath, cleanFileName)
		if nested {
			facts.NestedFileCount++
		}
		if external {
			facts.ExternalFileCount++
		}
		folderCandidate = folderCandidate || insideFolder
		collectValidationSeasonEpisodes(filepath.Base(cleanFileName), detectedEpisodes)
	}

	if facts.KnownFileCount == 0 {
		return facts
	}
	facts.Status = MetadataEvidenceStatusPartial
	facts.SingleFileFolder = facts.KnownFileCount == 1 && folderCandidate
	facts.Extensions = make([]string, 0, len(extensions))
	for extension := range extensions {
		facts.Extensions = append(facts.Extensions, extension)
	}
	slices.Sort(facts.Extensions)
	facts.ExtraKinds = make([]PackageFileKind, 0, len(extraKinds))
	for kind := range extraKinds {
		facts.ExtraKinds = append(facts.ExtraKinds, kind)
	}
	slices.Sort(facts.ExtraKinds)

	seasons := make([]int, 0, len(detectedEpisodes))
	for season := range detectedEpisodes {
		seasons = append(seasons, season)
	}
	slices.Sort(seasons)
	facts.DetectedSeasons = slices.Clone(seasons)
	facts.DetectedEpisodes = make([]SeasonEpisodeFacts, 0, len(seasons))
	for _, season := range seasons {
		episodeSet := detectedEpisodes[season]
		episodes := make([]int, 0, len(episodeSet))
		for episode := range episodeSet {
			episodes = append(episodes, episode)
		}
		slices.Sort(episodes)
		facts.DetectedEpisodes = append(facts.DetectedEpisodes, SeasonEpisodeFacts{
			Season:   season,
			Episodes: episodes,
		})
	}
	return facts
}

func validationPackageFileIsMedia(extension string) bool {
	_, ok := validationMediaExtensions[extension]
	return ok
}

func validationPackageFileKind(fileName string, extension string, media bool) PackageFileKind {
	base := strings.ToLower(filepath.Base(fileName))
	clean := strings.ToLower(filepath.Clean(fileName))
	switch {
	case validationArchivePart.MatchString(extension):
		return PackageFileKindArchive
	case hasValidationExtension(validationArchiveExtensions, extension):
		return PackageFileKindArchive
	case hasValidationExtension(validationSubtitleExtensions, extension):
		return PackageFileKindExternalSubtitle
	case strings.Contains(base, "sample"):
		return PackageFileKindSample
	case strings.Contains(clean, string(filepath.Separator)+"proof"+string(filepath.Separator)) || strings.Contains(base, "proof"):
		return PackageFileKindProof
	case extension == ".nfo":
		return PackageFileKindNFO
	case extension == ".sfv" || extension == ".md5" || extension == ".sha1" || extension == ".sha256":
		return PackageFileKindChecksum
	case extension == ".bmp" || extension == ".gif" || extension == ".jpeg" || extension == ".jpg" ||
		extension == ".png" || extension == ".webp":
		return PackageFileKindImage
	case extension == ".txt":
		return PackageFileKindText
	case extension == ".bat" || extension == ".cmd" || extension == ".com" || extension == ".exe" ||
		extension == ".msi" || extension == ".ps1" || extension == ".scr":
		return PackageFileKindExecutable
	case media:
		return PackageFileKindMedia
	default:
		return PackageFileKindOther
	}
}

func hasValidationExtension(values map[string]struct{}, extension string) bool {
	_, ok := values[extension]
	return ok
}

func validationPackagePathFacts(sourcePath string, fileName string) (nested bool, external bool, insideFolder bool) {
	if sourcePath == "" {
		return filepath.Dir(fileName) != ".", false, false
	}
	cleanSource := filepath.Clean(sourcePath)
	if validationLocalPathsEqual(cleanSource, fileName) {
		return false, false, false
	}
	relative, err := filepath.Rel(cleanSource, fileName)
	if err != nil {
		return false, true, false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false, true, false
	}
	return filepath.Dir(relative) != ".", false, relative != "."
}

func validationLocalPathsEqual(left string, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func collectValidationSeasonEpisodes(fileName string, detected map[int]map[int]struct{}) {
	seasonMatch := validationSeasonPattern.FindStringSubmatch(fileName)
	if len(seasonMatch) < 2 {
		return
	}
	season, err := strconv.Atoi(seasonMatch[1])
	if err != nil || season < 0 {
		return
	}
	if detected[season] == nil {
		detected[season] = make(map[int]struct{})
	}
	for _, episodeMatch := range validationEpisodePattern.FindAllStringSubmatch(fileName, -1) {
		if len(episodeMatch) < 2 {
			continue
		}
		episode, parseErr := strconv.Atoi(episodeMatch[1])
		if parseErr == nil && episode >= 0 {
			detected[season][episode] = struct{}{}
		}
	}
}

func deriveValidationMediaFileFacts(subject UploadSubject, expectedFileCount int) MediaFileFacts {
	return buildValidationMediaFileFacts(
		subject.VideoPath,
		subject.FileList,
		expectedFileCount,
		subject.Container,
		subject.Source,
		subject.Release.Resolution,
		subject.VideoCodec,
		subject.VideoEncode,
		subject.BitDepth,
		subject.AudioLanguages,
		subject.SubtitleLanguages,
		validationOriginalLanguage(subject.ProviderMetadata),
	)
}

func deriveValidationRuleMediaFileFacts(subject RuleSubject, expectedFileCount int) MediaFileFacts {
	return buildValidationMediaFileFacts(
		subject.VideoPath,
		subject.FileList,
		expectedFileCount,
		subject.Container,
		subject.Source,
		subject.Release.Resolution,
		subject.VideoCodec,
		subject.VideoEncode,
		subject.BitDepth,
		subject.AudioLanguages,
		subject.SubtitleLanguages,
		validationOriginalLanguage(subject.ProviderMetadata),
	)
}

func buildValidationMediaFileFacts(
	videoPath string,
	fileList []string,
	expectedFileCount int,
	container string,
	source string,
	resolution string,
	videoCodec string,
	videoEncode string,
	bitDepth string,
	audioLanguages []string,
	subtitleLanguages []string,
	originalLanguage string,
) MediaFileFacts {
	facts := MediaFileFacts{
		Status:            MetadataEvidenceStatusUnavailable,
		TechnicalStatus:   MetadataEvidenceStatusUnavailable,
		LanguageStatus:    MetadataEvidenceStatusUnavailable,
		ExpectedFileCount: expectedFileCount,
		OriginalLanguage:  strings.TrimSpace(originalLanguage),
	}
	primaryPath := strings.TrimSpace(videoPath)
	if primaryPath == "" && len(fileList) > 0 {
		primaryPath = strings.TrimSpace(fileList[0])
	}
	technicalKnown := primaryPath != "" || strings.TrimSpace(container) != "" || strings.TrimSpace(source) != "" ||
		strings.TrimSpace(resolution) != "" || strings.TrimSpace(videoCodec) != "" || strings.TrimSpace(videoEncode) != "" ||
		strings.TrimSpace(bitDepth) != ""
	languageKnown := len(audioLanguages) > 0 || len(subtitleLanguages) > 0 || facts.OriginalLanguage != ""
	if !technicalKnown && !languageKnown {
		return facts
	}
	fileName := ""
	if primaryPath != "" {
		fileName = filepath.Base(filepath.Clean(primaryPath))
	}
	facts.Files = []MediaFileFact{{
		FileName:          fileName,
		Primary:           true,
		Container:         strings.TrimSpace(container),
		Source:            strings.TrimSpace(source),
		Resolution:        strings.TrimSpace(resolution),
		VideoCodec:        strings.TrimSpace(videoCodec),
		VideoEncode:       strings.TrimSpace(videoEncode),
		BitDepth:          strings.TrimSpace(bitDepth),
		AudioLanguages:    slices.Clone(audioLanguages),
		SubtitleLanguages: slices.Clone(subtitleLanguages),
	}}
	facts.Status = MetadataEvidenceStatusPartial
	if technicalKnown {
		facts.TechnicalStatus = MetadataEvidenceStatusPartial
	}
	if languageKnown {
		facts.LanguageStatus = MetadataEvidenceStatusPartial
	}
	return facts
}

func validationOriginalLanguage(metadata SourceScopedMetadata) string {
	switch {
	case metadata.TMDB != nil && strings.TrimSpace(metadata.TMDB.OriginalLanguage) != "":
		return strings.TrimSpace(metadata.TMDB.OriginalLanguage)
	case metadata.TVDB != nil && strings.TrimSpace(metadata.TVDB.OriginalLanguage) != "":
		return strings.TrimSpace(metadata.TVDB.OriginalLanguage)
	case metadata.IMDB != nil && strings.TrimSpace(metadata.IMDB.OriginalLanguage) != "":
		return strings.TrimSpace(metadata.IMDB.OriginalLanguage)
	case metadata.TVmaze != nil && strings.TrimSpace(metadata.TVmaze.Language) != "":
		return strings.TrimSpace(metadata.TVmaze.Language)
	default:
		return ""
	}
}

func deriveValidationAssetFacts(subject UploadSubject) AssetFacts {
	mediaInfoJSONReady := strings.TrimSpace(subject.MediaInfoJSONPath) != ""
	mediaInfoTextReady := strings.TrimSpace(subject.MediaInfoTextPath) != ""
	dvdVOBMediaInfoReady := strings.TrimSpace(subject.DVDVOBMediaInfoText) != ""
	facts := AssetFacts{
		Status:            MetadataEvidenceStatusPartial,
		MediaInfoJSON:     completeAssetEvidence(mediaInfoJSONReady, boolCount(mediaInfoJSONReady)),
		MediaInfoText:     completeAssetEvidence(mediaInfoTextReady, boolCount(mediaInfoTextReady)),
		DVDVOBMediaInfo:   completeAssetEvidence(dvdVOBMediaInfoReady, boolCount(dvdVOBMediaInfoReady)),
		BDInfo:            completeAssetEvidence(strings.TrimSpace(subject.Disc.Summary) != "", boolCount(strings.TrimSpace(subject.Disc.Summary) != "")),
		NFO:               completeAssetEvidence(strings.TrimSpace(subject.SceneNFOPath) != "", boolCount(strings.TrimSpace(subject.SceneNFOPath) != "")),
		Screenshots:       unavailableAssetEvidence(),
		HostedScreenshots: unavailableAssetEvidence(),
		DVDMenus:          unavailableAssetEvidence(),
		HostedDVDMenus:    unavailableAssetEvidence(),
	}
	if subject.ExactMedia == nil {
		return facts
	}
	screenshotCount := countValidationScreenshots(subject.ExactMedia.Screenshots, ScreenshotPurposeFinal)
	menuCount := countValidationDVDMenus(subject.ExactMedia.DVDMenus)
	hostedScreenshotCount := countValidationImageLinks(subject.ExactMedia.ScreenshotUploads)
	hostedMenuCount := countValidationImageLinks(subject.ExactMedia.DVDMenuUploads)
	facts.Status = MetadataEvidenceStatusComplete
	facts.Screenshots = completeAssetEvidence(screenshotCount > 0, screenshotCount)
	facts.HostedScreenshots = completeAssetEvidence(hostedScreenshotCount > 0, hostedScreenshotCount)
	facts.DVDMenus = completeAssetEvidence(menuCount > 0, menuCount)
	facts.HostedDVDMenus = completeAssetEvidence(hostedMenuCount > 0, hostedMenuCount)
	return facts
}

func deriveValidationRuleAssetFacts(subject RuleSubject) AssetFacts {
	return AssetFacts{
		Status:            MetadataEvidenceStatusPartial,
		MediaInfoJSON:     completeAssetEvidence(subject.MediaInfoJSONReady, boolCount(subject.MediaInfoJSONReady)),
		MediaInfoText:     completeAssetEvidence(subject.MediaInfoTextReady, boolCount(subject.MediaInfoTextReady)),
		DVDVOBMediaInfo:   completeAssetEvidence(subject.DVDVOBMediaInfoReady, boolCount(subject.DVDVOBMediaInfoReady)),
		BDInfo:            completeAssetEvidence(strings.TrimSpace(subject.Disc.Summary) != "", boolCount(strings.TrimSpace(subject.Disc.Summary) != "")),
		NFO:               completeAssetEvidence(strings.TrimSpace(subject.SceneNFOPath) != "", boolCount(strings.TrimSpace(subject.SceneNFOPath) != "")),
		Screenshots:       unavailableAssetEvidence(),
		HostedScreenshots: unavailableAssetEvidence(),
		DVDMenus:          unavailableAssetEvidence(),
		HostedDVDMenus:    unavailableAssetEvidence(),
	}
}

func completeAssetEvidence(ready bool, count int) AssetEvidence {
	return AssetEvidence{
		Status: MetadataEvidenceStatusComplete,
		Ready:  ready,
		Count:  count,
	}
}

func unavailableAssetEvidence() AssetEvidence {
	return AssetEvidence{Status: MetadataEvidenceStatusUnavailable}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func countValidationScreenshots(images []ScreenshotImage, purpose ScreenshotPurpose) int {
	count := 0
	for _, image := range images {
		if image.Purpose == purpose && strings.TrimSpace(image.Path) != "" {
			count++
		}
	}
	return count
}

func countValidationDVDMenus(images []DVDMenuCaptureImage) int {
	count := 0
	for _, image := range images {
		if image.Purpose == ScreenshotPurposeMenu && strings.TrimSpace(image.Path) != "" {
			count++
		}
	}
	return count
}

func countValidationImageLinks(links []UploadedImageLink) int {
	count := 0
	for _, link := range links {
		if strings.TrimSpace(link.ImagePath) != "" &&
			(strings.TrimSpace(link.ImgURL) != "" || strings.TrimSpace(link.RawURL) != "" || strings.TrimSpace(link.WebURL) != "") {
			count++
		}
	}
	return count
}

func deriveValidationAvailabilityFacts(metadata SourceScopedMetadata) AvailabilityFacts {
	facts := AvailabilityFacts{
		Status:    MetadataEvidenceStatusUnavailable,
		Providers: slices.Clone(metadata.ProviderAvailability),
	}
	if len(facts.Providers) > 0 {
		facts.Status = MetadataEvidenceStatusPartial
	}
	return facts
}

func deriveValidationProvenanceFacts(identity ExternalIdentity, metadata SourceScopedMetadata) ProvenanceFacts {
	facts := ProvenanceFacts{
		Status:             MetadataEvidenceStatusUnavailable,
		IdentitySourcePath: strings.TrimSpace(identity.SourcePath),
		IdentityGeneration: identity.Generation,
		MetadataSourcePath: strings.TrimSpace(metadata.SourcePath),
		MetadataGeneration: metadata.Generation,
		Identity:           identity.Provenance,
	}
	identityKnown := facts.IdentitySourcePath != "" || facts.IdentityGeneration > 0 || validationIdentityProvenanceKnown(facts.Identity)
	metadataKnown := facts.MetadataSourcePath != "" || facts.MetadataGeneration > 0
	if !identityKnown && !metadataKnown {
		return facts
	}
	facts.Status = MetadataEvidenceStatusPartial
	if !identityKnown || !metadataKnown {
		return facts
	}
	if facts.IdentitySourcePath != "" && facts.MetadataSourcePath != "" &&
		!validationLocalPathsEqual(facts.IdentitySourcePath, facts.MetadataSourcePath) {
		facts.Status = MetadataEvidenceStatusContradictory
		return facts
	}
	if facts.IdentityGeneration > 0 && facts.MetadataGeneration > 0 &&
		facts.IdentityGeneration != facts.MetadataGeneration {
		facts.Status = MetadataEvidenceStatusContradictory
		return facts
	}
	if facts.IdentitySourcePath != "" && facts.MetadataSourcePath != "" &&
		facts.IdentityGeneration > 0 && facts.MetadataGeneration > 0 {
		facts.Status = MetadataEvidenceStatusComplete
	}
	return facts
}

func validationIdentityProvenanceKnown(provenance IdentityProvenanceSet) bool {
	return validationIdentityProvenanceValueKnown(provenance.TMDB) ||
		validationIdentityProvenanceValueKnown(provenance.IMDB) ||
		validationIdentityProvenanceValueKnown(provenance.TVDB) ||
		validationIdentityProvenanceValueKnown(provenance.TVmaze) ||
		validationIdentityProvenanceValueKnown(provenance.MAL) ||
		validationIdentityProvenanceValueKnown(provenance.Category)
}

func validationIdentityProvenanceValueKnown(provenance IdentityProvenance) bool {
	return provenance != "" && provenance != IdentityProvenanceUnknown
}

// NewTrackerValidationSubjectFromRuleSubject preserves the legacy generic-rule
// entry point while routing custom checks through the validation contract.
func NewTrackerValidationSubjectFromRuleSubject(subject RuleSubject, tracker string) TrackerValidationSubject {
	packageFacts := deriveValidationPackageFacts(subject.SourcePath, subject.FileList)
	mediaFacts := deriveValidationRuleMediaFileFacts(subject, packageFacts.MediaFileCount)
	return TrackerValidationSubject{
		Tracker:              strings.ToUpper(strings.TrimSpace(tracker)),
		SourcePath:           subject.SourcePath,
		VideoPath:            subject.VideoPath,
		FileList:             slices.Clone(subject.FileList),
		DiscType:             subject.DiscType,
		Scene:                subject.Scene,
		SceneNFOReady:        strings.TrimSpace(subject.SceneNFOPath) != "",
		SceneRenamed:         subject.SceneRenamed,
		SceneRenamedReason:   subject.SceneRenamedReason,
		PersonalRelease:      subject.PersonalRelease,
		Release:              cloneTrackerValidationValue(subject.Release),
		ReleaseName:          subject.ReleaseName,
		ReleaseNameNoTag:     subject.ReleaseNameNoTag,
		Tag:                  subject.Tag,
		Identity:             cloneTrackerValidationValue(subject.Identity),
		ProviderMetadata:     cloneTrackerValidationValue(subject.ProviderMetadata),
		AudioLanguages:       slices.Clone(subject.AudioLanguages),
		SubtitleLanguages:    slices.Clone(subject.SubtitleLanguages),
		TVPack:               subject.TVPack,
		Type:                 subject.Type,
		Source:               subject.Source,
		Container:            subject.Container,
		BitDepth:             subject.BitDepth,
		VideoCodec:           subject.VideoCodec,
		VideoEncode:          subject.VideoEncode,
		HDR:                  subject.HDR,
		Region:               subject.Region,
		WebDV:                subject.WebDV,
		Anime:                subject.Anime,
		Assessments:          cloneTrackerValidationValue(subject.Assessments),
		DescriptionOverride:  subject.DescriptionOverride,
		Disc:                 cloneTrackerValidationValue(subject.Disc),
		MediaInfoJSONReady:   subject.MediaInfoJSONReady,
		MediaInfoTextReady:   subject.MediaInfoTextReady,
		DVDVOBMediaInfoReady: subject.DVDVOBMediaInfoReady,
		BDInfoReady:          strings.TrimSpace(subject.Disc.Summary) != "",
		PackageFacts:         packageFacts,
		MediaFileFacts:       mediaFacts,
		AssetFacts:           deriveValidationRuleAssetFacts(subject),
		AvailabilityFacts:    deriveValidationAvailabilityFacts(subject.ProviderMetadata),
		ProvenanceFacts:      deriveValidationProvenanceFacts(subject.Identity, subject.ProviderMetadata),
	}
}

// NewRuleSubject projects upload facts into the rule evaluator's read model.
func NewRuleSubject(subject UploadSubject) RuleSubject {
	return RuleSubject{
		SourcePath:           subject.SourcePath,
		VideoPath:            subject.VideoPath,
		FileList:             append([]string(nil), subject.FileList...),
		DiscType:             subject.DiscType,
		Scene:                subject.Scene,
		SceneNFOPath:         subject.SceneNFOPath,
		SceneRenamed:         subject.SceneRenamed,
		SceneRenamedReason:   subject.SceneRenamedReason,
		PersonalRelease:      subject.PersonalRelease,
		Release:              subject.Release,
		ReleaseName:          subject.ReleaseName,
		ReleaseNameNoTag:     subject.ReleaseNameNoTag,
		Tag:                  subject.Tag,
		Identity:             subject.Identity,
		ProviderMetadata:     subject.ProviderMetadata,
		AudioLanguages:       append([]string(nil), subject.AudioLanguages...),
		SubtitleLanguages:    append([]string(nil), subject.SubtitleLanguages...),
		TVPack:               subject.TVPack,
		Type:                 subject.Type,
		Source:               subject.Source,
		Container:            subject.Container,
		BitDepth:             subject.BitDepth,
		VideoCodec:           subject.VideoCodec,
		VideoEncode:          subject.VideoEncode,
		HDR:                  subject.HDR,
		Region:               subject.Region,
		WebDV:                subject.WebDV,
		Anime:                subject.Anime,
		Assessments:          subject.Assessments,
		DescriptionOverride:  subject.DescriptionOverride,
		Disc:                 subject.Disc,
		MediaInfoJSONReady:   strings.TrimSpace(subject.MediaInfoJSONPath) != "",
		MediaInfoTextReady:   strings.TrimSpace(subject.MediaInfoTextPath) != "",
		DVDVOBMediaInfoReady: strings.TrimSpace(subject.DVDVOBMediaInfoText) != "",
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
	ExactMedia            *ExactMediaAssets
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
		ExactMedia:            subject.ExactMedia.Clone(),
	}
	cloned, err := clonePreparedValue(projected)
	if err != nil {
		panic(fmt.Sprintf("clone description subject: %v", err))
	}
	return cloned
}

func cloneOptionalSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
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
	// FailedHosts lists image hosts whose earlier attempt failed for this run.
	// Later stages must not reuse or retry them.
	FailedHosts []string
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
	// SkipIfRehashTrackers lists selected tracker names to omit when their
	// torrent policy would otherwise require regeneration. Names are case-insensitive.
	SkipIfRehashTrackers []string
	TorrentOverrides     TorrentOverrides
}

// ClientSubject contains prepared content source facts and caller instructions
// for torrent-client search, linking, and injection. [TorrentResult] paths and
// URLs identify torrent artifacts and do not replace these source facts.
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
	// EvidenceStatus describes the completeness of the facts supporting this
	// result; an empty value denotes a legacy result.
	EvidenceStatus MetadataEvidenceStatus `json:"evidenceStatus,omitempty"`
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

// CountBlockingRuleFailures returns the number of unresolved rule results that
// block tracker work. Authorized waivable results do not block.
func CountBlockingRuleFailures(failures []TrackerRuleFailure) int {
	count := 0
	for _, failure := range failures {
		disposition := NormalizeRuleDisposition(failure.Disposition)
		if disposition != RuleDispositionAdvisory && (disposition != RuleDispositionWaivable || !failure.Authorized) {
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

// TVDBNameDisambiguation records provider evidence used to decide whether a
// TV series name needs its TVDB year and country locale.
type TVDBNameDisambiguation struct {
	// CanonicalName is the selected English series name used for comparison.
	CanonicalName string
	// SeriesYear is the selected series year used for same-year comparison.
	SeriesYear int
	// Locale is the normalized country token emitted only when IncludeLocale
	// is true.
	Locale string
	// SameNameSeries counts distinct other TVDB IDs with the same normalized
	// English primary name or alias.
	SameNameSeries int
	// SameNameAndYearSeries counts SameNameSeries entries with a matching known
	// year.
	SameNameAndYearSeries int
	// IncludeYear and IncludeLocale are the prepared naming decisions consumed
	// by tracker policies without further provider I/O.
	IncludeYear   bool
	IncludeLocale bool
	// Status records whether the general-name search evidence is complete,
	// partial, unavailable, or contradictory.
	Status MetadataEvidenceStatus
	// Source identifies the versioned disambiguation algorithm.
	Source string
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
	NameDisambiguation     TVDBNameDisambiguation
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

// UploadSummary records completed tracker submissions and any returned registered-torrent authority.
type UploadSummary struct {
	Uploaded         int
	UploadedTorrents []UploadedTorrent
	// PendingPublication reports that a successful submission has no registered torrent until the tracker publishes it.
	PendingPublication bool
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

// TorrentResult carries a torrent artifact reference and tracker context
// between creation, upload, and client-injection services.
type TorrentResult struct {
	Path      string
	InfoHash  string
	URL       string
	Tracker   string
	CrossSeed bool
	// RehashedTrackers lists selected trackers whose preparation required the
	// created base torrent and should be scheduled after reusable uploads.
	RehashedTrackers []string
	// SkippedTrackers lists selected trackers omitted by SkipIfRehashTrackers.
	// Path can be empty when every selected tracker was skipped.
	SkippedTrackers []string
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PreparedGeneration identifies one immutable prepared-release generation for
// a normalized source path.
type PreparedGeneration uint64

// PreparationCompatibility records the four exact keys that permit reuse of a
// prepared-release generation.
type PreparationCompatibility struct {
	SourceFingerprint          string
	FactInstructionFingerprint string
	PolicyFingerprint          string
	ContractVersion            string
}

// PreparedRelease is the immutable, source-scoped read model of finalized
// release facts. Workflow choices, diagnostics, resources, and outcomes are
// intentionally excluded.
type PreparedRelease struct {
	Generation       PreparedGeneration
	Compatibility    PreparationCompatibility
	Source           SourceManifest
	Naming           NamingFacts
	Episode          EpisodeFacts
	Media            MediaFacts
	Disc             DiscFacts
	Identity         ExternalIdentity
	ProviderMetadata SourceScopedMetadata
	Assessments      ReleaseAssessments
	PreparedAt       time.Time `ts_type:"string"`
}

// PrepareResult returns one immutable release projection plus non-canonical
// preparation diagnostics.
type PrepareResult struct {
	Release     PreparedRelease
	Diagnostics []PreparationDiagnostic
}

// ReleaseRef identifies one exact prepared generation without exposing its
// facts or private preparation resources.
type ReleaseRef struct {
	SourcePath string
	Generation PreparedGeneration
}

// SourceEntryType classifies one normalized source-manifest entry.
type SourceEntryType string

const (
	// SourceEntryTypeUnknown marks an entry whose type could not be classified.
	SourceEntryTypeUnknown SourceEntryType = "unknown"
	// SourceEntryTypeFile marks a regular file.
	SourceEntryTypeFile SourceEntryType = "file"
	// SourceEntryTypeDirectory marks a directory.
	SourceEntryTypeDirectory SourceEntryType = "directory"
	// SourceEntryTypePlaylist marks a resolved disc playlist.
	SourceEntryTypePlaylist SourceEntryType = "playlist"
)

// SourceManifest is the finalized source inventory and measurement set for one
// prepared release.
type SourceManifest struct {
	SourcePath        string
	Size              int64
	Entries           []SourceManifestEntry
	SelectedPlaylists []PlaylistInfo
	Classification    SourceClassification
}

// SourceManifestEntry is one stable inventory item used by preparation and
// source fingerprinting.
type SourceManifestEntry struct {
	Path       string
	Type       SourceEntryType
	Size       int64
	ModifiedAt time.Time `ts_type:"string"`
	Disc       string
	Playlist   string
}

// SourceClassification records finalized source structure without exposing
// preparation-resource paths.
type SourceClassification struct {
	DiscType  string
	Container string
	MediaType string
}

// NamingFacts contains finalized reusable naming facts rather than raw parser
// output or workflow-specific name overrides.
type NamingFacts struct {
	Filename       string
	ReleaseName    string
	NameWithoutTag string
	CleanName      string
	Tag            string
	Type           string
	Artist         string
	Title          string
	Subtitle       string
	AlternateTitle string
	Year           int
	Month          int
	Day            int
	Source         string
	Resolution     string
	Codecs         []string
	Audio          []string
	HDR            []string
	Extension      string
	Languages      []string
	Site           string
	Genre          string
	Channels       string
	Collection     string
	Region         string
	Size           string
	Group          string
	Disc           string
	Editions       []string
	Other          []string
	Scene          bool
	SceneName      string
	Personal       bool
}

// EpisodeFacts contains canonical reusable episodic identity and schedule
// facts, independent of tracker policy.
type EpisodeFacts struct {
	Season            int
	Episode           int
	SeasonLabel       string
	EpisodeLabel      string
	DailyDate         string
	Pack              bool
	Title             string
	Overview          string
	Year              int
	AiredDate         string
	AirDays           []string
	AirTime           string
	AirTimezone       string
	AirTimezoneSource string
	DateMatched       bool
}

// MediaFacts contains finalized reusable media characteristics.
type MediaFacts struct {
	AudioLanguages    []string
	SubtitleLanguages []string
	Container         string
	Audio             string
	Channels          string
	Commentary        bool
	ThreeD            string
	Source            string
	Type              string
	UHD               string
	HDR               string
	Distributor       string
	Region            string
	VideoCodec        string
	VideoEncode       string
	HasEncodeSettings bool
	BitDepth          string
	Edition           string
	Repack            string
	WebDV             bool
	StreamOptimized   int
	Service           string
	ServiceLongName   string
	MediaInfoUniqueID string
	Anime             bool
}

// DiscFacts contains typed disc measurements that are safe to publish as
// reusable facts. Local report and media paths remain preparation resources.
type DiscFacts struct {
	Type            string
	Summary         string
	DurationSeconds float64
	PlaylistCount   int
	DVDVOBSet       string
}

// UniqueIDStatus records the concrete MediaInfo unique-ID assessment.
type UniqueIDStatus string

const (
	// UniqueIDStatusUnknown means preparation has not established the fact.
	UniqueIDStatusUnknown UniqueIDStatus = "unknown"
	// UniqueIDStatusNotApplicable means the source does not require this evidence.
	UniqueIDStatusNotApplicable UniqueIDStatus = "not_applicable"
	// UniqueIDStatusPresent means required unique-ID evidence is present.
	UniqueIDStatusPresent UniqueIDStatus = "present"
	// UniqueIDStatusMissing means required unique-ID evidence is absent.
	UniqueIDStatusMissing UniqueIDStatus = "missing"
)

// EncodeSettingsStatus records the concrete MediaInfo encode-settings
// assessment.
type EncodeSettingsStatus string

const (
	// EncodeSettingsStatusUnknown means preparation has not established the fact.
	EncodeSettingsStatusUnknown EncodeSettingsStatus = "unknown"
	// EncodeSettingsStatusNotApplicable means the source does not require encode settings.
	EncodeSettingsStatusNotApplicable EncodeSettingsStatus = "not_applicable"
	// EncodeSettingsStatusPresent means required encode settings are present.
	EncodeSettingsStatusPresent EncodeSettingsStatus = "present"
	// EncodeSettingsStatusMissing means required encode settings are absent.
	EncodeSettingsStatusMissing EncodeSettingsStatus = "missing"
)

// NamingStatus records whether finalized naming facts are complete.
type NamingStatus string

const (
	// NamingStatusUnknown means preparation has not assessed naming completeness.
	NamingStatusUnknown NamingStatus = "unknown"
	// NamingStatusComplete means all required naming facts are present.
	NamingStatusComplete NamingStatus = "complete"
	// NamingStatusIncomplete means one or more required naming facts are missing.
	NamingStatusIncomplete NamingStatus = "incomplete"
)

// NamingAssessment records concrete naming completeness and typed missing fact
// labels without turning tracker policy into prepared truth.
type NamingAssessment struct {
	Status  NamingStatus
	Missing []NamingRequirement
}

// NamingRequirement identifies one missing finalized naming fact.
type NamingRequirement string

// ReleaseAssessments contains concrete source-derived assessments. Operations
// decide whether these facts block work.
type ReleaseAssessments struct {
	MediaInfoUniqueID       UniqueIDStatus
	MediaInfoEncodeSettings EncodeSettingsStatus
	Naming                  NamingAssessment
}

// UniqueIDRequirementSatisfied reports whether the concrete assessment permits
// work that requires a MediaInfo unique ID.
func (a ReleaseAssessments) UniqueIDRequirementSatisfied() bool {
	return a.MediaInfoUniqueID == UniqueIDStatusPresent || a.MediaInfoUniqueID == UniqueIDStatusNotApplicable
}

// EncodeSettingsRequirementSatisfied reports whether the concrete assessment
// permits work that requires MediaInfo encode settings.
func (a ReleaseAssessments) EncodeSettingsRequirementSatisfied() bool {
	return a.MediaInfoEncodeSettings == EncodeSettingsStatusPresent ||
		a.MediaInfoEncodeSettings == EncodeSettingsStatusNotApplicable
}

// CanonicalCategory is the finalized top-level movie-or-TV classification.
type CanonicalCategory string

const (
	// CanonicalCategoryUnknown represents an intentionally or currently unknown category.
	CanonicalCategoryUnknown CanonicalCategory = "unknown"
	// CanonicalCategoryMovie identifies movie releases.
	CanonicalCategoryMovie CanonicalCategory = "movie"
	// CanonicalCategoryTV identifies television releases.
	CanonicalCategoryTV CanonicalCategory = "tv"
)

// IdentityProvider identifies one explicit external-ID provider.
type IdentityProvider string

const (
	// IdentityProviderTMDB identifies The Movie Database.
	IdentityProviderTMDB IdentityProvider = "tmdb"
	// IdentityProviderIMDB identifies IMDb.
	IdentityProviderIMDB IdentityProvider = "imdb"
	// IdentityProviderTVDB identifies TheTVDB.
	IdentityProviderTVDB IdentityProvider = "tvdb"
	// IdentityProviderTVmaze identifies TVmaze.
	IdentityProviderTVmaze IdentityProvider = "tvmaze"
	// IdentityProviderMAL identifies MyAnimeList/AniList-compatible identity.
	IdentityProviderMAL IdentityProvider = "mal"
)

// IdentityProvenance identifies the origin of one canonical identity fact.
type IdentityProvenance string

const (
	// IdentityProvenanceUnknown means no canonical fact has been resolved.
	IdentityProvenanceUnknown IdentityProvenance = "unknown"
	// IdentityProvenanceExplicit identifies a caller-locked fact.
	IdentityProvenanceExplicit IdentityProvenance = "explicit"
	// IdentityProvenancePersisted identifies reusable source-scoped state.
	IdentityProvenancePersisted IdentityProvenance = "persisted"
	// IdentityProvenanceMediaInfo identifies embedded MediaInfo evidence.
	IdentityProvenanceMediaInfo IdentityProvenance = "mediainfo"
	// IdentityProvenanceScene identifies scene/NFO evidence.
	IdentityProvenanceScene IdentityProvenance = "scene"
	// IdentityProvenanceArr identifies Sonarr or Radarr evidence.
	IdentityProvenanceArr IdentityProvenance = "arr"
	// IdentityProvenanceTracker identifies tracker evidence.
	IdentityProvenanceTracker IdentityProvenance = "tracker"
	// IdentityProvenanceProvider identifies provider-derived evidence.
	IdentityProvenanceProvider IdentityProvenance = "provider"
	// IdentityProvenanceLegacy identifies migrated pre-contract evidence.
	IdentityProvenanceLegacy IdentityProvenance = "legacy"
)

// IdentityProvenanceSet records provenance for every canonical provider and
// category fact.
type IdentityProvenanceSet struct {
	TMDB     IdentityProvenance
	IMDB     IdentityProvenance
	TVDB     IdentityProvenance
	TVmaze   IdentityProvenance
	MAL      IdentityProvenance
	Category IdentityProvenance
}

// OverrideState records tri-state caller intent.
type OverrideState string

const (
	// OverrideStateUnset means automatic resolution may supply a fact.
	OverrideStateUnset OverrideState = "unset"
	// OverrideStateValue means a caller locked an explicit non-empty value.
	OverrideStateValue OverrideState = "value"
	// OverrideStateClear means a caller locked the fact as unknown.
	OverrideStateClear OverrideState = "clear"
)

// IdentityOverrideState records tri-state override intent for every canonical
// provider and the category.
type IdentityOverrideState struct {
	TMDB     OverrideState
	IMDB     OverrideState
	TVDB     OverrideState
	TVmaze   OverrideState
	MAL      OverrideState
	Category OverrideState
}

// IdentityConflictStatus records whether accepted evidence contains an
// unverified inconsistency. Proven locked conflicts are fatal and unpublished.
type IdentityConflictStatus string

const (
	// IdentityConflictNone means no inconsistency was found.
	IdentityConflictNone IdentityConflictStatus = "none"
	// IdentityConflictUnverified means evidence may conflict but cannot be proven inconsistent.
	IdentityConflictUnverified IdentityConflictStatus = "unverified"
)

// IdentityResolutionKey records non-secret fingerprints controlling canonical
// identity reuse.
type IdentityResolutionKey struct {
	SourceFingerprint string
	IntentFingerprint string
	ContractVersion   string
}

// ExternalIdentity is the only prepared-release source for provider IDs and
// top-level movie-or-TV classification.
type ExternalIdentity struct {
	SourcePath string
	Generation PreparedGeneration
	TMDBID     int
	IMDBID     int
	TVDBID     int
	TVmazeID   int
	MALID      int
	Category   CanonicalCategory
	Provenance IdentityProvenanceSet
	Overrides  IdentityOverrideState
	Conflict   IdentityConflictStatus
	Resolution IdentityResolutionKey
	ResolvedAt time.Time `ts_type:"string"`
}

// ProviderID returns the canonical ID for provider without applying fallbacks.
func (i ExternalIdentity) ProviderID(provider IdentityProvider) (int, bool) {
	var id int
	switch provider {
	case IdentityProviderTMDB:
		id = i.TMDBID
	case IdentityProviderIMDB:
		id = i.IMDBID
	case IdentityProviderTVDB:
		id = i.TVDBID
	case IdentityProviderTVmaze:
		id = i.TVmazeID
	case IdentityProviderMAL:
		id = i.MALID
	default:
		return 0, false
	}
	return id, id > 0
}

// RequirementKind identifies a canonical prerequisite required by an operation.
type RequirementKind string

const (
	// RequirementKindProviderID identifies a missing provider ID.
	RequirementKindProviderID RequirementKind = "provider_id"
	// RequirementKindCategory identifies a missing canonical category.
	RequirementKindCategory RequirementKind = "category"
)

// MissingRequirementError reports one absent canonical prerequisite.
type MissingRequirementError struct {
	Requirement RequirementKind
	Provider    IdentityProvider
}

func (e *MissingRequirementError) Error() string {
	if e == nil {
		return "missing canonical requirement"
	}
	if e.Requirement == RequirementKindProviderID {
		return fmt.Sprintf("missing canonical %s provider ID", e.Provider)
	}
	return "missing canonical category"
}

// RequireProviderID returns one canonical provider ID or a typed missing
// requirement error.
func (i ExternalIdentity) RequireProviderID(provider IdentityProvider) (int, error) {
	id, ok := i.ProviderID(provider)
	if !ok {
		return 0, &MissingRequirementError{Requirement: RequirementKindProviderID, Provider: provider}
	}
	return id, nil
}

// RequireCategory returns the canonical category or a typed missing
// requirement error.
func (i ExternalIdentity) RequireCategory() (CanonicalCategory, error) {
	category, err := NormalizeCanonicalCategory(string(i.Category))
	if err != nil {
		return CanonicalCategoryUnknown, &MissingRequirementError{Requirement: RequirementKindCategory}
	}
	switch category {
	case CanonicalCategoryMovie, CanonicalCategoryTV:
		return category, nil
	case CanonicalCategoryUnknown:
		return CanonicalCategoryUnknown, &MissingRequirementError{Requirement: RequirementKindCategory}
	}
	return CanonicalCategoryUnknown, &MissingRequirementError{Requirement: RequirementKindCategory}
}

// SourceScopedMetadata stores optional provider enrichment for one source and
// prepared generation.
type SourceScopedMetadata struct {
	SourcePath string
	Generation PreparedGeneration
	TMDB       *TMDBMetadata
	IMDB       *IMDBMetadata
	TVDB       *TVDBMetadata
	TVmaze     *TVmazeMetadata
	AniList    *AniListMetadata
	Bluray     *BlurayMetadata
	UpdatedAt  time.Time `ts_type:"string"`
}

// DiagnosticSeverity classifies preparation diagnostics without turning them
// into workflow outcomes.
type DiagnosticSeverity string

const (
	// DiagnosticSeverityInfo records explanatory evidence.
	DiagnosticSeverityInfo DiagnosticSeverity = "info"
	// DiagnosticSeverityWarning records a non-fatal preparation concern.
	DiagnosticSeverityWarning DiagnosticSeverity = "warning"
)

// PreparationDiagnostic is non-canonical evidence or warning returned beside
// a prepared release.
type PreparationDiagnostic struct {
	Code       string
	Severity   DiagnosticSeverity
	Message    string
	Candidates []ExternalIdentityCandidate
}

// ExternalIdentityCandidate is possible provider evidence exposed for review,
// never a canonical external-identity fact.
type ExternalIdentityCandidate struct {
	Provider      IdentityProvider
	ID            int
	Title         string
	OriginalTitle string
	Year          int
	Category      CanonicalCategory
	MediaType     string
	Overview      string
	PosterURL     string
	Similarity    float64
}

// Clone returns a detached prepared-release projection.
func (r PreparedRelease) Clone() (PreparedRelease, error) {
	return clonePreparedValue(r)
}

// Clone returns a detached result whose release and diagnostics can be mutated
// by the caller without affecting module-owned state.
func (r PrepareResult) Clone() (PrepareResult, error) {
	return clonePreparedValue(r)
}

func clonePreparedValue[T any](value T) (T, error) {
	var cloned T
	payload, err := json.Marshal(value)
	if err != nil {
		return cloned, fmt.Errorf("clone prepared value: marshal: %w", err)
	}
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return cloned, fmt.Errorf("clone prepared value: unmarshal: %w", err)
	}
	return cloned, nil
}

// NormalizeCanonicalCategory maps supported legacy/user labels to a canonical
// movie, TV, or explicit unknown value.
func NormalizeCanonicalCategory(value string) (CanonicalCategory, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "unknown":
		return CanonicalCategoryUnknown, nil
	case "movie", "film":
		return CanonicalCategoryMovie, nil
	case "tv", "television", "series", "episode":
		return CanonicalCategoryTV, nil
	default:
		return CanonicalCategoryUnknown, errors.New("unsupported canonical category")
	}
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ReleaseWorkflowUploadMode selects tracker execution or the debug/site-check path.
type ReleaseWorkflowUploadMode string

const (
	ReleaseWorkflowUploadModeUpload ReleaseWorkflowUploadMode = "upload"
	ReleaseWorkflowUploadModeDebug  ReleaseWorkflowUploadMode = "debug"
)

// ReleaseWorkflowPreparedReleaseMode controls whether cached prepared state may be created.
type ReleaseWorkflowPreparedReleaseMode string

const (
	ReleaseWorkflowPreparedReleaseAllow   ReleaseWorkflowPreparedReleaseMode = "allow"
	ReleaseWorkflowPreparedReleaseRequire ReleaseWorkflowPreparedReleaseMode = "require"
)

// ReleaseWorkflowDuplicateDisposition controls evidence handling.
type ReleaseWorkflowDuplicateDisposition string

const (
	ReleaseWorkflowDuplicateAsk    ReleaseWorkflowDuplicateDisposition = "ask"
	ReleaseWorkflowDuplicateBlock  ReleaseWorkflowDuplicateDisposition = "block"
	ReleaseWorkflowDuplicateUpload ReleaseWorkflowDuplicateDisposition = "upload"
)

// CreateReleaseWorkflowUploadRequest starts one durable single-source upload.
// IdempotencyKey is supplied by the transport and is never decoded from JSON.
type CreateReleaseWorkflowUploadRequest struct {
	Source         ReleaseWorkflowUploadSource       `json:"source"`
	Unattended     *ReleaseWorkflowUploadUnattended  `json:"unattended"`
	Execution      ReleaseWorkflowUploadExecution    `json:"execution,omitempty"`
	Trackers       ReleaseWorkflowUploadTrackers     `json:"trackers,omitempty"`
	Preparation    ReleaseWorkflowUploadPreparation  `json:"preparation,omitempty"`
	Duplicates     ReleaseWorkflowUploadDuplicates   `json:"duplicates,omitempty"`
	Media          ReleaseWorkflowUploadMedia        `json:"media,omitempty"`
	Descriptions   ReleaseWorkflowUploadDescriptions `json:"descriptions,omitempty"`
	ImageHosting   ReleaseWorkflowUploadImageHosting `json:"imageHosting,omitempty"`
	Client         ReleaseWorkflowUploadClient       `json:"client,omitempty"`
	Torrent        ReleaseWorkflowUploadTorrent      `json:"torrent,omitempty"`
	IdempotencyKey string                            `json:"-"`
}

// ReleaseWorkflowUploadSource names one server-local source.
type ReleaseWorkflowUploadSource struct {
	Path string `json:"path"`
}

// ReleaseWorkflowUploadUnattended selects strict unattended when Confirm is
// false or retained required-action feedback when Confirm is true.
type ReleaseWorkflowUploadUnattended struct {
	Confirm bool `json:"confirm,omitempty"`
}

// ReleaseWorkflowUploadExecution contains top-level execution choices.
type ReleaseWorkflowUploadExecution struct {
	Mode            ReleaseWorkflowUploadMode          `json:"mode,omitempty"`
	PreparedRelease ReleaseWorkflowPreparedReleaseMode `json:"preparedRelease,omitempty"`
	RunLogLevel     *string                            `json:"runLogLevel,omitempty"`
}

// ReleaseWorkflowUploadTrackers contains normalized tracker selection and projection intent.
type ReleaseWorkflowUploadTrackers struct {
	Include           []TrackerID                                          `json:"include,omitempty"`
	Remove            []TrackerID                                          `json:"remove,omitempty"`
	SourceIDs         map[TrackerID]string                                 `json:"sourceIds,omitempty"`
	DefaultProjection *ReleaseWorkflowUploadTrackerProjection              `json:"defaultProjection,omitempty"`
	Projection        map[TrackerID]ReleaseWorkflowUploadTrackerProjection `json:"projection,omitempty"`
}

// ReleaseWorkflowUploadTrackerProjection contains transport-neutral tracker-local input.
type ReleaseWorkflowUploadTrackerProjection struct {
	UploadReleaseName WorkflowPatch[string]              `json:"uploadReleaseName,omitempty"`
	AdditionalNames   map[string]*string                 `json:"additionalNames,omitempty"`
	Questionnaire     map[string]*string                 `json:"questionnaire,omitempty"`
	Config            ReleaseWorkflowUploadTrackerConfig `json:"config,omitempty"`
	Site              ReleaseWorkflowUploadTrackerSite   `json:"site,omitempty"`
}

// ReleaseWorkflowUploadTrackerConfig carries optional per-upload tracker configuration.
type ReleaseWorkflowUploadTrackerConfig struct {
	Anon    *bool   `json:"anon,omitempty"`
	Draft   *bool   `json:"draft,omitempty"`
	ModQ    *bool   `json:"modq,omitempty"`
	Channel *string `json:"channel,omitempty"`
}

// ReleaseWorkflowUploadTrackerSite carries typed tracker-site options.
type ReleaseWorkflowUploadTrackerSite struct {
	TIK ReleaseWorkflowUploadTIKOptions `json:"tik,omitempty"`
}

// ReleaseWorkflowUploadTIKOptions carries TIK-specific typed options.
type ReleaseWorkflowUploadTIKOptions struct {
	Foreign  *bool   `json:"foreign,omitempty"`
	Opera    *bool   `json:"opera,omitempty"`
	Asian    *bool   `json:"asian,omitempty"`
	DiscType *string `json:"discType,omitempty"`
}

// ReleaseWorkflowUploadPreparation contains canonical preparation intent.
type ReleaseWorkflowUploadPreparation struct {
	Facts         ReleaseWorkflowUploadFacts        `json:"facts,omitempty"`
	Policy        ReleaseWorkflowUploadPolicy       `json:"policy,omitempty"`
	ClientSearch  ReleaseWorkflowUploadClientSearch `json:"clientSearch,omitempty"`
	Force         bool                              `json:"force,omitempty"`
	ConfirmRescan bool                              `json:"confirmRescan,omitempty"`
}

// ReleaseWorkflowUploadFacts contains fact-producing overrides only.
type ReleaseWorkflowUploadFacts struct {
	ExternalIDs  ReleaseWorkflowUploadExternalIDs `json:"externalIds,omitempty"`
	ReleaseName  ReleaseWorkflowUploadReleaseName `json:"releaseName,omitempty"`
	Metadata     ReleaseWorkflowUploadMetadata    `json:"metadata,omitempty"`
	Category     *CanonicalCategory               `json:"category,omitempty"`
	SourceLookup *string                          `json:"sourceLookup,omitempty"`
	Playlist     *ReleaseWorkflowUploadPlaylist   `json:"playlist,omitempty"`
}

// ReleaseWorkflowUploadNumericID preserves explicit zero as an instruction.
type ReleaseWorkflowUploadNumericID struct {
	Value *int `json:"value"`
}

// ReleaseWorkflowUploadStringID preserves explicit empty string as an instruction.
type ReleaseWorkflowUploadStringID struct {
	Value *string `json:"value"`
}

// ReleaseWorkflowUploadExternalIDs contains typed provider identifiers.
type ReleaseWorkflowUploadExternalIDs struct {
	TMDB   *ReleaseWorkflowUploadNumericID `json:"tmdb,omitempty"`
	IMDB   *ReleaseWorkflowUploadStringID  `json:"imdb,omitempty"`
	TVDB   *ReleaseWorkflowUploadNumericID `json:"tvdb,omitempty"`
	TVmaze *ReleaseWorkflowUploadNumericID `json:"tvmaze,omitempty"`
	MAL    *ReleaseWorkflowUploadNumericID `json:"mal,omitempty"`
}

// ReleaseWorkflowUploadReleaseName contains presence-aware naming overrides.
type ReleaseWorkflowUploadReleaseName struct {
	Category         *string `json:"category,omitempty"`
	Type             *string `json:"type,omitempty"`
	Source           *string `json:"source,omitempty"`
	Resolution       *string `json:"resolution,omitempty"`
	Tag              *string `json:"tag,omitempty"`
	Service          *string `json:"service,omitempty"`
	Edition          *string `json:"edition,omitempty"`
	Season           *string `json:"season,omitempty"`
	Episode          *string `json:"episode,omitempty"`
	EpisodeTitle     *string `json:"episodeTitle,omitempty"`
	ManualYear       *int    `json:"manualYear,omitempty"`
	Daily            *string `json:"daily,omitempty"`
	UseSeasonEpisode *bool   `json:"useSeasonEpisode,omitempty"`
	NoSeason         *bool   `json:"noSeason,omitempty"`
	NoYear           *bool   `json:"noYear,omitempty"`
	NoAKA            *bool   `json:"noAka,omitempty"`
	NoTag            *bool   `json:"noTag,omitempty"`
	NoEdition        *bool   `json:"noEdition,omitempty"`
	NoDub            *bool   `json:"noDub,omitempty"`
	NoDual           *bool   `json:"noDual,omitempty"`
	DualAudio        *bool   `json:"dualAudio,omitempty"`
	Region           *string `json:"region,omitempty"`
}

// ReleaseWorkflowUploadMetadata contains presence-aware metadata overrides.
type ReleaseWorkflowUploadMetadata struct {
	Distributor      *string `json:"distributor,omitempty"`
	OriginalLanguage *string `json:"originalLanguage,omitempty"`
	PersonalRelease  *bool   `json:"personalRelease,omitempty"`
	Commentary       *bool   `json:"commentary,omitempty"`
	WebDV            *bool   `json:"webDv,omitempty"`
	StreamOptimized  *bool   `json:"streamOptimized,omitempty"`
	Anime            *bool   `json:"anime,omitempty"`
}

// ReleaseWorkflowUploadPlaylist preserves explicit empty selection.
type ReleaseWorkflowUploadPlaylist struct {
	Selected []string `json:"selected"`
	UseAll   bool     `json:"useAll,omitempty"`
}

// ReleaseWorkflowUploadPolicy controls canonical preparation compatibility.
type ReleaseWorkflowUploadPolicy struct {
	KeepFolder *bool `json:"keepFolder,omitempty"`
	KeepImages *bool `json:"keepImages,omitempty"`
	OnlyID     *bool `json:"onlyId,omitempty"`
}

// ReleaseWorkflowUploadClientSearch controls fact-producing client discovery.
type ReleaseWorkflowUploadClientSearch struct {
	Skip   *bool   `json:"skip,omitempty"`
	Client *string `json:"client,omitempty"`
}

// ReleaseWorkflowUploadDuplicates controls duplicate checks and evidence defaults.
type ReleaseWorkflowUploadDuplicates struct {
	RemoteCheck *bool                               `json:"remoteCheck,omitempty"`
	CheckCount  *int                                `json:"checkCount,omitempty"`
	OnEvidence  ReleaseWorkflowDuplicateDisposition `json:"onEvidence,omitempty"`
	AllowUpload []TrackerID                         `json:"allowUpload,omitempty"`
}

// ReleaseWorkflowUploadMedia groups screenshot and DVD-menu choices.
type ReleaseWorkflowUploadMedia struct {
	Screenshots ReleaseWorkflowUploadScreenshots `json:"screenshots,omitempty"`
	DVDMenus    ReleaseWorkflowUploadDVDMenus    `json:"dvdMenus,omitempty"`
}

// ReleaseWorkflowUploadScreenshots carries capture, manual, comparison, and selection intent.
type ReleaseWorkflowUploadScreenshots struct {
	Count                  *int               `json:"count,omitempty"`
	Frames                 []int              `json:"frames,omitempty"`
	ComparisonPaths        []string           `json:"comparisonPaths,omitempty"`
	ComparisonPrimaryIndex *int               `json:"comparisonPrimaryIndex,omitempty"`
	ArtifactIDs            []PublicResourceID `json:"artifactIds,omitempty"`
}

// ReleaseWorkflowUploadDVDMenus carries automatic and manual DVD-menu intent.
type ReleaseWorkflowUploadDVDMenus struct {
	Capture   *bool    `json:"capture,omitempty"`
	MaxItems  *int     `json:"maxItems,omitempty"`
	MenuPaths []string `json:"menuPaths,omitempty"`
}

// ReleaseWorkflowUploadDescriptions contains typed description override sources.
type ReleaseWorkflowUploadDescriptions struct {
	Overrides       []ReleaseWorkflowUploadDescriptionOverride `json:"overrides,omitempty"`
	TemplateVersion *string                                    `json:"templateVersion,omitempty"`
}

// ReleaseWorkflowUploadDescriptionOverride accepts exactly one content source.
type ReleaseWorkflowUploadDescriptionOverride struct {
	GroupKey string  `json:"groupKey"`
	Inline   *string `json:"inline,omitempty"`
	File     *string `json:"file,omitempty"`
	URL      *string `json:"url,omitempty"`
}

// ReleaseWorkflowUploadImageHosting contains caller-visible host choices.
type ReleaseWorkflowUploadImageHosting struct {
	PreferredHost *string `json:"preferredHost,omitempty"`
	SkipUpload    *bool   `json:"skipUpload,omitempty"`
}

// ReleaseWorkflowUploadClient contains torrent-client choices.
type ReleaseWorkflowUploadClient struct {
	NoSeed            *bool   `json:"noSeed,omitempty"`
	SkipAutoDiscovery *bool   `json:"skipAutoDiscovery,omitempty"`
	Selected          *string `json:"selected,omitempty"`
	QbitTag           *string `json:"qbitTag,omitempty"`
	QbitCategory      *string `json:"qbitCategory,omitempty"`
	ForceRecheck      *bool   `json:"forceRecheck,omitempty"`
}

// ReleaseWorkflowUploadTorrent contains torrent construction choices.
type ReleaseWorkflowUploadTorrent struct {
	InfoHash        *string `json:"infoHash,omitempty"`
	MaxPieceSizeMiB *int    `json:"maxPieceSizeMiB,omitempty"`
	NoHash          *bool   `json:"noHash,omitempty"`
	Rehash          *bool   `json:"rehash,omitempty"`
}

// Validate rejects malformed or contradictory composite upload requests.
func (r CreateReleaseWorkflowUploadRequest) Validate() error {
	if strings.TrimSpace(r.Source.Path) == "" {
		return errors.New("source path is required")
	}
	if r.Unattended == nil {
		return errors.New("unattended object is required")
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return errors.New("idempotency key is required")
	}
	switch r.Execution.Mode {
	case "", ReleaseWorkflowUploadModeUpload, ReleaseWorkflowUploadModeDebug:
	default:
		return fmt.Errorf("unsupported upload execution mode %q", r.Execution.Mode)
	}
	switch r.Execution.PreparedRelease {
	case "", ReleaseWorkflowPreparedReleaseAllow, ReleaseWorkflowPreparedReleaseRequire:
	default:
		return fmt.Errorf("unsupported prepared release policy %q", r.Execution.PreparedRelease)
	}
	if err := validateReleaseWorkflowUploadTrackerIDs(r.Trackers); err != nil {
		return err
	}
	if r.Duplicates.CheckCount != nil && (*r.Duplicates.CheckCount < 1 || *r.Duplicates.CheckCount > 2) {
		return errors.New("duplicate check count must be one or two")
	}
	switch r.Duplicates.OnEvidence {
	case "", ReleaseWorkflowDuplicateAsk, ReleaseWorkflowDuplicateBlock, ReleaseWorkflowDuplicateUpload:
	default:
		return fmt.Errorf("unsupported duplicate evidence disposition %q", r.Duplicates.OnEvidence)
	}
	if r.Media.Screenshots.Count != nil && *r.Media.Screenshots.Count < 0 {
		return errors.New("screenshot count must not be negative")
	}
	if r.Media.Screenshots.ComparisonPrimaryIndex != nil {
		index := *r.Media.Screenshots.ComparisonPrimaryIndex
		if index < 1 || index > len(r.Media.Screenshots.ComparisonPaths) {
			return errors.New("comparison primary index must identify a supplied comparison path")
		}
	}
	if r.Media.DVDMenus.MaxItems != nil && *r.Media.DVDMenus.MaxItems < 0 {
		return errors.New("DVD menu count must not be negative")
	}
	for _, override := range r.Descriptions.Overrides {
		if strings.TrimSpace(override.GroupKey) == "" {
			return errors.New("description override group key is required")
		}
		sources := 0
		for _, present := range []bool{override.Inline != nil, override.File != nil, override.URL != nil} {
			if present {
				sources++
			}
		}
		if sources != 1 {
			return errors.New("description override requires exactly one inline, file, or URL source")
		}
		if override.URL != nil {
			parsed, err := url.ParseRequestURI(strings.TrimSpace(*override.URL))
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("description override URL must use http or https")
			}
		}
	}
	if r.Torrent.NoHash != nil && r.Torrent.Rehash != nil && *r.Torrent.NoHash && *r.Torrent.Rehash {
		return errors.New("torrent noHash and rehash cannot both be true")
	}
	return validateReleaseWorkflowUploadExternalIDs(r.Preparation.Facts.ExternalIDs)
}

func validateReleaseWorkflowUploadTrackerIDs(trackers ReleaseWorkflowUploadTrackers) error {
	include := make(map[TrackerID]struct{}, len(trackers.Include))
	for _, trackerID := range trackers.Include {
		normalized := TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if normalized == "" {
			return errors.New("tracker ID is required")
		}
		include[normalized] = struct{}{}
	}
	for _, trackerID := range trackers.Remove {
		normalized := TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if normalized == "" {
			return errors.New("tracker ID is required")
		}
		if _, exists := include[normalized]; exists {
			return fmt.Errorf("tracker %s cannot be both included and removed", normalized)
		}
	}
	for trackerID := range trackers.SourceIDs {
		if strings.TrimSpace(string(trackerID)) == "" {
			return errors.New("tracker source ID key is required")
		}
	}
	for trackerID := range trackers.Projection {
		if strings.TrimSpace(string(trackerID)) == "" {
			return errors.New("tracker projection key is required")
		}
	}
	return nil
}

func validateReleaseWorkflowUploadExternalIDs(ids ReleaseWorkflowUploadExternalIDs) error {
	if ids.IMDB != nil && ids.IMDB.Value != nil {
		value := strings.TrimSpace(*ids.IMDB.Value)
		if value != "" {
			if !strings.HasPrefix(strings.ToLower(value), "tt") || len(value) <= 2 {
				return errors.New("IMDb ID must use tt-prefixed form")
			}
			for _, digit := range value[2:] {
				if digit < '0' || digit > '9' {
					return errors.New("IMDb ID must use tt-prefixed numeric form")
				}
			}
		}
	}
	for name, value := range map[string]*ReleaseWorkflowUploadNumericID{
		"tmdb":   ids.TMDB,
		"tvdb":   ids.TVDB,
		"tvmaze": ids.TVmaze,
		"mal":    ids.MAL,
	} {
		if value != nil && value.Value != nil && *value.Value < 0 {
			return fmt.Errorf("%s ID must not be negative", name)
		}
	}
	return nil
}

// ReleaseWorkflowUploadFeedbackKind discriminates one required-action response.
type ReleaseWorkflowUploadFeedbackKind string

const (
	ReleaseWorkflowUploadFeedbackPlaylistSelection  ReleaseWorkflowUploadFeedbackKind = "playlistSelection"
	ReleaseWorkflowUploadFeedbackMetadataSelection  ReleaseWorkflowUploadFeedbackKind = "metadataSelection"
	ReleaseWorkflowUploadFeedbackRescanConfirmation ReleaseWorkflowUploadFeedbackKind = "rescanConfirmation"
	// Deprecated: tracker authentication must be resolved outside upload workflows.
	ReleaseWorkflowUploadFeedbackTrackerAuthentication ReleaseWorkflowUploadFeedbackKind = "trackerAuthentication"
	// Deprecated: tracker two-factor authentication must be resolved outside upload workflows.
	ReleaseWorkflowUploadFeedbackTwoFactor         ReleaseWorkflowUploadFeedbackKind = "twoFactor"
	ReleaseWorkflowUploadFeedbackTrackerInput      ReleaseWorkflowUploadFeedbackKind = "trackerInput"
	ReleaseWorkflowUploadFeedbackQuestionnaire     ReleaseWorkflowUploadFeedbackKind = "questionnaire"
	ReleaseWorkflowUploadFeedbackRuleAuthorization ReleaseWorkflowUploadFeedbackKind = "ruleAuthorization"
	ReleaseWorkflowUploadFeedbackDuplicateReview   ReleaseWorkflowUploadFeedbackKind = "duplicateReview"
	ReleaseWorkflowUploadFeedbackTrackerApproval   ReleaseWorkflowUploadFeedbackKind = "trackerApproval"
	// Deprecated: final upload approval is no longer emitted by runtime workflows.
	ReleaseWorkflowUploadFeedbackUploadApproval ReleaseWorkflowUploadFeedbackKind = "uploadApproval"
	ReleaseWorkflowUploadFeedbackReprepare      ReleaseWorkflowUploadFeedbackKind = "reprepare"
	ReleaseWorkflowUploadFeedbackReconciliation ReleaseWorkflowUploadFeedbackKind = "reconciliation"
)

// ReleaseWorkflowUploadActionIdentity binds feedback to exact current action authority.
type ReleaseWorkflowUploadActionIdentity struct {
	ID               RequiredActionID `json:"id"`
	WorkflowRevision WorkflowRevision `json:"workflowRevision"`
}

// ReleaseWorkflowUploadFeedback supplies one required-action response.
type ReleaseWorkflowUploadFeedback struct {
	Action         ReleaseWorkflowUploadActionIdentity   `json:"action"`
	Response       ReleaseWorkflowUploadFeedbackResponse `json:"response"`
	IdempotencyKey string                                `json:"-"`
}

// ReleaseWorkflowUploadFeedbackResponse is a strict discriminated response
// object with exactly one member matching Kind.
type ReleaseWorkflowUploadFeedbackResponse struct {
	Kind                  ReleaseWorkflowUploadFeedbackKind           `json:"kind"`
	PlaylistSelection     *ReleaseWorkflowUploadPlaylistSelection     `json:"playlistSelection,omitempty"`
	MetadataSelection     *ReleaseWorkflowUploadMetadataSelection     `json:"metadataSelection,omitempty"`
	RescanConfirmation    *ReleaseWorkflowUploadConfirmation          `json:"rescanConfirmation,omitempty"`
	TrackerAuthentication *ReleaseWorkflowUploadTrackerAuthentication `json:"trackerAuthentication,omitempty"`
	TwoFactor             *ReleaseWorkflowUploadTwoFactor             `json:"twoFactor,omitempty"`
	TrackerInput          *ReleaseWorkflowUploadTrackerInput          `json:"trackerInput,omitempty"`
	Questionnaire         *ReleaseWorkflowUploadQuestionnaire         `json:"questionnaire,omitempty"`
	RuleAuthorization     *ReleaseWorkflowUploadConfirmation          `json:"ruleAuthorization,omitempty"`
	DuplicateReview       *ReleaseWorkflowUploadDuplicateReview       `json:"duplicateReview,omitempty"`
	TrackerApproval       *ReleaseWorkflowUploadTrackerApproval       `json:"trackerApproval,omitempty"`
	// Deprecated: retained for v1 decoding compatibility.
	UploadApproval *ReleaseWorkflowUploadApproval       `json:"uploadApproval,omitempty"`
	Reprepare      *ReleaseWorkflowUploadReprepare      `json:"reprepare,omitempty"`
	Reconciliation *ReleaseWorkflowUploadReconciliation `json:"reconciliation,omitempty"`
}

// ReleaseWorkflowUploadPlaylistSelection replaces retained playlist selection.
type ReleaseWorkflowUploadPlaylistSelection struct {
	Selected []string `json:"selected"`
	UseAll   bool     `json:"useAll,omitempty"`
}

// ReleaseWorkflowUploadMetadataSelection chooses one retained metadata candidate
// or supplies replacement preparation facts.
type ReleaseWorkflowUploadMetadataSelection struct {
	SelectedValues []string                    `json:"selectedValues,omitempty"`
	Facts          *ReleaseWorkflowUploadFacts `json:"facts,omitempty"`
}

// ReleaseWorkflowUploadConfirmation positively acknowledges a gated action.
type ReleaseWorkflowUploadConfirmation struct {
	Confirmed bool `json:"confirmed"`
}

// ReleaseWorkflowUploadApproval authorizes an exact subset of the reviewed tracker operations.
// An omitted tracker list preserves whole-plan approval for non-CLI callers.
//
// Deprecated: final upload approval is no longer emitted by runtime workflows.
type ReleaseWorkflowUploadApproval struct {
	Confirmed  bool        `json:"confirmed"`
	TrackerIDs []TrackerID `json:"trackerIds,omitempty"`
}

// ReleaseWorkflowUploadTrackerApproval authorizes an explicit post-dupe tracker subset.
type ReleaseWorkflowUploadTrackerApproval struct {
	Confirmed  bool        `json:"confirmed"`
	TrackerIDs []TrackerID `json:"trackerIds"`
}

// ReleaseWorkflowUploadTrackerAuthentication is retained for v1 decoding compatibility.
//
// Deprecated: tracker authentication must be resolved outside upload workflows.
type ReleaseWorkflowUploadTrackerAuthentication struct {
	TrackerID TrackerID `json:"trackerId"`
}

// ReleaseWorkflowUploadTwoFactor is retained for v1 decoding compatibility.
//
// Deprecated: tracker two-factor authentication must be resolved outside upload workflows.
type ReleaseWorkflowUploadTwoFactor struct {
	TrackerID   TrackerID `json:"trackerId"`
	ChallengeID string    `json:"challengeId"`
	Code        string    `json:"code"`
}

// ReleaseWorkflowUploadTrackerInput replaces projection input for one tracker.
type ReleaseWorkflowUploadTrackerInput struct {
	TrackerID  TrackerID                              `json:"trackerId"`
	Projection ReleaseWorkflowUploadTrackerProjection `json:"projection"`
}

// ReleaseWorkflowUploadQuestionnaire supplies questionnaire answers for one tracker.
type ReleaseWorkflowUploadQuestionnaire struct {
	TrackerID TrackerID          `json:"trackerId"`
	Answers   map[string]*string `json:"answers"`
}

// ReleaseWorkflowUploadDuplicateReview records an accepted or ignored duplicate
// decision for the action tracker or an explicitly named tracker.
type ReleaseWorkflowUploadDuplicateReview struct {
	TrackerID TrackerID    `json:"trackerId,omitempty"`
	Decision  DupeDecision `json:"decision"`
}

// ReleaseWorkflowUploadReprepare confirms forced preparation and may replace
// its preparation input.
type ReleaseWorkflowUploadReprepare struct {
	Confirmed   bool                              `json:"confirmed"`
	Preparation *ReleaseWorkflowUploadPreparation `json:"preparation,omitempty"`
}

// ReleaseWorkflowUploadReconciliation confirms that an uncertain external
// effect did not complete.
type ReleaseWorkflowUploadReconciliation struct {
	Selection string `json:"selection"`
}

// Validate rejects stale-shaped, ambiguous, or contradictory feedback.
func (f ReleaseWorkflowUploadFeedback) Validate() error {
	if strings.TrimSpace(string(f.Action.ID)) == "" || f.Action.WorkflowRevision == 0 {
		return errors.New("exact required action identity is required")
	}
	if strings.TrimSpace(f.IdempotencyKey) == "" {
		return errors.New("idempotency key is required")
	}
	members := []struct {
		kind    ReleaseWorkflowUploadFeedbackKind
		present bool
	}{
		{ReleaseWorkflowUploadFeedbackPlaylistSelection, f.Response.PlaylistSelection != nil},
		{ReleaseWorkflowUploadFeedbackMetadataSelection, f.Response.MetadataSelection != nil},
		{ReleaseWorkflowUploadFeedbackRescanConfirmation, f.Response.RescanConfirmation != nil},
		{ReleaseWorkflowUploadFeedbackTrackerAuthentication, f.Response.TrackerAuthentication != nil},
		{ReleaseWorkflowUploadFeedbackTwoFactor, f.Response.TwoFactor != nil},
		{ReleaseWorkflowUploadFeedbackTrackerInput, f.Response.TrackerInput != nil},
		{ReleaseWorkflowUploadFeedbackQuestionnaire, f.Response.Questionnaire != nil},
		{ReleaseWorkflowUploadFeedbackRuleAuthorization, f.Response.RuleAuthorization != nil},
		{ReleaseWorkflowUploadFeedbackDuplicateReview, f.Response.DuplicateReview != nil},
		{ReleaseWorkflowUploadFeedbackTrackerApproval, f.Response.TrackerApproval != nil},
		{ReleaseWorkflowUploadFeedbackUploadApproval, f.Response.UploadApproval != nil},
		{ReleaseWorkflowUploadFeedbackReprepare, f.Response.Reprepare != nil},
		{ReleaseWorkflowUploadFeedbackReconciliation, f.Response.Reconciliation != nil},
	}
	count := 0
	matched := false
	for _, member := range members {
		if member.present {
			count++
			matched = member.kind == f.Response.Kind
		}
	}
	if count != 1 || !matched {
		return errors.New("feedback response requires exactly one member matching kind")
	}
	switch f.Response.Kind {
	case ReleaseWorkflowUploadFeedbackTrackerAuthentication:
		if strings.TrimSpace(string(f.Response.TrackerAuthentication.TrackerID)) == "" {
			return errors.New("tracker authentication feedback requires tracker ID")
		}
	case ReleaseWorkflowUploadFeedbackTwoFactor:
		if strings.TrimSpace(string(f.Response.TwoFactor.TrackerID)) == "" ||
			strings.TrimSpace(f.Response.TwoFactor.ChallengeID) == "" ||
			strings.TrimSpace(f.Response.TwoFactor.Code) == "" {
			return errors.New("two-factor feedback requires tracker ID, challenge ID, and code")
		}
	case ReleaseWorkflowUploadFeedbackTrackerInput:
		if strings.TrimSpace(string(f.Response.TrackerInput.TrackerID)) == "" {
			return errors.New("tracker input feedback requires tracker ID")
		}
	case ReleaseWorkflowUploadFeedbackQuestionnaire:
		if strings.TrimSpace(string(f.Response.Questionnaire.TrackerID)) == "" || len(f.Response.Questionnaire.Answers) == 0 {
			return errors.New("questionnaire feedback requires tracker ID and answers")
		}
	case ReleaseWorkflowUploadFeedbackDuplicateReview:
		if f.Response.DuplicateReview.Decision != DupeDecisionAccepted &&
			f.Response.DuplicateReview.Decision != DupeDecisionIgnored {
			return errors.New("duplicate feedback decision must be accepted or ignored")
		}
	case ReleaseWorkflowUploadFeedbackRescanConfirmation:
		if !f.Response.RescanConfirmation.Confirmed {
			return errors.New("rescan feedback requires confirmation")
		}
	case ReleaseWorkflowUploadFeedbackRuleAuthorization:
		if !f.Response.RuleAuthorization.Confirmed {
			return errors.New("rule authorization feedback requires confirmation")
		}
	case ReleaseWorkflowUploadFeedbackTrackerApproval:
		if !f.Response.TrackerApproval.Confirmed {
			return errors.New("tracker approval feedback requires confirmation")
		}
		if len(f.Response.TrackerApproval.TrackerIDs) == 0 {
			return errors.New("tracker approval feedback requires at least one tracker ID")
		}
		seen := make(map[TrackerID]struct{}, len(f.Response.TrackerApproval.TrackerIDs))
		for _, trackerID := range f.Response.TrackerApproval.TrackerIDs {
			trackerID = normalizeTrackerID(trackerID)
			if trackerID == "" {
				return errors.New("tracker approval tracker IDs must not be empty")
			}
			if _, duplicate := seen[trackerID]; duplicate {
				return fmt.Errorf("tracker approval contains duplicate tracker %s", trackerID)
			}
			seen[trackerID] = struct{}{}
		}
	case ReleaseWorkflowUploadFeedbackUploadApproval:
		if !f.Response.UploadApproval.Confirmed {
			return errors.New("upload approval feedback requires confirmation")
		}
		seen := make(map[TrackerID]struct{}, len(f.Response.UploadApproval.TrackerIDs))
		for _, trackerID := range f.Response.UploadApproval.TrackerIDs {
			trackerID = normalizeTrackerID(trackerID)
			if trackerID == "" {
				return errors.New("upload approval tracker IDs must not be empty")
			}
			if _, duplicate := seen[trackerID]; duplicate {
				return fmt.Errorf("upload approval contains duplicate tracker %s", trackerID)
			}
			seen[trackerID] = struct{}{}
		}
	case ReleaseWorkflowUploadFeedbackReprepare:
		if !f.Response.Reprepare.Confirmed {
			return errors.New("reprepare feedback requires confirmation")
		}
	case ReleaseWorkflowUploadFeedbackReconciliation:
		if f.Response.Reconciliation.Selection != RequiredActionReconcileNotCompleted {
			return errors.New("reconciliation feedback must select not_completed")
		}
	case ReleaseWorkflowUploadFeedbackPlaylistSelection,
		ReleaseWorkflowUploadFeedbackMetadataSelection:
	default:
		return fmt.Errorf("unsupported upload feedback kind %q", f.Response.Kind)
	}
	return nil
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

// ReleaseFactInstructionSnapshot retains exact fact-producing caller intent.
type ReleaseFactInstructionSnapshot struct {
	ID           ReleaseFactInstructionSnapshotID `json:"id"`
	WorkflowID   WorkflowID                       `json:"workflowId"`
	Revision     WorkflowRevision                 `json:"revision"`
	Instructions ReleaseFactInstructions          `json:"instructions"`
	Fingerprint  WorkflowFingerprint              `json:"fingerprint"`
	CreatedAt    time.Time                        `json:"createdAt" ts_type:"string"`
}

// ReleaseSnapshot exposes canonical release facts with derived display and diagnostics.
type ReleaseSnapshot struct {
	ID                     ReleaseSnapshotID                 `json:"id"`
	WorkflowID             WorkflowID                        `json:"workflowId"`
	Revision               WorkflowRevision                  `json:"revision"`
	FactInstructions       ReleaseFactInstructionSnapshotRef `json:"factInstructions"`
	PreparationFingerprint WorkflowFingerprint               `json:"preparationFingerprint,omitempty"`
	Release                PreparedRelease                   `json:"release"`
	Display                PreparedReleaseDisplay            `json:"display"`
	Diagnostics            []PreparationDiagnostic           `json:"diagnostics,omitempty"`
	Fingerprint            WorkflowFingerprint               `json:"fingerprint"`
	CreatedAt              time.Time                         `json:"createdAt" ts_type:"string"`
}

// TrackerCapabilityDescriptor contains only safe declarative tracker capabilities.
type TrackerCapabilityDescriptor struct {
	DuplicateSearch        bool `json:"duplicateSearch"`
	Rules                  bool `json:"rules"`
	Metadata               bool `json:"metadata"`
	LocalizedMetadata      bool `json:"localizedMetadata"`
	TrackerData            bool `json:"trackerData"`
	Claims                 bool `json:"claims"`
	Authentication         bool `json:"authentication"`
	StaticBannedGroups     bool `json:"staticBannedGroups"`
	DynamicBannedGroups    bool `json:"dynamicBannedGroups"`
	ScreenshotRequirement  bool `json:"screenshotRequirement"`
	Description            bool `json:"description"`
	ImageHosting           bool `json:"imageHosting"`
	TorrentPersonalization bool `json:"torrentPersonalization"`
}

// TrackerCatalogDescriptor separates stable identity from mutable presentation data.
type TrackerCatalogDescriptor struct {
	TrackerID           TrackerID                   `json:"trackerId"`
	DisplayName         string                      `json:"displayName"`
	Aliases             []string                    `json:"aliases,omitempty"`
	Family              string                      `json:"family"`
	BaseURL             string                      `json:"baseUrl"`
	UploadContentMode   string                      `json:"uploadContentMode"`
	DescriptionGroup    string                      `json:"descriptionGroup,omitempty"`
	MetadataLocale      string                      `json:"metadataLocale,omitempty"`
	ProjectorVersion    string                      `json:"projectorVersion"`
	PolicyFingerprint   WorkflowFingerprint         `json:"policyFingerprint"`
	Capabilities        TrackerCapabilityDescriptor `json:"capabilities"`
	ConfigurationFields []TrackerCatalogField       `json:"configurationFields,omitempty"`
}

// TrackerCatalogSnapshot is an immutable supported-tracker catalog.
type TrackerCatalogSnapshot struct {
	ID             TrackerCatalogSnapshotID   `json:"id"`
	Revision       WorkflowRevision           `json:"revision"`
	CatalogVersion string                     `json:"catalogVersion"`
	Trackers       []TrackerCatalogDescriptor `json:"trackers"`
	Fingerprint    WorkflowFingerprint        `json:"fingerprint"`
	CreatedAt      time.Time                  `json:"createdAt" ts_type:"string"`
}

// TrackerRuntimeEntry is safe effective runtime state without secret values.
type TrackerRuntimeEntry struct {
	TrackerID            TrackerID           `json:"trackerId"`
	Configured           bool                `json:"configured"`
	Default              bool                `json:"default"`
	ConfigurationVersion string              `json:"configurationVersion"`
	ConfigFingerprint    WorkflowFingerprint `json:"configFingerprint"`
}

// TrackerRuntimeSnapshot is one immutable safe runtime/configuration projection.
type TrackerRuntimeSnapshot struct {
	ID                TrackerRuntimeSnapshotID  `json:"id"`
	Revision          WorkflowRevision          `json:"revision"`
	Catalog           TrackerCatalogSnapshotRef `json:"catalog"`
	RuntimeGeneration string                    `json:"runtimeGeneration"`
	Trackers          []TrackerRuntimeEntry     `json:"trackers"`
	Fingerprint       WorkflowFingerprint       `json:"fingerprint"`
	CreatedAt         time.Time                 `json:"createdAt" ts_type:"string"`
}

// TrackerSelection is one immutable ordered tracker choice for a workflow.
type TrackerSelection struct {
	ID          TrackerSelectionID        `json:"id"`
	WorkflowID  WorkflowID                `json:"workflowId"`
	Revision    WorkflowRevision          `json:"revision"`
	Catalog     TrackerCatalogSnapshotRef `json:"catalog"`
	Runtime     TrackerRuntimeSnapshotRef `json:"runtime"`
	TrackerIDs  []TrackerID               `json:"trackerIds"`
	Fingerprint WorkflowFingerprint       `json:"fingerprint"`
	CreatedAt   time.Time                 `json:"createdAt" ts_type:"string"`
}

// TrackerProjectionInstructions contains optional tracker-local caller intent.
// Nil means automatic resolution; pointed-to empty values are explicit clears.
type TrackerProjectionInstructions struct {
	UploadReleaseName WorkflowPatch[string]  `json:"-"`
	AdditionalNames   map[string]*string     `json:"additionalNames,omitempty"`
	Questionnaire     map[string]*string     `json:"questionnaire,omitempty"`
	TrackerConfig     TrackerConfigOverrides `json:"trackerConfig,omitempty"`
	TrackerSite       TrackerSiteOverrides   `json:"trackerSite,omitempty"`
}

// TrackerProjectionInstructionSnapshot retains tracker-local projection instructions.
type TrackerProjectionInstructionSnapshot struct {
	ID           TrackerProjectionInstructionSnapshotID      `json:"id"`
	WorkflowID   WorkflowID                                  `json:"workflowId"`
	Revision     WorkflowRevision                            `json:"revision"`
	Instructions map[TrackerID]TrackerProjectionInstructions `json:"instructions"`
	Fingerprint  WorkflowFingerprint                         `json:"fingerprint"`
	CreatedAt    time.Time                                   `json:"createdAt" ts_type:"string"`
}

// TrackerReleaseNameRole describes an additional tracker name-bearing field.
type TrackerReleaseNameRole string

const (
	// TrackerReleaseNameRoleAlternate is an alternate public display name.
	TrackerReleaseNameRoleAlternate TrackerReleaseNameRole = "alternate"
	// TrackerReleaseNameRoleScene is a tracker-native scene-name field.
	TrackerReleaseNameRoleScene TrackerReleaseNameRole = "scene"
	// TrackerReleaseNameRoleGroupTitle is a tracker group or edition title.
	TrackerReleaseNameRoleGroupTitle TrackerReleaseNameRole = "group_title"
	// TrackerReleaseNameRoleSearch is a duplicate-search-specific semantic name.
	TrackerReleaseNameRoleSearch TrackerReleaseNameRole = "search"
)

// TrackerReleaseName is one typed non-principal name-bearing value.
type TrackerReleaseName struct {
	Role  TrackerReleaseNameRole `json:"role"`
	Value string                 `json:"value"`
}

// TrackerTaxonomyValue is one tracker-native taxonomy ID and display label.
type TrackerTaxonomyValue struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// TrackerTaxonomy contains finalized tracker-native classification values.
type TrackerTaxonomy struct {
	Category   TrackerTaxonomyValue `json:"category"`
	Type       TrackerTaxonomyValue `json:"type"`
	Resolution TrackerTaxonomyValue `json:"resolution"`
	Source     TrackerTaxonomyValue `json:"source"`
	Container  TrackerTaxonomyValue `json:"container"`
	Codec      TrackerTaxonomyValue `json:"codec"`
}

// TrackerProviderID is one non-secret provider or tracker identifier used by a projection.
type TrackerProviderID struct {
	Provider string `json:"provider"`
	Value    string `json:"value"`
}

// TrackerDuplicateCriteria is the sanitized semantic duplicate-query projection.
type TrackerDuplicateCriteria struct {
	Name        string               `json:"name,omitempty"`
	ProviderIDs []TrackerProviderID  `json:"providerIds,omitempty"`
	Category    TrackerTaxonomyValue `json:"category"`
	Type        TrackerTaxonomyValue `json:"type"`
	Resolution  TrackerTaxonomyValue `json:"resolution"`
	Source      TrackerTaxonomyValue `json:"source"`
	Container   TrackerTaxonomyValue `json:"container"`
	Codecs      []string             `json:"codecs,omitempty"`
	Season      int                  `json:"season,omitempty"`
	Episode     int                  `json:"episode,omitempty"`
	Date        string               `json:"date,omitempty"`
}

// TrackerDuplicateTarget is the projection-bound proposed release used only
// for local duplicate policy evaluation.
type TrackerDuplicateTarget struct {
	Names         []string `json:"names,omitempty"`
	Category      string   `json:"category,omitempty"`
	Type          string   `json:"type,omitempty"`
	Source        string   `json:"source,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	ReleaseOrigin string   `json:"releaseOrigin,omitempty"`
	Resolution    string   `json:"resolution,omitempty"`
	Container     string   `json:"container,omitempty"`
	VideoCodec    string   `json:"videoCodec,omitempty"`
	VideoEncode   string   `json:"videoEncode,omitempty"`
	HDR           HDRFacts `json:"hdr"`
	Edition       string   `json:"edition,omitempty"`
	Region        string   `json:"region,omitempty"`
	ThreeD        string   `json:"threeD,omitempty"`
	Group         string   `json:"group,omitempty"`
	Repack        string   `json:"repack,omitempty"`
	Season        int      `json:"season,omitempty"`
	Episode       int      `json:"episode,omitempty"`
	Date          string   `json:"date,omitempty"`
	Pack          bool     `json:"pack"`
	SizeBytes     int64    `json:"sizeBytes,omitempty"`
	FileNames     []string `json:"fileNames,omitempty"`
}

// TrackerArtifactRequirements are backend-owned tracker artifact requirements.
type TrackerArtifactRequirements struct {
	// ScreenshotCount is the minimum number of selected normal screenshots.
	ScreenshotCount int `json:"screenshotCount"`
	// DVDMenuCount is a tracker-declared minimum, not an automatic-capture cap.
	DVDMenuCount      int      `json:"dvdMenuCount"`
	MediaInfo         bool     `json:"mediaInfo"`
	BDInfo            bool     `json:"bdInfo"`
	NFO               bool     `json:"nfo"`
	Description       bool     `json:"description"`
	ImageHosting      bool     `json:"imageHosting"`
	AllowedImageHosts []string `json:"allowedImageHosts,omitempty"`
	Torrent           bool     `json:"torrent"`
}

// TrackerQuestionnaireRequirement describes one non-secret tracker input requirement.
type TrackerQuestionnaireRequirement struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

// TrackerDerivedFlag is one non-secret finalized upload flag.
type TrackerDerivedFlag struct {
	Key   string `json:"key"`
	Value bool   `json:"value"`
}

// TrackerPolicyDecision is one deterministic tracker policy outcome.
type TrackerPolicyDecision struct {
	Code     string `json:"code"`
	Decision string `json:"decision"`
	Blocking bool   `json:"blocking"`
	Message  string `json:"message,omitempty"`
	// Disposition is the backend-owned execution effect of a failed rule.
	Disposition RuleDisposition `json:"disposition,omitempty"`
	// EvidenceStatus states how completely the backend could evaluate the rule.
	EvidenceStatus MetadataEvidenceStatus `json:"evidenceStatus,omitempty"`
}

// TrackerReleaseProjection is one exact tracker-local interpretation of a release.
type TrackerReleaseProjection struct {
	TrackerID            TrackerID                `json:"trackerId"`
	DisplayName          string                   `json:"displayName"`
	CanonicalReleaseName string                   `json:"canonicalReleaseName"`
	UploadReleaseName    string                   `json:"uploadReleaseName"`
	AdditionalNames      []TrackerReleaseName     `json:"additionalNames,omitempty"`
	Taxonomy             TrackerTaxonomy          `json:"taxonomy"`
	ProviderIDs          []TrackerProviderID      `json:"providerIds,omitempty"`
	DuplicateCriteria    TrackerDuplicateCriteria `json:"duplicateCriteria"`
	// DuplicateTarget is the proposed release consumed by local duplicate policy.
	DuplicateTarget TrackerDuplicateTarget `json:"duplicateTarget"`
	// DuplicatePolicyID identifies the tracker policy used for duplicate evaluation.
	DuplicatePolicyID string `json:"duplicatePolicyId"`
	// DuplicatePolicyFingerprint binds the policy inputs to this projection.
	DuplicatePolicyFingerprint WorkflowFingerprint `json:"duplicatePolicyFingerprint"`
	// DuplicateTargetFingerprint binds the proposed-release fields to this projection.
	DuplicateTargetFingerprint WorkflowFingerprint `json:"duplicateTargetFingerprint"`
	// DuplicateSearchFingerprint identifies the normalized search request.
	DuplicateSearchFingerprint WorkflowFingerprint `json:"duplicateSearchFingerprint"`
	NamingPolicyID             string              `json:"namingPolicyId"`
	// NamingElementPolicyVersion identifies the shared structural policy
	// applied before the tracker-local naming policy.
	NamingElementPolicyVersion string `json:"namingElementPolicyVersion"`
	// EpisodeTitleMode records the normalized structural decision included in
	// NamingFingerprint.
	EpisodeTitleMode            EpisodeTitleMode                  `json:"episodeTitleMode"`
	NamingFingerprint           WorkflowFingerprint               `json:"namingFingerprint"`
	PolicyDecisions             []TrackerPolicyDecision           `json:"policyDecisions,omitempty"`
	Artifacts                   TrackerArtifactRequirements       `json:"artifacts"`
	DescriptionGroup            string                            `json:"descriptionGroup,omitempty"`
	MetadataLocale              string                            `json:"metadataLocale,omitempty"`
	Questionnaire               []TrackerQuestionnaireRequirement `json:"questionnaire,omitempty"`
	DerivedFlags                []TrackerDerivedFlag              `json:"derivedFlags,omitempty"`
	InputFingerprint            WorkflowFingerprint               `json:"inputFingerprint"`
	CatalogFingerprint          WorkflowFingerprint               `json:"catalogFingerprint"`
	ConfigFingerprint           WorkflowFingerprint               `json:"configFingerprint"`
	ProjectorFingerprint        WorkflowFingerprint               `json:"projectorFingerprint"`
	CriteriaFingerprint         WorkflowFingerprint               `json:"criteriaFingerprint"`
	PreparedResourceFingerprint WorkflowFingerprint               `json:"preparedResourceFingerprint,omitempty"`
	Readiness                   ReadinessStatus                   `json:"readiness"`
	DupeReady                   bool                              `json:"dupeReady"`
	UploadReady                 bool                              `json:"uploadReady"`
	RequiredActions             []RequiredAction                  `json:"requiredActions,omitempty"`
	Failures                    []WorkflowFailure                 `json:"failures,omitempty"`
}

// TrackerReleaseProjectionSet is an immutable projection for every selected tracker.
type TrackerReleaseProjectionSet struct {
	ID                TrackerReleaseProjectionSetID            `json:"id"`
	WorkflowID        WorkflowID                               `json:"workflowId"`
	Revision          WorkflowRevision                         `json:"revision"`
	Release           ReleaseSnapshotRef                       `json:"release"`
	ReleaseRef        ReleaseRef                               `json:"releaseRef"`
	Catalog           TrackerCatalogSnapshotRef                `json:"catalog"`
	Runtime           TrackerRuntimeSnapshotRef                `json:"runtime"`
	Selection         TrackerSelectionRef                      `json:"selection"`
	Instructions      *TrackerProjectionInstructionSnapshotRef `json:"instructions,omitempty"`
	Preflight         *TrackerPreflightAssessmentRef           `json:"preflight,omitempty"`
	ExecutionMode     WorkflowExecutionMode                    `json:"executionMode,omitempty"`
	InputFingerprint  WorkflowFingerprint                      `json:"inputFingerprint"`
	PolicyFingerprint WorkflowFingerprint                      `json:"policyFingerprint"`
	Projections       []TrackerReleaseProjection               `json:"projections"`
	Status            StageStatus                              `json:"status"`
	RequiredActions   []RequiredAction                         `json:"requiredActions,omitempty"`
	Failures          []WorkflowFailure                        `json:"failures,omitempty"`
	CreatedAt         time.Time                                `json:"createdAt" ts_type:"string"`
}

// TrackerPreflightState identifies one time-sensitive tracker prerequisite.
type TrackerPreflightState string

const (
	// TrackerPreflightStateReady means all required live checks passed.
	TrackerPreflightStateReady TrackerPreflightState = "ready"
	// TrackerPreflightStateActionRequired means the owner must resolve an action.
	TrackerPreflightStateActionRequired TrackerPreflightState = "action_required"
	// TrackerPreflightStateRetryable means a transient check may be retried.
	TrackerPreflightStateRetryable TrackerPreflightState = "retryable"
	// TrackerPreflightStateFailed means a non-retryable prerequisite failed.
	TrackerPreflightStateFailed TrackerPreflightState = "failed"
	// TrackerPreflightStateExpired means retained live evidence is no longer fresh.
	TrackerPreflightStateExpired TrackerPreflightState = "expired"
)

// TrackerPreflightResult is one sanitized tracker live-readiness result.
type TrackerPreflightResult struct {
	TrackerID             TrackerID             `json:"trackerId"`
	State                 TrackerPreflightState `json:"state"`
	AuthReady             bool                  `json:"authReady"`
	ClaimsReady           bool                  `json:"claimsReady"`
	BannedGroupsReady     bool                  `json:"bannedGroupsReady"`
	RemoteMetadataReady   bool                  `json:"remoteMetadataReady"`
	ConfigFingerprint     WorkflowFingerprint   `json:"configFingerprint"`
	ProjectionFingerprint WorkflowFingerprint   `json:"projectionFingerprint"`
	RequiredActions       []RequiredAction      `json:"requiredActions,omitempty"`
	Failures              []WorkflowFailure     `json:"failures,omitempty"`
	AssessedAt            time.Time             `json:"assessedAt" ts_type:"string"`
	FreshUntil            time.Time             `json:"freshUntil" ts_type:"string"`
}

// TrackerPreflightAssessment retains live evidence for one projection revision.
type TrackerPreflightAssessment struct {
	ID               TrackerPreflightAssessmentID   `json:"id"`
	WorkflowID       WorkflowID                     `json:"workflowId"`
	Revision         WorkflowRevision               `json:"revision"`
	ProjectionSet    TrackerReleaseProjectionSetRef `json:"projectionSet"`
	Runtime          TrackerRuntimeSnapshotRef      `json:"runtime"`
	ExecutionMode    WorkflowExecutionMode          `json:"executionMode,omitempty"`
	InputFingerprint WorkflowFingerprint            `json:"inputFingerprint"`
	Results          []TrackerPreflightResult       `json:"results"`
	Status           StageStatus                    `json:"status"`
	CreatedAt        time.Time                      `json:"createdAt" ts_type:"string"`
	ExpiresAt        time.Time                      `json:"expiresAt" ts_type:"string"`
}

// DupeDecision is the retained owner decision for one duplicate result.
type DupeDecision string

const (
	// DupeDecisionPending means review has not resolved the result.
	DupeDecisionPending DupeDecision = "pending"
	// DupeDecisionAccepted means matching evidence blocks upload.
	DupeDecisionAccepted DupeDecision = "accepted"
	// DupeDecisionIgnored means the owner explicitly accepted upload risk.
	DupeDecisionIgnored DupeDecision = "ignored"
	// DupeDecisionNoMatch means no blocking duplicate exists.
	DupeDecisionNoMatch DupeDecision = "no_match"
	// DupeDecisionBypassed means debug authority waived a runtime policy gate.
	DupeDecisionBypassed DupeDecision = "bypassed"
	// DupeDecisionSkipped means backend policy skipped remote search.
	DupeDecisionSkipped DupeDecision = "skipped"
)

// DupeMatchProjection is sanitized duplicate evidence safe for public clients.
type DupeMatchProjection struct {
	ID             string            `json:"id,omitempty"`
	Name           string            `json:"name"`
	Link           string            `json:"link,omitempty"`
	SizeBytes      int64             `json:"sizeBytes,omitempty"`
	Flags          []string          `json:"flags,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	Relation       DupeRelation      `json:"relation,omitempty"`
	Reasons        []DupeReason      `json:"reasons,omitempty"`
	HDR            HDRFacts          `json:"hdr"`
	Category       string            `json:"category,omitempty"`
	Type           string            `json:"type,omitempty"`
	Resolution     string            `json:"resolution,omitempty"`
	Source         string            `json:"source,omitempty"`
	Codec          string            `json:"codec,omitempty"`
	Container      string            `json:"container,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Group          string            `json:"group,omitempty"`
	ReleaseOrigin  string            `json:"releaseOrigin,omitempty"`
	Edition        string            `json:"edition,omitempty"`
	Region         string            `json:"region,omitempty"`
	ThreeD         string            `json:"threeD,omitempty"`
	Repack         string            `json:"repack,omitempty"`
	Season         int               `json:"season,omitempty"`
	Episode        int               `json:"episode,omitempty"`
	Date           string            `json:"date,omitempty"`
	Pack           bool              `json:"pack"`
	Internal       bool              `json:"internal"`
	Trumpable      bool              `json:"trumpable"`
	EvidenceStatus HDREvidenceStatus `json:"evidenceStatus,omitempty"`
}

// TrackerDupeAssessment is one retained projection-bound duplicate result.
type TrackerDupeAssessment struct {
	TrackerID             TrackerID                `json:"trackerId"`
	UploadReleaseName     string                   `json:"uploadReleaseName"`
	ProjectionFingerprint WorkflowFingerprint      `json:"projectionFingerprint"`
	CriteriaFingerprint   WorkflowFingerprint      `json:"criteriaFingerprint"`
	Criteria              TrackerDuplicateCriteria `json:"criteria"`
	// TargetFingerprint binds the assessment to the proposed duplicate target.
	TargetFingerprint WorkflowFingerprint `json:"targetFingerprint"`
	// SearchFingerprint binds the assessment to the normalized search request.
	SearchFingerprint WorkflowFingerprint `json:"searchFingerprint"`
	// PolicyID identifies the duplicate policy that produced this result.
	PolicyID string `json:"policyId"`
	// PolicyFingerprint binds the assessment to the policy inputs.
	PolicyFingerprint WorkflowFingerprint `json:"policyFingerprint"`
	// EvidenceFingerprint identifies the retained search/evaluation evidence.
	EvidenceFingerprint WorkflowFingerprint `json:"evidenceFingerprint,omitempty"`
	// Search records safe completion and warning state for the tracker search.
	Search          DupeSearchEvidence    `json:"search"`
	Matches         []DupeMatchProjection `json:"matches,omitempty"`
	Decision        DupeDecision          `json:"decision"`
	Status          StageStatus           `json:"status"`
	RequiredActions []RequiredAction      `json:"requiredActions,omitempty"`
	Failures        []WorkflowFailure     `json:"failures,omitempty"`
	CheckedAt       time.Time             `json:"checkedAt" ts_type:"string"`
	FreshUntil      time.Time             `json:"freshUntil" ts_type:"string"`
}

// DupeAssessment retains duplicate results for exact projection and preflight revisions.
type DupeAssessment struct {
	ID               DupeAssessmentID               `json:"id"`
	WorkflowID       WorkflowID                     `json:"workflowId"`
	Revision         WorkflowRevision               `json:"revision"`
	Release          ReleaseSnapshotRef             `json:"release"`
	ReleaseRef       ReleaseRef                     `json:"releaseRef"`
	Selection        TrackerSelectionRef            `json:"selection"`
	ProjectionSet    TrackerReleaseProjectionSetRef `json:"projectionSet"`
	Preflight        *TrackerPreflightAssessmentRef `json:"preflight,omitempty"`
	InputFingerprint WorkflowFingerprint            `json:"inputFingerprint"`
	CheckOrdinal     uint8                          `json:"checkOrdinal,omitempty"`
	Results          []TrackerDupeAssessment        `json:"results"`
	Status           StageStatus                    `json:"status"`
	CreatedAt        time.Time                      `json:"createdAt" ts_type:"string"`
	ExpiresAt        time.Time                      `json:"expiresAt" ts_type:"string"`
}

// TrackerApprovalSnapshot is durable exact tracker authority for gated workflows.
type TrackerApprovalSnapshot struct {
	ID                  TrackerApprovalSnapshotID      `json:"id"`
	WorkflowID          WorkflowID                     `json:"workflowId"`
	Revision            WorkflowRevision               `json:"revision"`
	Release             ReleaseSnapshotRef             `json:"release"`
	Selection           TrackerSelectionRef            `json:"selection"`
	ProjectionSet       TrackerReleaseProjectionSetRef `json:"projectionSet"`
	Preflight           TrackerPreflightAssessmentRef  `json:"preflight"`
	Dupes               DupeAssessmentRef              `json:"dupes"`
	CandidateTrackerIDs []TrackerID                    `json:"candidateTrackerIds"`
	ApprovedTrackerIDs  []TrackerID                    `json:"approvedTrackerIds"`
	InputFingerprint    WorkflowFingerprint            `json:"inputFingerprint"`
	CreatedAt           time.Time                      `json:"createdAt" ts_type:"string"`
}

// MediaArtifactKind classifies one safe retained media artifact.
type MediaArtifactKind string

const (
	// MediaArtifactScreenshot identifies a captured screenshot.
	MediaArtifactScreenshot MediaArtifactKind = "screenshot"
	// MediaArtifactDVDMenu identifies an automatically or manually captured DVD menu.
	MediaArtifactDVDMenu MediaArtifactKind = "dvd_menu"
	// MediaArtifactHostedImage identifies a hosted image result.
	MediaArtifactHostedImage MediaArtifactKind = "hosted_image"
)

// MediaArtifact is a safe opaque projection of one retained private artifact.
type MediaArtifact struct {
	ID               PublicResourceID  `json:"id"`
	Kind             MediaArtifactKind `json:"kind"`
	Purpose          ScreenshotPurpose `json:"purpose"`
	Selected         bool              `json:"selected"`
	Order            int               `json:"order,omitempty"`
	Index            int               `json:"index,omitempty"`
	TimestampSeconds float64           `json:"timestampSeconds,omitempty"`
	Source           string            `json:"source,omitempty"`
	Width            int               `json:"width,omitempty"`
	Height           int               `json:"height,omitempty"`
	SizeBytes        int64             `json:"sizeBytes,omitempty"`
	Host             string            `json:"host,omitempty"`
	URL              string            `json:"url,omitempty"`
}

// MediaCaptureInstructions are transport-safe capture choices. Local paths
// and image bytes remain private workflow resources.
type MediaCaptureInstructions struct {
	ScreenshotCount int                   `json:"screenshotCount"`
	Purpose         ScreenshotPurpose     `json:"purpose"`
	Selections      []ScreenshotSelection `json:"selections,omitempty"`
	CaptureDVDMenus bool                  `json:"captureDvdMenus"`
	// MaxDVDMenuItems caps an explicitly requested automatic menu capture.
	MaxDVDMenuItems int `json:"maxDvdMenuItems,omitempty"`
}

// MediaArtifactSet retains generation- and projection-bound media results.
type MediaArtifactSet struct {
	ID                        MediaArtifactSetID             `json:"id"`
	WorkflowID                WorkflowID                     `json:"workflowId"`
	Revision                  WorkflowRevision               `json:"revision"`
	Release                   ReleaseSnapshotRef             `json:"release"`
	ReleaseRef                ReleaseRef                     `json:"releaseRef"`
	ProjectionSet             TrackerReleaseProjectionSetRef `json:"projectionSet"`
	TrackerApproval           *TrackerApprovalSnapshotRef    `json:"trackerApproval,omitempty"`
	CaptureFingerprint        WorkflowFingerprint            `json:"captureFingerprint"`
	RequirementsFingerprint   WorkflowFingerprint            `json:"requirementsFingerprint"`
	Artifacts                 []MediaArtifact                `json:"artifacts"`
	HostAttempts              []HostedImageAttempt           `json:"hostAttempts,omitempty"`
	FailedHosts               []string                       `json:"failedHosts,omitempty"`
	ImageRequirementsPrepared bool                           `json:"imageRequirementsPrepared"`
	Status                    StageStatus                    `json:"status"`
	RequiredActions           []RequiredAction               `json:"requiredActions,omitempty"`
	Failures                  []WorkflowFailure              `json:"failures,omitempty"`
	CreatedAt                 time.Time                      `json:"createdAt" ts_type:"string"`
}

// DescriptionOverrideInput is one explicit user-authored group description.
// Rendered output is generated and retained by the backend.
type DescriptionOverrideInput struct {
	GroupKey string `json:"groupKey"`
	Source   string `json:"source"`
}

// DescriptionInstructions are transport-safe generation choices. Canonical
// templates and generated output remain backend-owned workflow state.
type DescriptionInstructions struct {
	Overrides            []DescriptionOverrideInput      `json:"overrides,omitempty"`
	QuestionnaireAnswers map[TrackerID]map[string]string `json:"questionnaireAnswers,omitempty"`
	Options              UploadOptions                   `json:"options"`
	ImageHost            ImageHostOverrides              `json:"imageHost"`
	TrackerConfig        TrackerConfigOverrides          `json:"trackerConfig,omitempty"`
	TrackerSite          TrackerSiteOverrides            `json:"trackerSite,omitempty"`
	Client               ClientOverrides                 `json:"client,omitempty"`
	Torrent              TorrentOverrides                `json:"torrent,omitempty"`
	TemplateVersion      string                          `json:"templateVersion,omitempty"`
}

// RenderedDescription contains one retained description group projection.
type RenderedDescription struct {
	GroupKey           string              `json:"groupKey"`
	TrackerIDs         []TrackerID         `json:"trackerIds"`
	Source             string              `json:"source"`
	Rendered           string              `json:"rendered"`
	ContentFingerprint WorkflowFingerprint `json:"contentFingerprint"`
}

// DescriptionTrackerResult is one retained tracker-scoped description outcome.
type DescriptionTrackerResult struct {
	TrackerID TrackerID   `json:"trackerId"`
	Status    StageStatus `json:"status"`
	Message   string      `json:"message,omitempty"`
}

// DescriptionSet retains descriptions for exact release, projection, and media revisions.
type DescriptionSet struct {
	ID                  DescriptionSetID               `json:"id"`
	WorkflowID          WorkflowID                     `json:"workflowId"`
	Revision            WorkflowRevision               `json:"revision"`
	Release             ReleaseSnapshotRef             `json:"release"`
	ReleaseRef          ReleaseRef                     `json:"releaseRef"`
	ProjectionSet       TrackerReleaseProjectionSetRef `json:"projectionSet"`
	TrackerApproval     *TrackerApprovalSnapshotRef    `json:"trackerApproval,omitempty"`
	Media               *MediaArtifactSetRef           `json:"media,omitempty"`
	InputFingerprint    WorkflowFingerprint            `json:"inputFingerprint"`
	TemplateFingerprint WorkflowFingerprint            `json:"templateFingerprint"`
	Descriptions        []RenderedDescription          `json:"descriptions"`
	TrackerResults      []DescriptionTrackerResult     `json:"trackerResults,omitempty"`
	Status              StageStatus                    `json:"status"`
	RequiredActions     []RequiredAction               `json:"requiredActions,omitempty"`
	Failures            []WorkflowFailure              `json:"failures,omitempty"`
	CreatedAt           time.Time                      `json:"createdAt" ts_type:"string"`
}

// UploadPlanField is one ordered sanitized request field from a retained operation.
type UploadPlanField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// UploadPlanFile reports file attachment presence without exposing local paths.
type UploadPlanFile struct {
	Field   string `json:"field"`
	Present bool   `json:"present"`
}

// UploadPlanTracker is the sanitized semantic projection of one retained prepared operation.
type UploadPlanTracker struct {
	TrackerID              TrackerID            `json:"trackerId"`
	DisplayName            string               `json:"displayName"`
	UploadReleaseName      string               `json:"uploadReleaseName"`
	Taxonomy               TrackerTaxonomy      `json:"taxonomy"`
	DescriptionGroup       string               `json:"descriptionGroup,omitempty"`
	Endpoint               string               `json:"endpoint,omitempty"`
	Fields                 []UploadPlanField    `json:"fields,omitempty"`
	Files                  []UploadPlanFile     `json:"files,omitempty"`
	Eligible               bool                 `json:"eligible"`
	Status                 StageStatus          `json:"status,omitempty"`
	ClientInjectionStatus  StageStatus          `json:"clientInjectionStatus,omitempty"`
	ClientInjectionMessage string               `json:"clientInjectionMessage,omitempty"`
	Warnings               []string             `json:"warnings,omitempty"`
	RequiredActions        []RequiredAction     `json:"requiredActions,omitempty"`
	Failures               []WorkflowFailure    `json:"failures,omitempty"`
	PreparedOperationID    PublicResourceID     `json:"preparedOperationId,omitempty"`
	TorrentArtifactID      PublicResourceID     `json:"torrentArtifactId,omitempty"`
	TorrentFingerprint     WorkflowFingerprint  `json:"torrentFingerprint,omitempty"`
	ClientFailureCode      OperationFailureCode `json:"clientFailureCode,omitempty"`
	SemanticFingerprint    WorkflowFingerprint  `json:"semanticFingerprint"`
}

// UploadPlan is a safe projection of exact retained private prepared operations.
type UploadPlan struct {
	ID               UploadPlanID                   `json:"id"`
	WorkflowID       WorkflowID                     `json:"workflowId"`
	Revision         WorkflowRevision               `json:"revision"`
	Release          ReleaseSnapshotRef             `json:"release"`
	ReleaseRef       ReleaseRef                     `json:"releaseRef"`
	ProjectionSet    TrackerReleaseProjectionSetRef `json:"projectionSet"`
	Dupes            DupeAssessmentRef              `json:"dupes"`
	TrackerApproval  *TrackerApprovalSnapshotRef    `json:"trackerApproval,omitempty"`
	Media            *MediaArtifactSetRef           `json:"media,omitempty"`
	Descriptions     *DescriptionSetRef             `json:"descriptions,omitempty"`
	InputFingerprint WorkflowFingerprint            `json:"inputFingerprint"`
	Trackers         []UploadPlanTracker            `json:"trackers"`
	Status           StageStatus                    `json:"status"`
	SingleUse        bool                           `json:"singleUse"`
	CreatedAt        time.Time                      `json:"createdAt" ts_type:"string"`
	ExpiresAt        time.Time                      `json:"expiresAt" ts_type:"string"`
}

// UploadTrackerResult is one safe immutable tracker submission outcome.
type UploadTrackerResult struct {
	TrackerID              TrackerID            `json:"trackerId"`
	Status                 StageStatus          `json:"status"`
	SubmissionStatus       StageStatus          `json:"submissionStatus,omitempty"`
	ClientInjectionStatus  StageStatus          `json:"clientInjectionStatus,omitempty"`
	ClientInjectionMessage string               `json:"clientInjectionMessage,omitempty"`
	ClientFailureCode      OperationFailureCode `json:"clientFailureCode,omitempty"`
	RemoteID               string               `json:"remoteId,omitempty"`
	RemoteURL              string               `json:"remoteUrl,omitempty"`
	ClientInjected         bool                 `json:"clientInjected"`
	CrossSeeded            bool                 `json:"crossSeeded"`
	Failures               []WorkflowFailure    `json:"failures,omitempty"`
}

// UploadResult retains terminal tracker outcomes bound to exact workflow inputs.
type UploadResult struct {
	ID               UploadResultID                 `json:"id"`
	WorkflowID       WorkflowID                     `json:"workflowId"`
	Revision         WorkflowRevision               `json:"revision"`
	ProjectionSet    TrackerReleaseProjectionSetRef `json:"projectionSet"`
	Dupes            DupeAssessmentRef              `json:"dupes"`
	TrackerApproval  *TrackerApprovalSnapshotRef    `json:"trackerApproval,omitempty"`
	Media            MediaArtifactSetRef            `json:"media"`
	Descriptions     DescriptionSetRef              `json:"descriptions"`
	InputFingerprint WorkflowFingerprint            `json:"inputFingerprint"`
	Results          []UploadTrackerResult          `json:"results"`
	Status           StageStatus                    `json:"status"`
	CreatedAt        time.Time                      `json:"createdAt" ts_type:"string"`
}

// ReleaseWorkflow is the revisioned aggregate of immutable stage references.
type ReleaseWorkflow struct {
	ID                     WorkflowID                               `json:"id"`
	Revision               WorkflowRevision                         `json:"revision"`
	FactInstructions       ReleaseFactInstructionSnapshotRef        `json:"factInstructions"`
	Release                *ReleaseSnapshotRef                      `json:"release,omitempty"`
	TrackerCatalog         *TrackerCatalogSnapshotRef               `json:"trackerCatalog,omitempty"`
	TrackerRuntime         *TrackerRuntimeSnapshotRef               `json:"trackerRuntime,omitempty"`
	Selection              *TrackerSelectionRef                     `json:"selection,omitempty"`
	ProjectionInstructions *TrackerProjectionInstructionSnapshotRef `json:"projectionInstructions,omitempty"`
	TrackerProjections     *TrackerReleaseProjectionSetRef          `json:"trackerProjections,omitempty"`
	TrackerPreflight       *TrackerPreflightAssessmentRef           `json:"trackerPreflight,omitempty"`
	Dupes                  *DupeAssessmentRef                       `json:"dupes,omitempty"`
	TrackerApproval        *TrackerApprovalSnapshotRef              `json:"trackerApproval,omitempty"`
	Media                  *MediaArtifactSetRef                     `json:"media,omitempty"`
	Descriptions           *DescriptionSetRef                       `json:"descriptions,omitempty"`
	DryRun                 *UploadDryRunResultRef                   `json:"dryRun,omitempty"`
	UploadResult           *UploadResultRef                         `json:"uploadResult,omitempty"`
	Status                 WorkflowStatus                           `json:"status"`
	RequiredActions        []RequiredAction                         `json:"requiredActions,omitempty"`
	Failures               []WorkflowFailure                        `json:"failures,omitempty"`
	CreatedAt              time.Time                                `json:"createdAt" ts_type:"string"`
	UpdatedAt              time.Time                                `json:"updatedAt" ts_type:"string"`
}

// ReleaseWorkflowCurrent is the complete safe current projection returned to
// every workflow adapter. Private execution resources never appear here.
type ReleaseWorkflowCurrent struct {
	Workflow               ReleaseWorkflow                       `json:"workflow"`
	FactInstructions       *ReleaseFactInstructionSnapshot       `json:"factInstructions,omitempty"`
	Release                *ReleaseSnapshot                      `json:"release,omitempty"`
	Catalog                *TrackerCatalogSnapshot               `json:"catalog,omitempty"`
	Runtime                *TrackerRuntimeSnapshot               `json:"runtime,omitempty"`
	Selection              *TrackerSelection                     `json:"selection,omitempty"`
	ProjectionInstructions *TrackerProjectionInstructionSnapshot `json:"projectionInstructions,omitempty"`
	Projections            *TrackerReleaseProjectionSet          `json:"projections,omitempty"`
	Preflight              *TrackerPreflightAssessment           `json:"preflight,omitempty"`
	Dupes                  *DupeAssessment                       `json:"dupes,omitempty"`
	TrackerApproval        *TrackerApprovalSnapshot              `json:"trackerApproval,omitempty"`
	Media                  *MediaArtifactSet                     `json:"media,omitempty"`
	Descriptions           *DescriptionSet                       `json:"descriptions,omitempty"`
	DryRun                 *UploadDryRunResult                   `json:"dryRun,omitempty"`
	UploadResult           *UploadResult                         `json:"uploadResult,omitempty"`
	Operation              *WorkflowOperationStatus              `json:"operation,omitempty"`
	Continuation           WorkflowContinuation                  `json:"continuation"`
}

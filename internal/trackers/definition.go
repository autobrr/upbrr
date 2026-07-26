// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// ErrSubmitted2FARejected marks tracker auth failure after a supplied manual 2FA code was rejected.
var ErrSubmitted2FARejected = errors.New("trackers: submitted 2FA rejected")

// AuthResolutionError reports tracker-owned remote auth classification to the generic coordinator.
type AuthResolutionError struct {
	// Reason is sanitized operator-facing failure detail.
	Reason string
	// AuthRequired reports that configured or interactive authentication is needed.
	AuthRequired bool
	// ConfirmedInvalid reports that existing authentication was rejected remotely.
	ConfirmedInvalid bool
	// Transient reports that retrying may succeed without changing credentials.
	Transient bool
	// Err retains the underlying diagnostic cause for errors.Is and errors.As.
	Err error
}

// Error returns the underlying cause text when available, otherwise the public reason.
func (e *AuthResolutionError) Error() string {
	if e == nil {
		return "tracker auth resolution failed"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Reason
}

// Unwrap exposes the diagnostic cause.
func (e *AuthResolutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// PreparationInput supplies one immutable, operation-scoped tracker preparation snapshot.
// The tracker module resolves this input before invoking an adapter.
type PreparationInput struct {
	// Intent selects the maximum preparation depth for this invocation.
	Intent PreparationIntent
	// ExecutionMode controls projection-time policy waivers. Empty means normal.
	ExecutionMode api.WorkflowExecutionMode
	// Tracker is the normalized tracker name receiving the upload.
	Tracker string
	// Meta is the prepared release snapshot used throughout this upload attempt.
	// For dry-run and upload preparation, TorrentPath is the exact tracker artifact;
	// adapters must not replace it with client, source, or generic torrent paths.
	Meta api.UploadSubject
	// Projection locks reviewed tracker-local names and taxonomy for final preparation.
	Projection *api.TrackerReleaseProjection
	// RequestedUploadName is an optional user instruction consumed by the
	// tracker naming policy before projection. A non-nil empty value is invalid.
	RequestedUploadName *string
	// AdditionalReleaseNames contains typed user-supplied secondary names that
	// are normalized and fingerprinted with the tracker naming result.
	AdditionalReleaseNames []api.TrackerReleaseName
	// TrackerConfig is the effective configuration for Tracker.
	TrackerConfig config.TrackerConfig
	// Runtime contains the deliberately projected non-tracker configuration needed by adapters.
	Runtime PreparationRuntime
	// Logger receives tracker workflow progress and diagnostics.
	Logger api.Logger
	// Assets contains pre-resolved description text and selected images, when available.
	Assets *DescriptionAssets
	// SelectedImageHost is the module-resolved image target for adapter-specific assets.
	SelectedImageHost string
	// UploadImages uploads to SelectedImageHost without exposing the generic image service.
	UploadImages func(context.Context, []api.ScreenshotImage) ([]api.UploadedImageLink, error)
}

// PreparationRuntime is a narrow immutable projection of application settings used during preparation.
type PreparationRuntime struct {
	// DBPath is the host filesystem database path used to resolve managed artifacts and sessions.
	DBPath string
	// Description contains projected description-layout settings only.
	Description config.DescriptionSettingsConfig
	// Internal reports whether the release group is internal for the target tracker.
	Internal bool
	// BTNAPIToken is the resolved BTN API credential used only by the BTN adapter.
	BTNAPIToken string
}

// PreparationRuntimeFromConfig projects protocol-test configuration into the narrow runtime value.
func PreparationRuntimeFromConfig(cfg config.Config) PreparationRuntime {
	return PreparationRuntime{
		DBPath:      cfg.MainSettings.DBPath,
		Description: cfg.Description,
		BTNAPIToken: config.ResolveBTNAPIToken(cfg),
	}
}

// DescriptionConfig returns a configuration containing only projected description layout settings.
func (r PreparationRuntime) DescriptionConfig() config.Config {
	return config.Config{Description: r.Description}
}

// Definition is the required preparation contract for a registered tracker.
type Definition interface {
	// Name returns the stable normalized tracker identifier.
	Name() string
	// Prepare creates a fresh operation-scoped plan for the requested intent.
	Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure)
}

// ReleaseNameInput contains the immutable values available to one pure
// tracker-owned release-name policy.
type ReleaseNameInput struct {
	Subject       api.UploadSubject
	TrackerConfig config.TrackerConfig
	RequestedName *string
}

// ResolvedReleaseNames contains tracker-facing principal and secondary names.
// Duplicate defaults to Upload when empty.
type ResolvedReleaseNames struct {
	Upload     string
	Duplicate  string
	Additional []api.TrackerReleaseName
}

// ReleaseNamePolicy resolves tracker-facing names without I/O or mutable state.
type ReleaseNamePolicy func(ReleaseNameInput) (ResolvedReleaseNames, error)

// ReleaseNamePolicyBinding identifies one versioned naming implementation.
type ReleaseNamePolicyBinding struct {
	ID       string
	Resolver ReleaseNamePolicy
}

// ReleaseNamePolicyProvider declares tracker-owned release-name behavior.
type ReleaseNamePolicyProvider interface {
	// ReleaseNamePolicy returns one required pure, versioned naming binding.
	ReleaseNamePolicy() ReleaseNamePolicyBinding
}

// FamilyProvider declares a tracker's protocol family.
type FamilyProvider interface {
	// TrackerFamily returns the tracker's protocol family.
	TrackerFamily() Family
}

// BaseURLProvider declares a tracker's default endpoint.
type BaseURLProvider interface {
	// DefaultBaseURL returns the tracker endpoint used when configuration supplies none.
	DefaultBaseURL() string
}

// LocalizedMetadataProvider declares a locale consumed by tracker-owned naming or description behavior.
type LocalizedMetadataProvider interface {
	// LocalizedMetadataLocale returns the locale used for tracker-owned metadata rendering.
	LocalizedMetadataLocale() string
}

// DescriptionGroupProvider declares a tracker-specific description override group.
type DescriptionGroupProvider interface {
	// DescriptionGroup returns the tracker-specific description override group.
	DescriptionGroup() string
}

// AuthSessionResolver validates or refreshes tracker-owned auth material.
type AuthSessionResolver func(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) error

// AuthSessionProvider declares tracker-owned remote auth behavior.
type AuthSessionProvider interface {
	// AuthSessionResolver returns the tracker-owned session validation and refresh operation.
	AuthSessionResolver() AuthSessionResolver
}

// AuthCapabilityProvider declares tracker-owned auth support metadata.
type AuthCapabilityProvider interface {
	// AuthCapability describes supported tracker authentication interactions.
	AuthCapability() api.TrackerAuthCapability
}

// AuthCapabilityDescriptorProvider declares optional tracker auth metadata
// without using a zero-valued capability as an absence sentinel.
type AuthCapabilityDescriptorProvider interface {
	// AuthCapabilityDescriptor returns nil when the tracker has no configurable auth capability.
	AuthCapabilityDescriptor() *api.TrackerAuthCapability
}

// AuthRequirement identifies one secret-free fact needed for tracker auth.
type AuthRequirement string

const (
	// AuthRequirementAPIKey requires an effective tracker API key.
	AuthRequirementAPIKey AuthRequirement = "api_key"
	// AuthRequirementPasskey requires a tracker passkey or equivalent announce key.
	AuthRequirementPasskey AuthRequirement = "passkey"
	// AuthRequirementStoredCookie requires a persisted tracker session cookie.
	AuthRequirementStoredCookie AuthRequirement = "stored_cookie"
	// AuthRequirementCredentialLogin requires configured username/password login.
	AuthRequirementCredentialLogin AuthRequirement = "credential_login" //nolint:gosec // Requirement label, not a credential.
	// AuthRequirementUsername requires the tracker username independently of login support.
	AuthRequirementUsername AuthRequirement = "username"
	// AuthRequirementAPIUser requires a tracker API user independently of its API key.
	AuthRequirementAPIUser AuthRequirement = "api_user"
	// AuthRequirementAnnounceURL requires a personal announce URL.
	AuthRequirementAnnounceURL AuthRequirement = "announce_url"
)

// AuthRequirementAlternative is one complete set of auth facts. Alternatives
// are ORed; requirements within AllOf are ANDed.
type AuthRequirementAlternative struct {
	AllOf []AuthRequirement
}

// EffectiveAuthRequirements describes the auth facts for the effective
// tracker mode without exposing credential values.
type EffectiveAuthRequirements struct {
	Mode         string
	Alternatives []AuthRequirementAlternative
	Supports2FA  bool
}

// Clone returns an independent requirements value.
func (r EffectiveAuthRequirements) Clone() EffectiveAuthRequirements {
	clone := r
	clone.Alternatives = make([]AuthRequirementAlternative, len(r.Alternatives))
	for idx := range r.Alternatives {
		clone.Alternatives[idx].AllOf = slices.Clone(r.Alternatives[idx].AllOf)
	}
	return clone
}

// AuthRequirementsResolver returns secret-free requirements for effective
// application and tracker config.
type AuthRequirementsResolver func(config.Config, config.TrackerConfig) EffectiveAuthRequirements

// AuthPolicy declares coordinator behavior that cannot be inferred from the
// user-facing auth capability alone.
type AuthPolicy struct {
	// ResolveRequirements returns the secret-free auth facts for the effective
	// tracker mode.
	ResolveRequirements AuthRequirementsResolver
	// ResolveAPIKey returns the effective API credential, including any legacy
	// config source still supported by the owning tracker.
	ResolveAPIKey func(config.Config, config.TrackerConfig) string
	// APIKeyRequiresUploadSession keeps API-only search auth separate from
	// cookie/login-based upload readiness.
	APIKeyRequiresUploadSession bool
	// CookieCompletesAPIKeyAuth promotes API-key plus stored-cookie auth to configured state.
	CookieCompletesAPIKeyAuth bool
	// MissingAPIKeyMessage explains a separate API prerequisite after session auth succeeds.
	MissingAPIKeyMessage string
	// UploadSessionMissingMessage explains why an API key alone is not upload-ready.
	UploadSessionMissingMessage string
	// LoginRequiresAnnounceURL requires a personal announce URL in addition to credentials.
	LoginRequiresAnnounceURL bool
	// PasskeyCoversAuth allows a passkey alone to satisfy auth readiness.
	PasskeyCoversAuth bool
	// PasskeyRequiresUsername requires username alongside the passkey.
	PasskeyRequiresUsername bool
	// PasskeyRequiresCookie requires a validated stored cookie alongside passkey credentials.
	PasskeyRequiresCookie bool
}

// AuthPolicyProvider declares tracker-owned auth coordinator policy.
type AuthPolicyProvider interface {
	// AuthPolicy returns tracker-specific auth readiness semantics.
	AuthPolicy() *AuthPolicy
}

// AuthStateSnapshot restores tracker-owned auth state after a later deletion step fails.
type AuthStateSnapshot interface {
	// Restore performs best-effort rollback independent of caller cancellation.
	Restore(context.Context) error
}

// AuthStateManager owns tracker-specific persisted auth material outside generic cookies.
type AuthStateManager interface {
	// Snapshot captures state needed to roll back a multi-step delete.
	Snapshot(context.Context, string) (AuthStateSnapshot, error)
	// Delete removes tracker-owned persisted auth state.
	Delete(context.Context, string) error
}

// AuthStateManagerProvider declares tracker-owned persisted auth cleanup.
type AuthStateManagerProvider interface {
	// AuthStateManager returns the tracker-specific auth state manager.
	AuthStateManager() AuthStateManager
}

// RuleProvider declares tracker-owned validation rules.
type RuleProvider interface {
	// Rules returns tracker-owned release validation rules.
	Rules() *RuleSet
}

// ValidationPolicyProvider declares tracker-owned, side-effect-free
// constructibility and custom policy validation.
type ValidationPolicyProvider interface {
	// ValidationPolicy returns one versioned validation binding.
	ValidationPolicy() ValidationPolicyBinding
}

// ArtifactPolicy declares tracker-owned torrent artifact constraints.
type ArtifactPolicy struct {
	// MaxPieceSizeMiB is the largest permitted torrent piece size; zero imposes no limit.
	MaxPieceSizeMiB int
	// MaxTorrentBytes is the largest permitted encoded torrent size; zero imposes no limit.
	MaxTorrentBytes int64
}

// ArtifactPolicyProvider declares tracker-owned torrent artifact policy.
type ArtifactPolicyProvider interface {
	// ArtifactPolicy returns tracker-owned torrent artifact limits.
	ArtifactPolicy() *ArtifactPolicy
}

// DataLookupRequest contains tracker metadata lookup inputs.
type DataLookupRequest struct {
	// TrackerID is the tracker-side torrent or release identifier when already known.
	TrackerID string
	// Meta is the prepared release whose tracker metadata is requested.
	Meta api.UploadSubject
	// SearchName overrides the release name used for tracker search when non-empty.
	SearchName string
	// OnlyID limits lookup work to resolving tracker identity where supported.
	OnlyID bool
	// KeepImages requests preservation of images found in tracker descriptions.
	KeepImages bool
}

// DataLookup resolves tracker-owned metadata for a release.
type DataLookup interface {
	// Lookup resolves tracker-owned metadata for one prepared release.
	Lookup(ctx context.Context, req DataLookupRequest) (DataLookupResult, error)
}

// DataLookupFactory constructs a tracker-owned lookup from runtime deps.
type DataLookupFactory interface {
	// NewDataLookup constructs a lookup bound to runtime configuration and diagnostics.
	NewDataLookup(cfg config.Config, httpClient *http.Client, logger api.Logger) DataLookup
}

// DataLookupConfigProvider validates tracker-data lookup credentials.
type DataLookupConfigProvider interface {
	// DataLookupConfigured reports whether required lookup credentials are present.
	DataLookupConfigured(cfg config.Config) bool
}

// DataLookupPolicy declares tracker-specific lookup orchestration behavior.
type DataLookupPolicy struct {
	// Cooldown is the minimum delay applied around tracker lookup operations.
	Cooldown time.Duration
	// DeferWhenCollectingImages postpones lookup while the caller is still collecting images.
	DeferWhenCollectingImages bool
}

// DataLookupPolicyProvider declares tracker-owned lookup orchestration policy.
type DataLookupPolicyProvider interface {
	// DataLookupPolicy returns tracker-specific lookup orchestration settings.
	DataLookupPolicy() *DataLookupPolicy
}

// BannedGroupsProvider declares tracker-owned static banned release groups.
type BannedGroupsProvider interface {
	// BannedGroups returns the tracker's static banned release-group list.
	BannedGroups() []string
}

// BannedGroupPolicy declares a tracker-owned dynamic blacklist source.
type BannedGroupPolicy struct {
	// EndpointPath is appended to the configured tracker base URL.
	EndpointPath string
	// DefaultEndpoint is used when tracker configuration supplies no base URL.
	DefaultEndpoint string
	// TRaSHGuideURL supplies an optional external banned-group source.
	TRaSHGuideURL string
	// RequireAPIKey disables remote refresh when no API key is configured.
	RequireAPIKey bool
	// RawAPIKeyFallback allows the configured APIKey field when no specialized key exists.
	RawAPIKeyFallback bool
}

// BannedGroupPolicyProvider declares dynamic banned-group retrieval behavior.
type BannedGroupPolicyProvider interface {
	// BannedGroupPolicy returns dynamic banned-group retrieval settings.
	BannedGroupPolicy() *BannedGroupPolicy
}

// MetadataPolicyProvider declares tracker-owned metadata requirements.
type MetadataPolicyProvider interface {
	// MetadataPolicy returns tracker-owned metadata requirements.
	MetadataPolicy() *TrackerMetadataPolicy
}

// UploadArtifactPolicy declares tracker torrent personalization fields.
type UploadArtifactPolicy struct {
	// Source replaces the torrent info dictionary's private-tracker source field.
	Source string
	// DefaultAnnounce is used when tracker configuration has no announce URL.
	DefaultAnnounce string
	// UseMyAnnounce selects the tracker configuration's personal announce URL.
	UseMyAnnounce bool
	// RequireAnnounce prevents artifact preparation without an announce URL.
	RequireAnnounce bool
}

// UploadArtifactPolicyProvider declares tracker-owned personalization policy.
type UploadArtifactPolicyProvider interface {
	// UploadArtifactPolicy returns tracker torrent personalization settings.
	UploadArtifactPolicy() *UploadArtifactPolicy
}

// DupePolicy declares tracker-specific duplicate comparison semantics.
type DupePolicy struct {
	// DolbyVisionImpliesHDR treats Dolby Vision candidates as HDR during matching.
	DolbyVisionImpliesHDR bool
	// MatchAggregateSize compares aggregate file size rather than a single-file size.
	MatchAggregateSize bool
	// ContainsFilenameMatch permits containment-based filename comparison.
	ContainsFilenameMatch bool
	// NormalizeMTVName applies MTV-specific release-name normalization.
	NormalizeMTVName bool
	// TrackTrumpableID preserves a matched tracker ID for trumpability checks.
	TrackTrumpableID bool
	// MatchDVDReleaseGroup includes DVD release-group identity in matching.
	MatchDVDReleaseGroup bool
	// RequireReleaseGroup rejects candidates without a comparable release group.
	RequireReleaseGroup bool
	// RejectEpisodeResolutionMismatch blocks episode candidates at a different resolution.
	RejectEpisodeResolutionMismatch bool
	// NormalizeDDPlusName normalizes Dolby Digital Plus naming variants.
	NormalizeDDPlusName bool
	// SDMatchesHD permits standard-definition metadata to match high-definition candidates.
	SDMatchesHD bool
	// CompareDVDResolution includes DVD resolution in candidate comparison.
	CompareDVDResolution bool
	// AllowSizeVariance1080 enables the tracker-specific 1080p size tolerance.
	AllowSizeVariance1080 bool
}

// DupePolicyProvider declares tracker-owned duplicate comparison policy.
type DupePolicyProvider interface {
	// DupePolicy returns tracker-specific duplicate comparison settings.
	DupePolicy() *DupePolicy
}

// AudioPolicy declares tracker-specific multi-language upload constraints.
type AudioPolicy struct {
	// AllowBloat accepts additional non-original audio languages without warning.
	AllowBloat bool
	// AllowedLanguages contains normalized languages accepted for foreign audio.
	AllowedLanguages []string
	// BlockEnglishOriginalWithForeign rejects foreign tracks when English is original audio.
	BlockEnglishOriginalWithForeign bool
}

// AudioPolicyProvider declares tracker-owned audio constraints.
type AudioPolicyProvider interface {
	// AudioPolicy returns tracker-specific audio-language restrictions.
	AudioPolicy() *AudioPolicy
}

// ImageHostPolicy declares tracker-owned accepted image hosts and activation gates.
type ImageHostPolicy struct {
	// AllowedHosts lists normalized image hosts accepted in descriptions.
	AllowedHosts []string
	// OwnedHosts lists private upload hosts scoped to this tracker.
	OwnedHosts []string
	// DisableWithoutRehost disables the policy unless image rehosting is enabled.
	DisableWithoutRehost bool
	// DisableWithoutAPI disables the policy unless tracker image API credentials exist.
	DisableWithoutAPI bool
	// ConditionalHost is enabled only when its associated runtime condition is met.
	ConditionalHost string
	// EnableWithImageHosting enables ConditionalHost through global image-hosting settings.
	EnableWithImageHosting bool
}

// ImageHostPolicyProvider declares tracker-owned image-host restrictions.
type ImageHostPolicyProvider interface {
	// ImageHostPolicy returns accepted host and activation settings.
	ImageHostPolicy() *ImageHostPolicy
}

// TorrentSearchPreference identifies tracker-owned torrent reuse selection behavior.
type TorrentSearchPreference string

const (
	// TorrentSearchPreferenceNone applies the global torrent reuse preference.
	TorrentSearchPreferenceNone TorrentSearchPreference = ""
	// TorrentSearchPreferenceSmallPieces prefers the smallest reusable torrent pieces.
	TorrentSearchPreferenceSmallPieces TorrentSearchPreference = "small_pieces"
)

// TorrentIdentityPolicy declares how torrent-client metadata identifies a tracker.
type TorrentIdentityPolicy struct {
	// TrackerURLPatterns match announce URLs reported by torrent clients.
	TrackerURLPatterns []string
	// CommentURLPatterns match tracker detail URLs in torrent comments.
	CommentURLPatterns []string
	// DetailIDPattern extracts a tracker torrent ID from a matching comment.
	DetailIDPattern string
	// WorkingTrackerID supplies a stable synthetic ID when a working announce URL is sufficient.
	WorkingTrackerID string
	// SearchPreference selects optional tracker-owned torrent reuse behavior.
	SearchPreference TorrentSearchPreference
	// InferMatchFromResolvedID treats a resolved tracker ID as provenance even
	// when no announce URL was available in the matched torrent response.
	InferMatchFromResolvedID bool
}

// TorrentIdentityPolicyProvider declares torrent-client identity and search behavior.
type TorrentIdentityPolicyProvider interface {
	// TorrentIdentityPolicy returns tracker-owned torrent identity patterns and search behavior.
	TorrentIdentityPolicy() *TorrentIdentityPolicy
}

// ClaimChecker evaluates tracker-owned active-claim rules.
type ClaimChecker interface {
	// HasClaim reports whether an active tracker claim blocks this release.
	HasClaim(ctx context.Context, meta api.UploadSubject) (bool, error)
	// FailureReason returns sanitized operator-facing text for a positive claim.
	FailureReason(meta api.UploadSubject) string
}

// ClaimCheckerFactory constructs a tracker-owned claim checker.
type ClaimCheckerFactory interface {
	// NewClaimChecker constructs a tracker-owned claim checker.
	NewClaimChecker(cfg config.Config, logger api.Logger) ClaimChecker
}

// ClaimPolicy declares generic claim orchestration required by a tracker.
type ClaimPolicy struct {
	// APIBacked reports that claim evaluation requires a remote tracker lookup.
	APIBacked bool
}

// ClaimPolicyProvider declares tracker-owned claim orchestration policy.
type ClaimPolicyProvider interface {
	// ClaimPolicy returns generic claim-orchestration requirements.
	ClaimPolicy() *ClaimPolicy
}

// WorkflowMediaRequirements declares genuine tracker-owned media minimums.
// Capture preferences and global caps do not belong here.
type WorkflowMediaRequirements struct {
	// DVDMenuCount is the minimum selected DVD-menu image count.
	DVDMenuCount int
}

// WorkflowMediaRequirementsProvider declares tracker-owned workflow media minimums.
type WorkflowMediaRequirementsProvider interface {
	// WorkflowMediaRequirements returns the tracker's explicit media minimums.
	WorkflowMediaRequirements() *WorkflowMediaRequirements
}

// Descriptor binds a tracker definition to its optional capabilities.
type Descriptor struct {
	// Name is the stable normalized tracker identifier.
	Name string
	// DisplayName is mutable presentation text separate from stable identity.
	DisplayName string
	// Aliases contains explicit legacy identities accepted during migration.
	Aliases []string
	// ProjectorVersion identifies the naming/taxonomy implementation contract.
	ProjectorVersion string
	// Family identifies the tracker protocol family.
	Family Family
	// BaseURL is the tracker's default endpoint.
	BaseURL string
	// Definition is the required preparation adapter.
	Definition Definition
	// ReleaseNamePolicy owns the tracker-local upload and duplicate-search names.
	ReleaseNamePolicy ReleaseNamePolicyBinding
	// UploadContentMode identifies the shared content object consumed before preparation.
	UploadContentMode UploadContentMode
	// WorkflowMedia contains explicit tracker-owned media minimums.
	WorkflowMedia *WorkflowMediaRequirements
	// Rules contains optional tracker-owned validation rules.
	Rules *RuleSet
	// Validation contains the versioned tracker-native validation policy.
	Validation ValidationPolicyBinding
	// Artifact contains optional generic torrent limits.
	Artifact *ArtifactPolicy
	// DataFactory constructs optional tracker metadata lookup support.
	DataFactory DataLookupFactory
	// DataPolicy contains optional lookup orchestration settings.
	DataPolicy *DataLookupPolicy
	// BannedGroups is the static banned release-group list.
	BannedGroups []string
	// BannedPolicy contains optional dynamic banned-group retrieval settings.
	BannedPolicy *BannedGroupPolicy
	// Metadata contains optional metadata requirements.
	Metadata *TrackerMetadataPolicy
	// UploadArtifact contains optional torrent personalization settings.
	UploadArtifact *UploadArtifactPolicy
	// DupePolicy contains optional duplicate comparison settings.
	DupePolicy *DupePolicy
	// AudioPolicy contains optional audio-language restrictions.
	AudioPolicy *AudioPolicy
	// ImageHost contains optional accepted-host restrictions.
	ImageHost *ImageHostPolicy
	// TorrentIdentity contains optional torrent-client identity and search behavior.
	TorrentIdentity *TorrentIdentityPolicy
	// ClaimFactory constructs optional claim checking support.
	ClaimFactory ClaimCheckerFactory
	// ClaimPolicy contains optional generic claim orchestration settings.
	ClaimPolicy *ClaimPolicy
	// AuthResolver performs optional tracker-owned auth resolution.
	AuthResolver AuthSessionResolver
	// AuthCapability describes optional interactive auth support.
	AuthCapability *api.TrackerAuthCapability
	// AuthPolicy contains optional tracker-owned auth readiness semantics.
	AuthPolicy *AuthPolicy
	// AuthStateManager owns optional tracker-specific persisted auth cleanup.
	AuthStateManager AuthStateManager
	// MetadataLocale is the optional locale for tracker-owned rendering.
	MetadataLocale string
	// DescriptionGroup is the optional tracker-specific description override group.
	DescriptionGroup string
}

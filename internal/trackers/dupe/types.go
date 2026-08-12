// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"context"
	"maps"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Disposition identifies whether a tracker search resolved, did not run, or failed.
type Disposition uint8

const (
	// DispositionInvalid is the zero value and represents a malformed adapter result.
	DispositionInvalid Disposition = iota
	// DispositionResolved indicates that tracker search completed with usable evidence.
	DispositionResolved
	// DispositionNotRun indicates an intentional structurally classified skip.
	DispositionNotRun
	// DispositionFailed indicates that attempted tracker work failed.
	DispositionFailed
)

// Stable not-run codes.
const (
	// NotRunUserRequested indicates that the caller explicitly skipped the search.
	NotRunUserRequested = "user_requested"
	// NotRunBannedGroup indicates a policy-blocked release group.
	NotRunBannedGroup = "banned_group"
	// NotRunAuthNotReady indicates that tracker authentication is not usable.
	NotRunAuthNotReady = "auth_not_ready"
	// NotRunMissingCredentials indicates required tracker credentials are absent.
	NotRunMissingCredentials = "missing_credentials"
	// NotRunMissingMetadata indicates required metadata was unavailable.
	NotRunMissingMetadata = "missing_metadata"
	// NotRunUnsupportedContent indicates the tracker does not support the content.
	NotRunUnsupportedContent = "unsupported_content"
	// NotRunManualCheckRequired indicates that policy requires manual review.
	NotRunManualCheckRequired = "manual_check_required"
	// NotRunNotImplemented indicates that this tracker operation is unavailable.
	NotRunNotImplemented = "not_implemented"
)

// Stable failure codes.
const (
	// FailureRequest indicates a request could not be completed.
	FailureRequest = "request"
	// FailureAuthentication indicates tracker authentication failed.
	FailureAuthentication = "authentication"
	// FailureResponseStatus indicates the tracker returned an unusable status.
	FailureResponseStatus = "response_status"
	// FailureResponseParse indicates the tracker response was malformed.
	FailureResponseParse = "response_parse"
	// FailureInternal indicates an internal adapter failure.
	FailureInternal = "internal"
)

var validNotRunCodes = map[string]struct{}{
	NotRunUserRequested:       {},
	NotRunBannedGroup:         {},
	NotRunAuthNotReady:        {},
	NotRunMissingCredentials:  {},
	NotRunMissingMetadata:     {},
	NotRunUnsupportedContent:  {},
	NotRunManualCheckRequired: {},
	NotRunNotImplemented:      {},
}

var validFailureCodes = map[string]struct{}{
	FailureRequest:        {},
	FailureAuthentication: {},
	FailureResponseStatus: {},
	FailureResponseParse:  {},
	FailureInternal:       {},
}

// Dependencies are the narrow, construction-bound inputs for one duplicate adapter.
// Config contains only the effective tracker entry; DBPath anchors credential/session access.
type Dependencies struct {
	tracker  string
	config   config.TrackerConfig
	dbPath   string
	http     *http.Client
	logger   api.Logger
	registry *trackerspkg.Registry
}

// NewDependencies binds one adapter to its normalized tracker identity and effective config.
func NewDependencies(
	tracker string,
	trackerConfig config.TrackerConfig,
	dbPath string,
	httpClient *http.Client,
	logger api.Logger,
) Dependencies {
	tracker = strings.ToUpper(strings.TrimSpace(tracker))
	trackerConfig.InternalGroups = append([]string(nil), trackerConfig.InternalGroups...)
	trackerConfig.Unknown = maps.Clone(trackerConfig.Unknown)
	if logger == nil {
		logger = api.NopLogger{}
	}
	return Dependencies{
		tracker: tracker,
		config:  trackerConfig,
		dbPath:  strings.TrimSpace(dbPath),
		http:    httpClient,
		logger:  logger,
	}
}

// NewAdapter is a convenience constructor that narrows a runtime config before invoking a factory.
func NewAdapter(
	factory Factory,
	tracker string,
	cfg config.Config,
	httpClient *http.Client,
	logger api.Logger,
	registries ...*trackerspkg.Registry,
) Adapter {
	tracker = strings.ToUpper(strings.TrimSpace(tracker))
	var trackerConfig config.TrackerConfig
	for name, candidate := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), tracker) {
			trackerConfig = candidate
			break
		}
	}
	dependencies := NewDependencies(tracker, trackerConfig, cfg.MainSettings.DBPath, httpClient, logger)
	if len(registries) > 0 {
		dependencies.registry = registries[0]
	}
	return factory.NewDuplicateAdapter(dependencies)
}

// Tracker returns the normalized tracker identity bound to the adapter.
func (d Dependencies) Tracker() string { return d.tracker }

// TrackerConfig returns a defensive copy of the effective tracker config.
func (d Dependencies) TrackerConfig() config.TrackerConfig {
	copied := d.config
	copied.InternalGroups = append([]string(nil), d.config.InternalGroups...)
	copied.Unknown = maps.Clone(d.config.Unknown)
	return copied
}

// DBPath returns the credential/session store path.
func (d Dependencies) DBPath() string { return d.dbPath }

// HTTPClient returns the shared bounded client.
func (d Dependencies) HTTPClient() *http.Client { return d.http }

// Logger returns the adapter diagnostic sink.
func (d Dependencies) Logger() api.Logger { return d.logger }

// Registry returns the composed tracker registry when construction occurs through the duplicate service.
func (d Dependencies) Registry() *trackerspkg.Registry { return d.registry }

// MaxPages returns the tracker policy's search bound or fallback when none is configured.
func (d Dependencies) MaxPages(fallback int) int {
	if d.registry != nil {
		if policy, ok := d.registry.LookupDupePolicy(d.tracker); ok && policy.SearchScope.MaxPages > 0 {
			return policy.SearchScope.MaxPages
		}
	}
	return fallback
}

// BoundConfig returns a minimal config snapshot containing only this adapter's bound inputs.
// It exists for tracker protocol helpers shared with upload/auth code; it never contains unrelated app config.
func (d Dependencies) BoundConfig() config.Config {
	return config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: d.dbPath},
		Trackers:     config.TrackersConfig{Trackers: map[string]config.TrackerConfig{d.tracker: d.TrackerConfig()}},
	}
}

// Factory constructs one tracker-bound duplicate adapter.
type Factory interface {
	// NewDuplicateAdapter constructs an adapter bound to one tracker and dependency set.
	NewDuplicateAdapter(Dependencies) Adapter
}

// Adapter returns tracker protocol facts for one immutable Prepared release view.
type Adapter interface {
	// Search performs one tracker protocol lookup and returns a structural outcome.
	Search(context.Context, api.DuplicateSubject) AdapterResult
}

// AdapterFunc adapts a function to Adapter.
type AdapterFunc func(context.Context, api.DuplicateSubject) AdapterResult

// Search invokes f.
func (f AdapterFunc) Search(ctx context.Context, meta api.DuplicateSubject) AdapterResult {
	return f(ctx, meta)
}

type adapterResultData struct {
	disposition Disposition
	entries     []api.DupeEntry
	search      SearchEvidence
	notes       []string
	code        string
	safeMessage string
	cause       error
}

// WorkScope identifies how a search was bound to one work.
type WorkScope string

const (
	// WorkScopeUnknown means the search contract does not prove work identity.
	WorkScopeUnknown WorkScope = ""
	// WorkScopeProviderID means an authoritative provider ID bound the search.
	WorkScopeProviderID WorkScope = "provider_id"
	// WorkScopeTrackerGroup means an exact tracker group or content page bound the search.
	WorkScopeTrackerGroup WorkScope = "tracker_group"
	// WorkScopeTitle means title-derived terms bound the search.
	WorkScopeTitle WorkScope = "title_fallback"
)

// SearchEvidence records enumeration and work-identity coverage independently.
type SearchEvidence struct {
	Complete       bool
	WorkScope      WorkScope
	Pages          int
	Scope          string
	Warnings       []string
	WrongWorkCount int
}

// EffectiveComplete reports whether an exhaustive search was authoritatively bound to one work.
func (e SearchEvidence) EffectiveComplete() bool {
	return e.Complete && (e.WorkScope == WorkScopeProviderID || e.WorkScope == WorkScopeTrackerGroup)
}

// AdapterResult is an immutable, constructor-created tracker protocol outcome.
type AdapterResult struct{ data *adapterResultData }

// Resolved constructs a successful compatibility-adapter outcome whose remote
// result space is not proven complete. Evidence-backed adapters must use
// ResolvedWithSearch explicitly.
func Resolved(entries []api.DupeEntry, notes []string) AdapterResult {
	return ResolvedWithSearch(entries, notes, SearchEvidence{
		Pages:    1,
		Warnings: []string{"adapter search completeness is not evidenced"},
	})
}

// ResolvedWithSearch constructs a successful adapter outcome with explicit
// search completion evidence.
func ResolvedWithSearch(entries []api.DupeEntry, notes []string, search SearchEvidence) AdapterResult {
	if search.Pages < 0 {
		search.Pages = 0
	}
	if search.WrongWorkCount < 0 {
		search.WrongWorkCount = 0
	}
	search.Scope = strings.TrimSpace(search.Scope)
	search.Warnings = cloneNotes(search.Warnings)
	return AdapterResult{data: &adapterResultData{
		disposition: DispositionResolved,
		entries:     cloneEntries(entries),
		search:      search,
		notes:       cloneNotes(notes),
	}}
}

// NotRun constructs an explicit non-executed adapter outcome.
func NotRun(code string, safeMessage string, notes []string) AdapterResult {
	return AdapterResult{data: &adapterResultData{
		disposition: DispositionNotRun,
		code:        strings.TrimSpace(code),
		safeMessage: strings.TrimSpace(safeMessage),
		notes:       cloneNotes(notes),
	}}
}

// Failed constructs an explicit failed adapter outcome. Cause remains diagnostic-only.
func Failed(code string, safeMessage string, cause error) AdapterResult {
	return AdapterResult{data: &adapterResultData{
		disposition: DispositionFailed,
		code:        strings.TrimSpace(code),
		safeMessage: strings.TrimSpace(safeMessage),
		cause:       cause,
	}}
}

// Disposition returns the structural adapter outcome class.
func (r AdapterResult) Disposition() Disposition {
	if r.data == nil {
		return DispositionInvalid
	}
	return r.data.disposition
}

// Entries returns a defensive copy of resolved evidence.
func (r AdapterResult) Entries() []api.DupeEntry {
	if r.data == nil {
		return nil
	}
	return cloneEntries(r.data.entries)
}

// SearchEvidence returns a defensive copy of search completion evidence.
func (r AdapterResult) SearchEvidence() SearchEvidence {
	if r.data == nil {
		return SearchEvidence{}
	}
	search := r.data.search
	search.Warnings = cloneNotes(search.Warnings)
	return search
}

// Notes returns display-only notes.
func (r AdapterResult) Notes() []string {
	if r.data == nil {
		return nil
	}
	return cloneNotes(r.data.notes)
}

// Code returns the stable disposition code.
func (r AdapterResult) Code() string {
	if r.data == nil {
		return ""
	}
	return r.data.code
}

// SafeMessage returns the adapter-provided public-safe message.
func (r AdapterResult) SafeMessage() string {
	if r.data == nil {
		return ""
	}
	return r.data.safeMessage
}

// Cause returns the diagnostic failure cause. Callers must not project it publicly.
func (r AdapterResult) Cause() error { return r.cause() }

func (r AdapterResult) cause() error {
	if r.data == nil {
		return nil
	}
	return r.data.cause
}

func validNotRunCode(code string) bool {
	_, ok := validNotRunCodes[strings.TrimSpace(code)]
	return ok
}

func validFailureCode(code string) bool {
	_, ok := validFailureCodes[strings.TrimSpace(code)]
	return ok
}

func cloneNotes(notes []string) []string {
	out := make([]string, 0, len(notes))
	for _, note := range notes {
		if note = strings.TrimSpace(note); note != "" {
			out = append(out, note)
		}
	}
	return out
}

func cloneEntries(entries []api.DupeEntry) []api.DupeEntry {
	out := make([]api.DupeEntry, len(entries))
	for idx, entry := range entries {
		entry.Files = append([]string(nil), entry.Files...)
		entry.ProviderIDs = append([]api.TrackerProviderID(nil), entry.ProviderIDs...)
		entry.Flags = append([]string(nil), entry.Flags...)
		entry.HDR.Formats = append([]api.HDRFormat(nil), entry.HDR.Formats...)
		entry.HDR.FallbackFormats = append([]api.HDRFormat(nil), entry.HDR.FallbackFormats...)
		entry.HDR.SourceFields = append([]string(nil), entry.HDR.SourceFields...)
		entry.HDR.Contradictions = append([]string(nil), entry.HDR.Contradictions...)
		entry.Attributes = maps.Clone(entry.Attributes)
		out[idx] = entry
	}
	return out
}

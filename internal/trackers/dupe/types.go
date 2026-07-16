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
	NotRunUserRequested       = "user_requested"
	NotRunRuleFailed          = "rule_failed"
	NotRunBannedGroup         = "banned_group"
	NotRunAuthNotReady        = "auth_not_ready"
	NotRunMissingCredentials  = "missing_credentials"
	NotRunMissingMetadata     = "missing_metadata"
	NotRunUnsupportedContent  = "unsupported_content"
	NotRunManualCheckRequired = "manual_check_required"
	NotRunNotImplemented      = "not_implemented"
)

// Stable failure codes.
const (
	FailureRequest        = "request"
	FailureAuthentication = "authentication"
	FailureResponseStatus = "response_status"
	FailureResponseParse  = "response_parse"
	FailureInternal       = "internal"
)

var validNotRunCodes = map[string]struct{}{
	NotRunUserRequested:       {},
	NotRunRuleFailed:          {},
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
	notes       []string
	code        string
	safeMessage string
	cause       error
}

// AdapterResult is an immutable, constructor-created tracker protocol outcome.
type AdapterResult struct{ data *adapterResultData }

// Resolved constructs a successful adapter outcome.
func Resolved(entries []api.DupeEntry, notes []string) AdapterResult {
	return AdapterResult{data: &adapterResultData{
		disposition: DispositionResolved,
		entries:     cloneEntries(entries),
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
		entry.Flags = append([]string(nil), entry.Flags...)
		out[idx] = entry
	}
	return out
}

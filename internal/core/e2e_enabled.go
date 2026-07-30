// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	e2eEnabledEnv      = "UPBRR_E2E_FAKE_SERVICES"
	e2eTrackerURLEnv   = "UPBRR_E2E_TRACKER_URL"
	e2eImageURLEnv     = "UPBRR_E2E_IMAGE_URL"
	e2eClientURLEnv    = "UPBRR_E2E_CLIENT_URL"
	e2eShotPathEnv     = "UPBRR_E2E_SCREENSHOT_PATH"
	e2eResolutionEnv   = "UPBRR_E2E_RESOLUTION"
	e2eDuplicateEnv    = "UPBRR_E2E_DUPLICATE_TRACKERS"
	e2eBlurayEnv       = "UPBRR_E2E_BLURAY_CANDIDATES"
	e2eAuthNeededEnv   = "UPBRR_E2E_AUTH_REQUIRED_TRACKERS"
	e2eAuthScenarioEnv = "UPBRR_E2E_AUTH_SCENARIOS"
	e2eAuthCounterEnv  = "UPBRR_E2E_AUTH_COUNTER_PATH"
	e2eClockOffsetEnv  = "UPBRR_E2E_CLOCK_OFFSET"
)

// maybeApplyE2EServices replaces only missing runtime capabilities when both
// the e2e build tag and fake-services environment gate are active.
func maybeApplyE2EServices(_ context.Context, services *api.ServiceSet, cfg config.Config, repositories api.RepositoryCapabilities, logger api.Logger) error {
	if !isE2EEnabled() {
		return nil
	}
	if services == nil {
		return errors.New("core: e2e services target is nil")
	}
	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return fmt.Errorf("core: e2e tmp root: %w", err)
	}
	logger.Infof("core: using e2e fake services")
	if services.Clients == nil {
		services.Clients = e2eClientService{endpoint: os.Getenv(e2eClientURLEnv)}
	}
	if services.Metadata == nil {
		services.Metadata = e2eMetadataService{
			repo:    repositories.ReleaseState(),
			clients: clientdiscovery.New(services.Clients, logger),
		}
	}
	if services.Torrents == nil {
		services.Torrents = e2eTorrentService{dbPath: cfg.MainSettings.DBPath}
	}
	if services.Trackers == nil {
		services.Trackers = e2eTrackerService{
			endpoint: os.Getenv(e2eTrackerURLEnv),
			dbPath:   cfg.MainSettings.DBPath,
			repo:     repositories.Uploads(),
		}
	}
	if services.Images == nil {
		services.Images = e2eImageService{
			endpoint: os.Getenv(e2eImageURLEnv),
			shotPath: os.Getenv(e2eShotPathEnv),
			tmpRoot:  tmpRoot,
			repo:     repositories.Media(),
		}
	}
	if services.Screenshots == nil {
		services.Screenshots = e2eScreenshotService{
			shotPath: os.Getenv(e2eShotPathEnv),
			tmpRoot:  tmpRoot,
			repo:     repositories.Media(),
		}
	}
	if services.DVDMenus == nil {
		services.DVDMenus = e2eDVDMenuService{shotPath: os.Getenv(e2eShotPathEnv)}
	}
	if services.Dupes == nil {
		services.Dupes = e2eDupeService{cfg: cfg}
	}
	if services.TrackerAuth == nil {
		services.TrackerAuth = e2eTrackerAuthService{}
	}
	return nil
}

func isE2EEnabled() bool {
	value := strings.TrimSpace(os.Getenv(e2eEnabledEnv))
	return value == "1" || strings.EqualFold(value, "true")
}

type e2eWorkflowClock struct {
	offset time.Duration
}

func (c e2eWorkflowClock) Now() time.Time {
	return time.Now().UTC().Add(c.offset)
}

func e2eReleaseWorkflowOptions() []releaseworkflow.Option {
	offset, err := time.ParseDuration(strings.TrimSpace(os.Getenv(e2eClockOffsetEnv)))
	if err != nil || offset == 0 {
		return nil
	}
	return []releaseworkflow.Option{releaseworkflow.WithClock(e2eWorkflowClock{offset: offset})}
}

// e2eMetadataService supplies deterministic preparation evidence under the
// e2e-only fake-services gate.
type e2eMetadataService struct {
	repo interface {
		Save(context.Context, api.FileMetadata) error
	}
	clients *clientdiscovery.Module
}

// CollectPreparationEvidence emits deterministic progress and metadata while
// exercising the same client-discovery boundary as production preparation.
func (s e2eMetadataService) CollectPreparationEvidence(ctx context.Context, request preparationstate.Request) (preparationstate.State, error) {
	input := request.Input
	if strings.TrimSpace(input.SourcePath) == "" {
		return preparationstate.State{}, errors.New("e2e metadata: path is required")
	}
	sourcePath := strings.TrimSpace(input.SourcePath)
	api.EmitPreparationProgress(
		ctx,
		api.NewPreparationProgressUpdate(api.PreparationPhaseSourceEvidence, api.PreparationProgressRunning, "Collecting synthetic source evidence."),
	)
	if request.Layout.DiscType == "BDMV" {
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressRunning, "Scanning selected Blu-ray playlist."),
		)
	}
	resolution := strings.TrimSpace(os.Getenv(e2eResolutionEnv))
	if resolution == "" {
		resolution = "1080p"
	}
	meta := preparationstate.State{
		SourcePath: sourcePath,
		Paths:      []string{sourcePath},
		FileList:   []string{sourcePath},
		Policy: preparationstate.CollectionPolicy{
			OnlyID:          input.Policy.OnlyID,
			KeepFolder:      input.Policy.KeepFolder,
			KeepImages:      input.Policy.KeepImages,
			InteractionMode: input.Controls.Interaction,
		},
		ReleaseName:       "E2E.Movie.2026.1080p.WEB-DL.DD5.1.H264-UPBRR",
		ReleaseNameNoTag:  "E2E.Movie.2026.1080p.WEB-DL.DD5.1.H264",
		ReleaseNameClean:  "E2E Movie 2026 1080p WEB-DL DD5.1 H264",
		Filename:          filepath.Base(sourcePath),
		Tag:               "-UPBRR",
		Type:              "WEBDL",
		Source:            "WEB-DL",
		Container:         "MKV",
		VideoCodec:        "AVC",
		VideoEncode:       "H264",
		Audio:             "DD 5.1",
		Channels:          "5.1",
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		Release: api.ReleaseInfo{
			Category:   string(api.CategoryMovie),
			Type:       "WEBDL",
			Title:      "E2E Movie",
			Year:       2026,
			Source:     "WEB-DL",
			Resolution: resolution,
			Ext:        ".mkv",
			Group:      "UPBRR",
		},
		DescriptionTemplate: "E2E description fixture.",
	}
	if s.clients != nil {
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhaseClientDiscovery, api.PreparationProgressRunning, "Searching the synthetic torrent client."),
		)
		evidence, err := s.clients.Discover(ctx, clientdiscovery.SearchInput{
			SourcePath:   sourcePath,
			FileList:     meta.FileList,
			DiscType:     request.Layout.DiscType,
			Policy:       input.Search,
			ForceRecheck: input.Controls.ForceRecheck,
		})
		if err != nil {
			return preparationstate.State{}, fmt.Errorf("e2e metadata: discover client evidence: %w", err)
		}
		meta.ClientEvidence = e2eClientEvidenceSnapshot(input, evidence)
		meta.InfoHash = evidence.InfoHash
		meta.DiscoveredTorrentPath = evidence.TorrentPath
		meta.TrackerIDs = evidence.TrackerIDs
		meta.FoundTrackerMatch = evidence.FoundTrackerMatch
		meta.EvidenceTrackers = append([]string(nil), evidence.MatchedTrackers...)
		meta.MatchedEvidenceTrackers = append([]string(nil), evidence.MatchedTrackers...)
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(
				api.PreparationPhaseClientDiscovery,
				api.PreparationProgressCompleted,
				"Synthetic torrent client search complete.",
			),
		)
	}
	meta.Identity = api.ExternalIdentity{
		SourcePath: sourcePath,
		TMDBID:     1001,
		IMDBID:     1234567,
		Category:   api.CanonicalCategoryMovie,
	}
	meta.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: sourcePath,
		TMDB: &api.TMDBMetadata{
			TMDBID:           1001,
			IMDBID:           1234567,
			Category:         string(api.CategoryMovie),
			Title:            "E2E Movie",
			OriginalTitle:    "E2E Movie",
			Year:             2026,
			ReleaseDate:      "2026-01-02",
			OriginalLanguage: "en",
			Overview:         "Deterministic E2E metadata fixture.",
		},
	}
	if value := strings.TrimSpace(os.Getenv(e2eBlurayEnv)); value == "1" || strings.EqualFold(value, "true") {
		meta.ProviderMetadata.Bluray = e2eBlurayMetadata(sourcePath)
	}
	if s.repo != nil {
		if info, err := os.Stat(sourcePath); err == nil {
			meta.SourceSize = info.Size()
		}
		if err := s.repo.Save(ctx, db.FileMetadata{
			Path:       sourcePath,
			UpdatedAt:  time.Now().UTC(),
			SourceSize: meta.SourceSize,
			Category:   api.NormalizeCategory(meta.Release.Category),
			Type:       meta.Release.Type,
			Title:      meta.Release.Title,
			Year:       meta.Release.Year,
			Source:     meta.Release.Source,
			Resolution: meta.Release.Resolution,
			Ext:        meta.Release.Ext,
			Group:      meta.Release.Group,
		}); err != nil {
			return preparationstate.State{}, fmt.Errorf("e2e metadata: save: %w", err)
		}
	}
	if request.Layout.DiscType == "BDMV" {
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressCompleted, "Blu-ray analysis complete."),
		)
	}
	api.EmitPreparationProgress(
		ctx,
		api.NewPreparationProgressUpdate(api.PreparationPhaseSourceEvidence, api.PreparationProgressCompleted, "Synthetic source evidence complete."),
	)
	return meta, nil
}

// HydrateClientEvidence rebuilds restart-only private client evidence without
// rerunning the complete synthetic metadata pipeline.
func (s e2eMetadataService) HydrateClientEvidence(
	ctx context.Context,
	request preparationstate.Request,
) (preparationstate.ClientEvidenceSnapshot, error) {
	files := make([]string, 0, len(request.Manifest.Entries))
	for _, entry := range request.Manifest.Entries {
		if entry.Type == api.SourceEntryTypeFile || entry.Type == api.SourceEntryTypePlaylist {
			files = append(files, entry.Path)
		}
	}
	if s.clients == nil {
		return e2eClientEvidenceSnapshot(
			request.Input,
			clientdiscovery.Evidence{Disposition: clientdiscovery.DispositionUnavailable},
		), nil
	}
	discType := strings.TrimSpace(request.Layout.DiscType)
	if discType == "" {
		discType = request.Manifest.Classification.DiscType
	}
	evidence, err := s.clients.Discover(ctx, clientdiscovery.SearchInput{
		SourcePath:   request.Manifest.SourcePath,
		FileList:     files,
		DiscType:     discType,
		Policy:       request.Input.Search,
		ForceRecheck: request.Input.Controls.ForceRecheck,
	})
	if err != nil {
		return preparationstate.ClientEvidenceSnapshot{}, fmt.Errorf("e2e metadata: hydrate client evidence: %w", err)
	}
	return e2eClientEvidenceSnapshot(request.Input, evidence), nil
}

func (s e2eMetadataService) HydratePrivateResources(
	ctx context.Context,
	request preparationstate.Request,
) (preparationstate.State, error) {
	return s.CollectPreparationEvidence(ctx, request)
}

func e2eClientEvidenceSnapshot(
	input api.PrepareInput,
	evidence clientdiscovery.Evidence,
) preparationstate.ClientEvidenceSnapshot {
	disposition := preparationstate.ClientEvidenceDispositionUnknown
	switch evidence.Disposition {
	case clientdiscovery.DispositionSearched:
		disposition = preparationstate.ClientEvidenceDispositionSearched
	case clientdiscovery.DispositionSkipped:
		disposition = preparationstate.ClientEvidenceDispositionSkipped
	case clientdiscovery.DispositionUnavailable:
		disposition = preparationstate.ClientEvidenceDispositionUnavailable
	}
	forced := input.Controls.ForceRecheck != nil && *input.Controls.ForceRecheck &&
		disposition == preparationstate.ClientEvidenceDispositionSearched
	return preparationstate.CloneClientEvidenceSnapshot(preparationstate.ClientEvidenceSnapshot{
		Disposition:   disposition,
		Policy:        input.Search,
		ForcedRecheck: forced,
		Result: api.ClientSearchResult{
			InfoHash:            evidence.InfoHash,
			TorrentPath:         evidence.TorrentPath,
			TrackerIDs:          evidence.TrackerIDs,
			FoundTrackerMatch:   evidence.FoundTrackerMatch,
			TorrentComments:     evidence.TorrentComments,
			PieceSizeConstraint: evidence.PieceSizeConstraint,
			FoundPreferredPiece: evidence.FoundPreferredPiece,
			MatchedTrackers:     evidence.MatchedTrackers,
		},
	})
}

func e2eBlurayMetadata(sourcePath string) *api.BlurayMetadata {
	return &api.BlurayMetadata{
		SourcePath:        sourcePath,
		IMDBID:            1234567,
		SearchURL:         "https://example.com/search/example-release-2026",
		SelectedReleaseID: "example-bluray-primary",
		SelectedURL:       "https://example.com/releases/example-bluray-primary",
		AutoSelected:      true,
		SelectionReason:   "synthetic e2e selection",
		BestScore:         98,
		Threshold:         80,
		Candidates: []api.BlurayReleaseCandidate{
			{
				ReleaseID:  "example-bluray-primary",
				MovieTitle: "Example Release",
				MovieYear:  "2026",
				Title:      "Example Release 2026 Collector Edition",
				URL:        "https://example.com/releases/example-bluray-primary",
				Publisher:  "Example Publisher",
				Country:    "Exampleland",
				Region:     "A",
				Score:      98,
				Accepted:   true,
				Specs: api.BluraySpecs{
					Video: api.BlurayVideoSpec{Codec: "AVC", Resolution: "1080p"},
					Audio: []string{"English DD 5.1"},
					Discs: api.BlurayDiscSpec{
						Type:   "Blu-ray",
						Count:  1,
						Format: "BD-50",
					},
				},
				CoverImages: []api.BlurayImage{{Kind: "Front", URL: "https://example.com/images/example-bluray-primary.jpg"}},
			},
			{
				ReleaseID:  "example-bluray-alternate",
				MovieTitle: "Example Release",
				MovieYear:  "2026",
				Title:      "Example Release 2026 Standard Edition",
				URL:        "https://example.com/releases/example-bluray-alternate",
				Publisher:  "Example Publisher",
				Country:    "Exampleland",
				Region:     "B",
				Score:      92,
				Specs: api.BluraySpecs{
					Video: api.BlurayVideoSpec{Codec: "AVC", Resolution: "1080p"},
					Audio: []string{"English DD 5.1"},
					Discs: api.BlurayDiscSpec{
						Type:   "Blu-ray",
						Count:  1,
						Format: "BD-50",
					},
				},
			},
		},
		UpdatedAt: time.Now().UTC(),
	}
}

type e2eTorrentService struct {
	dbPath string
}

func (s e2eTorrentService) Create(_ context.Context, meta api.TorrentSubject) (api.TorrentResult, error) {
	root := filepath.Dir(strings.TrimSpace(s.dbPath))
	if root == "." || root == "" {
		root = os.TempDir()
	}
	dir := filepath.Join(root, "e2e-artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return api.TorrentResult{}, fmt.Errorf("e2e torrent: mkdir: %w", err)
	}
	path := filepath.Join(dir, "input.torrent")
	const torrentFixture = "d8:announce13:http://e2e.ee4:infod6:lengthi0e4:name8:test.txt12:piece lengthi16384e6:pieces0:ee"
	if err := os.WriteFile(path, []byte(torrentFixture), 0o600); err != nil {
		return api.TorrentResult{}, fmt.Errorf("e2e torrent: write: %w", err)
	}
	return api.TorrentResult{Path: path, InfoHash: "0123456789abcdef0123456789abcdef01234567"}, nil
}

// e2eClientService obtains deterministic pathed-torrent evidence from the fake E2E server.
type e2eClientService struct {
	endpoint string
}

func (s e2eClientService) Inject(ctx context.Context, _ api.ClientSubject, _ api.TorrentResult) error {
	endpoint := strings.TrimRight(strings.TrimSpace(s.endpoint), "/")
	if endpoint == "" {
		return errors.New("e2e client: endpoint is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/client-inject", http.NoBody)
	if err != nil {
		return fmt.Errorf("e2e client: injection request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("e2e client: inject: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("e2e client: injection status %d", resp.StatusCode)
	}
	return nil
}

// SearchPathedTorrents verifies the fake client endpoint and returns stable evidence.
func (s e2eClientService) SearchPathedTorrents(ctx context.Context, _ api.ClientSubject) (api.ClientSearchResult, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(s.endpoint), "/")
	if endpoint == "" {
		return api.ClientSearchResult{}, errors.New("e2e client: endpoint is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/client-search", http.NoBody)
	if err != nil {
		return api.ClientSearchResult{}, fmt.Errorf("e2e client: request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return api.ClientSearchResult{}, fmt.Errorf("e2e client: search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return api.ClientSearchResult{}, fmt.Errorf("e2e client: status %d", resp.StatusCode)
	}
	return api.ClientSearchResult{
		InfoHash:   "0123456789abcdef0123456789abcdef01234567",
		TrackerIDs: map[string]string{"btn": "e2e-pathed-id"},
	}, nil
}

type e2eDupeService struct {
	cfg config.Config
}

func (e2eDupeService) Check(_ context.Context, meta api.DuplicateSubject, trackers []string) (api.DupeCheckSummary, error) {
	duplicateTrackers := e2eTrackerSet(e2eDuplicateEnv)
	results := make([]api.DupeCheckResult, 0, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		result := api.DupeCheckResult{Tracker: name, Status: "completed"}
		if _, ok := duplicateTrackers[name]; ok {
			result.HasDupes = true
			result.Filtered = []api.DupeEntry{{
				ID:   "e2e-dupe-1",
				Name: "Example.Release.2026.1080p-GRP",
			}}
		}
		results = append(results, result)
	}
	return api.DupeCheckSummary{SourcePath: meta.SourcePath, Results: results}, nil
}

func (s e2eDupeService) CheckWithAssessment(
	ctx context.Context,
	meta api.DuplicateSubject,
	trackers []string,
	_ dupechecking.CheckOptions,
) (api.DupeCheckSummary, dupechecking.Assessment, error) {
	summary, err := s.Check(ctx, meta, trackers)
	evidence := make([]dupechecking.AssessmentEvidence, 0, len(summary.Results))
	for _, result := range summary.Results {
		evidence = append(evidence, dupechecking.AssessmentEvidence{
			Tracker:     result.Tracker,
			Disposition: dupechecking.DispositionResolved,
			HasDupes:    result.HasDupes,
			Match:       result.Match,
			Raw:         result.Raw,
		})
	}
	return summary, dupechecking.NewAssessment(meta, s.cfg, evidence), err
}

// e2eTrackerAuthService keeps fake-services runs isolated from tracker auth IO.
type e2eTrackerAuthService struct{}

const (
	e2eAuthScenarioReady                   = "ready"
	e2eAuthScenarioAutoLoginSucceeds       = "auto_login_succeeds"
	e2eAuthScenarioAutoLoginRequired       = "auto_login_required"
	e2eAuthScenarioManual2FA               = "manual_2fa"
	e2eAuthScenarioParseFailure            = "parse_failure"
	e2eAuthScenarioValidationOnlyNoCookies = "validation_only_missing_cookies"
)

// Capabilities enables deterministic managed-auth preflight only for trackers
// selected by the e2e environment.
func (e2eTrackerAuthService) Capabilities(context.Context) ([]api.TrackerAuthCapability, error) {
	scenarios := e2eAuthScenarios()
	trackerIDs := make([]string, 0, len(scenarios))
	for trackerID := range scenarios {
		trackerIDs = append(trackerIDs, trackerID)
	}
	slices.Sort(trackerIDs)
	capabilities := make([]api.TrackerAuthCapability, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		scenario := scenarios[trackerID]
		capability := api.TrackerAuthCapability{
			TrackerID:   trackerID,
			DisplayName: trackerID,
			AuthKind:    "cookies",
		}
		switch scenario {
		case e2eAuthScenarioReady, e2eAuthScenarioManual2FA, e2eAuthScenarioParseFailure:
			capability.SupportsCookieFile = true
		case e2eAuthScenarioAutoLoginSucceeds, e2eAuthScenarioAutoLoginRequired:
			capability.AuthKind = "cookies_login"
			capability.SupportsCookieFile = true
			capability.SupportsLogin = true
			capability.SupportsAutoLogin = true
		case e2eAuthScenarioValidationOnlyNoCookies:
			capability.SupportsCookieFile = true
		default:
			capability.SupportsCookieFile = true
		}
		if scenario == e2eAuthScenarioManual2FA {
			capability.SupportsLogin = true
			capability.SupportsAutoLogin = true
			capability.SupportsManual2FA = true
		}
		capabilities = append(capabilities, capability)
	}
	recordE2EAuthCounters(true, nil, 0)
	return capabilities, nil
}

// ValidateMany returns configured statuses without contacting trackers.
func (e2eTrackerAuthService) ValidateMany(_ context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	scenarios := e2eAuthScenarios()
	statuses := make([]api.TrackerAuthStatus, 0, len(trackerIDs))
	loginAttempts := 0
	for _, trackerID := range trackerIDs {
		normalized := strings.ToUpper(strings.TrimSpace(trackerID))
		status := api.TrackerAuthStatus{
			TrackerID: normalized,
			State:     trackerauth.StateConfigured,
		}
		switch scenarios[normalized] {
		case e2eAuthScenarioAutoLoginSucceeds:
			loginAttempts++
		case e2eAuthScenarioAutoLoginRequired:
			loginAttempts++
			status.State = trackerauth.StateLoginRequired
		case e2eAuthScenarioManual2FA:
			loginAttempts++
			status.State = trackerauth.StateLoginRequired
			status.Needs2FA = true
			status.ChallengeID = "synthetic-e2e-challenge"
		case e2eAuthScenarioParseFailure:
			status.LastError = "synthetic remote response parse failure"
		case e2eAuthScenarioValidationOnlyNoCookies:
			status.State = trackerauth.StateLoginRequired
		}
		statuses = append(statuses, status)
	}
	recordE2EAuthCounters(false, trackerIDs, loginAttempts)
	return statuses, nil
}

func e2eAuthScenarios() map[string]string {
	scenarios := make(map[string]string)
	for trackerID := range e2eTrackerSet(e2eAuthNeededEnv) {
		scenarios[trackerID] = e2eAuthScenarioValidationOnlyNoCookies
	}
	for value := range strings.SplitSeq(os.Getenv(e2eAuthScenarioEnv), ",") {
		trackerID, scenario, ok := strings.Cut(value, "=")
		trackerID = strings.ToUpper(strings.TrimSpace(trackerID))
		scenario = strings.ToLower(strings.TrimSpace(scenario))
		if ok && trackerID != "" && scenario != "" {
			scenarios[trackerID] = scenario
		}
	}
	return scenarios
}

type e2eAuthCounterSnapshot struct {
	CapabilityCalls int            `json:"capabilityCalls"`
	ValidationCalls int            `json:"validationCalls"`
	LoginAttempts   int            `json:"loginAttempts"`
	Validations     map[string]int `json:"validations"`
}

var e2eAuthCounterMu sync.Mutex

func recordE2EAuthCounters(capabilityCall bool, trackerIDs []string, loginAttempts int) {
	path := strings.TrimSpace(os.Getenv(e2eAuthCounterEnv))
	if path == "" {
		return
	}
	e2eAuthCounterMu.Lock()
	defer e2eAuthCounterMu.Unlock()
	snapshot := e2eAuthCounterSnapshot{Validations: make(map[string]int)}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &snapshot)
	}
	if snapshot.Validations == nil {
		snapshot.Validations = make(map[string]int)
	}
	if capabilityCall {
		snapshot.CapabilityCalls++
	} else {
		snapshot.ValidationCalls++
		for _, trackerID := range trackerIDs {
			trackerID = strings.ToUpper(strings.TrimSpace(trackerID))
			if trackerID != "" {
				snapshot.Validations[trackerID]++
			}
		}
	}
	snapshot.LoginAttempts += loginAttempts
	data, err := json.Marshal(snapshot)
	if err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}

func e2eTrackerSet(environment string) map[string]struct{} {
	result := make(map[string]struct{})
	for tracker := range strings.SplitSeq(os.Getenv(environment), ",") {
		if tracker = strings.ToUpper(strings.TrimSpace(tracker)); tracker != "" {
			result[tracker] = struct{}{}
		}
	}
	return result
}

type e2eTrackerService struct {
	endpoint string
	dbPath   string
	repo     api.UploadLedgerRepository
}

type e2eDVDMenuService struct {
	shotPath string
}

func (s e2eDVDMenuService) Capture(
	_ context.Context,
	subject api.DVDMenuSubject,
	maxItems int,
) (api.DVDMenuCaptureResult, error) {
	return api.DVDMenuCaptureResult{
		SourcePath: subject.SourcePath,
		Images: []api.DVDMenuCaptureImage{
			{ScreenshotImage: e2eScreenshotImage(s.shotPath, api.ScreenshotPurposeMenu), Discovery: api.DVDMenuDiscoveryReachable},
		},
		DiscoveredMenus: 1,
		VisitedStates:   1,
		VisitedButtons:  1,
		MaxItems:        maxItems,
		Complete:        true,
		Engine: api.DVDMenuEngineInfo{
			EngineVersion:  "e2e",
			SchemaVersion:  1,
			FFmpegDVDVideo: true,
		},
	}, nil
}

func (s e2eDVDMenuService) List(_ context.Context, _ api.DVDMenuSubject) ([]api.ScreenshotImage, error) {
	return []api.ScreenshotImage{e2eScreenshotImage(s.shotPath, api.ScreenshotPurposeMenu)}, nil
}

func (e2eDVDMenuService) Delete(context.Context, api.DVDMenuSubject, string) error { return nil }

func (e2eDVDMenuService) Capability(context.Context) (api.DVDMenuEngineInfo, error) {
	return api.DVDMenuEngineInfo{
		EngineVersion:  "e2e",
		SchemaVersion:  1,
		FFmpegDVDVideo: true,
	}, nil
}

func e2eScreenshotImage(path string, purpose api.ScreenshotPurpose) api.ScreenshotImage {
	return api.ScreenshotImage{
		Index:     1,
		Path:      path,
		Purpose:   purpose,
		Width:     320,
		Height:    180,
		SizeBytes: 68,
	}
}

type e2eRetainedUploadPlan struct {
	mu           sync.Mutex
	service      e2eTrackerService
	subject      api.UploadSubject
	preparations []trackers.RetainedTrackerPreparation
	executed     bool
	released     bool
}

func (s e2eTrackerService) PrepareRetainedUploadPlan(
	ctx context.Context,
	subject api.UploadSubject,
	projections []api.TrackerReleaseProjection,
) (workflowRetainedUploadPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("e2e tracker: prepare retained plan: %w", err)
	}
	trackerNames := make([]string, 0, len(projections))
	for _, projection := range projections {
		if projection.UploadReady && projection.Readiness == api.ReadinessStatusReady {
			trackerNames = append(trackerNames, string(projection.TrackerID))
		}
	}
	if len(trackerNames) == 0 {
		return nil, errors.New("e2e tracker: retained plan has no eligible trackers")
	}
	previews, err := s.BuildUploadDryRun(ctx, subject, trackerNames)
	if err != nil {
		return nil, fmt.Errorf("e2e tracker: prepare retained previews: %w", err)
	}
	preparations := make([]trackers.RetainedTrackerPreparation, 0, len(previews))
	for _, preview := range previews {
		preparations = append(preparations, trackers.RetainedTrackerPreparation{
			Tracker:     preview.Tracker,
			TorrentPath: subject.TorrentPath,
			Preview:     preview,
		})
	}
	subject.Trackers = append([]string(nil), trackerNames...)
	return &e2eRetainedUploadPlan{
		service:      s,
		subject:      subject,
		preparations: preparations,
	}, nil
}

func (p *e2eRetainedUploadPlan) Preparations() []trackers.RetainedTrackerPreparation {
	if p == nil {
		return nil
	}
	return append([]trackers.RetainedTrackerPreparation(nil), p.preparations...)
}

func (p *e2eRetainedUploadPlan) Execute(ctx context.Context) ([]trackers.RetainedTrackerResult, error) {
	if p == nil {
		return nil, errors.New("e2e tracker: retained plan is unavailable")
	}
	trackerIDs := make([]string, 0, len(p.preparations))
	for _, preparation := range p.preparations {
		trackerIDs = append(trackerIDs, preparation.Tracker)
	}
	return p.ExecuteSelected(ctx, trackerIDs)
}

func (p *e2eRetainedUploadPlan) ExecuteSelected(
	ctx context.Context,
	trackerIDs []string,
) ([]trackers.RetainedTrackerResult, error) {
	if p == nil {
		return nil, errors.New("e2e tracker: retained plan is unavailable")
	}
	preparationByTracker := make(map[string]trackers.RetainedTrackerPreparation, len(p.preparations))
	for _, preparation := range p.preparations {
		preparationByTracker[strings.ToUpper(strings.TrimSpace(preparation.Tracker))] = preparation
	}
	selected := make([]trackers.RetainedTrackerPreparation, 0, len(trackerIDs))
	selectedNames := make([]string, 0, len(trackerIDs))
	seen := make(map[string]struct{}, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		name := strings.ToUpper(strings.TrimSpace(trackerID))
		preparation, exists := preparationByTracker[name]
		if name == "" || !exists {
			return nil, fmt.Errorf("e2e tracker: retained plan does not contain tracker %q", trackerID)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("e2e tracker: retained plan tracker %s is duplicated", name)
		}
		seen[name] = struct{}{}
		selected = append(selected, preparation)
		selectedNames = append(selectedNames, name)
	}
	if len(selected) == 0 {
		return nil, errors.New("e2e tracker: retained plan selection is empty")
	}
	p.mu.Lock()
	if p.released || p.executed {
		p.mu.Unlock()
		return nil, errors.New("e2e tracker: retained plan is no longer executable")
	}
	p.executed = true
	p.mu.Unlock()
	subject := p.subject
	subject.Trackers = selectedNames
	summary, err := p.service.Upload(ctx, subject)
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	results := make([]trackers.RetainedTrackerResult, 0, len(selected))
	for _, preparation := range selected {
		results = append(results, trackers.RetainedTrackerResult{Tracker: preparation.Tracker, Summary: summary})
	}
	return results, nil
}

func (p *e2eRetainedUploadPlan) Release() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
	return nil
}

func (s e2eTrackerService) Upload(ctx context.Context, meta api.UploadSubject) (api.UploadSummary, error) {
	trackerNames := meta.Trackers
	if len(trackerNames) == 0 {
		trackerNames = []string{"BTN"}
	}
	summary := api.UploadSummary{}
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if s.repo != nil {
			if err := s.repo.CreateUploadRecord(ctx, db.UploadRecord{
				Tracker:    name,
				Status:     "pending",
				SourcePath: meta.SourcePath,
				CreatedAt:  time.Now().UTC(),
			}); err != nil {
				return api.UploadSummary{}, fmt.Errorf("e2e tracker: create record: %w", err)
			}
		}
		if err := postE2ETrackerUpload(ctx, s.endpoint, name, meta); err != nil {
			if s.repo != nil {
				_ = s.repo.UpdateLatestUploadRecordStatus(ctx, meta.SourcePath, name, "failed")
			}
			return api.UploadSummary{}, err
		}
		artifactPath := ""
		registeredPath, resolveErr := trackers.ResolveTrackerTorrentArtifactPath(meta, s.dbPath, name)
		if resolveErr == nil {
			downloadURL := strings.TrimRight(s.endpoint, "/") + "/download/e2e-123"
			downloadRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
			if requestErr == nil {
				if downloadErr := trackers.DownloadRegisteredTorrent(
					ctx,
					&http.Client{Timeout: 30 * time.Second},
					downloadRequest,
					registeredPath,
				); downloadErr == nil {
					artifactPath = registeredPath
				}
			}
		}
		if s.repo != nil {
			if err := s.repo.UpdateLatestUploadRecordStatus(ctx, meta.SourcePath, name, "uploaded"); err != nil {
				return api.UploadSummary{}, fmt.Errorf("e2e tracker: update record: %w", err)
			}
		}
		summary.Uploaded++
		summary.UploadedTorrents = append(summary.UploadedTorrents, api.UploadedTorrent{
			Tracker:     name,
			TorrentID:   "e2e-123",
			DownloadURL: strings.TrimRight(s.endpoint, "/") + "/download/e2e-123",
			TorrentURL:  strings.TrimRight(s.endpoint, "/") + "/torrent/e2e-123",
			TorrentPath: artifactPath,
		})
	}
	return summary, nil
}

func (s e2eTrackerService) BuildPreparation(_ context.Context, meta api.DescriptionSubject, trackers []string) (api.PreparationPreview, error) {
	if len(trackers) == 0 {
		trackers = meta.Trackers
	}
	descriptions := make([]api.PreparationDescription, 0, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		descriptions = append(descriptions, api.PreparationDescription{
			GroupKey:           strings.ToLower(name),
			Trackers:           []string{name},
			RawDescription:     "E2E description fixture.",
			RawDescriptionHTML: "<p>E2E description fixture.</p>",
			Description:        "E2E description fixture.",
			DescriptionHTML:    "<p>E2E description fixture.</p>",
		})
	}
	return api.PreparationPreview{SourcePath: meta.SourcePath, Descriptions: descriptions}, nil
}

func (s e2eTrackerService) BuildUploadDryRun(_ context.Context, meta api.UploadSubject, trackers []string) ([]api.TrackerDryRunEntry, error) {
	if len(trackers) == 0 {
		trackers = meta.Trackers
	}
	entries := make([]api.TrackerDryRunEntry, 0, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		entries = append(entries, api.TrackerDryRunEntry{
			Tracker:             name,
			Status:              "ready",
			ReleaseName:         meta.ReleaseName,
			OriginalReleaseName: meta.ReleaseName,
			UploadReleaseName:   meta.ReleaseName,
			DescriptionGroup:    "unit3d",
			Description:         "E2E description fixture.",
			Endpoint:            strings.TrimRight(s.endpoint, "/") + "/upload",
			Payload: map[string]string{
				"name":     meta.ReleaseName,
				"category": string(api.CategoryMovie),
			},
			Files: []api.TrackerDryRunFile{{
				Field:   "torrent",
				Path:    meta.TorrentPath,
				Present: strings.TrimSpace(meta.TorrentPath) != "",
			}},
			ImageHost: api.ImageHostFeedback{
				Status:       "ready",
				SelectedHost: "imgbb",
				AllowedHosts: []string{"imgbb"},
			},
		})
	}
	return entries, nil
}

func postE2ETrackerUpload(ctx context.Context, endpoint string, tracker string, meta api.UploadSubject) error {
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("e2e tracker: endpoint is required")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("tracker", tracker)
	_ = writer.WriteField("name", meta.ReleaseName)
	if strings.TrimSpace(meta.TorrentPath) != "" {
		part, err := writer.CreateFormFile("torrent", filepath.Base(meta.TorrentPath))
		if err != nil {
			return fmt.Errorf("e2e tracker: create multipart file: %w", err)
		}
		file, err := os.Open(meta.TorrentPath)
		if err != nil {
			return fmt.Errorf("e2e tracker: open torrent: %w", err)
		}
		_, copyErr := io.Copy(part, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("e2e tracker: copy torrent: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("e2e tracker: close torrent: %w", closeErr)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("e2e tracker: close multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/upload", &body)
	if err != nil {
		return fmt.Errorf("e2e tracker: request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("e2e tracker: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("e2e tracker: status %d: %s", resp.StatusCode, strings.TrimSpace(redaction.RedactValue(string(payload), nil)))
	}
	return nil
}

type e2eImageService struct {
	endpoint string
	shotPath string
	tmpRoot  string
	repo     interface {
		SaveUploadedImages(context.Context, string, string, []api.UploadedImageLink) error
	}
}

func (s e2eImageService) ListCandidates(_ context.Context, meta api.ImageHostingSubject) ([]api.ScreenshotImage, error) {
	shot, err := e2eManagedScreenshot(s.shotPath, s.tmpRoot, filepath.Base(meta.SourcePath), 1)
	if err != nil {
		return nil, err
	}
	return []api.ScreenshotImage{shot}, nil
}

func (s e2eImageService) Upload(
	ctx context.Context,
	meta api.ImageHostingSubject,
	host string,
	usageScope string,
	images []api.ScreenshotImage,
) ([]api.UploadedImageLink, error) {
	if strings.TrimSpace(s.endpoint) == "" {
		return nil, errors.New("e2e image: endpoint is required")
	}
	links := make([]api.UploadedImageLink, 0, len(images))
	for idx, image := range images {
		if err := postE2EImageUpload(ctx, s.endpoint, host, image.Path); err != nil {
			return nil, err
		}
		base := fmt.Sprintf("%s/image/%d", strings.TrimRight(s.endpoint, "/"), idx+1)
		links = append(links, api.UploadedImageLink{
			ImagePath:  image.Path,
			Host:       strings.ToLower(strings.TrimSpace(host)),
			ImgURL:     base + ".jpg",
			RawURL:     base + ".jpg",
			WebURL:     base,
			UsageScope: usageScope,
			UploadedAt: time.Now().UTC(),
		})
	}
	if s.repo != nil && len(links) > 0 {
		if err := s.repo.SaveUploadedImages(ctx, meta.SourcePath, strings.ToLower(strings.TrimSpace(host)), links); err != nil {
			return nil, fmt.Errorf("e2e image: save uploads: %w", err)
		}
	}
	return links, nil
}

func postE2EImageUpload(ctx context.Context, endpoint string, host string, imagePath string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("host", host)
	part, err := writer.CreateFormFile("image", filepath.Base(imagePath))
	if err != nil {
		return fmt.Errorf("e2e image: create multipart file: %w", err)
	}
	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("e2e image: open image: %w", err)
	}
	_, copyErr := io.Copy(part, file)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("e2e image: copy image: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("e2e image: close image: %w", closeErr)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("e2e image: close multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/upload", &body)
	if err != nil {
		return fmt.Errorf("e2e image: request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("e2e image: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("e2e image: status %d: %s", resp.StatusCode, strings.TrimSpace(redaction.RedactValue(string(payload), nil)))
	}
	return nil
}

type e2eScreenshotService struct {
	shotPath string
	tmpRoot  string
	repo     api.ScreenshotLifecycleRepository
}

func (s e2eScreenshotService) Plan(_ context.Context, meta api.ScreenshotSubject, count int) (api.ScreenshotPlan, error) {
	selections := make([]api.ScreenshotSelection, 0, count)
	for index := range count {
		selections = append(selections, api.ScreenshotSelection{
			Index:            index + 1,
			TimestampSeconds: float64((index + 1) * 10),
			Frame:            (index + 1) * 240,
		})
	}
	return api.ScreenshotPlan{
		SourcePath:          meta.SourcePath,
		DurationSeconds:     120,
		FrameRate:           24,
		SuggestedSelections: selections,
	}, nil
}

func (s e2eScreenshotService) Capture(
	_ context.Context,
	meta api.ScreenshotSubject,
	selections []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	images := make([]api.ScreenshotImage, 0, len(selections))
	for _, selection := range selections {
		shot, err := s.imageAt(meta, selection.Index)
		if err != nil {
			return api.ScreenshotResult{}, err
		}
		shot.Index = selection.Index
		shot.TimestampSeconds = selection.TimestampSeconds
		shot.Purpose = purpose
		images = append(images, shot)
	}
	return api.ScreenshotResult{
		SourcePath: meta.SourcePath,
		Purpose:    purpose,
		Images:     images,
	}, nil
}

func (s e2eScreenshotService) PreviewFrame(_ context.Context, meta api.ScreenshotSubject, timestampSeconds float64) (api.ScreenshotPreview, error) {
	shot, err := s.image(meta)
	if err != nil {
		return api.ScreenshotPreview{}, err
	}
	payload, err := os.ReadFile(shot.Path)
	if err != nil {
		return api.ScreenshotPreview{}, fmt.Errorf("e2e screenshots: read preview: %w", err)
	}
	return api.ScreenshotPreview{
		TimestampSeconds: timestampSeconds,
		ImageBytes:       payload,
		Width:            shot.Width,
		Height:           shot.Height,
		SizeBytes:        shot.SizeBytes,
	}, nil
}

func (s e2eScreenshotService) Delete(ctx context.Context, _ api.ScreenshotSubject, imagePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filepath.Abs(filepath.Join(s.tmpRoot, "e2e"))
	if err != nil {
		return fmt.Errorf("e2e screenshots: resolve managed root: %w", err)
	}
	target, err := filepath.Abs(strings.TrimSpace(imagePath))
	if err != nil {
		return fmt.Errorf("e2e screenshots: resolve delete target: %w", err)
	}
	if pathutil.SamePath(target, root) || !pathutil.IsWithinRoot(root, target) {
		return errors.New("e2e screenshots: delete target is outside managed root")
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("e2e screenshots: delete managed image: %w", err)
	}
	return nil
}

func (s e2eScreenshotService) SaveFinalSelections(ctx context.Context, meta api.ScreenshotSubject, images []api.ScreenshotImage) error {
	if s.repo == nil {
		return nil
	}
	selections := make([]api.ScreenshotFinalSelection, 0, len(images))
	for idx, image := range images {
		selections = append(selections, api.ScreenshotFinalSelection{
			SourcePath: meta.SourcePath,
			ImagePath:  image.Path,
			Order:      idx,
			Source:     string(api.ScreenshotPurposeFinal),
			SelectedAt: time.Now().UTC(),
		})
	}
	return s.repo.ReplaceNormalFinalSelections(ctx, meta.SourcePath, selections)
}

func (s e2eScreenshotService) image(meta api.ScreenshotSubject) (api.ScreenshotImage, error) {
	return s.imageAt(meta, 1)
}

func (s e2eScreenshotService) imageAt(meta api.ScreenshotSubject, index int) (api.ScreenshotImage, error) {
	return e2eManagedScreenshot(s.shotPath, s.tmpRoot, filepath.Base(meta.SourcePath), index)
}

func e2eManagedScreenshot(shotPath string, tmpRoot string, releaseName string, index int) (api.ScreenshotImage, error) {
	path := strings.TrimSpace(shotPath)
	if path == "" {
		return api.ScreenshotImage{}, errors.New("e2e screenshots: screenshot path is required")
	}
	root := strings.TrimSpace(tmpRoot)
	if root == "" {
		return api.ScreenshotImage{}, errors.New("e2e screenshots: tmp root is required")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("e2e screenshots: read fixture: %w", err)
	}
	release := strings.TrimSpace(releaseName)
	if release == "" {
		release = "e2e-release"
	}
	release = strings.Map(func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		default:
			return r
		}
	}, release)
	managedDir := filepath.Join(root, "e2e", release)
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("e2e screenshots: create managed dir: %w", err)
	}
	managedPath := filepath.Join(managedDir, fmt.Sprintf("screenshot-%d.png", index))
	if err := os.WriteFile(managedPath, payload, 0o600); err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("e2e screenshots: write managed screenshot: %w", err)
	}
	info, err := os.Stat(managedPath)
	if err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("e2e screenshots: stat managed screenshot: %w", err)
	}
	return api.ScreenshotImage{
		Index:            index,
		TimestampSeconds: float64(index * 10),
		Path:             managedPath,
		Purpose:          api.ScreenshotPurposeFinal,
		Width:            320,
		Height:           180,
		SizeBytes:        info.Size(),
	}, nil
}

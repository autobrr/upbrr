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
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	e2eEnabledEnv    = "UPBRR_E2E_FAKE_SERVICES"
	e2eTrackerURLEnv = "UPBRR_E2E_TRACKER_URL"
	e2eImageURLEnv   = "UPBRR_E2E_IMAGE_URL"
	e2eClientURLEnv  = "UPBRR_E2E_CLIENT_URL"
	e2eShotPathEnv   = "UPBRR_E2E_SCREENSHOT_PATH"
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
		services.Trackers = e2eTrackerService{endpoint: os.Getenv(e2eTrackerURLEnv), repo: repositories.Uploads()}
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
			Resolution: "1080p",
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

func (e2eClientService) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
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
	results := make([]api.DupeCheckResult, 0, len(trackers))
	for _, tracker := range trackers {
		results = append(results, api.DupeCheckResult{Tracker: strings.ToUpper(strings.TrimSpace(tracker)), Status: "completed"})
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

// Capabilities disables managed-auth preflight in fake-services runs.
func (e2eTrackerAuthService) Capabilities(context.Context) ([]api.TrackerAuthCapability, error) {
	return nil, nil
}

// ValidateMany returns configured statuses without contacting trackers.
func (e2eTrackerAuthService) ValidateMany(_ context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	statuses := make([]api.TrackerAuthStatus, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		statuses = append(statuses, api.TrackerAuthStatus{TrackerID: strings.ToUpper(strings.TrimSpace(trackerID)), State: trackerauth.StateConfigured})
	}
	return statuses, nil
}

type e2eTrackerService struct {
	endpoint string
	repo     api.UploadLedgerRepository
}

func (s e2eTrackerService) Upload(ctx context.Context, meta api.UploadSubject) (api.UploadSummary, error) {
	trackers := meta.Trackers
	if len(trackers) == 0 {
		trackers = []string{"BTN"}
	}
	summary := api.UploadSummary{}
	for _, tracker := range trackers {
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
		artifactPath := meta.TorrentPath
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
	return api.PreparationPreview{SourcePath: meta.SourcePath, Descriptions: []api.PreparationDescription{{
		GroupKey:           "unit3d",
		Trackers:           trackers,
		RawDescription:     "E2E description fixture.",
		RawDescriptionHTML: "<p>E2E description fixture.</p>",
		Description:        "E2E description fixture.",
		DescriptionHTML:    "<p>E2E description fixture.</p>",
	}}}, nil
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
	shot, err := e2eManagedScreenshot(s.shotPath, s.tmpRoot, filepath.Base(meta.SourcePath))
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

func (s e2eScreenshotService) Plan(_ context.Context, meta api.ScreenshotSubject, _ int) (api.ScreenshotPlan, error) {
	return api.ScreenshotPlan{
		SourcePath:      meta.SourcePath,
		DurationSeconds: 120,
		FrameRate:       24,
		SuggestedSelections: []api.ScreenshotSelection{{
			Index:            1,
			TimestampSeconds: 10,
			Frame:            240,
		}},
	}, nil
}

func (s e2eScreenshotService) Capture(
	_ context.Context,
	meta api.ScreenshotSubject,
	_ []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	shot, err := s.image(meta)
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	shot.Purpose = purpose
	return api.ScreenshotResult{
		SourcePath: meta.SourcePath,
		Purpose:    purpose,
		Images:     []api.ScreenshotImage{shot},
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

func (s e2eScreenshotService) Delete(_ context.Context, _ api.ScreenshotSubject, _ string) error {
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
	return e2eManagedScreenshot(s.shotPath, s.tmpRoot, filepath.Base(meta.SourcePath))
}

func e2eManagedScreenshot(shotPath string, tmpRoot string, releaseName string) (api.ScreenshotImage, error) {
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
	managedPath := filepath.Join(managedDir, "screenshot-1.png")
	if err := os.WriteFile(managedPath, payload, 0o600); err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("e2e screenshots: write managed screenshot: %w", err)
	}
	info, err := os.Stat(managedPath)
	if err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("e2e screenshots: stat managed screenshot: %w", err)
	}
	return api.ScreenshotImage{
		Index:            1,
		TimestampSeconds: 10,
		Path:             managedPath,
		Purpose:          api.ScreenshotPurposeFinal,
		Width:            320,
		Height:           180,
		SizeBytes:        info.Size(),
	}, nil
}

func writeJSONE2E(w io.Writer, value any) error {
	return json.NewEncoder(w).Encode(value)
}

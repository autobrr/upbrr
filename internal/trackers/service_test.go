// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadNoTrackers(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, NewRegistry())
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected 0 uploads, got %d", summary.Uploaded)
	}
}

func TestUploadConfiguredTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"blU", "bhd"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, NewRegistry())
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected unsupported configured trackers to remain inert, got %d uploads", summary.Uploaded)
	}
}

func TestUploadOverrideTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, NewRegistry())
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file", Trackers: []string{"aither"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected unsupported override tracker to remain inert, got %d uploads", summary.Uploaded)
	}
}

func TestUploadOverrideTrackersReplaceDefaults(t *testing.T) {
	t.Parallel()

	requests := make(chan PreparationInput, 2)
	registry := NewRegistry()
	for _, definition := range []Definition{
		trackingUploadDefinition{name: "BLU", requests: requests},
		trackingUploadDefinition{name: "AITHER", requests: requests},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)
	summary, err := svc.Upload(context.Background(), api.UploadSubject{
		SourcePath: "/tmp/file",
		Trackers:   []string{"aither"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected 1 upload, got %d", summary.Uploaded)
	}

	select {
	case req := <-requests:
		if req.Tracker != "AITHER" {
			t.Fatalf("expected AITHER upload, got %q", req.Tracker)
		}
	default:
		t.Fatal("expected upload request")
	}
	select {
	case req := <-requests:
		t.Fatalf("expected defaults excluded from override upload, got extra %q", req.Tracker)
	default:
	}
}

func TestUploadRemovesTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, NewRegistry())
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file", TrackersRemove: []string{"bhd"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected unsupported configured trackers to remain inert, got %d uploads", summary.Uploaded)
	}
}

func TestUploadUnknownTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"NOPE"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, NewRegistry())
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected 0 uploads, got %d", summary.Uploaded)
	}
}

func TestUploadBannedGroup(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"TOS"}}}
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:         "TOS",
		Definition:   trackingUploadDefinition{name: "TOS"},
		BannedGroups: []string{"FL3ER"},
	}); err != nil {
		t.Fatalf("register TOS: %v", err)
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)
	_, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file", Tag: "-FL3ER"})
	if !errors.Is(err, internalerrors.ErrBannedGroup) {
		t.Fatalf("expected banned group error, got %v", err)
	}
}

func TestUploadSkipsDynamicBannedRefreshForEmptyEffectiveGroup(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"data":[],"meta":{"next_cursor":""}}`))
	}))
	defer server.Close()

	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:         "AITHER",
		BaseURL:      "https://aither.cc",
		Definition:   trackingUploadDefinition{name: "AITHER"},
		BannedPolicy: &BannedGroupPolicy{EndpointPath: "/api/blacklists/releasegroups", RequireAPIKey: true},
	}); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")},
		Trackers: config.TrackersConfig{
			DefaultTrackers: config.CSVList{"AITHER"},
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "aither-key"},
			},
		},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)
	svc.banned.client = bannedRewriteClient(t, server)

	for _, tag := range []string{"-", " - ", "   "} {
		summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file", Tag: tag})
		if err != nil {
			t.Fatalf("upload tag %q: %v", tag, err)
		}
		if summary.Uploaded != 1 {
			t.Fatalf("expected upload to proceed for tag %q, got %d", tag, summary.Uploaded)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected no dynamic banned refresh for empty effective group, got %d request(s)", got)
	}

	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file", Tag: "-GRP"})
	if err != nil {
		t.Fatalf("upload real group: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected upload to proceed for real group, got %d", summary.Uploaded)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected real group to refresh dynamic banned groups once, got %d request(s)", got)
	}
}

func TestNormalizeBannedReleaseGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tag  string
		want string
	}{
		{tag: "", want: ""},
		{tag: "-", want: ""},
		{tag: " - ", want: ""},
		{tag: "   ", want: ""},
		{tag: "-GRP", want: "grp"},
		{tag: "-TAoE", want: "taoe"},
		{tag: "-NotTAoE", want: "nottaoe"},
	}
	for _, tt := range tests {
		if got := NormalizeBannedReleaseGroup(tt.tag); got != tt.want {
			t.Fatalf("NormalizeBannedReleaseGroup(%q)=%q, want %q", tt.tag, got, tt.want)
		}
	}
}

func TestNormalizeTrackersDedup(t *testing.T) {
	t.Parallel()

	values := normalizeTrackers([]string{"blu", "BLU", "bhd", ""})
	if len(values) != 2 {
		t.Fatalf("expected 2 trackers, got %d", len(values))
	}
	if values[0] != "BLU" || values[1] != "BHD" {
		t.Fatalf("unexpected tracker list: %v", values)
	}
}

type stubDryRunDefinition struct {
	name         string
	mode         UploadContentMode
	dryRunErr    error
	bannedGroups []string
	intents      *[]PreparationIntent
	inputs       *[]PreparationInput
}

func (s stubDryRunDefinition) BannedGroups() []string { return s.bannedGroups }

type stubUploadArtifactDefinition struct {
	name string
}

type stubPreparationDefinition struct {
	name        string
	group       string
	description string
}

type trackingUploadDefinition struct {
	name     string
	started  chan<- string
	requests chan<- PreparationInput
	release  <-chan struct{}
}

type blockingImageService struct {
	mu      sync.Mutex
	started chan<- string
	release <-chan struct{}
	calls   []string
	repo    *stubRepo
}

type blockingUploadDefinition struct {
	name      string
	started   chan<- string
	release   <-chan struct{}
	uploaded  int
	uploadErr error
}

type cancelAfterUploadDefinition struct {
	name             string
	cancel           context.CancelFunc
	uploaded         int
	uploadedTorrents []api.UploadedTorrent
}

type testHDBPreparationDefinition struct{}

func (s stubDryRunDefinition) UploadContentMode() UploadContentMode {
	if s.mode != "" {
		return s.mode
	}
	return UploadContentModeDescription
}

func (stubUploadArtifactDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (stubPreparationDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (trackingUploadDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (blockingUploadDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (cancelAfterUploadDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (testHDBPreparationDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (stubDryRunDefinition) DefaultBaseURL() string         { return "https://tracker.example.invalid" }
func (stubUploadArtifactDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }
func (stubPreparationDefinition) DefaultBaseURL() string    { return "https://tracker.example.invalid" }
func (trackingUploadDefinition) DefaultBaseURL() string     { return "https://tracker.example.invalid" }
func (blockingUploadDefinition) DefaultBaseURL() string     { return "https://tracker.example.invalid" }
func (cancelAfterUploadDefinition) DefaultBaseURL() string  { return "https://tracker.example.invalid" }
func (hostAwareDescriptionDefinition) DefaultBaseURL() string {
	return "https://tracker.example.invalid"
}
func (testHDBPreparationDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (s stubDryRunDefinition) TrackerFamily() Family         { return testTrackerFamily(s.name) }
func (s stubUploadArtifactDefinition) TrackerFamily() Family { return testTrackerFamily(s.name) }
func (s stubPreparationDefinition) TrackerFamily() Family    { return testTrackerFamily(s.name) }
func (t trackingUploadDefinition) TrackerFamily() Family     { return testTrackerFamily(t.name) }
func (b blockingUploadDefinition) TrackerFamily() Family     { return testTrackerFamily(b.name) }
func (d cancelAfterUploadDefinition) TrackerFamily() Family  { return testTrackerFamily(d.name) }
func (s hostAwareDescriptionDefinition) TrackerFamily() Family {
	return testTrackerFamily(s.name)
}
func (testHDBPreparationDefinition) TrackerFamily() Family { return FamilyStandalone }

func (s stubDryRunDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker(s.name)
}
func (s stubUploadArtifactDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker(s.name)
}
func (s stubPreparationDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker(s.name)
}
func (t trackingUploadDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker(t.name)
}
func (b blockingUploadDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker(b.name)
}
func (d cancelAfterUploadDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker(d.name)
}
func (s hostAwareDescriptionDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker(s.name)
}
func (testHDBPreparationDefinition) ImageHostPolicy() *ImageHostPolicy {
	return testImageHostPolicyForTracker("HDB")
}

func (b blockingUploadDefinition) Name() string {
	return b.name
}

func (b blockingUploadDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, b)
}

func (b blockingUploadDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	if b.started != nil {
		b.started <- b.name
	}
	if b.release != nil {
		<-b.release
	}
	if b.uploadErr != nil {
		return api.UploadSummary{}, b.uploadErr
	}
	return api.UploadSummary{Uploaded: b.uploaded}, nil
}

func (d cancelAfterUploadDefinition) Name() string {
	return d.name
}

func (d cancelAfterUploadDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, d)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (d cancelAfterUploadDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	if d.cancel != nil {
		d.cancel()
	}
	return api.UploadSummary{Uploaded: d.uploaded, UploadedTorrents: d.uploadedTorrents}, nil
}

type failingStatusUpdateRepo struct {
	stubRepo
	failTracker string
	failStatus  string
	failed      bool
}

func (r *failingStatusUpdateRepo) UpdateLatestUploadRecordStatus(_ context.Context, _ string, tracker string, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.statusUpdates = append(r.statusUpdates, uploadStatusUpdate{tracker: tracker, status: status})
	if !r.failed && tracker == r.failTracker && status == r.failStatus {
		r.failed = true
		return errors.New("status update failed")
	}
	return nil
}

type recordingStatusContextRepo struct {
	stubRepo
	cancel  context.CancelFunc
	updates []recordedStatusContext
}

type recordedStatusContext struct {
	tracker     string
	status      string
	ctxErr      error
	hasDeadline bool
	deadline    time.Time
}

func (r *recordingStatusContextRepo) UpdateLatestUploadRecordStatus(ctx context.Context, _ string, tracker string, status string) error {
	if r.cancel != nil {
		r.cancel()
	}
	deadline, hasDeadline := ctx.Deadline()
	update := recordedStatusContext{
		tracker:     tracker,
		status:      status,
		ctxErr:      ctx.Err(),
		hasDeadline: hasDeadline,
		deadline:    deadline,
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, update)
	if update.ctxErr != nil {
		return update.ctxErr
	}
	return nil
}

type transientStatusUpdateRepo struct {
	*dbsvc.SQLiteRepository
	mu          sync.Mutex
	failTracker string
	failStatus  string
	failed      bool
}

func (r *transientStatusUpdateRepo) UpdateLatestUploadRecordStatus(ctx context.Context, sourcePath string, tracker string, status string) error {
	r.mu.Lock()
	shouldFail := !r.failed && tracker == r.failTracker && status == r.failStatus
	if shouldFail {
		r.failed = true
	}
	r.mu.Unlock()
	if shouldFail {
		return errors.New("transient status update failure")
	}
	if err := r.SQLiteRepository.UpdateLatestUploadRecordStatus(ctx, sourcePath, tracker, status); err != nil {
		return fmt.Errorf("update latest upload record status: %w", err)
	}
	return nil
}

func (s stubUploadArtifactDefinition) Name() string {
	return s.name
}

func (s stubUploadArtifactDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, s)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (s stubUploadArtifactDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{
			{
				Tracker:     s.name,
				TorrentID:   "374352",
				DownloadURL: "https://aither.cc/torrent/download/374352.382",
				TorrentURL:  "https://aither.cc/torrents/374352",
			},
		},
	}, nil
}

func (s stubPreparationDefinition) Name() string {
	return s.name
}

func (s stubPreparationDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, s)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (s stubPreparationDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

//nolint:unparam // Error is required by the adapter description callback contract.
func (s stubPreparationDefinition) prepareDescription(context.Context, PreparationInput) (DescriptionResult, error) {
	return DescriptionResult{Group: s.group, Description: s.description}, nil
}

func (s *blockingImageService) ListCandidates(context.Context, api.ImageHostingSubject) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (s *blockingImageService) Upload(ctx context.Context, meta api.ImageHostingSubject, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	s.mu.Lock()
	s.calls = append(s.calls, host)
	s.mu.Unlock()
	if s.started != nil {
		select {
		case s.started <- host:
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		}
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		}
	}
	results := make([]api.UploadedImageLink, 0, len(images))
	for idx, image := range images {
		results = append(results, api.UploadedImageLink{
			SourcePath: meta.SourcePath,
			ImagePath:  image.Path,
			Host:       host,
			UsageScope: usageScope,
			ImgURL:     fmt.Sprintf("https://%s/%d.png", host, idx),
			RawURL:     fmt.Sprintf("https://%s/%d.png", host, idx),
			WebURL:     fmt.Sprintf("https://%s/%d", host, idx),
		})
	}
	if s.repo != nil {
		s.repo.mu.Lock()
		s.repo.uploads = append(s.repo.uploads, results...)
		s.repo.mu.Unlock()
	}
	return results, nil
}

func (s *blockingImageService) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

type hostAwareDescriptionDefinition struct {
	name  string
	group string
}

func (hostAwareDescriptionDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (s hostAwareDescriptionDefinition) Name() string {
	return s.name
}

func (s hostAwareDescriptionDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, s)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (s hostAwareDescriptionDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

//nolint:unparam // Error is required by the adapter description callback contract.
func (s hostAwareDescriptionDefinition) prepareDescription(_ context.Context, req PreparationInput) (DescriptionResult, error) {
	host := "none"
	if req.Assets != nil && len(req.Assets.Screenshots) > 0 {
		host = strings.ToLower(strings.TrimSpace(req.Assets.Screenshots[0].Host))
		if host == "" {
			host = "none"
		}
	}

	return DescriptionResult{
		Group:       s.group,
		Description: "unit3d screenshot host: " + host,
	}, nil
}

func (testHDBPreparationDefinition) Name() string {
	return "HDB"
}

func (d testHDBPreparationDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, d)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (testHDBPreparationDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

//nolint:unparam // Error is required by the adapter description callback contract.
func (testHDBPreparationDefinition) prepareDescription(_ context.Context, req PreparationInput) (DescriptionResult, error) {
	assets := DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	}
	var description strings.Builder
	description.WriteString(assets.Description)
	for _, image := range assets.Screenshots {
		if strings.TrimSpace(image.RawURL) != "" {
			description.WriteString("\n[img]")
			description.WriteString(image.RawURL)
			description.WriteString("[/img]")
		}
	}
	return DescriptionResult{Group: "hdb", Description: description.String()}, nil
}

func (t trackingUploadDefinition) Name() string {
	return t.name
}

func (t trackingUploadDefinition) UploadArtifactPolicy() *UploadArtifactPolicy {
	switch t.name {
	case "HDB":
		return &UploadArtifactPolicy{Source: "HDBits"}
	case "PTP":
		return &UploadArtifactPolicy{Source: "PTP"}
	default:
		return nil
	}
}

func (t trackingUploadDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, t)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (t trackingUploadDefinition) submit(_ context.Context, req PreparationInput) (api.UploadSummary, error) {
	if t.started != nil {
		t.started <- t.name
	}
	if t.requests != nil {
		t.requests <- req
	}
	if t.release != nil {
		<-t.release
	}
	return api.UploadSummary{Uploaded: 1}, nil
}

func (s stubDryRunDefinition) Name() string {
	return s.name
}

func (s stubDryRunDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	if s.intents != nil {
		*s.intents = append(*s.intents, input.Intent)
	}
	if s.inputs != nil {
		*s.inputs = append(*s.inputs, input)
	}
	return prepareTestDefinition(ctx, input, s)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (s stubDryRunDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

func (s stubDryRunDefinition) prepareDryRun(context.Context, PreparationInput) (api.TrackerDryRunEntry, error) {
	if s.dryRunErr != nil {
		return api.TrackerDryRunEntry{}, s.dryRunErr
	}
	return api.TrackerDryRunEntry{
		Tracker: s.name,
		Status:  "ready",
		Payload: map[string]string{"category": "movie"},
	}, nil
}

func TestBuildUploadDryRunUsesBuilder(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "BLU"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"}, []string{"BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Tracker != "BLU" {
		t.Fatalf("expected BLU tracker, got %q", entries[0].Tracker)
	}
	if entries[0].Payload["category"] != "movie" {
		t.Fatalf("expected payload category movie, got %q", entries[0].Payload["category"])
	}
}

func TestBuildUploadPreviewUsesTypedReviewAndDryRunIntents(t *testing.T) {
	t.Parallel()

	intents := make([]PreparationIntent, 0, 2)
	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "BLU", intents: &intents}); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	subject := api.UploadSubject{SourcePath: "/tmp/file"}
	if _, err := svc.BuildUploadReview(context.Background(), subject, []string{"BLU"}); err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if _, err := svc.BuildUploadDryRun(context.Background(), subject, []string{"BLU"}); err != nil {
		t.Fatalf("build explicit dry run: %v", err)
	}
	if !slices.Equal(intents, []PreparationIntent{PreparationIntentUploadReview, PreparationIntentDryRun}) {
		t.Fatalf("preparation intents = %#v", intents)
	}
}

func TestBuildPreparationSkipsAllNonDescriptionContent(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubDryRunDefinition{name: "NONE", mode: UploadContentModeNone},
		stubDryRunDefinition{name: "IMAGES", mode: UploadContentModeScreenshots},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}
	repo := &stubRepo{
		trackerRecordsErr:      errors.New("tracker metadata unavailable"),
		selectionsErr:          errors.New("selections unavailable"),
		screenshotSlotsErr:     errors.New("slots unavailable"),
		uploadsErr:             errors.New("uploads unavailable"),
		descriptionOverrideErr: errors.New("overrides unavailable"),
	}
	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)

	preview, err := svc.BuildPreparation(
		context.Background(),
		api.NewDescriptionSubject(api.UploadSubject{SourcePath: "/tmp/source"}),
		[]string{"NONE", "IMAGES"},
	)
	if err != nil {
		t.Fatalf("build preparation: %v", err)
	}
	if len(preview.Descriptions) != 0 || len(preview.ContentFailures) != 0 {
		t.Fatalf("expected empty non-description preview, got %#v", preview)
	}
	if repo.trackerRecordsCalls != 0 || repo.selectionsCalls != 0 || repo.screenshotSlotCalls != 0 || repo.uploadsCalls != 0 || repo.overrideCalls != 0 {
		t.Fatalf("expected no content repository reads, got tracker=%d selections=%d slots=%d uploads=%d overrides=%d",
			repo.trackerRecordsCalls,
			repo.selectionsCalls,
			repo.screenshotSlotCalls,
			repo.uploadsCalls,
			repo.overrideCalls,
		)
	}
}

func TestBuildUploadDryRunNoneModeSkipsSharedContent(t *testing.T) {
	t.Parallel()

	inputs := make([]PreparationInput, 0, 1)
	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{
name: "NONE",
 mode: UploadContentModeNone,
 inputs: &inputs,
}); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	repo := &stubRepo{
		trackerRecordsErr:      errors.New("tracker metadata unavailable"),
		selectionsErr:          errors.New("selections unavailable"),
		screenshotSlotsErr:     errors.New("slots unavailable"),
		uploadsErr:             errors.New("uploads unavailable"),
		descriptionOverrideErr: errors.New("overrides unavailable"),
	}
	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)

	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{SourcePath: "/tmp/source"}, []string{"NONE"})
	if err != nil {
		t.Fatalf("build dry run: %v", err)
	}
	if len(entries) != 1 || entries[0].Status != "ready" {
		t.Fatalf("expected ready none-mode preview, got %#v", entries)
	}
	if len(inputs) != 1 || inputs[0].Assets != nil {
		t.Fatalf("expected adapter invocation without shared assets, got %#v", inputs)
	}
	if repo.trackerRecordsCalls != 0 || repo.selectionsCalls != 0 || repo.screenshotSlotCalls != 0 || repo.uploadsCalls != 0 || repo.overrideCalls != 0 {
		t.Fatalf("expected no content repository reads, got tracker=%d selections=%d slots=%d uploads=%d overrides=%d",
			repo.trackerRecordsCalls,
			repo.selectionsCalls,
			repo.screenshotSlotCalls,
			repo.uploadsCalls,
			repo.overrideCalls,
		)
	}
}

func TestBuildUploadDryRunDistinguishesReadyEmptyAndFailedScreenshots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		repo       *stubRepo
		wantStatus string
		wantCalls  int
	}{
		{
name: "ready empty",
 repo: &stubRepo{},
 wantStatus: "ready",
 wantCalls: 1,
},
		{
name: "failed",
 repo: &stubRepo{selectionsErr: errors.New("selections unavailable")},
 wantStatus: "blocked",
 wantCalls: 0,
},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			inputs := make([]PreparationInput, 0, 1)
			registry := NewRegistry()
			if err := registry.Register(stubDryRunDefinition{
name: "IMAGES",
 mode: UploadContentModeScreenshots,
 inputs: &inputs,
}); err != nil {
				t.Fatalf("register stub: %v", err)
			}
			svc := NewServiceWithRegistry(config.Config{}, nil, test.repo, registry)

			entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{SourcePath: "/tmp/source"}, []string{"IMAGES"})
			if err != nil {
				t.Fatalf("build dry run: %v", err)
			}
			if len(entries) != 1 || entries[0].Status != test.wantStatus {
				t.Fatalf("expected status %q, got %#v", test.wantStatus, entries)
			}
			if len(inputs) != test.wantCalls {
				t.Fatalf("adapter calls = %d, want %d", len(inputs), test.wantCalls)
			}
			if test.wantStatus == "ready" {
				if inputs[0].Assets == nil || len(inputs[0].Assets.Screenshots) != 0 || strings.TrimSpace(inputs[0].Assets.Description) != "" {
					t.Fatalf("expected ready empty screenshot assets, got %#v", inputs[0].Assets)
				}
				if test.repo.overrideCalls != 0 {
					t.Fatalf("screenshot mode loaded description overrides %d time(s)", test.repo.overrideCalls)
				}
			} else if entries[0].ContentFailure == nil || entries[0].ContentFailure.Code != api.TrackerEligibilityScreenshotPreparationFailed {
				t.Fatalf("expected structured screenshot failure, got %#v", entries[0].ContentFailure)
			}
		})
	}
}

func TestBuildUploadDryRunScopesDescriptionPreloadFailure(t *testing.T) {
	t.Parallel()

	screenshotInputs := make([]PreparationInput, 0, 1)
	descriptionInputs := make([]PreparationInput, 0, 1)
	registry := NewRegistry()
	for _, definition := range []Definition{
		stubDryRunDefinition{
name: "IMAGES",
 mode: UploadContentModeScreenshots,
 inputs: &screenshotInputs,
},
		stubDryRunDefinition{
name: "DESCRIPTION",
 mode: UploadContentModeDescription,
 inputs: &descriptionInputs,
},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}
	repo := &stubRepo{descriptionOverrideErr: errors.New("description overrides unavailable")}
	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)

	entries, err := svc.BuildUploadDryRun(
		context.Background(),
		api.UploadSubject{SourcePath: "/tmp/source"},
		[]string{"IMAGES", "DESCRIPTION"},
	)
	if err != nil {
		t.Fatalf("build dry run: %v", err)
	}
	if len(entries) != 2 || entries[0].Tracker != "IMAGES" || entries[0].Status != "ready" || entries[1].Tracker != "DESCRIPTION" || entries[1].Status != "blocked" {
		t.Fatalf("unexpected mixed-mode results: %#v", entries)
	}
	if len(screenshotInputs) != 1 || len(descriptionInputs) != 0 {
		t.Fatalf("expected only screenshot adapter invocation, screenshots=%d descriptions=%d", len(screenshotInputs), len(descriptionInputs))
	}
	if entries[1].ContentFailure == nil || entries[1].ContentFailure.Code != api.TrackerEligibilityDescriptionPreparationFailed {
		t.Fatalf("expected structured description failure, got %#v", entries[1].ContentFailure)
	}
}

func TestBuildUploadDryRunBlocksWhenImageHostFallbacksFail(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "PTP"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	images := &stubImageService{
		errs: map[string]error{
			"pixhost": errors.New("pixhost unavailable"),
		},
	}
	svc := NewServiceWithRegistryAndImages(config.Config{}, nil, repo, registry, images)

	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{SourcePath: "/tmp/source"}, []string{"PTP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked dry run, got %#v", entry)
	}
	if entry.ImageHost.Status != "blocked" || len(entry.ImageHost.Warnings) != 1 {
		t.Fatalf("expected blocked image host feedback, got %#v", entry.ImageHost)
	}
	if entry.ContentFailure == nil || entry.ContentFailure.Code != api.TrackerEligibilityDescriptionPreparationFailed {
		t.Fatalf("expected structured description failure, got %#v", entry.ContentFailure)
	}
}

func TestBuildPreparationBlocksWhenImageHostFallbacksFail(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubPreparationDefinition{
		name:        "PTP",
		group:       "ptp",
		description: "saveable description",
	}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	images := &stubImageService{
		errs: map[string]error{
			"pixhost": errors.New("pixhost unavailable"),
		},
	}
	svc := NewServiceWithRegistryAndImages(config.Config{}, nil, repo, registry, images)

	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{SourcePath: "/tmp/source"}), []string{"PTP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 0 {
		t.Fatalf("expected no saveable description, got %d", len(preview.Descriptions))
	}
	if len(preview.ContentFailures) != 1 {
		t.Fatalf("expected 1 structured content failure, got %#v", preview.ContentFailures)
	}
	failure := preview.ContentFailures[0]
	if failure.Tracker != "PTP" || failure.Code != api.TrackerEligibilityDescriptionPreparationFailed {
		t.Fatalf("expected PTP description failure, got %#v", failure)
	}
	if !strings.Contains(failure.Message, "could not upload screenshots") {
		t.Fatalf("expected blocking image host message, got %q", failure.Message)
	}
	if got := strings.Join(images.calls, ","); got != "pixhost" {
		t.Fatalf("expected one preflight upload attempt, got %q", got)
	}
}

func TestBuildPreparationBlocksWhenUploadedImagesDoNotCoverRHDSlots(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubPreparationDefinition{
		name:        "RHD",
		group:       "rhd",
		description: "saveable description",
	}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	imagePath := filepath.Join(t.TempDir(), "screen.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	repo := &stubRepo{
		screenshotSlots: []api.ScreenshotSlot{
			{
				SourcePath:          sourcePath,
				SlotOrder:           0,
				SourceKind:          screenshotSlotSourceSelection,
				SectionKind:         screenshotSectionWrapped,
				RenderInScreenshots: true,
			},
			{
				SourcePath:          sourcePath,
				SlotOrder:           1,
				SourceKind:          screenshotSlotSourceSelection,
				OriginalKey:         imagePath,
				ImagePath:           imagePath,
				SectionKind:         screenshotSectionWrapped,
				RenderInScreenshots: true,
			},
		},
	}
	images := &stubImageService{}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"RHD": {ImageHost: "imgbb"},
			},
		},
	}
	svc := NewServiceWithRegistryAndImages(cfg, nil, repo, registry, images)

	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{SourcePath: sourcePath}), []string{"RHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 0 {
		t.Fatalf("expected no saveable description, got %d", len(preview.Descriptions))
	}
	if len(preview.ContentFailures) != 1 {
		t.Fatalf("expected 1 structured content failure, got %#v", preview.ContentFailures)
	}
	failure := preview.ContentFailures[0]
	if failure.Tracker != "RHD" || failure.Code != api.TrackerEligibilityDescriptionPreparationFailed {
		t.Fatalf("expected RHD description failure, got %#v", failure)
	}
	if !strings.Contains(failure.Message, "missing screenshot variant for slot 0") {
		t.Fatalf("expected slot coverage failure, got %#v", failure)
	}
	if got := strings.Join(images.calls, ","); got != "imgbb" {
		t.Fatalf("expected one imgbb upload attempt, got %q", got)
	}
}

func TestBuildUploadDryRunIncludesRuleFailedTrackers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "BLU"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{
		SourcePath: "/tmp/file",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"BLU": {{Rule: "require_movie_only", Reason: "movie only"}},
		},
	}, []string{"BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Tracker != "BLU" {
		t.Fatalf("expected BLU tracker, got %q", entries[0].Tracker)
	}
}

func TestBuildUploadDryRunUsesRequiredPreparationContract(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "BLU"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"}, []string{"BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Status != "ready" {
		t.Fatalf("expected ready status, got %q", entries[0].Status)
	}
}

func TestBuildPreparationGroupsExactMatchingUnit3DDescriptions(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "AITHER",
			group:       "unit3d",
			description: "same description",
		},
		stubPreparationDefinition{
			name:        "HHD",
			group:       "unit3d",
			description: "same description",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{}), []string{"AITHER", "HHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected exact matching unit3d descriptions to group, got %d groups", len(preview.Descriptions))
	}
	group := preview.Descriptions[0]
	if group.GroupKey != "unit3d" {
		t.Fatalf("expected canonical unit3d group key, got %q", group.GroupKey)
	}
	if got := strings.Join(group.Trackers, ","); got != "AITHER,HHD" {
		t.Fatalf("expected both trackers in group, got %q", got)
	}
}

func TestBuildPreparationSplitsSameGroupWhenDescriptionDiffers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "AITHER",
			group:       "unit3d",
			description: "aither description",
		},
		stubPreparationDefinition{
			name:        "HHD",
			group:       "unit3d",
			description: "hhd description",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{}), []string{"AITHER", "HHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 2 {
		t.Fatalf("expected mismatched descriptions to split, got %d groups", len(preview.Descriptions))
	}
	if preview.Descriptions[0].GroupKey == preview.Descriptions[1].GroupKey {
		t.Fatalf("expected unique group keys, got %q", preview.Descriptions[0].GroupKey)
	}
	if got := strings.Join(preview.Descriptions[0].Trackers, ","); got != "AITHER" {
		t.Fatalf("expected first group to contain AITHER, got %q", got)
	}
	if got := strings.Join(preview.Descriptions[1].Trackers, ","); got != "HHD" {
		t.Fatalf("expected second group to contain HHD, got %q", got)
	}
	if preview.Descriptions[0].GroupKey != "unit3d|aither|tracker:AITHER" {
		t.Fatalf("expected stable AITHER group key, got %q", preview.Descriptions[0].GroupKey)
	}
	if preview.Descriptions[1].GroupKey != "unit3d|hhd|tracker:HHD" {
		t.Fatalf("expected stable HHD group key, got %q", preview.Descriptions[1].GroupKey)
	}

	reversed, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{}), []string{"HHD", "AITHER"})
	if err != nil {
		t.Fatalf("unexpected reversed error: %v", err)
	}
	groupKeyByTracker := make(map[string]string, len(reversed.Descriptions))
	for _, group := range reversed.Descriptions {
		for _, tracker := range group.Trackers {
			groupKeyByTracker[tracker] = group.GroupKey
		}
	}
	if groupKeyByTracker["AITHER"] != "unit3d|aither|tracker:AITHER" {
		t.Fatalf("expected reversed AITHER stable group key, got %q", groupKeyByTracker["AITHER"])
	}
	if groupKeyByTracker["HHD"] != "unit3d|hhd|tracker:HHD" {
		t.Fatalf("expected reversed HHD stable group key, got %q", groupKeyByTracker["HHD"])
	}
}

func TestBuildPreparationGroupsSameFinalDescriptionWhenExtractedDescriptionDiffers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "AITHER",
			group:       "unit3d",
			description: "same final description",
		},
		stubPreparationDefinition{
			name:        "BLU",
			group:       "unit3d",
			description: "same final description",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "AITHER", Description: "aither raw description"},
			{Tracker: "BLU", Description: "blu raw description"},
		},
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{SourcePath: sourcePath}), []string{"AITHER", "BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected matching final description to group, got %d groups", len(preview.Descriptions))
	}
	if preview.Descriptions[0].RawDescription != "same final description" {
		t.Fatalf("expected canonical raw description to be final build, got %q", preview.Descriptions[0].RawDescription)
	}
	if got := strings.Join(preview.Descriptions[0].Trackers, ","); got != "AITHER,BLU" {
		t.Fatalf("expected both trackers in grouped final description, got %q", got)
	}
}

func TestBuildPreparationGroupsSameDescriptionWhenImageHostFeedbackDiffers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "AITHER",
			group:       "shared",
			description: "same description",
		},
		stubPreparationDefinition{
			name:        "PTP",
			group:       "shared",
			description: "same description",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{}), []string{"AITHER", "PTP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected matching descriptions to group despite image-host feedback mismatch, got %d groups", len(preview.Descriptions))
	}
	if got := strings.Join(preview.Descriptions[0].Trackers, ","); got != "AITHER,PTP" {
		t.Fatalf("expected both trackers in group, got %q", got)
	}
	if len(preview.Descriptions[0].ImageHost.AllowedHosts) == 0 {
		t.Fatalf("expected PTP image host policy to be captured")
	}
}

func TestBuildPreparationGroupsUnit3DWhenImageHostMessageOnlyDiffers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "AITHER",
			group:       "unit3d",
			description: "same description",
		},
		stubPreparationDefinition{
			name:        "HHD",
			group:       "unit3d",
			description: "same description",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {ImageHost: "imgbox", ImgRehost: true},
				"HHD":    {ImageHost: "imgbox", ImgRehost: true},
			},
		},
	}
	repo := &stubRepo{}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{SourcePath: sourcePath}), []string{"AITHER", "HHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected image-host message-only diff to group, got %d groups", len(preview.Descriptions))
	}
	if got := strings.Join(preview.Descriptions[0].Trackers, ","); got != "AITHER,HHD" {
		t.Fatalf("expected both unit3d trackers in group, got %q", got)
	}
	if preview.Descriptions[0].ImageHost.Status != "warning" {
		t.Fatalf("expected image host warning status, got %q", preview.Descriptions[0].ImageHost.Status)
	}
	if len(preview.Descriptions[0].ImageHost.AllowedHosts) != 0 {
		t.Fatalf("expected unrestricted configured-host policy, got %#v", preview.Descriptions[0].ImageHost.AllowedHosts)
	}
}

func TestBuildPreparationUnit3DGroupsOnConfiguredHostPreference(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		hostAwareDescriptionDefinition{name: "HHD", group: "unit3d"},
		hostAwareDescriptionDefinition{name: "LUME", group: "unit3d"},
		hostAwareDescriptionDefinition{name: "RAS", group: "unit3d"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	imagePath := filepath.Join(t.TempDir(), "screen.png")
	olderUpload := time.Now().Add(-time.Hour)
	newerUpload := time.Now()
	repo := &stubRepo{
		screenshotSlots: []api.ScreenshotSlot{
			{
				SourcePath:          sourcePath,
				SlotOrder:           0,
				SourceKind:          screenshotSlotSourceSelection,
				ImagePath:           imagePath,
				RenderInScreenshots: true,
				Variants: []api.ScreenshotSlotVariant{
					{
						SourcePath: sourcePath,
						SlotOrder:  0,
						Host:       "pixhost",
						UsageScope: globalImageUsageScope,
						ImagePath:  imagePath,
						ImgURL:     "https://pixhost/old.png",
						RawURL:     "https://pixhost/raw-old.png",
						WebURL:     "https://pixhost/old",
						UploadedAt: olderUpload,
					},
					{
						SourcePath: sourcePath,
						SlotOrder:  0,
						Host:       "imgbb",
						UsageScope: globalImageUsageScope,
						ImagePath:  imagePath,
						ImgURL:     "https://imgbb/new.png",
						RawURL:     "https://imgbb/raw-new.png",
						WebURL:     "https://imgbb/new",
						UploadedAt: newerUpload,
					},
				},
			},
		},
		uploads: []api.UploadedImageLink{
			{
				Host:       "pixhost",
				UsageScope: globalImageUsageScope,
				ImagePath:  imagePath,
				ImgURL:     "https://pixhost/old.png",
				RawURL:     "https://pixhost/raw-old.png",
				WebURL:     "https://pixhost/old",
				UploadedAt: olderUpload,
			},
			{
				Host:       "imgbb",
				UsageScope: globalImageUsageScope,
				ImagePath:  imagePath,
				ImgURL:     "https://imgbb/new.png",
				RawURL:     "https://imgbb/raw-new.png",
				WebURL:     "https://imgbb/new",
				UploadedAt: newerUpload,
			},
		},
	}

	cfg := config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "pixhost",
			Host2: "imgbb",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"HHD": {ImageHost: "pixhost", ImgRehost: true},
			},
		},
	}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{SourcePath: sourcePath}), []string{"HHD", "LUME", "RAS"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected configured-host preference to collapse HHD+LUME+RAS into one group, got %d", len(preview.Descriptions))
	}
	if got := strings.Join(preview.Descriptions[0].Trackers, ","); got != "HHD,LUME,RAS" {
		t.Fatalf("expected both unit3d trackers in group, got %q", got)
	}
	if preview.Descriptions[0].GroupKey != "unit3d|pixhost|global" {
		t.Fatalf("expected preferred pixhost group key, got %q", preview.Descriptions[0].GroupKey)
	}
	if preview.Descriptions[0].Description != "unit3d screenshot host: pixhost" {
		t.Fatalf("expected host-resolved description, got %q", preview.Descriptions[0].Description)
	}
}

func TestPreparationDescriptionGroupMergesUnit3DHostVariantsWhenDescriptionMatches(t *testing.T) {
	t.Parallel()

	grouped := make(map[string]*api.PreparationDescription)
	order := make([]string, 0, 2)
	first := api.PreparationDescription{
		RawDescription:     "raw",
		RawDescriptionHTML: "<p>raw</p>",
		Description:        "same description",
		DescriptionHTML:    "<p>same description</p>",
		ImageHost: api.ImageHostFeedback{
			Status:       "reused",
			SelectedHost: "pixhost",
			AllowedHosts: []string{"pixhost"},
		},
	}
	second := first
	second.ImageHost = api.ImageHostFeedback{
		Status:       "reused",
		SelectedHost: "imgbb",
		AllowedHosts: []string{"imgbb"},
	}
	second.RawDescriptionHTML = "<div>raw</div>"
	second.DescriptionHTML = "<div>same description</div>"

	firstEntry := preparationDescriptionGroup(grouped, &order, "unit3d|pixhost|global", first)
	firstEntry.Trackers = append(firstEntry.Trackers, "HHD")
	secondEntry := preparationDescriptionGroup(grouped, &order, "unit3d|imgbb|global", second)
	secondEntry.Trackers = append(secondEntry.Trackers, "LUME")
	secondEntry.ImageHost = mergePreparationImageHostFeedback(secondEntry.ImageHost, second.ImageHost)

	if firstEntry != secondEntry {
		t.Fatal("expected matching unit3d descriptions to share one group across image-host key variants")
	}
	if len(order) != 1 {
		t.Fatalf("expected one ordered group, got %d", len(order))
	}
	if got := strings.Join(firstEntry.Trackers, ","); got != "HHD,LUME" {
		t.Fatalf("expected both trackers in merged group, got %q", got)
	}
	if len(firstEntry.ImageHost.AllowedHosts) != 2 {
		t.Fatalf("expected merged image host policy data, got %#v", firstEntry.ImageHost.AllowedHosts)
	}
}

func TestApplyTrackerConfigOverrides(t *testing.T) {
	t.Parallel()

	channel := "spd"
	trueValue := true

	cfg := applyTrackerConfigOverrides(config.TrackerConfig{}, api.TrackerConfigOverrides{
		Anon:    &trueValue,
		Draft:   &trueValue,
		ModQ:    &trueValue,
		Channel: &channel,
	})

	if !cfg.Anon {
		t.Fatalf("expected anon override")
	}
	if !cfg.Draft {
		t.Fatalf("expected draft override")
	}
	if !cfg.ModQ {
		t.Fatalf("expected modq override")
	}
	if cfg.Channel != "spd" {
		t.Fatalf("expected channel override, got %q", cfg.Channel)
	}
}

func TestUploadAggregatesUploadedTorrentArtifacts(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubUploadArtifactDefinition{name: "AITHER"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected 1 upload, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected 1 uploaded torrent artifact, got %d", len(summary.UploadedTorrents))
	}
	if summary.UploadedTorrents[0].DownloadURL != "https://aither.cc/torrent/download/374352.382" {
		t.Fatalf("unexpected download URL: %q", summary.UploadedTorrents[0].DownloadURL)
	}
}

func TestUploadPreflightsMultipleConfiguredImageHostsOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubUploadArtifactDefinition{name: "PTP"},
		stubUploadArtifactDefinition{name: "STC"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	images := &stubImageService{repo: repo}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			DefaultTrackers: config.CSVList{"PTP", "STC"},
			Trackers: map[string]config.TrackerConfig{
				"PTP": {ImageHost: "pixhost"},
				"STC": {ImageHost: "imgbox"},
			},
		},
	}
	svc := NewServiceWithRegistryAndImages(cfg, nil, repo, registry, images)

	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/source"})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if summary.Uploaded != 2 {
		t.Fatalf("expected 2 tracker uploads, got %d", summary.Uploaded)
	}
	firstRunCalls := append([]string{}, images.calls...)
	sort.Strings(firstRunCalls)
	if got := strings.Join(firstRunCalls, ","); got != "imgbox,pixhost" {
		t.Fatalf("expected one upload per configured host, got %q", got)
	}

	summary, err = svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/source"})
	if err != nil {
		t.Fatalf("unexpected second upload error: %v", err)
	}
	if summary.Uploaded != 2 {
		t.Fatalf("expected 2 tracker uploads on second run, got %d", summary.Uploaded)
	}
	secondRunCalls := append([]string{}, images.calls...)
	sort.Strings(secondRunCalls)
	if got := strings.Join(secondRunCalls, ","); got != "imgbox,pixhost" {
		t.Fatalf("expected existing host variants to be reused on second run, got calls %q", got)
	}
}

func TestUploadPreflightsUnrestrictedTrackerToFirstConfiguredImageHost(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubUploadArtifactDefinition{name: "RHD"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	images := &blockingImageService{repo: repo}
	cfg := config.Config{
		ImageHosting: config.ImageHostingConfig{Host1: "imgbb", Host2: "pixhost"},
		Trackers:     config.TrackersConfig{DefaultTrackers: config.CSVList{"RHD"}},
	}
	svc := NewServiceWithRegistryAndImages(cfg, nil, repo, registry, images)

	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/source"})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected 1 tracker upload, got %d", summary.Uploaded)
	}
	calls := images.Calls()
	if len(calls) != 1 || calls[0] != "imgbb" {
		t.Fatalf("expected unrestricted tracker to upload screenshots to first configured host, got %v", calls)
	}
}

func TestUploadPreparesDistinctTrackerArtifactsBeforeConcurrentUploads(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Movie.mkv")
	torrentPath := filepath.Join(tmp, "release.torrent")
	createServiceTestTorrent(t, filepath.Join(tmp, "source.bin"), torrentPath)

	requests := make(chan PreparationInput, 2)
	release := make(chan struct{})
	registry := NewRegistry()
	for _, definition := range []Definition{
		trackingUploadDefinition{
			name:     "HDB",
			requests: requests,
			release:  release,
		},
		trackingUploadDefinition{
			name:     "PTP",
			requests: requests,
			release:  release,
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")},
		Trackers: config.TrackersConfig{
			DefaultTrackers: config.CSVList{"HDB", "PTP"},
			Trackers: map[string]config.TrackerConfig{
				"HDB": {AnnounceURL: "https://hdb.example/passkey/announce"},
				"PTP": {AnnounceURL: "https://ptp.example/passkey/announce"},
			},
		},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	done := make(chan struct {
		summary api.UploadSummary
		err     error
	}, 1)
	progress := make(chan api.UploadProgressUpdate, 32)
	uploadCtx := api.WithUploadProgressReporter(context.Background(), func(update api.UploadProgressUpdate) { progress <- update })
	go func() {
		summary, err := svc.Upload(uploadCtx, api.UploadSubject{
			SourcePath:  sourcePath,
			TorrentPath: torrentPath,
		})
		done <- struct {
			summary api.UploadSummary
			err     error
		}{summary: summary, err: err}
	}()

	received := make(map[string]PreparationInput, 2)
	updates := make([]api.UploadProgressUpdate, 0)
	timeout := time.After(10 * time.Second)
	for len(received) < 2 {
		select {
		case req := <-requests:
			received[req.Tracker] = req
		case result := <-done:
			t.Fatalf("upload returned before both submissions: summary=%#v err=%v received=%v", result.summary, result.err, received)
		case update := <-progress:
			updates = append(updates, update)
		case <-timeout:
			t.Fatalf("timed out waiting for upload requests, got %d updates=%#v", len(received), updates)
		}
	}

	hdbReq := received["HDB"]
	ptpReq := received["PTP"]
	if hdbReq.Meta.TorrentPath == ptpReq.Meta.TorrentPath {
		t.Fatalf("expected distinct tracker artifact paths, got %q", hdbReq.Meta.TorrentPath)
	}
	assertTrackerArtifact(t, hdbReq.Meta.TorrentPath, "https://hdb.example/passkey/announce", "HDBits")
	assertTrackerArtifact(t, ptpReq.Meta.TorrentPath, "https://ptp.example/passkey/announce", "PTP")

	close(release)

	result := <-done
	if result.err != nil {
		t.Fatalf("unexpected upload error: %v", result.err)
	}
	if result.summary.Uploaded != 2 {
		t.Fatalf("expected 2 uploads, got %d", result.summary.Uploaded)
	}
}

func TestBuildPreparationPreflightsMultipleConfiguredImageHostsConcurrently(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		hostAwareDescriptionDefinition{name: "PTP", group: "ptp"},
		hostAwareDescriptionDefinition{name: "STC", group: "stc"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	images := &blockingImageService{started: started, release: release}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {ImageHost: "pixhost"},
				"STC": {ImageHost: "imgbox"},
			},
		},
	}
	svc := NewServiceWithRegistryAndImages(cfg, nil, repo, registry, images)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := svc.BuildPreparation(ctx, api.NewDescriptionSubject(api.UploadSubject{SourcePath: "/tmp/source"}), []string{"PTP", "STC"})
		done <- err
	}()

	seen := make(map[string]struct{}, 2)
	for len(seen) < 2 {
		select {
		case host := <-started:
			seen[host] = struct{}{}
		case <-ctx.Done():
			t.Fatalf("expected both host uploads to start concurrently, saw %v: %v", seen, ctx.Err())
		}
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected preparation error: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("preparation did not finish after concurrent uploads released: %v", ctx.Err())
	}

	calls := images.Calls()
	sort.Strings(calls)
	if got := strings.Join(calls, ","); got != "imgbox,pixhost" {
		t.Fatalf("expected one preflight upload per configured host, got %q", got)
	}
}

func TestUploadRunsTrackersConcurrentlyWithLimit(t *testing.T) {
	t.Parallel()

	started := make(chan string, 3)
	release := make(chan struct{})

	registry := NewRegistry()
	definitions := []Definition{
		blockingUploadDefinition{
			name:     "BLU",
			started:  started,
			release:  release,
			uploaded: 1,
		},
		blockingUploadDefinition{
			name:     "BHD",
			started:  started,
			release:  release,
			uploaded: 1,
		},
		blockingUploadDefinition{
			name:     "AITHER",
			started:  started,
			release:  release,
			uploaded: 1,
		},
	}
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 2},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD", "AITHER"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	type uploadResult struct {
		summary api.UploadSummary
		err     error
	}
	resultCh := make(chan uploadResult, 1)
	go func() {
		summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
		resultCh <- uploadResult{summary: summary, err: err}
	}()

	seen := map[string]struct{}{}
	for range 2 {
		select {
		case tracker := <-started:
			seen[tracker] = struct{}{}
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for initial tracker starts")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 trackers to start first, got %v", seen)
	}

	select {
	case tracker := <-started:
		t.Fatalf("expected third tracker to wait for worker slot, started early: %s", tracker)
	default:
	}

	close(release)

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for third tracker to start")
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("unexpected error: %v", result.err)
		}
		if result.summary.Uploaded != 3 {
			t.Fatalf("expected 3 uploads, got %d", result.summary.Uploaded)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for upload result")
	}
}

func TestUploadReportsCancellationAfterCompletedTrackerUpload(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry()
	if err := registry.Register(cancelAfterUploadDefinition{
		name:     "AITHER",
		cancel:   cancel,
		uploaded: 1,
	}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	summary, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "/tmp/file"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation after upload, got %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected completed upload to be preserved, got %d", summary.Uploaded)
	}
}

func TestUploadCanceledBeforeStartReturnsZeroUpload(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	registry := NewRegistry()
	if err := registry.Register(blockingUploadDefinition{name: "AITHER", uploaded: 1}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	summary, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "/tmp/file"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected zero uploads, got %d", summary.Uploaded)
	}
}

func TestUploadCancellationKeepsCompletedTrackerOnly(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry()
	if err := registry.Register(cancelAfterUploadDefinition{
		name:     "AITHER",
		cancel:   cancel,
		uploaded: 1,
	}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploaded: 1}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}

	repo := &stubRepo{}
	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 1},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER", "BLU"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	summary, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "/tmp/file"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected only completed tracker upload, got %d", summary.Uploaded)
	}

	finalStatus := make(map[string]string)
	for _, update := range repo.statusUpdates {
		finalStatus[update.tracker] = update.status
	}
	if finalStatus["AITHER"] != "uploaded" {
		t.Fatalf("expected AITHER to remain uploaded, got %q", finalStatus["AITHER"])
	}
	if finalStatus["BLU"] != "canceled" {
		t.Fatalf("expected BLU to be canceled, got %q", finalStatus["BLU"])
	}
}

func TestUploadStatusFailureDoesNotCancelCompletedTracker(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry()
	if err := registry.Register(cancelAfterUploadDefinition{
		name:     "AITHER",
		cancel:   cancel,
		uploaded: 1,
		uploadedTorrents: []api.UploadedTorrent{
			{
				Tracker:     "AITHER",
				TorrentID:   "1",
				DownloadURL: "https://aither.cc/torrent/download/1",
			},
		},
	}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploaded: 1}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}

	repo := &failingStatusUpdateRepo{failTracker: "AITHER", failStatus: "uploaded"}
	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 1},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER", "BLU"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	summary, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "/tmp/file"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected completed tracker upload to be preserved, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected uploaded torrent artifact to be preserved, got %d", len(summary.UploadedTorrents))
	}

	var aitherUploaded bool
	var aitherCanceled bool
	var bluCanceled bool
	for _, update := range repo.statusUpdates {
		switch {
		case update.tracker == "AITHER" && update.status == "uploaded":
			aitherUploaded = true
		case update.tracker == "AITHER" && update.status == "canceled":
			aitherCanceled = true
		case update.tracker == "BLU" && update.status == "canceled":
			bluCanceled = true
		}
	}
	if !repo.failed {
		t.Fatal("expected uploaded status update to fail")
	}
	if !aitherUploaded {
		t.Fatal("expected uploaded status update for AITHER")
	}
	if aitherCanceled {
		t.Fatal("expected completed AITHER not to be finalized as canceled")
	}
	if !bluCanceled {
		t.Fatal("expected truly pending BLU to be finalized as canceled")
	}
}

func TestUploadCancellationFinalizesPendingWithCleanupContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry()
	if err := registry.Register(cancelAfterUploadDefinition{
		name:     "AITHER",
		cancel:   cancel,
		uploaded: 1,
	}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploaded: 1}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}

	repo := &recordingStatusContextRepo{}
	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 1},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER", "BLU"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	summary, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "/tmp/file"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected completed upload to be preserved, got %d", summary.Uploaded)
	}

	finalStatus := make(map[string]recordedStatusContext)
	for _, update := range repo.updates {
		finalStatus[update.tracker] = update
	}
	for tracker, wantStatus := range map[string]string{"AITHER": "uploaded", "BLU": "canceled"} {
		update, ok := finalStatus[tracker]
		if !ok {
			t.Fatalf("expected status update for %s", tracker)
		}
		if update.status != wantStatus {
			t.Fatalf("expected %s status %q, got %q", tracker, wantStatus, update.status)
		}
		if update.ctxErr != nil {
			t.Fatalf("expected cleanup context for %s to remain active, got %v", tracker, update.ctxErr)
		}
		if !update.hasDeadline {
			t.Fatalf("expected cleanup context for %s to have a deadline", tracker)
		}
		if until := time.Until(update.deadline); until <= 0 || until > uploadRecordFinalizationTimeout {
			t.Fatalf("expected cleanup deadline within %s for %s, got %s", uploadRecordFinalizationTimeout, tracker, until)
		}
	}
}

func TestUploadLateCancellationUsesCleanupContextForStatusWrites(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry()
	if err := registry.Register(blockingUploadDefinition{name: "AITHER", uploaded: 1}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}

	repo := &recordingStatusContextRepo{cancel: cancel}
	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	summary, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected completed upload to be preserved, got %d", summary.Uploaded)
	}

	if len(repo.updates) != 1 {
		t.Fatalf("expected one status update, got %#v", repo.updates)
	}
	update := repo.updates[0]
	if update.tracker != "AITHER" || update.status != "uploaded" {
		t.Fatalf("expected AITHER uploaded status, got %#v", update)
	}
	if update.ctxErr != nil {
		t.Fatalf("expected cleanup context to remain active after late cancel, got %v", update.ctxErr)
	}
	if !update.hasDeadline {
		t.Fatal("expected cleanup context to have a deadline")
	}
	if until := time.Until(update.deadline); until <= 0 || until > uploadRecordFinalizationTimeout {
		t.Fatalf("expected cleanup deadline within %s, got %s", uploadRecordFinalizationTimeout, until)
	}
}

func TestUploadBestEffortWithFailures(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploaded: 1}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BHD", uploadErr: errors.New("tracker boom")}); err != nil {
		t.Fatalf("register BHD: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "AITHER", uploaded: 1}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}

	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 3},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD", "AITHER"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
	if err == nil {
		t.Fatal("expected failed tracker error for best-effort upload")
	}
	if !strings.Contains(err.Error(), "BHD: tracker boom") {
		t.Fatalf("expected BHD failure in error, got %v", err)
	}
	if summary.Uploaded != 2 {
		t.Fatalf("expected 2 successful uploads, got %d", summary.Uploaded)
	}
}

func TestUploadBestEffortWithFailuresAndRepoReturnsError(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploaded: 1}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BHD", uploadErr: errors.New("tracker boom")}); err != nil {
		t.Fatalf("register BHD: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "AITHER", uploaded: 1}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}

	repo := &stubRepo{}
	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 3},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD", "AITHER"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
	if err == nil {
		t.Fatal("expected failed tracker error for best-effort upload")
	}
	if !strings.Contains(err.Error(), "BHD: tracker boom") {
		t.Fatalf("expected BHD failure in error, got %v", err)
	}
	if summary.Uploaded != 2 {
		t.Fatalf("expected 2 successful uploads, got %d", summary.Uploaded)
	}

	finalStatus := make(map[string]string)
	for _, update := range repo.statusUpdates {
		finalStatus[update.tracker] = update.status
	}
	for tracker, wantStatus := range map[string]string{
		"BLU":    "uploaded",
		"BHD":    "failed",
		"AITHER": "uploaded",
	} {
		if finalStatus[tracker] != wantStatus {
			t.Fatalf("expected %s status %q, got %q", tracker, wantStatus, finalStatus[tracker])
		}
	}
}

func TestUploadRetriesTransientUploadedStatusFailure(t *testing.T) {
	t.Parallel()

	sqliteRepo, err := dbsvc.Open(":memory:")
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = sqliteRepo.Close()
	})
	if err := sqliteRepo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

	registry := NewRegistry()
	if err := registry.Register(blockingUploadDefinition{name: "AITHER", uploaded: 1}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}

	repo := &transientStatusUpdateRepo{
		SQLiteRepository: sqliteRepo,
		failTracker:      "AITHER",
		failStatus:       "uploaded",
	}
	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	ctx := context.Background()
	summary, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected completed upload to be preserved, got %d", summary.Uploaded)
	}
	if !repo.failed {
		t.Fatal("expected first uploaded status update to fail")
	}

	history, err := sqliteRepo.ListUploadHistoryByPath(ctx, "/tmp/file")
	if err != nil {
		t.Fatalf("list upload history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected one upload record, got %d", len(history))
	}
	if history[0].Status != "uploaded" {
		t.Fatalf("expected final upload record status to be uploaded, got %q", history[0].Status)
	}
}

func TestUploadAllFailuresReturnsError(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	errBoom := errors.New("tracker boom")
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploadErr: errBoom}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BHD", uploadErr: errBoom}); err != nil {
		t.Fatalf("register BHD: %v", err)
	}

	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 2},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	_, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "/tmp/file"})
	if err == nil {
		t.Fatalf("expected error when all trackers fail")
	}
}

func TestBuildPreparationSeparatesScopedImageHostGroups(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "HDB",
			group:       "shared",
			description: "same description",
		},
		stubPreparationDefinition{
			name:        "AITHER",
			group:       "shared",
			description: "same description",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/a.png",
				RawURL:     "https://hdb/a.png",
				WebURL:     "https://hdb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/b.png",
				RawURL:     "https://hdb/b.png",
				WebURL:     "https://hdb/b",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"HDB": {ImgRehost: true},
			},
		},
	}

	svc := NewServiceWithRegistry(cfg, nil, repo, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{SourcePath: "/tmp/source"}), []string{"HDB", "AITHER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 2 {
		t.Fatalf("expected 2 grouped descriptions, got %d", len(preview.Descriptions))
	}
}

func TestBuildPreparationRehostsHDBScreenshotsForURLOnlySlots(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(testHDBPreparationDefinition{}); err != nil {
		t.Fatalf("register HDB definition: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"HDB": {ImgRehost: true},
			},
		},
	}
	meta := api.UploadSubject{
		SourcePath: `D:\TV\The.Pitt.S02E15.1080p.WEB-DL.mkv`,
		Options:    api.UploadOptions{KeepImages: true},
		TrackerData: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://pixhost.to/4m092k.png", "https://pixhost.to/7oj122.png"},
		}},
	}
	tmpRoot, err := dbsvc.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	trackerDir := filepath.Join(tmpDir, "aither")
	if err := os.MkdirAll(trackerDir, 0o700); err != nil {
		t.Fatalf("tracker dir: %v", err)
	}
	firstPath := filepath.Join(trackerDir, "4m092k_01.png")
	secondPath := filepath.Join(trackerDir, "7oj122_02.png")
	for _, pathValue := range []string{firstPath, secondPath} {
		if err := os.WriteFile(pathValue, []byte("png"), 0o600); err != nil {
			t.Fatalf("write tracker artifact %s: %v", pathValue, err)
		}
	}

	repo := &stubRepo{
		descriptionOverride: strings.TrimSpace(`
[center]
[url=https://pixhost.to/4m092k.png][img]https://pixhost.to/4m092k.png[/img][/url]
[url=https://pixhost.to/7oj122.png][img]https://pixhost.to/7oj122.png[/img][/url]
[/center]`),
		overrideGroupKey: "hdb",
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://pixhost.to/4m092k.png", "https://pixhost.to/7oj122.png"},
		}},
	}
	images := &stubImageService{
		uploads: map[string][]api.UploadedImageLink{
			"hdb": {
				{
					SourcePath: meta.SourcePath,
					ImagePath:  firstPath,
					Host:       "hdb",
					UsageScope: "tracker:HDB",
					ImgURL:     "https://t.hdbits.org/51q8jo2.jpg",
					RawURL:     "https://img.hdbits.org/51q8jo2.jpg",
					WebURL:     "https://img.hdbits.org/51q8jo2",
				},
				{
					SourcePath: meta.SourcePath,
					ImagePath:  secondPath,
					Host:       "hdb",
					UsageScope: "tracker:HDB",
					ImgURL:     "https://t.hdbits.org/w0S7ltI.jpg",
					RawURL:     "https://img.hdbits.org/w0S7ltI.jpg",
					WebURL:     "https://img.hdbits.org/w0S7ltI",
				},
			},
		},
	}

	svc := NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repo, registry, images)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(meta), []string{"HDB"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected 1 description group, got %d", len(preview.Descriptions))
	}
	description := preview.Descriptions[0].Description
	if strings.TrimSpace(description) == "" {
		t.Fatal("expected HDB description to be built")
	}
	if strings.Contains(description, "pixhost.to/4m092k.png") || strings.Contains(description, "pixhost.to/7oj122.png") {
		t.Fatalf("expected HDB screenshots to replace original tracker urls, got %q", description)
	}
	if !strings.Contains(description, "img.hdbits.org/51q8jo2") || !strings.Contains(description, "img.hdbits.org/w0S7ltI") {
		t.Fatalf("expected HDB screenshot urls in description, got %q", description)
	}
}

func TestBuildPreparationPreloadsDescriptionAssetQueriesOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "HDB",
			group:       "hdb",
			description: "same description",
		},
		stubPreparationDefinition{
			name:        "AITHER",
			group:       "aither",
			description: "same description",
		},
		stubPreparationDefinition{
			name:        "BHD",
			group:       "bhd",
			description: "same description",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)
	_, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{SourcePath: "/tmp/source"}), []string{"HDB", "AITHER", "BHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.overrideCalls != 1 {
		t.Fatalf("expected 1 description override query, got %d", repo.overrideCalls)
	}
	if repo.trackerRecordsCalls != 1 {
		t.Fatalf("expected 1 tracker metadata query, got %d", repo.trackerRecordsCalls)
	}
	if repo.selectionsCalls != 1 {
		t.Fatalf("expected 1 final selections query, got %d", repo.selectionsCalls)
	}
	if repo.uploadsCalls != 1 {
		t.Fatalf("expected 1 uploaded images query, got %d", repo.uploadsCalls)
	}
}

func TestBuildPreparationDoesNotImportUploadOutcomeBlocks(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{
			name:        "HDB",
			group:       "hdb",
			description: "hdb",
		},
		stubPreparationDefinition{
			name:        "BHD",
			group:       "bhd",
			description: "bhd",
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.NewDescriptionSubject(api.UploadSubject{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	}),

		[]string{"HDB", "BHD"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 2 {
		t.Fatalf("expected both description-owned tracker groups, got %d", len(preview.Descriptions))
	}
}

func TestBuildUploadDryRunIncludesBlockedTrackers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubDryRunDefinition{name: "HDB"},
		stubDryRunDefinition{name: "BHD"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	}, []string{"HDB", "BHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 dry-run entries, got %d", len(entries))
	}
	if entries[0].Tracker != "HDB" || entries[1].Tracker != "BHD" {
		t.Fatalf("expected blocked and unblocked dry-run entries, got %#v", entries)
	}
}

func TestBuildUploadDryRunAnnotatesBannedGroupWithoutBlockingPayload(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "ANT", bannedGroups: []string{"AFG"}}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{
		SourcePath: "/tmp/source",
		Tag:        "-AFG",
	}, []string{"ANT"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 dry-run entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Tracker != "ANT" {
		t.Fatalf("expected ANT dry-run entry, got %#v", entry)
	}
	if entry.Status != "ready" || entry.Payload["category"] != "movie" {
		t.Fatalf("expected payload to remain ready, got %#v", entry)
	}
	if !entry.Banned {
		t.Fatalf("expected dry-run banned annotation, got %#v", entry)
	}
	if !strings.Contains(entry.BannedReason, "group afg is banned on ANT") {
		t.Fatalf("expected banned reason, got %q", entry.BannedReason)
	}
}

func TestBuildUploadDryRunAnnotatesBannedGroupOnBuilderError(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{
		name:         "ANT",
		dryRunErr:    errors.New("build failed"),
		bannedGroups: []string{"AFG"},
	}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{
		SourcePath: "/tmp/source",
		Tag:        "-AFG",
	}, []string{"ANT"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 dry-run entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Tracker != "ANT" {
		t.Fatalf("expected ANT dry-run entry, got %#v", entry)
	}
	if entry.Status != "error" || entry.Message != "build failed" {
		t.Fatalf("expected builder error entry, got %#v", entry)
	}
	if !entry.Banned {
		t.Fatalf("expected dry-run banned annotation, got %#v", entry)
	}
	if !strings.Contains(entry.BannedReason, "group afg is banned on ANT") {
		t.Fatalf("expected banned reason, got %q", entry.BannedReason)
	}
	if entry.BannedCheckError != "" {
		t.Fatalf("expected no banned check error, got %q", entry.BannedCheckError)
	}
}

func TestBuildUploadDryRunReportsBannedCheckErrorOnBuilderError(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "BLU", dryRunErr: errors.New("build failed")}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "upbrr.db")
	svc := NewServiceWithRegistry(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	}, nil, nil, registry)
	bannedDir := filepath.Join(tempDir, "cache", "banned")
	if err := os.MkdirAll(bannedDir, 0o700); err != nil {
		t.Fatalf("create banned cache dir: %v", err)
	}
	bannedPath := filepath.Join(bannedDir, "BLU_banned_groups.json")
	if err := os.WriteFile(bannedPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write invalid banned cache: %v", err)
	}

	entries, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{
		SourcePath: "/tmp/source",
		Tag:        "-ExampleGRP",
	}, []string{"BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 dry-run entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Status != "error" || entry.Message != "build failed" {
		t.Fatalf("expected builder error entry, got %#v", entry)
	}
	if entry.BannedCheckError == "" {
		t.Fatalf("expected banned check error, got %#v", entry)
	}
	if !strings.Contains(entry.BannedCheckError, "banned group check failed") {
		t.Fatalf("expected banned check failure message, got %q", entry.BannedCheckError)
	}
}

func TestUploadSkipsBlockedTrackersBeforePendingRecords(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	registry := NewRegistry()
	for _, definition := range []Definition{
		trackingUploadDefinition{name: "HDB", started: started},
		trackingUploadDefinition{name: "AITHER", started: started},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{}
	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"HDB", "AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	summary, err := svc.Upload(context.Background(), api.UploadSubject{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected 1 upload, got %d", summary.Uploaded)
	}
	if len(repo.createdUploads) != 1 || repo.createdUploads[0].Tracker != "AITHER" {
		t.Fatalf("expected pending record only for AITHER, got %#v", repo.createdUploads)
	}

	close(started)
	startedTrackers := make([]string, 0, len(started))
	for tracker := range started {
		startedTrackers = append(startedTrackers, tracker)
	}
	if len(startedTrackers) != 1 || startedTrackers[0] != "AITHER" {
		t.Fatalf("expected upload to run only for AITHER, got %#v", startedTrackers)
	}
}

func TestBuildUploadDryRunPreloadsDescriptionAssetQueriesOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubDryRunDefinition{name: "HDB"},
		stubDryRunDefinition{name: "AITHER"},
		stubDryRunDefinition{name: "BHD"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)
	_, err := svc.BuildUploadDryRun(context.Background(), api.UploadSubject{SourcePath: "/tmp/source"}, []string{"HDB", "AITHER", "BHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.overrideCalls != 1 {
		t.Fatalf("expected 1 description override query, got %d", repo.overrideCalls)
	}
	if repo.trackerRecordsCalls != 1 {
		t.Fatalf("expected 1 tracker metadata query, got %d", repo.trackerRecordsCalls)
	}
	if repo.selectionsCalls != 1 {
		t.Fatalf("expected 1 final selections query, got %d", repo.selectionsCalls)
	}
	if repo.uploadsCalls != 1 {
		t.Fatalf("expected 1 uploaded images query, got %d", repo.uploadsCalls)
	}
}

func createServiceTestTorrent(t *testing.T, sourcePath string, torrentPath string) {
	t.Helper()

	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       sourcePath,
		OutputPath: torrentPath,
		IsPrivate:  true,
	})
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
}

func assertTrackerArtifact(t *testing.T, torrentPath string, wantAnnounce string, wantSource string) {
	t.Helper()

	torrentMeta, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		t.Fatalf("load tracker artifact: %v", err)
	}
	if torrentMeta.Announce != wantAnnounce {
		t.Fatal("expected tracker artifact announce")
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal tracker artifact info: %v", err)
	}
	if info.Source != wantSource {
		t.Fatalf("expected source %q, got %q", wantSource, info.Source)
	}
}

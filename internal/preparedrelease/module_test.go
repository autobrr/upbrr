// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareUsesExactCompatibilityAndPublishesConcreteAssessments(t *testing.T) {
	t.Parallel()
	path := writePreparedTestFile(t, "source.mkv", "first")
	store := newMemoryStore()
	collector := &recordingCollector{}
	module := newTestModule(t, store, collector)
	input := api.PrepareInput{
		SourcePath: path,
		Intent:     api.PreparationIntentPreview,
		Instructions: api.ReleaseFactInstructions{
			ReleaseName: api.ReleaseNameOverrides{},
		},
		Policy: api.PreparationPolicy{KeepFolder: true},
	}

	first, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() first error = %v", err)
	}
	if first.Release.Generation != 1 {
		t.Fatalf("generation = %d, want 1", first.Release.Generation)
	}
	if first.Release.Assessments.MediaInfoUniqueID != api.UniqueIDStatusPresent ||
		first.Release.Assessments.MediaInfoEncodeSettings != api.EncodeSettingsStatusMissing {
		t.Fatalf("assessments = %#v", first.Release.Assessments)
	}

	input.Intent = api.PreparationIntentUpload
	reused, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() reuse error = %v", err)
	}
	if reused.Release.Generation != first.Release.Generation {
		t.Fatalf("intent-specific generation = %d, want %d", reused.Release.Generation, first.Release.Generation)
	}
	if collector.callCount() != 1 || store.commitCount() != 1 {
		t.Fatalf("reuse calls collector=%d commits=%d, want 1/1", collector.callCount(), store.commitCount())
	}

	name := "Example.Release.2026.1080p-GRP"
	input.Instructions.ReleaseName.Type = &name
	second, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() instruction change error = %v", err)
	}
	if second.Release.Generation != 2 {
		t.Fatalf("instruction generation = %d, want 2", second.Release.Generation)
	}

	input.Policy.OnlyID = true
	third, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() policy change error = %v", err)
	}
	if third.Release.Generation != 3 {
		t.Fatalf("policy generation = %d, want 3", third.Release.Generation)
	}

	if err := os.WriteFile(path, []byte("changed source"), 0o600); err != nil {
		t.Fatal(err)
	}
	fourth, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() source change error = %v", err)
	}
	if fourth.Release.Generation != 4 {
		t.Fatalf("source generation = %d, want 4", fourth.Release.Generation)
	}
}

func TestPrepareReportsCanonicalOwnerStagesAndReuse(t *testing.T) {
	t.Parallel()
	path := writePreparedTestFile(t, "source.mkv", "synthetic media")
	module := newTestModule(t, newMemoryStore(), &recordingCollector{})
	var progress []api.PreparationProgressUpdate
	ctx := api.WithPreparationProgressReporter(context.Background(), func(update api.PreparationProgressUpdate) {
		progress = append(progress, update)
	})

	if _, err := module.Prepare(ctx, api.PrepareInput{SourcePath: path}); err != nil {
		t.Fatalf("prepare generation: %v", err)
	}
	for _, phase := range []api.PreparationProgressPhase{
		api.PreparationPhaseSourceInspection,
		api.PreparationPhasePreparedCache,
		api.PreparationPhaseCanonicalIdentity,
		api.PreparationPhaseGenerationCommit,
	} {
		if !hasPreparationProgress(progress, phase, api.PreparationProgressCompleted) {
			t.Fatalf("canonical owner phase %q did not complete", phase)
		}
	}

	progress = nil
	if _, err := module.Prepare(ctx, api.PrepareInput{SourcePath: path}); err != nil {
		t.Fatalf("reuse generation: %v", err)
	}
	if !hasPreparationProgress(progress, api.PreparationPhasePreparedCache, api.PreparationProgressCompleted) ||
		!hasPreparationProgress(progress, api.PreparationPhaseSourceEvidence, api.PreparationProgressSkipped) {
		t.Fatal("compatible generation reuse did not report cache and skipped collection stages")
	}
}

func hasPreparationProgress(
	updates []api.PreparationProgressUpdate,
	phase api.PreparationProgressPhase,
	status api.PreparationProgressStatus,
) bool {
	for _, update := range updates {
		if update.Phase == phase && update.Status == status {
			return true
		}
	}
	return false
}

func TestPrepareDoesNotCacheImplicitBDMVPlaylistSelection(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "disc")
	if err := os.MkdirAll(filepath.Join(sourcePath, "BDMV", "PLAYLIST"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourcePath, "BDMV", "STREAM"), 0o755); err != nil {
		t.Fatal(err)
	}
	collector := &recordingCollector{}
	module := newTestModule(t, newMemoryStore(), collector)
	input := api.PrepareInput{SourcePath: sourcePath}

	first, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() first error = %v", err)
	}
	second, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() second error = %v", err)
	}
	if second.Release.Generation != first.Release.Generation+1 || collector.callCount() != 2 {
		t.Fatalf("implicit BDMV reuse generation=%d calls=%d, want generation=%d calls=2", second.Release.Generation, collector.callCount(), first.Release.Generation+1)
	}

	input.Instructions.Playlist = api.PlaylistInstruction{Set: true, Selected: []string{"00001.MPLS"}}
	third, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() direct instruction error = %v", err)
	}
	fourth, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() direct instruction reuse error = %v", err)
	}
	if fourth.Release.Generation != third.Release.Generation || collector.callCount() != 3 {
		t.Fatalf("direct BDMV reuse generation=%d calls=%d, want generation=%d calls=3", fourth.Release.Generation, collector.callCount(), third.Release.Generation)
	}
}

func TestPreparationCompatibilityIncludesEvidencePolicyAndExcludesOneShotControls(t *testing.T) {
	t.Parallel()

	stringPtr := func(value string) *string { return &value }
	boolPtr := func(value bool) *bool { return &value }
	baseline := api.PrepareInput{SourcePath: "Example.Release.2026.mkv"}
	want, err := preparationCompatibility(baseline, "source")
	if err != nil {
		t.Fatalf("baseline compatibility: %v", err)
	}
	included := []struct {
		name   string
		mutate func(*api.PrepareInput)
	}{
		{name: "fact instructions", mutate: func(input *api.PrepareInput) { input.Instructions.TrackerIDs = map[string]string{"btn": "123"} }},
		{name: "keep folder", mutate: func(input *api.PrepareInput) { input.Policy.KeepFolder = true }},
		{name: "keep images", mutate: func(input *api.PrepareInput) { input.Policy.KeepImages = true }},
		{name: "only id", mutate: func(input *api.PrepareInput) { input.Policy.OnlyID = true }},
		{name: "skip client", mutate: func(input *api.PrepareInput) { input.Search.Skip = true }},
		{name: "client selector", mutate: func(input *api.PrepareInput) { input.Search.Client = stringPtr("qbit") }},
	}
	for _, test := range included {
		t.Run(test.name, func(t *testing.T) {
			input := baseline
			test.mutate(&input)
			got, compatibilityErr := preparationCompatibility(input, "source")
			if compatibilityErr != nil {
				t.Fatalf("compatibility: %v", compatibilityErr)
			}
			if got == want {
				t.Fatalf("included change did not affect compatibility: %#v", got)
			}
		})
	}
	excluded := []struct {
		name   string
		mutate func(*api.PrepareInput)
	}{
		{name: "intent", mutate: func(input *api.PrepareInput) { input.Intent = api.PreparationIntentUpload }},
		{name: "interaction", mutate: func(input *api.PrepareInput) { input.Controls.Interaction = api.InteractionModeInteractive }},
		{name: "rescan permission", mutate: func(input *api.PrepareInput) { input.Controls.ConfirmBDMVRescan = true }},
		{name: "force recheck", mutate: func(input *api.PrepareInput) { input.Controls.ForceRecheck = boolPtr(true) }},
		{name: "force preparation", mutate: func(input *api.PrepareInput) { input.Force = true }},
	}
	for _, test := range excluded {
		t.Run(test.name, func(t *testing.T) {
			input := baseline
			test.mutate(&input)
			got, compatibilityErr := preparationCompatibility(input, "source")
			if compatibilityErr != nil {
				t.Fatalf("compatibility: %v", compatibilityErr)
			}
			if got != want {
				t.Fatalf("one-shot change affected compatibility: got %#v want %#v", got, want)
			}
		})
	}
}

func TestOperationSubjectsUseExactGenerationAndDetachedFacts(t *testing.T) {
	t.Parallel()

	path := writePreparedTestFile(t, "source.mkv", "source")
	module := newTestModule(t, newMemoryStore(), &recordingCollector{})
	prepared, err := module.Prepare(context.Background(), api.PrepareInput{SourcePath: path})
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ReleaseRef{SourcePath: path, Generation: prepared.Release.Generation}
	upload, err := module.ResolveUploadSubject(context.Background(), api.UploadReviewInput{
		Release:  ref,
		Trackers: []string{"AITHER"},
		Options:  api.UploadOptions{KeepImages: true},
	})
	if err != nil {
		t.Fatalf("ResolveUploadSubject() error = %v", err)
	}
	if upload.SourcePath != path || upload.ReleaseName != "Example.Release.2026.1080p-GRP" || len(upload.Trackers) != 1 {
		t.Fatalf("upload subject = %#v", upload)
	}
	if upload.Assessments.MediaInfoUniqueID != api.UniqueIDStatusPresent ||
		upload.Assessments.MediaInfoEncodeSettings != api.EncodeSettingsStatusMissing {
		t.Fatalf("upload assessments = %#v", upload.Assessments)
	}
	upload.Trackers[0] = "changed"

	duplicate, err := module.ResolveDuplicateSubject(context.Background(), api.DuplicateCheckInput{Release: ref})
	if err != nil {
		t.Fatalf("ResolveDuplicateSubject() error = %v", err)
	}
	if duplicate.SourcePath != path || duplicate.ReleaseName != "Example.Release.2026.1080p-GRP" {
		t.Fatalf("duplicate subject = %#v", duplicate)
	}

	_, err = module.ResolveDuplicateSubject(context.Background(), api.DuplicateCheckInput{
		Release: api.ReleaseRef{SourcePath: path, Generation: prepared.Release.Generation + 1},
	})
	var stale *StalePreparationError
	if !errors.As(err, &stale) || stale.Reason != StaleReasonGeneration {
		t.Fatalf("wrong generation error = %v", err)
	}
}

func TestPrepareCommitFailureLeavesPriorGenerationPublished(t *testing.T) {
	t.Parallel()
	path := writePreparedTestFile(t, "source.mkv", "source")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	input := api.PrepareInput{SourcePath: path}
	first, err := module.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	store.setCommitError(errors.New("forced commit failure"))
	input.Policy.OnlyID = true
	if _, err := module.Prepare(context.Background(), input); err == nil {
		t.Fatal("Prepare() error = nil, want commit failure")
	}

	seed, err := module.Export(context.Background(), api.ReleaseRef{
		SourcePath: path,
		Generation: first.Release.Generation,
	})
	if err != nil {
		t.Fatalf("Export() prior generation error = %v", err)
	}
	if seed.payload.result.Release.Generation != 1 {
		t.Fatalf("published generation = %d, want 1", seed.payload.result.Release.Generation)
	}
	current, err := store.LoadPreparedRelease(context.Background(), first.Release.Source.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != 1 {
		t.Fatalf("persisted generation = %d, want 1", current.Generation)
	}
}

func TestSeedImportRechecksSourceFingerprint(t *testing.T) {
	t.Parallel()
	path := writePreparedTestFile(t, "source.mkv", "source")
	sourceStore := newMemoryStore()
	source := newTestModule(t, sourceStore, &recordingCollector{})
	prepared, err := source.Prepare(context.Background(), api.PrepareInput{SourcePath: path})
	if err != nil {
		t.Fatal(err)
	}
	seed, err := source.Export(context.Background(), api.ReleaseRef{
		SourcePath: path,
		Generation: prepared.Release.Generation,
	})
	if err != nil {
		t.Fatal(err)
	}

	target := newTestModule(t, newMemoryStore(), &recordingCollector{})
	layout, layoutErr := sourcelayout.Resolve(context.Background(), path)
	if layoutErr != nil {
		t.Fatal(layoutErr)
	}
	_, actualFingerprint, inspectErr := inspectSource(context.Background(), api.PrepareInput{SourcePath: path}, layout)
	if inspectErr != nil {
		t.Fatal(inspectErr)
	}
	if actualFingerprint != prepared.Release.Compatibility.SourceFingerprint {
		t.Fatalf("unchanged fingerprint = %s, want %s", actualFingerprint, prepared.Release.Compatibility.SourceFingerprint)
	}
	ref, err := target.Import(context.Background(), seed)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if ref.Generation != prepared.Release.Generation {
		t.Fatalf("imported generation = %d, want %d", ref.Generation, prepared.Release.Generation)
	}

	if err := os.WriteFile(path, []byte("changed after acceptance"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleTarget := newTestModule(t, newMemoryStore(), &recordingCollector{})
	_, err = staleTarget.Import(context.Background(), seed)
	var stale *StalePreparationError
	if !errors.As(err, &stale) || stale.Reason != StaleReasonFingerprint {
		t.Fatalf("Import() error = %v, want fingerprint StalePreparationError", err)
	}
}

func TestPrepareCancellationDoesNotReorderSourceFIFO(t *testing.T) {
	t.Parallel()
	path := writePreparedTestFile(t, "source.mkv", "source")
	collector := newBlockingCollector()
	module := newTestModule(t, newMemoryStore(), collector)

	firstDone := make(chan error, 1)
	go func() {
		_, err := module.Prepare(context.Background(), prepareInputWithLookup(path, "first"))
		firstDone <- err
	}()
	waitForString(t, collector.started, "first")

	canceledCtx, cancel := context.WithCancel(context.Background())
	canceledDone := make(chan error, 1)
	go func() {
		_, err := module.Prepare(canceledCtx, prepareInputWithLookup(path, "canceled"))
		canceledDone <- err
	}()
	thirdDone := make(chan error, 1)
	go func() {
		_, err := module.Prepare(context.Background(), prepareInputWithLookup(path, "third"))
		thirdDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-canceledDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Prepare() error = %v", err)
	}

	collector.release <- struct{}{}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	waitForString(t, collector.started, "third")
	collector.release <- struct{}{}
	if err := <-thirdDone; err != nil {
		t.Fatal(err)
	}
	if got := collector.order(); len(got) != 2 || got[0] != "first" || got[1] != "third" {
		t.Fatalf("collector order = %v, want [first third]", got)
	}
}

func TestPrepareDifferentSourcesRunConcurrently(t *testing.T) {
	t.Parallel()
	firstPath := writePreparedTestFile(t, "first.mkv", "first")
	secondPath := writePreparedTestFile(t, "second.mkv", "second")
	collector := newBlockingCollector()
	module := newTestModule(t, newMemoryStore(), collector)
	done := make(chan error, 2)
	for path, lookup := range map[string]string{firstPath: "first", secondPath: "second"} {
		go func() {
			_, err := module.Prepare(context.Background(), prepareInputWithLookup(path, lookup))
			done <- err
		}()
	}
	seen := map[string]bool{}
	seen[<-collector.started] = true
	seen[<-collector.started] = true
	if !seen["first"] || !seen["second"] {
		t.Fatalf("concurrent starts = %v", seen)
	}
	collector.release <- struct{}{}
	collector.release <- struct{}{}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func prepareInputWithLookup(path string, lookup string) api.PrepareInput {
	return api.PrepareInput{
		SourcePath: path,
		Instructions: api.ReleaseFactInstructions{
			SourceLookup: lookup,
		},
	}
}

func writePreparedTestFile(t *testing.T, name string, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestModule(t *testing.T, store Store, collector Collector) *Module {
	t.Helper()
	module, err := New(store, staticIdentityResolver{}, collector)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

type staticIdentityResolver struct{}

func (staticIdentityResolver) Resolve(_ context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	now := time.Now().UTC()
	identity := api.ExternalIdentity{
		SourcePath: request.SourcePath,
		Generation: request.Generation,
		TMDBID:     1234567,
		Category:   api.CanonicalCategoryMovie,
		Provenance: api.IdentityProvenanceSet{
			TMDB:     api.IdentityProvenanceProvider,
			Category: api.IdentityProvenanceProvider,
		},
		Conflict: api.IdentityConflictNone,
		Resolution: api.IdentityResolutionKey{
			SourceFingerprint: request.SourceFingerprint,
			IntentFingerprint: "intent",
			ContractVersion:   externalidentity.ContractVersion,
		},
		ResolvedAt: now,
	}
	return externalidentity.Result{
		Identity: identity,
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			UpdatedAt:  now,
		},
	}, nil
}

type recordingCollector struct {
	mu    sync.Mutex
	calls []string
}

func (c *recordingCollector) Collect(_ context.Context, request preparationstate.Request) (CollectedFacts, error) {
	c.mu.Lock()
	c.calls = append(c.calls, request.Input.Instructions.SourceLookup)
	c.mu.Unlock()
	return CollectedFacts{
		Naming: api.NamingFacts{
			Filename:    filepath.Base(request.Manifest.SourcePath),
			ReleaseName: "Example.Release.2026.1080p-GRP",
		},
		Assessments: api.ReleaseAssessments{
			MediaInfoUniqueID:       api.UniqueIDStatusPresent,
			MediaInfoEncodeSettings: api.EncodeSettingsStatusMissing,
			Naming: api.NamingAssessment{
				Status: api.NamingStatusComplete,
			},
		},
	}, nil
}

func (c *recordingCollector) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

type blockingCollector struct {
	*recordingCollector
	started chan string
	release chan struct{}
}

func newBlockingCollector() *blockingCollector {
	return &blockingCollector{
		recordingCollector: &recordingCollector{},
		started:            make(chan string, 4),
		release:            make(chan struct{}, 4),
	}
}

func (c *blockingCollector) Collect(ctx context.Context, request preparationstate.Request) (CollectedFacts, error) {
	c.mu.Lock()
	c.calls = append(c.calls, request.Input.Instructions.SourceLookup)
	c.mu.Unlock()
	c.started <- request.Input.Instructions.SourceLookup
	select {
	case <-c.release:
		request.Input = api.PrepareInput{}
		return c.recordingCollector.Collect(ctx, request)
	case <-ctx.Done():
		return CollectedFacts{}, fmt.Errorf("blocking collector canceled: %w", ctx.Err())
	}
}

func (c *blockingCollector) order() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]string, 0, len(c.calls))
	for _, call := range c.calls {
		if call != "" {
			result = append(result, call)
		}
	}
	return result
}

func waitForString(t *testing.T, values <-chan string, want string) {
	t.Helper()
	select {
	case got := <-values:
		if got != want {
			t.Fatalf("started = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

type memoryStore struct {
	mu        sync.Mutex
	current   map[string]api.PreparedRelease
	commits   int
	commitErr error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{current: make(map[string]api.PreparedRelease)}
}

func (s *memoryStore) LoadPreparedRelease(_ context.Context, sourcePath string) (api.PreparedRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, ok := s.current[canonicalSourceKey(sourcePath)]
	if !ok {
		return api.PreparedRelease{}, internalerrors.ErrNotFound
	}
	cloned, err := release.Clone()
	if err != nil {
		return api.PreparedRelease{}, fmt.Errorf("memory store: clone loaded release: %w", err)
	}
	return cloned, nil
}

func (s *memoryStore) CommitPreparedRelease(_ context.Context, release api.PreparedRelease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitErr != nil {
		return s.commitErr
	}
	cloned, err := release.Clone()
	if err != nil {
		return fmt.Errorf("memory store: clone committed release: %w", err)
	}
	s.current[canonicalSourceKey(release.Source.SourcePath)] = cloned
	s.commits++
	return nil
}

func (s *memoryStore) PurgePreparedRelease(_ context.Context, sourcePath string) error {
	s.mu.Lock()
	delete(s.current, canonicalSourceKey(sourcePath))
	s.mu.Unlock()
	return nil
}

func (s *memoryStore) setCommitError(err error) {
	s.mu.Lock()
	s.commitErr = err
	s.mu.Unlock()
}

func (s *memoryStore) commitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits
}

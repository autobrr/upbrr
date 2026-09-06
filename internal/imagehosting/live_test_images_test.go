// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func liveImagesFixture(t *testing.T, count int) (*imageEffectJournal, []string, config.Config) {
	t.Helper()
	dir := t.TempDir()
	journal, err := newImageEffectJournal("test-run", filepath.Join(dir, "image-effects.private.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, count)
	for index := range count {
		paths[index] = filepath.Join(dir, fmt.Sprintf("image-%d.png", index))
		if err := os.WriteFile(paths[index], []byte(fmt.Sprintf("synthetic-image-%d", index)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return journal, paths, config.Config{ImageHosting: config.ImageHostingConfig{LostimgAPI: "test-secret"}}
}

func legacyLiveImagesFixture(t *testing.T, count int) (*imageEffectJournal, []string, config.Config) {
	t.Helper()
	dir := t.TempDir()
	journal := &imageEffectJournal{path: filepath.Join(dir, "legacy.jsonl"), runID: "legacy-run"}
	run := imageEffectRecord{
		Version:  legacyImageJournalVersion,
		RunID:    journal.runID,
		Kind:     "run",
		ID:       journal.runID,
		Provider: "lostimg",
		Time:     time.Now().UTC(),
	}
	if err := journal.write(run, os.O_CREATE|os.O_EXCL|os.O_WRONLY); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.read(); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, count)
	for index := range paths {
		paths[index] = filepath.Join(dir, fmt.Sprintf("legacy-upload-%02d.png", index))
		if err := os.WriteFile(paths[index], []byte(fmt.Sprintf("synthetic-legacy-image-%02d", index)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return journal, paths, config.Config{ImageHosting: config.ImageHostingConfig{LostimgAPI: "test-secret"}}
}

func imageTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testLiveLostimgUploader(
	t *testing.T,
	journal *imageEffectJournal,
	cfg config.Config,
	client *http.Client,
	maxImages int,
) batchUploader {
	t.Helper()
	wrapped := wrapLiveTestUploader("lostimg", &lostimgUploader{
		apiKey: cfg.ImageHosting.LostimgAPI,
		client: liveImageClient(client),
	}, journal, maxImages)
	batch, ok := wrapped.(batchUploader)
	if !ok {
		t.Fatal("guarded Lostimg uploader lost batch support")
	}
	return batch
}

func testLiveBatchGuard(t *testing.T, wrapped uploader) *liveTestBatchUploader {
	t.Helper()
	switch guarded := wrapped.(type) {
	case *liveTestBatchUploader:
		return guarded
	case *liveTestNamedBatchUploader:
		return guarded.liveTestBatchUploader
	default:
		t.Fatalf("uploader %T is not a guarded batch uploader", wrapped)
		return nil
	}
}

type controlledLiveTestUploader struct {
	entered chan string
	release <-chan struct{}

	mu    sync.Mutex
	calls int
}

func (u *controlledLiveTestUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if err := u.enter(ctx, imagePath); err != nil {
		return uploadResult{}, err
	}
	return controlledLiveTestUploadResult(imagePath), nil
}

func (u *controlledLiveTestUploader) enter(ctx context.Context, label string) error {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	if u.entered != nil {
		select {
		case u.entered <- label:
		case <-ctx.Done():
			return fmt.Errorf("controlled upload canceled before entry: %w", ctx.Err())
		}
	}
	if u.release != nil {
		select {
		case <-u.release:
		case <-ctx.Done():
			return fmt.Errorf("controlled upload canceled while blocked: %w", ctx.Err())
		}
	}
	return nil
}

func controlledLiveTestUploadResult(imagePath string) uploadResult {
	url := "https://images.invalid/" + filepath.Base(imagePath)
	return uploadResult{
		ImgURL: url,
		RawURL: url,
		WebURL: url,
	}
}

func (u *controlledLiveTestUploader) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

type controlledLiveTestBatchUploader struct {
	*controlledLiveTestUploader
}

func (u *controlledLiveTestBatchUploader) UploadBatch(ctx context.Context, imagePaths []string) ([]uploadResult, error) {
	if err := u.enter(ctx, strings.Join(imagePaths, ",")); err != nil {
		return nil, err
	}
	results := make([]uploadResult, len(imagePaths))
	for index, imagePath := range imagePaths {
		results[index] = controlledLiveTestUploadResult(imagePath)
	}
	return results, nil
}

type controlledLiveTestNamedBatchUploader struct {
	*controlledLiveTestBatchUploader
}

func (u *controlledLiveTestNamedBatchUploader) UploadBatchWithName(
	ctx context.Context,
	imagePaths []string,
	_ string,
) ([]uploadResult, error) {
	return u.UploadBatch(ctx, imagePaths)
}

func TestLiveImagesJournalChunksAndCleanupResume(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 51)
	var uploaded []string
	var uploadSizes, deleteSizes []int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != lostimgImagesEndpoint || request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatal("unexpected endpoint or credentials")
		}
		state, err := journal.read()
		if err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case http.MethodPost:
			if err := request.ParseMultipartForm(1024 * 1024); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = request.MultipartForm.RemoveAll() })
			size := len(request.MultipartForm.File["file[]"])
			uploadSizes = append(uploadSizes, size)
			attempt := state.attempts[state.order[len(state.order)-1]]
			if attempt.returned || len(attempt.sources) != len(paths) {
				t.Fatal("upload dispatched before durable attempt")
			}
			urls := make([]string, size)
			for index := range size {
				urls[index] = fmt.Sprintf("https://lostimg.cc/synthetic-%d.png", len(uploaded)+index)
			}
			uploaded = append(uploaded, urls...)
			body, err := json.Marshal(map[string]any{"urls": urls})
			if err != nil {
				t.Fatal(err)
			}
			return imageTestResponse(http.StatusOK, string(body)), nil
		case http.MethodDelete:
			var body struct {
				URLs []string `json:"urls"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			deleteSizes = append(deleteSizes, len(body.URLs))
			for _, raw := range body.URLs {
				if !slices.Contains(uploaded, raw) {
					t.Fatal("attempted deletion without run ownership")
				}
				durable := false
				for id, saved := range state.urls {
					if saved == raw && state.images[id].State == "cleanup_pending" {
						durable = true
					}
				}
				if !durable {
					t.Fatal("deletion dispatched before durable pending record")
				}
			}
			result, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			return imageTestResponse(http.StatusOK, string(result)), nil
		default:
			t.Fatal("unexpected method")
			return nil, errors.New("unexpected method")
		}
	})}
	uploader := testLiveLostimgUploader(t, journal, cfg, client, len(paths)*2)
	results, err := uploader.UploadBatch(t.Context(), paths)
	if err != nil || len(results) != 51 || !slices.Equal(uploadSizes, []int{50, 1}) {
		t.Fatalf("upload: count=%d batches=%v err=%v", len(results), uploadSizes, err)
	}
	report, err := cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
	if err != nil || report.Deleted != 51 || report.Pending+report.Unknown != 0 || !slices.Equal(deleteSizes, []int{50, 1}) {
		t.Fatalf("cleanup: %+v batches=%v err=%v", report, deleteSizes, err)
	}
	report, err = cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
	if err != nil || report.Deleted != 51 || len(deleteSizes) != 2 {
		t.Fatal("acknowledged deletes were retried")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "https:") || strings.Contains(string(encoded), cfg.ImageHosting.LostimgAPI) {
		t.Fatal("report exposed private values")
	}
	if _, err := uploader.UploadBatch(t.Context(), paths[:1]); err == nil {
		t.Fatal("upload allowed after cleanup")
	}
}

func TestLiveImagesDuplicateLostimgURLStillCountsUploadedImages(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 2)
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch request.Method {
		case http.MethodPost:
			return imageTestResponse(http.StatusOK, `{"urls":["https://lostimg.cc/shared.png","https://lostimg.cc/shared.png"]}`), nil
		case http.MethodDelete:
			return imageTestResponse(http.StatusOK, `{"url":"https://lostimg.cc/shared.png"}`), nil
		default:
			t.Fatalf("method = %s", request.Method)
			return nil, errors.New("unexpected method")
		}
	})}
	uploader := testLiveLostimgUploader(t, journal, cfg, client, 2)
	results, err := uploader.UploadBatch(t.Context(), paths)
	if err != nil || len(results) != 2 {
		t.Fatalf("duplicate URL upload: %#v, %v", results, err)
	}
	report, err := cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
	if err != nil || report.Deleted != 2 || len(report.Images) != 2 || report.Pending+report.Unknown+report.Retained != 0 || requests != 2 {
		t.Fatalf("duplicate URL cleanup: %+v requests=%d err=%v", report, requests, err)
	}
}

func TestLiveImagesPartialUploadPreservesKnownEffectsAndBlocksRetry(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 51)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		if calls == 2 {
			return imageTestResponse(http.StatusInternalServerError, `{"url":"https://lostimg.cc/partial.png","error":"private-error"}`), nil
		}
		urls := make([]string, 50)
		for index := range urls {
			urls[index] = fmt.Sprintf("https://lostimg.cc/image-%d.png", index)
		}
		body, err := json.Marshal(map[string]any{"urls": urls})
		if err != nil {
			t.Fatal(err)
		}
		return imageTestResponse(http.StatusOK, string(body)), nil
	})}
	uploader := testLiveLostimgUploader(t, journal, cfg, client, len(paths)*2)
	results, err := uploader.UploadBatch(t.Context(), paths)
	if err == nil || len(results) != 51 {
		t.Fatalf("partial results lost: %d, %v", len(results), err)
	}
	state, err := journal.read()
	if err != nil {
		t.Fatal(err)
	}
	report := state.report(journal.runID)
	if report.Pending != 51 || report.Unknown != 1 {
		t.Fatalf("ownership lost: %+v", report)
	}
	if _, err := uploader.UploadBatch(t.Context(), paths[50:]); err == nil || calls != 2 {
		t.Fatal("ambiguous upload retried")
	}
}

func TestLiveImagesUnknownUploadAndJournalWriteFailure(t *testing.T) {
	for _, failure := range []string{"transport", "malformed", "journal"} {
		t.Run(failure, func(t *testing.T) {
			journal, paths, cfg := liveImagesFixture(t, 1)
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls++
				if failure == "transport" {
					return nil, errors.New("private transport error https://private.example/")
				}
				return imageTestResponse(http.StatusOK, `{"url":`), nil
			})}
			if failure == "journal" {
				if err := os.Remove(journal.path); err != nil {
					t.Fatal(err)
				}
			}
			uploader := testLiveLostimgUploader(t, journal, cfg, client, len(paths)*2)
			_, err := uploader.UploadBatch(t.Context(), paths)
			if err == nil || strings.Contains(err.Error(), "private.example") || strings.Contains(err.Error(), cfg.ImageHosting.LostimgAPI) {
				t.Fatalf("unsafe failure: %v", err)
			}
			if failure == "journal" {
				if calls != 0 {
					t.Fatal("upload before journal availability")
				}
				return
			}
			operationFailure, ok := api.AsOperationFailure(err)
			if !ok || operationFailure.Code != api.OperationFailureUnknownOutcome {
				t.Fatalf("upload failure is not typed unknown outcome: %v, %#v", err, operationFailure)
			}
			report, err := cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
			if err == nil || report.Unknown != 1 || calls != 1 {
				t.Fatal("unknown upload was treated as no effect")
			}
		})
	}
}

func TestLiveImagesSerializesIdenticalNonBatchUploads(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 2)
	contents, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths[1], contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.ScreenshotHandling.MaxConcurrentUploads = 2
	release := make(chan struct{})
	delegate := &controlledLiveTestUploader{
		entered: make(chan string, 2),
		release: release,
	}
	service := NewServiceWithRegistry(cfg, api.NopLogger{}, nil, imageHostingTestRegistry(t))
	service.uploaders = map[string]uploader{
		"pixhost": wrapLiveTestUploader("pixhost", delegate, journal, len(paths)),
	}
	done := make(chan struct {
		links []api.UploadedImageLink
		err   error
	}, 1)
	go func() {
		links, uploadErr := service.Upload(t.Context(), api.ImageHostingSubject{SourcePath: "Example.Release.2026.mkv"}, "pixhost", "global", []api.ScreenshotImage{
			{Path: paths[0]},
			{Path: paths[1]},
		})
		done <- struct {
			links []api.UploadedImageLink
			err   error
		}{links: links, err: uploadErr}
	}()

	select {
	case <-delegate.entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first upload did not start")
	}
	prematureSecond := false
	select {
	case <-delegate.entered:
		prematureSecond = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	result := <-done
	if prematureSecond {
		t.Fatal("identical upload entered provider while the first outcome was pending")
	}
	if result.err != nil || len(result.links) != len(paths) || delegate.callCount() != len(paths) {
		t.Fatalf("serialized uploads: links=%d calls=%d err=%v", len(result.links), delegate.callCount(), result.err)
	}
	state, err := journal.read()
	if err != nil {
		t.Fatal(err)
	}
	report := state.report(journal.runID)
	if report.Retained != len(paths) || report.Unknown+report.Pending != 0 {
		t.Fatalf("serialized upload journal = %+v", report)
	}
}

func TestLiveImagesSerializesConcurrentBatchUploads(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		delegate func(*controlledLiveTestUploader) uploader
		upload   func(context.Context, uploader, string) ([]uploadResult, error)
	}{
		{
			name: "batch",
			delegate: func(control *controlledLiveTestUploader) uploader {
				return &controlledLiveTestBatchUploader{controlledLiveTestUploader: control}
			},
			upload: func(ctx context.Context, guarded uploader, path string) ([]uploadResult, error) {
				batch, ok := guarded.(batchUploader)
				if !ok {
					return nil, fmt.Errorf("guarded uploader %T lost batch support", guarded)
				}
				return batch.UploadBatch(ctx, []string{path})
			},
		},
		{
			name: "named batch",
			delegate: func(control *controlledLiveTestUploader) uploader {
				return &controlledLiveTestNamedBatchUploader{
					controlledLiveTestBatchUploader: &controlledLiveTestBatchUploader{controlledLiveTestUploader: control},
				}
			},
			upload: func(ctx context.Context, guarded uploader, path string) ([]uploadResult, error) {
				named, ok := guarded.(namedBatchUploader)
				if !ok {
					return nil, fmt.Errorf("guarded uploader %T lost named batch support", guarded)
				}
				return named.UploadBatchWithName(ctx, []string{path}, "Synthetic gallery")
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			journal, paths, _ := liveImagesFixture(t, 2)
			contents, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths[1], contents, 0o600); err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			control := &controlledLiveTestUploader{
				entered: make(chan string, 2),
				release: release,
			}
			guarded := wrapLiveTestUploader("pixhost", scenario.delegate(control), journal, len(paths))
			done := make(chan error, 2)
			run := func(path string) {
				results, uploadErr := scenario.upload(t.Context(), guarded, path)
				if uploadErr == nil && len(results) != 1 {
					uploadErr = fmt.Errorf("upload results = %d", len(results))
				}
				done <- uploadErr
			}
			go run(paths[0])
			select {
			case <-control.entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("first batch upload did not start")
			}
			go run(paths[1])
			prematureSecond := false
			select {
			case <-control.entered:
				prematureSecond = true
			case <-time.After(200 * time.Millisecond):
			}
			close(release)
			for range 2 {
				if err := <-done; err != nil {
					t.Fatalf("serialized batch upload: %v", err)
				}
			}
			if prematureSecond || control.callCount() != len(paths) {
				t.Fatalf("concurrent provider calls: premature=%t calls=%d", prematureSecond, control.callCount())
			}
			state, err := journal.read()
			if err != nil {
				t.Fatal(err)
			}
			report := state.report(journal.runID)
			if report.Retained != len(paths) || report.Unknown+report.Pending != 0 {
				t.Fatalf("serialized batch journal = %+v", report)
			}
		})
	}
}

func TestLiveImagesNonBatchUnknownOutcomeIsNotRetried(t *testing.T) {
	journal, paths, _ := liveImagesFixture(t, 1)
	hashes, err := imageSourceHashes(paths)
	if err != nil {
		t.Fatal(err)
	}
	pending := journal.recordForProvider("upload_pending", "interrupted", "pixhost")
	pending.Sources = hashes
	if err := journal.append(pending); err != nil {
		t.Fatal(err)
	}
	delegate := &controlledLiveTestUploader{}
	guard := wrapLiveTestUploader("pixhost", delegate, journal, 2)
	_, err = guard.Upload(t.Context(), paths[0])
	failure, ok := api.AsOperationFailure(err)
	if !ok || failure.Code != api.OperationFailureUnknownOutcome || delegate.callCount() != 0 {
		t.Fatalf("interrupted upload retry: calls=%d err=%v failure=%#v", delegate.callCount(), err, failure)
	}
}

func TestLiveImagesCleanupUnconfirmedNeverRetries(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		response string
		deleted  int
	}{
		{
			name:     "partial",
			status:   200,
			response: `{"url":"https://lostimg.cc/one.png"}`,
			deleted:  1,
		},
		{
			name:     "unowned",
			status:   200,
			response: `{"urls":["https://lostimg.cc/one.png","https://lostimg.cc/unowned.png"]}`,
		},
		{
			name:     "missing",
			status:   200,
			response: `{}`,
		},
		{
			name:     "error",
			status:   404,
			response: `{"error":"Not found"}`,
		},
		{
			name:     "malformed",
			status:   200,
			response: `invalid`,
		},
		{name: "transport"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			journal, _, cfg := liveImagesFixture(t, 0)
			record := journal.record("upload_pending", "attempt")
			record.Sources = []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
			if err := journal.append(record); err != nil {
				t.Fatal(err)
			}
			record = journal.record("uploaded", "attempt")
			record.URLs = []string{"https://lostimg.cc/one.png", "https://lostimg.cc/two.png"}
			record.Uploaded = 2
			record.Complete = true
			if err := journal.append(record); err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls++
				if scenario.name == "transport" {
					return nil, errors.New("response lost")
				}
				return imageTestResponse(scenario.status, scenario.response), nil
			})}
			for range 2 {
				report, err := cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
				if err != nil || report.Deleted != scenario.deleted || report.Retained != 2-scenario.deleted || report.Unknown != 0 || calls != 1 {
					t.Fatalf("unsafe cleanup/retry: %+v calls=%d err=%v", report, calls, err)
				}
				for _, image := range report.Images {
					if image.State == "retained" && image.Reason != "deletion_unconfirmed" {
						t.Fatalf("unconfirmed deletion lacks retained reason: %+v", image)
					}
				}
			}
		})
	}
}

func TestLiveImagesJournalOwnershipAndCrashRecovery(t *testing.T) {
	for _, scenario := range []string{"run_mismatch", "version", "unowned", "torn", "upload_crash", "delete_crash"} {
		t.Run(scenario, func(t *testing.T) {
			journal, _, cfg := liveImagesFixture(t, 0)
			runID := journal.runID
			record := journal.record("upload_pending", "attempt")
			record.Sources = []string{strings.Repeat("a", 64)}
			if scenario == "version" {
				record.Version++
			}
			if scenario == "unowned" {
				record.Kind = "uploaded"
				record.Sources = nil
				record.URLs = []string{"https://lostimg.cc/unowned.png"}
			}
			appendRecord := journal.append
			if scenario == "version" || scenario == "unowned" {
				appendRecord = func(record imageEffectRecord) error {
					return journal.write(record, os.O_APPEND|os.O_WRONLY)
				}
			}
			if err := appendRecord(record); err != nil {
				t.Fatal(err)
			}
			if scenario == "run_mismatch" {
				runID = "another-run"
			}
			if scenario == "torn" {
				file, err := os.OpenFile(journal.path, os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString(`{"version":`); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "delete_crash" {
				record = journal.record("uploaded", "attempt")
				record.URLs = []string{"https://lostimg.cc/one.png"}
				record.Uploaded = 1
				record.Complete = true
				if err := journal.append(record); err != nil {
					t.Fatal(err)
				}
				record = journal.record("cleanup_pending", "deletion")
				record.Images = []CleanupImageResult{{ID: "attempt_0", State: "cleanup_pending"}}
				if err := journal.append(record); err != nil {
					t.Fatal(err)
				}
			}
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				t.Fatal("unowned or unknown effect dispatched")
				return nil, errors.New("unexpected")
			})}
			report, err := cleanupLiveTestImages(t.Context(), cfg, runID, journal.path, client)
			if scenario == "delete_crash" {
				if err != nil || report.Retained != 1 || report.Unknown != 0 {
					t.Fatalf("uncertain deletion did not become terminal retained state: %+v, %v", report, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid or unknown effect accepted")
			}
			if scenario == "upload_crash" && report.Unknown != 1 {
				t.Fatalf("crash outcome lost: %+v", report)
			}
		})
	}
}

func TestLiveImagesWrapsGenericProviderAndRetainsSuccess(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 1)
	service, err := NewLiveTestServiceWithRegistry(cfg, nil, nil, imageHostingTestRegistry(t), journal.runID, journal.path, 1)
	if err != nil {
		t.Fatal(err)
	}
	guard, ok := service.uploaders["pixhost"].(*liveTestUploader)
	if !ok {
		t.Fatalf("pixhost guard = %T", service.uploaders["pixhost"])
	}
	pixhost, ok := guard.delegate.(*pixhostUploader)
	if !ok {
		t.Fatalf("pixhost delegate = %T", guard.delegate)
	}
	pixhost.client = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return imageTestResponse(http.StatusOK, `{"th_url":"https://t1.pixhost.invalid/thumbs/one.png","show_url":"https://pixhost.invalid/show/one"}`), nil
	})}
	links, err := service.Upload(
		t.Context(), api.ImageHostingSubject{SourcePath: paths[0]}, "pixhost", "global", []api.ScreenshotImage{{Path: paths[0]}},
	)
	if err != nil || len(links) != 1 {
		t.Fatalf("generic live upload: links=%#v err=%v", links, err)
	}
	state, err := journal.read()
	if err != nil {
		t.Fatal(err)
	}
	attempt := state.attempts[state.order[0]]
	if attempt.provider != "pixhost" || !attempt.complete || attempt.uploaded != 1 || len(attempt.urls) != 3 {
		t.Fatalf("generic provider journal = %#v", attempt)
	}
	report, err := cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, &http.Client{})
	if err != nil || report.Retained != 1 || report.Pending+report.Unknown+report.Deleted != 0 {
		t.Fatalf("retained provider cleanup = %+v err=%v", report, err)
	}
	if _, ok := service.uploaders["lostimg"].(batchUploader); !ok {
		t.Fatal("live wrapper removed Lostimg batch support")
	}
	if _, ok := service.uploaders["hdb"].(namedBatchUploader); !ok {
		t.Fatal("live wrapper removed HDB named-batch support")
	}
	if _, ok := service.uploaders["pixhost"].(batchUploader); ok {
		t.Fatal("live wrapper added batch support to pixhost")
	}
}

func TestLiveImagesBlocksCredentialRedirects(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 1)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != lostimgImagesEndpoint {
			t.Fatal("credentials followed redirect")
		}
		response := imageTestResponse(http.StatusTemporaryRedirect, "")
		response.Header.Set("Location", "https://lostimg.cc/redirected")
		return response, nil
	})}
	uploader := testLiveLostimgUploader(t, journal, cfg, client, len(paths)*2)
	if _, err := uploader.UploadBatch(t.Context(), paths); err == nil || calls != 1 {
		t.Fatal("upload redirect followed")
	}
	confirmed := deleteLiveImages(t.Context(), client, cfg.ImageHosting.LostimgAPI, []string{"https://lostimg.cc/owned.png"})
	if len(confirmed) != 0 || calls != 2 {
		t.Fatal("delete redirect followed")
	}
}

func TestLiveImagesUnavailableCleanupRetainsKnownUploads(t *testing.T) {
	for _, scenario := range []string{"canceled", "missing_auth"} {
		t.Run(scenario, func(t *testing.T) {
			journal, _, cfg := liveImagesFixture(t, 0)
			record := journal.record("upload_pending", "attempt")
			record.Sources = []string{strings.Repeat("a", 64)}
			if err := journal.append(record); err != nil {
				t.Fatal(err)
			}
			record = journal.record("uploaded", "attempt")
			record.URLs = []string{"https://lostimg.cc/one.png"}
			record.Uploaded = 1
			record.Complete = true
			if err := journal.append(record); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			if scenario == "canceled" {
				cancel()
			} else {
				cfg.ImageHosting.LostimgAPI = ""
			}
			defer cancel()
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				t.Fatal("unavailable cleanup dispatched a deletion")
				return nil, errors.New("unexpected")
			})}
			report, err := cleanupLiveTestImages(ctx, cfg, journal.runID, journal.path, client)
			if err != nil || report.Retained != 1 || report.Pending+report.Unknown != 0 ||
				len(report.Images) != 1 || report.Images[0].Reason != "deletion_unconfirmed" {
				t.Fatalf("unavailable cleanup result: %+v %v", report, err)
			}
		})
	}
}

func TestLiveImagesResponseOwnershipFieldsSurviveMalformedFields(t *testing.T) {
	for _, body := range []string{
		`{"urls":["https://lostimg.cc/owned.png"],"error":5}`,
		`{"urls":["https://lostimg.cc/owned.png","not-a-url"]}`,
		`{"url":"https://lostimg.cc/owned.png","error":"provider failure"}`,
	} {
		urls, valid := parseLiveImageURLs([]byte(body))
		if valid || !slices.Equal(urls, []string{"https://lostimg.cc/owned.png"}) {
			t.Fatal("recoverable ownership fields were discarded")
		}
	}
}

func TestLiveImagesResponseJournalFailureDoesNotExposeSuccess(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 1)
	retainedPath := filepath.Join(filepath.Dir(journal.path), "retained.jsonl")
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		if err := os.Rename(journal.path, retainedPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(journal.path, 0o700); err != nil {
			t.Fatal(err)
		}
		return imageTestResponse(http.StatusOK, `{"url":"https://lostimg.cc/owned.png"}`), nil
	})}
	uploader := testLiveLostimgUploader(t, journal, cfg, client, len(paths)*2)
	results, err := uploader.UploadBatch(t.Context(), paths)
	if err == nil || len(results) != 0 {
		t.Fatal("upload success exposed without durable returned URL")
	}
	journal.path = retainedPath
	state, err := journal.read()
	if err != nil {
		t.Fatal(err)
	}
	if state.report(journal.runID).Unknown != 1 {
		t.Fatal("attempt lost after response journal failure")
	}
}

func TestLiveImagesConcurrentCleanupSharesAcknowledgment(t *testing.T) {
	journal, _, cfg := liveImagesFixture(t, 0)
	record := journal.record("upload_pending", "attempt")
	record.Sources = []string{strings.Repeat("a", 64)}
	if err := journal.append(record); err != nil {
		t.Fatal(err)
	}
	record = journal.record("uploaded", "attempt")
	record.URLs = []string{"https://lostimg.cc/owned.png"}
	record.Uploaded = 1
	record.Complete = true
	if err := journal.append(record); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
		}
		return imageTestResponse(http.StatusOK, `{"url":"https://lostimg.cc/owned.png"}`), nil
	})}
	outcomes := make(chan CleanupReport, 2)
	failures := make(chan error, 2)
	cleanup := func() {
		report, err := cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
		outcomes <- report
		failures <- err
	}
	go cleanup()
	<-entered
	go cleanup()
	close(release)
	for range 2 {
		if report := <-outcomes; report.Deleted != 1 {
			t.Fatalf("concurrent cleanup lost acknowledgment: %+v", report)
		}
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("concurrent cleanup repeated deletion")
	}
}

func TestLiveImagesBudgetBlocksWholeRequestBeforeDispatch(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		budget int
		count  int
	}{
		{
			name:   "zero",
			budget: 0,
			count:  1,
		},
		{
			name:   "exceeded_before_first_chunk",
			budget: 50,
			count:  51,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			journal, paths, cfg := liveImagesFixture(t, scenario.count)
			service, err := NewLiveTestServiceWithRegistry(cfg, nil, nil, imageHostingTestRegistry(t), journal.runID, journal.path, scenario.budget)
			if err != nil {
				t.Fatal(err)
			}
			guard := testLiveBatchGuard(t, service.uploaders["lostimg"])
			uploader, ok := guard.batch.(*lostimgUploader)
			if !ok {
				t.Fatalf("lostimg delegate = %T", guard.batch)
			}
			uploader.client = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				t.Fatal("over-budget request dispatched")
				return nil, errors.New("unexpected request")
			})}
			if _, err := guard.UploadBatch(t.Context(), paths); !errors.Is(err, errLiveImageBudget) {
				t.Fatalf("budget not enforced: %v", err)
			}
			state, err := journal.read()
			if err != nil {
				t.Fatal(err)
			}
			if len(state.attempts) != 0 {
				t.Fatal("over-budget request wrote upload attempt")
			}
		})
	}
}

func TestLiveImagesJournalSupportsMaximumFlattenedBatch(t *testing.T) {
	journal, _, _ := liveImagesFixture(t, 0)
	pending := journal.recordForProvider("upload_pending", "maximum-batch", "pixhost")
	for index := range api.MaxLiveTestImageUploads {
		pending.Sources = append(pending.Sources, fmt.Sprintf("%064x", index+1))
	}
	if err := journal.append(pending); err != nil {
		t.Fatal(err)
	}
	uploaded := journal.recordForProvider("uploaded", pending.ID, "pixhost")
	uploaded.Uploaded = api.MaxLiveTestImageUploads
	uploaded.Complete = true
	for index := range api.MaxLiveTestImageUploads {
		for variant := range 3 {
			uploaded.URLs = append(uploaded.URLs, fmt.Sprintf(
				"https://images.invalid/%03d/%d/%s", index, variant, strings.Repeat("a", 32),
			))
		}
	}
	if err := journal.append(uploaded); err != nil {
		t.Fatal(err)
	}
	state, err := journal.read()
	if err != nil {
		t.Fatal(err)
	}
	report := state.report(journal.runID)
	if report.Retained != api.MaxLiveTestImageUploads || report.Unknown+report.Pending != 0 || len(report.Images) != api.MaxLiveTestImageUploads {
		t.Fatalf("maximum batch report = %+v", report)
	}
}

func TestLiveImagesBudgetCountsAttemptsAfterRestart(t *testing.T) {
	for _, outcome := range []string{"success", "failed", "unknown", "interrupted"} {
		t.Run(outcome, func(t *testing.T) {
			journal, paths, cfg := liveImagesFixture(t, 2)
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls++
				switch outcome {
				case "failed":
					return imageTestResponse(http.StatusBadRequest, `{"error":"failed"}`), nil
				case "unknown":
					return nil, errors.New("response lost")
				default:
					return imageTestResponse(http.StatusOK, `{"url":"https://lostimg.cc/owned.png"}`), nil
				}
			})}
			uploader := testLiveLostimgUploader(t, journal, cfg, client, 1)
			if outcome == "interrupted" {
				hashes, err := imageSourceHashes(paths[:1])
				if err != nil {
					t.Fatal(err)
				}
				record := journal.record("upload_pending", "interrupted")
				record.Sources = hashes
				if err := journal.append(record); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := uploader.UploadBatch(t.Context(), paths[:1])
				if (err == nil) != (outcome == "success") {
					t.Fatalf("unexpected initial upload: %v", err)
				}
			}
			initialCalls := calls
			service, err := NewLiveTestServiceWithRegistry(cfg, nil, nil, imageHostingTestRegistry(t), journal.runID, journal.path, 1)
			if err != nil {
				t.Fatal(err)
			}
			resumed := testLiveBatchGuard(t, service.uploaders["lostimg"])
			lostimg, ok := resumed.batch.(*lostimgUploader)
			if !ok {
				t.Fatalf("lostimg delegate = %T", resumed.batch)
			}
			lostimg.client = client
			if _, err := resumed.UploadBatch(t.Context(), paths[1:]); !errors.Is(err, errLiveImageBudget) {
				t.Fatalf("resumed budget not enforced: %v", err)
			}
			state, err := journal.read()
			if err != nil {
				t.Fatal(err)
			}
			if calls != initialCalls || len(state.attempts) != 1 {
				t.Fatal("exhausted budget allowed another attempt")
			}
		})
	}
}

func TestLiveImagesBudgetRejectsNegativeConstructorValue(t *testing.T) {
	_, _, cfg := liveImagesFixture(t, 0)
	path := filepath.Join(t.TempDir(), "image-effects.private.jsonl")
	if _, err := NewLiveTestServiceWithRegistry(cfg, nil, nil, imageHostingTestRegistry(t), "test-run", path, -1); err == nil {
		t.Fatal("negative image budget accepted")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("negative budget initialized journal")
	}
	if _, err := NewLiveTestServiceWithRegistry(
		cfg, nil, nil, imageHostingTestRegistry(t), "test-run", path, api.MaxLiveTestImageUploads+1,
	); err == nil {
		t.Fatal("over-limit image budget accepted")
	}
}

func TestLiveImagesReadsLegacyLostimgJournal(t *testing.T) {
	journal, paths, cfg := legacyLiveImagesFixture(t, 51)
	dir := filepath.Dir(journal.path)
	pending := journal.record("upload_pending", "attempt")
	pending.Sources = []string{strings.Repeat("a", 64)}
	if err := journal.append(pending); err != nil {
		t.Fatal(err)
	}
	uploaded := journal.record("uploaded", "attempt")
	uploaded.URLs = []string{"https://lostimg.cc/legacy.png"}
	uploaded.Complete = true
	if err := journal.append(uploaded); err != nil {
		t.Fatal(err)
	}
	state, err := journal.read()
	if err != nil {
		t.Fatal(err)
	}
	report := state.report(journal.runID)
	if state.version != legacyImageJournalVersion || report.Pending != 1 || report.Unknown+report.Retained != 0 {
		t.Fatalf("legacy journal = version %d report %+v", state.version, report)
	}
	hashes, err := imageSourceHashes(paths)
	if err != nil {
		t.Fatal(err)
	}
	oversized := journal.record("upload_pending", "oversized")
	oversized.Sources = hashes
	if err := journal.append(oversized); !errors.Is(err, errImageJournal) {
		t.Fatalf("oversized legacy attempt append = %v", err)
	}
	state, err = journal.read()
	if err != nil || len(state.attempts) != 1 {
		t.Fatalf("oversized append corrupted legacy journal: state=%#v err=%v", state, err)
	}

	var uploadSizes []int
	var uploadedURLs []string
	uploadClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		if err := request.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = request.MultipartForm.RemoveAll() })
		size := len(request.MultipartForm.File["file[]"])
		uploadSizes = append(uploadSizes, size)
		urls := make([]string, size)
		for index := range size {
			urls[index] = fmt.Sprintf("https://lostimg.cc/legacy-upload-%02d.png", len(uploadedURLs)+index)
		}
		uploadedURLs = append(uploadedURLs, urls...)
		body, err := json.Marshal(map[string]any{"urls": urls})
		if err != nil {
			t.Fatal(err)
		}
		return imageTestResponse(http.StatusOK, string(body)), nil
	})}
	uploader := testLiveLostimgUploader(t, journal, cfg, uploadClient, len(paths)+1)
	results, err := uploader.UploadBatch(t.Context(), paths)
	if err != nil || len(results) != len(paths) || !slices.Equal(uploadSizes, []int{50, 1}) {
		t.Fatalf("legacy journal upload after restart: %v", err)
	}
	state, err = journal.read()
	if err != nil || state.report(journal.runID).Pending != len(paths)+1 || len(state.attempts) != 3 {
		t.Fatalf("legacy journal after upload: %#v, %v", state, err)
	}
	for _, id := range state.order[1:] {
		attempt := state.attempts[id]
		if len(attempt.sources) > lostimgMaxBatchUploadImages || !attempt.complete || !attempt.returned {
			t.Fatalf("legacy chunk attempt %s = %#v", id, attempt)
		}
	}
	extraPath := filepath.Join(dir, "legacy-over-budget.png")
	if err := os.WriteFile(extraPath, []byte("synthetic-over-budget-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := uploader.UploadBatch(t.Context(), []string{extraPath}); !errors.Is(err, errLiveImageBudget) || len(uploadSizes) != 2 {
		t.Fatalf("legacy resumed budget: requests=%v err=%v", uploadSizes, err)
	}

	var deleteSizes []int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodDelete {
			t.Fatalf("method = %s", request.Method)
		}
		var body struct {
			URLs []string `json:"urls"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		deleteSizes = append(deleteSizes, len(body.URLs))
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return imageTestResponse(http.StatusOK, string(encoded)), nil
	})}
	report, err = cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
	if err != nil || report.Deleted != len(paths)+1 || report.Pending+report.Unknown+report.Retained != 0 ||
		!slices.Equal(deleteSizes, []int{50, 2}) {
		t.Fatalf("legacy cleanup = %+v batches=%v err=%v", report, deleteSizes, err)
	}
	state, err = journal.read()
	if err != nil {
		t.Fatal(err)
	}
	if state.version != legacyImageJournalVersion {
		t.Fatalf("legacy journal after cleanup version = %d", state.version)
	}
}

func TestLiveImagesLegacySecondChunkFailureRemainsRecoverable(t *testing.T) {
	journal, paths, cfg := legacyLiveImagesFixture(t, 51)
	var uploadSizes []int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := request.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = request.MultipartForm.RemoveAll() })
		size := len(request.MultipartForm.File["file[]"])
		uploadSizes = append(uploadSizes, size)
		if len(uploadSizes) == 2 {
			return imageTestResponse(http.StatusOK, `{"url":`), nil
		}
		urls := make([]string, size)
		for index := range size {
			urls[index] = fmt.Sprintf("https://lostimg.cc/legacy-partial-%02d.png", index)
		}
		body, err := json.Marshal(map[string]any{"urls": urls})
		if err != nil {
			t.Fatal(err)
		}
		return imageTestResponse(http.StatusOK, string(body)), nil
	})}
	uploader := testLiveLostimgUploader(t, journal, cfg, client, len(paths))
	results, err := uploader.UploadBatch(t.Context(), paths)
	failure, ok := api.AsOperationFailure(err)
	if !ok || failure.Code != api.OperationFailureUnknownOutcome || len(results) != 50 || !slices.Equal(uploadSizes, []int{50, 1}) {
		t.Fatalf("legacy partial upload: results=%d batches=%v err=%v failure=%#v", len(results), uploadSizes, err, failure)
	}
	state, err := journal.read()
	if err != nil {
		t.Fatalf("read partial legacy journal: %v", err)
	}
	report := state.report(journal.runID)
	if len(state.attempts) != 2 || report.Pending != 50 || report.Unknown != 1 {
		t.Fatalf("partial legacy journal = %+v attempts=%d", report, len(state.attempts))
	}
	if _, err := uploader.UploadBatch(t.Context(), paths[50:]); err == nil || len(uploadSizes) != 2 {
		t.Fatalf("uncertain legacy chunk retried: requests=%v err=%v", uploadSizes, err)
	}

	var deleteSizes []int
	cleanupClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body struct {
			URLs []string `json:"urls"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		deleteSizes = append(deleteSizes, len(body.URLs))
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return imageTestResponse(http.StatusOK, string(encoded)), nil
	})}
	report, err = cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, cleanupClient)
	if err == nil || report.Deleted != 50 || report.Unknown != 1 || !slices.Equal(deleteSizes, []int{50}) {
		t.Fatalf("partial legacy cleanup = %+v batches=%v err=%v", report, deleteSizes, err)
	}
}

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
	"testing"

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

func imageTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
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
			if attempt.returned || len(attempt.sources) != size {
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
	uploader := &lostimgUploader{
		apiKey:    cfg.ImageHosting.LostimgAPI,
		client:    client,
		journal:   journal,
		maxImages: len(paths) * 2,
	}
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
	uploader := &lostimgUploader{
		apiKey:    cfg.ImageHosting.LostimgAPI,
		client:    client,
		journal:   journal,
		maxImages: len(paths) * 2,
	}
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
			uploader := &lostimgUploader{
				apiKey:    cfg.ImageHosting.LostimgAPI,
				client:    client,
				journal:   journal,
				maxImages: len(paths) * 2,
			}
			_, err := uploader.UploadBatch(t.Context(), paths)
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe failure: %v", err)
			}
			if failure == "journal" {
				if calls != 0 {
					t.Fatal("upload before journal availability")
				}
				return
			}
			report, err := cleanupLiveTestImages(t.Context(), cfg, journal.runID, journal.path, client)
			if err == nil || report.Unknown != 1 || calls != 1 {
				t.Fatal("unknown upload was treated as no effect")
			}
		})
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
				if err == nil || report.Deleted != scenario.deleted || report.Unknown != 2-scenario.deleted || calls != 1 {
					t.Fatalf("unsafe cleanup/retry: %+v calls=%d err=%v", report, calls, err)
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
			if err := journal.append(record); err != nil {
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
			if err == nil {
				t.Fatal("invalid or unknown effect accepted")
			}
			if (scenario == "upload_crash" || scenario == "delete_crash") && report.Unknown != 1 {
				t.Fatalf("crash outcome lost: %+v", report)
			}
		})
	}
}

func TestLiveImagesBlocksUnsupportedHostAndRedirects(t *testing.T) {
	journal, paths, cfg := liveImagesFixture(t, 1)
	service, err := NewLiveTestServiceWithRegistry(cfg, nil, nil, imageHostingTestRegistry(t), journal.runID, journal.path, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Upload(t.Context(), api.ImageHostingSubject{SourcePath: paths[0]}, "pixhost", "global", []api.ScreenshotImage{{Path: paths[0]}})
	if err == nil {
		t.Fatal("unsupported cleanup provider accepted")
	}
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
	uploader := &lostimgUploader{
		apiKey:    cfg.ImageHosting.LostimgAPI,
		client:    client,
		journal:   journal,
		maxImages: len(paths) * 2,
	}
	if _, err := uploader.UploadBatch(t.Context(), paths); err == nil || calls != 1 {
		t.Fatal("upload redirect followed")
	}
	confirmed := deleteLiveImages(t.Context(), client, cfg.ImageHosting.LostimgAPI, []string{"https://lostimg.cc/owned.png"})
	if len(confirmed) != 0 || calls != 2 {
		t.Fatal("delete redirect followed")
	}
}

func TestLiveImagesCanceledBeforeDispatchStaysPending(t *testing.T) {
	journal, _, cfg := liveImagesFixture(t, 0)
	record := journal.record("upload_pending", "attempt")
	record.Sources = []string{strings.Repeat("a", 64)}
	if err := journal.append(record); err != nil {
		t.Fatal(err)
	}
	record = journal.record("uploaded", "attempt")
	record.URLs = []string{"https://lostimg.cc/one.png"}
	record.Complete = true
	if err := journal.append(record); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report, err := cleanupLiveTestImages(ctx, cfg, journal.runID, journal.path, &http.Client{})
	if !errors.Is(err, context.Canceled) || report.Pending != 1 || report.Unknown != 0 {
		t.Fatalf("canceled cleanup lost retry eligibility: %+v %v", report, err)
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
	uploader := &lostimgUploader{
		apiKey:    cfg.ImageHosting.LostimgAPI,
		client:    client,
		journal:   journal,
		maxImages: len(paths) * 2,
	}
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
			uploader, ok := service.uploaders["lostimg"].(*lostimgUploader)
			if !ok {
				t.Fatal("Lostimg uploader missing")
			}
			uploader.client = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				t.Fatal("over-budget request dispatched")
				return nil, errors.New("unexpected request")
			})}
			if _, err := uploader.UploadBatch(t.Context(), paths); !errors.Is(err, errLiveImageBudget) {
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
			uploader := &lostimgUploader{
				apiKey:    cfg.ImageHosting.LostimgAPI,
				client:    client,
				journal:   journal,
				maxImages: 1,
			}
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
			resumed, ok := service.uploaders["lostimg"].(*lostimgUploader)
			if !ok {
				t.Fatal("Lostimg uploader missing")
			}
			resumed.client = client
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
}

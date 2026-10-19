// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCollectionServiceReusesExactCompletedSet(t *testing.T) {
	t.Parallel()
	for _, count := range []int{1, 2} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			input.Images, input.Labels = input.Images[:count], input.Labels[:count]
			first, err := service.UploadCollection(t.Context(), subject, input, false)
			if err != nil || first == nil || first.Validate() != nil {
				t.Fatalf("first=%+v error=%v", first, err)
			}
			// A new service instance sees the same durable completion without another POST.
			service = collectionTestService(repo, remote)
			input.Columns = 1 // A single source and the default sequence share the same viewer row.
			for _, skip := range []bool{false, true} {
				result, err := service.UploadCollection(t.Context(), subject, input, skip)
				if err != nil || !reflect.DeepEqual(result, first) {
					t.Fatalf("skip=%v result=%+v error=%v", skip, result, err)
				}
			}
			if remote.creates != 1 || remote.members != len(input.Images) || repo.reserves != 1 || repo.completes != 1 || repo.releases != 0 {
				t.Fatalf("creates=%d members=%d reserves=%d completes=%d releases=%d", remote.creates, remote.members, repo.reserves, repo.completes, repo.releases)
			}
			if repo.savedImages != nil {
				t.Fatal("collection stored as ordinary UploadedImageLink")
			}
		})
	}
}

func TestCollectionServiceUsesOperationLogger(t *testing.T) {
	t.Parallel()
	repo := &collectionTestRepository{}
	remote := &collectionTestRemote{t: t}
	service, subject, input := collectionTestSetup(t, repo, remote)
	root, operation := &recordingImageHostLogger{}, &recordingImageHostLogger{}
	service.logger = root
	ctx := logging.WithOperationLogger(t.Context(), operation)
	for range 2 {
		if _, err := service.UploadCollection(ctx, subject, input, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(root.messages) != 0 || !operation.contains("request stage=create state=started") ||
		!operation.contains("state=completed stage=persist") || !operation.contains("state=found completed=true") {
		t.Fatalf("collection logs escaped operation context: root=%v operation=%v", root.messages, operation.messages)
	}
	if remote.creates != 1 || remote.members != len(input.Images) {
		t.Fatal("logging changed collection reuse")
	}
}

func TestCollectionServiceCombinedAnalysisKeepsPurposeBoundary(t *testing.T) {
	t.Parallel()
	for _, purpose := range []api.ScreenshotPurpose{api.ScreenshotPurposeAudioAnalysis, api.ScreenshotPurposeHDRAnalysis, api.ScreenshotPurposeFinal, api.ScreenshotPurposeMenu, api.ScreenshotPurposeComparison, api.ScreenshotPurposePreview} {
		t.Run(string(purpose), func(t *testing.T) {
			t.Parallel()
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			input.Category = api.ImageCollectionCategoryAnalysis
			input.Images[0].Purpose = purpose
			result, err := service.UploadCollection(t.Context(), subject, input, false)
			if purpose.IsAnalysis() {
				if err != nil || result == nil || result.Validate() != nil || remote.creates != 1 || remote.members != 2 {
					t.Fatalf("result=%+v err=%v creates=%d members=%d", result, err, remote.creates, remote.members)
				}
			} else if err == nil || result != nil || remote.requests != 0 || repo.reserves != 0 {
				t.Fatalf("ordinary member accepted: result=%+v err=%v requests=%d", result, err, remote.requests)
			}
		})
	}
}

func TestCollectionServiceFingerprintRejectsChangedAuthority(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"order", "member bytes", "member label", "collection label", "category", "generation", "binding", "purpose", "configuration", "columns"} {
		t.Run(change, func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			if change == "columns" {
				input.Images = append(input.Images, input.Images...)
				input.Labels = append(input.Labels, input.Labels...)
			}
			first, err := service.UploadCollection(t.Context(), subject, input, false)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "columns":
				input.Columns = 2
			case "order":
				slices.Reverse(input.Images)
				slices.Reverse(input.Labels)
			case "member bytes":
				input.Images[0].Path = collectionTestPNG(t, 99)
			case "member label":
				input.Labels[0] = "Different frame"
			case "collection label":
				input.Label = "Different collection"
			case "category":
				input.Category = "Grade Checks"
			case "generation":
				input.Generation = "generation-two"
			case "binding":
				subject.MediaBinding.PreparedGeneration++
			case "purpose":
				input.Purpose = api.ScreenshotPurposeAudioAnalysis
				input.Category = "Audio Plots"
				for i := range input.Images {
					input.Images[i].Purpose = input.Purpose
				}
			case "configuration":
				service.cfg.ImageHosting.AnalysisHost = "imgbox"
			}
			result, err := service.UploadCollection(t.Context(), subject, input, true)
			if result != nil {
				t.Fatalf("changed %s reused collection %q from %q", change, result.Fingerprint, first.Fingerprint)
			}
			if change == "configuration" && err == nil {
				t.Fatal("unsupported collection config accepted")
			}
			if change != "configuration" && err != nil {
				t.Fatal(err)
			}
			if remote.creates != 1 || remote.members != len(input.Images) || repo.reserves != 1 {
				t.Fatalf("skip created or reserved new collection: creates=%d reserves=%d", remote.creates, repo.reserves)
			}
		})
	}
}

func TestCollectionViewerInvalidatesFlatCacheButKeepsUnknownFence(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"gallery", "single-source"} {
		for _, completed := range []bool{false, true} {
			t.Run(layout+"/"+strconv.FormatBool(completed), func(t *testing.T) {
				repo := &collectionTestRepository{}
				remote := &collectionTestRemote{t: t}
				service, subject, input := collectionTestSetup(t, repo, remote)
				first, err := service.UploadCollection(t.Context(), subject, input, false)
				if err != nil {
					t.Fatal(err)
				}
				legacy := *first
				legacy.Fingerprint, legacy.URL, legacy.AttemptID, legacy.EffectID, legacy.Completed = "", "", "", "", false
				oldFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
					Binding    api.PreparedMediaBinding
					Generation string
					Collection api.ImageCollection
				}{subject.MediaBinding, input.Generation, legacy})
				if layout == "single-source" {
					input.Columns = 1
					oldFingerprint, err = api.CanonicalWorkflowFingerprint(struct {
						Binding    api.PreparedMediaBinding
						Generation string
						Collection api.ImageCollection
						Layout     string
						Columns    int
					}{subject.MediaBinding, input.Generation, legacy, "comparison-v1", 1})
				}
				if err != nil {
					t.Fatal(err)
				}
				legacy.Fingerprint, legacy.AttemptID, legacy.Completed = string(oldFingerprint), "older-flat-attempt", completed
				if completed {
					legacy.URL = "https://slow.pics/c/OlderGallery"
				}
				repo.records = map[string]api.ImageCollection{legacy.Fingerprint: legacy}
				calls := remote.requests
				cached, err := service.UploadCollection(t.Context(), subject, input, true)
				if cached != nil || err != nil || remote.requests != calls {
					t.Fatalf("old gallery reused or uploaded during skip: %+v %v", cached, err)
				}
				result, err := service.UploadCollection(t.Context(), subject, input, false)
				if completed {
					if err != nil || result == nil || result.Fingerprint == legacy.Fingerprint || remote.creates != 2 {
						t.Fatalf("gallery was not rebuilt as a viewer: %+v %v", result, err)
					}
				} else if !errors.Is(err, errSlowpicsOutcomeUnknown) || result != nil || remote.requests != calls || remote.creates != 1 {
					t.Fatalf("viewer bypassed pending gallery fence: %+v %v", result, err)
				}
			})
		}
	}
}

func TestCollectionServiceUsesFingerprintedSnapshot(t *testing.T) {
	t.Parallel()
	repo := &collectionTestRepository{}
	remote := &collectionTestRemote{t: t}
	service, subject, input := collectionTestSetup(t, repo, remote)
	original := make([][]byte, 0, len(input.Images))
	for _, img := range input.Images {
		data, err := os.ReadFile(img.Path)
		if err != nil {
			t.Fatal(err)
		}
		original = append(original, data)
	}
	repo.reserveHook = func() {
		for _, img := range input.Images {
			if err := os.WriteFile(img.Path, []byte("changed after reservation"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := service.UploadCollection(t.Context(), subject, input, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(remote.uploaded, original) {
		t.Fatal("upload reread mutated local files after fingerprinting")
	}
	for index, data := range original {
		digest := sha256.Sum256(data)
		if result.Members[index].Identity != hex.EncodeToString(digest[:]) {
			t.Fatal("persisted identity does not hash uploaded bytes")
		}
	}
}

func TestCollectionServiceUnknownOutcomePreventsRecreation(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"create", "member", "persistence"} {
		t.Run(failure, func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			if failure == "create" {
				remote.createStatus = http.StatusServiceUnavailable
			}
			if failure == "member" {
				remote.memberStatus = http.StatusForbidden
			}
			if failure == "persistence" {
				repo.completeErr = errors.New("synthetic persistence failure")
			}
			service, subject, input := collectionTestSetup(t, repo, remote)
			logger := &recordingImageHostLogger{}
			service.logger = logger
			result, err := service.UploadCollection(t.Context(), subject, input, false)
			if result != nil || !errors.Is(err, errSlowpicsOutcomeUnknown) {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			operation, ok := api.AsOperationFailure(err)
			if !ok || operation.Code != api.OperationFailureUnknownOutcome || operation.Recovery != api.OperationRecoveryNone {
				t.Fatalf("missing typed unknown outcome: %v", err)
			}
			stage := "upload"
			if failure == "persistence" {
				stage = "persist"
			}
			if !logger.contains("state=blocked stage=" + stage + " outcome_unknown=true") {
				t.Fatalf("missing durable failure stage: %v", logger.messages)
			}
			if failure != "persistence" && !logger.contains("slow.pics: collection upload failed stage=") {
				t.Fatalf("upload failure cause was discarded before logging: %v", logger.messages)
			}
			calls := remote.requests
			service = collectionTestService(repo, remote)
			service.logger = logger
			result, err = service.UploadCollection(t.Context(), subject, input, false)
			if result != nil || !errors.Is(err, errSlowpicsOutcomeUnknown) || remote.requests != calls || remote.creates != 1 || repo.releases != 0 {
				t.Fatalf("recreated unknown collection: result=%+v error=%v requests=%d want=%d creates=%d releases=%d", result, err, remote.requests, calls, remote.creates, repo.releases)
			}
		})
	}
}

func TestCollectionServiceSkipAndRejectedPurposesDoNotReserve(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"skip", "final", "menu", "comparison without group", "analysis mismatch", "invalid PNG", "invalid binding", "missing generation", "missing labels", "invalid category", "partial comparison", "mixed comparison groups"} {
		t.Run(variant, func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			skip := false
			switch variant {
			case "skip":
				skip = true
			case "final":
				input.Purpose = api.ScreenshotPurposeFinal
			case "menu":
				input.Purpose = api.ScreenshotPurposeMenu
			case "comparison without group":
				input.Purpose = api.ScreenshotPurposeComparison
				input.Images[0].Purpose = api.ScreenshotPurposeFinal
			case "partial comparison", "mixed comparison groups":
				input.Purpose = api.ScreenshotPurposeComparison
				input.Category = "Comps"
				for i := range input.Images {
					input.Images[i].Purpose = api.ScreenshotPurposeFinal
					input.Images[i].ComparisonGroup = "group"
					input.Images[i].ComparisonCount = len(input.Images)
				}
				if variant == "partial comparison" {
					input.Images[0].ComparisonCount++
				} else {
					input.Images[1].ComparisonGroup = "other"
				}
			case "analysis mismatch":
				input.Images[0].Purpose = api.ScreenshotPurposeAudioAnalysis
			case "invalid PNG":
				if err := os.WriteFile(input.Images[1].Path, []byte("not PNG"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "invalid binding":
				subject.MediaBinding = api.PreparedMediaBinding{}
			case "missing generation":
				input.Generation = ""
			case "missing labels":
				input.Labels = nil
			case "invalid category":
				input.Category = "invalid"
			}
			result, err := service.UploadCollection(t.Context(), subject, input, skip)
			if result != nil || variant != "skip" && err == nil || variant == "skip" && err != nil {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if remote.requests != 0 || repo.reserves != 0 {
				t.Fatalf("requests=%d reserves=%d", remote.requests, repo.reserves)
			}
		})
	}
}

func TestCollectionServiceReleasesOnlyPreCreateFailure(t *testing.T) {
	t.Parallel()
	for _, releaseFails := range []bool{false, true} {
		t.Run(strconv.FormatBool(releaseFails), func(t *testing.T) {
			repo := &collectionTestRepository{}
			if releaseFails {
				repo.releaseErr = errors.New("synthetic release failure")
			}
			remote := &collectionTestRemote{t: t, bootstrapStatus: http.StatusForbidden}
			service, subject, input := collectionTestSetup(t, repo, remote)
			result, err := service.UploadCollection(t.Context(), subject, input, false)
			if result != nil || err == nil || errors.Is(err, errSlowpicsOutcomeUnknown) != releaseFails || repo.releases != 1 || remote.creates != 0 {
				t.Fatalf("result=%+v error=%v releases=%d creates=%d", result, err, repo.releases, remote.creates)
			}
			if !releaseFails {
				remote.bootstrapStatus = 0
				if result, err := service.UploadCollection(t.Context(), subject, input, false); err != nil || result == nil {
					t.Fatalf("safe retry result=%+v error=%v", result, err)
				}
			}
		})
	}
}

func TestCollectionServiceRateLimitExhaustionReleasesReservation(t *testing.T) {
	t.Parallel()
	for _, releaseFails := range []bool{false, true} {
		t.Run(strconv.FormatBool(releaseFails), func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			first, err := service.UploadCollection(t.Context(), subject, input, false)
			if err != nil {
				t.Fatal(err)
			}
			if releaseFails {
				repo.releaseErr = errors.New("synthetic release failure")
			}
			remote.createStatus = http.StatusTooManyRequests
			service.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				response, err := remote.RoundTrip(req)
				if response != nil {
					response.Header.Set("Retry-After", "0")
				}
				return response, err
			})
			secondInput := input
			secondInput.Label = "Second synthetic collection"
			result, err := service.UploadCollection(t.Context(), subject, secondInput, false)
			if result != nil || err == nil || errors.Is(err, errSlowpicsOutcomeUnknown) != releaseFails || repo.releases != 1 || remote.creates != 4 || remote.members != 2 {
				t.Fatalf("result=%+v error=%v releases=%d creates=%d members=%d", result, err, repo.releases, remote.creates, remote.members)
			}
			cached, err := service.UploadCollection(t.Context(), subject, input, false)
			if err != nil || !reflect.DeepEqual(cached, first) || remote.creates != 4 {
				t.Fatalf("lost completed first collection: result=%+v error=%v", cached, err)
			}
			if !releaseFails {
				remote.createStatus = 0
				if result, err := service.UploadCollection(t.Context(), subject, secondInput, false); err != nil || result == nil || remote.creates != 5 || repo.completes != 2 {
					t.Fatalf("safe retry result=%+v error=%v creates=%d completes=%d", result, err, remote.creates, repo.completes)
				}
			}
		})
	}
}

func TestCollectionServiceAcceptsOnlyExplicitComparisonMembers(t *testing.T) {
	t.Parallel()
	repo := &collectionTestRepository{}
	remote := &collectionTestRemote{t: t}
	service, subject, input := collectionTestSetup(t, repo, remote)
	input.Purpose = api.ScreenshotPurposeComparison
	input.Category = "Comps"
	for i := range input.Images {
		input.Images[i].Purpose = api.ScreenshotPurposeFinal
		input.Images[i].ComparisonGroup = "synthetic-group"
		input.Images[i].ComparisonCount = len(input.Images)
	}
	result, err := service.UploadCollection(t.Context(), subject, input, false)
	if err != nil || result == nil || result.Purpose != api.ScreenshotPurposeComparison {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

type collectionTestRepository struct {
	recordingRepo
	mu                            sync.Mutex
	records                       map[string]api.ImageCollection
	reserves, completes, releases int
	reserveHook                   func()
	completeErr, releaseErr       error
}

func (r *collectionTestRepository) LoadImageCollection(_ context.Context, _ api.PreparedMediaBinding, fingerprint string) (*api.ImageCollection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[fingerprint]
	if !ok {
		return nil, nil
	}
	record.Members = slices.Clone(record.Members)
	return &record, nil
}

func (r *collectionTestRepository) ReserveImageCollection(_ context.Context, _ api.PreparedMediaBinding, record api.ImageCollection) (*api.ImageCollection, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reserves++
	if existing, ok := r.records[record.Fingerprint]; ok {
		return &existing, false, nil
	}
	for _, existing := range r.records {
		if !existing.Completed && existing.ContentFingerprint == record.ContentFingerprint {
			return &existing, false, nil
		}
	}
	if r.records == nil {
		r.records = make(map[string]api.ImageCollection)
	}
	record.Members = slices.Clone(record.Members)
	r.records[record.Fingerprint] = record
	if r.reserveHook != nil {
		r.reserveHook()
	}
	return nil, true, nil
}

func (r *collectionTestRepository) CompleteImageCollection(_ context.Context, _ api.PreparedMediaBinding, record api.ImageCollection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completes++
	if r.completeErr != nil {
		return r.completeErr
	}
	record.Members = slices.Clone(record.Members)
	if existing, ok := r.records[record.Fingerprint]; !ok || existing.AttemptID != record.AttemptID || existing.Completed {
		return errors.New("stale attempt")
	}
	r.records[record.Fingerprint] = record
	return nil
}

func (r *collectionTestRepository) ReleaseImageCollection(_ context.Context, _ api.PreparedMediaBinding, record api.ImageCollection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releases++
	if r.releaseErr != nil {
		return r.releaseErr
	}
	if existing, ok := r.records[record.Fingerprint]; !ok || existing.AttemptID != record.AttemptID || existing.Completed {
		return errors.New("stale attempt")
	}
	delete(r.records, record.Fingerprint)
	return nil
}

type collectionTestRemote struct {
	t                                           *testing.T
	requests, creates, members                  int
	bootstrapStatus, createStatus, memberStatus int
	uploaded                                    [][]byte
}

func (r *collectionTestRemote) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests++
	switch req.URL.Path {
	case "/comparison":
		status := r.bootstrapStatus
		if status == 0 {
			status = http.StatusOK
		}
		return slowpicsTestBootstrapResponse(status), nil
	case "/upload/comparison", "/upload/collection":
		r.creates++
		if r.createStatus != 0 {
			return slowpicsTestResponse(r.createStatus, "application/json", "{}"), nil
		}
		fields, _ := slowpicsTestMultipart(r.t, req)
		if req.URL.Path == "/upload/collection" {
			return slowpicsTestCreated(slowpicsTestImageID), nil
		}
		ids := []string{slowpicsTestImageID, slowpicsTestSecondImageID}
		if fields["comparisons[0].images[2].name"] != "" {
			ids = append(ids, "7b3b2639-f8b5-4b7c-a6eb-66ce166575f7", "7b3b2639-f8b5-4b7c-a6eb-66ce166575f8")
		}
		return slowpicsTestCreated(ids...), nil
	default:
		if !strings.HasPrefix(req.URL.Path, "/upload/image/") {
			r.t.Fatalf("unexpected request: %s", req.URL)
		}
		r.members++
		_, data := slowpicsTestMultipart(r.t, req)
		r.uploaded = append(r.uploaded, data)
		status := r.memberStatus
		if status == 0 {
			status = http.StatusNoContent
		}
		response := slowpicsTestResponse(status, "", "")
		response.Header.Set("Retry-After", "0")
		return response, nil
	}
}

func collectionTestService(repo *collectionTestRepository, remote *collectionTestRemote) *Service {
	return &Service{
		cfg:    config.Config{ImageHosting: config.ImageHostingConfig{AnalysisHost: "slowpics"}},
		repo:   repo,
		logger: api.NopLogger{},
		client: &http.Client{Transport: remote},
	}
}

func collectionTestSetup(t *testing.T, repo *collectionTestRepository, remote *collectionTestRemote) (*Service, api.ImageHostingSubject, api.ImageCollectionInput) {
	t.Helper()
	subject := imageHostingTestSubject(filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"))
	input := api.ImageCollectionInput{
		Generation: "generation-one",
		Purpose:    api.ScreenshotPurposeHDRAnalysis,
		Category:   "Plots",
		Label:      "Synthetic Collection",
		Images:     []api.ScreenshotImage{{Path: collectionTestPNG(t, 20), Purpose: api.ScreenshotPurposeHDRAnalysis}, {Path: collectionTestPNG(t, 80), Purpose: api.ScreenshotPurposeHDRAnalysis}},
		Labels:     []string{"Frame one", "Frame two"},
	}
	return collectionTestService(repo, remote), subject, input
}

func collectionTestPNG(t *testing.T, shade uint8) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: shade, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "synthetic.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectionServiceUncertainContentFencedAcrossGenerations(t *testing.T) {
	t.Parallel()
	repo := &collectionTestRepository{}
	remote := &collectionTestRemote{t: t, memberStatus: http.StatusForbidden}
	service, subject, input := collectionTestSetup(t, repo, remote)
	if _, err := service.UploadCollection(t.Context(), subject, input, false); !errors.Is(err, errSlowpicsOutcomeUnknown) {
		t.Fatalf("first outcome: %v", err)
	}
	input.Generation = "new-generation"
	subject.MediaBinding.PreparedGeneration++
	service = collectionTestService(repo, remote)
	if _, err := service.UploadCollection(t.Context(), subject, input, false); !errors.Is(err, errSlowpicsOutcomeUnknown) {
		t.Fatalf("new generation outcome: %v", err)
	}
	if remote.creates != 1 {
		t.Fatalf("recreated uncertain content %d times", remote.creates)
	}
}

func TestCollectionServiceRejectsDifferentReservedSet(t *testing.T) {
	t.Parallel()
	repo := &collectionTestRepository{}
	remote := &collectionTestRemote{t: t}
	service, subject, input := collectionTestSetup(t, repo, remote)
	completed, err := service.UploadCollection(t.Context(), subject, input, false)
	if err != nil {
		t.Fatal(err)
	}
	conflict := *completed
	conflict.Members = slices.Clone(completed.Members)
	conflict.Members[0].Identity = strings.Repeat("f", 64)
	service.repo = &collectionConflictRepository{conflict: conflict}
	requests := remote.requests
	result, err := service.UploadCollection(t.Context(), subject, input, false)
	if result != nil || err == nil || remote.requests != requests {
		t.Fatalf("conflicting receipt result=%+v error=%v requests=%d want=%d", result, err, remote.requests, requests)
	}
}

type collectionConflictRepository struct {
	collectionTestRepository
	conflict api.ImageCollection
}

func (r *collectionConflictRepository) ReserveImageCollection(context.Context, api.PreparedMediaBinding, api.ImageCollection) (*api.ImageCollection, bool, error) {
	return &r.conflict, false, nil
}

func TestCollectionSelectionRejectsDirectOrdinaryUploadPurposes(t *testing.T) {
	t.Parallel()
	for _, purpose := range []api.ScreenshotPurpose{api.ScreenshotPurposeComparison, api.ScreenshotPurposeHDRAnalysis, api.ScreenshotPurposeAudioAnalysis} {
		t.Run(string(purpose), func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			direct := &fakeUploader{result: uploadResult{RawURL: "https://synthetic.invalid/image.png"}}
			service.uploaders = map[string]uploader{"imgbb": direct}
			input.Images[0].Purpose = purpose
			result, err := service.Upload(t.Context(), subject, "imgbb", "global", input.Images[:1])
			if err == nil || !strings.Contains(err.Error(), "require collection hosting") || len(result) != 0 || len(direct.calls) != 0 || remote.requests != 0 {
				t.Fatalf("direct collection purpose escaped: result=%#v error=%v calls=%d", result, err, len(direct.calls))
			}
		})
	}
}

func TestCollectionSkipOmitsUnverifiableLocalMembers(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"missing", "non-PNG"} {
		t.Run(variant, func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			if variant == "missing" {
				if err := os.Remove(input.Images[0].Path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(input.Images[0].Path, []byte("synthetic JPEG bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := service.UploadCollection(t.Context(), subject, input, true)
			if err != nil || result != nil {
				t.Fatalf("cache-only unverified set = %#v %v", result, err)
			}
			if result, err = service.UploadCollection(t.Context(), subject, input, false); err == nil || result != nil {
				t.Fatal("required upload accepted invalid local member")
			}
			input.Purpose, input.Category = api.ScreenshotPurposeComparison, "Comps"
			for index := range input.Images {
				input.Images[index].Purpose = api.ScreenshotPurposeFinal
				input.Images[index].ComparisonGroup = "group"
				input.Images[index].ComparisonCount = len(input.Images) + 1
			}
			if _, err := service.UploadCollection(t.Context(), subject, input, true); err == nil {
				t.Fatal("skip suppressed malformed group authority")
			}
			if remote.requests != 0 || repo.reserves != 0 {
				t.Fatal("cache-only missing members caused remote effects")
			}
		})
	}
}

func TestCollectionSkipPreservesPendingFenceAndReportsCorruption(t *testing.T) {
	t.Parallel()
	repo := &collectionTestRepository{}
	remote := &collectionTestRemote{t: t, createStatus: http.StatusServiceUnavailable}
	service, subject, input := collectionTestSetup(t, repo, remote)
	if _, err := service.UploadCollection(t.Context(), subject, input, false); !errors.Is(err, errSlowpicsOutcomeUnknown) {
		t.Fatalf("expected unknown outcome: %v", err)
	}
	calls := remote.requests
	if result, err := service.UploadCollection(t.Context(), subject, input, true); result != nil || err != nil {
		t.Fatalf("skip incomplete set = %#v %v", result, err)
	}
	if _, err := service.UploadCollection(t.Context(), subject, input, false); !errors.Is(err, errSlowpicsOutcomeUnknown) {
		t.Fatal("skip cleared pending reservation")
	}
	for key, record := range repo.records {
		record.Completed = true
		record.URL = "https://unsafe.invalid/collection"
		repo.records[key] = record
	}
	if _, err := service.UploadCollection(t.Context(), subject, input, true); err == nil {
		t.Fatal("skip suppressed corrupt durable record")
	}
	if remote.requests != calls || repo.reserves != 1 || repo.releases != 0 {
		t.Fatal("skip changed pending authority or caused remote effects")
	}
}

func TestCollectionServiceReleasesMissingBootstrapTokenReservation(t *testing.T) {
	t.Parallel()
	for _, cookie := range []string{"", "XSRF-TOKEN=; Path=/upload; Secure"} {
		t.Run(cookie, func(t *testing.T) {
			repo := &collectionTestRepository{}
			remote := &collectionTestRemote{t: t}
			service, subject, input := collectionTestSetup(t, repo, remote)
			posts := 0
			service.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					posts++
					return slowpicsTestCreated(slowpicsTestImageID, slowpicsTestSecondImageID), nil
				}
				response := slowpicsTestResponse(http.StatusOK, "text/html", "")
				if cookie != "" {
					response.Header.Add("Set-Cookie", cookie)
				}
				return response, nil
			})}
			result, err := service.UploadCollection(t.Context(), subject, input, false)
			if result != nil || err == nil || errors.Is(err, errSlowpicsOutcomeUnknown) || posts != 0 || repo.releases != 1 {
				t.Fatalf("result=%+v error=%v posts=%d releases=%d", result, err, posts, repo.releases)
			}
			service.client = &http.Client{Transport: remote}
			if result, err := service.UploadCollection(t.Context(), subject, input, false); result == nil || err != nil {
				t.Fatalf("safe retry result=%+v error=%v", result, err)
			}
		})
	}
}

func TestCollectionServiceUnknownOutcomeRetainsExactConfirmationAuthority(t *testing.T) {
	t.Parallel()
	repo := &collectionTestRepository{}
	remote := &collectionTestRemote{t: t, createStatus: http.StatusServiceUnavailable}
	service, subject, input := collectionTestSetup(t, repo, remote)
	reporter := &collectionEffectTestReporter{}
	ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
	_, err := service.UploadCollection(ctx, subject, input, false)
	var outcome *api.ImageCollectionOutcomeError
	if !errors.As(err, &outcome) || outcome.AttemptID == "" {
		t.Fatalf("missing exact attempt: %v", err)
	}
	failure, ok := api.AsOperationFailure(err)
	if !ok || failure.Recovery != api.OperationRecoveryConfirm {
		t.Fatalf("confirmation unavailable: %v", err)
	}
	if len(reporter.effects) != 1 || reporter.effects[0].ScopeID != api.ImageCollectionEffectScope(outcome.AttemptID) || reporter.completions != 0 {
		t.Fatalf("effect authority = %#v", reporter)
	}
	calls := remote.requests
	originalAttempt := outcome.AttemptID
	input.Generation = "another-generation"
	_, err = service.UploadCollection(ctx, subject, input, false)
	if !errors.As(err, &outcome) || outcome.AttemptID != originalAttempt || remote.requests != calls || remote.creates != 1 || repo.releases != 0 {
		t.Fatalf("retry changed attempt or made request: %v", err)
	}
	if len(reporter.effects) != 2 || reporter.completions != 1 {
		t.Fatalf("unused competing effect not settled: %#v", reporter)
	}
}

type collectionEffectTestReporter struct {
	effects     []api.WorkflowExternalEffect
	completions int
}

func (r *collectionEffectTestReporter) Begin(_ context.Context, effect api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	r.effects = append(r.effects, effect)
	return api.WorkflowExternalEffectReceipt{EffectID: "effect-" + strconv.Itoa(len(r.effects))}, nil
}
func (r *collectionEffectTestReporter) Complete(_ context.Context, _ api.WorkflowExternalEffectReceipt, succeeded bool) error {
	if succeeded {
		return errors.New("collection completion must be atomic with its reservation")
	}
	r.completions++
	return nil
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package czt implements uploads to CZTeam (CZT) via its dedicated JSON
// endpoint takeupload_api.php.
//
// Unlike most impls in this repo CZTeam is not a UNIT3D site and does not need a
// cookie jar: the user's passkey authenticates the multipart POST. The endpoint
// returns the registered .torrent inline as base64, already personalized with
// the uploader's announce passkey and source=CzT.
package czt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	trackerName               = "CZT"
	descGroup                 = "czt"
	defaultBaseURL            = "https://czteam.me"
	uploadPath                = "/takeupload_api.php"
	uploadTimeout             = 120 * time.Second
	defaultCZTPostCancelGrace = 5 * time.Second
	cztUploadDecodeFieldBytes = 512
)

var cztPostCancelGrace atomic.Int64

func init() {
	cztPostCancelGrace.Store(int64(defaultCZTPostCancelGrace))
}

var (
	newCZTUploadHTTPClient = func() *http.Client {
		return &http.Client{Timeout: uploadTimeout}
	}
	cztBaseURL = defaultBaseURL
)

// uploadResponse mirrors the JSON returned by takeupload_api.php. A 201 carries
// the full set; a 409 duplicate still returns id/name/download_url/torrent_b64.
type uploadResponse struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	InfoHash    string `json:"infohash"`
	DownloadURL string `json:"download_url"`
	TorrentB64  string `json:"torrent_b64"`
	Error       string `json:"error"`
}

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	files         []commonhttp.FileField
	endpoint      string
	baseURL       string
	questionnaire *api.TrackerQuestionnaire
}

func upload(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	if err := ctx.Err(); err != nil {
		return api.UploadSummary{}, fmt.Errorf("context canceled: %w", err)
	}
	req.Intent = trackers.PreparationIntentUpload
	plan, failure := trackers.PrepareAdapter(ctx, req, nil, prepareUpload)
	if failure != nil {
		return api.UploadSummary{}, failure
	}
	summary, err := plan.Submit(ctx)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT submit prepared upload: %w", err)
	}
	return summary, nil
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	state, err := prepareUploadState(ctx, req, req.Intent == trackers.PreparationIntentUpload)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, state.files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
) (api.UploadSummary, error) {
	if err := ctx.Err(); err != nil {
		return api.UploadSummary{}, fmt.Errorf("context canceled: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, state.endpoint, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	// Once the CZT upload POST is sent, cancellation can race with an
	// irreversible remote 201. Give the tracker a short grace to return that
	// result, but do not wait for the full client timeout after caller cancel.
	postCtx, startPost, cancelPost := newCZTPostRequestContext(ctx)
	defer cancelPost()
	client := cztUploadHTTPClientWithStartHook(newCZTUploadHTTPClient(), startPost)
	execution, err := commonhttp.ExecuteUpload(client, httpReq.WithContext(postCtx), commonhttp.UploadExecutionOptions{
		Tracker:       trackerName,
		SuccessStatus: func(status int) bool { return status == http.StatusCreated },
		SuccessBody:   commonhttp.FullSuccessBody,
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT execute upload: %w", err)
	}
	responseBody, responsePreview := execution.Body, execution.Preview
	if execution.StatusCode != http.StatusCreated {
		if err := ctx.Err(); err != nil {
			return api.UploadSummary{}, fmt.Errorf("context canceled: %w", err)
		}
	}

	// Only a fresh 201 with a torrent id is a successful upload. A 409 means the
	// release name already exists; surface it as an error (the response still
	// carries the existing torrent for callers who want to cross-seed).
	if execution.StatusCode != http.StatusCreated {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, trackerName, "upload_failure", responsePreview, ".json")
		return api.UploadSummary{}, commonhttp.UploadHTTPError(trackerName, execution.StatusCode, responsePreview)
	}

	torrentIDValue, err := parseCZTUploadID(responseBody)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT parse upload response id: %w", err)
	}
	if torrentIDValue <= 0 {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, trackerName, "upload_failure", responsePreview, ".json")
		return api.UploadSummary{}, commonhttp.UploadHTTPError(trackerName, execution.StatusCode, responsePreview)
	}

	torrentID := strconv.Itoa(torrentIDValue)
	torrentURL := state.baseURL + "/details.php?id=" + torrentID
	summary := api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
		Tracker:    trackerName,
		TorrentID:  torrentID,
		TorrentURL: torrentURL,
	}}}

	parsed, err := parseCZTUploadResponse(responseBody)
	if err != nil {
		warnCZTLocalUploadFailure(req.Logger, "parse upload response", err)
	}

	if strings.TrimSpace(parsed.DownloadURL) != "" {
		downloadURL, err := joinCZTURL(state.baseURL, parsed.DownloadURL)
		if err != nil {
			warnCZTLocalUploadFailure(req.Logger, "upload response download_url", err)
		} else {
			summary.UploadedTorrents[0].DownloadURL = downloadURL
		}
	}

	if err := ctx.Err(); err != nil {
		warnCZTLocalUploadFailure(req.Logger, "post-success cancellation", err)
		return finalizeCZTUploadSummary(summary), nil
	}

	// The endpoint returns the registered .torrent inline (base64), already
	// personalized with the uploader's announce passkey and source=CzT, so we
	// persist that directly rather than re-deriving an announce URL.
	artifactPath, err := persistReturnedTorrent(req, parsed.TorrentB64)
	if err != nil {
		warnCZTLocalUploadFailure(req.Logger, "persist returned torrent", err)
		return finalizeCZTUploadSummary(summary), nil
	}
	if strings.TrimSpace(artifactPath) != "" {
		summary.UploadedTorrents[0].TorrentPath = artifactPath
	}

	if err := ctx.Err(); err != nil {
		warnCZTLocalUploadFailure(req.Logger, "post-success cancellation", err)
		return finalizeCZTUploadSummary(summary), nil
	}

	return finalizeCZTUploadSummary(summary), nil
}

func warnCZTLocalUploadFailure(logger api.Logger, step string, err error) {
	if logger == nil || err == nil {
		return
	}
	logger.Warnf("trackers: CZT upload completed remotely but local %s failed: %v", step, err)
}

// newCZTPostRequestContext lets an in-flight POST outlive caller cancellation
// for a short grace period so an irreversible 201 response can still be
// accounted for, then cancels the request before the full client timeout.
func newCZTPostRequestContext(ctx context.Context) (context.Context, func() error, context.CancelFunc) {
	postCtx, cancelPost := context.WithCancel(context.WithoutCancel(ctx))
	startPost := func() error { return nil }
	if ctx.Done() == nil {
		return postCtx, startPost, cancelPost
	}

	started := make(chan struct{})
	done := make(chan struct{})
	var startedFlag atomic.Bool
	var startOnce sync.Once
	var cancelOnce sync.Once
	startPost = func() error {
		if startedFlag.Load() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			cancelPost()
			return fmt.Errorf("context canceled: %w", err)
		}
		startOnce.Do(func() {
			startedFlag.Store(true)
			close(started)
		})
		return nil
	}

	waitGrace := func() {
		timer := time.NewTimer(time.Duration(cztPostCancelGrace.Load()))
		defer timer.Stop()
		select {
		case <-timer.C:
			cancelPost()
		case <-done:
		}
	}

	go func() {
		select {
		case <-started:
			select {
			case <-ctx.Done():
				waitGrace()
			case <-done:
			}
		case <-ctx.Done():
			select {
			case <-started:
				waitGrace()
			default:
				cancelPost()
			}
		case <-done:
		}
	}()

	return postCtx, startPost, func() {
		cancelOnce.Do(func() {
			close(done)
			cancelPost()
		})
	}
}

type cztPostStartTransport struct {
	base  http.RoundTripper
	start func() error
}

// RoundTrip records the exact start of the irreversible CZT POST and rejects
// the request if the caller canceled before the transport begins sending it.
func (t cztPostStartTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.start(); err != nil {
		return nil, fmt.Errorf("start CZT upload request: %w", err)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("CZT upload round trip: %w", err)
	}
	return resp, nil
}

func cztUploadHTTPClientWithStartHook(client *http.Client, start func() error) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: uploadTimeout}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out := *client
	out.Transport = cztPostStartTransport{base: base, start: start}
	return &out
}

// finalizeCZTUploadSummary drops uploaded rows that cannot be injected or
// downloaded locally while preserving the accepted remote upload count.
func finalizeCZTUploadSummary(summary api.UploadSummary) api.UploadSummary {
	if len(summary.UploadedTorrents) == 0 {
		return summary
	}
	out := summary.UploadedTorrents[:0]
	for _, uploaded := range summary.UploadedTorrents {
		if strings.TrimSpace(uploaded.TorrentPath) != "" || strings.TrimSpace(uploaded.DownloadURL) != "" {
			out = append(out, uploaded)
		}
	}
	summary.UploadedTorrents = out
	return summary
}

func parseCZTUploadID(body []byte) (int, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return 0, redactCZTUploadDecodeError("decode CZT upload response fields", err)
	}
	var id int
	if err := json.Unmarshal(fields["id"], &id); err != nil {
		return 0, redactCZTUploadDecodeError("decode CZT upload response id", err)
	}
	return id, nil
}

func parseCZTUploadResponse(body []byte) (uploadResponse, error) {
	var parsed uploadResponse
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return parsed, redactCZTUploadDecodeError("decode CZT upload response fields", err)
	}
	if err := json.Unmarshal(fields["id"], &parsed.ID); err != nil {
		return parsed, redactCZTUploadDecodeError("decode CZT upload response id", err)
	}
	var fieldErrs []error
	parseOptionalStringField(fields, "name", &parsed.Name, &fieldErrs)
	parseOptionalStringField(fields, "infohash", &parsed.InfoHash, &fieldErrs)
	parseOptionalStringField(fields, "download_url", &parsed.DownloadURL, &fieldErrs)
	parseOptionalStringField(fields, "torrent_b64", &parsed.TorrentB64, &fieldErrs)
	parseOptionalStringField(fields, "error", &parsed.Error, &fieldErrs)
	return parsed, errors.Join(fieldErrs...)
}

// redactCZTUploadDecodeError preserves parse context while removing any
// secret-bearing fragments that encoding/json may echo from the response body.
func redactCZTUploadDecodeError(context string, err error) error {
	return fmt.Errorf("%s: %s", context, redaction.RedactValue(err.Error(), nil))
}

func parseOptionalStringField(fields map[string]json.RawMessage, name string, dest *string, errs *[]error) {
	raw := fields[name]
	if len(raw) == 0 {
		return
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		preview := string(raw)
		if len(preview) > cztUploadDecodeFieldBytes {
			preview = preview[:cztUploadDecodeFieldBytes] + "..."
		}
		*errs = append(*errs, fmt.Errorf("%s: %s", name, redaction.RedactValue(fmt.Sprintf("value=%s: %s", preview, err.Error()), nil)))
	}
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	fields := maps.Clone(state.fields)
	if _, ok := fields["passkey"]; ok {
		fields["passkey"] = "[redacted]"
	}
	blockedReason := ""
	if missingRequiredCategory(state) {
		blockedReason = "answer the category questionnaire to continue"
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          trackerName,
		BlockedReason:    blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: descGroup,
		Description:      state.description,
		Endpoint:         state.endpoint,
		Payload:          fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, requireCategory bool) (uploadState, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}

	assets := uploadDescriptionAssets(ctx, req)

	// CZTeam stores two description fields separately: `descr` holds the raw
	// MediaInfo/BDInfo dump, and `user_descr` holds the free-form BBCode body
	// (user notes + screenshot images).
	mediaInfo := buildMediaInfo(req)
	userDescr := buildDescription(req, assets)
	releaseName := resolveName(req.Meta)
	baseURL := resolveBaseURL()
	passkey := strings.TrimSpace(req.TrackerConfig.Passkey)
	if passkey == "" {
		return uploadState{}, errors.New("trackers: CZT missing passkey")
	}

	category, err := resolveCategory(req.Meta)
	if err != nil {
		if requireCategory {
			return uploadState{}, err
		}
		category = ""
	}

	fields := map[string]string{
		"name": releaseName,
	}
	if category != "" {
		fields["category"] = category
	}
	if strings.TrimSpace(mediaInfo) != "" {
		fields["descr"] = mediaInfo
	}
	if strings.TrimSpace(userDescr) != "" {
		fields["user_descr"] = userDescr
	}
	if imdb := imdbID(req.Meta); imdb != "" {
		fields["imdb_id"] = imdb
	}
	// resolution/codec/container/source are validated server-side against the
	// tracker's allowed value set; unknown values are dropped, not rejected.
	if res := strings.TrimSpace(req.Meta.Release.Resolution); res != "" {
		fields["resolution"] = res
	}
	if codec := firstCodec(req.Meta); codec != "" {
		fields["codec"] = codec
	}
	if container := strings.TrimSpace(req.Meta.Container); container != "" {
		fields["container"] = container
	}
	if source := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(req.Meta.Source, req.Meta.Release.Source)); source != "" {
		fields["source"] = source
	}
	fields["passkey"] = passkey

	return uploadState{
		torrentPath: torrentPath,
		description: userDescr,
		releaseName: releaseName,
		fields:      fields,
		files: []commonhttp.FileField{{
			FieldName: "file",
			FileName:  releaseName + ".torrent",
			Path:      torrentPath,
		}},
		endpoint:      baseURL + uploadPath,
		baseURL:       baseURL,
		questionnaire: categoryQuestionnaire(req.Meta),
	}, nil
}

// uploadDescriptionAssets uses caller-prepared assets when available, falling
// back to local resolution and an empty asset set on resolution failure.
func uploadDescriptionAssets(_ context.Context, req trackers.PreparationInput) trackers.DescriptionAssets {
	if req.Assets != nil {
		return *req.Assets
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		return trackers.DescriptionAssets{}
	}
	return assets
}

// persistReturnedTorrent decodes the tracker-returned base64 torrent, verifies
// it parses as metainfo, and writes the registered torrent artifact with user
// read/write permissions only. Cleanup errors after replacement return the
// artifact path with a non-nil error so callers can avoid reporting it as
// successfully persisted.
func persistReturnedTorrent(req trackers.PreparationInput, b64 string) (string, error) {
	if strings.TrimSpace(b64) == "" {
		return "", errors.New("empty torrent_b64")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("decode torrent_b64: %w", err)
	}
	if len(decoded) == 0 {
		return "", errors.New("decoded torrent_b64 is empty")
	}
	torrentMeta, err := metainfo.Load(bytes.NewReader(decoded))
	if err != nil {
		return "", fmt.Errorf("load returned torrent: %w", err)
	}
	if _, err := torrentMeta.UnmarshalInfo(); err != nil {
		return "", fmt.Errorf("unmarshal returned torrent info: %w", err)
	}
	path, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, trackerName)
	if err != nil {
		return "", fmt.Errorf("resolve returned torrent path: %w", err)
	}
	if err := writeReturnedTorrent(path, decoded); err != nil {
		var cleanupErr returnedTorrentCleanupError
		if errors.As(err, &cleanupErr) {
			return path, fmt.Errorf("write returned torrent: %w", err)
		}
		return "", fmt.Errorf("write returned torrent: %w", err)
	}
	return path, nil
}

// writeReturnedTorrent stages returned torrent bytes in the destination
// directory before replacing the final artifact path, so a failed write does
// not truncate an existing registered torrent.
func writeReturnedTorrent(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp returned torrent: %w", err)
	}
	tmpPath := tmpFile.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod temp returned torrent: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp returned torrent: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp returned torrent: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp returned torrent: %w", err)
	}
	if err := replaceStagedReturnedTorrent(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

// replaceStagedReturnedTorrent moves a staged torrent into place. Existing
// artifacts are first moved to a temporary backup and restored if the final
// rename fails.
func replaceStagedReturnedTorrent(tmpPath string, outputPath string) error {
	info, err := os.Stat(outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			if renameErr := os.Rename(tmpPath, outputPath); renameErr != nil {
				return fmt.Errorf("rename staged returned torrent into place: %w", renameErr)
			}
			return nil
		}
		return fmt.Errorf("stat returned torrent: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", outputPath)
	}

	backupPath, err := reserveReturnedTorrentBackupPath(filepath.Dir(outputPath), filepath.Base(outputPath)+".backup-*")
	if err != nil {
		return err
	}
	if err := os.Rename(outputPath, backupPath); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("backup existing returned torrent: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		restoreErr := os.Rename(backupPath, outputPath)
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore existing returned torrent: %w", restoreErr))
		}
		return fmt.Errorf("replace existing returned torrent: %w", err)
	}
	if err := removeReturnedTorrentBackup(backupPath); err != nil {
		return returnedTorrentCleanupError{cause: fmt.Errorf("remove returned torrent backup: %w", err)}
	}
	return nil
}

type returnedTorrentCleanupError struct {
	cause error
}

func (e returnedTorrentCleanupError) Error() string {
	return e.cause.Error()
}

func (e returnedTorrentCleanupError) Unwrap() error {
	return e.cause
}

var removeReturnedTorrentBackup = os.Remove

// reserveReturnedTorrentBackupPath reserves and releases a same-directory path
// suitable for temporarily holding the existing registered torrent.
func reserveReturnedTorrentBackupPath(dir string, pattern string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp returned torrent backup marker: %w", err)
	}
	path := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(path)
	if closeErr != nil || removeErr != nil {
		return "", errors.Join(closeErr, removeErr)
	}
	return path, nil
}

// buildMediaInfo returns the raw MediaInfo/BDInfo text for the CZTeam `descr`
// field.
func buildMediaInfo(req trackers.PreparationInput) string {
	return strings.TrimSpace(trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, req.Meta))
}

// buildDescription assembles the CZTeam `user_descr` body: the (possibly
// user-edited) description text followed by a BBCode screenshot block. Kept as a
// separate function so definition.BuildDescription can drive the description
// builder UI with the same output.
func buildDescription(_ trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	// A "final" description is the already-assembled body (saved override or
	// canonical group description) with screenshots embedded; the resolver does
	// not clear assets.Screenshots here, so re-appending would duplicate them.
	// Use it verbatim, matching the assets.Final convention other impls follow.
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	parts := make([]string, 0, 2)
	if body := strings.TrimSpace(assets.Description); body != "" {
		parts = append(parts, body)
	}
	if shots := bbcodeScreenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// bbcodeScreenshotBlock renders at most two raw screenshot image URLs. CZTeam's
// formatter accepts plain [img] tags here; linked/thumbnail URLs are ignored.
func bbcodeScreenshotBlock(images []api.ScreenshotImage) string {
	parts := make([]string, 0, 2)
	for _, image := range images {
		raw := strings.TrimSpace(image.RawURL)
		if raw == "" {
			continue
		}
		parts = append(parts, "[img]"+raw+"[/img]")
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, "\n")
}

// resolveBaseURL returns the fixed CZTeam origin used for upload, details, and
// returned download URLs. CZTeam is not a tracker family/configurable endpoint.
func resolveBaseURL() string {
	if trimmed := strings.TrimRight(strings.TrimSpace(cztBaseURL), "/"); trimmed != "" {
		return trimmed
	}
	return defaultBaseURL
}

// joinCZTURL resolves a tracker-provided download URL against the CZTeam base
// URL, strips URL userinfo, and rejects empty, non-addressable, or
// cross-host results.
func joinCZTURL(baseURL string, rawRef string) (string, error) {
	trimmedRef := strings.TrimSpace(rawRef)
	if trimmedRef == "" {
		return "", errors.New("empty URL")
	}
	base, err := url.Parse(resolveCZTURLBase(baseURL) + "/")
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", errors.New("base URL must be absolute")
	}
	ref, err := url.Parse(trimmedRef)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme == "" || resolved.Host == "" {
		return "", errors.New("resolved URL must be absolute")
	}
	if !strings.EqualFold(resolved.Scheme, base.Scheme) || !strings.EqualFold(resolved.Host, base.Host) {
		return "", errors.New("resolved URL must stay on configured CZT host")
	}
	if !hasUsableCZTDownloadPath(resolved.Path) {
		return "", errors.New("resolved URL has no torrent path")
	}
	resolved.User = nil
	return resolved.String(), nil
}

func hasUsableCZTDownloadPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	return trimmed != "" && trimmed != "/"
}

// resolveCZTURLBase normalizes a base URL before resolving tracker-provided
// relative download URLs against it.
func resolveCZTURLBase(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(trimmed, "/")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.User = nil
	return strings.TrimRight(parsed.String(), "/")
}

func resolveName(meta api.UploadSubject) string {
	if name := strings.TrimSpace(meta.SceneName); name != "" {
		return name
	}
	return strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename))
}

// cztCategory pairs a CZTeam categories.id with its display name.
type cztCategory struct {
	id   string
	name string
}

// cztCategories lists CZTeam upload categories for the upload-time override
// dropdown. upbrr auto-detects only video categories from metadata; everything
// else (software, games, music, XXX, images, docs, …) is chosen here.
var cztCategories = []cztCategory{
	{"1", "XxX"},
	{"4", "Games/PC ISO"},
	{"5", "TvEps/HD"},
	{"6", "Music/Audio"},
	{"7", "TvEps"},
	{"9", "Mobile"},
	{"12", "Games/Consoles"},
	{"19", "Movies/XviD"},
	{"20", "Movies/DVD-R"},
	{"21", "Games/PC Rips"},
	{"22", "Software"},
	{"23", "Anime"},
	{"24", "Images"},
	{"25", "Docs"},
	{"28", "Movies/DVD-RO"},
	{"29", "Movies/HD"},
	{"30", "Music/MVID"},
	{"31", "MAC"},
	{"32", "Sports"},
	{"33", "Movies/HDTV-RO"},
	{"34", "TvEps/HD-RO"},
	{"35", "Music/Lossless"},
	{"36", "Full BluRay-RO"},
	{"37", "Movies/3D"},
	{"38", "Movies-RO"},
}

func categoryNames() []string {
	out := make([]string, 0, len(cztCategories))
	for _, c := range cztCategories {
		out = append(out, c.name)
	}
	return out
}

func categoryIDForName(name string) string {
	name = strings.TrimSpace(name)
	for _, c := range cztCategories {
		if strings.EqualFold(c.name, name) {
			return c.id
		}
	}
	return ""
}

func categoryNameForID(id string) string {
	for _, c := range cztCategories {
		if c.id == id {
			return c.name
		}
	}
	return ""
}

// categoryQuestionnaire offers a (non-blocking) category dropdown pre-filled
// with the auto-detected category, so the user can override it for content
// upbrr can't classify from video metadata.
func categoryQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	auto := autoCategory(meta)
	return &api.TrackerQuestionnaire{
		Tracker: trackerName,
		Fields: []api.TrackerQuestionnaireField{{
			Key:      "category",
			Label:    "Category",
			Kind:     "select",
			Options:  categoryNames(),
			Value:    categoryNameForID(auto),
			Help:     "Auto-detected from video metadata. Override for software, games, music, XXX, etc.",
			Required: auto == "",
		}},
	}
}

// resolveCategory returns the CZTeam category id: an explicit questionnaire
// override when the user picked one, otherwise the auto-detected video category.
func resolveCategory(meta api.UploadSubject) (string, error) {
	if id := resolveQuestionnaireCategory(standalone.QuestionnaireAnswers(meta, trackerName)["category"]); id != "" {
		return id, nil
	}
	if id := autoCategory(meta); id != "" {
		return id, nil
	}
	return "", errors.New("trackers: CZT category requires explicit questionnaire selection for non-video content")
}

func resolveQuestionnaireCategory(value string) string {
	if id := categoryIDForName(value); id != "" {
		return id
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, c := range cztCategories {
		if c.id == trimmed {
			return c.id
		}
	}
	return ""
}

// autoCategory maps prepared metadata to a CZTeam numeric categories.id when
// metadata supports automatic classification. Unknown non-video content returns
// empty so callers can require an explicit questionnaire category instead of
// falling back to a movie bucket.
func autoCategory(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}

	ro := hasRomanianSubs(meta)
	hd := isHD(meta.Release.Resolution)

	switch {
	case meta.Anime:
		return "23" // Anime
	case category == api.CanonicalCategoryTV:
		if hd && ro {
			return "34" // TvEps/HD-RO
		}
		if hd {
			return "5" // TvEps/HD
		}
		return "7" // TvEps (no SD-RO TV category exists)
	}

	// Movies.
	src := strings.ToUpper(metautil.FirstNonEmptyTrimmed(meta.Source, meta.Release.Source))
	isDVD := strings.Contains(src, "DVD") || strings.EqualFold(meta.DiscType, "DVD") || strings.EqualFold(meta.Type, "DVDRIP")
	isFullBluRay := strings.EqualFold(meta.DiscType, "BDMV") ||
		(strings.EqualFold(meta.Type, "REMUX") && strings.Contains(src, "BLURAY"))
	if category != api.CanonicalCategoryMovie {
		return ""
	}

	if ro {
		switch {
		case isFullBluRay:
			return "36" // Full BluRay-RO
		case isDVD:
			return "28" // Movies/DVD-RO
		case hd:
			return "33" // Movies/HDTV-RO
		default:
			return "38" // Movies-RO
		}
	}
	switch {
	case isDVD:
		return "20" // Movies/DVD-R
	case hd:
		return "29" // Movies/HD
	case hasCodec(meta, "XviD"):
		return "19" // Movies/XviD
	default:
		return "29" // default to Movies/HD when movie evidence exists
	}
}

func missingRequiredCategory(state uploadState) bool {
	if state.questionnaire == nil {
		return false
	}
	if strings.TrimSpace(state.fields["category"]) != "" {
		return false
	}
	for _, field := range state.questionnaire.Fields {
		if field.Key == "category" && field.Required {
			return true
		}
	}
	return false
}

func hasRomanianSubs(meta api.UploadSubject) bool {
	for _, s := range meta.SubtitleLanguages {
		v := strings.ToLower(strings.TrimSpace(s))
		if v == "ro" || v == "rum" || v == "ron" || strings.HasPrefix(v, "roman") {
			return true
		}
	}
	return false
}

func isHD(res string) bool {
	res = strings.TrimSpace(res)
	for _, prefix := range []string{"720", "1080", "2160", "4320"} {
		if strings.HasPrefix(res, prefix) {
			return true
		}
	}
	return false
}

func hasCodec(meta api.UploadSubject, want string) bool {
	for _, c := range meta.Release.Codec {
		if strings.EqualFold(strings.TrimSpace(c), want) {
			return true
		}
	}
	return false
}

func firstCodec(meta api.UploadSubject) string {
	for _, c := range meta.Release.Codec {
		if v := strings.TrimSpace(c); v != "" {
			return v
		}
	}
	return ""
}

func imdbID(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
}

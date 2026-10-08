// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

var torrentIDPattern = regexp.MustCompile(`^/torrents/(\d+)(?:/|$)`)

// retryAfterUnit scales Retry-After delays; tests shorten it.
var retryAfterUnit = time.Second

type uploadState struct {
	torrentPath     string
	description     string
	payload         uploadPayload
	screenshotPaths []string
	coverURL        string
	blockedReason   string
	questionnaire   *api.TrackerQuestionnaire
	releaseName     string
}

// preparedSubmission is everything captured during preparation. Submit only
// adds server-issued values: the XSRF token and the stored screenshot paths.
// Preparation never writes to ASC, so dry runs, debug runs and abandoned
// plans leave nothing behind on the site.
type preparedSubmission struct {
	fields       map[string][]string
	files        []commonhttp.FileField
	images       []commonhttp.FileField
	artifactPath string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	cookies, err := loadSessionCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC load cookies: %w", err)
	}
	state, err := prepareUploadState(ctx, req, len(cookies) > 0)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC %s", state.blockedReason)
	}

	client := httpclient.New(httpclient.DefaultTimeout)
	submission, err := captureSubmission(ctx, req, client, state)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		session, err := newSessionClient(client, cookies)
		if err != nil {
			return api.UploadSummary{}, err
		}
		return submitPreparedUpload(submitCtx, req, session, submission)
	}, nil), nil
}

// captureSubmission reads every byte the upload needs so submit replays
// exactly what was previewed.
func captureSubmission(
	ctx context.Context,
	req trackers.PreparationInput,
	client *http.Client,
	state uploadState,
) (preparedSubmission, error) {
	torrentBytes, err := commonhttp.FileBytes(state.torrentPath)
	if err != nil {
		return preparedSubmission{}, fmt.Errorf("trackers: ASC read torrent: %w", err)
	}
	files := []commonhttp.FileField{{
		FieldName: "torrent",
		FileName:  filepath.Base(state.torrentPath),
		Content:   torrentBytes,
	}}
	if state.coverURL != "" {
		cover, err := downloadCover(ctx, client, state.coverURL)
		if err != nil {
			return preparedSubmission{}, err
		}
		files = append(files, cover)
	}
	images, err := loadScreenshotFiles(state.screenshotPaths)
	if err != nil {
		return preparedSubmission{}, err
	}
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "ASC")
	if err != nil {
		return preparedSubmission{}, fmt.Errorf("trackers: %w", err)
	}
	return preparedSubmission{
		fields:       state.payload.multipartFields(nil),
		files:        files,
		images:       images,
		artifactPath: artifactPath,
	}, nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	client *http.Client,
	submission preparedSubmission,
) (api.UploadSummary, error) {
	if err := warmUploadSession(ctx, client); err != nil {
		return api.UploadSummary{}, err
	}
	writer := withoutRedirects(client)
	storedPaths, err := storeImages(ctx, writer, submission.images, req.Logger)
	if err != nil {
		return api.UploadSummary{}, err
	}
	req.Logger.Infof("trackers: ASC screenshots stored tracker=ASC count=%d", len(storedPaths))
	fields := make(map[string][]string, len(submission.fields)+1)
	maps.Copy(fields, submission.fields)
	fields["screenshot_paths[]"] = storedPaths
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(fields, submission.files)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC build payload: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+uploadPath, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	setXHRHeaders(httpReq, writer)
	result, err := commonhttp.ExecuteUpload(writer, httpReq, commonhttp.UploadExecutionOptions{Tracker: "ASC"})
	if err != nil {
		req.Logger.Warnf("trackers: ASC upload request failed tracker=ASC state=failed reason=%s", redaction.RedactValue(err.Error(), nil))
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC upload: %w", err)
	}

	// Laravel answers an accepted upload with a redirect to the new torrent page;
	// reading it from the POST's own response keeps a slow or restricted torrent
	// page from turning a remote success into a reported failure.
	location := redirectPath(result.StatusCode, result.Header.Get("Location"))
	if torrentID := parseUploadID(location); torrentID != "" {
		return completeUpload(ctx, req, client, submission, torrentID), nil
	}
	if strings.HasPrefix(location, "/login") {
		req.Logger.Warnf("trackers: ASC upload rejected tracker=ASC state=failed status=%d reason=session_expired", result.StatusCode)
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", errSessionExpired)
	}
	req.Logger.Warnf(
		"trackers: ASC upload not accepted tracker=ASC state=failed status=%d redirect=%q response=%q",
		result.StatusCode,
		redaction.RedactValue(location, nil),
		responseSummary(result.Preview),
	)

	if _, artifactErr := commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "ASC", "upload_failure", result.Preview, ".html"); artifactErr != nil {
		req.Logger.Warnf("trackers: ASC failure artifact write failed: %v", artifactErr)
	}
	switch {
	case location != "":
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC upload rejected status=%d redirect=%s", result.StatusCode, location)
	case result.Success:
		return api.UploadSummary{}, fmt.Errorf(
			"trackers: ASC upload response did not identify the torrent status=%d; it may have been created, check ASC before retrying",
			result.StatusCode,
		)
	default:
		return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("ASC", result.StatusCode, result.FinalURL, result.Body)
	}
}

func completeUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	client *http.Client,
	submission preparedSubmission,
	torrentID string,
) api.UploadSummary {
	req.Logger.Infof("trackers: ASC upload succeeded tracker=ASC torrent_id=%s", torrentID)
	registeredPath := persistRegisteredTorrent(ctx, req, client, submission, torrentID)
	maybeApprove(ctx, withoutRedirects(client), req, torrentID)
	if req.Runtime.Internal {
		req.Logger.Warnf("trackers: ASC internal flag not applied tracker=ASC reason=moderator_only")
	}
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "ASC",
			TorrentID:   torrentID,
			TorrentURL:  baseURL + torrentPath + torrentID,
			TorrentPath: registeredPath,
		}},
	}
}

// redirectPath returns the path of a redirect response's Location, or "".
func redirectPath(status int, location string) string {
	if status < http.StatusMultipleChoices || status >= http.StatusBadRequest {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil {
		return ""
	}
	return parsed.Path
}

// storeImages uploads each screenshot to `/torrents/screenshots` and returns
// the server paths the upload form references. It stops at the first failure;
// images already stored are not cleaned up.
func storeImages(ctx context.Context, client *http.Client, images []commonhttp.FileField, logger api.Logger) ([]string, error) {
	paths := make([]string, 0, len(images))
	for idx, image := range images {
		path, err := storeImage(ctx, client, image, logger)
		if err != nil {
			return nil, fmt.Errorf("trackers: ASC screenshot %d (%s): %w", idx+1, image.FileName, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// storeImage posts one image, retrying HTTP 429 up to maxScreenshotAttempts
// attempts in total and waiting as waitRetryAfter directs.
func storeImage(ctx context.Context, client *http.Client, image commonhttp.FileField, logger api.Logger) (string, error) {
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(nil, []commonhttp.FileField{image})
	if err != nil {
		return "", fmt.Errorf("build screenshot payload: %w", err)
	}
	for attempt := 1; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+screenshotUploadPath, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("build screenshot request: %w", err)
		}
		httpReq.Header.Set("Content-Type", contentType)
		setXHRHeaders(httpReq, client)
		result, err := commonhttp.ExecuteUpload(client, httpReq, commonhttp.UploadExecutionOptions{Tracker: "ASC"})
		if err != nil {
			return "", fmt.Errorf("screenshot request: %w", err)
		}
		if strings.HasPrefix(redirectPath(result.StatusCode, result.Header.Get("Location")), "/login") {
			return "", errSessionExpired
		}
		if result.StatusCode == http.StatusTooManyRequests {
			if attempt >= maxScreenshotAttempts {
				return "", fmt.Errorf("rate limited after %d attempts", attempt)
			}
			wait := retryAfter(result.Header.Get("Retry-After"))
			logger.Infof("trackers: ASC screenshot rate limited tracker=ASC attempt=%d wait=%s", attempt, wait)
			if err := sleepContext(ctx, wait); err != nil {
				return "", err
			}
			continue
		}
		if !result.Success {
			return "", commonhttp.UploadHTTPError("ASC", result.StatusCode, result.Body)
		}
		var stored struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(result.Body, &stored); err != nil {
			return "", fmt.Errorf("decode screenshot response status=%d: %s", result.StatusCode, redaction.RedactValue(err.Error(), nil))
		}
		if strings.TrimSpace(stored.Path) == "" {
			return "", errors.New("screenshot response did not include a path")
		}
		return strings.TrimSpace(stored.Path), nil
	}
}

// retryAfter converts a Retry-After header (seconds or HTTP date) to a delay.
// Missing or unusable values fall back to defaultRetryAfterSeconds; delays are
// capped at maxRetryAfterSeconds to keep submission bounded.
func retryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	seconds := defaultRetryAfterSeconds
	if parsed, err := strconv.Atoi(header); err == nil && parsed > 0 {
		seconds = parsed
	} else if when, err := http.ParseTime(header); err == nil {
		if until := int(time.Until(when).Seconds()); until > 0 {
			seconds = until
		}
	}
	return time.Duration(min(seconds, maxRetryAfterSeconds)) * retryAfterUnit
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("screenshot retry canceled: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// persistRegisteredTorrent stores the torrent ASC serves for the new upload.
// ASC rewrites the info dictionary (it sets its own source), so the site's
// copy is the only correct registered torrent: when the download fails there
// is no local reconstruction, and the upload is reported without one.
func persistRegisteredTorrent(
	ctx context.Context,
	req trackers.PreparationInput,
	client *http.Client,
	submission preparedSubmission,
	torrentID string,
) string {
	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+torrentPath+torrentID+"/download", nil)
	if err == nil {
		downloadReq.Header.Set("User-Agent", userAgent)
		if err = trackers.DownloadRegisteredTorrent(ctx, client, downloadReq, submission.artifactPath); err == nil {
			return submission.artifactPath
		}
	}
	req.Logger.Warnf(
		"trackers: ASC registered torrent download failed tracker=ASC torrent_id=%s decision=skip_injection err=%s",
		torrentID, commonhttp.RedactErrorDetail(err.Error()),
	)
	trackers.LogRegisteredTorrentUnavailable(req.Logger, "ASC")
	return ""
}

// maybeApprove self-approves the new torrent through the staff moderation
// endpoint when uploader_status is enabled. ASC refuses it (403) for accounts
// without staff rights.
func maybeApprove(ctx context.Context, client *http.Client, req trackers.PreparationInput, torrentID string) {
	if !req.TrackerConfig.UploaderStatus {
		req.Logger.Debugf("trackers: ASC auto approval skipped tracker=ASC reason=uploader_status_disabled")
		return
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, baseURL+"/admin/torrents/"+url.PathEscape(torrentID)+"/approve", nil)
	if err != nil {
		req.Logger.Warnf("trackers: ASC auto approval failed tracker=ASC reason=request_build err=%s", commonhttp.RedactErrorDetail(err.Error()))
		return
	}
	setXHRHeaders(httpReq, client)
	resp, err := client.Do(httpReq)
	if err != nil {
		req.Logger.Warnf("trackers: ASC auto approval failed tracker=ASC reason=request err=%s", commonhttp.RedactErrorDetail(err.Error()))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden:
		req.Logger.Warnf("trackers: ASC auto approval refused tracker=ASC status=403 hint=account_not_staff_disable_uploader_status")
	case resp.StatusCode >= http.StatusBadRequest:
		req.Logger.Warnf("trackers: ASC auto approval failed tracker=ASC status=%d", resp.StatusCode)
	default:
		req.Logger.Infof("trackers: ASC auto approval accepted tracker=ASC torrent_id=%s status=%d", torrentID, resp.StatusCode)
	}
}

// downloadCover fetches the poster during preparation so the multipart body
// sent at submit is exactly the previewed content.
func downloadCover(ctx context.Context, client *http.Client, coverURL string) (commonhttp.FileField, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
	if err != nil {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover download returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC read cover: %w", err)
	}
	if len(data) > maxImageBytes {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover exceeds %d bytes", maxImageBytes)
	}
	contentType := http.DetectContentType(data)
	ext, ok := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
	}[contentType]
	if !ok {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover has unsupported type %s", contentType)
	}
	return commonhttp.FileField{
		FieldName: "cover",
		FileName:  "cover" + ext,
		Content:   data,
	}, nil
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	files := make([]api.TrackerDryRunFile, 0, 2+len(state.screenshotPaths))
	files = append(files,
		api.TrackerDryRunFile{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		},
		api.TrackerDryRunFile{
			Field:   "cover",
			Path:    state.coverURL,
			Present: state.coverURL != "",
		},
	)
	for _, screenshot := range state.screenshotPaths {
		files = append(files, api.TrackerDryRunFile{
			Field:   "screenshot_paths[]",
			Path:    screenshot,
			Present: true,
		})
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "ASC",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "asc",
		Description:      state.description,
		Endpoint:         baseURL + uploadPath,
		Payload:          state.payload.previewFields(),
		Questionnaire:    state.questionnaire,
		Files:            files,
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, hasCookies bool) (uploadState, error) {
	torrentFile, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: ASC release name: %w", err)
	}
	mediaInfo, mediaInfoErr := resolveMediaInfoReport(req.Meta, req.Runtime.DBPath)
	logTorrentContentName(req, torrentFile, mediaInfo)
	description := buildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets, req.Logger)
	state := uploadState{
		torrentPath:     torrentFile,
		description:     description,
		payload:         buildPayload(req.Meta, req.TrackerConfig, description, releaseName, mediaInfo),
		screenshotPaths: selectScreenshots(assets),
		coverURL:        resolveCoverURL(req.Meta),
		releaseName:     releaseName,
		questionnaire:   buildQuestionnaire(req.Meta),
	}
	authReason := ""
	if !hasCookies {
		authReason = missingCookiesReason
	}
	mediaInfoReason := ""
	if mediaInfoErr != nil {
		mediaInfoReason = "MediaInfo report unreadable: " + redaction.RedactValue(mediaInfoErr.Error(), nil)
	}
	state.blockedReason = metautil.FirstNonEmptyTrimmed(
		authReason,
		mediaInfoReason,
		validatePayloadFields(req.Meta, state.payload, len(state.screenshotPaths), state.coverURL),
	)
	return state, nil
}

// parseUploadID extracts the torrent ID from a `/torrents/{id}` path or URL.
func parseUploadID(location string) string {
	parsed, err := url.Parse(location)
	if err != nil {
		return ""
	}
	if matches := torrentIDPattern.FindStringSubmatch(parsed.Path); len(matches) == 2 {
		return matches[1]
	}
	return ""
}

// logTorrentContentName reports the name stored in the ASC torrent and warns
// when it still lacks the audio token the site requires or no longer matches the
// MediaInfo report, the two cases that make the site reject an upload.
func logTorrentContentName(req trackers.PreparationInput, torrentFile string, mediaInfo string) {
	name, err := torrentContentName(torrentFile)
	if err != nil {
		req.Logger.Warnf("trackers: ASC torrent content name unreadable tracker=ASC state=failed reason=%s", redaction.RedactValue(err.Error(), nil))
		return
	}
	req.Logger.Infof("trackers: ASC torrent content name tracker=ASC name=%q", name)
	if _, skip := complianceFileNameWithReason(req.Meta, name); skip.unresolved() {
		req.Logger.Warnf("trackers: ASC torrent name lacks the audio token tracker=ASC state=non_compliant name=%q reason=%s", name, skip)
	}
	if strings.Contains(mediaInfo, "Complete name") && !strings.Contains(mediaInfo, name) && hasVideoExtension(name) {
		req.Logger.Warnf("trackers: ASC MediaInfo file name differs from the torrent tracker=ASC state=mismatch name=%q", name)
	}
}

// hasVideoExtension reports whether name ends in a container extension, i.e. it
// is a single-file torrent whose root name is the file name.
func hasVideoExtension(name string) bool {
	_, ok := fileNameExtensions[strings.ToLower(filepath.Ext(name))]
	return ok
}

// torrentContentName returns the root name stored in the tracker torrent.
func torrentContentName(path string) (string, error) {
	torrentMeta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return "", fmt.Errorf("load torrent: %w", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return "", fmt.Errorf("decode torrent info: %w", err)
	}
	return info.BestName(), nil
}

const responseSummaryLimit = 300

// responseSummary reduces a response preview, already redacted by
// commonhttp.ExecuteUpload, to a short single-line excerpt for the operator log.
// Script and style bodies and markup are dropped, and the excerpt is cut on a
// rune boundary so accented text never becomes invalid UTF-8. The full body
// stays in the upload_failure artifact.
func responseSummary(preview []byte) string {
	text := htmlNoisePattern.ReplaceAllString(string(preview), " ")
	text = strings.Join(strings.Fields(htmlTagPattern.ReplaceAllString(text, " ")), " ")
	if utf8.RuneCountInString(text) <= responseSummaryLimit {
		return text
	}
	return string([]rune(text)[:responseSummaryLimit]) + "..."
}

var (
	// Both patterns also match a construct cut off by the preview limit, so an
	// unclosed script body or half a tag never reaches the log.
	htmlTagPattern   = regexp.MustCompile(`<[^>]*(?:>|$)`)
	htmlNoisePattern = regexp.MustCompile(`(?is)<(?:script|style)\b.*?(?:</(?:script|style)>|$)`)
)

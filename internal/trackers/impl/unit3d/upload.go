// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path" //nolint:depguard // Parses Unit3D URL path IDs, not local filesystem paths.
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

type unit3dUploadResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

// submitUnit3DUpload sends an already serialized multipart payload and maps a
// successful Unit3D response to one upload, with an artifact only when the
// response identifies a download URL.
func submitUnit3DUpload(
	ctx context.Context,
	trackerName string,
	releaseName string,
	apiKey string,
	baseURL string,
	uploadURL string,
	contentType string,
	payload string,
	meta api.UploadSubject,
	dbPath string,
	logger api.Logger,
) (api.UploadSummary, error) {
	logger.Infof("trackers: starting upload to %s for release: %s", trackerName, releaseName)
	logger.Debugf("trackers: %s upload URL: %s", trackerName, uploadURL)

	reqCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, uploadURL, strings.NewReader(payload))
	if err != nil {
		logger.Errorf("trackers: %s failed to create HTTP request: %v", trackerName, err)
		return api.UploadSummary{}, fmt.Errorf("trackers: %s build upload request: %w", trackerName, err)
	}
	trackerdata.SetUnit3DAPIHeaders(httpReq, apiKey)
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	logger.Debugf("trackers: %s sending upload request...", trackerName)
	client := &http.Client{Timeout: 40 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		logger.Errorf("trackers: %s HTTP request failed: %v", trackerName, err)
		return api.UploadSummary{}, fmt.Errorf("trackers: %s upload request: %w", trackerName, err)
	}
	defer resp.Body.Close()

	logger.Debugf("trackers: %s received HTTP %d response", trackerName, resp.StatusCode)

	body, bodyPreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		logger.Errorf("trackers: %s failed to read response body: %v", trackerName, err)
		return api.UploadSummary{}, fmt.Errorf("trackers: %s read response body: %w", trackerName, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err := commonhttp.UploadHTTPError(trackerName, resp.StatusCode, bodyPreview)
		logger.Errorf("trackers: %s upload request failed: %v", trackerName, err)
		if len(bodyPreview) > 0 {
			logger.Tracef("trackers: %s response body: %s", trackerName, redaction.RedactValue(string(bodyPreview), nil))
		}
		return api.UploadSummary{}, err
	}

	var result unit3dUploadResponse
	if err := json.Unmarshal(body, &result); err != nil {
		logger.Errorf("trackers: %s failed to parse response JSON: %v", trackerName, err)
		logger.Tracef("trackers: %s response body: %s", trackerName, redaction.RedactValue(string(bodyPreview), nil))
		return api.UploadSummary{}, fmt.Errorf("trackers: %s response json: %w", trackerName, err)
	}
	if !result.Success {
		message := commonhttp.ExtractHTTPErrorDetail(bodyPreview)
		if message == "" {
			message = commonhttp.RedactErrorDetail(result.Message)
		}
		if message == "" {
			message = "unknown error"
		}
		err := fmt.Errorf("trackers: %s api error: %s", trackerName, message)
		logger.Errorf("trackers: %s upload rejected: %v", trackerName, err)
		return api.UploadSummary{}, err
	}

	artifact, artifactErr := parseUnit3DUploadArtifact(baseURL, result.Data)
	artifact.Tracker = trackerName
	if artifact.TorrentID != "" {
		logger.Infof("trackers: %s upload succeeded - torrent ID: %s", trackerName, artifact.TorrentID)
	} else {
		logger.Infof("trackers: %s upload succeeded", trackerName)
	}

	summary := api.UploadSummary{
		Uploaded:         1,
		UploadedTorrents: []api.UploadedTorrent{artifact},
	}
	if artifactErr != nil || artifact.DownloadURL == "" {
		trackers.LogRegisteredTorrentUnavailable(logger, trackerName)
		return summary, nil
	}
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(meta, dbPath, trackerName)
	if err != nil {
		trackers.LogRegisteredTorrentUnavailable(logger, trackerName)
		return summary, nil
	}
	downloadRequest, err := http.NewRequestWithContext(reqCtx, http.MethodGet, artifact.DownloadURL, nil)
	if err != nil {
		trackers.LogRegisteredTorrentUnavailable(logger, trackerName)
		return summary, nil
	}
	trackerdata.SetUnit3DAPIHeaders(downloadRequest, apiKey)
	downloadRequest.Header.Set("User-Agent", "upbrr")
	downloadClient := unit3DRegisteredTorrentClient(client, baseURL)
	if err := trackers.DownloadRegisteredTorrent(reqCtx, downloadClient, downloadRequest, artifactPath); err != nil {
		trackers.LogRegisteredTorrentUnavailable(logger, trackerName)
		return summary, nil
	}
	summary.UploadedTorrents[0].TorrentPath = artifactPath
	logger.Infof(
		"trackers: registered artifact ready tracker=%s artifact=registered_torrent decision=downloaded",
		trackerName,
	)
	return summary, nil
}

// prepareUnit3DUpload builds one preview for every intent. Upload intent
// serializes its files during preparation and captures the payload, endpoint,
// API key, and logger so submission does not reread mutable prepared inputs.
func prepareUnit3DUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	configuredBaseURL string,
	profiles ...SiteProfile,
) (trackers.PreparedOperation, error) {
	preview, err := buildUploadDryRunUnit3D(ctx, req, configuredBaseURL, profiles...)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if !strings.EqualFold(preview.Status, "ready") {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %s upload blocked: %s", preview.Tracker, preview.Message)
	}

	logger := req.Logger
	if logger == nil {
		logger = api.NopLogger{}
	}
	torrentPath := preparedFilePath(preview.Files, "torrent")
	nfoPath := preparedFilePath(preview.Files, "nfo")
	payload, contentType, err := buildMultipartPayload(preview.Payload, torrentPath, nfoPath, logger)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	baseURL, uploadURL := resolveUnit3DURLs(configuredBaseURL)
	trackerName := preview.Tracker
	releaseName := preview.ReleaseName
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitUnit3DUpload(
			submitCtx,
			trackerName,
			releaseName,
			apiKey,
			baseURL,
			uploadURL,
			contentType,
			payload,
			req.Meta,
			req.Runtime.DBPath,
			logger,
		)
	}, nil), nil
}

func preparedFilePath(files []api.TrackerDryRunFile, field string) string {
	for _, file := range files {
		if file.Field == field && file.Present {
			return file.Path
		}
	}
	return ""
}

func resolveUnit3DURLs(configuredBaseURL string) (string, string) {
	baseURL := strings.TrimSpace(configuredBaseURL)
	return baseURL, strings.TrimRight(baseURL, "/") + "/api/torrents/upload"
}

func parseUnit3DUploadArtifact(baseURL, rawData string) (api.UploadedTorrent, error) {
	artifact := api.UploadedTorrent{}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	data := strings.TrimSpace(rawData)
	if data == "" {
		return artifact, errors.New("unit3d upload response omitted registered torrent authority")
	}

	if isNumericID(data) {
		artifact.TorrentID = data
		artifact.DownloadURL = base + "/torrent/download/" + data
		artifact.TorrentURL = base + "/torrents/" + data
		return artifact, nil
	}

	downloadURL, err := resolveUnit3DRegisteredTorrentURL(baseURL, data)
	if err != nil {
		return artifact, err
	}
	artifact.DownloadURL = downloadURL

	id := extractUnit3DTorrentID(downloadURL)
	artifact.TorrentID = id
	if artifact.TorrentID != "" && base != "" {
		artifact.TorrentURL = base + "/torrents/" + artifact.TorrentID
	}

	return artifact, nil
}

func resolveUnit3DRegisteredTorrentURL(baseURL string, rawURL string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse unit3d base url: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || strings.TrimSpace(base.Host) == "" {
		return "", errors.New("unit3d base url must be an absolute HTTP(S) URL")
	}
	reference, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse unit3d registered torrent url: %w", err)
	}
	resolved := base.ResolveReference(reference)
	if !sameUnit3DOrigin(base, resolved) {
		return "", errors.New("unit3d registered torrent url is off-origin")
	}
	return resolved.String(), nil
}

func unit3DRegisteredTorrentClient(base *http.Client, baseURL string) *http.Client {
	client := *base
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		origin, err := url.Parse(strings.TrimSpace(baseURL))
		if err != nil || !sameUnit3DOrigin(origin, request.URL) {
			return errors.New("unit3d registered torrent redirect is off-origin")
		}
		if len(via) >= 10 {
			return errors.New("unit3d registered torrent stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

func sameUnit3DOrigin(left *url.URL, right *url.URL) bool {
	return left != nil && right != nil &&
		(left.Scheme == "http" || left.Scheme == "https") &&
		strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Host, right.Host) &&
		strings.TrimSpace(left.Host) != ""
}

func extractUnit3DTorrentID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if isNumericID(trimmed) {
		return trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err == nil {
		base := path.Base(parsed.Path)
		if id := extractLeadingNumericToken(base); id != "" {
			return id
		}
		if id := extractLeadingNumericToken(strings.Trim(parsed.Path, "/")); id != "" {
			return id
		}
	}

	if id := extractLeadingNumericToken(path.Base(trimmed)); id != "" {
		return id
	}

	return ""
}

func extractLeadingNumericToken(value string) string {
	token := strings.TrimSpace(value)
	if token == "" {
		return ""
	}
	token = strings.Split(token, "/")[0]
	token = strings.Split(token, ".")[0]
	token = strings.TrimSpace(token)
	if isNumericID(token) {
		return token
	}
	return ""
}

func isNumericID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// buildUploadDryRunUnit3D returns a Unit3D preview entry with the payload,
// files, and endpoint that would be used locally. TV payloads with zero-valued
// canonical season or episode metadata are returned as blocked because the
// payload no longer satisfies upload prerequisites.
func buildUploadDryRunUnit3D(
	ctx context.Context,
	req trackers.PreparationInput,
	configuredBaseURL string,
	profiles ...SiteProfile,
) (api.TrackerDryRunEntry, error) {
	profile := firstSiteProfile(profiles)
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, NewWithProfile(Profile{
		Name: req.Tracker,
		Site: profile,
	}).ReleaseNamePolicy())
	if nameFailure != nil {
		return api.TrackerDryRunEntry{}, nameFailure
	}
	select {
	case <-ctx.Done():
		return api.TrackerDryRunEntry{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	trackerName := strings.ToUpper(strings.TrimSpace(req.Tracker))
	logger := req.Logger
	if logger == nil {
		logger = api.NopLogger{}
	}

	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	if apiKey == "" {
		return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: %s missing api_key", trackerName)
	}
	if !req.Meta.Assessments.EncodeSettingsRequirementSatisfied() {
		return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: %s mediainfo missing required fields", trackerName)
	}

	_, uploadURL := resolveUnit3DURLs(configuredBaseURL)

	originalName := strings.TrimSpace(req.Meta.ReleaseName)
	if originalName == "" {
		originalName = strings.TrimSpace(req.Meta.ReleaseNameNoTag)
	}
	name, err := req.ReviewedUploadName()
	if err != nil {
		return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: %s release name: %w", trackerName, err)
	}
	if name != originalName {
		logger.Infof("trackers: %s dry-run name formatting applied", trackerName)
	}

	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: %w", err)
			}
			trackers.LogDescriptionAssetResolutionFailure(logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description, err = buildUnit3DDescription(
			ctx,
			trackerName,
			req.Meta,
			req.Runtime.DescriptionConfig(),
			req.TrackerConfig,
			logger,
			assets.Description,
			assets.MenuImages,
			assets.Screenshots,
			profile,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return api.TrackerDryRunEntry{}, err
			}
			description = ""
		}
	}
	description = ensureUnit3DDVDVOBDescription(description, req.Meta)

	mediainfo, bdinfo, err := loadUnit3DMedia(req.Meta, req.Runtime.DBPath, logger)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	data, err := buildUnit3DData(req, name, description, mediainfo, bdinfo, profile)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	data["mod_queue_opt_in"] = boolFlag(req.TrackerConfig.ModQ)
	if req.TrackerConfig.Exclusive {
		data["exclusive"] = "1"
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: unit3d prepared upload torrent: %w", err)
	}
	nfoPath := resolveNFOPath(req.Meta, req.Runtime.DBPath)

	files := []api.TrackerDryRunFile{{
		Field:   "torrent",
		Path:    torrentPath,
		Present: strings.TrimSpace(torrentPath) != "",
	}}
	if strings.TrimSpace(nfoPath) != "" {
		files = append(files, api.TrackerDryRunFile{
			Field:   "nfo",
			Path:    nfoPath,
			Present: true,
		})
	}

	message := "dry-run payload generated"
	status := "ready"
	if metadataMessage, err := validateUnit3DTVPayloadMetadata(trackerName, req.Meta, data); err != nil {
		message += "; " + metadataMessage
		status = "blocked"
		if req.Intent == trackers.PreparationIntentUpload {
			logger.Warnf("trackers: %s %s", trackerName, metadataMessage)
		}
	}

	return api.TrackerDryRunEntry{
		Tracker:          trackerName,
		Status:           status,
		Message:          message,
		ReleaseName:      name,
		DescriptionGroup: "unit3d",
		Description:      description,
		Endpoint:         uploadURL,
		Payload:          data,
		Files:            files,
	}, nil
}

func buildMultipartPayload(data map[string]string, torrentPath string, nfoPath string, logger api.Logger) (string, string, error) {
	var builder strings.Builder
	writer := multipart.NewWriter(&builder)

	logger.Tracef("trackers: adding %d form fields to payload", len(data))
	for key, value := range data {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return "", "", fmt.Errorf("trackers: UNIT3D write multipart field %q: %w", key, err)
		}
	}

	logger.Debugf("trackers: attaching torrent file: %s", filepath.Base(torrentPath))
	if err := addFile(writer, "torrent", torrentPath); err != nil {
		_ = writer.Close()
		return "", "", err
	}

	if nfoPath != "" {
		logger.Debugf("trackers: attaching NFO file: %s", filepath.Base(nfoPath))
		if err := addFile(writer, "nfo", nfoPath); err != nil {
			_ = writer.Close()
			return "", "", err
		}
	} else {
		logger.Tracef("trackers: no NFO file found")
	}

	if err := writer.Close(); err != nil {
		return "", "", fmt.Errorf("trackers: UNIT3D close multipart writer: %w", err)
	}

	return builder.String(), writer.FormDataContentType(), nil
}

func addFile(writer *multipart.Writer, field, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("trackers: UNIT3D open multipart file: %w", err)
	}
	defer file.Close()

	part, err := writer.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("trackers: UNIT3D create multipart file %q: %w", field, err)
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return fmt.Errorf("trackers: UNIT3D copy multipart file: %w", err)
	}
	return nil
}

func buildUnit3DData(req trackers.PreparationInput, name, description, mediainfo, bdinfo string, profiles ...SiteProfile) (map[string]string, error) {
	profile := firstSiteProfile(profiles)
	meta := req.Meta
	typeID, err := resolveUnit3DTypeIDForTracker(req.Tracker, meta, profile)
	if err != nil {
		return nil, err
	}
	if _, err := meta.Identity.RequireCategory(); err != nil {
		return nil, fmt.Errorf("trackers: Unit3D category: %w", err)
	}
	category := resolveUnit3DCategory(meta)
	imdbID := providerid.IMDb(meta.Identity.IMDBID).Digits()
	if imdbID == "" {
		imdbID = "0"
	}
	data := map[string]string{
		"name":             name,
		"description":      description,
		"mediainfo":        mediainfo,
		"bdinfo":           bdinfo,
		"category_id":      resolveUnit3DCategoryIDForTracker(req.Tracker, meta, profile),
		"type_id":          typeID,
		"resolution_id":    resolveUnit3DResolutionIDForTracker(req.Tracker, meta, profile),
		"tmdb":             formatOptionalInt(meta.Identity.TMDBID),
		"imdb":             imdbID,
		"mal":              formatOptionalInt(meta.Identity.MALID),
		"igdb":             "0",
		"anonymous":        boolFlag(req.TrackerConfig.Anon),
		"stream":           boolFlag(meta.StreamOptimized != 0),
		"sd":               boolFlag(isSDResolution(resolveResolution(meta))),
		"keywords":         resolveKeywordsForTracker(req.Tracker, meta, profile),
		"personal_release": boolFlag(meta.PersonalRelease),
		"internal":         boolFlag(req.Runtime.Internal),
		"featured":         "0",
		"free":             "0",
		"doubleup":         "0",
		"sticky":           "0",
	}

	if strings.EqualFold(category, "TV") {
		if !shouldIncludeUnit3DTVFields(meta, category) {
			applyUnit3DAdditionalPayload(req, data, profile)
			return data, nil
		}
		data["tvdb"] = formatOptionalInt(resolveTVDBID(meta))
		data["season_number"] = resolveSeason(meta)
		data["episode_number"] = resolveEpisode(meta)
	}

	applyUnit3DAdditionalPayload(req, data, profile)

	return data, nil
}

func applyUnit3DAdditionalPayload(req trackers.PreparationInput, data map[string]string, profiles ...SiteProfile) {
	profile := firstSiteProfile(profiles)
	if profile.ApplyAdditionalPayload == nil {
		return
	}
	profile.ApplyAdditionalPayload(req, data)
}

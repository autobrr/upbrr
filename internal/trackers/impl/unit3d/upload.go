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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

type unit3dUploadResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func uploadUnit3D(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	trackerName := strings.ToUpper(strings.TrimSpace(req.Tracker))
	logger := req.Logger
	if logger == nil {
		logger = api.NopLogger{}
	}

	logger.Infof("trackers: starting upload to %s for release: %s", trackerName, req.Meta.ReleaseName)

	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	if apiKey == "" {
		err := fmt.Errorf("trackers: %s missing api_key", trackerName)
		logger.Errorf("trackers: %s upload aborted: %v", trackerName, err)
		return api.UploadSummary{}, err
	}
	if !req.Meta.ValidMediaInfoSettings {
		err := fmt.Errorf("trackers: %s mediainfo missing required fields", trackerName)
		logger.Errorf("trackers: %s upload aborted: %v", trackerName, err)
		return api.UploadSummary{}, err
	}

	baseURL, uploadURL := resolveUnit3DURLs(trackerName, req.TrackerConfig.URL)
	logger.Debugf("trackers: %s upload URL: %s", trackerName, uploadURL)

	originalName := strings.TrimSpace(req.Meta.ReleaseName)
	if originalName == "" {
		originalName = strings.TrimSpace(req.Meta.ReleaseNameNoTag)
	}
	name := buildUnit3DName(trackerName, req.Meta, req.TrackerConfig)
	if name != originalName {
		logger.Infof("trackers: %s name formatting applied", trackerName)
		logger.Infof("  Original: %s", originalName)
		logger.Infof("  Formatted: %s", name)
	} else {
		logger.Debugf("trackers: %s using original name: %s", trackerName, name)
	}

	var err error
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, logger)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			logger.Warnf("trackers: %s description assets failed: %v", trackerName, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description, err = buildUnit3DDescription(ctx, trackerName, req.Meta, req.AppConfig, req.TrackerConfig, logger, assets.Description, assets.MenuImages, assets.Screenshots)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return api.UploadSummary{}, err
			}
			logger.Warnf("trackers: %s description build failed: %v", trackerName, err)
			description = ""
		}
	}
	description = ensureUnit3DDVDVOBDescription(description, req.Meta)
	mediainfo, bdinfo, err := loadUnit3DMedia(req.Meta, req.AppConfig.MainSettings.DBPath, logger)
	if err != nil {
		logger.Errorf("trackers: %s failed to load media info: %v", trackerName, err)
		return api.UploadSummary{}, err
	}

	data, err := buildUnit3DData(req, name, description, mediainfo, bdinfo)
	if err != nil {
		logger.Errorf("trackers: %s failed to build upload data: %v", trackerName, err)
		return api.UploadSummary{}, err
	}
	if message, err := validateUnit3DTVPayloadMetadata(trackerName, req.Meta, data); err != nil {
		logger.Warnf("trackers: %s %s", trackerName, message)
		return api.UploadSummary{}, err
	}
	category := resolveUnit3DCategory(req.Meta)
	_, hasTVDB := data["tvdb"]
	_, hasSeason := data["season_number"]
	_, hasEpisode := data["episode_number"]
	logger.Debugf(
		"trackers: %s payload mapping category=%s category_id=%s type_id=%s meta_type=%q release_type=%q tvdb=%t season_number=%t episode_number=%t",
		trackerName,
		category,
		data["category_id"],
		data["type_id"],
		strings.TrimSpace(req.Meta.Type),
		strings.TrimSpace(req.Meta.Release.Type),
		hasTVDB,
		hasSeason,
		hasEpisode,
	)
	data["mod_queue_opt_in"] = boolFlag(req.TrackerConfig.ModQ)

	if req.TrackerConfig.Exclusive {
		data["exclusive"] = "1"
		logger.Debugf("trackers: %s marking as exclusive release", trackerName)
	}
	if req.TrackerConfig.Anon {
		logger.Debugf("trackers: %s uploading anonymously", trackerName)
	}
	if req.TrackerConfig.ModQ {
		logger.Debugf("trackers: %s opted into moderation queue", trackerName)
	}

	logger.Tracef("trackers: %s building multipart form payload", trackerName)
	payload, contentType, err := buildMultipartPayload(req, data, logger)
	if err != nil {
		logger.Errorf("trackers: %s failed to build payload: %v", trackerName, err)
		return api.UploadSummary{}, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, uploadURL, payload)
	if err != nil {
		logger.Errorf("trackers: %s failed to create HTTP request: %v", trackerName, err)
		return api.UploadSummary{}, fmt.Errorf("trackers: %s build upload request: %w", trackerName, err)
	}
	setUnit3DAPIHeaders(httpReq, apiKey)
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

	body, bodyPreview, err := commonhttp.ReadUploadResponseBody(resp, resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices, commonhttp.DefaultResponsePreviewBytes)
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

	artifact := parseUnit3DUploadArtifact(baseURL, result.Data)
	artifact.Tracker = trackerName
	if artifact.TorrentID != "" {
		logger.Infof("trackers: %s upload succeeded - torrent ID: %s", trackerName, artifact.TorrentID)
	} else {
		logger.Infof("trackers: %s upload succeeded", trackerName)
	}
	if artifact.DownloadURL != "" {
		logger.Infof("trackers: %s download URL: %s", trackerName, artifact.DownloadURL)
	}
	if artifact.TorrentURL != "" {
		logger.Infof("trackers: %s torrent URL: %s", trackerName, artifact.TorrentURL)
	}

	summary := api.UploadSummary{Uploaded: 1}
	if artifact.DownloadURL != "" {
		summary.UploadedTorrents = append(summary.UploadedTorrents, artifact)
	}

	return summary, nil
}

func defaultUnit3DBaseURL(tracker string) string {
	if baseURL, ok := unit3dmeta.BaseURL(tracker); ok {
		return baseURL
	}
	baseURL, ok := unit3dmeta.BaseURL(unit3dmeta.DefaultTracker())
	if ok {
		return baseURL
	}
	return ""
}

func resolveUnit3DURLs(tracker string, configuredBaseURL string) (string, string) {
	baseURL := strings.TrimSpace(configuredBaseURL)
	if baseURL == "" {
		baseURL = defaultUnit3DBaseURL(tracker)
	}
	return baseURL, strings.TrimRight(baseURL, "/") + "/api/torrents/upload"
}

func parseUnit3DUploadArtifact(baseURL, rawData string) api.UploadedTorrent {
	artifact := api.UploadedTorrent{}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	data := strings.TrimSpace(rawData)
	if data == "" {
		return artifact
	}

	if isNumericID(data) {
		artifact.TorrentID = data
		artifact.DownloadURL = base + "/torrent/download/" + data
		artifact.TorrentURL = base + "/torrents/" + data
		return artifact
	}

	downloadURL := data
	if strings.HasPrefix(downloadURL, "/") && base != "" {
		downloadURL = base + downloadURL
	}
	if strings.HasPrefix(strings.ToLower(downloadURL), "http://") || strings.HasPrefix(strings.ToLower(downloadURL), "https://") {
		artifact.DownloadURL = downloadURL
	} else if base != "" {
		artifact.DownloadURL = base + "/" + strings.TrimLeft(downloadURL, "/")
	}

	id := extractUnit3DTorrentID(downloadURL)
	if id == "" {
		id = extractUnit3DTorrentID(artifact.DownloadURL)
	}
	artifact.TorrentID = id
	if artifact.TorrentID != "" && base != "" {
		artifact.TorrentURL = base + "/torrents/" + artifact.TorrentID
	}

	return artifact
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
func buildUploadDryRunUnit3D(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
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
	if !req.Meta.ValidMediaInfoSettings {
		return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: %s mediainfo missing required fields", trackerName)
	}

	_, uploadURL := resolveUnit3DURLs(trackerName, req.TrackerConfig.URL)

	originalName := strings.TrimSpace(req.Meta.ReleaseName)
	if originalName == "" {
		originalName = strings.TrimSpace(req.Meta.ReleaseNameNoTag)
	}
	name := buildUnit3DName(trackerName, req.Meta, req.TrackerConfig)
	if name != originalName {
		logger.Infof("trackers: %s dry-run name formatting applied", trackerName)
	}

	var err error
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, logger)
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
		description, err = buildUnit3DDescription(ctx, trackerName, req.Meta, req.AppConfig, req.TrackerConfig, logger, assets.Description, assets.MenuImages, assets.Screenshots)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return api.TrackerDryRunEntry{}, err
			}
			description = ""
		}
	}
	description = ensureUnit3DDVDVOBDescription(description, req.Meta)

	mediainfo, bdinfo, err := loadUnit3DMedia(req.Meta, req.AppConfig.MainSettings.DBPath, logger)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	data, err := buildUnit3DData(req, name, description, mediainfo, bdinfo)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	data["mod_queue_opt_in"] = boolFlag(req.TrackerConfig.ModQ)
	if req.TrackerConfig.Exclusive {
		data["exclusive"] = "1"
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath, logger)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	nfoPath := resolveNFOPath(req.Meta, req.AppConfig.MainSettings.DBPath)

	files := []api.TrackerDryRunFile{{
		Field:   "torrent",
		Path:    torrentPath,
		Present: strings.TrimSpace(torrentPath) != "",
	}}
	if strings.TrimSpace(nfoPath) != "" {
		files = append(files, api.TrackerDryRunFile{Field: "nfo", Path: nfoPath, Present: true})
	}

	message := "dry-run payload generated"
	status := "ready"
	if metadataMessage, err := validateUnit3DTVPayloadMetadata(trackerName, req.Meta, data); err != nil {
		message += "; " + metadataMessage
		status = "blocked"
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

func buildMultipartPayload(req trackers.UploadRequest, data map[string]string, logger api.Logger) (*strings.Reader, string, error) {
	var builder strings.Builder
	writer := multipart.NewWriter(&builder)

	logger.Tracef("trackers: adding %d form fields to payload", len(data))
	for key, value := range data {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: UNIT3D write multipart field %q: %w", key, err)
		}
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath, logger)
	if err != nil {
		_ = writer.Close()
		logger.Errorf("trackers: failed to resolve torrent path: %v", err)
		return nil, "", err
	}

	logger.Debugf("trackers: attaching torrent file: %s", filepath.Base(torrentPath))
	if err := addFile(writer, "torrent", torrentPath); err != nil {
		_ = writer.Close()
		return nil, "", err
	}

	if nfoPath := resolveNFOPath(req.Meta, req.AppConfig.MainSettings.DBPath); nfoPath != "" {
		logger.Debugf("trackers: attaching NFO file: %s", filepath.Base(nfoPath))
		if err := addFile(writer, "nfo", nfoPath); err != nil {
			_ = writer.Close()
			return nil, "", err
		}
	} else {
		logger.Tracef("trackers: no NFO file found")
	}

	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: UNIT3D close multipart writer: %w", err)
	}

	return strings.NewReader(builder.String()), writer.FormDataContentType(), nil
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

func ensureUnit3DDVDVOBDescription(description string, meta api.PreparedMetadata) string {
	return descriptionunit3d.AppendDVDVOBMediaInfoBlock(description, meta)
}

func loadUnit3DMedia(meta api.PreparedMetadata, dbPath string, logger api.Logger) (string, string, error) {
	bdinfo := ""
	mediainfo := ""

	if isDiscType(meta.DiscType) {
		logger.Debugf("trackers: loading BDInfo for disc type: %s", meta.DiscType)
		text, err := trackers.ReadBDInfo(dbPath, meta)
		if err != nil {
			logger.Warnf("trackers: unit3d bdinfo read failed: %v", err)
		} else if text != "" {
			logger.Tracef("trackers: loaded BDInfo (%d bytes)", len(text))
		}
		bdinfo = text
	}

	if bdinfo == "" {
		logger.Debugf("trackers: loading MediaInfo from: %s", filepath.Base(meta.MediaInfoTextPath))
		text, err := readTextFile(meta.MediaInfoTextPath)
		if err != nil {
			logger.Errorf("trackers: failed to read MediaInfo: %v", err)
			return "", "", fmt.Errorf("trackers: unit3d mediainfo: %w", err)
		}
		if text == "" {
			err := errors.New("trackers: MediaInfo is empty")
			logger.Errorf("trackers: unit3d mediainfo load failed: %v", err)
			return "", "", err
		}
		logger.Tracef("trackers: loaded MediaInfo (%d bytes)", len(text))
		mediainfo = text
	}

	return mediainfo, bdinfo, nil
}

func readTextFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: UNIT3D read text file: %w", err)
	}
	return string(payload), nil
}

func resolveTorrentPath(meta api.PreparedMetadata, dbPath string, logger api.Logger) (string, error) {
	logger.Tracef("trackers: attempting to resolve torrent file path")
	candidates := []string{
		strings.TrimSpace(meta.TorrentPath),
		strings.TrimSpace(meta.ClientTorrentPath),
		strings.TrimSpace(meta.SourcePath),
	}

	for i, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if strings.EqualFold(filepath.Ext(candidate), ".torrent") {
			logger.Tracef("trackers: checking candidate %d: %s", i+1, filepath.Base(candidate))
			if existsFile(candidate) {
				logger.Debugf("trackers: resolved torrent path: %s", candidate)
				return candidate, nil
			}
		}
	}

	if strings.TrimSpace(dbPath) != "" && strings.TrimSpace(meta.SourcePath) != "" {
		logger.Tracef("trackers: checking temp directory for torrent file")
		tmpRoot, err := db.Subdir(dbPath, "tmp")
		if err == nil {
			if guessed, err := torrentPathFromTemp(tmpRoot, meta); err == nil {
				logger.Tracef("trackers: checking temp path: %s", guessed)
				if existsFile(guessed) {
					logger.Debugf("trackers: resolved torrent path from temp: %s", guessed)
					return guessed, nil
				}
			}
		}
	}

	err := errors.New("trackers: unit3d torrent file not found")
	logger.Errorf("trackers: unit3d torrent resolution failed: %v", err)
	return "", err
}

func torrentPathFromTemp(tmpRoot string, meta api.PreparedMetadata) (string, error) {
	tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, base+".torrent"), nil
}

func resolveNFOPath(meta api.PreparedMetadata, dbPath string) string {
	if path := strings.TrimSpace(meta.SceneNFOPath); path != "" && existsFile(path) {
		return path
	}

	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return ""
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".nfo") {
			return filepath.Join(tmpDir, entry.Name())
		}
	}
	return ""
}

func buildUnit3DData(req trackers.UploadRequest, name, description, mediainfo, bdinfo string) (map[string]string, error) {
	meta := req.Meta
	typeID, err := resolveUnit3DTypeIDForTracker(req.Tracker, meta)
	if err != nil {
		return nil, err
	}
	category := resolveUnit3DCategory(meta)
	data := map[string]string{
		"name":             name,
		"description":      description,
		"mediainfo":        mediainfo,
		"bdinfo":           bdinfo,
		"category_id":      resolveUnit3DCategoryIDForTracker(req.Tracker, meta),
		"type_id":          typeID,
		"resolution_id":    resolveUnit3DResolutionIDForTracker(req.Tracker, meta),
		"tmdb":             formatOptionalInt(meta.ExternalIDs.TMDBID),
		"imdb":             formatOptionalInt(meta.ExternalIDs.IMDBID),
		"mal":              formatOptionalInt(meta.ExternalIDs.MALID),
		"igdb":             "0",
		"anonymous":        boolFlag(req.TrackerConfig.Anon),
		"stream":           boolFlag(meta.StreamOptimized != 0),
		"sd":               boolFlag(isSDResolution(resolveResolution(meta))),
		"keywords":         resolveKeywordsForTracker(req.Tracker, meta),
		"personal_release": boolFlag(meta.PersonalRelease),
		"internal":         boolFlag(trackers.IsInternalGroup(req.AppConfig, req.Tracker, meta)),
		"featured":         "0",
		"free":             "0",
		"doubleup":         "0",
		"sticky":           "0",
	}

	if strings.EqualFold(category, "TV") {
		if !shouldIncludeUnit3DTVFields(meta, category) {
			applyUnit3DAdditionalPayload(req, data)
			return data, nil
		}
		data["tvdb"] = formatOptionalInt(resolveTVDBID(meta))
		data["season_number"] = resolveSeason(meta)
		data["episode_number"] = resolveEpisode(meta)
	}

	applyUnit3DAdditionalPayload(req, data)

	return data, nil
}

func applyUnit3DAdditionalPayload(req trackers.UploadRequest, data map[string]string) {
	profile, ok := unit3DSiteProfileFor(req.Tracker)
	if !ok || profile.applyAdditionalPayload == nil {
		return
	}
	profile.applyAdditionalPayload(req, data)
}

func resolveUnit3DTypeIDForTracker(tracker string, meta api.PreparedMetadata) (string, error) {
	trackerName := strings.ToUpper(strings.TrimSpace(tracker))
	profile, ok := unit3DSiteProfileFor(trackerName)
	if !ok || profile.resolveTypeID == nil {
		return resolveUnit3DTypeID(meta)
	}
	typeID := profile.resolveTypeID(meta)
	if strings.TrimSpace(typeID) == "" || typeID == "0" {
		resolvedType := inferUnit3DType(meta)
		if resolvedType == "" {
			resolvedType = strings.ToUpper(strings.TrimSpace(meta.Type))
		}
		return "", fmt.Errorf("trackers: %s unsupported type value %q", trackerName, resolvedType)
	}
	return typeID, nil
}

func resolveUnit3DResolutionIDForTracker(tracker string, meta api.PreparedMetadata) string {
	profile, ok := unit3DSiteProfileFor(tracker)
	if ok && profile.resolveResolutionID != nil {
		return profile.resolveResolutionID(meta)
	}
	return resolveUnit3DResolutionID(meta)
}

func resolveUnit3DCategoryIDForTracker(tracker string, meta api.PreparedMetadata) string {
	profile, ok := unit3DSiteProfileFor(tracker)
	if ok && profile.resolveCategoryID != nil {
		return profile.resolveCategoryID(meta)
	}
	return resolveUnit3DCategoryID(meta)
}

func resolveKeywords(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveKeywordsForTracker(tracker string, meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(tracker), "ACM") {
		return resolveACMKeywords(meta)
	}
	return resolveKeywords(meta)
}

func setUnit3DAPIHeaders(req *http.Request, apiKey string) {
	if req == nil {
		return
	}
	req.Header.Set("Accept", "application/json")
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func resolveTVDBID(meta api.PreparedMetadata) int {
	if strings.EqualFold(resolveUnit3DCategory(meta), "TV") {
		if meta.ExternalIDs.TVDBID != 0 {
			return meta.ExternalIDs.TVDBID
		}
	}
	return 0
}

// resolveUnit3DCategory resolves the upload category from recognized explicit
// tracker metadata, parsed release metadata, structured episode fields, then
// release-name heuristics.
func resolveUnit3DCategory(meta api.PreparedMetadata) string {
	if category := resolveExplicitUnit3DCategory(meta.ExternalIDs.Category); category != "" {
		return category
	}
	if category := resolveExplicitUnit3DCategory(meta.MediaInfoCategory); category != "" {
		return category
	}
	if category := canonicalUnit3DCategory(meta.Release.Category); category != "" {
		return category
	}
	if meta.HasTVSeasonEpisodeSignal() {
		return "TV"
	}
	if hasSeasonEpisode(meta.ReleaseName) {
		return "TV"
	}
	return "MOVIE"
}

// resolveExplicitUnit3DCategory treats unrecognized explicit values as absent
// so parsed release metadata can still determine the category.
func resolveExplicitUnit3DCategory(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if category := canonicalUnit3DCategory(value); category != "" {
		return category
	}
	return ""
}

func canonicalUnit3DCategory(value string) string {
	switch api.NormalizeCategory(value) {
	case api.CategoryMovie:
		return "MOVIE"
	case api.CategoryTV:
		return "TV"
	case api.CategoryUnknown:
		return ""
	default:
		return ""
	}
}

func resolveUnit3DCategoryID(meta api.PreparedMetadata) string {
	return trackerdata.CategoryID(resolveUnit3DCategory(meta))
}

func resolveUnit3DTypeID(meta api.PreparedMetadata) (string, error) {
	typeValue := inferUnit3DType(meta)
	if value := trackerdata.TypeID(typeValue); value != "" {
		return value, nil
	}
	if typeValue == "" {
		typeValue = strings.ToUpper(strings.TrimSpace(meta.Type))
		if typeValue == "" {
			typeValue = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
		}
	}
	return "", fmt.Errorf("trackers: unit3d unsupported type value %q", typeValue)
}

func inferUnit3DType(meta api.PreparedMetadata) string {
	for _, candidate := range []string{meta.Type, meta.Release.Type} {
		normalized := normalizeUnit3DTypeCandidate(candidate)
		if normalized != "" && !isUnit3DCategoryType(normalized) {
			return normalized
		}
	}

	releaseName := strings.ToUpper(strings.TrimSpace(meta.ReleaseName))
	source := strings.ToUpper(strings.TrimSpace(meta.Source))
	if source == "" {
		source = strings.ToUpper(strings.TrimSpace(meta.Release.Source))
	}

	switch {
	case strings.Contains(releaseName, "REMUX"):
		return "REMUX"
	case strings.Contains(releaseName, "WEB-DL") || strings.Contains(releaseName, "WEBDL"):
		return "WEBDL"
	case strings.Contains(releaseName, "WEBRIP") || strings.Contains(releaseName, "WEB-RIP"):
		return "WEBRIP"
	case strings.Contains(releaseName, "DVDRIP"):
		return "DVDRIP"
	case strings.Contains(releaseName, "HDTV"):
		return "HDTV"
	}

	if isDiscType(meta.DiscType) {
		return "DISC"
	}

	switch {
	case strings.Contains(source, "WEB-DL") || strings.Contains(source, "WEBDL"):
		return "WEBDL"
	case strings.Contains(source, "WEBRIP") || strings.Contains(source, "WEB-RIP"):
		return "WEBRIP"
	case strings.Contains(source, "HDTV") || strings.Contains(source, "UHDTV"):
		return "HDTV"
	case strings.Contains(source, "BLURAY") || strings.Contains(source, "BDRIP"):
		return "ENCODE"
	case strings.Contains(source, "WEB"):
		if strings.TrimSpace(meta.VideoEncode) != "" {
			return "WEBRIP"
		}
		return "WEBDL"
	}

	if strings.TrimSpace(meta.VideoEncode) != "" {
		return "ENCODE"
	}

	return ""
}

func normalizeUnit3DTypeCandidate(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	compact := strings.NewReplacer("-", "", "_", "", " ", "").Replace(upper)

	switch compact {
	case "DISC", "REMUX", "WEBDL", "WEBRIP", "HDTV", "ENCODE", "DVDRIP":
		return compact
	case "MOVIE", "TV", "EPISODE", "SERIES", "SHOW", "FILM", "TVSHOW":
		return compact
	}

	switch {
	case strings.Contains(compact, "WEBDL"):
		return "WEBDL"
	case strings.Contains(compact, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(compact, "DVDRIP"):
		return "DVDRIP"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "REMUX"):
		return "REMUX"
	}

	return ""
}

func isUnit3DCategoryType(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "MOVIE", "TV", "EPISODE", "SERIES", "SHOW", "FILM", "TVSHOW":
		return true
	default:
		return false
	}
}

func shouldIncludeUnit3DTVFields(meta api.PreparedMetadata, category string) bool {
	if !strings.EqualFold(strings.TrimSpace(category), "TV") {
		return false
	}

	if hasExplicitMovieSignal(meta) {
		return false
	}

	if strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "TV") {
		return true
	}
	if meta.HasTVSeasonEpisodeSignal() {
		return true
	}

	releaseType := normalizeUnit3DTypeCandidate(meta.Release.Type)
	if strings.EqualFold(releaseType, "EPISODE") || strings.EqualFold(releaseType, "SERIES") {
		return true
	}

	return hasSeasonEpisode(meta.ReleaseName)
}

func hasExplicitMovieSignal(meta api.PreparedMetadata) bool {
	for _, value := range []string{meta.ExternalIDs.Category, meta.MediaInfoCategory, meta.Type, meta.Release.Type} {
		normalized := normalizeUnit3DTypeCandidate(value)
		if strings.EqualFold(normalized, "MOVIE") || strings.EqualFold(normalized, "FILM") {
			return true
		}
	}
	return false
}

func resolveUnit3DResolutionID(meta api.PreparedMetadata) string {
	resolution := resolveResolution(meta)
	if value := trackerdata.ResolutionID(resolution); value != "" {
		return value
	}
	return "10"
}

func resolveResolution(meta api.PreparedMetadata) string {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution == "" {
		resolution = detectResolution(meta.ReleaseName)
	}
	return resolution
}

func detectResolution(value string) string {
	clean := strings.ToLower(value)
	for _, candidate := range []string{"8640p", "4320p", "2160p", "1440p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i"} {
		if strings.Contains(clean, candidate) {
			return candidate
		}
	}
	return ""
}

// resolveSeason returns the Unit3D payload season value from SeasonInt only.
func resolveSeason(meta api.PreparedMetadata) string {
	if meta.SeasonInt <= 0 {
		return "0"
	}
	return formatOptionalInt(meta.SeasonInt)
}

// resolveEpisode returns the Unit3D payload episode value from EpisodeInt only.
func resolveEpisode(meta api.PreparedMetadata) string {
	if meta.EpisodeInt <= 0 {
		return "0"
	}
	return formatOptionalInt(meta.EpisodeInt)
}

// validateUnit3DTVPayloadMetadata returns the shared Unit3D TV metadata block
// reason used by live upload and dry-run when canonical season or episode data
// is missing from payload fields that would otherwise be submitted as zero.
func validateUnit3DTVPayloadMetadata(trackerName string, meta api.PreparedMetadata, data map[string]string) (string, error) {
	message := unit3DTVPayloadMetadataMessage(meta, data)
	if message == "" {
		return "", nil
	}
	return message, fmt.Errorf("trackers: %s %s", trackerName, message)
}

// unit3DTVPayloadMetadataMessage explains when Unit3D TV fields are present but
// canonical season or episode metadata is missing. Parsed release and manual
// naming values are reported only as ignored signals, and the message includes
// the operator action required by blocked dry-run entries.
func unit3DTVPayloadMetadataMessage(meta api.PreparedMetadata, data map[string]string) string {
	if _, hasSeason := data["season_number"]; !hasSeason {
		return ""
	}
	if _, hasEpisode := data["episode_number"]; !hasEpisode {
		return ""
	}

	missing := make([]string, 0, 2)
	ignored := make([]string, 0, 2)
	if meta.SeasonInt <= 0 {
		missing = append(missing, "season")
		if hasParsedSeasonSignal(meta) {
			ignored = append(ignored, "season")
		}
	}
	if meta.EpisodeInt <= 0 && !meta.TVPack {
		missing = append(missing, "episode")
		if hasParsedEpisodeSignal(meta) {
			ignored = append(ignored, "episode")
		}
	}
	if len(missing) == 0 {
		return ""
	}
	message := "canonical TV " + strings.Join(missing, "/") + " missing; tracker payload uses 0"
	if len(ignored) > 0 {
		message += " and ignores parsed " + strings.Join(ignored, "/") + " fallback"
	}
	message += "; refresh metadata or correct canonical season/episode before upload"
	return message
}

func hasParsedSeasonSignal(meta api.PreparedMetadata) bool {
	if meta.Release.Season > 0 {
		return true
	}
	if meta.ReleaseNameOverrides.Season != nil && parseSeasonEpisodeToken(*meta.ReleaseNameOverrides.Season, "S") > 0 {
		return true
	}
	season, _ := parseSeasonEpisode(meta.ReleaseName)
	return season > 0
}

func hasParsedEpisodeSignal(meta api.PreparedMetadata) bool {
	if meta.Release.Episode > 0 {
		return true
	}
	if meta.ReleaseNameOverrides.Episode != nil && parseSeasonEpisodeToken(*meta.ReleaseNameOverrides.Episode, "E") > 0 {
		return true
	}
	_, episode := parseSeasonEpisode(meta.ReleaseName)
	return episode > 0
}

var seasonEpisodePattern = regexp.MustCompile(`(?i)S(\d{1,2})(?:E(\d{1,2}))?`)

func parseSeasonEpisode(name string) (int, int) {
	matches := seasonEpisodePattern.FindStringSubmatch(name)
	if len(matches) < 2 {
		return 0, 0
	}
	season := atoi(matches[1])
	episode := 0
	if len(matches) > 2 {
		episode = atoi(matches[2])
	}
	return season, episode
}

func hasSeasonEpisode(name string) bool {
	return seasonEpisodePattern.MatchString(name)
}

func isSDResolution(resolution string) bool {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480p", "480i", "576p", "576i":
		return true
	default:
		return false
	}
}

func isDiscType(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "BDMV", "DVD", "HDDVD":
		return true
	default:
		return false
	}
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func existsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func formatOptionalInt(value int) string {
	if value <= 0 {
		return "0"
	}
	return strconv.Itoa(value)
}

func atoi(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	out := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		out = out*10 + int(r-'0')
	}
	return out
}

func parseSeasonEpisodeToken(value string, prefix string) int {
	trimmed := strings.TrimSpace(strings.ToUpper(value))
	if trimmed == "" {
		return 0
	}
	trimmed = strings.TrimPrefix(trimmed, strings.ToUpper(prefix))
	return atoi(trimmed)
}

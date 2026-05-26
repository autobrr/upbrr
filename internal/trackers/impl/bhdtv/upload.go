// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

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

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	uploadURL  = "https://www.bit-hdtv.com/takeupload.php"
	sourceFlag = "BIT-HDTV"
)

type uploadState struct {
	torrentPath  string
	releaseName  string
	screenBlock  string
	inlineDescr  string
	mediaDump    string
	fields       map[string]string
	artifactPath string
}

type uploadResponse struct {
	Status  string `json:"status"`
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		View string `json:"view"`
	} `json:"data"`
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}

	response, responseBody, err := sendUpload(ctx, state)
	if err != nil {
		return api.UploadSummary{}, err
	}
	viewURL := strings.TrimSpace(response.Data.View)
	if viewURL == "" {
		artifactPath, artifactErr := writeFailureArtifact(req, responseBody, "upload_failure")
		if artifactErr != nil && req.Logger != nil {
			req.Logger.Warnf("trackers: BHDTV failure artifact write failed: %v", artifactErr)
		}
		message := firstNonEmpty(strings.TrimSpace(response.Message), strings.TrimSpace(response.Status), "upload response did not include a view URL")
		if artifactPath != "" {
			message += " (" + artifactPath + ")"
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: BHDTV %s", message)
	}

	if strings.TrimSpace(req.TrackerConfig.MyAnnounceURL) != "" {
		artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "BHDTV")
		if err != nil {
			return api.UploadSummary{}, err
		}
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, req.TrackerConfig.MyAnnounceURL, viewURL, sourceFlag); err != nil {
			return api.UploadSummary{}, err
		}
		state.artifactPath = artifactPath
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "BHDTV",
			TorrentURL:  viewURL,
			DownloadURL: viewURL,
			TorrentPath: state.artifactPath,
		}},
	}, nil
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	return api.TrackerDryRunEntry{
		Tracker:          "BHDTV",
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "bhdtv",
		Description:      state.screenBlock,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Files: []api.TrackerDryRunFile{
			{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""},
		},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, ctx.Err()
	default:
	}

	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: BHDTV missing api_key")
	}

	descriptionAssets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		descriptionAssets = trackers.DescriptionAssets{}
	}

	screenBlock := buildDescription(descriptionAssets)

	mediaDump, err := resolveMediaDump(req.Meta)
	if err != nil {
		return uploadState{}, err
	}

	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, err
	}

	fields := map[string]string{
		"api_key":    strings.TrimSpace(req.TrackerConfig.APIKey),
		"name":       resolveUploadName(req.Meta),
		"mediainfo":  mediaDump,
		"cat":        resolveCategoryID(req.Meta),
		"subcat":     resolveSubcategoryID(req.Meta),
		"resolution": resolveResolutionID(req.Meta),
		"sdescr":     " ",
		"descr":      resolveInlineDescription(req.Meta),
		"screen":     screenBlock,
		"url":        resolveReferenceURL(req.Meta),
		"format":     "json",
	}

	return uploadState{
		torrentPath: torrentPath,
		releaseName: fields["name"],
		screenBlock: screenBlock,
		inlineDescr: fields["descr"],
		mediaDump:   mediaDump,
		fields:      fields,
	}, nil
}

func sendUpload(ctx context.Context, state uploadState) (uploadResponse, []byte, error) {
	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return uploadResponse{}, nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHDTV request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHDTV upload request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHDTV read response: %w", err)
	}

	var decoded uploadResponse
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			return uploadResponse{}, responseBody, fmt.Errorf("trackers: BHDTV decode response: %w", err)
		}
	}
	return decoded, responseBody, nil
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", err
		}
	}

	file, err := os.Open(strings.TrimSpace(torrentPath))
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHDTV open torrent file: %w", err)
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", filepath.Base(torrentPath))
	if err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveMediaDump(meta api.PreparedMetadata) (string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		text := readBDInfoNoErr("", meta)
		if text == "" {
			return "", errors.New("trackers: BHDTV missing BD summary")
		}
		return text, nil
	}

	text := firstNonEmpty(strings.TrimSpace(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
	if strings.EqualFold(text, strings.TrimSpace(meta.MediaInfoTextPath)) {
		payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", fmt.Errorf("trackers: BHDTV read mediainfo: %w", err)
		}
		return string(payload), nil
	}
	if strings.TrimSpace(text) != "" {
		return text, nil
	}
	return "", errors.New("trackers: BHDTV missing mediainfo text")
}

func resolveInlineDescription(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "Disc so Check Mediainfo dump "
	}
	text, err := resolveMediaDump(meta)
	if err != nil {
		return ""
	}
	return text
}

func resolveUploadName(meta api.PreparedMetadata) string {
	name := firstNonEmpty(
		strings.TrimSpace(meta.ReleaseName),
		strings.TrimSpace(meta.ReleaseNameNoTag),
		strings.TrimSpace(meta.Filename),
		pathutil.Base(meta.SourcePath),
	)
	replacer := strings.NewReplacer(" ", ".", ":.", ".", ":", ".", "DD+", "DDP")
	normalized := replacer.Replace(name)
	for strings.Contains(normalized, "..") {
		normalized = strings.ReplaceAll(normalized, "..", ".")
	}
	return strings.Trim(normalized, ".")
}

func resolveCategoryID(meta api.PreparedMetadata) string {
	if categoryOf(meta) == "TV" {
		if meta.TVPack {
			return "12"
		}
		return "10"
	}
	return "7"
}

func resolveSubcategoryID(meta api.PreparedMetadata) string {
	if categoryOf(meta) != "TV" {
		return resolveMovieSubcategory(meta)
	}
	if meta.TVPack {
		return resolveTVPackSubcategory(meta.Type)
	}
	return resolveTVEpisodeSubcategory(meta.Type)
}

func resolveMovieSubcategory(meta api.PreparedMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "DISC":
		if meta.Is3D != "" {
			return "46"
		}
		return "2"
	case "REMUX":
		switch {
		case strings.Contains(strings.ToUpper(firstNonEmpty(meta.ReleaseName, meta.ReleaseNameNoTag)), "265"):
			return "48"
		case meta.Is3D != "":
			return "45"
		default:
			return "2"
		}
	case "HDTV":
		return "6"
	case "ENCODE":
		switch {
		case strings.Contains(strings.ToUpper(firstNonEmpty(meta.ReleaseName, meta.ReleaseNameNoTag)), "265"):
			return "43"
		case meta.Is3D != "":
			return "44"
		default:
			return "1"
		}
	case "WEBDL", "WEBRIP":
		return "5"
	default:
		return "0"
	}
}

func resolveTVEpisodeSubcategory(typeValue string) string {
	switch strings.ToUpper(strings.TrimSpace(typeValue)) {
	case "HDTV":
		return "7"
	case "WEBDL", "WEBRIP":
		return "8"
	case "ENCODE":
		return "10"
	case "REMUX":
		return "11"
	case "DISC":
		return "12"
	default:
		return "0"
	}
}

func resolveTVPackSubcategory(typeValue string) string {
	switch strings.ToUpper(strings.TrimSpace(typeValue)) {
	case "HDTV":
		return "13"
	case "WEBDL":
		return "14"
	case "WEBRIP":
		return "8"
	case "ENCODE":
		return "16"
	case "REMUX":
		return "17"
	case "DISC":
		return "18"
	default:
		return "0"
	}
}

func resolveResolutionID(meta api.PreparedMetadata) string {
	switch normalizeResolution(firstNonEmpty(meta.Release.Resolution, meta.ReleaseName, meta.Filename)) {
	case "2160P":
		return "4"
	case "1080P":
		return "3"
	case "1080I":
		return "2"
	case "720P":
		return "1"
	default:
		return "10"
	}
}

func resolveReferenceURL(meta api.PreparedMetadata) string {
	if categoryOf(meta) == "TV" && meta.ExternalIDs.TVmazeID != 0 {
		return fmt.Sprintf("https://www.tvmaze.com/shows/%d", meta.ExternalIDs.TVmazeID)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbURL)
	}
	return ""
}

func categoryOf(meta api.PreparedMetadata) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "TV"):
		return "TV"
	case strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "TV"):
		return "TV"
	default:
		return "MOVIE"
	}
}

func buildDescription(assets trackers.DescriptionAssets) string {
	base := strings.ReplaceAll(strings.TrimSpace(assets.Description), "[img=250]", "[img=250x250]")
	parts := make([]string, 0, 1+len(assets.Screenshots))
	if base != "" {
		parts = append(parts, base)
	}
	for _, image := range assets.Screenshots {
		webURL := strings.TrimSpace(firstNonEmpty(image.WebURL, image.RawURL))
		imgURL := strings.TrimSpace(firstNonEmpty(image.RawURL, image.ImgURL, image.WebURL))
		if webURL == "" || imgURL == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[url=%s][img]%s[/img][/url]", webURL, imgURL))
	}
	return strings.Join(parts, " ")
}

func writeFailureArtifact(req trackers.UploadRequest, payload []byte, name string) (string, error) {
	if strings.TrimSpace(req.AppConfig.MainSettings.DBPath) == "" || strings.TrimSpace(req.Meta.SourcePath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(req.AppConfig.MainSettings.DBPath, "tmp")
	if err != nil {
		return "", err
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, req.Meta, req.Meta.SourcePath)
	if err != nil {
		return "", err
	}
	ext := ".txt"
	if bytes.Contains(bytes.ToLower(payload), []byte("<html")) {
		ext = ".html"
	}
	path := filepath.Join(tmpDir, "[BHDTV]"+name+ext)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("trackers: BHDTV create failure artifact dir: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", fmt.Errorf("trackers: BHDTV write failure artifact: %w", err)
	}
	return path, nil
}

func readBDInfoNoErr(_ string, meta api.PreparedMetadata) string {
	if summary, ok := meta.BDInfo["summary"].(string); ok {
		return strings.TrimSpace(summary)
	}
	if infoPath := guessBDInfoPath(meta); infoPath != "" {
		payload, err := os.ReadFile(infoPath)
		if err == nil {
			return strings.TrimSpace(string(payload))
		}
	}
	return ""
}

func guessBDInfoPath(meta api.PreparedMetadata) string {
	if len(meta.SelectedBDMVPlaylists) == 0 {
		return ""
	}
	base := strings.TrimSpace(meta.MediaInfoTextPath)
	if base != "" {
		return filepath.Join(filepath.Dir(base), paths.BDMVSummaryFilename(paths.PrimaryBDMVPlaylist(meta)))
	}
	return ""
}

func normalizeResolution(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	for _, candidate := range []string{"2160P", "1080P", "1080I", "720P"} {
		if strings.Contains(upper, candidate) {
			return candidate
		}
	}
	return upper
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneFields(fields map[string]string) map[string]string {
	cloned := make(map[string]string, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

type uploadState struct {
	torrentPath   string
	description   string
	fields        map[string]string
	blockedReason string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, client, err := prepareUploadState(ctx, req, req.Intent != trackers.PreparationIntentUpload)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: AR %s", state.blockedReason)
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "AR")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, client, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	client *http.Client,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, arUploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: AR request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", arUserAgent)

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: AR upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	bodyBytes, responsePreview, err := commonhttp.ReadUploadResponseBody(resp, resp.StatusCode == http.StatusOK, commonhttp.DefaultResponsePreviewBytes)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: AR read upload response: %w", err)
	}
	groupID, torrentID := parseUploadIDs(finalURL, string(bodyBytes))
	if resp.StatusCode == http.StatusOK && groupID != "" {
		torrentURL := buildTorrentURL(groupID, torrentID)
		downloadURL := buildDownloadURL(torrentID, torrentURL)
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "AR", state.torrentPath, artifactPath, announceURL, arSourceFlag,
		)
		id := metautil.FirstNonEmptyTrimmed(torrentID, groupID)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "AR",
				TorrentID:   id,
				DownloadURL: downloadURL,
				TorrentURL:  torrentURL,
				TorrentPath: registeredPath,
			}},
		}, nil
	}

	failurePath := ""
	if pathValue, pathErr := resolveFailurePath(req.Meta, req.Runtime.DBPath); pathErr == nil {
		failurePath = pathValue
		redactedBody := []byte(redaction.RedactValue(string(responsePreview), nil))
		_ = os.WriteFile(failurePath, redactedBody, 0o600)
	}
	if failurePath != "" {
		return api.UploadSummary{}, fmt.Errorf(
			"%w failure=%s",
			commonhttp.UploadHTTPErrorWithURL("AR", resp.StatusCode, finalURL, responsePreview),
			failurePath,
		)
	}
	return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("AR", resp.StatusCode, finalURL, responsePreview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	fields := maps.Clone(state.fields)
	if _, ok := fields["auth"]; ok {
		fields["auth"] = "[redacted]"
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "AR",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.fields["title"],
		DescriptionGroup: "ar",
		Description:      state.description,
		Endpoint:         arUploadURL,
		Payload:          fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, dryRun bool) (uploadState, *http.Client, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, nil, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	var assets trackers.DescriptionAssets
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	description := buildDescription(req.Meta, req.Runtime.DBPath, assets)
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: AR release name: %w", err)
	}

	fields := map[string]string{
		"submit": "true",
		"type":   resolveTypeID(req.Meta),
		"title":  releaseName,
		"tags":   resolveTags(req.Meta),
		"image":  resolvePoster(req.Meta),
		"desc":   description,
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		fields:      fields,
	}
	if strings.TrimSpace(fields["image"]) == "" {
		state.blockedReason = "missing poster URL"
	}

	if dryRun {
		if authProblem := dryRunAuthProblem(ctx, req.TrackerConfig, req.Runtime.DBPath); authProblem != "" && state.blockedReason == "" {
			state.blockedReason = authProblem
		}
		fields["auth"] = "dry-run-auth"
		return state, nil, nil
	}

	client, authKey, err := resolveSession(ctx, req.TrackerConfig, req.Runtime.DBPath, req.Logger)
	if err != nil {
		return uploadState{}, nil, err
	}
	fields["auth"] = authKey
	return state, client, nil
}

func buildDatabaseLinks(meta api.UploadSubject) string {
	links := make([]string, 0, 5)
	if imdb := resolveIMDbURL(meta); imdb != "" {
		links = append(links, imdb)
	}
	category, categoryErr := meta.Identity.RequireCategory()
	if id := meta.Identity.TMDBID; id > 0 && categoryErr == nil {
		links = append(links, fmt.Sprintf("https://www.themoviedb.org/%s/%d", category, id))
	}
	if isTVDBCategory(meta) && meta.Identity.TVDBID > 0 {
		id := meta.Identity.TVDBID
		links = append(links, fmt.Sprintf("https://www.thetvdb.com/?id=%d&tab=series", id))
	}
	if id := meta.Identity.TVmazeID; id > 0 {
		links = append(links, fmt.Sprintf("https://www.tvmaze.com/shows/%d", id))
	}
	if meta.Identity.MALID > 0 {
		links = append(links, fmt.Sprintf("https://myanimelist.net/anime/%d", meta.Identity.MALID))
	}
	return strings.Join(links, "\n")
}

func cleanNotes(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	replacer := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	trimmed = replacer.Replace(trimmed)
	sceneBlocks := []*regexp.Regexp{
		regexp.MustCompile(`(?is)\[center\]\[spoiler=Scene NFO:\].*?\[/center\]`),
		regexp.MustCompile(`(?is)\[center\]\[spoiler=FraMeSToR NFO:\].*?\[/center\]`),
		regexp.MustCompile(`(?is)\[color=red\]\[size=4\]Screenshots\[/size\]\[/color\]\s*\[align=center\].*?\[/align\]`),
		regexp.MustCompile(`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`),
	}
	for _, pattern := range sceneBlocks {
		trimmed = pattern.ReplaceAllString(trimmed, "")
	}
	return strings.TrimSpace(trimmed)
}

func resolveKeywords(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveYouTube(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveIMDbURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL)
	}
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).URL()
	}
	return ""
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	file, err := os.Open(strings.TrimSpace(torrentPath))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: AR open torrent file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", fmt.Errorf("trackers: AR write multipart field %q: %w", key, err)
		}
	}
	part, err := writer.CreateFormFile("file_input", filepath.Base(torrentPath))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: AR create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", fmt.Errorf("trackers: AR copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: AR close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func parseUploadIDs(finalURL string, body string) (string, string) {
	if matches := arURLPattern.FindStringSubmatch(finalURL); len(matches) >= 2 {
		return strings.TrimSpace(matches[1]), strings.TrimSpace(matchAt(matches, 2))
	}
	if matches := arURLPattern.FindStringSubmatch(body); len(matches) >= 2 {
		return strings.TrimSpace(matches[1]), strings.TrimSpace(matchAt(matches, 2))
	}
	torrentID := ""
	if matches := arDownloadPattern.FindStringSubmatch(body); len(matches) == 2 {
		torrentID = strings.TrimSpace(matches[1])
	}
	return "", torrentID
}

func buildTorrentURL(groupID string, torrentID string) string {
	if groupID == "" {
		return ""
	}
	if torrentID != "" {
		return arBaseURL + "/torrents.php?id=" + url.QueryEscape(groupID) + "&torrentid=" + url.QueryEscape(torrentID)
	}
	return arBaseURL + "/torrents.php?id=" + url.QueryEscape(groupID)
}

func buildDownloadURL(torrentID string, fallback string) string {
	if torrentID == "" {
		return fallback
	}
	return arBaseURL + "/torrents.php?action=download&id=" + url.QueryEscape(torrentID)
}

func resolveFailurePath(meta api.UploadSubject, dbPath string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, "[AR]upload_failure.html"), nil
}

func readTextFileNoErr(path string) string {
	payload, err := readTextFile(path)
	if err != nil {
		return ""
	}
	return payload
}

func collapseDots(value string) string {
	cleaned := strings.TrimSpace(value)
	for strings.Contains(cleaned, "..") {
		cleaned = strings.ReplaceAll(cleaned, "..", ".")
	}
	return strings.Trim(cleaned, ".")
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func matchAt(values []string, idx int) string {
	if idx < len(values) {
		return values[idx]
	}
	return ""
}

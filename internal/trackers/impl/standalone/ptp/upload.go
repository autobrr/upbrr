// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

type uploadState struct {
	baseURL     string
	uploadURL   string
	announceURL string
	client      *http.Client
	groupID     string
	torrentPath string
	description string
	releaseName string
	fields      map[string]string
}

func uploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (api.UploadSummary, error) {
	req.Intent = trackers.PreparationIntentUpload
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, trackers.CanonicalReleaseNamePolicy())
	if nameFailure != nil {
		return api.UploadSummary{}, nameFailure
	}
	plan, failure := trackers.PrepareAdapter(ctx, req, nil, func(ctx context.Context, input trackers.PreparationInput) (trackers.PreparedOperation, error) {
		return prepareUploadAt(ctx, input, baseURL)
	})
	if failure != nil {
		return api.UploadSummary{}, failure
	}
	summary, err := plan.Submit(ctx)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP submit prepared upload: %w", err)
	}
	return summary, nil
}

func prepareUploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, err := prepareUploadStateAt(ctx, req, req.Intent != trackers.PreparationIntentUpload, baseURL)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state, req.Meta)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath, "file_input")
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	trackerTorrentPath, err := resolveTrackerTorrentPath(req.Meta, req.Runtime.DBPath, "PTP")
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType, trackerTorrentPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
	trackerTorrentPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", ptpUserAgent)

	resp, err := state.client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	_, responsePreview, err := commonhttp.ReadUploadResponseBody(resp, resp.StatusCode == http.StatusOK, commonhttp.DefaultResponsePreviewBytes)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP read upload response: %w", err)
	}
	if matches := ptpSuccessPattern.FindStringSubmatch(finalURL); len(matches) == 3 {
		groupID := strings.TrimSpace(matches[1])
		torrentID := strings.TrimSpace(matches[2])
		torrentURL := strings.TrimRight(state.baseURL, "/") + "/torrents.php?id=" + url.QueryEscape(groupID) + "&torrentid=" + url.QueryEscape(torrentID)
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "PTP", state.torrentPath, trackerTorrentPath, state.announceURL, torrentURL, "PTP",
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "PTP",
				TorrentID:   torrentID,
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
	errText := commonhttp.RedactErrorDetail(extractAlertError(string(responsePreview)))
	if errText == "" {
		errText = commonhttp.ExtractHTTPErrorDetail(responsePreview)
	}
	if errText == "" {
		errText = "upload failed"
	}
	if failurePath != "" {
		return api.UploadSummary{}, fmt.Errorf(
			"trackers: PTP upload failed status=%d url=%s error=%s failure=%s",
			resp.StatusCode,
			commonhttp.RedactErrorDetail(finalURL),
			compactError(errText),
			failurePath,
		)
	}
	return api.UploadSummary{}, fmt.Errorf(
		"trackers: PTP upload failed status=%d url=%s error=%s",
		resp.StatusCode,
		commonhttp.RedactErrorDetail(finalURL),
		compactError(errText),
	)
}

func buildUploadPreview(state uploadState, meta api.UploadSubject) api.TrackerDryRunEntry {
	message := "dry-run payload generated"
	if state.groupID != "" {
		message += " for existing group"
	} else {
		message += " for new group"
	}
	fields := maps.Clone(state.fields)
	if _, ok := fields["AntiCsrfToken"]; ok {
		fields["AntiCsrfToken"] = "[redacted]"
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "PTP",
		ReadyMessage:     message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "ptp",
		Description:      state.description,
		Endpoint:         state.uploadURL,
		Payload:          fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
		Questionnaire: buildQuestionnaire(meta, state.groupID),
	})
}

func prepareUploadStateAt(ctx context.Context, req trackers.PreparationInput, dryRun bool, baseURL string) (uploadState, error) {
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, trackers.CanonicalReleaseNamePolicy())
	if nameFailure != nil {
		return uploadState{}, nameFailure
	}
	announceURL := normalizedAnnounceURL(req.TrackerConfig.AnnounceURL)
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: PTP resolve upload torrent: %w", err)
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: PTP reviewed upload name: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req.Meta, req.TrackerConfig, req.Runtime.DescriptionConfig(), assets)
	groupID, err := lookupGroupID(ctx, baseURL, req.TrackerConfig, req.Meta)
	if err != nil {
		return uploadState{}, err
	}
	answers := standalone.QuestionnaireAnswers(req.Meta, "PTP")
	poster := metautil.FirstNonEmptyTrimmed(answers["poster"], resolvePoster(req.Meta))
	if !dryRun {
		poster = rehostPosterToSelectedHost(ctx, req, poster)
	}
	fields, err := buildUploadFields(req.Meta, description, groupID, answers, poster)
	if err != nil {
		return uploadState{}, err
	}
	fields["AntiCsrfToken"] = "dry-run-token"

	var client *http.Client
	if !dryRun {
		client, fields["AntiCsrfToken"], err = resolveSession(ctx, req.TrackerConfig, req.Runtime.DBPath, baseURL, req.Logger)
		if err != nil {
			return uploadState{}, err
		}
	}

	return uploadState{
		baseURL:     baseURL,
		uploadURL:   baseURL + ptpUploadPath,
		announceURL: announceURL,
		client:      client,
		groupID:     groupID,
		torrentPath: torrentPath,
		description: description,
		releaseName: releaseName,
		fields:      fields,
	}, nil
}

func requiresMinimumTwoScreens(meta api.UploadSubject) bool {
	if len(meta.FileList) > 1 {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") || strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV")
}

func lookupGroupID(ctx context.Context, baseURL string, trackerConfig config.TrackerConfig, meta api.UploadSubject) (string, error) {
	apiUser := strings.TrimSpace(trackerConfig.PTPAPIUser)
	apiKey := strings.TrimSpace(trackerConfig.PTPAPIKey)
	if apiUser == "" || apiKey == "" || meta.Identity.IMDBID == 0 {
		return "", nil
	}
	headers := map[string]string{
		"ApiUser":    apiUser,
		"ApiKey":     apiKey,
		"User-Agent": ptpUserAgent,
	}
	values := url.Values{}
	values.Set("imdb", fmt.Sprintf("tt%07d", meta.Identity.IMDBID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+ptpTorrentPath+"?"+values.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP build group lookup request: %w", err)
	}
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil
	}
	if movies, ok := payload["Movies"].([]any); ok && len(movies) > 0 {
		if movie, ok := movies[0].(map[string]any); ok {
			if groupID := stringFromAny(movie["GroupId"]); groupID != "" {
				return groupID, nil
			}
		}
	}
	return stringFromAny(payload["GroupId"]), nil
}

func ipInPrefixes(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func buildUploadFields(meta api.UploadSubject, description string, groupID string, answers map[string]string, poster string) (map[string]string, error) {
	resolution, otherResolution := resolveResolution(meta)
	fields := map[string]string{
		"submit":          "true",
		"remaster_year":   "",
		"remaster_title":  resolveRemasterTitle(meta),
		"type":            resolveType(meta),
		"codec":           "Other",
		"other_codec":     resolveCodec(meta),
		"container":       "Other",
		"other_container": resolveContainer(meta),
		"resolution":      resolution,
		"source":          "Other",
		"other_source":    resolveSource(meta.Source),
		"release_desc":    description,
		"nfo_text":        "",
		"subtitles[]":     joinInts(resolveSubtitles(meta)),
		"trumpable[]":     "",
	}
	if resolution == "Other" && otherResolution != "" {
		fields["other_resolution"] = otherResolution
	}
	if fields["remaster_title"] != "" {
		fields["remaster"] = "on"
	}
	if meta.Scene {
		fields["scene"] = "on"
	}
	if meta.PersonalRelease {
		fields["internalrip"] = "on"
	}
	if meta.Identity.IMDBID == 0 {
		fields["imdb"] = "0"
	} else {
		fields["imdb"] = fmt.Sprintf("%07d", meta.Identity.IMDBID)
	}
	if groupID != "" {
		fields["groupid"] = groupID
		return fields, nil
	}

	title, year := resolveGroupTitleYear(meta)
	title = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["title"]), title)
	year = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["year"]), year)
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("trackers: PTP missing title for new group upload")
	}
	fields["title"] = title
	fields["year"] = year
	fields["image"] = strings.TrimSpace(poster)
	fields["tags"] = metautil.FirstNonEmptyTrimmed(answers["tags"], resolveTags(meta))
	fields["album_desc"] = metautil.FirstNonEmptyTrimmed(answers["album_desc"], resolveOverview(meta))
	fields["trailer"] = metautil.FirstNonEmptyTrimmed(answers["trailer"], resolveTrailer(meta))
	directors := resolveDirectors(meta)
	if len(directors) > 0 {
		fields["artist[]"] = strings.Join(directors, "\n")
		fields["importance[]"] = "1"
	}
	if fields["image"] == "" {
		return nil, errors.New("trackers: PTP missing poster for new group upload")
	}
	return fields, nil
}

func buildMultipartPayload(fields map[string]string, torrentPath string, fileField string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if key == "artist[]" {
			for value := range strings.SplitSeq(fields[key], "\n") {
				if strings.TrimSpace(value) == "" {
					continue
				}
				if err := writer.WriteField(key, value); err != nil {
					_ = writer.Close()
					return nil, "", fmt.Errorf("trackers: PTP write multipart field %q: %w", key, err)
				}
			}
			continue
		}
		if key == "subtitles[]" || key == "trumpable[]" {
			for value := range strings.SplitSeq(fields[key], ",") {
				trimmed := strings.TrimSpace(value)
				if trimmed == "" {
					continue
				}
				if err := writer.WriteField(key, trimmed); err != nil {
					_ = writer.Close()
					return nil, "", fmt.Errorf("trackers: PTP write multipart field %q: %w", key, err)
				}
			}
			continue
		}
		if err := writer.WriteField(key, fields[key]); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: PTP write multipart field %q: %w", key, err)
		}
	}

	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: PTP open torrent file: %w", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile(fileField, "placeholder.torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: PTP create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: PTP copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: PTP close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveTrackerTorrentPath(meta api.UploadSubject, dbPath string, tracker string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: PTP tracker torrent path requires db path and source path")
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, base+"."+strings.ToLower(strings.TrimSpace(tracker))+".torrent"), nil
}

func resolveFailurePath(meta api.UploadSubject, dbPath string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: PTP failure path requires db path and source path")
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, "[PTP]upload_failure.html"), nil
}

func resolveRemasterTitle(meta api.UploadSubject) string {
	parts := make([]string, 0, 8)
	distributor := strings.ToUpper(strings.TrimSpace(meta.Distributor))
	switch distributor {
	case "WARNER ARCHIVE", "WARNER ARCHIVE COLLECTION", "WAC":
		parts = append(parts, "Warner Archive Collection")
	case "CRITERION", "CRITERION COLLECTION", "CC":
		parts = append(parts, "The Criterion Collection")
	case "MASTERS OF CINEMA", "MOC":
		parts = append(parts, "Masters of Cinema")
	}
	edition := strings.TrimSpace(meta.Edition)
	switch {
	case strings.Contains(strings.ToLower(edition), "director's cut"):
		parts = append(parts, "Director's Cut")
	case strings.Contains(strings.ToLower(edition), "extended"):
		parts = append(parts, "Extended Edition")
	case strings.Contains(strings.ToLower(edition), "theatrical"):
		parts = append(parts, "Theatrical Cut")
	case strings.Contains(strings.ToLower(edition), "uncut"):
		parts = append(parts, "Uncut")
	case strings.Contains(strings.ToLower(edition), "unrated"):
		parts = append(parts, "Unrated")
	case edition != "":
		parts = append(parts, edition)
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		parts = append(parts, "Remux")
	}
	audio := strings.TrimSpace(meta.Audio)
	if strings.Contains(audio, "DTS:X") {
		parts = append(parts, "DTS:X")
	}
	if strings.Contains(audio, "Atmos") {
		parts = append(parts, "Dolby Atmos")
	}
	if strings.Contains(audio, "Dual") {
		parts = append(parts, "Dual Audio")
	}
	if strings.Contains(audio, "Dubbed") {
		parts = append(parts, "English Dub")
	}
	if meta.HDR == "" && meta.BitDepth == "10" {
		parts = append(parts, "10-bit")
	}
	if strings.Contains(meta.HDR, "DV") {
		parts = append(parts, "Dolby Vision")
	}
	if strings.Contains(meta.HDR, "HDR10+") {
		parts = append(parts, "HDR10+")
	} else if strings.Contains(meta.HDR, "HDR") {
		parts = append(parts, "HDR10")
	}
	if strings.Contains(meta.HDR, "HLG") {
		parts = append(parts, "HLG")
	}
	if meta.HasCommentary {
		parts = append(parts, "With Commentary")
	}
	return strings.Join(parts, " / ")
}

func resolveGroupTitleYear(meta api.UploadSubject) (string, string) {
	title := ""
	year := 0
	if meta.ProviderMetadata.TMDB != nil {
		title = strings.TrimSpace(meta.ProviderMetadata.TMDB.Title)
		year = meta.ProviderMetadata.TMDB.Year
	}
	if title == "" && meta.ProviderMetadata.IMDB != nil {
		title = strings.TrimSpace(meta.ProviderMetadata.IMDB.Title)
		year = meta.ProviderMetadata.IMDB.Year
	}
	if title == "" {
		title = strings.TrimSpace(meta.Release.Title)
	}
	if year == 0 {
		year = meta.Release.Year
	}
	if year == 0 {
		return title, ""
	}
	return title, strconv.Itoa(year)
}

func extractAlertError(body string) string {
	start := strings.Index(body, `alert alert--error`)
	if start == -1 {
		return ""
	}
	segment := body[start:]
	end := strings.Index(segment, "</div>")
	if end != -1 {
		segment = segment[:end]
	}
	return stripTags(segment)
}

func stripTags(value string) string {
	inTag := false
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				builder.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func compactError(value string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(trimmed) > 220 {
		return trimmed[:220]
	}
	return trimmed
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.Itoa(int(typed))
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func joinInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

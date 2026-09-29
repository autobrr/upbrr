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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/redaction"
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
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
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
	if req.Logger != nil {
		req.Logger.Debugf("trackers: PTP upload preparation state=started mode=%s tracker_count=%d rehashed=%t",
			req.Intent, len(req.Meta.Trackers), slices.ContainsFunc(req.Meta.RehashedTrackers, func(tracker string) bool {
				return strings.EqualFold(tracker, "PTP")
			}))
	}
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
	if req.Logger != nil {
		req.Logger.Debugf("trackers: PTP upload preparation state=ready group=%s fields=%d body_bytes=%d description_bytes=%d",
			ptpGroupMode(state.groupID), len(state.fields), len(body), len(state.description))
	}
	trackerTorrentPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "PTP")
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: PTP resolve registered torrent path: %w", err)
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType, trackerTorrentPath)
	}, nil), nil
}

// submitPreparedUpload sends the prepared PTP payload once. A final torrent URL confirms
// success; other responses are logged and saved as bounded, redacted diagnostics when possible.
func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
	trackerTorrentPath string,
) (api.UploadSummary, error) {
	started := time.Now()
	if req.Logger != nil {
		req.Logger.Debugf("trackers: PTP submission state=started group=%s body_bytes=%d timeout=%s",
			ptpGroupMode(state.groupID), len(body), state.client.Timeout)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", ptpUserAgent)

	resp, err := state.client.Do(httpReq)
	if err != nil {
		if req.Logger != nil {
			req.Logger.Warnf("trackers: PTP submission state=transport_failed duration=%s error=%s",
				time.Since(started), redaction.RedactValue(err.Error(), nil))
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if matches := ptpSuccessPattern.FindStringSubmatch(finalURL); len(matches) == 3 {
		if req.Logger != nil {
			req.Logger.Debugf("trackers: PTP submission state=accepted status=%d final_page=%s duration=%s",
				resp.StatusCode, ptpResponsePage(resp), time.Since(started))
		}
		groupID := strings.TrimSpace(matches[1])
		torrentID := strings.TrimSpace(matches[2])
		torrentURL := strings.TrimRight(state.baseURL, "/") + "/torrents.php?id=" + url.QueryEscape(groupID) + "&torrentid=" + url.QueryEscape(torrentID)
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "PTP", state.torrentPath, trackerTorrentPath, state.announceURL, "PTP",
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

	responseBody, responsePreview, readErr := commonhttp.ReadUploadResponseBody(resp, false, commonhttp.DefaultResponsePreviewBytes)
	failurePath, artifactErr := commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "PTP", "upload_failure", responsePreview, ".html")
	errText := commonhttp.ExtractHTTPErrorDetail(responsePreview)
	if errText == "" {
		errText = "upload failed"
	}
	if req.Logger != nil {
		groupIDPresent, torrentIDPresent := false, false
		if resp.Request != nil && resp.Request.URL != nil {
			groupIDPresent = resp.Request.URL.Query().Has("id")
			torrentIDPresent = resp.Request.URL.Query().Has("torrentid")
		}
		const failureDiagnostic = "trackers: PTP submission state=rejected status=%d final_page=%s response_kind=%s login_page=%t " +
			"group_id_present=%t torrent_id_present=%t duration=%s detail=%q artifact_saved=%t read_error=%s artifact_error=%s"
		req.Logger.Warnf(
			failureDiagnostic,
			resp.StatusCode,
			ptpResponsePage(resp),
			ptpAuthResponseKind(resp, responseBody),
			ptpLoginPageResponse(resp, responseBody),
			groupIDPresent,
			torrentIDPresent,
			time.Since(started),
			compactError(errText),
			failurePath != "",
			ptpDiagnosticError(readErr),
			ptpDiagnosticError(artifactErr),
		)
	}
	if readErr != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP read upload response: %w", readErr)
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

func ptpGroupMode(groupID string) string {
	if groupID != "" {
		return "existing"
	}
	return "new"
}

// ptpResponsePage classifies the final response path without exposing its URL or query values.
func ptpResponsePage(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return "unknown"
	}
	switch resp.Request.URL.Path {
	case ptpUploadPath:
		return "upload"
	case ptpTorrentPath:
		return "torrent"
	case "/login.php":
		return "login"
	default:
		return "other"
	}
}

// ptpDiagnosticError redacts error text before it is included in a PTP diagnostic log.
func ptpDiagnosticError(err error) string {
	if err == nil {
		return "none"
	}
	return redaction.RedactValue(err.Error(), nil)
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
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return uploadState{}, nameFailure
	}
	announceURL := normalizedAnnounceURL(req.TrackerConfig.AnnounceURL)
	if !dryRun && announceURL == "" {
		return uploadState{}, errors.New("trackers: PTP required announce URL is missing")
	}
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
	groupID, err := lookupGroupID(ctx, baseURL, req.TrackerConfig, req.Meta, req.Logger)
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
	if req.Logger != nil {
		req.Logger.Debugf("trackers: PTP upload inputs state=ready group=%s poster_present=%t screenshots=%d selected_image_host=%s",
			ptpGroupMode(groupID), poster != "", len(assets.Screenshots), strings.TrimSpace(req.SelectedImageHost))
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

// lookupGroupID finds an existing PTP group when API credentials and an IMDb ID are available.
// Transport, HTTP status, and decode failures are logged and fall back to a new group.
func lookupGroupID(ctx context.Context, baseURL string, trackerConfig config.TrackerConfig, meta api.UploadSubject, logger api.Logger) (string, error) {
	if logger == nil {
		logger = api.NopLogger{}
	}
	apiUser := strings.TrimSpace(trackerConfig.PTPAPIUser)
	apiKey := strings.TrimSpace(trackerConfig.PTPAPIKey)
	if apiUser == "" || apiKey == "" || meta.Identity.IMDBID == 0 {
		logger.Debugf("trackers: PTP group lookup state=skipped api_credentials_present=%t imdb_present=%t",
			apiUser != "" && apiKey != "", meta.Identity.IMDBID != 0)
		return "", nil
	}
	logger.Debugf("trackers: PTP group lookup state=started")
	headers := map[string]string{
		"ApiUser":    apiUser,
		"ApiKey":     apiKey,
		"User-Agent": ptpUserAgent,
	}
	values := url.Values{}
	values.Set("imdb", providerid.IMDb(meta.Identity.IMDBID).Digits())
	values.Set("json", "noredirect")
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
		logger.Warnf("trackers: PTP group lookup state=request_failed decision=new_group error=%s", redaction.RedactValue(err.Error(), nil))
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Warnf("trackers: PTP group lookup state=http_failed decision=new_group status=%d", resp.StatusCode)
		return "", nil
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		logger.Warnf(
			"trackers: PTP group lookup state=decode_failed decision=new_group status=%d error=%s",
			resp.StatusCode,
			redaction.RedactValue(err.Error(), nil),
		)
		return "", nil
	}
	if movies, ok := payload["Movies"].([]any); ok && len(movies) > 0 {
		if movie, ok := movies[0].(map[string]any); ok {
			if groupID := stringFromAny(movie["GroupId"]); groupID != "" {
				logger.Debugf("trackers: PTP group lookup state=completed group=existing")
				return groupID, nil
			}
		}
	}
	groupID := stringFromAny(payload["GroupId"])
	logger.Debugf("trackers: PTP group lookup state=completed group=%s", ptpGroupMode(groupID))
	return groupID, nil
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
	var err error
	meta, err = withHardcodedSubtitleLanguages(meta, answers["hardcoded_subtitle_languages"])
	if err != nil {
		return nil, err
	}
	resolution, resolutionWidth, resolutionHeight := resolveResolution(meta)
	if resolution == "Other" && (resolutionWidth == "" || resolutionHeight == "") {
		return nil, errors.New("trackers: PTP custom resolution requires width and height")
	}
	codec, otherCodec := ptpUploadTaxonomy(
		resolveCodec(meta), "XviD", "DivX", "H.264", "x264", "H.265", "x265", "DVD5", "DVD9", "BD25", "BD50", "BD66", "BD100",
	)
	container, otherContainer := ptpUploadTaxonomy(resolveContainer(meta), "AVI", "MPG", "MKV", "MP4", "VOB IFO", "ISO", "m2ts")
	source, otherSource := ptpUploadTaxonomy(resolveSource(meta.Source), "Blu-ray", "DVD", "WEB", "HD-DVD", "HDTV", "TV", "VHS")
	if otherSource != "" {
		otherSource = metautil.FirstNonEmptyTrimmed(meta.Source, "Unknown")
	}
	fields := map[string]string{
		"submit":          "true",
		"remaster_year":   "",
		"remaster_title":  resolveRemasterTitle(meta),
		"type":            resolveType(meta),
		"codec":           codec,
		"other_codec":     otherCodec,
		"container":       container,
		"other_container": otherContainer,
		"resolution":      resolution,
		"source":          source,
		"other_source":    otherSource,
		"release_desc":    description,
		"nfo_text":        "",
		"subtitles[]":     joinInts(resolveSubtitles(meta)),
		"trumpable[]":     joinInts(resolveTrumpable(meta)),
	}
	if resolution == "Other" {
		fields["other_resolution_width"] = resolutionWidth
		fields["other_resolution_height"] = resolutionHeight
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
		fields["imdb"] = providerid.IMDb(meta.Identity.IMDBID).Digits()
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

func ptpUploadTaxonomy(value string, allowed ...string) (string, string) {
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), item) {
			return item, ""
		}
	}
	return "Other", strings.TrimSpace(value)
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
	title = trackers.PreferredTitle(meta, title)
	year = trackers.PreferredYear(meta, year)
	if year == 0 {
		return title, ""
	}
	return title, strconv.Itoa(year)
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

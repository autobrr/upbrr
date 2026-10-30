// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const antUploadURL = "https://anthelion.me/api.php"

type uploadState struct {
	torrentPath  string
	description  string
	releaseName  string
	fields       map[string]string
	adultContent bool
	manualTags   bool
	typeName     string
	tags         string
	trumpRetry   bool
	trumpReason  string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state, req.Meta)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "ANT")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req.Logger, state, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

// submitPreparedUpload submits the captured payload and retains registered
// torrent authority when available. Artifact retrieval failure does not undo
// remote success; modified torrents are never reconstructed from unregistered bytes.
func submitPreparedUpload(
	ctx context.Context,
	logger api.Logger,
	state uploadState,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	client := httpclient.New(httpclient.DefaultTimeout)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Scheme != "https" || req.URL.Host != "anthelion.me" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	status, payload, bodyBytes, err := postPreparedUpload(ctx, client, body, contentType)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if key := trumpChallengeKey(status, payload); state.trumpRetry && key != "" {
		body, contentType, err = trumpRetryPayload(body, contentType, key, state.trumpReason)
		if err != nil {
			return api.UploadSummary{}, err
		}
		logger.Infof("trackers: tracker=ANT state=retrying reason=reviewed_trump_challenge")
		status, payload, bodyBytes, err = postPreparedUpload(ctx, client, body, contentType, key)
		if err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: ANT trump retry: %w", trumpRetryError{cause: err, key: key})
		}
		// A replacement challenge is a failed retry, never permission to submit again.
		if status < 200 || status >= 300 || !antUploadSuccess(payload) {
			return api.UploadSummary{}, fmt.Errorf("trackers: ANT trump retry: %w", antUploadError(status, payload, bodyBytes, key))
		}
	}
	if status < 200 || status >= 300 || !antUploadSuccess(payload) {
		return api.UploadSummary{}, antUploadError(status, payload, bodyBytes)
	}

	viewURL := antString(payload["view"])
	if viewURL == "" {
		viewURL = strings.TrimSpace(stringValue(payload["link"]))
	}
	torrentID := ""
	if parsed, parseErr := url.Parse(viewURL); parseErr == nil {
		torrentID = parsed.Query().Get("torrentid")
	}

	registeredPath := ""
	downloadURL := antString(payload["download"])
	switch {
	case downloadURL != "":
		// Only retrieve ANT-hosted download links; API credentials must not leave ANT.
		download, parseErr := url.Parse(downloadURL)
		if parseErr == nil && download.Scheme == "https" && download.Host == "anthelion.me" && artifactPath != "" {
			downloadReq, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
			if requestErr == nil {
				downloadReq.Header.Set("User-Agent", "upbrr")
				downloadReq.Header.Set("X-Api-Key", state.fields["api_key"])
				if trackers.DownloadRegisteredTorrent(ctx, client, downloadReq, artifactPath) == nil {
					registeredPath = artifactPath
				}
			}
		}
		if registeredPath == "" {
			trackers.LogRegisteredTorrentUnavailable(logger, "ANT")
		}
	case antString(payload["message"]) != "":
		// ANT reports a modified torrent through message/download. Without the
		// registered bytes, reconstruction cannot establish the server's infohash.
		trackers.LogRegisteredTorrentUnavailable(logger, "ANT")
	default:
		registeredPath = trackers.PersistReconstructedRegisteredTorrent(logger, "ANT", state.torrentPath, artifactPath, announceURL, "ANT")
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "ANT",
			TorrentID:   torrentID,
			TorrentURL:  viewURL,
			TorrentPath: registeredPath,
		}},
	}, nil
}

// trumpRetryError hides a challenge key even in transport/body-read diagnostics,
// while preserving the cause for cancellation and uncertain-outcome classification.
type trumpRetryError struct {
	cause error
	key   string
}

func (e trumpRetryError) Error() string {
	return commonhttp.RedactErrorDetail(e.cause.Error(), e.key)
}

func (e trumpRetryError) Unwrap() error { return e.cause }

// postPreparedUpload sends one captured request without transport-level retries.
func postPreparedUpload(ctx context.Context, client *http.Client, body []byte, contentType string, secrets ...string) (int, map[string]any, []byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, antUploadURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("trackers: ANT request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	response, err := commonhttp.ExecuteUpload(client, httpReq, commonhttp.UploadExecutionOptions{
		Tracker: "ANT",
		// Retain bounded structured bodies for every status: errors may carry a
		// duplicate view link or challenge key. Status acceptance is checked below.
		SuccessStatus: func(int) bool { return true },
	})
	if err != nil {
		return 0, nil, nil, fmt.Errorf("trackers: ANT submit captured upload: %w", err)
	}
	bodyBytes := response.Body
	payload := map[string]any{}
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			return 0, nil, nil, antUploadError(response.StatusCode, payload, bodyBytes, secrets...)
		}
	}
	return response.StatusCode, payload, bodyBytes, nil
}

// trumpChallengeKey recognizes a fresh class-based challenge, not a rejected key.
func trumpChallengeKey(status int, payload map[string]any) string {
	if status != http.StatusConflict {
		return ""
	}
	if _, rejected := payload["key_error"]; rejected {
		return ""
	}
	key, ok := payload["trump_key"].(string)
	if !ok || strings.TrimSpace(key) != key || utf8.RuneCountInString(key) != 16 {
		return ""
	}
	expires, err := time.Parse(time.RFC3339, antString(payload["expires"]))
	if err != nil || !expires.After(time.Now()) || antString(payload["slot"]) == "" || antString(payload["error"]) == "" {
		return ""
	}
	return key
}

// trumpRetryPayload adds challenge fields to the immutable captured multipart body.
// In particular, it never reopens the torrent path after the initial submission.
func trumpRetryPayload(body []byte, contentType, key, reason string) ([]byte, string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: ANT retry content type: %w", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	for {
		part, readErr := reader.NextPart()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, "", fmt.Errorf("trackers: ANT retry read captured payload: %w", readErr)
		}
		destination, createErr := writer.CreatePart(part.Header)
		if createErr != nil {
			return nil, "", fmt.Errorf("trackers: ANT retry copy part: %w", createErr)
		}
		if _, copyErr := io.Copy(destination, part); copyErr != nil {
			return nil, "", fmt.Errorf("trackers: ANT retry copy captured payload: %w", copyErr)
		}
	}
	if err := writer.WriteField("trump_key", key); err != nil {
		return nil, "", fmt.Errorf("trackers: ANT retry write key: %w", err)
	}
	if reason != "" {
		if err := writer.WriteField("trump_reason", reason); err != nil {
			return nil, "", fmt.Errorf("trackers: ANT retry write reason: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: ANT retry close payload: %w", err)
	}
	return output.Bytes(), writer.FormDataContentType(), nil
}

func buildUploadPreview(state uploadState, meta api.UploadSubject) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "ANT",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "ant",
		Description:      state.description,
		Endpoint:         antUploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
		Questionnaire: buildQuestionnaire(meta, state),
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: ANT missing api_key")
	}
	if req.Meta.Identity.TMDBID == 0 {
		return uploadState{}, errors.New("trackers: ANT missing tmdb id")
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: ANT reviewed upload name: %w", err)
	}
	descriptionAssets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		descriptionAssets = trackers.DescriptionAssets{}
	}
	description, err := buildDescription(ctx, req, descriptionAssets)
	if err != nil {
		return uploadState{}, err
	}

	answers := standalone.QuestionnaireAnswers(req.Meta, "ANT")
	typeName, typeID := resolveType(req.Meta, answers)
	flags := resolveFlags(req.Meta)
	tags, manualTags := resolveTags(req.Meta, answers)
	adultContent := detectAdult(req.Meta)
	safeScreens := resolveAdultScreensAllowed(answers, adultContent)
	screenshots := resolveScreenshotPayload(descriptionAssets.Screenshots, safeScreens)
	mediaFields, err := resolveMediaFields(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, err
	}

	fields := map[string]string{
		"api_key":      strings.TrimSpace(req.TrackerConfig.APIKey),
		"action":       "upload",
		"tmdbid":       strconv.Itoa(req.Meta.Identity.TMDBID),
		"type":         strconv.Itoa(typeID),
		"media":        resolveMediaSource(req.Meta.Source),
		"release_desc": description,
		"screenshots":  screenshots,
	}
	maps.Copy(fields, mediaFields)
	if req.TrackerConfig.Anon {
		fields["anonymous"] = "1"
	}
	if len(flags) > 0 {
		fields["flags[]"] = strings.Join(flags, ",")
	}
	if req.Meta.Scene {
		fields["scene"] = "1"
	}
	if fullDisc(req.Meta) {
		if country := discCountry(req.Meta.Region); country != "" {
			fields["disc_country"] = country
		}
	}
	id, err := requestID(answers)
	if err != nil {
		return uploadState{}, err
	}
	if id != "" {
		fields["requestid"] = id
	}
	if tags != "" {
		fields["tags"] = tags
	}
	if releaseGroup, ok := resolveReleaseGroup(req.Meta.Tag); ok {
		fields["releasegroup"] = releaseGroup
	} else {
		fields["noreleasegroup"] = "1"
	}
	if adultContent && screenshots != "" {
		if !manualTags {
			fields["flagchangereason"] = "Adult with screens uploaded with upbrr"
		} else {
			fields["flagchangereason"] = "Adult with screens uploaded with upbrr. User to add tags manually."
		}
	} else if manualTags {
		fields["flagchangereason"] = "User prompted to add tags manually"
	}

	trumpRetry, trumpReason := intendedTrump(req)
	return uploadState{
		trumpRetry:   trumpRetry,
		trumpReason:  trumpReason,
		torrentPath:  torrentPath,
		description:  description,
		releaseName:  releaseName,
		fields:       fields,
		adultContent: adultContent,
		manualTags:   manualTags,
		typeName:     typeName,
		tags:         tags,
	}, nil
}

func resolveReleaseGroup(tag string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(tag, "-"))
	if trimmed == "" {
		return "", false
	}
	if isBannedReleaseGroup(trimmed) {
		return "", false
	}
	return trimmed, true
}

func resolveKeywords(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
	}
	return ""
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if key == "flags[]" {
			for item := range strings.SplitSeq(value, ",") {
				trimmed := strings.TrimSpace(item)
				if trimmed == "" {
					continue
				}
				if err := writer.WriteField(key, trimmed); err != nil {
					_ = writer.Close()
					return nil, "", fmt.Errorf("trackers: ANT write multipart field %q: %w", key, err)
				}
			}
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: ANT write multipart field %q: %w", key, err)
		}
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: ANT open torrent file: %w", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file_input", "torrent.torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: ANT create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: ANT copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: ANT close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func antUploadSuccess(payload map[string]any) bool {
	if success, ok := payload["success"]; ok {
		switch value := success.(type) {
		case bool:
			return value
		case string:
			return strings.EqualFold(strings.TrimSpace(value), "true") || strings.EqualFold(strings.TrimSpace(value), "success")
		}
	}
	return strings.EqualFold(strings.TrimSpace(stringValue(payload["status"])), "success")
}

// antUploadError preserves redacted API rejection details before status fallbacks,
// including reasons returned in a successful HTTP response.
func antUploadError(status int, payload map[string]any, body []byte, secrets ...string) error {
	if key, ok := payload["trump_key"].(string); ok && key != "" {
		secrets = append(secrets, key)
	}
	if detail := commonhttp.ExtractHTTPErrorDetail(body, secrets...); detail != "" {
		if viewURL := strings.TrimSpace(stringValue(payload["view"])); viewURL != "" {
			detail += " (" + commonhttp.RedactErrorDetail(viewURL, secrets...) + ")"
		}
		return fmt.Errorf("trackers: ANT upload failed status=%d: %s", status, detail)
	}
	switch status {
	case http.StatusForbidden:
		return errors.New("trackers: ANT wrong API key or insufficient permissions")
	case http.StatusInternalServerError:
		return errors.New("trackers: ANT internal server error")
	case http.StatusBadGateway:
		return errors.New("trackers: ANT bad gateway")
	}
	return commonhttp.UploadHTTPError("ANT", status, body)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

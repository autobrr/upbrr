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
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const antUploadURL = "https://anthelion.me/api.php"

var antTorrentIDPattern = regexp.MustCompile(`id=(\d+)`)

var antDefaultSignaturePattern = regexp.MustCompile(
	`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`,
)
var antEmptyURLPattern = regexp.MustCompile(`(?is)\[url=[^\]]*]\s*\[/url\]`)

type uploadState struct {
	torrentPath  string
	description  string
	releaseName  string
	fields       map[string]string
	adultContent bool
	manualTags   bool
	typeName     string
	tags         string
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

func submitPreparedUpload(
	ctx context.Context,
	logger api.Logger,
	state uploadState,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, antUploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ANT request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ANT upload request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ANT read response: %w", err)
	}

	payload := map[string]any{}
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return api.UploadSummary{}, errors.New("trackers: ANT json decode error, the API is probably down")
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !antUploadSuccess(payload) {
		return api.UploadSummary{}, antUploadError(resp.StatusCode, payload, bodyBytes)
	}

	viewURL := strings.TrimSpace(stringValue(payload["view"]))
	if viewURL == "" {
		viewURL = strings.TrimSpace(stringValue(payload["link"]))
	}
	torrentID := ""
	if matches := antTorrentIDPattern.FindStringSubmatch(viewURL); len(matches) > 1 {
		torrentID = strings.TrimSpace(matches[1])
	}

	registeredPath := trackers.PersistReconstructedRegisteredTorrent(
		logger, "ANT", state.torrentPath, artifactPath, announceURL, viewURL, "ANT",
	)

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
	descriptionAssets.Description = trackers.StripDefaultDescriptionSignature(descriptionAssets.Description)
	description := buildDescription(req, descriptionAssets)

	answers := standalone.QuestionnaireAnswers(req.Meta, "ANT")
	typeName, typeID := resolveType(req.Meta, answers)
	audio := resolveAudioFormat(req.Meta)
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
		"audioformat":  audio,
		"release_desc": description,
		"screenshots":  screenshots,
	}
	maps.Copy(fields, mediaFields)
	if len(flags) > 0 {
		fields["flags[]"] = strings.Join(flags, ",")
	}
	if req.Meta.Scene {
		fields["censored"] = "1"
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

	return uploadState{
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

func antUploadError(status int, payload map[string]any, body []byte) error {
	text := strings.ToLower(compactJSON(payload))
	if status == http.StatusBadRequest {
		switch {
		case strings.Contains(text, "same infohash"):
			if viewURL := strings.TrimSpace(stringValue(payload["view"])); viewURL != "" {
				return fmt.Errorf("trackers: ANT same infohash already exists: %s", commonhttp.RedactErrorDetail(viewURL))
			}
			return errors.New("trackers: ANT same infohash already exists")
		case strings.Contains(text, "exact same"):
			return errors.New("trackers: ANT exact same media file already exists")
		}
	}
	if detail := commonhttp.ExtractHTTPErrorDetail(body); detail != "" {
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
	if message := strings.TrimSpace(stringValue(payload["error"])); message != "" {
		return fmt.Errorf("trackers: ANT api error: %s", commonhttp.RedactErrorDetail(message))
	}
	return commonhttp.UploadHTTPError("ANT", status, body)
}

func compactJSON(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
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

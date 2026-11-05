// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://greatposterwall.com"
	torrentURL = baseURL + "/torrents.php?torrentid="
	sourceFlag = "GreatPosterWall"
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string][]string
	groupID       string
	questionnaire *api.TrackerQuestionnaire
	blockedReason string
}

type apiResponse struct {
	Status   any    `json:"status"`
	Response any    `json:"response"`
	Error    string `json:"error"`
	Message  string `json:"message"`
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, trackers.NewQuestionnairePreparationFailure("GPW", state.blockedReason, state.questionnaire)
	}
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(state.fields, []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  "GPW.placeholder.torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	uploadEndpoint := baseURL + "/api.php?api_key=" + req.TrackerConfig.APIKey + "&action=upload"
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "GPW")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType, uploadEndpoint, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
	uploadEndpoint string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		uploadEndpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return api.UploadSummary{}, submission.NewError(
			submission.NotSent,
			fmt.Errorf("trackers: GPW upload request build: %s", commonhttp.RedactErrorDetail(err.Error())),
		)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	execution, readErr := commonhttp.ExecuteUpload(httpclient.New(httpclient.DefaultTimeout), httpReq, commonhttp.UploadExecutionOptions{Tracker: "GPW"})
	diagnostic := uploadDiagnostic(execution, req.TrackerConfig.APIKey, req.TrackerConfig.Passkey, req.TrackerConfig.AnnounceURL)
	if readErr != nil && !execution.BodyComplete {
		detail := commonhttp.RedactErrorDetail(readErr.Error(), req.TrackerConfig.APIKey, req.TrackerConfig.Passkey, req.TrackerConfig.AnnounceURL)
		diagnostic += "; error=" + diagnosticURL.ReplaceAllString(detail, "[redacted URL]")
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "GPW", "upload_failure", []byte(diagnostic), ".txt")
		return api.UploadSummary{}, &diagnosticError{message: "trackers: GPW response unavailable; " + diagnostic, cause: readErr}
	}
	var decoded apiResponse
	if err := json.Unmarshal(execution.ProtocolBody, &decoded); err != nil {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "GPW", "upload_failure", []byte(diagnostic), ".txt")
		return api.UploadSummary{}, &diagnosticError{message: "trackers: GPW returned a non-JSON response; " + diagnostic, cause: err}
	}
	id := extractTorrentID(decoded.Response)
	status := strings.ToLower(strings.TrimSpace(fmt.Sprint(decoded.Status)))
	if execution.Success && strings.TrimSpace(decoded.Error) == "" &&
		(status == "success" || status == "ok" || (status == "200" && id != "")) {
		tURL := ""
		if id != "" {
			tURL = torrentURL + id
		}
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "GPW", state.torrentPath, artifactPath, announceURL, sourceFlag, tURL,
		)
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "GPW",
			TorrentID:   id,
			TorrentURL:  tURL,
			TorrentPath: registeredPath,
		}}}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "GPW", "upload_failure", []byte(diagnostic), ".txt")
	return api.UploadSummary{}, fmt.Errorf("trackers: GPW upload failed; %s", diagnostic)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	message := "dry-run payload generated"
	if state.groupID == "" {
		message += " for new group"
	} else {
		message += " for existing group"
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "GPW",
		ReadyMessage:     message,
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "gpw",
		Description:      state.description,
		Endpoint:         baseURL + "/api.php?action=upload",
		Payload:          previewFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: GPW missing api_key")
	}
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	groupID, _ := lookupGroupID(ctx, req.TrackerConfig.APIKey, req.Meta)
	answers := standalone.QuestionnaireAnswers(req.Meta, "GPW")
	fields := buildForm(req, description, groupID, answers)
	if reason := validateMetadataFields(groupID, fields); reason != "" {
		return uploadState{}, trackers.NewPreparationFailure("GPW", "missing_metadata", reason, nil)
	}
	if req.Logger != nil {
		unmapped := 0
		for _, language := range append(append([]string(nil), req.Meta.SubtitleLanguages...), req.Meta.HardcodedSubtitleLanguages...) {
			if subtitleKey(language) == "" {
				unmapped++
			}
		}
		if unmapped > 0 {
			req.Logger.Warnf("trackers: subtitle languages have no unambiguous GPW key tracker=GPW state=unmapped count=%d", unmapped)
		}
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: GPW reviewed upload name: %w", err)
	}
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        fields,
		groupID:       groupID,
		questionnaire: buildQuestionnaire(req.Meta, groupID, answers),
	}
	if reason := validateFields(groupID, fields); reason != "" {
		state.blockedReason = reason
	}
	return state, nil
}

func lookupGroupID(ctx context.Context, apiKey string, meta api.UploadSubject) (string, error) {
	if meta.Identity.IMDBID == 0 {
		return "", nil
	}
	url := fmt.Sprintf("%s/api.php?api_key=%s&action=torrent&req=group&imdbID=%s", baseURL, apiKey, providerid.IMDb(meta.Identity.IMDBID).Prefixed())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: GPW torrent URL lookup request build: %s", commonhttp.RedactErrorDetail(err.Error()))
	}
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return "", fmt.Errorf("trackers: GPW torrent URL lookup request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var decoded apiResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", nil
	}
	if responseMap, ok := decoded.Response.(map[string]any); ok {
		if value, ok := responseMap["ID"]; ok {
			return strings.TrimSpace(fmt.Sprint(value)), nil
		}
	}
	return "", nil
}

func extractTorrentID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if torrentID, ok := typed["torrent_id"]; ok {
			return commonhttp.PositiveTorrentID(torrentID)
		}
	case []any:
		if len(typed) > 0 {
			if first, ok := typed[0].(map[string]any); ok {
				if torrentID, ok := first["torrent_id"]; ok {
					return commonhttp.PositiveTorrentID(torrentID)
				}
			}
		}
	}
	return ""
}

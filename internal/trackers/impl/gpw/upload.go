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
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
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
	fields        map[string]string
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

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  "GPW.placeholder.torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api.php?api_key="+req.TrackerConfig.APIKey+"&action=upload", bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW upload request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	var decoded apiResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW decode response: %w", err)
	}
	id := extractTorrentID(decoded.Response)
	status := strings.ToLower(strings.TrimSpace(fmt.Sprint(decoded.Status)))
	if (status == "success" || status == "ok" || status == "200") && id != "" {
		tURL := torrentURL + id
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "GPW")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{Tracker: "GPW", TorrentID: id, TorrentURL: tURL, DownloadURL: tURL, TorrentPath: artifactPath}}}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "GPW", "upload_failure", responseBody, ".json")
	return api.UploadSummary{}, fmt.Errorf("trackers: GPW %s", firstNonEmpty(decoded.Error, decoded.Message, "upload failed"))
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	status := "ready"
	message := "dry-run payload generated"
	if state.groupID == "" {
		message += " for new group"
	} else {
		message += " for existing group"
	}
	if state.blockedReason != "" {
		status = "blocked"
		message = state.blockedReason
	}
	return api.TrackerDryRunEntry{
		Tracker:          "GPW",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "gpw",
		Description:      state.description,
		Endpoint:         baseURL + "/api.php?api_key=" + req.TrackerConfig.APIKey + "&action=upload",
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "file_input", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: GPW missing api_key")
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(assets)
	groupID, _ := lookupGroupID(ctx, req.TrackerConfig.APIKey, req.Meta)
	answers := questionnaireAnswers(req.Meta)
	fields := buildFields(req.Meta, req.TrackerConfig, description, groupID, answers)
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   firstNonEmpty(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename),
		fields:        fields,
		groupID:       groupID,
		questionnaire: buildQuestionnaire(req.Meta, groupID, answers),
	}
	if reason := validateFields(groupID, fields); reason != "" {
		state.blockedReason = reason
	}
	return state, nil
}

func lookupGroupID(ctx context.Context, apiKey string, meta api.PreparedMetadata) (string, error) {
	if meta.ExternalIDs.IMDBID == 0 {
		return "", nil
	}
	url := fmt.Sprintf("%s/api.php?api_key=%s&action=torrent&req=group&imdbID=tt%07d", baseURL, apiKey, meta.ExternalIDs.IMDBID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: GPW torrent URL lookup request build: %w", err)
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

func buildFields(meta api.PreparedMetadata, trackerCfg config.TrackerConfig, description string, groupID string, answers map[string]string) map[string]string {
	fields := map[string]string{
		"codec_other":               "",
		"codec":                     resolveCodec(meta),
		"container_other":           "",
		"container":                 resolveContainer(meta),
		"mediainfo[]":               resolveMedia(meta),
		"movie_edition_information": onOff(strings.TrimSpace(meta.Edition) != ""),
		"processing_other":          "",
		"processing":                resolveProcessing(meta),
		"release_desc":              description,
		"remaster_custom_title":     "",
		"remaster_title":            strings.TrimSpace(meta.Edition),
		"remaster_year":             "",
		"resolution_height":         "",
		"resolution_width":          "",
		"resolution":                resolveResolution(meta),
		"source_other":              "",
		"source":                    resolveSource(meta),
		"submit":                    "true",
		"subtitle_type":             resolveSubtitleType(meta),
		"subtitles[]":               strings.Join(resolveSubtitles(meta), ","),
	}
	if groupID != "" {
		fields["groupid"] = groupID
	} else {
		fields["data_source"] = firstNonEmpty(strings.TrimSpace(answers["data_source"]), "imdb")
		fields["identifier"] = firstNonEmpty(strings.TrimSpace(answers["identifier"]), resolveIdentifier(meta))
		fields["desc"] = firstNonEmpty(strings.TrimSpace(answers["desc"]), resolveOverview(meta))
		fields["image"] = strings.TrimSpace(answers["poster_url"])
		fields["maindesc"] = firstNonEmpty(strings.TrimSpace(answers["main_desc"]), resolveOverview(meta))
		fields["name"] = firstNonEmpty(strings.TrimSpace(answers["title"]), meta.Release.Title)
		fields["releasetype"] = firstNonEmpty(strings.TrimSpace(answers["release_type"]), resolveMovieType(meta))
		fields["subname"] = firstNonEmpty(strings.TrimSpace(answers["subname"]), meta.Release.Title)
		fields["tags"] = firstNonEmpty(strings.TrimSpace(answers["tags"]), resolveTags(meta))
		fields["year"] = strconv.Itoa(resolveYear(meta))
		fields["artists[]"] = firstNonEmpty(strings.TrimSpace(answers["director_name"]), resolveDirectorName(meta))
		fields["importance[]"] = "1"
		fields["artist_ids[]"] = strings.TrimSpace(answers["director_imdb"])
		fields["artist_subs[]"] = strings.TrimSpace(answers["director_chinese"])
		fields["characters[]"] = ""
		fields["main_artist_number"] = "1"
	}
	for key, value := range resolveMediaFlags(meta) {
		fields[key] = value
	}
	if meta.Scene {
		fields["scene"] = "on"
	}
	if meta.PersonalRelease {
		if isDiscUpload(meta) {
			fields["buy"] = "on"
		} else {
			fields["diy"] = "on"
		}
	}
	if trackerCfg.Exclusive {
		fields["jinzhuan"] = "on"
	}
	return fields
}

func isDiscUpload(meta api.PreparedMetadata) bool {
	return strings.TrimSpace(meta.DiscType) != "" || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC")
}

func buildQuestionnaire(meta api.PreparedMetadata, groupID string, answers map[string]string) *api.TrackerQuestionnaire {
	if groupID != "" {
		return nil
	}
	fields := []api.TrackerQuestionnaireField{
		{Key: "poster_url", Label: "Poster URL", Kind: "text", Value: firstNonEmpty(answers["poster_url"], resolvePoster(meta)), Required: true},
		{Key: "director_imdb", Label: "Director IMDb ID", Kind: "text", Value: answers["director_imdb"], Placeholder: "nm0000138", Required: true},
		{Key: "director_name", Label: "Director Name", Kind: "text", Value: firstNonEmpty(answers["director_name"], resolveDirectorName(meta)), Required: true},
		{Key: "director_chinese", Label: "Director Chinese", Kind: "text", Value: answers["director_chinese"]},
		{Key: "tags", Label: "Tags", Kind: "text", Value: firstNonEmpty(answers["tags"], resolveTags(meta)), Required: true},
	}
	return &api.TrackerQuestionnaire{Tracker: "GPW", Fields: fields}
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["GPW"]
}

func validateFields(groupID string, fields map[string]string) string {
	if groupID == "" {
		for _, key := range []string{"image", "artists[]", "artist_ids[]", "tags"} {
			if strings.TrimSpace(fields[key]) == "" {
				return "missing required new-group data"
			}
		}
	}
	return ""
}

func buildDescription(assets trackers.DescriptionAssets) string {
	return bbcode.FinalizeTrackerDescription("GPW", strings.TrimSpace(assets.Description))
}

func extractTorrentID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if torrentID, ok := typed["torrent_id"]; ok {
			return strings.TrimSpace(fmt.Sprint(torrentID))
		}
	case []any:
		if len(typed) > 0 {
			if first, ok := typed[0].(map[string]any); ok {
				if torrentID, ok := first["torrent_id"]; ok {
					return strings.TrimSpace(fmt.Sprint(torrentID))
				}
			}
		}
	}
	return ""
}

func resolveCodec(meta api.PreparedMetadata) string {
	codec := strings.ToLower(strings.TrimSpace(firstNonEmpty(meta.VideoEncode, meta.VideoCodec)))
	switch {
	case strings.Contains(codec, "hevc"), strings.Contains(codec, "265"):
		return "HEVC"
	case strings.Contains(codec, "avc"), strings.Contains(codec, "264"):
		return "AVC"
	case strings.Contains(codec, "vc-1"):
		return "VC-1"
	default:
		return "Other"
	}
}

func resolveContainer(meta api.PreparedMetadata) string {
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	switch container {
	case "mkv", "mp4", "avi", "vob", "m2ts":
		return strings.ToUpper(container)
	default:
		return "Other"
	}
}

func resolveProcessing(meta api.PreparedMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "ENCODE":
		return "Encode"
	case "REMUX":
		return "Remux"
	case "DIY":
		return "DIY"
	default:
		return "Untouched"
	}
}

func resolveResolution(meta api.PreparedMetadata) string {
	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	switch resolution {
	case "480p", "576p", "720p", "1080i", "1080p", "2160p":
		return resolution
	default:
		return "Other"
	}
}

func resolveSource(meta api.PreparedMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "DISC":
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
			return "Blu-ray"
		}
		return "DVD"
	case "WEBDL", "WEBRIP":
		return "WEB"
	case "REMUX", "ENCODE":
		return "Blu-ray"
	case "HDTV":
		return "HDTV"
	default:
		return "Other"
	}
}

func resolveSubtitleType(meta api.PreparedMetadata) string {
	if len(meta.SubtitleLanguages) > 0 {
		return "1"
	}
	return "3"
}

func resolveSubtitles(meta api.PreparedMetadata) []string {
	out := make([]string, 0, len(meta.SubtitleLanguages))
	for _, lang := range meta.SubtitleLanguages {
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "english", "en":
			out = append(out, "English")
		case "chinese", "zh":
			out = append(out, "Chinese")
		case "portuguese", "pt":
			out = append(out, "Portuguese")
		}
	}
	return out
}

func resolveMedia(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if summary, ok := meta.BDInfo["summary"].(string); ok {
			return strings.TrimSpace(summary)
		}
	}
	return strings.TrimSpace(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath))
}

func resolveIdentifier(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
	}
	if meta.ExternalIDs.TMDBID > 0 {
		return strconv.Itoa(meta.ExternalIDs.TMDBID)
	}
	return ""
}

func resolveOverview(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot)
	}
	return ""
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover)
	}
	return ""
}

func resolveMovieType(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.RuntimeMinutes > 0 && meta.ExternalMetadata.IMDB.RuntimeMinutes < 45 {
		return "2"
	}
	return "1"
}

func resolveTags(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(strings.ToLower(strings.ReplaceAll(meta.ExternalMetadata.TMDB.Genres, ", ", ",")))
	}
	return strings.TrimSpace(strings.ToLower(meta.Release.Genre))
}

func resolveDirectorName(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.Directors) > 0 {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Directors[0])
	}
	if meta.ExternalMetadata.IMDB != nil && len(meta.ExternalMetadata.IMDB.Directors) > 0 {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Directors[0].Name)
	}
	return ""
}

func resolveYear(meta api.PreparedMetadata) int {
	if meta.ExternalMetadata.TMDB != nil && meta.ExternalMetadata.TMDB.Year > 0 {
		return meta.ExternalMetadata.TMDB.Year
	}
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.Year > 0 {
		return meta.ExternalMetadata.IMDB.Year
	}
	return meta.Release.Year
}

func resolveMediaFlags(meta api.PreparedMetadata) map[string]string {
	flags := map[string]string{}
	audio := strings.ToLower(strings.TrimSpace(meta.Audio))
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	if strings.Contains(audio, "atmos") {
		flags["dolby_atmos"] = "on"
	}
	if strings.Contains(audio, "dts:x") {
		flags["dts_x"] = "on"
	}
	if meta.Channels == "5.1" {
		flags["audio_51"] = "on"
	}
	if meta.Channels == "7.1" {
		flags["audio_71"] = "on"
	}
	if meta.BitDepth == "10" && hdr == "" {
		flags["10_bit"] = "on"
	}
	if strings.Contains(hdr, "DV") {
		flags["dolby_vision"] = "on"
	}
	if strings.Contains(hdr, "HDR10+") {
		flags["hdr10plus"] = "on"
	} else if strings.Contains(hdr, "HDR") {
		flags["hdr10"] = "on"
	}
	return flags
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return ""
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

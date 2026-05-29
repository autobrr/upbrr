// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

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
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	bhdBaseURL            = "https://beyond-hd.me"
	bhdTorrentIDPattern   = regexp.MustCompile(`https://beyond-hd\.me/torrent/download/[^\"'\s]+?\.(\d+)\.`)
	bhdInvalidIMDbPattern = regexp.MustCompile(`(?i)^invalid imdb_id`)
)

type uploadState struct {
	torrentPath string
	mediaDump   string
	description string
	fields      map[string]string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}

	response, responseBody, err := sendUpload(ctx, req, state)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if response.StatusCode == 0 && bhdInvalidIMDbPattern.MatchString(response.StatusMessage) {
		state.fields["imdb_id"] = "1"
		response, responseBody, err = sendUpload(ctx, req, state)
		if err != nil {
			return api.UploadSummary{}, err
		}
	}
	if response.StatusCode == 0 {
		artifactPath, artifactErr := writeFailureArtifact(req, responseBody, "upload_failure")
		if artifactErr != nil && req.Logger != nil {
			req.Logger.Warnf("trackers: BHD failure artifact write failed: %v", artifactErr)
		}
		if artifactPath != "" && req.Logger != nil {
			req.Logger.Warnf("trackers: BHD upload failure artifact saved to %s", artifactPath)
		}
		message := commonhttp.ExtractHTTPErrorDetail(responseBody)
		if message == "" {
			message = commonhttp.RedactErrorDetail(response.StatusMessage)
		}
		if message == "" {
			message = "upload failed"
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: BHD api error: %s", message)
	}

	torrentID := extractTorrentID(response.StatusMessage)
	if torrentID == "" {
		return api.UploadSummary{}, errors.New("trackers: BHD upload succeeded but torrent id was not returned")
	}
	torrentURL := strings.TrimRight(bhdBaseURL, "/") + "/details/" + torrentID
	downloadURL := strings.TrimRight(bhdBaseURL, "/") + "/torrent/download/" + torrentID

	artifactPath := ""
	if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "BHD")
		if err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
		}
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, "BHD"); err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
		}
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "BHD",
			TorrentID:   torrentID,
			DownloadURL: downloadURL,
			TorrentURL:  torrentURL,
			TorrentPath: artifactPath,
		}},
	}, nil
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	return api.TrackerDryRunEntry{
		Tracker:          "BHD",
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      state.fields["name"],
		DescriptionGroup: "bhd",
		Description:      state.description,
		Endpoint:         uploadEndpoint(strings.TrimSpace(req.TrackerConfig.APIKey)),
		Payload:          cloneFields(state.fields),
		Files: []api.TrackerDryRunFile{
			{Field: "mediainfo", Path: resolveMediaPath(req.Meta, req.AppConfig.MainSettings.DBPath), Present: strings.TrimSpace(state.mediaDump) != ""},
			{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""},
		},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: BHD missing api_key")
	}
	if req.Meta.ExternalIDs.TMDBID == 0 {
		return uploadState{}, errors.New("trackers: BHD missing tmdb id")
	}
	if err := validateBHDContainer(req.Meta); err != nil {
		return uploadState{}, err
	}

	var err error
	var assets trackers.DescriptionAssets
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
		if err != nil {
			trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	description := buildDescription(req.Meta, req.AppConfig, assets)
	mediaDump, err := resolveMediaDump(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, err
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}

	tags := resolveTags(req.Meta)
	customEdition, edition := resolveEdition(req.Meta, tags)
	fields := map[string]string{
		"name":        resolveUploadName(req.Meta),
		"category_id": resolveCategoryID(req.Meta),
		"type":        resolveType(req.Meta),
		"source":      resolveSource(req.Meta.Source),
		"imdb_id":     resolveIMDbID(req.Meta),
		"tmdb_id":     strconv.Itoa(req.Meta.ExternalIDs.TMDBID),
		"description": description,
		"anon":        resolveAnon(req.TrackerConfig),
		"sd":          boolFlag(isSD(req.Meta)),
		"live":        resolveLive(req.TrackerConfig),
	}
	if trackers.IsInternalGroup(req.AppConfig, "BHD", req.Meta) {
		fields["internal"] = "1"
	}
	if req.Meta.TVPack {
		fields["pack"] = "1"
	}
	if strings.EqualFold(strings.TrimSpace(req.Meta.SeasonStr), "S00") {
		fields["special"] = "1"
	}
	if region := resolveRegion(req.Meta.Region); region != "" {
		fields["region"] = region
	}
	if customEdition {
		fields["custom_edition"] = edition
	} else if edition != "" {
		fields["edition"] = edition
	}
	if len(tags) > 0 {
		fields["tags"] = strings.Join(tags, ",")
	}

	return uploadState{
		torrentPath: torrentPath,
		mediaDump:   mediaDump,
		description: description,
		fields:      fields,
	}, nil
}

func sendUpload(ctx context.Context, req trackers.UploadRequest, state uploadState) (uploadResponse, []byte, error) {
	body, contentType, err := buildMultipartPayload(state.fields, state.mediaDump, state.torrentPath)
	if err != nil {
		return uploadResponse{}, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadEndpoint(strings.TrimSpace(req.TrackerConfig.APIKey)), bytes.NewReader(body))
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHD request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHD upload request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHD read response: %w", err)
	}
	decoded := uploadResponse{}
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			return uploadResponse{}, responseBody, fmt.Errorf("trackers: BHD decode response: %w", err)
		}
	}
	return decoded, responseBody, nil
}

type uploadResponse struct {
	StatusCode    int    `json:"status_code"`
	StatusMessage string `json:"status_message"`
}

func buildMultipartPayload(fields map[string]string, mediaDump string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: BHD write multipart field %q: %w", key, err)
		}
	}
	if err := writer.WriteField("mediainfo", mediaDump); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHD write multipart field %q: %w", "mediainfo", err)
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHD open torrent file: %w", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file", "torrent.torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHD create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHD copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: BHD close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveMediaDump(meta api.PreparedMetadata, dbPath string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text := readBDInfoNoErr(dbPath, meta)
		if text == "" {
			return "", errors.New("trackers: BHD missing BDInfo text")
		}
		return text, nil
	case "DVD":
		text := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
		if text == "" {
			return "", errors.New("trackers: BHD missing DVD MediaInfo text")
		}
		return text, nil
	default:
		if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
			return "", errors.New("trackers: BHD missing mediainfo text")
		}
		payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", fmt.Errorf("trackers: BHD read mediainfo: %w", err)
		}
		return string(payload), nil
	}
}

func resolveMediaPath(meta api.PreparedMetadata, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
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
		return paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta))
	default:
		return strings.TrimSpace(meta.MediaInfoTextPath)
	}
}

func uploadEndpoint(apiKey string) string {
	return strings.TrimRight(bhdBaseURL, "/") + "/api/upload/" + strings.TrimSpace(apiKey)
}

func extractTorrentID(statusMessage string) string {
	matches := bhdTorrentIDPattern.FindStringSubmatch(strings.TrimSpace(statusMessage))
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func writeFailureArtifact(req trackers.UploadRequest, payload []byte, name string) (string, error) {
	if strings.TrimSpace(req.AppConfig.MainSettings.DBPath) == "" || strings.TrimSpace(req.Meta.SourcePath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(req.AppConfig.MainSettings.DBPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, req.Meta, req.Meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	ext := ".txt"
	if bytes.Contains(bytes.ToLower(payload), []byte("<html")) {
		ext = ".html"
	}
	path := filepath.Join(tmpDir, "[BHD]"+name+ext)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("trackers: BHD create failure artifact dir: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", fmt.Errorf("trackers: BHD write failure artifact: %w", err)
	}
	return path, nil
}

func resolveUploadName(meta api.PreparedMetadata) string {
	name := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ReleaseName), strings.TrimSpace(meta.ReleaseNameNoTag), strings.TrimSpace(meta.Filename), pathutil.Base(meta.SourcePath))
	if isDVDSource(meta.Source) {
		audio := strings.Join(strings.Fields(strings.TrimSpace(meta.Audio)), " ")
		if audio != "" && strings.TrimSpace(meta.VideoCodec) != "" {
			name = strings.Replace(name, audio, strings.TrimSpace(meta.VideoCodec)+" "+audio, 1)
		}
	}
	return strings.ReplaceAll(name, "DD+", "DDP")
}

func resolveCategoryID(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "TV") || strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "TV") {
		return "2"
	}
	return "1"
}

func validateBHDContainer(meta api.PreparedMetadata) error {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "REMUX", "ENCODE", "WEBDL", "WEBRIP":
		container := strings.ToLower(strings.TrimSpace(meta.Container))
		if container != "" && container != "mkv" && container != "mp4" {
			return fmt.Errorf("trackers: BHD container %q is not allowed for %s: only MKV and MP4 are permitted", meta.Container, meta.Type)
		}
	}
	return nil
}

func resolveSource(source string) string {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "BLURAY", "BLU-RAY":
		return "Blu-ray"
	case "HDDVD", "HD DVD":
		return "HD-DVD"
	case "WEB", "WEB-DL", "WEBDL", "WEBRIP":
		return "WEB"
	case "HDTV", "UHDTV":
		return "HDTV"
	case "NTSC", "PAL", "NTSC DVD", "PAL DVD", "DVD":
		return "DVD"
	default:
		return strings.TrimSpace(source)
	}
}

func resolveType(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		size := 100
		for _, candidate := range []int{25, 50, 66, 100} {
			if meta.SourceSize > 0 && meta.SourceSize < int64(candidate)<<30 {
				size = candidate
				break
			}
		}
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && size != 25 {
			if size == 50 || size == 66 || size == 100 {
				return fmt.Sprintf("UHD %d", size)
			}
			return "Other"
		}
		if size == 25 || size == 50 {
			return fmt.Sprintf("BD %d", size)
		}
		return "Other"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		upper := strings.ToUpper(strings.TrimSpace(meta.Release.Size))
		switch {
		case strings.Contains(upper, "DVD5"):
			return "DVD 5"
		case strings.Contains(upper, "DVD9"):
			return "DVD 9"
		default:
			return "Other"
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		switch {
		case strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD"):
			return "UHD Remux"
		case isDVDSource(meta.Source):
			return "DVD Remux"
		case strings.EqualFold(strings.TrimSpace(meta.Source), "BluRay"), strings.EqualFold(strings.TrimSpace(meta.Source), "Blu-ray"):
			return "BD Remux"
		default:
			return "Other"
		}
	}
	resolution := normalizeResolution(meta.Release.Resolution)
	switch resolution {
	case "2160p", "1080p", "1080i", "720p", "576p", "576i", "540p", "480p":
		return resolution
	default:
		return "Other"
	}
}

func resolveEdition(meta api.PreparedMetadata, tags []string) (bool, string) {
	edition := strings.TrimSpace(meta.Edition)
	for _, tag := range tags {
		if tag == "Hybrid" {
			edition = strings.TrimSpace(strings.ReplaceAll(edition, "Hybrid", ""))
			break
		}
	}
	if edition == "" {
		return false, ""
	}
	for _, token := range []string{"collector", "director", "extended", "limited", "special", "theatrical", "uncut", "unrated"} {
		if strings.Contains(strings.ToLower(edition), token) {
			switch token {
			case "director":
				return false, "director"
			default:
				return false, token
			}
		}
	}
	return true, edition
}

func resolveTags(meta api.PreparedMetadata) []string {
	tags := make([]string, 0, 12)
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "WEBRIP":
		tags = append(tags, "WEBRip")
	case "WEBDL", "WEB-DL":
		tags = append(tags, "WEBDL")
	}
	if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
		tags = append(tags, "3D")
	}
	audio := strings.ToLower(strings.TrimSpace(meta.Audio))
	if strings.Contains(audio, "dual-audio") {
		tags = append(tags, "DualAudio")
	}
	if strings.Contains(audio, "dubbed") {
		tags = append(tags, "EnglishDub")
	}
	if strings.Contains(strings.ToLower(meta.Edition), "open matte") {
		tags = append(tags, "OpenMatte")
	}
	if meta.Scene {
		tags = append(tags, "Scene")
	}
	if meta.PersonalRelease {
		tags = append(tags, "Personal")
	}
	if strings.Contains(strings.ToLower(meta.Edition), "hybrid") {
		tags = append(tags, "Hybrid")
	}
	if meta.HasCommentary {
		tags = append(tags, "Commentary")
	}
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	if strings.Contains(hdr, "DV") {
		tags = append(tags, "DV")
	}
	if strings.Contains(hdr, "HDR") {
		if strings.Contains(hdr, "HDR10+") {
			tags = append(tags, "HDR10+")
		} else {
			tags = append(tags, "HDR10")
		}
	}
	if strings.Contains(hdr, "HLG") {
		tags = append(tags, "HLG")
	}
	return dedupeStrings(tags)
}

func resolveIMDbID(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID > 0 {
		return strconv.Itoa(meta.ExternalIDs.IMDBID)
	}
	return "1"
}

func resolveAnon(cfg config.TrackerConfig) string {
	if cfg.Anon {
		return "1"
	}
	return "0"
}

func resolveLive(cfg config.TrackerConfig) string {
	if cfg.DraftDefault || cfg.Draft {
		return "0"
	}
	return "1"
}

func resolveRegion(region string) string {
	allowed := map[string]struct{}{
		"AUS": {}, "CAN": {}, "CEE": {}, "CHN": {}, "ESP": {}, "EUR": {}, "FRA": {}, "GBR": {},
		"GER": {}, "HKG": {}, "ITA": {}, "JPN": {}, "KOR": {}, "NOR": {}, "NLD": {}, "RUS": {},
		"TWN": {}, "USA": {},
	}
	upper := strings.ToUpper(strings.TrimSpace(region))
	if _, ok := allowed[upper]; ok {
		return upper
	}
	return ""
}

func isSD(meta api.PreparedMetadata) bool {
	resolution := normalizeResolution(meta.Release.Resolution)
	return strings.Contains(resolution, "480") || strings.Contains(resolution, "540") || strings.Contains(resolution, "576")
}

func normalizeResolution(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isDVDSource(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	return upper == "PAL DVD" || upper == "NTSC DVD" || upper == "DVD" || upper == "PAL" || upper == "NTSC"
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func cloneFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
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

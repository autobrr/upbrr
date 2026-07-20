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
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	bhdBaseURL            = "https://beyond-hd.me"
	bhdTorrentIDPattern   = regexp.MustCompile(`https://beyond-hd\.me/torrent/download/[^\"'\s]+?\.(\d+)\.`)
	bhdInvalidIMDbPattern = regexp.MustCompile(`(?i)^invalid imdb_id`)
)

const bhdUploadResponseMaxBytes = commonhttp.DefaultResponsePreviewBytes

type uploadState struct {
	torrentPath string
	mediaDump   string
	description string
	fields      map[string]string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state, req)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	body, contentType, err := buildMultipartPayload(state.fields, state.mediaDump, state.torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	fallbackFields := maps.Clone(state.fields)
	fallbackFields["imdb_id"] = "1"
	fallbackBody, fallbackContentType, err := buildMultipartPayload(fallbackFields, state.mediaDump, state.torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	endpoint := uploadEndpoint(strings.TrimSpace(req.TrackerConfig.APIKey))
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "BHD")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(
			submitCtx, req, state, endpoint, body, contentType, fallbackBody, fallbackContentType, announceURL, artifactPath,
		)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	endpoint string,
	body []byte,
	contentType string,
	fallbackBody []byte,
	fallbackContentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	response, responseBody, err := sendPreparedUpload(ctx, endpoint, body, contentType)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if response.StatusCode == 0 && bhdInvalidIMDbPattern.MatchString(response.StatusMessage) {
		response, responseBody, err = sendPreparedUpload(ctx, endpoint, fallbackBody, fallbackContentType)
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

	if announceURL != "" {
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

func buildUploadPreview(state uploadState, req trackers.PreparationInput) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "BHD",
		ReleaseName:      state.fields["name"],
		DescriptionGroup: "bhd",
		Description:      state.description,
		Endpoint:         strings.TrimRight(bhdBaseURL, "/") + "/api/upload/[redacted]",
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{
			{
				Field:   "mediainfo",
				Path:    resolveMediaPath(req.Meta, req.Runtime.DBPath),
				Present: strings.TrimSpace(state.mediaDump) != "",
			},
			{
				Field:   "file",
				Path:    state.torrentPath,
				Present: strings.TrimSpace(state.torrentPath) != "",
			},
		},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: BHD missing api_key; configure the BHD api_key in tracker settings before uploading")
	}
	if req.Meta.Identity.TMDBID == 0 {
		return uploadState{}, errors.New("trackers: BHD missing tmdb id; refresh metadata or set a TMDB id before uploading")
	}
	var err error
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
	description := buildDescription(req.Meta, req.Runtime.DescriptionConfig(), assets)
	mediaDump, err := resolveMediaDump(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, err
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}

	tags := resolveTags(req.Meta)
	customEdition, edition := resolveEdition(req.Meta, tags)
	source, ok := resolveSource(req.Meta)
	if !ok {
		return uploadState{}, fmt.Errorf("trackers: BHD unsupported source %q", req.Meta.Source)
	}
	fields := map[string]string{
		"name":        resolveUploadName(req.Meta),
		"category_id": resolveCategoryID(req.Meta),
		"type":        resolveType(req.Meta),
		"source":      source,
		"imdb_id":     resolveIMDbID(req.Meta),
		"tmdb_id":     resolveTMDBID(req.Meta),
		"description": description,
		"anon":        resolveAnon(req.TrackerConfig),
		"sd":          boolFlag(isSD(req.Meta)),
		"live":        resolveLive(req.TrackerConfig),
	}
	if req.Runtime.Internal {
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

func sendPreparedUpload(ctx context.Context, endpoint string, body []byte, contentType string) (uploadResponse, []byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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

	responseBody, err := readUploadResponseBody(resp.Body)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHD read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return uploadResponse{}, responseBody, commonhttp.UploadHTTPError("BHD", resp.StatusCode, responseBody)
	}
	decoded := uploadResponse{}
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			return uploadResponse{}, responseBody, fmt.Errorf("trackers: BHD decode response: %w", err)
		}
	}
	return decoded, responseBody, nil
}

func readUploadResponseBody(r io.Reader) ([]byte, error) {
	responseBody, err := io.ReadAll(io.LimitReader(r, bhdUploadResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read upload response body: %w", err)
	}
	if int64(len(responseBody)) > bhdUploadResponseMaxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", bhdUploadResponseMaxBytes)
	}
	return responseBody, nil
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
	part, err := writer.CreateFormFile("mediainfo", "upload")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHD create mediainfo form file: %w", err)
	}
	if _, err := io.WriteString(part, mediaDump); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHD write mediainfo form file: %w", err)
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHD open torrent file: %w", err)
	}
	defer file.Close()
	part, err = writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="torrent.torrent"`},
		"Content-Type":        {"application/x-bittorrent"},
	})
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

func resolveMediaDump(meta api.UploadSubject, dbPath string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text := readBDInfoNoErr(dbPath, meta)
		if text == "" {
			return "", errors.New("trackers: BHD missing BDInfo text; generate or attach BDInfo before uploading")
		}
		return text, nil
	case "DVD":
		text := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
		if text == "" {
			return "", errors.New("trackers: BHD missing DVD MediaInfo text; generate or attach DVD MediaInfo before uploading")
		}
		return text, nil
	default:
		if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
			return "", errors.New("trackers: BHD missing mediainfo text; generate or attach MediaInfo before uploading")
		}
		payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", fmt.Errorf("trackers: BHD read mediainfo: %w", err)
		}
		return string(payload), nil
	}
}

func resolveMediaPath(meta api.UploadSubject, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
			return ""
		}
		tmpRoot, err := db.Subdir(dbPath, "tmp")
		if err != nil {
			return ""
		}
		tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
		if err != nil {
			return ""
		}
		return paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists))
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

func writeFailureArtifact(req trackers.PreparationInput, payload []byte, name string) (string, error) {
	if strings.TrimSpace(req.Runtime.DBPath) == "" || strings.TrimSpace(req.Meta.SourcePath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(req.Runtime.DBPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, req.Meta.SourcePath, req.Meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	ext := ".txt"
	if bytes.Contains(bytes.ToLower(payload), []byte("<html")) {
		ext = ".html"
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", fmt.Errorf("trackers: BHD create failure artifact dir: %w", err)
	}
	payload = []byte(redaction.RedactValue(string(payload), nil))
	file, err := os.CreateTemp(tmpDir, "[BHD]"+name+"-*"+ext)
	if err != nil {
		return "", fmt.Errorf("trackers: BHD create failure artifact: %w", err)
	}
	path := file.Name()
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("trackers: BHD write failure artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("trackers: BHD close failure artifact: %w", err)
	}
	return path, nil
}

func resolveUploadName(meta api.UploadSubject) string {
	name := metautil.FirstNonEmptyTrimmed(
		strings.TrimSpace(meta.ReleaseName),
		strings.TrimSpace(meta.ReleaseNameNoTag),
		strings.TrimSpace(meta.Filename),
		pathutil.Base(meta.SourcePath),
	)
	if IsDVDSource(meta.Source) {
		audio := strings.Join(strings.Fields(strings.TrimSpace(meta.Audio)), " ")
		if audio != "" && strings.TrimSpace(meta.VideoCodec) != "" {
			name = strings.Replace(name, audio, strings.TrimSpace(meta.VideoCodec)+" "+audio, 1)
		}
	}
	return strings.ReplaceAll(name, "DD+", "DDP")
}

func resolveCategoryID(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), "TV") {
		return "2"
	}
	return "1"
}

func resolveTMDBID(meta api.UploadSubject) string {
	if meta.Identity.TMDBID == 0 {
		return ""
	}
	return strconv.Itoa(meta.Identity.TMDBID)
}

func resolveSource(meta api.UploadSubject) (string, bool) {
	return SourceForMetadata(meta)
}

func resolveType(meta api.UploadSubject) string {
	return Type(meta)
}

func resolveEdition(meta api.UploadSubject, tags []string) (bool, string) {
	edition := strings.TrimSpace(meta.Edition)
	if slices.Contains(tags, "Hybrid") {
		edition = strings.TrimSpace(strings.ReplaceAll(edition, "Hybrid", ""))
	}
	if edition == "" {
		return false, ""
	}
	for _, token := range []string{"collector", "director", "extended", "limited", "special", "theatrical", "uncut", "unrated"} {
		if strings.Contains(strings.ToLower(edition), token) {
			switch token {
			case "director":
				return false, "Director"
			default:
				return false, strings.ToUpper(token[:1]) + token[1:]
			}
		}
	}
	return true, edition
}

func resolveTags(meta api.UploadSubject) []string {
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

func resolveIMDbID(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return strconv.Itoa(meta.Identity.IMDBID)
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
		"AUS": {},
		"CAN": {},
		"CEE": {},
		"CHN": {},
		"ESP": {},
		"EUR": {},
		"FRA": {},
		"GBR": {},
		"GER": {},
		"HKG": {},
		"ITA": {},
		"JPN": {},
		"KOR": {},
		"NOR": {},
		"NLD": {},
		"RUS": {},
		"TWN": {},
		"USA": {},
	}
	upper := strings.ToUpper(strings.TrimSpace(region))
	if _, ok := allowed[upper]; ok {
		return upper
	}
	return ""
}

func isSD(meta api.UploadSubject) bool {
	return IsSD(meta)
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
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

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package czt implements uploads to CZTeam (CZT) via its dedicated JSON
// endpoint takeupload_api.php.
//
// Unlike most impls in this repo CZTeam is not a UNIT3D site and does not need a
// cookie jar: a single Authorization: Bearer <token> header (bot/admin service
// accounts) or a passkey form field (regular users) authenticates the multipart
// POST. The endpoint returns the registered .torrent inline as base64, already
// personalized with the uploader's announce passkey and source=CzT.
package czt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	trackerName    = "CZT"
	descGroup      = "czt"
	defaultBaseURL = "https://czteam.me"
	uploadPath     = "/takeupload_api.php"
	uploadTimeout  = 120 * time.Second
)

// uploadResponse mirrors the JSON returned by takeupload_api.php. A 201 carries
// the full set; a 409 duplicate still returns id/name/download_url/torrent_b64.
type uploadResponse struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	InfoHash    string `json:"infohash"`
	DownloadURL string `json:"download_url"`
	TorrentB64  string `json:"torrent_b64"`
	Error       string `json:"error"`
}

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	files         []commonhttp.FileField
	endpoint      string
	baseURL       string
	token         string
	questionnaire *api.TrackerQuestionnaire
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req, true)
	if err != nil {
		return api.UploadSummary{}, err
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, state.files)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.endpoint, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	if state.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+state.token)
	}

	client := &http.Client{Timeout: uploadTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT read upload response: %w", err)
	}

	// Only a fresh 201 with a torrent id is a successful upload. A 409 means the
	// release name already exists; surface it as an error (the response still
	// carries the existing torrent for callers who want to cross-seed).
	if resp.StatusCode != http.StatusCreated {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, trackerName, "upload_failure", responseBody, ".json")
		return api.UploadSummary{}, commonhttp.UploadHTTPError(trackerName, resp.StatusCode, responseBody)
	}

	var parsed uploadResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT parse upload response: %w", err)
	}
	if parsed.ID <= 0 {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, trackerName, "upload_failure", responseBody, ".json")
		return api.UploadSummary{}, commonhttp.UploadHTTPError(trackerName, resp.StatusCode, responseBody)
	}

	torrentID := strconv.Itoa(parsed.ID)
	torrentURL := state.baseURL + "/details.php?id=" + torrentID
	downloadURL, err := joinCZTURL(state.baseURL, parsed.DownloadURL)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT upload response download_url: %w", err)
	}

	// The endpoint returns the registered .torrent inline (base64), already
	// personalized with the uploader's announce passkey and source=CzT, so we
	// persist that directly rather than re-deriving an announce URL.
	artifactPath, err := persistReturnedTorrent(req, parsed.TorrentB64)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: CZT persist returned torrent: %w", err)
	}

	return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
		Tracker:     trackerName,
		TorrentID:   torrentID,
		TorrentURL:  torrentURL,
		DownloadURL: downloadURL,
		TorrentPath: artifactPath,
	}}}, nil
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	status := "ready"
	message := "dry-run payload generated"
	if missingRequiredCategory(state) {
		status = "blocked"
		message = "answer the category questionnaire to continue"
	}
	return api.TrackerDryRunEntry{
		Tracker:          trackerName,
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: descGroup,
		Description:      state.description,
		Endpoint:         state.endpoint,
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, requireCategory bool) (uploadState, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}

	assets := uploadDescriptionAssets(ctx, req)

	// CZTeam stores two description fields separately: `descr` holds the raw
	// MediaInfo/BDInfo dump, and `user_descr` holds the free-form BBCode body
	// (user notes + screenshot images).
	mediaInfo := buildMediaInfo(req)
	userDescr := buildDescription(req, assets)
	releaseName := resolveName(req.Meta)
	baseURL := resolveBaseURL(req.TrackerConfig)

	category, err := resolveCategory(req.Meta)
	if err != nil {
		if requireCategory {
			return uploadState{}, err
		}
		category = ""
	}

	fields := map[string]string{
		"name": releaseName,
	}
	if category != "" {
		fields["category"] = category
	}
	if strings.TrimSpace(mediaInfo) != "" {
		fields["descr"] = mediaInfo
	}
	if strings.TrimSpace(userDescr) != "" {
		fields["user_descr"] = userDescr
	}
	if imdb := imdbID(req.Meta); imdb != "" {
		fields["imdb_id"] = imdb
	}
	// resolution/codec/container/source are validated server-side against the
	// tracker's allowed value set; unknown values are dropped, not rejected.
	if res := strings.TrimSpace(req.Meta.Release.Resolution); res != "" {
		fields["resolution"] = res
	}
	if codec := firstCodec(req.Meta); codec != "" {
		fields["codec"] = codec
	}
	if container := strings.TrimSpace(req.Meta.Container); container != "" {
		fields["container"] = container
	}
	if source := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(req.Meta.Source, req.Meta.Release.Source)); source != "" {
		fields["source"] = source
	}

	token := strings.TrimSpace(req.TrackerConfig.APIKey)
	// Passkey auth is the documented path for regular users (the Bearer token is
	// for bot/admin service accounts); both target the same endpoint.
	if token == "" {
		if passkey := strings.TrimSpace(req.TrackerConfig.Passkey); passkey != "" {
			fields["passkey"] = passkey
		}
	}

	return uploadState{
		torrentPath: torrentPath,
		description: userDescr,
		releaseName: releaseName,
		fields:      fields,
		files: []commonhttp.FileField{{
			FieldName: "file",
			FileName:  releaseName + ".torrent",
			Path:      torrentPath,
		}},
		endpoint:      baseURL + uploadPath,
		baseURL:       baseURL,
		token:         token,
		questionnaire: categoryQuestionnaire(req.Meta),
	}, nil
}

// uploadDescriptionAssets uses caller-prepared assets when available, falling
// back to local resolution and an empty asset set on resolution failure.
func uploadDescriptionAssets(ctx context.Context, req trackers.UploadRequest) trackers.DescriptionAssets {
	if req.Assets != nil {
		return *req.Assets
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		return trackers.DescriptionAssets{}
	}
	return assets
}

// persistReturnedTorrent decodes the tracker-returned base64 torrent, verifies
// it parses as metainfo, and writes the registered torrent artifact with user
// read/write permissions only.
func persistReturnedTorrent(req trackers.UploadRequest, b64 string) (string, error) {
	if strings.TrimSpace(b64) == "" {
		return "", errors.New("empty torrent_b64")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("decode torrent_b64: %w", err)
	}
	if len(decoded) == 0 {
		return "", errors.New("decoded torrent_b64 is empty")
	}
	torrentMeta, err := metainfo.Load(bytes.NewReader(decoded))
	if err != nil {
		return "", fmt.Errorf("load returned torrent: %w", err)
	}
	if _, err := torrentMeta.UnmarshalInfo(); err != nil {
		return "", fmt.Errorf("unmarshal returned torrent info: %w", err)
	}
	path, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, trackerName)
	if err != nil {
		return "", fmt.Errorf("resolve returned torrent path: %w", err)
	}
	if err := os.WriteFile(path, decoded, 0o600); err != nil {
		return "", fmt.Errorf("write returned torrent: %w", err)
	}
	return path, nil
}

// buildMediaInfo returns the raw MediaInfo/BDInfo text for the CZTeam `descr`
// field.
func buildMediaInfo(req trackers.UploadRequest) string {
	return strings.TrimSpace(trackers.ReadBDinfoOrMediaInfo(req.AppConfig.MainSettings.DBPath, req.Meta))
}

// buildDescription assembles the CZTeam `user_descr` body: the (possibly
// user-edited) description text followed by a BBCode screenshot block. Kept as a
// separate function so definition.BuildDescription can drive the description
// builder UI with the same output.
func buildDescription(_ trackers.UploadRequest, assets trackers.DescriptionAssets) string {
	// A "final" description is the already-assembled body (saved override or
	// canonical group description) with screenshots embedded; the resolver does
	// not clear assets.Screenshots here, so re-appending would duplicate them.
	// Use it verbatim, matching the assets.Final convention other impls follow.
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	parts := make([]string, 0, 2)
	if body := strings.TrimSpace(assets.Description); body != "" {
		parts = append(parts, body)
	}
	if shots := bbcodeScreenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// bbcodeScreenshotBlock renders uploaded screenshots as a centered BBCode block
// (CZTeam's comment formatter supports [img]/[url]). Screenshots without an
// uploaded image URL are skipped.
func bbcodeScreenshotBlock(images []api.ScreenshotImage) string {
	var b strings.Builder
	count := 0
	for _, image := range images {
		img := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL))
		if img == "" {
			continue
		}
		web := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.WebURL, img))
		b.WriteString("[url=" + web + "][img]" + img + "[/img][/url]")
		count++
	}
	if count == 0 {
		return ""
	}
	return "[center]" + b.String() + "[/center]"
}

func resolveBaseURL(cfg config.TrackerConfig) string {
	if value := strings.TrimSpace(cfg.URL); value != "" {
		return strings.TrimRight(value, "/")
	}
	return defaultBaseURL
}

// joinCZTURL resolves a tracker-provided download URL against the configured
// CZTeam base URL and rejects empty, non-addressable, or cross-host results.
func joinCZTURL(baseURL string, rawRef string) (string, error) {
	trimmedRef := strings.TrimSpace(rawRef)
	if trimmedRef == "" {
		return "", errors.New("empty URL")
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/")
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", errors.New("base URL must be absolute")
	}
	ref, err := url.Parse(trimmedRef)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme == "" || resolved.Host == "" {
		return "", errors.New("resolved URL must be absolute")
	}
	if !strings.EqualFold(resolved.Scheme, base.Scheme) || !strings.EqualFold(resolved.Host, base.Host) {
		return "", errors.New("resolved URL must stay on configured CZT host")
	}
	if strings.TrimSpace(resolved.Path) == "" && strings.TrimSpace(resolved.RawQuery) == "" {
		return "", errors.New("resolved URL has no path or query")
	}
	return resolved.String(), nil
}

func resolveName(meta api.PreparedMetadata) string {
	if name := strings.TrimSpace(meta.SceneName); name != "" {
		return name
	}
	return strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename))
}

// cztCategory pairs a CZTeam categories.id with its display name.
type cztCategory struct {
	id   string
	name string
}

// cztCategories lists CZTeam upload categories for the upload-time override
// dropdown. upbrr auto-detects only video categories from metadata; everything
// else (software, games, music, XXX, images, docs, …) is chosen here.
var cztCategories = []cztCategory{
	{"1", "XxX"},
	{"4", "Games/PC ISO"},
	{"5", "TvEps/HD"},
	{"6", "Music/Audio"},
	{"7", "TvEps"},
	{"9", "Mobile"},
	{"12", "Games/Consoles"},
	{"19", "Movies/XviD"},
	{"20", "Movies/DVD-R"},
	{"21", "Games/PC Rips"},
	{"22", "Software"},
	{"23", "Anime"},
	{"24", "Images"},
	{"25", "Docs"},
	{"28", "Movies/DVD-RO"},
	{"29", "Movies/HD"},
	{"30", "Music/MVID"},
	{"31", "MAC"},
	{"32", "Sports"},
	{"33", "Movies/HDTV-RO"},
	{"34", "TvEps/HD-RO"},
	{"35", "Music/Lossless"},
	{"36", "Full BluRay-RO"},
	{"37", "Movies/3D"},
	{"38", "Movies-RO"},
}

func categoryNames() []string {
	out := make([]string, 0, len(cztCategories))
	for _, c := range cztCategories {
		out = append(out, c.name)
	}
	return out
}

func categoryIDForName(name string) string {
	name = strings.TrimSpace(name)
	for _, c := range cztCategories {
		if strings.EqualFold(c.name, name) {
			return c.id
		}
	}
	return ""
}

func categoryNameForID(id string) string {
	for _, c := range cztCategories {
		if c.id == id {
			return c.name
		}
	}
	return ""
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers[trackerName]
}

// categoryQuestionnaire offers a (non-blocking) category dropdown pre-filled
// with the auto-detected category, so the user can override it for content
// upbrr can't classify from video metadata.
func categoryQuestionnaire(meta api.PreparedMetadata) *api.TrackerQuestionnaire {
	auto := autoCategory(meta)
	return &api.TrackerQuestionnaire{
		Tracker: trackerName,
		Fields: []api.TrackerQuestionnaireField{{
			Key:      "category",
			Label:    "Category",
			Kind:     "select",
			Options:  categoryNames(),
			Value:    categoryNameForID(auto),
			Help:     "Auto-detected from video metadata. Override for software, games, music, XXX, etc.",
			Required: auto == "",
		}},
	}
}

// resolveCategory returns the CZTeam category id: an explicit questionnaire
// override when the user picked one, otherwise the auto-detected video category.
func resolveCategory(meta api.PreparedMetadata) (string, error) {
	if id := resolveQuestionnaireCategory(questionnaireAnswers(meta)["category"]); id != "" {
		return id, nil
	}
	if id := autoCategory(meta); id != "" {
		return id, nil
	}
	return "", errors.New("trackers: CZT category requires explicit questionnaire selection for non-video content")
}

func resolveQuestionnaireCategory(value string) string {
	if id := categoryIDForName(value); id != "" {
		return id
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, c := range cztCategories {
		if c.id == trimmed {
			return c.id
		}
	}
	return ""
}

// autoCategory maps prepared metadata to a CZTeam numeric categories.id when
// metadata supports automatic classification. Unknown non-video content returns
// empty so callers can require an explicit questionnaire category instead of
// falling back to a movie bucket.
func autoCategory(meta api.PreparedMetadata) string {
	if id, ok := nonVideoCategory(meta); ok {
		return id
	}

	ro := hasRomanianSubs(meta)
	hd := isHD(meta.Release.Resolution)

	switch {
	case meta.Anime:
		return "23" // Anime
	case isTV(meta):
		if hd && ro {
			return "34" // TvEps/HD-RO
		}
		if hd {
			return "5" // TvEps/HD
		}
		return "7" // TvEps (no SD-RO TV category exists)
	}

	// Movies.
	src := strings.ToUpper(meta.Source)
	isDVD := strings.Contains(src, "DVD") || strings.EqualFold(meta.DiscType, "DVD") || strings.EqualFold(meta.Type, "DVDRIP")
	isFullBluRay := strings.EqualFold(meta.DiscType, "BDMV") ||
		(strings.EqualFold(meta.Type, "REMUX") && strings.Contains(src, "BLURAY"))

	if ro {
		switch {
		case isFullBluRay:
			return "36" // Full BluRay-RO
		case isDVD:
			return "28" // Movies/DVD-RO
		case hd:
			return "33" // Movies/HDTV-RO
		default:
			return "38" // Movies-RO
		}
	}
	switch {
	case isDVD:
		return "20" // Movies/DVD-R
	case hd:
		return "29" // Movies/HD
	case hasCodec(meta, "XviD"):
		return "19" // Movies/XviD
	default:
		return "29" // default to Movies/HD
	}
}

// nonVideoCategory classifies explicit non-video category hints. The boolean
// reports whether a non-video hint was found even when it still requires a
// manual questionnaire answer.
func nonVideoCategory(meta api.PreparedMetadata) (string, bool) {
	hints := []string{
		meta.ExternalIDs.Category,
		meta.MediaInfoCategory,
		meta.Release.Category,
	}
	for _, hint := range hints {
		normalized := normalizeCategoryHint(hint)
		if normalized == "" {
			continue
		}
		if isMusicVideoCategoryHint(normalized) {
			return "30", true
		}
		if isVideoCategoryHint(normalized) {
			continue
		}
		switch {
		case strings.Contains(normalized, "xxx"), strings.Contains(normalized, "adult"):
			return "1", true
		case strings.Contains(normalized, "console"):
			return "12", true
		case strings.Contains(normalized, "game"):
			return "4", true
		case strings.Contains(normalized, "lossless"), strings.Contains(normalized, "flac"):
			return "35", true
		case strings.Contains(normalized, "music"), strings.Contains(normalized, "audio"):
			return "6", true
		case strings.Contains(normalized, "software"), strings.Contains(normalized, "app"):
			return "22", true
		case strings.Contains(normalized, "mobile"), strings.Contains(normalized, "android"), strings.Contains(normalized, "ios"):
			return "9", true
		case strings.Contains(normalized, "image"), strings.Contains(normalized, "photo"):
			return "24", true
		case strings.Contains(normalized, "doc"), strings.Contains(normalized, "book"), strings.Contains(normalized, "ebook"):
			return "25", true
		case strings.Contains(normalized, "mac"):
			return "31", true
		case strings.Contains(normalized, "sport"):
			return "32", true
		default:
			return "", true
		}
	}
	return "", false
}

func missingRequiredCategory(state uploadState) bool {
	if state.questionnaire == nil {
		return false
	}
	if strings.TrimSpace(state.fields["category"]) != "" {
		return false
	}
	for _, field := range state.questionnaire.Fields {
		if field.Key == "category" && field.Required {
			return true
		}
	}
	return false
}

func normalizeCategoryHint(value string) string {
	replacer := strings.NewReplacer("-", " ", "_", " ", ".", " ", "/", " ", "\\", " ", ":", " ")
	return strings.ToLower(strings.TrimSpace(replacer.Replace(value)))
}

func isMusicVideoCategoryHint(value string) bool {
	fields := strings.Fields(value)
	for i, field := range fields {
		if field == "mvid" {
			return true
		}
		if field == "music" && i+1 < len(fields) && fields[i+1] == "video" {
			return true
		}
	}
	compact := strings.Join(fields, "")
	return strings.Contains(compact, "musicvideo") || strings.Contains(compact, "mvid")
}

func isVideoCategoryHint(value string) bool {
	for _, token := range []string{"movie", "film", "tv", "episode", "anime", "video"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func hasRomanianSubs(meta api.PreparedMetadata) bool {
	for _, s := range meta.SubtitleLanguages {
		v := strings.ToLower(strings.TrimSpace(s))
		if v == "ro" || v == "rum" || v == "ron" || strings.HasPrefix(v, "roman") {
			return true
		}
	}
	return false
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}

func isHD(res string) bool {
	res = strings.TrimSpace(res)
	for _, prefix := range []string{"720", "1080", "2160", "4320"} {
		if strings.HasPrefix(res, prefix) {
			return true
		}
	}
	return false
}

func hasCodec(meta api.PreparedMetadata, want string) bool {
	for _, c := range meta.Release.Codec {
		if strings.EqualFold(strings.TrimSpace(c), want) {
			return true
		}
	}
	return false
}

func firstCodec(meta api.PreparedMetadata) string {
	for _, c := range meta.Release.Codec {
		if v := strings.TrimSpace(c); v != "" {
			return v
		}
	}
	return ""
}

func imdbID(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
}

func cloneFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	maps.Copy(out, input)
	return out
}

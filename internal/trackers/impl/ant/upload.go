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
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const antUploadURL = "https://anthelion.me/api.php"

var antTorrentIDPattern = regexp.MustCompile(`id=(\d+)`)
var antDefaultSignaturePattern = regexp.MustCompile(`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`)
var antEmptyURLPattern = regexp.MustCompile(`(?is)\[url=[^\]]*]\s*\[/url\]`)

var antBannedReleaseGroups = map[string]struct{}{
	"3LTON": {}, "4yEo": {}, "ADE": {}, "AFG": {}, "AniHLS": {}, "AnimeRG": {}, "AniURL": {}, "AROMA": {}, "aXXo": {}, "Brrip": {},
	"CHD": {}, "CM8": {}, "CrEwSaDe": {}, "d3g": {}, "DDR": {}, "DNL": {}, "DeadFish": {}, "ELiTE": {}, "eSc": {}, "FaNGDiNG0": {},
	"FGT": {}, "Flights": {}, "FRDS": {}, "FUM": {}, "HAiKU": {}, "HD2DVD": {}, "HDS": {}, "HDTime": {}, "Hi10": {}, "ION10": {},
	"iPlanet": {}, "JIVE": {}, "KiNGDOM": {}, "Leffe": {}, "LiGaS": {}, "LOAD": {}, "MeGusta": {}, "MkvCage": {}, "mHD": {}, "mSD": {},
	"NhaNc3": {}, "nHD": {}, "NOIVTC": {}, "nSD": {}, "Oj": {}, "Ozlem": {}, "PiRaTeS": {}, "PRoDJi": {}, "RAPiDCOWS": {}, "RARBG": {},
	"RetroPeeps": {}, "RDN": {}, "REsuRRecTioN": {}, "RMTeam": {}, "SANTi": {}, "SicFoI": {}, "SPASM": {}, "SPDVD": {}, "STUTTERSHIT": {}, "TBS": {},
	"Telly": {}, "TM": {}, "UPiNSMOKE": {}, "URANiME": {}, "WAF": {}, "xRed": {}, "XS": {}, "YIFY": {}, "YTS": {}, "Zeus": {}, "ZKBL": {}, "ZmN": {}, "ZMNT": {},
}

type uploadState struct {
	torrentPath  string
	description  string
	fields       map[string]string
	adultContent bool
	manualTags   bool
	typeName     string
	tags         string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return api.UploadSummary{}, err
	}
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
			return api.UploadSummary{}, errors.New("trackers: ANT json decode error, the API is probably down")
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

	artifactPath := ""
	if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "ANT")
		if err != nil {
			return api.UploadSummary{}, err
		}
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, viewURL, "ANT"); err != nil {
			return api.UploadSummary{}, err
		}
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "ANT",
			TorrentID:   torrentID,
			DownloadURL: viewURL,
			TorrentURL:  viewURL,
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
		Tracker:          "ANT",
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      resolveUploadName(req.Meta),
		DescriptionGroup: "ant",
		Description:      state.description,
		Endpoint:         antUploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
		Questionnaire: buildQuestionnaire(req.Meta, state),
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, ctx.Err()
	default:
	}
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: ANT missing api_key")
	}
	if req.Meta.ExternalIDs.TMDBID == 0 {
		return uploadState{}, errors.New("trackers: ANT missing tmdb id")
	}

	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, err
	}
	descriptionAssets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		descriptionAssets = trackers.DescriptionAssets{}
	}
	description, err := buildDescription(req, descriptionAssets)
	if err != nil {
		return uploadState{}, err
	}

	answers := questionnaireAnswers(req.Meta)
	typeName, typeID := resolveType(req.Meta, answers)
	audio := resolveAudioFormat(req.Meta)
	flags := resolveFlags(req.Meta)
	tags, manualTags := resolveTags(req.Meta, answers)
	adultContent := detectAdult(req.Meta)
	safeScreens := resolveAdultScreensAllowed(answers, adultContent)
	screenshots := resolveScreenshotPayload(descriptionAssets.Screenshots, safeScreens)
	mediaInfo, err := resolveMediaInfo(req.Meta)
	if err != nil {
		return uploadState{}, err
	}

	fields := map[string]string{
		"api_key":      strings.TrimSpace(req.TrackerConfig.APIKey),
		"action":       "upload",
		"tmdbid":       strconv.Itoa(req.Meta.ExternalIDs.TMDBID),
		"type":         strconv.Itoa(typeID),
		"audioformat":  audio,
		"mediainfo":    mediaInfo,
		"release_desc": description,
		"screenshots":  screenshots,
	}
	if len(flags) > 0 {
		fields["flags[]"] = strings.Join(flags, ",")
	}
	if req.Meta.Scene {
		fields["censored"] = "1"
	}
	if strings.EqualFold(strings.TrimSpace(req.Meta.DiscType), "BDMV") {
		fields["media"] = "BluRay"
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
		fields:       fields,
		adultContent: adultContent,
		manualTags:   manualTags,
		typeName:     typeName,
		tags:         tags,
	}, nil
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) (string, error) {
	meta := req.Meta
	var parts []string

	// Base description
	base := strings.TrimSpace(antDefaultSignaturePattern.ReplaceAllString(assets.Description, ""))
	report := bbcode.CleanPTPDescription(base, meta.DiscType)
	userDesc := strings.TrimSpace(report.Description)
	if userDesc == "" && base != "" && len(report.Images) == 0 {
		userDesc = base
	}

	if userDesc != "" {
		// Custom Header
		if header := strings.TrimSpace(req.AppConfig.Description.CustomDescriptionHeader); header != "" {
			parts = append(parts, header)
		}

		// Logo
		logoURL, _ := unit3d.ResolveLogo(meta, req.AppConfig)
		if logoURL != "" {
			if strings.HasSuffix(logoURL, ".svg") {
				logoURL = strings.ReplaceAll(logoURL, ".svg", ".png")
			}
			parts = append(parts, "[align=center][img]"+logoURL+"[/img][/align]")
		}

		// User Description
		parts = append(parts, userDesc)
	}

	// Disc menus
	if len(assets.MenuImages) > 0 {
		if header := strings.TrimSpace(req.AppConfig.Description.DiscMenuHeader); header != "" {
			parts = append(parts, header)
		}
		var shotParts []string
		for _, img := range assets.MenuImages {
			url := metautil.FirstNonEmptyTrimmed(img.RawURL, img.ImgURL, img.WebURL)
			if url != "" {
				shotParts = append(shotParts, "[img]"+url+"[/img]")
			}
		}
		if len(shotParts) > 0 {
			parts = append(parts, "[align=center]"+strings.Join(shotParts, " ")+"[/align]")
		}
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(req.AppConfig.Description.TonemappedHeader); tonemapHeader != "" && unit3d.ShouldIncludeTonemappedHeader(meta, req.AppConfig, assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// Join and finalize
	description := strings.Join(parts, "\n\n")

	finalized := bbcode.FinalizeTrackerDescription("ANT", description)

	// Character replacements
	replacer := strings.NewReplacer("•", "-", "’", "'", "–", "-")
	finalized = replacer.Replace(finalized)

	finalized = strings.TrimSpace(antEmptyURLPattern.ReplaceAllString(finalized, ""))

	// Debug saving
	if meta.Options.Debug {
		unit3d.SaveDescriptionDebug(meta, "ANT", req.AppConfig.MainSettings.DBPath, finalized, req.Logger)
	}

	return finalized, nil
}

func resolveMediaInfo(meta api.PreparedMetadata) (string, error) {
	if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
		return "", errors.New("trackers: ANT missing mediainfo text")
	}
	payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
	if err != nil {
		return "", fmt.Errorf("trackers: ANT read mediainfo: %w", err)
	}
	return string(payload), nil
}

func buildQuestionnaire(meta api.PreparedMetadata, state uploadState) *api.TrackerQuestionnaire {
	current := questionnaireAnswers(meta)
	fields := make([]api.TrackerQuestionnaireField, 0, 3)
	if strings.TrimSpace(state.typeName) == "" {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "type",
			Label:       "ANT Type",
			Kind:        "select",
			Options:     []string{"Feature Film", "Short Film", "Miniseries", "Other"},
			Value:       strings.TrimSpace(current["type"]),
			Placeholder: "Select a release type",
			Help:        "Pick the ANT content type for this release",
			Required:    true,
		})
	}
	if strings.TrimSpace(state.tags) == "" {
		fields = append(fields, api.TrackerQuestionnaireField{Key: "tags", Label: "Tags", Kind: "text", Value: strings.TrimSpace(current["tags"]), Placeholder: "action, drama", Help: "Comma-separated ANT tags", Required: true})
	}
	if state.adultContent {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "adult_screens",
			Label:       "Upload Screenshots",
			Kind:        "select",
			Options:     []string{"no", "yes"},
			Value:       metautil.FirstNonEmptyTrimmed(strings.TrimSpace(current["adult_screens"]), "no"),
			Placeholder: "Select yes or no",
			Help:        "Set to yes to include screenshots for adult content",
			Required:    true,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ANT", Fields: fields}
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["ANT"]
}

func resolveType(meta api.PreparedMetadata, answers map[string]string) (string, int) {
	if text := normalizeTypeName(answers["type"]); text != "" {
		return text, antTypeID(text)
	}
	if meta.ExternalMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.IMDB.Type))
		runtime := meta.ExternalMetadata.IMDB.RuntimeMinutes
		switch imdbType {
		case "movie", "tv movie", "tvmovie":
			if runtime >= 45 || runtime == 0 {
				return "Feature Film", 0
			}
			return "Short Film", 1
		case "short":
			return "Short Film", 1
		case "tv mini series":
			return "Miniseries", 2
		case "comedy":
			return "Other", 3
		}
	}
	keywords := strings.ToLower(strings.TrimSpace(resolveKeywords(meta)))
	category := strings.ToLower(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "movie" {
		runtime := 0
		if meta.ExternalMetadata.TMDB != nil {
			runtime = meta.ExternalMetadata.TMDB.Runtime
		}
		if runtime >= 45 || runtime == 0 {
			return "Feature Film", 0
		}
		return "Short Film", 1
	}
	if strings.Contains(keywords, "miniseries") {
		return "Miniseries", 2
	}
	if strings.Contains(keywords, "short") || strings.Contains(keywords, "short film") {
		return "Short Film", 1
	}
	if strings.Contains(keywords, "stand-up comedy") {
		return "Other", 3
	}
	return "", 0
}

func normalizeTypeName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "feature film", "feature", "movie":
		return "Feature Film"
	case "short film", "short":
		return "Short Film"
	case "miniseries", "mini series", "mini-series":
		return "Miniseries"
	case "other", "comedy":
		return "Other"
	default:
		return ""
	}
}

func antTypeID(value string) int {
	switch normalizeTypeName(value) {
	case "Short Film":
		return 1
	case "Miniseries":
		return 2
	case "Other":
		return 3
	default:
		return 0
	}
}

func resolveAudioFormat(meta api.PreparedMetadata) string {
	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	switch {
	case strings.Contains(audio, "DD+"), strings.Contains(audio, "EAC3"):
		return "EAC3"
	case strings.Contains(audio, " DD "), strings.HasPrefix(audio, "DD"), strings.Contains(audio, "AC3"):
		return "AC3"
	case strings.Contains(audio, "DTS-HD MA"), strings.Contains(audio, "DTS MA"):
		return "DTSMA"
	case strings.Contains(audio, "DTS"):
		return "DTS"
	case strings.Contains(audio, "TRUEHD"):
		return "TrueHD"
	case strings.Contains(audio, "FLAC"):
		return "FLAC"
	case strings.Contains(audio, "PCM"):
		return "PCM"
	case strings.Contains(audio, "OPUS"):
		return "Opus"
	case strings.Contains(audio, "AAC"):
		return "AAC"
	case strings.Contains(audio, "MP3"):
		return "MP3"
	case strings.Contains(audio, "MP2"):
		return "MP2"
	default:
		return "Other"
	}
}

func resolveFlags(meta api.PreparedMetadata) []string {
	flags := make([]string, 0, 12)
	edition := strings.ReplaceAll(meta.Edition, "'", "")
	for _, candidate := range []string{"Directors", "Extended", "Uncut", "Unrated", "4KRemaster"} {
		if strings.Contains(edition, candidate) {
			flags = append(flags, candidate)
		}
	}
	if strings.Contains(meta.Audio, "Dual-Audio") {
		flags = append(flags, "DualAudio")
	}
	if strings.Contains(meta.Audio, "Atmos") {
		flags = append(flags, "Atmos")
	}
	if meta.HasCommentary {
		flags = append(flags, "Commentary")
	}
	if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
		flags = append(flags, "3D")
	}
	if strings.Contains(strings.ToUpper(meta.HDR), "HDR") {
		flags = append(flags, "HDR10")
	}
	if strings.Contains(strings.ToUpper(meta.HDR), "DV") {
		flags = append(flags, "DV")
	}
	if strings.Contains(strings.ToUpper(meta.Distributor), "CRITERION") {
		flags = append(flags, "Criterion")
	}
	if strings.Contains(strings.ToUpper(meta.Type), "REMUX") {
		flags = append(flags, "Remux")
	}
	return dedupeStrings(flags)
}

func resolveTags(meta api.PreparedMetadata, answers map[string]string) (string, bool) {
	if tagValue := normalizeTags(strings.TrimSpace(answers["tags"])); tagValue != "" {
		return tagValue, true
	}
	values := make([]string, 0, 8)
	if meta.ExternalMetadata.TMDB != nil {
		values = append(values, splitTags(meta.ExternalMetadata.TMDB.Genres)...)
	}
	if len(values) == 0 {
		if meta.ExternalMetadata.IMDB != nil && len(splitTags(meta.ExternalMetadata.IMDB.Genres)) > 0 {
			return "", true
		}
		return "", true
	}
	allowed := map[string]struct{}{"action": {}, "adventure": {}, "animation": {}, "comedy": {}, "crime": {}, "documentary": {}, "drama": {}, "family": {}, "fantasy": {}, "history": {}, "horror": {}, "music": {}, "mystery": {}, "romance": {}, "sci.fi": {}, "thriller": {}, "war": {}, "western": {}}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; ok {
			filtered = append(filtered, value)
		}
	}
	return strings.Join(dedupeStrings(filtered), ","), false
}

func normalizeTags(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return strings.Join(dedupeStrings(splitTags(value)), ",")
}

func splitTags(value string) []string {
	items := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	result := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(item, " ", ".")))
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}

func resolveReleaseGroup(tag string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(tag, "-"))
	if trimmed == "" {
		return "", false
	}
	if _, banned := antBannedReleaseGroups[trimmed]; banned {
		return "", false
	}
	return trimmed, true
}

func detectAdult(meta api.PreparedMetadata) bool {
	candidates := []string{meta.Release.Genre, resolveKeywords(meta)}
	if meta.ExternalMetadata.TMDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.TMDB.Genres)
	}
	for _, candidate := range candidates {
		lower := strings.ToLower(candidate)
		for _, token := range []string{"xxx", "erotic", "porn", "adult", "orgy"} {
			if strings.Contains(lower, token) {
				return true
			}
		}
	}
	return false
}

func resolveAdultScreensAllowed(answers map[string]string, adultContent bool) bool {
	if !adultContent {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(answers["adult_screens"])) {
	case "y", "yes", "true", "1":
		return true
	default:
		return false
	}
}

func resolveScreenshotPayload(images []api.ScreenshotImage, allow bool) string {
	if !allow || len(images) == 0 {
		return ""
	}
	urls := make([]string, 0, 4)
	for _, image := range images {
		rawURL := strings.TrimSpace(image.RawURL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(image.ImgURL)
		}
		if rawURL == "" {
			continue
		}
		urls = append(urls, rawURL)
		if len(urls) == 4 {
			break
		}
	}
	return strings.Join(urls, "\n")
}

func resolveKeywords(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveUploadName(meta api.PreparedMetadata) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	if name := strings.TrimSpace(meta.ReleaseNameNoTag); name != "" {
		return name
	}
	if name := strings.TrimSpace(meta.Filename); name != "" {
		return name
	}
	return pathutil.Base(meta.SourcePath)
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if key == "flags[]" {
			for _, item := range strings.Split(value, ",") {
				trimmed := strings.TrimSpace(item)
				if trimmed == "" {
					continue
				}
				if err := writer.WriteField(key, trimmed); err != nil {
					_ = writer.Close()
					return nil, "", err
				}
			}
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", err
		}
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file_input", "torrent.torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
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
				return fmt.Errorf("trackers: ANT same infohash already exists: %s", viewURL)
			}
			return errors.New("trackers: ANT same infohash already exists")
		case strings.Contains(text, "exact same"):
			return errors.New("trackers: ANT exact same media file already exists")
		}
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
		return fmt.Errorf("trackers: ANT api error: %s", message)
	}
	return fmt.Errorf("trackers: ANT upload failed status=%d body=%s", status, strings.TrimSpace(string(body)))
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

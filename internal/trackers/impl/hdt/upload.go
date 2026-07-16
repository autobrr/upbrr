// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/cookies"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var tokenPattern = regexp.MustCompile(`name="csrfToken"\s+value="([^"]+)"`)
var successPattern = regexp.MustCompile(`details\.php\?id=([a-zA-Z0-9]+)|Upload successful!`)
var detailsPattern = regexp.MustCompile(`details\.php\?id=([a-zA-Z0-9]+)`)

type uploadState struct {
	baseURL       string
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	nfo           *commonhttp.FileField
	blockedReason string
}

func upload(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT %s", state.blockedReason)
	}
	files := []commonhttp.FileField{{
		FieldName: "torrent",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}}
	if state.nfo != nil {
		files = append(files, *state.nfo)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.baseURL+"/upload.php", bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT upload request: %w", err)
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode >= 200 && resp.StatusCode < 400,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT read upload response: %w", err)
	}
	combined := finalURL + "\n" + string(responseBody)
	id := ""
	if match := detailsPattern.FindStringSubmatch(combined); len(match) >= 2 {
		id = match[1]
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && successPattern.MatchString(combined) {
		tURL := finalURL
		if id != "" && !strings.Contains(tURL, "details.php?id=") {
			tURL = state.baseURL + "/details.php?id=" + id
		}
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "HDT")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, tURL, "hd-torrents.org"); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDT",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: tURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "HDT", "upload_failure", responsePreview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("HDT", resp.StatusCode, responsePreview)
}

func buildUploadDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req, true)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	status := "ready"
	message := "dry-run payload generated"
	if state.blockedReason != "" {
		status = "blocked"
		message = state.blockedReason
	}
	return api.TrackerDryRunEntry{
		Tracker:          "HDT",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "hdt",
		Description:      state.description,
		Endpoint:         state.baseURL + "/upload.php",
		Payload:          cloneFields(state.fields),
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, dryRun bool) (uploadState, []*http.Cookie, error) {
	base := resolveBaseURL(req.TrackerConfig.URL)
	cookies, err := loadCookies(ctx, req.Runtime.DBPath, base)
	if err != nil {
		return uploadState{}, nil, err
	}
	token := strings.Join([]string{"dry", "run", "token"}, "-")
	if !dryRun {
		token, err = fetchToken(ctx, base, cookies)
		if err != nil {
			return uploadState{}, nil, err
		}
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	fields := map[string]string{
		"filename":  resolveName(req.Meta),
		"category":  strconv.Itoa(resolveCategoryID(req.Meta)),
		"info":      description,
		"csrfToken": token,
		"season":    boolString(req.Meta.TVPack),
		"anonymous": boolString(req.TrackerConfig.Anon),
	}
	if req.Meta.Is3D != "" {
		fields["3d"] = "true"
	}
	hdr := strings.ToUpper(strings.TrimSpace(req.Meta.HDR))
	if strings.Contains(hdr, "HDR10+") {
		fields["HDR10"] = "true"
		fields["HDR10Plus"] = "true"
	} else if strings.Contains(hdr, "HDR") {
		fields["HDR10"] = "true"
	}
	if strings.Contains(hdr, "DV") {
		fields["DolbyVision"] = "true"
	}
	if imdb := resolveIMDbURL(req.Meta); imdb != "" {
		fields["infosite"] = imdb + "/"
	}
	state := uploadState{
		baseURL:     base,
		torrentPath: torrentPath,
		description: description,
		releaseName: fields["filename"],
		fields:      fields,
	}
	if strings.TrimSpace(req.Meta.Release.Resolution) == "" {
		state.blockedReason = "missing resolution"
	}
	if file, ok := resolveNFO(req.Meta); ok {
		state.nfo = &file
	}
	return state, cookies, nil
}

func resolveBaseURL(configURL string) string {
	trimmed := strings.TrimSpace(configURL)
	if trimmed == "" {
		return "https://hd-torrents.me"
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Host != "" {
		return "https://" + parsed.Host
	}
	return strings.TrimRight(trimmed, "/")
}

func loadCookies(ctx context.Context, dbPath string, baseURL string) ([]*http.Cookie, error) {
	host := "hd-torrents.me"
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	return wrapTrackerResult(cookies.LoadTrackerHTTPCookies(ctx, dbPath, "HDT", host))
}

func fetchToken(ctx context.Context, baseURL string, cookies []*http.Cookie) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/upload.php", nil)
	if err != nil {
		return "", fmt.Errorf("trackers: HDT token request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: HDT token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	match := tokenPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", errors.New("trackers: HDT csrf token not found")
	}
	return strings.TrimSpace(match[1]), nil
}

func resolveCategoryID(meta api.UploadSubject) int {
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if category == "TV" {
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
			if resolution == "2160p" {
				return 72
			}
			return 59
		}
		if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
			if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
				return 73
			}
			return 60
		}
		switch resolution {
		case "2160p":
			return 65
		case "1080p", "1080i":
			return 30
		default:
			return 38
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
		if resolution == "2160p" {
			return 70
		}
		return 1
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
			return 71
		}
		return 2
	}
	switch resolution {
	case "2160p":
		return 64
	case "1080p", "1080i":
		return 5
	default:
		return 3
	}
}

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	parts := make([]string, 0, 15)

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// Logo
	if req.Runtime.Description.AddLogo {
		if logo := resolveLogo(meta); logo != "" {
			parts = append(parts, "[center][img]"+logo+"[/img][/center]")
		}
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (BDInfo or MediaInfo)
	if media := trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, meta); media != "" {
		parts = append(parts, "[pre]"+media+"[/pre]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	// menu
	if len(assets.MenuImages) > 0 {
		// header
		if header := strings.TrimSpace(req.Runtime.Description.DiscMenuHeader); header != "" {
			parts = append(parts, header)
		}
		// images
		if shots := screenshotBlock(assets.MenuImages); shots != "" {
			parts = append(parts, shots)
		}
	}
	// Screenshot Header
	if header := strings.TrimSpace(req.Runtime.Description.ScreenshotHeader); header != "" {
		parts = append(parts, header)
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(
		req.Runtime.Description.TonemappedHeader,
	); tonemapHeader != "" &&
		descriptionunit3d.ShouldIncludeTonemappedHeader(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig(), assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// screenshots
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}

	// custom user signature
	if signature := strings.TrimSpace(req.Runtime.Description.CustomSignature); signature != "" {
		parts = append(parts, signature)
	}

	// upbrr signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[right][url=%s][size=1]%s[/size][/url][/right]", link, text))

	// finalize description
	finalDescription := finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))

	// save debug description
	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "HDT", req.Runtime.DBPath, finalDescription, req.Logger)
	}

	return finalDescription
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.RawURL) == "" {
			continue
		}
		parts = append(parts, "<a href='"+strings.TrimSpace(image.RawURL)+"'><img src='"+strings.TrimSpace(image.ImgURL)+"' height=137></a> ")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[center]" + strings.Join(parts, " ") + "[/center]"
}

func resolveName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") || strings.EqualFold(strings.TrimSpace(meta.Type), "WEBRIP") ||
		strings.EqualFold(strings.TrimSpace(meta.Type), "ENCODE") {
		name = strings.Replace(name, meta.Audio, strings.Replace(meta.Audio, " ", "", 1), 1)
	}
	name = strings.ReplaceAll(name, " DV ", " DoVi ")
	name = strings.ReplaceAll(name, "BluRay REMUX", "Blu-ray Remux")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.ReplaceAll(name, ":", "")
	return strings.TrimSpace(name)
}

func resolveLogo(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolveIMDbURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL)
	}
	if meta.Identity.IMDBID > 0 {
		return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.Identity.IMDBID)
	}
	return ""
}

func resolveNFO(meta api.UploadSubject) (commonhttp.FileField, bool) {
	dir := filepath.Dir(metautil.FirstNonEmptyTrimmed(meta.MediaInfoTextPath, meta.SourcePath))
	payload, path, err := commonhttp.ReadFirstMatching(dir, "*.nfo")
	if err != nil {
		return commonhttp.FileField{}, false
	}
	return commonhttp.FileField{
		FieldName: "nfos",
		FileName:  filepath.Base(path),
		Content:   payload,
	}, true
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

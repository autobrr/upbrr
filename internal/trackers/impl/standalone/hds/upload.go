// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/cookies"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://hd-space.org"
	uploadURL  = baseURL + "/index.php?page=upload"
	torrentURL = baseURL + "/index.php?page=torrent-details&id="
	sourceFlag = "HD-Space"
)

var idPattern = regexp.MustCompile(`download\.php\?id=([a-zA-Z0-9]+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	nfo           *commonhttp.FileField
	blockedReason string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	state, cookies, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDS %s", state.blockedReason)
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
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	artifactPath := ""
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "HDS")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, cookies, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	cookies []*http.Cookie,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	result, err := commonhttp.ExecuteUpload(httpclient.New(httpclient.DefaultTimeout), httpReq, commonhttp.UploadExecutionOptions{
		Tracker:       "HDS",
		SuccessStatus: func(status int) bool { return status >= 200 && status < 400 },
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDS execute upload: %w", err)
	}
	combined := result.FinalURL + "\n" + string(result.Body)
	match := idPattern.FindStringSubmatch(combined)
	if result.Success && len(match) >= 2 {
		id := strings.TrimSpace(match[1])
		tURL := torrentURL + id
		if announceURL != "" {
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDS",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: baseURL + "/download.php?id=" + id,
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "HDS", "upload_failure", result.Preview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("HDS", result.StatusCode, result.Preview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "HDS",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "hds",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, []*http.Cookie, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	cookies, err := loadCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	fields := map[string]string{
		"category":      strconv.Itoa(resolveCategoryID(req.Meta)),
		"filename":      metautil.FirstNonEmptyTrimmed(req.Meta.ReleaseName, req.Meta.Filename, pathutil.Base(req.Meta.SourcePath)),
		"genre":         resolveGenres(req.Meta),
		"imdb":          resolveIMDbURL(req.Meta),
		"info":          description,
		"nuk_rea":       "",
		"nuk":           "false",
		"req":           "false",
		"submit":        "Send",
		"t3d":           boolString(req.Meta.Is3D != ""),
		"user_id":       "",
		"youtube_video": resolveYouTube(req.Meta),
		"anonymous":     boolString(req.TrackerConfig.Anon),
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: fields["filename"],
		fields:      fields,
	}
	if !supportsHDSResolution(req.Meta.Release.Resolution) {
		state.blockedReason = "resolution must be at least 720p"
	}
	if id := resolveIMDbURL(req.Meta); strings.TrimSpace(id) == "" {
		state.blockedReason = "missing IMDb ID"
	}
	if file, ok := resolveNFO(req.Meta); ok {
		state.nfo = &file
	}
	return state, cookies, nil
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "HDS", "hd-space.org")
	if err != nil {
		return values, fmt.Errorf("trackers: HDS load cookies: %w", err)
	}
	return values, nil
}

func resolveCategoryID(meta api.UploadSubject) int {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return 15
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		return 40
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	if strings.Contains(strings.ToLower(resolveGenres(meta)+" "+resolveKeywords(meta)), "documentary") {
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "2160p") {
			return 47
		}
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "1080p") || strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "1080i") {
			return 25
		}
		return 24
	}
	if meta.Anime {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 48
		case "1080p", "1080i":
			return 28
		default:
			return 27
		}
	}
	if category == "TV" {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 45
		case "1080p", "1080i":
			return 22
		default:
			return 21
		}
	}
	switch strings.TrimSpace(meta.Release.Resolution) {
	case "2160p":
		return 46
	case "1080p", "1080i":
		return 19
	default:
		return 18
	}
}

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	parts := make([]string, 0, 12)

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
	parts = append(parts, fmt.Sprintf("[center][url=%s][size=2]%s[/size][/url][/center]", link, text))

	// finalize description
	finalDescription := finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "HDS", req.Runtime.DBPath, finalDescription, req.Logger)
	}

	return finalDescription
}

func resolveLogo(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolveGenres(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres)
	case meta.ProviderMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres)
	default:
		return strings.TrimSpace(meta.Release.Genre)
	}
}

func resolveKeywords(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveIMDbURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL)
	}
	if meta.Identity.IMDBID > 0 {
		return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.Identity.IMDBID)
	}
	return ""
}

func resolveYouTube(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
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
		FieldName: "nfo",
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

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, image := range images {
		if strings.TrimSpace(image.WebURL) == "" || strings.TrimSpace(image.ImgURL) == "" {
			continue
		}
		sb.WriteString("[url=" + image.WebURL + "][img]" + image.ImgURL + "[/img][/url]")

		// HDS cannot resize images. If the image host does not provide small thumbnails(<400px), place only one image per line.
		// imgbox provides small thumbnails, so we can place them side-by-side.
		if !strings.Contains(strings.ToLower(image.WebURL), "imgbox") {
			sb.WriteString("\n")
		}
	}
	content := strings.TrimSpace(sb.String())
	if content == "" {
		return ""
	}
	return "[center]\n" + content + "\n[/center]"
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func supportsHDSResolution(value string) bool {
	switch strings.TrimSpace(value) {
	case "2160p", "1080p", "1080i", "720p":
		return true
	default:
		return false
	}
}

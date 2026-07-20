package fld

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	baseURL    = "https://flood.st"
	apiBaseURL = baseURL + "/api/torrents"
)

const sourceFlag = "FLD"

type uploadState struct {
	torrentPath   string
	releaseName   string
	description   string
	mediaInfo     string
	fields        map[string]string
	blockedReason string
}

type uploadResponse struct {
	Success    bool   `json:"success"`
	TorrentURL string `json:"torrent_url"`
	Message    string `json:"message"`
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: FLD %s", state.blockedReason)
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "meta_info",
		FileName:  state.releaseName + ".torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/upload", strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FLD request build: %w", err)
	}
	httpReq.Body = io.NopCloser(strings.NewReader(string(body)))
	httpReq.ContentLength = int64(len(body))
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.TrackerConfig.APIKey))

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FLD upload request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 300, commonhttp.DefaultResponsePreviewBytes)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FLD read upload response: %w", err)
	}

	var decoded uploadResponse
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return api.UploadSummary{}, commonhttp.UploadHTTPError("FLD", resp.StatusCode, responsePreview)
			}
			return api.UploadSummary{}, fmt.Errorf("trackers: FLD decode response: %w", err)
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && decoded.Success && decoded.TorrentURL != "" {
		torrentURL := decoded.TorrentURL
		torrentID := torrentURL
		downloadURL := fmt.Sprintf("%s/download?api_key=%s", torrentURL, strings.TrimSpace(req.TrackerConfig.APIKey))
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "FLD")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "FLD",
				TorrentID:   torrentID,
				TorrentURL:  torrentURL,
				DownloadURL: downloadURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	if _, artifactErr := commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "FLD", "upload_failure", responsePreview, ".json"); artifactErr != nil && req.Logger != nil {
		req.Logger.Warnf("trackers: FLD failure artifact write failed: %v", artifactErr)
	}

	message := metautil.FirstNonEmptyTrimmed(commonhttp.ExtractHTTPErrorDetail(responsePreview), commonhttp.RedactErrorDetail(decoded.Message), commonhttp.RedactErrorDetail(string(responsePreview)), "upload failed")
	return api.UploadSummary{}, fmt.Errorf("trackers: FLD %s", message)
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req)
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
		Tracker:          "FLD",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "fld",
		Description:      state.description,
		Endpoint:         apiBaseURL + "/upload",
		Payload:          cloneFields(state.fields),
		Files: []api.TrackerDryRunFile{{
			Field:   "meta_info",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: FLD missing api_key")
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
	description := buildDescription(req, assets)

	mediaInfo := trackers.ReadBDinfoOrMediaInfo(req.AppConfig.MainSettings.DBPath, req.Meta)
	if mediaInfo == "" {
		return uploadState{}, errors.New("trackers: FLD missing mediainfo")
	}

	releaseName := resolveUploadName(req.Meta)

	fields := map[string]string{
		"name":        releaseName,
		"imdb_id":     resolveIMDbID(req.Meta),
		"tmdb_id":     resolveTMDbID(req.Meta),
		"description": description,
		"media_info":  mediaInfo,
		"media_type":  resolveMediaType(req.Meta),
	}

	if req.TrackerConfig.Anon {
		fields["anonymous"] = "checked"
	}

	if edition := strings.TrimSpace(resolveEdition(req.Meta)); edition != "" {
		fields["edition"] = edition
	}

	state := uploadState{
		torrentPath: torrentPath,
		releaseName: releaseName,
		description: description,
		mediaInfo:   mediaInfo,
		fields:      fields,
	}

	if fields["imdb_id"] == "" && fields["tmdb_id"] == "" {
		state.blockedReason = "missing IMDb / TMDb ID"
	}
	if err := validateFLDRequirements(req.Meta); err != nil {
		state.blockedReason = err.Error()
	}
	return state, nil
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) string {
	meta := req.Meta

	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}

	var parts []string

	if header := strings.TrimSpace(req.AppConfig.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// Format media disc specs inside spoilers (DVD VOB MediaInfo / BDMV BDINFO)
	if discSection := buildDiscSection(meta, req.AppConfig.MainSettings.DBPath); discSection != "" {
		parts = append(parts, discSection)
	}

	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	// Layout screenshots two per row, identical to BHD
	allShots := make([]api.ScreenshotImage, 0, len(assets.MenuImages)+len(assets.Screenshots))
	allShots = append(allShots, assets.MenuImages...)
	allShots = append(allShots, assets.Screenshots...)
	if shots := buildScreenshotSection(allShots, maxInt(1, meta.Options.Screens)); shots != "" {
		parts = append(parts, shots)
	}

	if tonemapHeader := strings.TrimSpace(req.AppConfig.Description.TonemappedHeader); tonemapHeader != "" && descriptionunit3d.ShouldIncludeTonemappedHeader(meta, req.AppConfig, assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[center][url=%s]%s[/url][/center]", link, text))

	description := strings.Join(parts, "\n\n")
	finalized := bbcode.FinalizeTrackerDescription("FLD", description)

	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(meta, "FLD", req.AppConfig.MainSettings.DBPath, finalized, req.Logger)
	}

	return finalized
}

func buildDiscSection(meta api.PreparedMetadata, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "DVD":
		media := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), trackers.ReadBDinfoOrMediaInfo(dbPath, meta))
		if media == "" {
			return ""
		}
		return fmt.Sprintf("[spoiler=VOB MediaInfo][code]%s[/code][/spoiler]", media)
	case "BDMV":
		bdinfo, _ := trackers.ReadBDInfo(dbPath, meta)
		if bdinfo == "" {
			return ""
		}
		return fmt.Sprintf("[spoiler=BDINFO][code]%s[/code][/spoiler]", bdinfo)
	default:
		return ""
	}
}

func buildScreenshotSection(images []api.ScreenshotImage, limit int) string {
	if len(images) == 0 || limit <= 0 {
		return ""
	}

	var section strings.Builder
	section.WriteString("[align=center]")
	count := 0
	for _, image := range images {
		if count >= limit {
			break
		}
		imgURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.RawURL), strings.TrimSpace(image.ImgURL))
		webURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.WebURL), strings.TrimSpace(image.RawURL), imgURL)
		if imgURL == "" || webURL == "" {
			continue
		}
		if count > 0 {
			if count%2 == 0 {
				section.WriteString("\n\n")
			} else {
				section.WriteByte(' ')
			}
		}
		line := fmt.Sprintf("[url=%s][img width=350]%s[/img][/url]", webURL, imgURL)
		section.WriteString(line)
		count++
	}
	section.WriteString("[/align]")
	return section.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func resolveCategory(meta api.PreparedMetadata) string {
	category := strings.ToUpper(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = strings.ToUpper(strings.TrimSpace(meta.MediaInfoCategory))
	}
	return category
}

func resolveMediaType(meta api.PreparedMetadata) string {
	if resolveCategory(meta) == "TV" {
		if meta.TVPack {
			return "show_season"
		}
		return "show_episode"
	}
	return "movie"
}

func resolveTMDbID(meta api.PreparedMetadata) string {
	if resolveCategory(meta) == "TV" {
		return fmt.Sprintf("tv/%d", meta.ExternalIDs.TMDBID)
	}
	return fmt.Sprintf("movie/%d", meta.ExternalIDs.TMDBID)
}

func isDiscType(value string) bool {
	discType := strings.ToLower(strings.TrimSpace(value))
	return discType == "bdmv" || discType == "dvd" || discType == "hddvd"
}

func validateFLDRequirements(meta api.PreparedMetadata) error {
	if isDiscType(meta.DiscType) {
		return nil
	}

	container := strings.ToLower(strings.TrimSpace(meta.Container))
	if container != "" {
		allowed := []string{"mkv", "mp4"}
		if strings.EqualFold(strings.TrimSpace(meta.Type), "HDTV") {
			allowed = append(allowed, "ts")
		}

		if !slices.Contains(allowed, container) {
			return fmt.Errorf("container %q is not allowed for %s: only %s are permitted", meta.Container, meta.Type, strings.ToUpper(strings.Join(allowed, ", ")))
		}
	}

	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if resolution != "" {
		heightStr := strings.TrimSuffix(strings.TrimSuffix(resolution, "p"), "i")
		if height, err := strconv.Atoi(heightStr); err == nil && height > 0 && height < 1080 {
			return fmt.Errorf("resolution %s is not allowed: only 1080p and above are permitted", resolution)
		}
	}

	return nil
}

func resolveIMDbID(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID > 0 {
		return "tt" + strconv.Itoa(meta.ExternalIDs.IMDBID)
	}
	return ""
}

func resolveEdition(meta api.PreparedMetadata) string {
	if len(meta.Release.Edition) > 0 {
		return strings.Join(meta.Release.Edition, " ")
	}
	return ""
}

func resolveUploadName(meta api.PreparedMetadata) string {
	name := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.SceneName), strings.TrimSpace(meta.ReleaseNameClean), strings.TrimSpace(meta.ReleaseName), strings.TrimSpace(meta.Filename))
	if name == "" {
		name = "release"
	}

	source := strings.ToUpper(strings.TrimSpace(meta.Release.Source))
	if slices.Contains([]string{"PAL DVD", "NTSC DVD", "DVD", "NTSC", "PAL"}, source) {
		var audioClean string
		var audioDotted string
		if len(meta.Release.Audio) > 0 {
			audioClean = strings.Join(strings.Fields(strings.Join(meta.Release.Audio, " ")), " ")
			audioDotted = strings.Join(meta.Release.Audio, ".")
		}
		var videoCodec string
		if len(meta.Release.Codec) > 0 {
			videoCodec = meta.Release.Codec[0]
		}
		if videoCodec != "" {
			if audioClean != "" && strings.Contains(name, audioClean) {
				name = strings.ReplaceAll(name, audioClean, videoCodec+" "+audioClean)
			} else if audioDotted != "" && strings.Contains(name, audioDotted) {
				name = strings.ReplaceAll(name, audioDotted, videoCodec+"."+audioDotted)
			}
		}
	}
	name = strings.ReplaceAll(name, "DD+", "DDP")
	return name
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

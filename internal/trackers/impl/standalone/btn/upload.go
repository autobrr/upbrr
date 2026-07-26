// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

type uploadContext struct {
	baseURL   string
	uploadURL string
	apiToken  string
	apiURL    string
	client    *http.Client
}

func uploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (api.UploadSummary, error) {
	req.Intent = trackers.PreparationIntentUpload
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return api.UploadSummary{}, nameFailure
	}
	plan, failure := trackers.PrepareAdapter(ctx, req, nil, func(ctx context.Context, input trackers.PreparationInput) (trackers.PreparedOperation, error) {
		return prepareUploadAt(ctx, input, baseURL)
	})
	if failure != nil {
		return api.UploadSummary{}, failure
	}
	summary, err := plan.Submit(ctx)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BTN submit prepared upload: %w", err)
	}
	return summary, nil
}

func prepareUploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	if req.Intent != trackers.PreparationIntentUpload {
		preview, err := buildUploadDryRunAt(ctx, req, baseURL)
		if err != nil {
			return trackers.PreparedOperation{}, err
		}
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if err := validateBTNRequest(req); err != nil {
		return trackers.PreparedOperation{}, err
	}
	if message, err := validateBTNTVPayloadMetadata(req.Meta); err != nil {
		if req.Logger != nil {
			req.Logger.Warnf("trackers: BTN %s", message)
		}
		return trackers.PreparedOperation{}, err
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: BTN prepared upload torrent: %w", err)
	}

	uploadCtx, err := newUploadContextAt(ctx, req, baseURL)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	client, err := ensureBTNUploadSession(ctx, req.TrackerConfig, req.Runtime.DBPath, uploadCtx)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	uploadCtx.client = client

	if err := checkBTNSeasonPackReservation(ctx, uploadCtx, req); err != nil {
		return trackers.PreparedOperation{}, err
	}

	data, err := prepareUploadData(ctx, req, uploadCtx)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}

	files := resolveBTNUploadFiles(req.Meta, torrentPath)
	body, contentType, err := commonhttp.BuildMultipartPayload(data, files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	trackerTorrentPath, err := resolveTrackerTorrentPath(req.Meta, req.Runtime.DBPath, "BTN")
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: BTN reviewed upload name: %w", err)
	}
	preview := standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "BTN",
		ReleaseName:      releaseName,
		DescriptionGroup: "btn",
		Description:      data["release_desc"],
		Endpoint:         uploadCtx.uploadURL,
		Payload:          data,
		Files:            resolveBTNDryRunFiles(req.Meta, torrentPath),
	})
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, uploadCtx, trackerTorrentPath, body, contentType)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	uploadCtx uploadContext,
	trackerTorrentPath string,
	body []byte,
	contentType string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadCtx.uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BTN request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := uploadCtx.client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BTN upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	groupID, torrentID, matched := btnUploadIDsFromText(finalURL)
	torrentDownloaded := false
	if !matched {
		responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 400, 2048)
		if err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: BTN read upload response: %w", err)
		}
		intermediate, handled, err := resolveBTNUploadIntermediatePage(ctx, uploadCtx.client, uploadCtx.baseURL, finalURL, responseBody)
		if handled {
			if err != nil {
				if req.Logger != nil {
					req.Logger.Warnf("trackers: BTN intermediate upload page fallback to API search: %s", commonhttp.RedactErrorDetail(err.Error()))
				}
				groupID = intermediate.groupID
				selectedID, selectedGroupID, resolveErr := resolveAndDownloadViaAPI(
					ctx,
					uploadCtx.apiURL,
					uploadCtx.apiToken,
					req,
					groupID,
					trackerTorrentPath,
				)
				if selectedID != "" {
					torrentID = selectedID
				}
				if selectedGroupID != "" {
					groupID = selectedGroupID
				}
				if resolveErr != nil {
					trackers.LogRegisteredTorrentUnavailable(req.Logger, "BTN")
				} else {
					torrentDownloaded = true
				}
			} else {
				groupID = intermediate.groupID
				torrentID = intermediate.torrentID
			}
		} else {
			groupID, torrentID, matched = btnUploadIDsFromText(string(responseBody))
		}
		if !matched && groupID == "" && torrentID == "" {
			failurePath, _ := commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "BTN", "upload-failure", responsePreview, ".html")
			if failurePath != "" {
				return api.UploadSummary{}, fmt.Errorf(
					"%w failure=%s",
					commonhttp.UploadHTTPErrorWithURL("BTN", resp.StatusCode, finalURL, responsePreview),
					failurePath,
				)
			}
			return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("BTN", resp.StatusCode, finalURL, responsePreview)
		}
	}
	torrentURL := buildBTNTorrentURL(uploadCtx.baseURL, groupID, torrentID)

	if torrentID != "" && !torrentDownloaded {
		if err := downloadTrackerTorrent(ctx, uploadCtx.client, uploadCtx.baseURL, torrentID, trackerTorrentPath); err != nil {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "BTN")
		} else {
			torrentDownloaded = true
		}
	} else if torrentID == "" {
		selectedID, selectedGroupID, resolveErr := resolveAndDownloadViaAPI(
			ctx,
			uploadCtx.apiURL,
			uploadCtx.apiToken,
			req,
			groupID,
			trackerTorrentPath,
		)
		if selectedID != "" {
			torrentID = selectedID
		}
		if selectedGroupID != "" {
			groupID = selectedGroupID
		}
		torrentURL = buildBTNTorrentURL(uploadCtx.baseURL, groupID, torrentID)
		if resolveErr != nil {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "BTN")
		} else {
			torrentURL = buildBTNTorrentURL(uploadCtx.baseURL, groupID, torrentID)
			torrentDownloaded = true
		}
	}

	persistedPath := ""
	if torrentDownloaded {
		persistedPath = trackerTorrentPath
	}
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "BTN",
			TorrentID:   torrentID,
			TorrentURL:  torrentURL,
			DownloadURL: buildBTNDownloadURL(uploadCtx.baseURL, torrentID),
			TorrentPath: persistedPath,
		}},
	}, nil
}

// buildUploadDryRun returns a BTN preview entry with the exact payload that
// would be submitted locally. TV payloads that would serialize missing
// canonical season or episode metadata as zero are returned as blocked so the
// operator sees the remediation before upload.
func buildUploadDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRunAt(ctx, req, btnDefaultBaseURL)
}

func buildUploadDryRunAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (api.TrackerDryRunEntry, error) {
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return api.TrackerDryRunEntry{}, nameFailure
	}
	if err := validateBTNRequest(req); err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: BTN reviewed upload name: %w", err)
	}

	uploadCtx, err := newUploadContextAt(ctx, req, baseURL)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	if err := validateBTNDryRunUploadAuth(ctx, req, uploadCtx); err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	autofillPayload, uploadType := buildBTNAutofillPayload(req.Meta, releaseName)
	debugSections := make([]api.TrackerDryRunDebugSection, 0, 2)
	if req.Intent == trackers.PreparationIntentDryRun {
		debugSections = append(debugSections, api.TrackerDryRunDebugSection{
			Title:    "BTN autofill request",
			Endpoint: uploadCtx.uploadURL,
			Payload:  urlValuesToPayloadMap(autofillPayload),
		})
	}

	payload := map[string]string{
		"submit":       "true",
		"type":         uploadType,
		"scenename":    releaseName,
		"origin":       resolveOrigin(req.Meta),
		"release_desc": resolveBTNReleaseDesc(req.Meta),
		"tvdb":         "autofilled",
	}
	if resolveFastTorrent(req.TrackerConfig) {
		payload["fasttorrent"] = "on"
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return api.TrackerDryRunEntry{}, fmt.Errorf("trackers: BTN prepared dry-run torrent: %w", err)
	}

	message := "dry-run payload generated"
	status := "ready"
	if metadataMessage, err := validateBTNTVPayloadMetadata(req.Meta); err != nil {
		message += "; " + metadataMessage
		status = "blocked"
	}
	if req.Intent == trackers.PreparationIntentDryRun && status == "ready" {
		client, err := ensureBTNUploadSession(ctx, req.TrackerConfig, req.Runtime.DBPath, uploadCtx)
		if err != nil {
			return api.TrackerDryRunEntry{}, err
		}
		uploadCtx.client = client
		fields, err := requestBTNAutofillFields(ctx, uploadCtx, autofillPayload, uploadType)
		if err != nil {
			return api.TrackerDryRunEntry{}, err
		}
		payload, err = buildBTNUploadPayload(req, fields)
		if err != nil {
			return api.TrackerDryRunEntry{}, err
		}
		message += "; BTN autofill debug completed"
		debugSections = append(debugSections, api.TrackerDryRunDebugSection{
			Title:    "BTN final upload payload after autofill",
			Endpoint: uploadCtx.uploadURL,
			Payload:  payload,
			Files:    resolveBTNDryRunFiles(req.Meta, torrentPath),
		})
	}

	return api.TrackerDryRunEntry{
		Tracker:          "BTN",
		Status:           status,
		Message:          message,
		ReleaseName:      releaseName,
		DescriptionGroup: "btn",
		Description:      payload["release_desc"],
		Endpoint:         uploadCtx.uploadURL,
		Payload:          payload,
		Files:            resolveBTNDryRunFiles(req.Meta, torrentPath),
		DebugSections:    debugSections,
	}, nil
}

func newUploadContextAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (uploadContext, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return uploadContext{}, fmt.Errorf("trackers: BTN create cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 45 * time.Second, Jar: jar}
	uploadCtx := uploadContext{
		baseURL:   baseURL,
		uploadURL: baseURL + btnUploadPath,
		apiToken:  req.Runtime.BTNAPIToken,
		apiURL:    resolveBTNAPIURL(req.TrackerConfig),
		client:    client,
	}
	loadBTNCookiesIntoJar(ctx, client, req.Runtime.DBPath, baseURL)
	return uploadCtx, nil
}

func prepareUploadData(ctx context.Context, req trackers.PreparationInput, uploadCtx uploadContext) (map[string]string, error) {
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return nil, nameFailure
	}
	if _, err := validateBTNTVPayloadMetadata(req.Meta); err != nil {
		return nil, err
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN reviewed upload name: %w", err)
	}

	autofillPayload, uploadType := buildBTNAutofillPayload(req.Meta, releaseName)
	fields, err := requestBTNAutofillFields(ctx, uploadCtx, autofillPayload, uploadType)
	if err != nil {
		return nil, err
	}
	return buildBTNUploadPayload(req, fields)
}

// buildBTNUploadPayload merges BTN autofill fields with local metadata for the
// final upload form. BTN text fields such as album_desc are retained, release_desc
// is populated only from MediaInfo text, and local MediaInfo-derived dropdown
// mappings win when both sources provide a BTN-supported value.
func buildBTNUploadPayload(req trackers.PreparationInput, fields map[string]string) (map[string]string, error) {
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN reviewed upload name: %w", err)
	}
	format := mapContainer(req.Meta, fields)
	bitrate := mapCodec(req.Meta, fields)
	media := mapSource(req.Meta, fields)
	if format == "" || bitrate == "" || media == "" {
		return nil, fmt.Errorf("trackers: BTN dropdown mapping failed format=%q bitrate=%q media=%q", format, bitrate, media)
	}

	title := metautil.FirstNonEmptyTrimmed(fields["title"])
	if resolveUploadType(req.Meta) == "Season" && title != "" {
		isNumeric := true
		for _, r := range title {
			if r < '0' || r > '9' {
				isNumeric = false
				break
			}
		}
		if isNumeric {
			title = "Season " + title
		}
	}

	resolution := mapResolution(req.Meta, fields)
	logBTNAutofillMismatch(req.Logger, "format", format, fields["format"])
	logBTNAutofillMismatch(req.Logger, "bitrate", bitrate, fields["bitrate"])
	logBTNAutofillMismatch(req.Logger, "media", media, fields["media"])
	logBTNAutofillMismatch(req.Logger, "resolution", resolution, fields["resolution"])
	payload := map[string]string{
		"submit":       "true",
		"type":         resolveUploadType(req.Meta),
		"scenename":    releaseName,
		"seriesid":     metautil.FirstNonEmptyTrimmed(fields["seriesid"]),
		"artist":       metautil.FirstNonEmptyTrimmed(fields["artist"]),
		"title":        title,
		"actors":       metautil.FirstNonEmptyTrimmed(fields["actors"]),
		"origin":       resolveOrigin(req.Meta),
		"year":         metautil.FirstNonEmptyTrimmed(fields["year"]),
		"tags":         resolveBTNTags(req.Meta, fields),
		"image":        resolveBTNImage(req.Meta, fields),
		"album_desc":   buildAlbumDesc(req.Meta, fields),
		"format":       format,
		"bitrate":      bitrate,
		"media":        media,
		"resolution":   resolution,
		"release_desc": resolveBTNReleaseDesc(req.Meta),
		"tvdb":         "autofilled",
	}
	if resolveFastTorrent(req.TrackerConfig) {
		payload["fasttorrent"] = "on"
	}
	if language := resolveBTNOriginalLanguage(req.Meta); language != "" && !isBTNEnglishLanguage(language) {
		payload["foreign"] = "on"
		if countryID := resolveCountryID(req.Meta); countryID != "" {
			payload["country"] = countryID
		}
	}
	clean := make(map[string]string, len(payload))
	for key, value := range payload {
		if key != "release_desc" && strings.TrimSpace(value) == "" {
			continue
		}
		clean[key] = value
	}
	return clean, nil
}

// resolveBTNReleaseDesc returns the MediaInfo text BTN expects in release_desc.
// Description overrides are intentionally ignored for this field.

// urlValuesToPayloadMap flattens form values for debug display; BTN autofill
// fields are single-valued, so later values are intentionally ignored.
func urlValuesToPayloadMap(values url.Values) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		if len(value) == 0 {
			continue
		}
		out[key] = value[0]
	}
	return out
}

// resolveBTNTags keeps BTN autofill genres when present and otherwise maps
// TVDB then IMDb genres to BTN-supported tag labels.

// resolveBTNImage keeps BTN autofill poster data when present. Empty autofill
// falls back through TVDB, IMDb, TVmaze, then TMDB poster metadata.
func resolveBTNImage(meta api.UploadSubject, fields map[string]string) string {
	if image := strings.TrimSpace(fields["image"]); image != "" {
		return image
	}
	if meta.ProviderMetadata.TVDB != nil {
		if poster := strings.TrimSpace(meta.ProviderMetadata.TVDB.Poster); poster != "" {
			return poster
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		if poster := strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover); poster != "" {
			return poster
		}
	}
	if meta.ProviderMetadata.TVmaze != nil {
		if poster := strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster); poster != "" {
			return poster
		}
		if poster := strings.TrimSpace(meta.ProviderMetadata.TVmaze.PosterMedium); poster != "" {
			return poster
		}
	}
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
	}
	return ""
}

// mapBTNGenres converts provider genre text to comma-separated BTN genre tags.
// Unrecognized genres are omitted instead of being submitted as free-form tags.

// normalizeBTNGenreText lowercases and strips common separators so source
// genre text and local aliases can be compared consistently.

// normalizedBTNGenreContains reports whether a normalized genre list contains
// an alias as a complete token sequence.

// preferredBTNTVDBEpisodeTitle returns TVDB's English episode title when it is
// available, falling back to the original-language title.

// preferredBTNIMDBEpisodeTitle returns the selected IMDb episode title when an
// episode-specific IMDb entry can be matched to the upload metadata.

// preferredBTNTVDBOverview returns TVDB episode overview text before series
// overview text, using English translations before original-language values.

// preferredBTNIMDBOverview returns IMDb plot text when TVDB overview data is
// unavailable for the BTN description block.

// buildAlbumDesc keeps BTN's autofilled album_desc when available. If BTN did
// not return one, TV uploads fall back to BBCode episode blocks from TVDB or
// provider/local episode metadata.

// buildTVDBAlbumDesc builds BTN album_desc from TVDB data only. Episode uploads
// use the selected episode; season-pack uploads use every fetched episode in
// the selected season.

// formatBTNEpisodeAlbumDesc renders each episode as the BBCode block expected
// by BTN's album_desc field. Empty input returns an empty string.

// btnTVDBEpisodeAired returns the TVDB episode air date used in BTN-generated
// description text. An empty value leaves metadata date fallbacks in control.

// btnIMDBEpisodeAired returns the selected IMDb episode release date in the
// most specific YYYY[-MM[-DD]] form available.

// preferredBTNIMDBEpisode returns the IMDb episode entry for this upload when
// the canonical season and episode identify one, or the sole available IMDb
// episode when the metadata payload already represents a single episode.

// btnIMDBEpisodeNumber parses IMDb episode text such as "7", "E07", or
// "Episode 7" into the numeric episode value BTN expects.

// validateBTNTVPayloadMetadata returns the shared BTN TV metadata block reason
// used by live upload, autofill, and dry-run when canonical season or episode
// data is missing.

// resolveBTNTVSeasonEpisode returns the season and episode numbers BTN should
// use for generated request and description fields. TVDB episode numbers win
// over IMDb episode data, then metadata ints; missing values fall back
// independently.

// btnTVPayloadMetadataMessage explains when BTN cannot build TV season or
// episode fields because canonical metadata is absent. Parsed release values are
// reported only as ignored signals, and the message includes the operator action
// required by blocked dry-run entries.

// resolveBTNUploadFiles returns the multipart file parts BTN accepts for an
// upload. Scene NFOs are attached only when scene metadata confirms the release
// and the prepared NFO file still exists.

// resolveBTNDryRunFiles mirrors the upload file parts without reading file
// content so previews show whether BTN will receive an NFO.

func resolveTrackerTorrentPath(meta api.UploadSubject, dbPath string, tracker string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: BTN tracker torrent path requires db path and source path")
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: BTN tmp root: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: BTN tmp release dir: %w", err)
	}
	name := strings.ToLower(strings.TrimSpace(tracker))
	if name == "" {
		name = "tracker"
	}
	return filepath.Join(tmpDir, base+"."+name+".torrent"), nil
}

// downloadTrackerTorrent fetches BTN's exact generated torrent file.
func downloadTrackerTorrent(ctx context.Context, client *http.Client, baseURL string, torrentID string, outputPath string) error {
	if strings.TrimSpace(torrentID) == "" {
		return errors.New("trackers: BTN torrent_id missing")
	}
	downloadURL := buildBTNDownloadURL(baseURL, torrentID)
	return downloadBTNTorrentURL(ctx, client, downloadURL, outputPath)
}

func buildBTNDownloadURL(baseURL string, torrentID string) string {
	torrentID = strings.TrimSpace(torrentID)
	if torrentID == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/torrents.php?action=download&id=" + url.QueryEscape(torrentID)
}

// downloadBTNTorrentURL fetches a BTN torrent download URL and writes only a
// bencoded torrent payload to outputPath.
func downloadBTNTorrentURL(ctx context.Context, client *http.Client, downloadURL string, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("trackers: BTN torrent download request build: %w", err)
	}
	if err := trackers.DownloadRegisteredTorrent(ctx, client, req, outputPath); err != nil {
		return fmt.Errorf("trackers: BTN registered torrent: %w", err)
	}
	return nil
}

type btnUploadIntermediateResult struct {
	groupID   string
	torrentID string
}

// resolveBTNUploadIntermediatePage handles BTN's post-upload warning page that
// requires continuing to the canonical torrent page before the final torrent id
// is available. It returns handled=false for ordinary upload responses.
func resolveBTNUploadIntermediatePage(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	currentURL string,
	body []byte,
) (btnUploadIntermediateResult, bool, error) {
	if !isBTNUploadIntermediatePage(body) {
		return btnUploadIntermediateResult{}, false, nil
	}

	result := btnUploadIntermediateResult{}
	detailURL, detailGroupID, detailTorrentID, ok := findBTNUploadDetailURL(baseURL, currentURL, body)
	canonicalTorrentID := false
	if ok {
		result.groupID = detailGroupID
		if detailTorrentID != "" {
			result.torrentID = detailTorrentID
			canonicalTorrentID = true
		}
	}

	if !ok {
		return result, true, errors.New("trackers: BTN intermediate page missing torrent detail link")
	}
	detailBody, detailFinalURL, err := fetchBTNTorrentDetailPage(ctx, client, detailURL)
	if err != nil {
		return result, true, err
	}
	if groupID, torrentID, matched := btnUploadIDsFromText(detailFinalURL); matched {
		result.groupID = groupID
		if torrentID != "" {
			result.torrentID = torrentID
			return result, true, nil
		}
	}
	if groupID, torrentID, matched := btnUploadIDsFromText(string(detailBody)); matched {
		result.groupID = groupID
		if torrentID != "" {
			result.torrentID = torrentID
			return result, true, nil
		}
	}
	if result.groupID == "" || !canonicalTorrentID {
		return result, true, errors.New("trackers: BTN intermediate detail page missing canonical torrent id")
	}
	return result, true, nil
}

// isBTNUploadIntermediatePage detects BTN's warning page that appears after a
// successful upload before the user has downloaded the generated torrent file.
func isBTNUploadIntermediatePage(body []byte) bool {
	normalized := strings.ToLower(html.UnescapeString(string(body)))
	return strings.Contains(normalized, "download the torrent file") ||
		strings.Contains(normalized, "need to download the torrent")
}

// findBTNUploadDetailURL extracts the same-origin canonical torrent page URL
// from an intermediate BTN upload page.
func findBTNUploadDetailURL(baseURL string, currentURL string, body []byte) (string, string, string, bool) {
	for _, raw := range btnHTMLURLAttrPattern.FindAllStringSubmatch(string(body), -1) {
		if len(raw) < 2 {
			continue
		}
		candidate, ok := resolveBTNSameOriginURL(baseURL, currentURL, raw[1])
		if !ok || !strings.EqualFold(candidate.Path, "/torrents.php") {
			continue
		}
		query := candidate.Query()
		if strings.EqualFold(query.Get("action"), "download") {
			continue
		}
		groupID := strings.TrimSpace(query.Get("id"))
		if groupID == "" {
			continue
		}
		torrentID := strings.TrimSpace(query.Get("torrentid"))
		return candidate.String(), groupID, torrentID, true
	}
	return "", "", "", false
}

// fetchBTNTorrentDetailPage follows the intermediate-page continue target and
// returns the bounded HTML body with the final URL after redirects.
func fetchBTNTorrentDetailPage(ctx context.Context, client *http.Client, detailURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: BTN intermediate detail request build: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: BTN intermediate detail request: %w", err)
	}
	defer resp.Body.Close()
	finalURL := detailURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, finalURL, fmt.Errorf("trackers: BTN intermediate detail failed status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, finalURL, fmt.Errorf("trackers: BTN read intermediate detail response: %w", err)
	}
	return body, finalURL, nil
}

// btnUploadIDsFromText extracts BTN group and torrent ids from a URL or HTML
// fragment, including HTML-escaped query separators.
func btnUploadIDsFromText(value string) (string, string, bool) {
	matches := btnSuccessURLPattern.FindStringSubmatch(html.UnescapeString(value))
	if len(matches) < 2 {
		return "", "", false
	}
	groupID := strings.TrimSpace(matches[1])
	torrentID := ""
	if len(matches) > 2 {
		torrentID = strings.TrimSpace(matches[2])
	}
	return groupID, torrentID, groupID != ""
}

// buildBTNTorrentURL returns BTN's canonical torrent detail URL for a group and
// optional torrent id.
func buildBTNTorrentURL(baseURL string, groupID string, torrentID string) string {
	torrentURL := strings.TrimRight(baseURL, "/") + "/torrents.php?id=" + url.QueryEscape(strings.TrimSpace(groupID))
	if strings.TrimSpace(torrentID) != "" {
		torrentURL += "&torrentid=" + url.QueryEscape(strings.TrimSpace(torrentID))
	}
	return torrentURL
}

// decodeBTNAPIJSON reads a bounded BTN JSON-RPC response, rejects duplicate
// object keys, and unmarshals the single JSON value into dest.
func decodeBTNAPIJSON(r io.Reader, dest any) error {
	payload, err := io.ReadAll(io.LimitReader(r, btnAPIJSONMaxBytes+1))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if len(payload) > btnAPIJSONMaxBytes {
		return fmt.Errorf("response body exceeds %d bytes", btnAPIJSONMaxBytes)
	}
	if err := validateBTNJSONNoDuplicateObjectNames(payload); err != nil {
		return err
	}
	if err := json.Unmarshal(payload, dest); err != nil {
		return fmt.Errorf("unmarshal response body: %w", err)
	}
	return nil
}

func callBTNAPI(ctx context.Context, apiURL, id, method string, params []any, dest any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("trackers: BTN API %s encode: %w", method, err)
	}
	apiReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("trackers: BTN API %s request build: %w", method, err)
	}
	apiReq.Header.Set("Content-Type", "application/json")
	apiResp, err := (&http.Client{Timeout: 30 * time.Second}).Do(apiReq)
	if err != nil {
		return fmt.Errorf("trackers: BTN API %s request: %w", method, err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode < 200 || apiResp.StatusCode >= 300 {
		return fmt.Errorf("trackers: BTN API %s failed status=%d", method, apiResp.StatusCode)
	}
	if err := decodeBTNAPIJSON(apiResp.Body, dest); err != nil {
		return fmt.Errorf("trackers: BTN API decode %s response: %w", method, err)
	}
	return nil
}

// validateBTNJSONNoDuplicateObjectNames scans one JSON value before unmarshal
// so duplicate object names cannot collapse into the last decoded value.
func validateBTNJSONNoDuplicateObjectNames(payload []byte) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := validateBTNJSONValueNoDuplicateObjectNames(dec, ""); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("read trailing JSON token: %w", err)
	}
	return nil
}

// validateBTNJSONValueNoDuplicateObjectNames consumes one JSON value and
// reports duplicate object member names with their object path.
func validateBTNJSONValueNoDuplicateObjectNames(dec *json.Decoder, path string) error {
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON token at %q: %w", path, err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return fmt.Errorf("read JSON object key at %q: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("invalid JSON object key at %q", path)
			}
			if _, exists := seen[key]; exists {
				if path == "" {
					return fmt.Errorf("duplicate JSON object key %q", key)
				}
				return fmt.Errorf("duplicate JSON object key %q at %q", key, path)
			}
			seen[key] = struct{}{}
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if err := validateBTNJSONValueNoDuplicateObjectNames(dec, childPath); err != nil {
				return err
			}
		}
		return consumeBTNJSONDelim(dec, '}')
	case '[':
		index := 0
		for dec.More() {
			childPath := fmt.Sprintf("%s[%d]", path, index)
			if err := validateBTNJSONValueNoDuplicateObjectNames(dec, childPath); err != nil {
				return err
			}
			index++
		}
		return consumeBTNJSONDelim(dec, ']')
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %q", delim, path)
	}
}

// consumeBTNJSONDelim consumes and verifies a JSON closing delimiter.
func consumeBTNJSONDelim(dec *json.Decoder, want json.Delim) error {
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON delimiter %q: %w", want, err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != want {
		return fmt.Errorf("expected JSON delimiter %q", want)
	}
	return nil
}

// resolveAndDownloadViaAPI finds the uploaded torrent through BTN's JSON-RPC
// API, validates the returned DownloadURL, and writes the fetched bencoded
// torrent to outputPath. The selected BTN torrent and group ids are returned
// so upload summaries reflect the torrent that was actually downloaded.
func resolveAndDownloadViaAPI(
	ctx context.Context,
	apiURL string,
	apiToken string,
	req trackers.PreparationInput,
	groupID string,
	outputPath string,
) (string, string, error) {
	if strings.TrimSpace(apiToken) == "" {
		return "", "", errors.New("trackers: BTN api token missing for torrent resolution")
	}
	if strings.TrimSpace(apiURL) == "" {
		apiURL = btnAPIRPCURL
	}
	downloadOrigin, err := newBTNAPIDownloadOrigin(ctx, apiURL)
	if err != nil {
		return "", "", fmt.Errorf("trackers: BTN API download origin: %w", err)
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return "", "", fmt.Errorf("trackers: BTN reviewed upload name: %w", err)
	}
	filter := map[string]any{"searchstr": releaseName}
	if strings.TrimSpace(groupID) != "" {
		filter["group"] = groupID
	}

	var response struct {
		Result struct {
			Torrents map[string]map[string]any `json:"torrents"`
		} `json:"result"`
	}
	if err := callBTNAPI(ctx, apiURL, "ua-btn-upload", "getTorrentsSearch", []any{apiToken, filter, 50}, &response); err != nil {
		return "", "", err
	}

	selection := selectBTNAPITorrent(response.Result.Torrents, releaseName, groupID)
	if selection.ID == "" {
		return "", "", errors.New("trackers: BTN API did not return a matching torrent id")
	}

	var downloadResult struct {
		Result struct {
			DownloadURL string `json:"DownloadURL"`
		} `json:"result"`
	}
	if err := callBTNAPI(ctx, apiURL, "ua-btn-download", "getTorrentById", []any{apiToken, selection.ID}, &downloadResult); err != nil {
		return selection.ID, selection.GroupID, err
	}

	if downloadResult.Result.DownloadURL == "" {
		return selection.ID, selection.GroupID, errors.New("trackers: BTN API did not return DownloadURL")
	}

	if err := downloadOrigin.validateDownloadURL(ctx, downloadResult.Result.DownloadURL); err != nil {
		return selection.ID, selection.GroupID, fmt.Errorf("trackers: BTN API invalid download url: %w", err)
	}

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadResult.Result.DownloadURL, nil)
	if err != nil {
		return selection.ID, selection.GroupID, fmt.Errorf("trackers: BTN API torrent fetch request build: %w", err)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := downloadOrigin.validateDownloadURL(req.Context(), req.URL.String()); err != nil {
				return err
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
	if err := trackers.DownloadRegisteredTorrent(ctx, client, dlReq, outputPath); err != nil {
		return selection.ID, selection.GroupID, fmt.Errorf("trackers: BTN API registered torrent: %w", err)
	}
	return selection.ID, selection.GroupID, nil
}

// readBTNTorrentResponseBody rejects bodies beyond BTN's accepted torrent
// response cap before callers validate or persist the payload.
func readBTNTorrentResponseBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, btnTorrentMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read torrent response: %w", err)
	}
	if len(body) > btnTorrentMaxBytes {
		return nil, fmt.Errorf("torrent response exceeds %d bytes", btnTorrentMaxBytes)
	}
	return body, nil
}

// btnLoggedOutPage recognizes upload-page responses that prove the session is
// logged out and safe to classify as confirmed-invalid auth.
func btnLoggedOutPage(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "<form") && (strings.Contains(lower, "password") || strings.Contains(lower, "login.php")) ||
		strings.Contains(lower, "you must be logged in") ||
		strings.Contains(lower, "please log in")
}

// btnLooksLikeUploadPage recognizes enough upload-page structure to confirm a
// BTN session without depending on one exact page layout.
func btnLooksLikeUploadPage(body string) bool {
	lower := strings.ToLower(body)
	hasForm := strings.Contains(lower, "<form")
	hasUploadAction := strings.Contains(lower, "action=\"/upload.php") ||
		strings.Contains(lower, "action='/upload.php") ||
		strings.Contains(lower, "action=\"upload.php") ||
		strings.Contains(lower, "action='upload.php")
	hasFileInput := strings.Contains(lower, "name=\"file_input\"") ||
		strings.Contains(lower, "name='file_input'")
	hasAutofill := strings.Contains(lower, "name=\"autofill\"") ||
		strings.Contains(lower, "name='autofill'")
	return hasForm && (hasFileInput || (hasUploadAction && hasAutofill))
}

func resolveBTNAPIURL(cfg config.TrackerConfig) string {
	if cfg.Unknown != nil {
		if raw, ok := cfg.Unknown["api_url"]; ok {
			if value := strings.TrimSpace(fmt.Sprint(raw)); value != "" {
				return value
			}
		}
	}
	return btnAPIRPCURL
}

func resolveFastTorrent(cfg config.TrackerConfig) bool {
	if cfg.Unknown != nil {
		if raw, ok := cfg.Unknown["fast_torrent"]; ok {
			if b, ok := raw.(bool); ok {
				return b
			}
			if s, ok := raw.(string); ok {
				return strings.EqualFold(strings.TrimSpace(s), "true") || strings.TrimSpace(s) == "1"
			}
		}
	}
	return false
}

func stripHTML(value string) string {
	replacer := strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n")
	cleaned := replacer.Replace(value)
	cleaned = regexp.MustCompile(`(?s)<[^>]*>`).ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

// mapSource maps local source metadata to BTN's media dropdown. Autofill is
// used only when metadata does not resolve to a BTN-supported value.

// btnLookupIPAddrFunc matches net.Resolver lookups so tests can model DNS
// changes without relying on external name service.
type btnLookupIPAddrFunc func(context.Context, string) ([]net.IPAddr, error)

// btnAPIDownloadOrigin records the resolved BTN API origin that download URLs
// may reuse when they would otherwise fail public-address validation.
type btnAPIDownloadOrigin struct {
	scheme string
	host   string
	addrs  map[netip.Addr]struct{}
	lookup btnLookupIPAddrFunc
}

// resolveBTNURLAddrs resolves a URL host to unmapped IP addresses, preserving
// literal IP hosts without DNS.
func resolveBTNURLAddrs(ctx context.Context, parsed *url.URL, lookup btnLookupIPAddrFunc) ([]netip.Addr, error) {
	host := strings.TrimSpace(parsed.Hostname())
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}

	resolved, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve host %q: %w", host, err)
	}
	addrs := make([]netip.Addr, 0, len(resolved))
	for _, item := range resolved {
		if addr, ok := netip.AddrFromSlice(item.IP); ok {
			addrs = append(addrs, addr.Unmap())
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("host %q resolved to no usable addresses", host)
	}
	return addrs, nil
}

// validateBTNPublicResolvedAddrs rejects private, loopback, link-local,
// multicast, unspecified, and otherwise non-global-unicast addresses.
func validateBTNPublicResolvedAddrs(host string, addrs []netip.Addr) error {
	for _, addr := range addrs {
		if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() ||
			addr.IsUnspecified() {
			return fmt.Errorf("host %q resolved to blocked address %q", host, addr)
		}
	}
	return nil
}

type btnAPITorrentSelection struct {
	ID      string
	GroupID string
}

// selectBTNAPITorrent returns the BTN torrent that matches the uploaded
// release. It prefers an exact release-name match inside the uploaded group and
// only accepts a group-only match when that group has a single candidate.
func selectBTNAPITorrent(torrents map[string]map[string]any, releaseName string, groupID string) btnAPITorrentSelection {
	expectedRelease := normalizeBTNAPIMatchValue(releaseName)
	expectedGroup := strings.TrimSpace(groupID)

	ids := make([]string, 0, len(torrents))
	for id := range torrents {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	sortBTNAPITorrentIDs(ids)

	groupMatches := make([]string, 0, len(ids))
	for _, id := range ids {
		torrentData := torrents[id]
		if expectedGroup != "" {
			torrentGroup := btnAPITorrentGroupID(torrentData)
			if strings.TrimSpace(torrentGroup) != expectedGroup {
				continue
			}
		}
		groupMatches = append(groupMatches, id)
		if expectedRelease != "" && btnAPITorrentMatchesRelease(torrentData, expectedRelease) {
			return btnAPITorrentSelection{ID: id, GroupID: btnAPITorrentGroupID(torrentData)}
		}
	}
	if len(groupMatches) == 1 {
		return btnAPITorrentSelection{ID: groupMatches[0], GroupID: btnAPITorrentGroupID(torrents[groupMatches[0]])}
	}
	return btnAPITorrentSelection{}
}

// btnAPITorrentGroupID extracts BTN's group id from known API field spellings.
func btnAPITorrentGroupID(torrentData map[string]any) string {
	return metautil.FirstNonEmptyTrimmed(
		btnAPIStringField(torrentData, "GroupID"),
		btnAPIStringField(torrentData, "groupId"),
		btnAPIStringField(torrentData, "GroupId"),
		btnAPIStringField(torrentData, "group_id"),
	)
}

// sortBTNAPITorrentIDs orders API result ids deterministically, newest numeric
// ids first when all compared ids are numeric.
func sortBTNAPITorrentIDs(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		left, leftErr := strconv.Atoi(ids[i])
		right, rightErr := strconv.Atoi(ids[j])
		if leftErr == nil && rightErr == nil {
			return left > right
		}
		return ids[i] > ids[j]
	})
}

// btnAPITorrentMatchesRelease reports whether a known BTN API name field is an
// exact normalized match for the release name we uploaded.
func btnAPITorrentMatchesRelease(torrentData map[string]any, expectedRelease string) bool {
	for _, field := range []string{"ReleaseName", "releaseName", "TorrentName", "torrentName", "Name", "name", "Release", "release"} {
		if normalizeBTNAPIMatchValue(btnAPIStringField(torrentData, field)) == expectedRelease {
			return true
		}
	}
	return false
}

// btnAPIStringField returns an API field as a trimmed string. Missing or null
// fields produce an empty value; numeric fields keep their decimal form.
func btnAPIStringField(data map[string]any, field string) string {
	if data == nil {
		return ""
	}
	value, ok := data[field]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

// normalizeBTNAPIMatchValue canonicalizes BTN API comparison values for exact,
// case-insensitive release-name matching.
func normalizeBTNAPIMatchValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// validateBTNAPIDownloadURL applies public-address validation to a BTN API
// DownloadURL, but permits a same-origin private URL when the caller explicitly
// configured the API endpoint to that pinned private address. The pinned-origin
// exception keeps local test servers usable without allowing arbitrary private
// redirects or same-host DNS rebinding.
func validateBTNAPIDownloadURL(ctx context.Context, apiURL string, rawURL string) error {
	origin, err := newBTNAPIDownloadOrigin(ctx, apiURL)
	if err != nil {
		return err
	}
	return origin.validateDownloadURL(ctx, rawURL)
}

// validateDownloadURL accepts public HTTP(S) destinations and otherwise only
// permits same-origin destinations that still resolve to the pinned API address.
func (origin *btnAPIDownloadOrigin) validateDownloadURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return errors.New("missing host")
	}
	addrs, err := resolveBTNURLAddrs(ctx, parsed, origin.lookup)
	if err != nil {
		return err
	}
	publicErr := validateBTNPublicResolvedAddrs(host, addrs)
	lowerHost := strings.ToLower(host)
	if publicErr == nil && lowerHost != "localhost" && !strings.HasSuffix(lowerHost, ".localhost") && !strings.Contains(lowerHost, "%") {
		return nil
	}

	if !origin.sameOrigin(parsed) {
		if publicErr != nil {
			return publicErr
		}
		return fmt.Errorf("blocked private host %q", host)
	}

	for _, addr := range addrs {
		if _, ok := origin.addrs[addr]; !ok {
			return fmt.Errorf("blocked address %q not pinned to BTN API origin", addr)
		}
	}

	return nil
}

// checkBTNSeasonPackReservation enforces BTN's 2-hour internal season-pack
// reservation using the same canonical season source as the final upload
// payload, including TVDB/IMDb translated episode metadata.
func checkBTNSeasonPackReservation(ctx context.Context, uploadCtx uploadContext, req trackers.PreparationInput) error {
	if resolveUploadType(req.Meta) != "Season" {
		return nil
	}
	// The 2-hour reservation ONLY applies to season packs made of internal releases.
	if !isBTNInternalGroup(req.Meta) {
		return nil
	}

	tvdbID := req.Meta.Identity.TVDBID
	if tvdbID == 0 {
		return nil
	}

	group := strings.TrimPrefix(req.Meta.Tag, "-")
	if group == "" {
		return nil
	}

	filter := map[string]any{
		"tvdb":     strconv.Itoa(tvdbID),
		"category": "Episode",
		"group":    group,
	}

	// We only need the most recent episodes to check the 2-hour window
	torrentsMap, err := btnAPISearchTorrents(ctx, uploadCtx.apiURL, uploadCtx.apiToken, filter, 50)
	if err != nil {
		return err
	}

	seasonPrefix := ""
	season, _ := resolveBTNTVSeasonEpisode(req.Meta)
	if season > 0 {
		seasonPrefix = fmt.Sprintf("S%02dE", season)
	}

	var newestInternal time.Time
	for _, t := range torrentsMap {
		name, _ := t["ReleaseName"].(string)
		if seasonPrefix != "" && !strings.Contains(strings.ToUpper(name), seasonPrefix) {
			continue
		}

		tTime := parseBTNTimestamp(t["Time"])
		if tTime.After(newestInternal) {
			newestInternal = tTime
		}
	}

	if !newestInternal.IsZero() && time.Since(newestInternal) < 2*time.Hour {
		return errors.New("trackers: BTN 2-hour reservation period for internal season packs has not expired")
	}

	return nil
}

func btnAPISearchTorrents(ctx context.Context, apiURL, apiToken string, filter map[string]any, limit int) (map[string]map[string]any, error) {
	var response struct {
		Result struct {
			Torrents json.RawMessage `json:"torrents"`
		} `json:"result"`
	}
	if err := callBTNAPI(ctx, apiURL, "ua-btn-upload-check", "getTorrentsSearch", []any{apiToken, filter, limit}, &response); err != nil {
		return nil, err
	}

	if len(response.Result.Torrents) == 0 || string(response.Result.Torrents) == "false" || string(response.Result.Torrents) == "[]" {
		return nil, nil
	}

	var torrentsMap map[string]map[string]any
	if err := json.Unmarshal(response.Result.Torrents, &torrentsMap); err != nil {
		return nil, fmt.Errorf("trackers: BTN API parse torrents search response: %s", redaction.RedactValue(err.Error(), nil))
	}
	return torrentsMap, nil
}

func parseBTNTimestamp(val any) time.Time {
	var epoch int64
	switch v := val.(type) {
	case string:
		epoch, _ = strconv.ParseInt(v, 10, 64)
	case float64:
		epoch = int64(v)
	}
	if epoch > 0 {
		return time.Unix(epoch, 0)
	}
	return time.Time{}
}

func resolveBTNSameOriginURL(baseURL string, currentURL string, rawURL string) (*url.URL, bool) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, false
	}
	referenceBase := base
	if parsedCurrent, err := url.Parse(currentURL); err == nil && parsedCurrent.Scheme != "" && parsedCurrent.Host != "" {
		referenceBase = parsedCurrent
	}
	rawURL = strings.TrimSpace(html.UnescapeString(rawURL))
	if rawURL == "" {
		return nil, false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, false
	}
	resolved := referenceBase.ResolveReference(parsed)
	if !strings.EqualFold(resolved.Scheme, base.Scheme) || !strings.EqualFold(resolved.Host, base.Host) {
		return nil, false
	}
	return resolved, true
}

func newBTNAPIDownloadOrigin(ctx context.Context, apiURL string) (*btnAPIDownloadOrigin, error) {
	return newBTNAPIDownloadOriginWithLookup(ctx, apiURL, net.DefaultResolver.LookupIPAddr)
}

func newBTNAPIDownloadOriginWithLookup(ctx context.Context, apiURL string, lookup btnLookupIPAddrFunc) (*btnAPIDownloadOrigin, error) {
	parsed, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return nil, errors.New("missing host")
	}
	if strings.Contains(parsed.Hostname(), "%") {
		return nil, fmt.Errorf("blocked private host %q", parsed.Hostname())
	}
	addrs, err := resolveBTNURLAddrs(ctx, parsed, lookup)
	if err != nil {
		return nil, err
	}
	pinned := make(map[netip.Addr]struct{}, len(addrs))
	for _, addr := range addrs {
		pinned[addr] = struct{}{}
	}
	return &btnAPIDownloadOrigin{
		scheme: scheme,
		host:   parsed.Host,
		addrs:  pinned,
		lookup: lookup,
	}, nil
}

func (origin *btnAPIDownloadOrigin) sameOrigin(parsed *url.URL) bool {
	if origin == nil || parsed == nil {
		return false
	}
	return strings.EqualFold(origin.scheme, parsed.Scheme) &&
		strings.EqualFold(origin.host, parsed.Host) &&
		origin.host != ""
}

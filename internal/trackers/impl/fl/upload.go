// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL      = "https://filelist.io"
	loginPageURL = baseURL + "/login.php"
	loginURL     = baseURL + "/takelogin.php"
	uploadURL    = baseURL + "/takeupload.php"
	downloadURL  = baseURL + "/download.php?id="
)

var (
	validatorPattern = regexp.MustCompile(`name="validator"\s+value="([^"]+)"`)
	successPattern   = regexp.MustCompile(`details\.php\?id=(\d+)&uploaded=`)
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	questionnaire *api.TrackerQuestionnaire
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: FL %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file",
		FileName:  resolveTorrentFileName(req.Meta, state.releaseName) + ".torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return api.UploadSummary{}, err
	}
	jar, _ := cookiejar.New(nil)
	base, _ := url.Parse(baseURL)
	jar.SetCookies(base, cookies)
	client := httpclient.CloneWithTimeout(&http.Client{Jar: jar}, httpclient.DefaultTimeout)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FL request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FL upload request: %w", err)
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, _ := io.ReadAll(resp.Body)
	match := successPattern.FindStringSubmatch(finalURL)
	if len(match) >= 2 {
		id := match[1]
		artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "FL")
		if err != nil {
			return api.UploadSummary{}, err
		}
		if err := downloadPersonalizedTorrent(ctx, client, id, artifactPath); err != nil {
			return api.UploadSummary{}, err
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "FL",
				TorrentID:   id,
				TorrentURL:  baseURL + "/details.php?id=" + id,
				DownloadURL: downloadURL + id,
				TorrentPath: artifactPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "FL", "upload_failure", responseBody, ".html")
	return api.UploadSummary{}, fmt.Errorf("trackers: FL upload failed status=%d url=%s", resp.StatusCode, finalURL)
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
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
		Tracker:          "FL",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "fl",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, []*http.Cookie, error) {
	cookies, err := resolveCookies(ctx, req.Logger, req.TrackerConfig, req.AppConfig.MainSettings.DBPath, dryRun)
	if err != nil {
		return uploadState{}, nil, err
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(assets)
	name := resolveName(req.Meta, questionnaireAnswers(req.Meta))
	fields := map[string]string{
		"name":  name,
		"type":  strconv.Itoa(resolveCategoryID(req.Meta)),
		"descr": description,
		"nfo":   resolveMedia(req.Meta),
	}
	if req.Meta.ExternalIDs.IMDBID > 0 {
		fields["imdbid"] = strconv.Itoa(req.Meta.ExternalIDs.IMDBID)
		fields["description"] = resolveGenres(req.Meta)
	}
	if strings.TrimSpace(req.TrackerConfig.UploaderName) != "" && !req.TrackerConfig.Anon {
		fields["epenis"] = strings.TrimSpace(req.TrackerConfig.UploaderName)
	}
	if hasRomanianAudio(req.Meta) {
		fields["materialro"] = "on"
	}
	if strings.EqualFold(strings.TrimSpace(req.Meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(req.Meta.Type), "REMUX") || req.Meta.TVPack {
		fields["freeleech"] = "on"
	}
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   name,
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta, name),
	}
	if strings.TrimSpace(name) == "" {
		state.blockedReason = "missing release name"
	}
	return state, cookies, nil
}

func resolveCookies(ctx context.Context, logger api.Logger, cfg config.TrackerConfig, dbPath string, dryRun bool) ([]*http.Cookie, error) {
	loaded, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "FL", ".filelist.io")
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: LoadTrackerHTTPCookies failed for FL/.filelist.io, dbPath=%s: %v", dbPath, err)
		}
	} else if valid := validFLCookies(loaded); len(valid) > 0 {
		return valid, nil
	} else if logger != nil {
		logger.Debugf("trackers: FL loaded cookies were missing/expired, falling back to credential login, dbPath=%s", dbPath)
	}
	if dryRun {
		if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
			return nil, errors.New("trackers: FL cookies not found")
		}
		return []*http.Cookie{{Name: "dryrun", Value: "1", Domain: ".filelist.io", Path: "/"}}, nil
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: FL cookie invalid/missing and username/password not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	match := validatorPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return nil, errors.New("trackers: FL validator token not found")
	}
	data := url.Values{}
	data.Set("validator", match[1])
	data.Set("username", strings.TrimSpace(cfg.Username))
	data.Set("password", strings.TrimSpace(cfg.Password))
	data.Set("unlock", "1")
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := httpclient.New(httpclient.DefaultTimeout)
	loginResp, err := client.Do(loginReq)
	if err != nil {
		return nil, err
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode < 200 || loginResp.StatusCode >= 400 {
		return nil, fmt.Errorf("trackers: FL login failed status=%d", loginResp.StatusCode)
	}
	return loginResp.Cookies(), nil
}

func validFLCookies(values []*http.Cookie) []*http.Cookie {
	now := time.Now()
	valid := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		if cookie == nil {
			continue
		}
		if strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		if !cookie.Expires.IsZero() && cookie.Expires.Before(now) {
			continue
		}
		valid = append(valid, cookie)
	}
	return valid
}

func downloadPersonalizedTorrent(ctx context.Context, client *http.Client, id string, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL+id, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("trackers: FL read torrent response: %w", err)
	}
	if err := os.WriteFile(outputPath, body, 0o600); err != nil {
		return fmt.Errorf("trackers: FL write torrent output: %w", err)
	}
	return nil
}

func buildDescription(assets trackers.DescriptionAssets) string {
	return bbcode.FinalizeTrackerDescription("FL", strings.TrimSpace(assets.Description))
}

func buildQuestionnaire(meta api.PreparedMetadata, computedName string) *api.TrackerQuestionnaire {
	answers := questionnaireAnswers(meta)
	return &api.TrackerQuestionnaire{Tracker: "FL", Fields: []api.TrackerQuestionnaireField{{
		Key: "name", Label: "FileList Name", Kind: "text", Value: firstNonEmpty(strings.TrimSpace(answers["name"]), computedName), Required: true,
	}}}
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["FL"]
}

func resolveName(meta api.PreparedMetadata, answers map[string]string) string {
	if answers != nil && strings.TrimSpace(answers["name"]) != "" {
		return strings.TrimSpace(answers["name"])
	}
	name := strings.TrimSpace(meta.ReleaseName)
	name = strings.ReplaceAll(name, " DV ", " DoVi ")
	name = strings.ReplaceAll(name, "BluRay REMUX", "Remux")
	name = strings.ReplaceAll(name, "BluRay Remux", "Remux")
	name = strings.ReplaceAll(name, "PQ10", "HDR")
	name = strings.ReplaceAll(name, "HDR10+", "HDR")
	name = strings.ReplaceAll(name, "DD+", "DDP")
	name = strings.Join(strings.Fields(name), ".")
	return strings.Trim(name, ".")
}

func resolveTorrentFileName(meta api.PreparedMetadata, releaseName string) string {
	if meta.Anime && strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-")), "SubsPlease") {
		return releaseName
	}
	return strings.TrimSuffix(strings.TrimSpace(meta.Filename), filepath.Ext(strings.TrimSpace(meta.Filename)))
}

func resolveCategoryID(meta api.PreparedMetadata) int {
	if meta.Anime {
		return 24
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	switch {
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD"):
		if hasRomanianSub(meta) {
			return 3
		}
		return 2
	case category == "TV":
		if resolution == "2160p" {
			return 27
		}
		if isSD(meta) {
			return 23
		}
		return 21
	default:
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
			if resolution == "2160p" {
				return 26
			}
			return 20
		}
		if resolution == "2160p" {
			return 6
		}
		if isSD(meta) {
			return 1
		}
		if hasRomanianSub(meta) {
			return 19
		}
		return 4
	}
}

func hasRomanianAudio(meta api.PreparedMetadata) bool {
	for _, lang := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "romanian" || lower == "ro" {
			return true
		}
	}
	return false
}

func hasRomanianSub(meta api.PreparedMetadata) bool {
	for _, lang := range meta.SubtitleLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "romanian" || lower == "ro" {
			return true
		}
	}
	return false
}

func resolveMedia(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if summary, ok := meta.BDInfo["summary"].(string); ok {
			return strings.TrimSpace(summary)
		}
	}
	return firstNonEmpty(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
}

func resolveGenres(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres)
	}
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres)
	}
	return strings.TrimSpace(meta.Release.Genre)
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.TrimSpace(meta.ExternalIDs.Category); category != "" {
		return category
	}
	return strings.TrimSpace(meta.MediaInfoCategory)
}

func isSD(meta api.PreparedMetadata) bool {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	return resolution == "480p" || resolution == "576p"
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

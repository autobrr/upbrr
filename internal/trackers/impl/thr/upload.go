// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL              = "https://www.torrenthr.org"
	loginPagePath        = "/login.php"
	takeLoginPath        = "/takelogin.php"
	uploadURL            = baseURL + "/takeupload.php"
	sourceFlag           = "[https://www.torrenthr.org] TorrentHR.org"
	loginResponseMaxSize = 1024 * 1024
)

// ErrLoginFailed marks a completed THR login exchange that did not produce
// authenticated index-page evidence.
var ErrLoginFailed = errors.New("trackers: THR login failed")

var (
	subtitleMap = map[string]string{
		"croatian":  "1",
		"english":   "2",
		"bosnian":   "3",
		"serbian":   "4",
		"slovenian": "5",
	}
	idPattern = regexp.MustCompile(`id=(\d+)`)
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	questionnaire *api.TrackerQuestionnaire
}

func upload(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	state, client, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{
		{
			FieldName: "tfile",
			FileName:  state.releaseName + ".torrent",
			Path:      state.torrentPath,
		},
		{
			FieldName: "nfo",
			FileName:  "MEDIAINFO.txt",
			Content:   []byte(commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath))),
		},
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: THR build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: THR upload request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if strings.Contains(finalURL, "uploaded=1") {
		torrentID := ""
		if match := idPattern.FindStringSubmatch(finalURL); len(match) == 2 {
			torrentID = match[1]
		}
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "THR")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, finalURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "THR",
			TorrentID:   torrentID,
			TorrentURL:  finalURL,
			DownloadURL: finalURL,
			TorrentPath: artifactPath,
		}}}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "THR", "upload_failure", bodyBytes, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("THR", resp.StatusCode, bodyBytes)
}

func buildUploadDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	return api.TrackerDryRunEntry{
		Tracker:          "THR",
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "thr",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "tfile",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, *http.Client, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req.Meta, assets)
	releaseName := resolveName(req.Meta)
	if override := strings.TrimSpace(questionnaireAnswers(req.Meta)["name_override"]); override != "" {
		releaseName = override
	}
	fields := map[string]string{
		"name":  releaseName,
		"descr": description,
		"type":  resolveCategory(req.Meta),
		"url":   imdbURL(req.Meta),
		"tube":  strings.TrimSpace(req.Meta.ProviderMetadata.TMDB.YouTube),
	}
	for idx, sub := range resolveSubtitles(req.Meta) {
		fields["subs["+strconv.Itoa(idx)+"]"] = sub
	}
	client, err := login(ctx, req.TrackerConfig)
	if err != nil {
		return uploadState{}, nil, err
	}
	return uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta),
	}, client, nil
}

func login(ctx context.Context, cfg config.TrackerConfig) (*http.Client, error) {
	return LoginSession(ctx, cfg)
}

// LoginSession performs THR's browser-style login flow. It first loads the
// login page into a cookie jar, carries all non-empty hidden form fields into
// the credential POST, follows the redirect to the authenticated page, and
// returns the live client only after the final index page contains a structured
// logout link on the same scheme and hostname.
func LoginSession(ctx context.Context, cfg config.TrackerConfig) (*http.Client, error) {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: THR missing username/password")
	}
	resolvedBaseURL := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if resolvedBaseURL == "" {
		resolvedBaseURL = baseURL
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	loginPageURL := resolvedBaseURL + loginPagePath
	pageReq, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR build login page request: %w", err)
	}
	pageReq.Header.Set("User-Agent", "upbrr")
	pageResp, err := client.Do(pageReq)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR login page request: %w", err)
	}
	pageBody, readErr := readLoginResponse(pageResp)
	pageResp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("trackers: THR read login page: %w", readErr)
	}
	if pageResp.StatusCode < 200 || pageResp.StatusCode >= 300 {
		return nil, fmt.Errorf("trackers: THR login page failed status=%d", pageResp.StatusCode)
	}

	form := hiddenLoginFields(pageBody)
	form.Set("username", strings.TrimSpace(cfg.Username))
	form.Set("password", strings.TrimSpace(cfg.Password))
	form.Set("ssl", "yes")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolvedBaseURL+takeLoginPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: THR build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	req.Header.Set("Referer", loginPageURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: THR login request: %w", err)
	}
	body, readErr := readLoginResponse(resp)
	resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("trackers: THR read login response: %w", readErr)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: status=%d", ErrLoginFailed, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("trackers: THR login response status=%d", resp.StatusCode)
	}
	var finalURL *url.URL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	if !isAuthenticatedLoginPage(finalURL, body) {
		return nil, fmt.Errorf("%w: authenticated marker not found", ErrLoginFailed)
	}
	return client, nil
}

// isAuthenticatedLoginPage requires THR's post-login index path and a parsed
// logout anchor on the same scheme and hostname. Path fragments or incidental
// response text cannot independently prove authentication.
func isAuthenticatedLoginPage(finalURL *url.URL, body []byte) bool {
	if finalURL == nil || !strings.EqualFold(strings.Trim(finalURL.Path, "/"), "index.php") {
		return false
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "a") {
				continue
			}
			for _, attr := range token.Attr {
				if !strings.EqualFold(attr.Key, "href") {
					continue
				}
				candidate, err := url.Parse(strings.TrimSpace(attr.Val))
				if err != nil || !strings.EqualFold(strings.TrimPrefix(candidate.Path, "/"), "logout.php") {
					continue
				}
				if candidate.Scheme != "" && !strings.EqualFold(candidate.Scheme, finalURL.Scheme) {
					continue
				}
				if candidate.Host != "" && !strings.EqualFold(candidate.Hostname(), finalURL.Hostname()) {
					continue
				}
				return true
			}
		case html.TextToken, html.EndTagToken, html.CommentToken, html.DoctypeToken:
			continue
		}
	}
}

func readLoginResponse(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("empty response")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, loginResponseMaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > loginResponseMaxSize {
		return nil, fmt.Errorf("response exceeds %d bytes", loginResponseMaxSize)
	}
	return body, nil
}

func hiddenLoginFields(body []byte) url.Values {
	fields := url.Values{}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return fields
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "input") {
				continue
			}
			name, value, inputType := "", "", ""
			for _, attr := range token.Attr {
				switch {
				case strings.EqualFold(attr.Key, "name"):
					name = strings.TrimSpace(attr.Val)
				case strings.EqualFold(attr.Key, "value"):
					value = attr.Val
				case strings.EqualFold(attr.Key, "type"):
					inputType = strings.TrimSpace(attr.Val)
				}
			}
			if strings.EqualFold(inputType, "hidden") && name != "" && value != "" {
				fields.Set(name, value)
			}
		case html.TextToken, html.EndTagToken, html.CommentToken, html.DoctypeToken:
			continue
		}
	}
}

func buildDescription(meta api.UploadSubject, assets trackers.DescriptionAssets) string {
	parts := []string{
		"[quote=Info]",
		"Name: " + strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName)),
		"",
		"Overview: " + strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.EpisodeOverview, meta.ProviderMetadata.TMDB.Overview)),
		"",
		metautil.FirstNonEmptyTrimmed(meta.Release.Resolution, meta.Release.Source) + " / " + strings.TrimSpace(meta.Type),
		"",
		"Category: " + categoryName(meta),
	}
	if tmdb := meta.Identity.TMDBID; tmdb > 0 {
		parts = append(parts, fmt.Sprintf("TMDB: https://www.themoviedb.org/%s/%d", strings.ToLower(categoryName(meta)), tmdb))
	}
	if imdb := imdbURL(meta); imdb != "" {
		parts = append(parts, "IMDb: "+imdb)
	}
	parts = append(parts, "[/quote]")
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, base)
	}
	for _, image := range assets.Screenshots {
		raw := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL))
		if raw != "" {
			parts = append(parts, "[img]"+raw+"[/img]")
		}
	}
	parts = append(parts, `[size=2][url=https://www.torrenthr.org/forums.php?action=viewtopic&topicid=8977]upbrr[/url][/size]`)
	return finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n")))
}

func buildQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	return &api.TrackerQuestionnaire{
		Tracker: "THR",
		Fields: []api.TrackerQuestionnaireField{{
			Key:      "name_override",
			Label:    "Upload Name",
			Kind:     "text",
			Value:    resolveName(meta),
			Required: true,
		}},
	}
}

func resolveName(meta api.UploadSubject) string {
	base := strings.ReplaceAll(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename), "DD+", "DDP")
	return regexp.MustCompile(`[^0-9a-zA-Z. '\-\[\]]+`).ReplaceAllString(base, " ")
}

func resolveCategory(meta api.UploadSubject) string {
	if _, err := meta.Identity.RequireCategory(); err != nil {
		return ""
	}
	if containsWord(genresText(meta), "documentary") || containsWord(keywordsText(meta), "documentary") {
		return "12"
	}
	switch categoryName(meta) {
	case "MOVIE":
		if strings.EqualFold(meta.DiscType, "BDMV") {
			return "40"
		}
		if strings.EqualFold(meta.DiscType, "DVD") || strings.EqualFold(meta.DiscType, "HDDVD") {
			return "14"
		}
		if isSD(meta.Release.Resolution) {
			return "4"
		}
		return "17"
	case "TV":
		if isSD(meta.Release.Resolution) {
			return "7"
		}
		return "34"
	default:
		if meta.Anime {
			return "31"
		}
	}
	return ""
}

func resolveSubtitles(meta api.UploadSubject) []string {
	result := make([]string, 0, len(meta.SubtitleLanguages))
	seen := map[string]struct{}{}
	for _, lang := range meta.SubtitleLanguages {
		id := subtitleMap[strings.ToLower(strings.TrimSpace(lang))]
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d/", meta.Identity.IMDBID)
}

func questionnaireAnswers(meta api.UploadSubject) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["THR"]
}

func isSD(res string) bool {
	return strings.HasPrefix(res, "480") || strings.HasPrefix(res, "576") || strings.HasPrefix(res, "540")
}

func containsWord(a string, b string) bool {
	return strings.Contains(strings.ToLower(a), strings.ToLower(b))
}

func cloneFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	maps.Copy(out, input)
	return out
}

func categoryName(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	if category == api.CanonicalCategoryTV {
		return "TV"
	}
	return "MOVIE"
}

func genresText(meta api.UploadSubject) string {
	return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Genres, meta.Release.Genre)
}

func keywordsText(meta api.UploadSubject) string {
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
}

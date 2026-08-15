// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/rls"
	xhtml "golang.org/x/net/html"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

// buildBTNAutofillPayload returns the form fields for BTN's first upload.php
// POST. TVDB-backed uploads submit the series id plus season or episode token;
// scene-name autofill is used only when no TVDB series id is available.
func buildBTNAutofillPayload(meta api.UploadSubject, releaseName string) (url.Values, string) {
	if meta.Identity.TVDBID <= 0 {
		return buildBTNReleaseNameAutofillPayload(meta, releaseName)
	}
	autofillPayload := url.Values{}
	uploadType := resolveUploadType(meta)
	season, episode := resolveBTNTVSeasonEpisode(meta)
	autofillPayload.Set("type", uploadType)
	autofillPayload.Set("tvdb", "Get Info")

	autofillPayload.Set("scene_yesno", "No")
	autofillPayload.Set("auto_series", strconv.Itoa(meta.Identity.TVDBID))

	if uploadType == "Episode" {
		autofillPayload.Set("auto_title", fmt.Sprintf("S%02dE%02d", season, episode))
	} else {
		autofillPayload.Set("auto_season", fmt.Sprintf("S%02d", season))
	}

	return autofillPayload, uploadType
}

// buildBTNReleaseNameAutofillPayload forces BTN's scene-name autofill even
// when upbrr has a TVDB series ID.
func buildBTNReleaseNameAutofillPayload(meta api.UploadSubject, releaseName string) (url.Values, string) {
	uploadType := resolveUploadType(meta)
	autofillPayload := url.Values{}
	autofillPayload.Set("type", uploadType)
	autofillPayload.Set("tvdb", "Get Info")
	autofillPayload.Set("scene_yesno", "Yes")
	autofillPayload.Set("autofill", strings.TrimSpace(releaseName))
	return autofillPayload, uploadType
}

func prepareUploadDataWithAutofill(
	ctx context.Context,
	req trackers.PreparationInput,
	uploadCtx uploadContext,
	releaseNameAutofill bool,
) (map[string]string, error) {
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

	var autofillPayload url.Values
	var uploadType string
	if releaseNameAutofill {
		autofillPayload, uploadType = buildBTNReleaseNameAutofillPayload(req.Meta, releaseName)
	} else {
		autofillPayload, uploadType = buildBTNAutofillPayload(req.Meta, releaseName)
	}
	fields, err := requestBTNAutofillFields(ctx, uploadCtx, autofillPayload, uploadType)
	if err != nil {
		return nil, err
	}
	return buildBTNUploadPayload(req, fields)
}

// preferredBTNTVDBSeriesName returns the English TVDB name, falling back to the native name.
func preferredBTNTVDBSeriesName(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TVDB == nil {
		return ""
	}
	if name := strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish); name != "" {
		return name
	}
	return strings.TrimSpace(meta.ProviderMetadata.TVDB.Name)
}

// btnAutofillArtistAction requests confirmation unless BTN's artist exactly matches a TVDB name or alias.
// Missing TVDB identity or primary-name metadata disables the check.
func btnAutofillArtistAction(meta api.UploadSubject, artist string, releaseNameTried bool) *api.RequiredAction {
	if meta.Identity.TVDBID <= 0 || meta.ProviderMetadata.TVDB == nil {
		return nil
	}
	artist = strings.TrimSpace(artist)
	names := append(
		[]string{meta.ProviderMetadata.TVDB.Name, meta.ProviderMetadata.TVDB.NameEnglish},
		meta.ProviderMetadata.TVDB.Aliases...,
	)
	for _, name := range names {
		if artist == strings.TrimSpace(name) && artist != "" {
			return nil
		}
	}
	expected := preferredBTNTVDBSeriesName(meta)
	if expected == "" {
		return nil
	}
	prompt := "BTN autofill returned series %q, but upbrr tvdb is a different series %q. Continue uploading this result to BTN?"
	declineLabel := "Try release-name autofill"
	if releaseNameTried {
		prompt = "BTN release-name autofill also returned series %q, but upbrr tvdb is a different series %q. Continue uploading this result to BTN?"
		declineLabel = "Skip BTN"
	}
	return &api.RequiredAction{
		Kind:   api.RequiredActionResolveTrackerPreparation,
		Prompt: fmt.Sprintf(prompt, artist, expected),
		Options: []api.RequiredActionOption{
			{Value: "confirm", Label: "Continue upload"},
			{Value: "resolve", Label: declineLabel},
		},
	}
}

// requestBTNAutofillFields performs BTN's autofill POST and extracts the form
// values returned for the final upload payload. A validation failure means BTN
// did not return enough series/title data for the requested upload type.
func requestBTNAutofillFields(
	ctx context.Context,
	uploadCtx uploadContext,
	autofillPayload url.Values,
	uploadType string,
) (map[string]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadCtx.uploadURL, strings.NewReader(autofillPayload.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN autofill request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := uploadCtx.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN autofill request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("trackers: BTN autofill failed status=%d", resp.StatusCode)
	}
	htmlPayload, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN read autofill response: %w", err)
	}
	fields := extractAutofillFields(string(htmlPayload))
	if !validateAutofill(fields, uploadType) {
		return nil, errors.New("trackers: BTN autofill validation failed")
	}
	return fields, nil
}

// logBTNAutofillMismatch records when BTN autofill selected a different
// dropdown value than local metadata. The upload still uses metadata because
// autofill runs before MediaInfo or final upload fields are submitted.
func logBTNAutofillMismatch(logger api.Logger, field string, metadataValue string, autofillValue string) {
	if logger == nil {
		return
	}
	field = strings.TrimSpace(field)
	metadataValue = strings.TrimSpace(metadataValue)
	autofillValue = strings.TrimSpace(autofillValue)
	if field == "" || metadataValue == "" || autofillValue == "" || metadataValue == autofillValue {
		return
	}
	logger.Infof("trackers: BTN autofill %s mismatch metadata_%s=%q autofill_%s=%q decision=metadata", field, field, metadataValue, field, autofillValue)
}

// extractAutofillFields reads BTN's autofilled upload form into the field map
// consumed by final payload construction. Input values, selected dropdown
// values, and the album_desc textarea are normalized to lower-case field names.
func extractAutofillFields(htmlRaw string) map[string]string {
	fields := map[string]string{}
	for _, match := range btnInputPattern.FindAllStringSubmatch(htmlRaw, -1) {
		if len(match) < 3 {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(match[1]))] = html.UnescapeString(strings.TrimSpace(match[2]))
	}
	if match := btnTextAreaPattern.FindStringSubmatch(htmlRaw); len(match) > 1 {
		fields["album_desc"] = html.UnescapeString(strings.TrimSpace(stripHTML(match[1])))
	}
	for _, selectMatch := range btnSelectPattern.FindAllStringSubmatch(htmlRaw, -1) {
		if len(selectMatch) < 3 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(selectMatch[1]))
		body := selectMatch[2]
		if value := selectedBTNAutofillOptionValue(body); value != "" {
			fields[name] = value
		}
	}
	return fields
}

// selectedBTNAutofillOptionValue returns the selected option value regardless
// of attribute order, falling back to the first option value when no option is
// marked selected.
func selectedBTNAutofillOptionValue(body string) string {
	tokenizer := xhtml.NewTokenizer(strings.NewReader(body))
	firstValue := ""
	for {
		switch tokenizer.Next() {
		case xhtml.ErrorToken:
			return firstValue
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "option") {
				continue
			}
			value := ""
			selected := false
			for _, attr := range token.Attr {
				switch {
				case strings.EqualFold(attr.Key, "value"):
					value = html.UnescapeString(strings.TrimSpace(attr.Val))
				case strings.EqualFold(attr.Key, "selected"):
					selected = true
				}
			}
			if value == "" {
				continue
			}
			if firstValue == "" {
				firstValue = value
			}
			if selected {
				return value
			}
		case xhtml.TextToken, xhtml.EndTagToken, xhtml.CommentToken, xhtml.DoctypeToken:
			continue
		}
	}
}

// validateAutofill reports whether BTN returned the minimum autofill fields
// needed for the requested upload type. Episodes require a title; season packs
// only require a series artist.
func validateAutofill(fields map[string]string, uploadType string) bool {
	artist := strings.TrimSpace(fields["artist"])
	title := strings.TrimSpace(fields["title"])
	if artist == "" {
		return false
	}
	if uploadType == "Episode" && title == "" {
		return false
	}
	if strings.EqualFold(artist, "autofill fail") || strings.EqualFold(title, "autofill fail") {
		return false
	}
	return true
}

func resolveBTNSceneNFOPath(meta api.UploadSubject) string {
	if !isBTNSceneRelease(meta) {
		return ""
	}
	nfoPath := strings.TrimSpace(meta.SceneNFOPath)
	if nfoPath == "" {
		return ""
	}
	if info, err := os.Stat(nfoPath); err == nil && !info.IsDir() {
		return nfoPath
	}
	return ""
}

func validateBTNTVPayloadMetadata(meta api.UploadSubject) (string, error) {
	message := btnTVPayloadMetadataMessage(meta)
	if message == "" {
		return "", nil
	}
	return message, errors.New("trackers: BTN " + message)
}

func resolveBTNTVSeasonEpisode(meta api.UploadSubject) (int, int) {
	season, episode := meta.CanonicalSeasonEpisode()
	tvdb := meta.ProviderMetadata.TVDB
	imdbEpisode := preferredBTNIMDBEpisode(meta)
	if tvdb != nil && tvdb.EpisodeSeason > 0 {
		season = meta.ProviderMetadata.TVDB.EpisodeSeason
	} else if imdbEpisode != nil && imdbEpisode.Season > 0 {
		season = imdbEpisode.Season
	}
	if tvdb != nil && tvdb.EpisodeNumber > 0 {
		episode = meta.ProviderMetadata.TVDB.EpisodeNumber
	} else if imdbEpisode != nil {
		if imdbEpisodeNumber := btnIMDBEpisodeNumber(imdbEpisode.EpisodeText); imdbEpisodeNumber > 0 {
			episode = imdbEpisodeNumber
		}
	}
	return season, episode
}

func btnTVPayloadMetadataMessage(meta api.UploadSubject) string {
	if !strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), "TV") {
		return ""
	}
	missing := make([]string, 0, 2)
	ignored := make([]string, 0, 2)
	season, episode := resolveBTNTVSeasonEpisode(meta)
	if season <= 0 {
		missing = append(missing, "season")
		if meta.Release.Season > 0 {
			ignored = append(ignored, "season")
		}
	}
	if episode <= 0 && !meta.TVPack {
		missing = append(missing, "episode")
		if meta.Release.Episode > 0 {
			ignored = append(ignored, "episode")
		}
	}
	if len(missing) == 0 {
		return ""
	}
	message := "canonical TV " + strings.Join(missing, "/") + " missing; BTN upload requires TVDB or metadata season/episode ints"
	if len(ignored) > 0 {
		message += " and ignores parsed " + strings.Join(ignored, "/") + " fallback"
	}
	message += "; refresh metadata or correct canonical season/episode before upload"
	return message
}

func seasonPackHasMixedGroups(meta api.UploadSubject) bool {
	if !meta.TVPack || len(meta.FileList) < 2 {
		return false
	}

	groups := make(map[string]struct{})
	for _, file := range meta.FileList {
		base := pathutil.Base(strings.TrimSpace(file))
		if base == "" || base == "." || base == "/" {
			continue
		}
		parsed := rls.ParseString(base)
		group := strings.TrimSpace(parsed.Group)
		if group == "" {
			site := strings.TrimSpace(parsed.Site)
			if site != "" && strings.HasPrefix(strings.TrimSpace(base), "["+site+"]") {
				group = site
			}
		}
		group = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(group, "-")))
		switch group {
		case "", "nogrp", "nogroup", "unknown", "unk":
			continue
		}
		groups[group] = struct{}{}
		if len(groups) > 1 {
			return true
		}
	}
	return false
}

func resolveBTNUploadFiles(meta api.UploadSubject, torrentPath string) []commonhttp.FileField {
	files := []commonhttp.FileField{{
		FieldName: "file_input",
		Path:      torrentPath,
		FileName:  "torrent.torrent",
	}}
	if nfoPath := resolveBTNSceneNFOPath(meta); nfoPath != "" {
		files = append(files, commonhttp.FileField{
			FieldName: "nfo",
			Path:      nfoPath,
			FileName:  filepath.Base(nfoPath),
		})
	}
	return files
}

func resolveBTNDryRunFiles(meta api.UploadSubject, torrentPath string) []api.TrackerDryRunFile {
	files := []api.TrackerDryRunFile{{
		Field:   "file_input",
		Path:    torrentPath,
		Present: strings.TrimSpace(torrentPath) != "",
	}}
	if nfoPath := resolveBTNSceneNFOPath(meta); nfoPath != "" {
		files = append(files, api.TrackerDryRunFile{
			Field:   "nfo",
			Path:    nfoPath,
			Present: true,
		})
	}
	return files
}

func isBTNSceneRelease(meta api.UploadSubject) bool {
	return meta.Scene || strings.TrimSpace(meta.SceneName) != ""
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildUploadFields(req trackers.PreparationInput, auth string, description string) (map[string]string, error) {
	meta := req.Meta
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return nil, fmt.Errorf("trackers: MTV release name: %w", err)
	}
	anon := "1"
	if !req.TrackerConfig.Anon {
		anon = "0"
	}
	return map[string]string{
		"image":               "",
		"title":               releaseName,
		"category":            resolveCategoryID(meta),
		"Resolution":          resolveResolutionID(meta),
		"source":              resolveSourceID(meta),
		"origin":              resolveOriginID(meta),
		"taglist":             resolveTags(meta),
		"desc":                strings.TrimSpace(description),
		"groupDesc":           resolveGroupDescription(meta),
		"ignoredupes":         "1",
		"genre_tags":          "---",
		"autocomplete_toggle": "on",
		"fontfont":            "-1",
		"fontsize":            "-1",
		"auth":                auth,
		"anonymous":           anon,
		"submit":              "true",
	}, nil
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: MTV write multipart field %q: %w", key, err)
		}
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: MTV open torrent file: %w", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file_input", "[MTV].torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: MTV create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: MTV copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: MTV close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveTags(meta api.UploadSubject) string {
	tags := make([]string, 0, 12)
	resolution := strings.ToLower(strings.TrimSpace(resolveResolution(meta)))
	if resolution != "" {
		tags = append(tags, resolution)
	}
	switch {
	case isSD(meta):
		tags = append(tags, "sd")
	case resolution == "2160p" || resolution == "4320p":
		tags = append(tags, "uhd")
	default:
		tags = append(tags, "hd")
	}
	if service := strings.TrimSpace(meta.ServiceLongName); service != "" {
		svc := strings.ToLower(strings.ReplaceAll(service, " ", "."))
		svc = strings.ReplaceAll(svc, "+", "plus")
		tags = append(tags, svc+".source")
	}
	switch category := resolveCategory(meta); category {
	case "TV":
		switch {
		case meta.TVPack && isSD(meta):
			tags = append(tags, "sd.season")
		case meta.TVPack:
			tags = append(tags, "hd.season")
		case isSD(meta):
			tags = append(tags, "sd.episode")
		default:
			tags = append(tags, "hd.episode")
		}
	case "MOVIE":
		if isSD(meta) {
			tags = append(tags, "sd.movie")
		} else {
			tags = append(tags, "hd.movie")
		}
	}
	audio := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(meta.Audio, "+", "p"), "-", "."), " ", "."))
	for _, token := range []string{"dd", "ddp", "aac", "truehd", "mp3", "mp2", "dts", "dts.hd", "dts.x"} {
		if strings.Contains(audio, token) {
			tags = append(tags, token+".audio")
			break
		}
	}
	if strings.Contains(strings.ToLower(meta.Audio), "atmos") {
		tags = append(tags, "atmos.audio")
	}
	codec := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(meta.VideoCodec), "avc", "h264"), "hevc", "h265")
	codec = strings.ReplaceAll(codec, "-", "")
	if strings.TrimSpace(codec) != "" {
		tags = append(tags, strings.TrimSpace(codec))
	}
	if tag := strings.TrimSpace(meta.Tag); tag != "" {
		tags = append(tags, strings.TrimPrefix(strings.ReplaceAll(tag, " ", "."), "-")+".release")
	} else {
		tags = append(tags, "NOGRP.release")
	}
	if meta.Scene {
		tags = append(tags, "scene.group.release")
	} else {
		tags = append(tags, "p2p.group.release")
	}
	return strings.Join(tags, " ")
}

func resolveGroupDescription(meta api.UploadSubject) string {
	parts := make([]string, 0, 5)
	if meta.ProviderMetadata.IMDB != nil {
		if imdbURL := strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL); imdbURL != "" {
			parts = append(parts, imdbURL)
		}
	}
	if meta.Identity.TMDBID != 0 {
		category := strings.ToLower(strings.TrimSpace(resolveCategory(meta)))
		if category == "" {
			category = "movie"
		}
		parts = append(parts, "https://www.themoviedb.org/"+category+"/"+strconv.Itoa(meta.Identity.TMDBID))
	}
	if strings.EqualFold(resolveCategory(meta), "TV") && meta.Identity.TVDBID != 0 {
		parts = append(parts, "https://www.thetvdb.com/?id="+strconv.Itoa(meta.Identity.TVDBID))
	}
	if meta.Identity.TVmazeID != 0 {
		parts = append(parts, "https://www.tvmaze.com/shows/"+strconv.Itoa(meta.Identity.TVmazeID))
	}
	if meta.Identity.MALID != 0 {
		parts = append(parts, "https://myanimelist.net/anime/"+strconv.Itoa(meta.Identity.MALID))
	}
	return strings.Join(parts, "\n")
}

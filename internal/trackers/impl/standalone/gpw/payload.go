// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"maps"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func previewFields(form map[string][]string) map[string]string {
	fields := make(map[string]string, len(form))
	for key, values := range form {
		fields[key] = strings.Join(values, ",")
	}
	return fields
}

func buildForm(req trackers.PreparationInput, description, groupID string, answers map[string]string) map[string][]string {
	scalars := buildScalarFields(req, req.TrackerConfig, description, groupID, answers)
	form := make(map[string][]string, len(scalars)+2)
	for key, value := range scalars {
		form[key] = []string{value}
	}
	form["subtitles[]"] = resolveSubtitles(req.Meta)
	// GPW unconditionally joins this array, including subtitle_type=3.
	if len(form["subtitles[]"]) == 0 {
		form["subtitles[]"] = []string{""}
	}
	if groupID == "" {
		tags, explicit := answers["tags"]
		if !explicit {
			tags = resolveTags(req.Meta)
		}
		for tag := range strings.SplitSeq(tags, ",") {
			if tag = strings.TrimSpace(strings.ToLower(tag)); tag != "" {
				form["tags[]"] = append(form["tags[]"], tag)
			}
		}
	}
	return form
}

func otherDetail(enum, detail string) string {
	if enum == "Other" {
		return strings.TrimSpace(detail)
	}
	return ""
}

func resolveProviderPair(meta api.UploadSubject, answers map[string]string) (string, string) {
	source, explicit := answers["data_source"]
	source = strings.ToLower(strings.TrimSpace(source))
	if !explicit {
		switch {
		case meta.Identity.IMDBID > 0:
			source = "imdb"
		case meta.Identity.TMDBID > 0:
			source = "tmdb"
		default:
			source = "manual"
		}
	}
	identifier, explicitID := answers["identifier"]
	if !explicitID {
		switch source {
		case "imdb":
			if meta.Identity.IMDBID > 0 {
				identifier = providerid.IMDb(meta.Identity.IMDBID).Prefixed()
			}
		case "tmdb":
			if meta.Identity.TMDBID > 0 {
				identifier = strconv.Itoa(meta.Identity.TMDBID)
			}
		}
	}
	return source, strings.TrimSpace(identifier)
}

func buildScalarFields(
	req trackers.PreparationInput,
	trackerCfg config.TrackerConfig,
	description string,
	groupID string,
	answers map[string]string,
) map[string]string {
	meta := req.Meta
	edition, customEdition := editionFields(meta)
	fields := map[string]string{
		"codec_other":               otherDetail(resolveCodec(meta), codecFact(meta)),
		"codec":                     resolveCodec(meta),
		"container_other":           otherDetail(resolveContainer(meta), meta.Container),
		"container":                 resolveContainer(meta),
		"mediainfo[]":               trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, meta),
		"movie_edition_information": onOff(edition != "" || customEdition != ""),
		"processing_other":          "",
		"processing":                resolveProcessing(meta),
		"release_desc":              description,
		"remaster_custom_title":     customEdition,
		"remaster_title":            edition,
		"remaster_year":             "",
		"resolution_height":         "",
		"resolution_width":          "",
		"resolution":                resolveResolution(meta),
		"source_other":              "",
		"source":                    resolveSource(meta),
		"submit":                    "true",
		"subtitle_type":             resolveSubtitleType(meta),
	}
	if groupID != "" {
		fields["groupid"] = groupID
	} else {
		fields["data_source"], fields["identifier"] = resolveProviderPair(meta, answers)
		if meta.Identity.IMDBID > 0 {
			fields["imdb"] = providerid.IMDb(meta.Identity.IMDBID).Prefixed()
		} else {
			fields["no_imdb_link"] = "1"
		}
		if meta.Identity.TMDBID > 0 {
			fields["tmdb"] = strconv.Itoa(meta.Identity.TMDBID)
		}
		fields["desc"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["desc"]), resolveOverview(meta))
		fields["image"] = answerOr(answers, "poster_url", resolvePoster(meta))
		fields["maindesc"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["main_desc"]), resolveOverview(meta))
		fields["name"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["title"]), meta.Release.Title)
		fields["releasetype"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["release_type"]), resolveMovieType(meta))
		fields["subname"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["subname"]), meta.Release.Title)
		if year := resolveYear(meta); year > 0 {
			fields["year"] = strconv.Itoa(year)
		} else {
			fields["year"] = ""
		}
		fields["artists[]"], fields["artist_ids[]"] = resolveDirectorFields(meta, answers)
		fields["importance[]"] = "1"
		fields["artists_sub[]"] = strings.TrimSpace(answers["director_chinese"])
		fields["characters[]"] = ""
		fields["main_artist_number"] = "1"
	}
	maps.Copy(fields, resolveMediaFlags(meta))
	if meta.Scene {
		fields["scene"] = "on"
	}
	if meta.PersonalRelease {
		if isDiscUpload(meta) {
			fields["buy"] = "on"
		} else {
			fields["diy"] = "on"
		}
	}
	if trackerCfg.Exclusive {
		fields["jinzhuan"] = "on"
	}
	return fields
}

func isDiscUpload(meta api.UploadSubject) bool {
	return strings.TrimSpace(meta.DiscType) != "" || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC")
}

func resolveYear(meta api.UploadSubject) int {
	provider := 0
	if meta.ProviderMetadata.TMDB != nil {
		provider = meta.ProviderMetadata.TMDB.Year
	}
	if provider == 0 && meta.ProviderMetadata.IMDB != nil {
		provider = meta.ProviderMetadata.IMDB.Year
	}
	return trackers.PreferredYear(meta, provider)
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return ""
}

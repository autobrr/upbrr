// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package czt implements uploads to CZTeam (CZT) via its dedicated JSON
// endpoint takeupload_api.php.
//
// Unlike most impls in this repo CZTeam is not a UNIT3D site and does not need a
// cookie jar: the user's passkey authenticates the multipart POST. The endpoint
// returns the registered .torrent inline as base64, already personalized with
// the uploader's announce passkey and source=CzT.
package czt

import (
	"errors"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// cztCategory pairs a CZTeam categories.id with its display name.
type cztCategory struct {
	id   string
	name string
}

func categoryNames() []string {
	out := make([]string, 0, len(cztCategories))
	for _, c := range cztCategories {
		out = append(out, c.name)
	}
	return out
}

func categoryIDForName(name string) string {
	name = strings.TrimSpace(name)
	for _, c := range cztCategories {
		if strings.EqualFold(c.name, name) {
			return c.id
		}
	}
	return ""
}

func categoryNameForID(id string) string {
	for _, c := range cztCategories {
		if c.id == id {
			return c.name
		}
	}
	return ""
}

// resolveCategory returns the CZTeam category id: an explicit questionnaire
// override when the user picked one, otherwise the auto-detected video category.
func resolveCategory(meta api.UploadSubject) (string, error) {
	if id := resolveQuestionnaireCategory(standalone.QuestionnaireAnswers(meta, trackerName)["category"]); id != "" {
		return id, nil
	}
	if id := autoCategory(meta); id != "" {
		return id, nil
	}
	return "", errors.New("trackers: CZT category requires explicit questionnaire selection for non-video content")
}

// autoCategory maps prepared metadata to a CZTeam numeric categories.id when
// metadata supports automatic classification. Unknown non-video content returns
// empty so callers can require an explicit questionnaire category instead of
// falling back to a movie bucket.
func autoCategory(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}

	ro := hasRomanianSubs(meta)
	hd := isHD(meta.Release.Resolution)

	switch {
	case meta.Anime:
		return "23" // Anime
	case category == api.CanonicalCategoryTV:
		if hd && ro {
			return "34" // TvEps/HD-RO
		}
		if hd {
			return "5" // TvEps/HD
		}
		return "7" // TvEps (no SD-RO TV category exists)
	}

	// Movies.
	src := strings.ToUpper(metautil.FirstNonEmptyTrimmed(meta.Source, meta.Release.Source))
	isDVD := strings.Contains(src, "DVD") || strings.EqualFold(meta.DiscType, "DVD") || strings.EqualFold(meta.Type, "DVDRIP")
	isFullBluRay := strings.EqualFold(meta.DiscType, "BDMV") ||
		(strings.EqualFold(meta.Type, "REMUX") && strings.Contains(src, "BLURAY"))
	if category != api.CanonicalCategoryMovie {
		return ""
	}

	if ro {
		switch {
		case isFullBluRay:
			return "36" // Full BluRay-RO
		case isDVD:
			return "28" // Movies/DVD-RO
		case hd:
			return "33" // Movies/HDTV-RO
		default:
			return "38" // Movies-RO
		}
	}
	switch {
	case isDVD:
		return "20" // Movies/DVD-R
	case hd:
		return "29" // Movies/HD
	case hasCodec(meta, "XviD"):
		return "19" // Movies/XviD
	default:
		return "29" // default to Movies/HD when movie evidence exists
	}
}

func missingRequiredCategory(state uploadState) bool {
	if state.questionnaire == nil {
		return false
	}
	if strings.TrimSpace(state.fields["category"]) != "" {
		return false
	}
	for _, field := range state.questionnaire.Fields {
		if field.Key == "category" && field.Required {
			return true
		}
	}
	return false
}

func hasCodec(meta api.UploadSubject, want string) bool {
	for _, c := range meta.Release.Codec {
		if strings.EqualFold(strings.TrimSpace(c), want) {
			return true
		}
	}
	return false
}

func firstCodec(meta api.UploadSubject) string {
	for _, c := range meta.Release.Codec {
		if v := strings.TrimSpace(c); v != "" {
			return v
		}
	}
	return ""
}

// cztCategories lists CZTeam upload categories for the upload-time override
// dropdown. upbrr auto-detects only video categories from metadata; everything
// else (software, games, music, XXX, images, docs, …) is chosen here.
var cztCategories = []cztCategory{
	{"1", "XxX"},
	{"4", "Games/PC ISO"},
	{"5", "TvEps/HD"},
	{"6", "Music/Audio"},
	{"7", "TvEps"},
	{"9", "Mobile"},
	{"12", "Games/Consoles"},
	{"19", "Movies/XviD"},
	{"20", "Movies/DVD-R"},
	{"21", "Games/PC Rips"},
	{"22", "Software"},
	{"23", "Anime"},
	{"24", "Images"},
	{"25", "Docs"},
	{"28", "Movies/DVD-RO"},
	{"29", "Movies/HD"},
	{"30", "Music/MVID"},
	{"31", "MAC"},
	{"32", "Sports"},
	{"33", "Movies/HDTV-RO"},
	{"34", "TvEps/HD-RO"},
	{"35", "Music/Lossless"},
	{"36", "Full BluRay-RO"},
	{"37", "Movies/3D"},
	{"38", "Movies-RO"},
}

func hasRomanianSubs(meta api.UploadSubject) bool {
	for _, s := range meta.SubtitleLanguages {
		v := strings.ToLower(strings.TrimSpace(s))
		if v == "ro" || v == "rum" || v == "ron" || strings.HasPrefix(v, "roman") {
			return true
		}
	}
	return false
}

func isHD(res string) bool {
	res = strings.TrimSpace(res)
	for _, prefix := range []string{"720", "1080", "2160", "4320"} {
		if strings.HasPrefix(res, prefix) {
			return true
		}
	}
	return false
}

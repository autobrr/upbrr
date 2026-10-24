// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/pkg/api"
)

// VideoRateControlFromMediaInfo reads explicit x264/x265 encoder settings for
// one video track. Generic bitrate mode and settings presence are not evidence
// of CRF or two-pass encoding; ambiguous or unsupported reports stay unknown.
func VideoRateControlFromMediaInfo(doc MediaInfoDocument) api.VideoRateControl {
	var video map[string]any
	for _, track := range doc.Media.Track {
		if !strings.EqualFold(mediaInfoValue(track, "@type"), "Video") {
			continue
		}
		if video != nil {
			return ""
		}
		video = track
	}
	if video == nil {
		return ""
	}
	names := strings.Fields(strings.ToLower(mediaInfoValue(video, "Encoded_Library_Name", "Encoded_Library")))
	if len(names) == 0 || names[0] != "x264" && names[0] != "x265" {
		return ""
	}
	encoder := names[0]
	library := strings.Fields(strings.ToLower(mediaInfoValue(video, "Encoded_Library")))
	if len(library) > 0 && library[0] != encoder {
		return ""
	}
	format := strings.ToUpper(mediaInfoValue(video, "Format"))
	if format != "" && (encoder == "x264" && format != "AVC" || encoder == "x265" && format != "HEVC") {
		return ""
	}
	values := map[string]string{}
	for _, token := range strings.FieldsFunc(mediaInfoValue(video, "Encoded_Library_Settings"), func(r rune) bool { return unicode.IsSpace(r) || r == '/' }) {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			continue
		}
		switch key {
		case "rc", "crf", "qp", "bitrate", "stats-read", "stats-write":
			if value == "" {
				return ""
			}
			if previous, exists := values[key]; exists && previous != value {
				return ""
			}
			values[key] = value
		}
	}
	mode := values["rc"]
	if mode == "crf" && (values["qp"] != "" || values["bitrate"] != "") || mode != "crf" && values["crf"] != "" || mode != "cqp" && values["qp"] != "" {
		return ""
	}
	switch mode {
	case "crf":
		if encoder == "x265" && values["stats-read"] != "" && values["stats-read"] != "0" {
			return ""
		}
		return api.VideoRateControlCRF
	case "2pass":
		if encoder == "x264" {
			return api.VideoRateControlTwoPassVBR
		}
	case "abr":
		if encoder == "x264" {
			return api.VideoRateControlABR
		}
		if (values["stats-read"] == "1" || values["stats-read"] == "2") && values["stats-write"] == "0" {
			return api.VideoRateControlMultiPassVBR
		}
		if values["stats-read"] == "0" && (values["stats-write"] == "0" || values["stats-write"] == "1") {
			return api.VideoRateControlABR
		}
	case "cbr":
		return api.VideoRateControlCBR
	case "cqp":
		return api.VideoRateControlCQP
	}
	return ""
}

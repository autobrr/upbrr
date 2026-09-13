// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	name = strings.Join(strings.Fields(name), " ")
	source := strings.TrimSpace(meta.Source)
	resolution := unit3d.Resolution(meta)
	nameType := strings.ToUpper(strings.TrimSpace(meta.Type))
	dvdRemux := nameType == "REMUX" && (source == "PAL DVD" || source == "NTSC DVD" || source == "DVD")
	audio := strings.TrimSpace(meta.Audio)
	codec := strings.TrimSpace(meta.VideoCodec)
	system := ""
	switch resolution {
	case "576i", "576p":
		system = "PAL"
	case "480i", "480p":
		system = "NTSC"
	}

	switch {
	case nameType == "DVDRIP":
		name = removeLast(name, meta.Edition)
		name = removeLast(name, meta.Repack)
		encode := strings.TrimSpace(meta.VideoEncode)
		if encode == "" {
			encode = codec
		}
		name = removeLast(name, source)
		if encode != "" && audio != "" {
			if index := strings.LastIndex(name, " "+encode+" DVDRip"); index >= 0 {
				name = name[:index] + name[index+len(encode)+1:]
			}
			name = insertAfterLast(name, audio, encode)
		}
		source = "DVDRip"
		name = insertBefore(name, source, resolution)
	case strings.EqualFold(meta.DiscType, "DVD"):
		if size := strings.TrimSpace(meta.Release.Size); size == "DVD5" || size == "DVD9" {
			// The generic name collapses "NTSC DVD DVD9" to "NTSC DVD9".
			source = strings.TrimSpace(strings.TrimSuffix(source, "DVD"))
			if source == "" {
				source = system
				name = insertBefore(name, size, source)
			}
		}
	case dvdRemux:
		if source == "DVD" {
			if system != "" {
				name = insertBefore(name, source, system)
				source = system + " " + source
			}
		}
	case nameType == "ENCODE" && resolution != "":
		// A codec token makes the parser type a DVDRip as ENCODE with a DVD source.
		for _, token := range []string{" NTSC DVD ", " PAL DVD ", " DVD "} {
			if strings.Contains(name, resolution+token) {
				name = strings.Replace(name, resolution+token, resolution+" DVDRip ", 1)
				name = removeLast(name, meta.Edition)
				name = removeLast(name, meta.Repack)
				break
			}
		}
	}

	year := ""
	if meta.Release.Year > 0 {
		year = strconv.Itoa(meta.Release.Year)
		if !strings.Contains(" "+name+" ", " "+year+" ") {
			year = ""
		}
	}
	if alternate := strings.TrimSpace(meta.AlternateTitle); alternate != "" && year != "" {
		name = strings.Replace(name, year+" "+alternate, alternate+" "+year, 1)
	}
	if !unit3d.HasEnglishLanguage(meta.AudioLanguages) && !strings.EqualFold(meta.DiscType, "BDMV") {
		language := ""
		for _, label := range meta.AudioLanguages {
			switch strings.ToLower(label) {
			case "zxx", "no linguistic content", "und", "undetermined":
				continue
			default:
				language = strings.ToUpper(label)
			}
			break
		}
		if dvdRemux && year != "" {
			padded := " " + name + " "
			index := strings.LastIndex(padded, " "+year+" ")
			anchor := strings.TrimPrefix(strings.TrimSpace(padded[index+len(year)+2:]), language+" ")
			name = insertBefore(name, anchor, language)
		} else {
			for _, anchor := range []string{resolution, strings.TrimSpace(meta.Service), strings.TrimSpace(meta.Region), source} {
				if anchor != "" && strings.Contains(" "+name+" ", " "+anchor+" ") {
					name = insertBefore(name, anchor, language)
					break
				}
			}
		}
	}
	return strings.Join(strings.Fields(name), " ")
}

func removeLast(name, token string) string {
	if token == "" {
		return name
	}
	padded := " " + name + " "
	index := strings.LastIndex(padded, " "+token+" ")
	if index < 0 {
		return name
	}
	return strings.TrimSpace(padded[:index+1] + padded[index+len(token)+2:])
}

func insertBefore(name, token, prefix string) string {
	if token == "" || prefix == "" {
		return name
	}
	padded := " " + name + " "
	if strings.Contains(padded, " "+prefix+" "+token+" ") {
		return name
	}
	index := strings.LastIndex(padded, " "+token+" ")
	if index < 0 {
		return name
	}
	return strings.TrimSpace(padded[:index+1] + prefix + " " + padded[index+1:])
}

func insertAfterLast(name, token, suffix string) string {
	if token == "" || suffix == "" {
		return name
	}
	for end := len(name); end > 0; {
		index := strings.LastIndex(name[:end], token)
		if index < 0 {
			break
		}
		after := index + len(token)
		if (index == 0 || name[index-1] == ' ') && (after == len(name) || name[after] == ' ' || name[after] == '-') {
			return name[:after] + " " + suffix + name[after:]
		}
		end = index + len(token) - 1
	}
	return name
}

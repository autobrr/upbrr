// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"strconv"
	"strings"
)

// MediaInfoAudioTrack contains technical facts from one reported audio stream.
type MediaInfoAudioTrack struct {
	Format         string
	FormatProfile  string
	CommercialName string
	Language       string
	Title          string
	Channels       int
}

// AudioTracksFromMediaInfoText returns a single report's audio inventory when
// its stream count is explicit or its sole Audio section is unnumbered.
func AudioTracksFromMediaInfoText(value string) []MediaInfoAudioTrack {
	var tracks []MediaInfoAudioTrack
	section, declaredCount := "", -1
	general, video, unnumberedAudio := false, false, false
	originalChannels := false
	for line := range strings.SplitSeq(value, "\n") {
		line = strings.TrimSpace(line)
		if match := mediaInfoSectionPattern.FindStringSubmatch(line); len(match) == 2 {
			section = strings.ToLower(match[1])
			switch section {
			case "general":
				if general {
					return nil
				}
				general = true
			case "video":
				video = true
			case "audio":
				unnumberedAudio = unnumberedAudio || strings.EqualFold(line, "Audio")
				originalChannels = false
				tracks = append(tracks, MediaInfoAudioTrack{})
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if section == "general" && key == "count of audio streams" {
			count, err := strconv.Atoi(value)
			if err != nil || count < 0 || declaredCount >= 0 && count != declaredCount {
				return nil
			}
			declaredCount = count
		}
		if section != "audio" {
			continue
		}
		track := &tracks[len(tracks)-1]
		switch key {
		case "format":
			track.Format = value
		case "format profile":
			track.FormatProfile = value
		case "commercial name":
			track.CommercialName = value
		case "language":
			track.Language = value
		case "title":
			track.Title = value
		case "channel(s)":
			if !originalChannels {
				track.Channels = exactTextChannelCount(value)
			}
		case "channel(s)_original":
			originalChannels = true
			track.Channels = exactTextChannelCount(value)
		}
	}
	if !general || !video {
		return nil
	}
	if declaredCount == len(tracks) || declaredCount == -1 && len(tracks) == 1 && unnumberedAudio && tracks[0].Format != "" {
		return tracks
	}
	return nil
}

func exactTextChannelCount(value string) int {
	value = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(value), " channels"), " channel")
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || count <= 0 {
		return 0
	}
	return count
}

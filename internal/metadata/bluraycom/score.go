// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

func scoreCandidate(candidate *api.BlurayReleaseCandidate, bdinfo *discparse.BDInfo) {
	if candidate == nil {
		return
	}
	score := 100.0
	notes := make([]string, 0, 16)

	if candidate.Specs.Video.Codec == "" {
		score -= 5
		candidate.SpecsMissing = true
		notes = append(notes, "missing video specs (-5)")
	}
	if len(candidate.Specs.Audio) == 0 {
		score -= 5
		candidate.SpecsMissing = true
		notes = append(notes, "missing audio specs (-5)")
	}
	if bdinfo != nil && len(bdinfo.Subtitles) > 0 && len(candidate.Specs.Subtitles) == 0 {
		score -= 5
		candidate.SpecsMissing = true
		notes = append(notes, "missing subtitle specs (-5)")
	}
	if candidate.Specs.Discs.Format == "" {
		score -= 5
		candidate.SpecsMissing = true
		notes = append(notes, "missing disc specs (-5)")
	}

	if bdinfo != nil {
		score -= scoreDiscFormat(candidate, bdinfo, &notes)
		score -= scoreVideo(candidate, bdinfo, &notes)
		score -= scoreAudio(candidate, bdinfo, &notes)
		score -= scoreSubtitles(candidate, bdinfo, &notes)
	} else {
		score -= 15
		notes = append(notes, "local BDInfo unavailable (-15)")
	}

	if score < 0 {
		score = 0
	}
	candidate.Score = score
	candidate.MatchNotes = notes
}

func scoreDiscFormat(candidate *api.BlurayReleaseCandidate, bdinfo *discparse.BDInfo, notes *[]string) float64 {
	format := strings.ToLower(strings.TrimSpace(candidate.Specs.Discs.Format))
	if format == "" || bdinfo == nil || bdinfo.SizeGB <= 0 {
		return 0
	}
	expected := expectedBDFormat(bdinfo.SizeGB)
	if expected == "" {
		return 0
	}
	if strings.Contains(format, expected) || strings.Contains(strings.ReplaceAll(format, " ", ""), expected) {
		*notes = append(*notes, fmt.Sprintf("disc format matches %s", strings.ToUpper(expected)))
		return 0
	}
	if strings.Contains(format, "bd") && !strings.ContainsAny(format, "0123456789") {
		candidate.GenericDisc = true
		*notes = append(*notes, "generic BD format (-5)")
		return 5
	}
	*notes = append(*notes, fmt.Sprintf("disc format mismatch %q vs %s (-50)", candidate.Specs.Discs.Format, strings.ToUpper(expected)))
	return 50
}

func expectedBDFormat(sizeGB float64) string {
	switch {
	case sizeGB <= 0:
		return ""
	case sizeGB < 25:
		return "bd-25"
	case sizeGB < 50:
		return "bd-50"
	case sizeGB < 66:
		return "bd-66"
	default:
		return "bd-100"
	}
}

func scoreVideo(candidate *api.BlurayReleaseCandidate, bdinfo *discparse.BDInfo, notes *[]string) float64 {
	if bdinfo == nil || len(bdinfo.Video) == 0 {
		return 0
	}
	video := bdinfo.Video[0]
	penalty := 0.0
	if !codecMatches(candidate.Specs.Video.Codec, video.Codec) {
		penalty += 80
		*notes = append(*notes, fmt.Sprintf("video codec mismatch %q vs %q (-80)", candidate.Specs.Video.Codec, video.Codec))
	} else {
		*notes = append(*notes, "video codec matches")
	}
	if !resolutionMatches(candidate.Specs.Video.Resolution, video.Resolution) {
		penalty += 80
		*notes = append(*notes, fmt.Sprintf("resolution mismatch %q vs %q (-80)", candidate.Specs.Video.Resolution, video.Resolution))
	} else {
		*notes = append(*notes, "resolution matches")
	}
	return penalty
}

func codecMatches(left string, right string) bool {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	switch {
	case strings.Contains(left, "avc") || strings.Contains(left, "h.264"):
		return strings.Contains(right, "avc") || strings.Contains(right, "h.264")
	case strings.Contains(left, "hevc") || strings.Contains(left, "h.265"):
		return strings.Contains(right, "hevc") || strings.Contains(right, "h.265")
	case strings.Contains(left, "vc-1") || strings.Contains(left, "vc1"):
		return strings.Contains(right, "vc-1") || strings.Contains(right, "vc1")
	case strings.Contains(left, "mpeg-2") || strings.Contains(left, "mpeg2"):
		return strings.Contains(right, "mpeg-2") || strings.Contains(right, "mpeg2")
	default:
		return left != "" && right != "" && strings.Contains(right, left)
	}
}

func resolutionMatches(left string, right string) bool {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	switch {
	case strings.Contains(left, "2160") || strings.Contains(left, "4k"):
		return strings.Contains(right, "2160") || strings.Contains(right, "4k")
	case strings.Contains(left, "1080"):
		return strings.Contains(right, "1080")
	case strings.Contains(left, "720"):
		return strings.Contains(right, "720")
	case strings.Contains(left, "480"):
		return strings.Contains(right, "480")
	default:
		return left == "" || right == "" || strings.Contains(right, left)
	}
}

func scoreAudio(candidate *api.BlurayReleaseCandidate, bdinfo *discparse.BDInfo, notes *[]string) float64 {
	if bdinfo == nil || len(bdinfo.Audio) == 0 {
		return 0
	}
	if len(candidate.Specs.Audio) == 0 {
		return 5
	}
	available := append([]string{}, candidate.Specs.Audio...)
	fullMatches := 0
	partialMatches := 0
	missing := 0
	reducedMissing := 0

	for idx, track := range bdinfo.Audio {
		bestIndex := -1
		bestScore := 0
		for releaseIdx, releaseTrack := range available {
			score := audioTrackScore(track, releaseTrack)
			if score > bestScore {
				bestScore = score
				bestIndex = releaseIdx
			}
		}
		if bestIndex >= 0 && bestScore >= 3 {
			fullMatches++
			available = append(available[:bestIndex], available[bestIndex+1:]...)
			continue
		}
		if bestIndex >= 0 && bestScore >= 2 {
			partialMatches++
			available = append(available[:bestIndex], available[bestIndex+1:]...)
			continue
		}
		missing++
		if idx > 0 && lowBitrate(track.Bitrate) {
			reducedMissing++
		}
	}

	total := len(bdinfo.Audio)
	penalty := 0.0
	switch {
	case fullMatches == total:
	case total == 1 && partialMatches == 1:
		penalty += 5
	case total == 1:
		penalty += 10
	default:
		normalMissing := missing - reducedMissing
		penalty += float64(partialMatches) * 2.5
		penalty += float64(normalMissing) * 5
		penalty += float64(reducedMissing) * 2.5
	}
	if len(available) > 0 {
		penalty += float64(len(available)) * 5
	}
	*notes = append(*notes, fmt.Sprintf("audio full=%d partial=%d missing=%d extra=%d (-%.1f)", fullMatches, partialMatches, missing, len(available), penalty))
	return penalty
}

func audioTrackScore(local discparse.BDAudio, releaseTrack string) int {
	release := strings.ToLower(releaseTrack)
	score := 0
	if local.Language != "" && strings.Contains(release, strings.ToLower(local.Language)) {
		score++
	} else {
		return 0
	}
	format := normalizeAudioToken(local.Codec)
	if format != "" && strings.Contains(release, format) {
		score++
	} else if strings.Contains(format, "dts") && strings.Contains(release, "dts") {
		score++
	} else if strings.Contains(format, "dolby") && strings.Contains(release, "dolby") {
		score++
	} else if strings.Contains(format, "truehd") && strings.Contains(release, "truehd") {
		score++
	} else if strings.Contains(strings.ToLower(local.Atmos), "atmos") && strings.Contains(release, "atmos") {
		score++
	}
	channels := strings.ToLower(local.Channels)
	switch {
	case strings.Contains(channels, "7.1") && strings.Contains(release, "7.1"):
		score++
	case strings.Contains(channels, "5.1") && strings.Contains(release, "5.1"):
		score++
	case strings.Contains(channels, "2.0") && (strings.Contains(release, "2.0") || strings.Contains(release, "stereo")):
		score++
	case strings.Contains(channels, "1.0") && (strings.Contains(release, "1.0") || strings.Contains(release, "mono")):
		score++
	}
	return score
}

func normalizeAudioToken(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "audio", "")
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "-", "")
	switch {
	case strings.Contains(value, "lpcm"):
		return "pcm"
	case strings.Contains(value, "dtshd"):
		return "dts-hd"
	case strings.Contains(value, "truehd"):
		return "truehd"
	case strings.Contains(value, "dolby"):
		return "dolby"
	case strings.Contains(value, "dts"):
		return "dts"
	default:
		return value
	}
}

func lowBitrate(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "kbps")
	value = strings.TrimSpace(value)
	var parsed int
	_, err := fmt.Sscanf(value, "%d", &parsed)
	return err == nil && parsed <= 258
}

func scoreSubtitles(candidate *api.BlurayReleaseCandidate, bdinfo *discparse.BDInfo, notes *[]string) float64 {
	if bdinfo == nil || len(bdinfo.Subtitles) == 0 {
		return 0
	}
	if len(candidate.Specs.Subtitles) == 0 {
		return 5
	}
	available := append([]string{}, candidate.Specs.Subtitles...)
	matches := 0
	for _, localSub := range bdinfo.Subtitles {
		local := strings.ToLower(strings.TrimSpace(localSub))
		if local == "" {
			continue
		}
		matchedIndex := -1
		for idx, releaseSub := range available {
			release := strings.ToLower(strings.TrimSpace(releaseSub))
			if strings.Contains(local, release) || strings.Contains(release, local) {
				matchedIndex = idx
				break
			}
		}
		if matchedIndex >= 0 {
			matches++
			available = append(available[:matchedIndex], available[matchedIndex+1:]...)
		}
	}
	total := len(bdinfo.Subtitles)
	missing := total - matches
	penalty := float64(missing) * 5
	if total == 1 && matches == 0 {
		penalty = 10
	}
	if len(available) > 0 {
		penalty += float64(len(available)) * 5
	}
	*notes = append(*notes, fmt.Sprintf("subtitles matched=%d missing=%d extra=%d (-%.1f)", matches, missing, len(available), penalty))
	return penalty
}

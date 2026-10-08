// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadTitle(meta api.UploadSubject) string {
	base := resolveDisplayTitle(meta)
	if categoryOf(meta) == "TV" {
		seasonEpisode := metautil.FirstNonEmptyTrimmed(
			meta.DailyEpisodeDate,
			strings.TrimSpace(meta.SeasonStr)+strings.TrimSpace(meta.EpisodeStr),
			seasonEpisodeText(meta),
		)
		if seasonEpisode != "" {
			return strings.TrimSpace(base + " - " + seasonEpisode)
		}
	}
	return base
}

func resolveDisplayTitle(meta api.UploadSubject) string {
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	main := strings.TrimSpace(meta.Release.Title)
	alt := ""
	if tmdb := meta.ProviderMetadata.TMDB; tmdb != nil {
		main = strings.TrimSpace(metautil.FirstNonEmptyTrimmed(ptBR.Title, tmdb.Title, meta.Release.Title))
		if categoryOf(meta) == "TV" {
			alt = strings.TrimSpace(metautil.FirstNonEmptyTrimmed(tmdb.Title, meta.Release.Title))
		} else {
			alt = strings.TrimSpace(tmdb.OriginalTitle)
		}
	}
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		main = trackers.PreferredTitle(meta, "")
	}
	if meta.EffectiveMetadata.OriginalTitleProvenance.IsManual() {
		alt = trackers.PreferredOriginalTitle(meta, "")
	}
	if meta.NamePresentation.Version == api.ReleaseNamePresentationVersionV1 && meta.NamePresentation.OmitAlternateTitle {
		alt = ""
	}
	if main != "" && alt != "" && !strings.EqualFold(main, alt) {
		return main + " (" + alt + ")"
	}
	if main != "" {
		return main
	}
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		return ""
	}
	return strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, pathutil.Base(meta.SourcePath)))
}

// resolveSearchTitle returns the anime duplicate-search name: the manual or
// finalized title, then the parsed release title, then the release name. It
// never falls back to the source path, and a pack folder name such as
// "Season 03" cannot displace the finalized title.
func resolveSearchTitle(meta api.UploadSubject) string {
	if title := trackers.PreferredTitle(meta, ""); title != "" || meta.EffectiveMetadata.TitleProvenance.IsManual() {
		return title
	}
	return strings.TrimSpace(meta.ReleaseName)
}

func seasonEpisodeText(meta api.UploadSubject) string {
	if meta.EpisodeInt > 0 {
		return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
	}
	if meta.SeasonInt > 0 {
		return fmt.Sprintf("S%02d", meta.SeasonInt)
	}
	return ""
}

var (
	fileNameResolutionPattern = regexp.MustCompile(`(?i)[._ ](?:2160|1440|1080|720|576|540|480)[pi](?:[._ -]|$)`)
	fileNameVideoPattern      = regexp.MustCompile(`(?i)([._ ])(?:H[._ ]?26[45]|x26[45]|HEVC|AVC|AV1|VP9|XviD|DivX|VC-1|MPEG-?2)(?:[._ -]|$)`)
	// fileNameAudioPattern recognises an existing audio token, including spellings
	// without the dot (DDP51), the DDPA variant and hyphenated AC-3, so a name that
	// already has audio is never given a second token.
	fileNameGroupPattern = regexp.MustCompile(`-[A-Za-z0-9]+$`)
	fileNameAudioPattern = regexp.MustCompile(
		`(?i)(?:^|[._ -])(?:DDPA?\+?|DD\+?|AAC|E-?AC-?3|AC-?3|DTS(?:-HD|-X|-ES)?|TrueHD|FLAC|L?PCM|OPUS|MP3|Atmos)(?:\d\.?\d?)?(?:[._ -]|$)`,
	)
)

// fileNameExtensions lists the container extensions stripped before the file
// name is analysed; folder names are not expected to carry one.
var fileNameExtensions = map[string]struct{}{
	".mkv":  {},
	".mp4":  {},
	".m4v":  {},
	".avi":  {},
	".ts":   {},
	".m2ts": {},
	".mpg":  {},
	".mpeg": {},
	".wmv":  {},
	".mov":  {},
}

// complianceSkip explains why complianceFileName left a name unchanged, so the
// caller can tell a name that is already compliant from one that could not be
// fixed.
type complianceSkip string

const (
	complianceApplied      complianceSkip = ""
	complianceNoAudioFacts complianceSkip = "no_audio_facts"
	complianceHasAudio     complianceSkip = "audio_present"
	complianceNoResolution complianceSkip = "no_resolution_anchor"
	complianceNoVideoCodec complianceSkip = "no_video_codec_anchor"
)

// unresolved reports whether a skip left a name that is still missing its audio
// token, as opposed to one that already has it.
func (c complianceSkip) unresolved() bool {
	return c != complianceApplied && c != complianceHasAudio
}

// complianceFileName returns the name the site requires for a release file or
// folder: the primary audio token inserted immediately before the video codec
// token that follows the resolution (`…1080p.DSNP.WEB-DL.DDP5.1.H.264-GRP`).
// The name is returned unchanged when an audio token already follows the
// resolution, when no audio token can be derived from the finalized media
// facts, or when no resolution and video-codec token anchor the insertion. It
// never reorders or drops existing tokens.
func complianceFileName(meta api.UploadSubject, name string) string {
	renamed, _ := complianceFileNameWithReason(meta, name)
	return renamed
}

// complianceFileNameWithReason is complianceFileName plus the reason a name was
// left unchanged.
func complianceFileNameWithReason(meta api.UploadSubject, name string) (string, complianceSkip) {
	token := audioFileNameToken(meta)
	if token == "" {
		return name, complianceNoAudioFacts
	}
	stem, ext := name, ""
	if dot := strings.LastIndex(name, "."); dot > 0 {
		if _, ok := fileNameExtensions[strings.ToLower(name[dot:])]; ok {
			stem, ext = name[:dot], name[dot:]
		}
	}
	resolution := fileNameResolutionPattern.FindStringIndex(stem)
	if resolution == nil {
		return name, complianceNoResolution
	}
	tail := stem[resolution[0]:]
	// The release group follows the last hyphen and can itself look like an audio
	// token (-DTS, -AAC), so it is left out of the audio check.
	if fileNameAudioPattern.MatchString(fileNameGroupPattern.ReplaceAllString(tail, "")) {
		return name, complianceHasAudio
	}
	video := fileNameVideoPattern.FindStringSubmatchIndex(tail)
	if video == nil {
		return name, complianceNoVideoCodec
	}
	// Group 1 is the separator preceding the codec; insert right after it.
	insertAt := resolution[0] + video[3]
	separator := stem[resolution[0]+video[2] : insertAt]
	return stem[:insertAt] + token + separator + stem[insertAt:] + ext, complianceApplied
}

// renameContent is the ASC content renamer. Files and subfolders get the audio
// token through complianceFileName. A root folder that carries no release
// information (a pack folder such as "Season 03") is replaced by the release
// name in dotted form, because clients show the root as the torrent name and
// link staging uses it as the seeding folder: a generic name is unreadable and
// collides with other packs of the same season.
func renameContent(meta api.UploadSubject, name string, kind trackers.ContentNameKind) string {
	if kind == trackers.ContentRootFolderName && fileNameResolutionPattern.FindStringIndex(name) == nil {
		if folder := releaseFolderName(meta.ReleaseName); folder != "" {
			name = folder
		}
	}
	return complianceFileName(meta, name)
}

// releaseFolderName turns a release name into a dotted folder name, dropping
// characters that are not allowed in file names on common systems.
func releaseFolderName(releaseName string) string {
	cleaned := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return -1
		}
		return r
	}, releaseName)
	return strings.Trim(strings.Join(strings.Fields(cleaned), "."), ".")
}

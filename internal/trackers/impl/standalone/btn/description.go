// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveBTNReleaseDesc(meta api.UploadSubject) string {
	return strings.TrimSpace(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath))
}

func preferredBTNTVDBEpisodeTitle(tvdb *api.TVDBMetadata) string {
	if tvdb == nil {
		return ""
	}
	return metautil.FirstNonEmptyTrimmed(strings.TrimSpace(tvdb.EpisodeNameEnglish), strings.TrimSpace(tvdb.EpisodeName))
}

func preferredBTNIMDBEpisodeTitle(meta api.UploadSubject) string {
	if episode := preferredBTNIMDBEpisode(meta); episode != nil {
		return strings.TrimSpace(episode.Title)
	}
	return ""
}

func preferredBTNTVDBOverview(tvdb *api.TVDBMetadata) string {
	if tvdb == nil {
		return ""
	}
	return metautil.FirstNonEmptyTrimmed(
		strings.TrimSpace(tvdb.EpisodeOverviewEnglish),
		strings.TrimSpace(tvdb.EpisodeOverview),
		strings.TrimSpace(tvdb.OverviewEnglish),
		strings.TrimSpace(tvdb.Overview),
	)
}

func preferredBTNIMDBOverview(imdb *api.IMDBMetadata) string {
	if imdb == nil {
		return ""
	}
	return strings.TrimSpace(imdb.Plot)
}

func buildAlbumDesc(meta api.UploadSubject, fields map[string]string) string {
	if desc := metautil.FirstNonEmptyTrimmed(fields["album_desc"]); desc != "" {
		return desc
	}
	if !strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), "TV") {
		return ""
	}
	if desc := buildTVDBAlbumDesc(meta); desc != "" {
		return desc
	}
	tvdb := meta.ProviderMetadata.TVDB
	overview := metautil.FirstNonEmptyTrimmed(
		preferredBTNTVDBOverview(tvdb),
		preferredBTNIMDBOverview(meta.ProviderMetadata.IMDB),
		strings.TrimSpace(meta.EpisodeOverview),
	)
	aired := metautil.FirstNonEmptyTrimmed(
		btnTVDBEpisodeAired(tvdb),
		btnIMDBEpisodeAired(meta),
		strings.TrimSpace(meta.TVDBAiredDate),
		strings.TrimSpace(meta.DailyEpisodeDate),
		"TBA",
	)
	season, episode := resolveBTNTVSeasonEpisode(meta)
	episodeTitle := metautil.FirstNonEmptyTrimmed(
		preferredBTNTVDBEpisodeTitle(tvdb),
		preferredBTNIMDBEpisodeTitle(meta),
		strings.TrimSpace(meta.EpisodeTitle),
		"TBA",
	)
	return formatBTNEpisodeAlbumDesc([]api.TVDBEpisodeMetadata{{
		SeasonNumber:    season,
		EpisodeNumber:   episode,
		EpisodeName:     episodeTitle,
		EpisodeOverview: overview,
		EpisodeAired:    aired,
	}})
}

func buildTVDBAlbumDesc(meta api.UploadSubject) string {
	tvdb := meta.ProviderMetadata.TVDB
	if tvdb == nil {
		return ""
	}
	season, episode := resolveBTNTVSeasonEpisode(meta)
	if resolveUploadType(meta) == "Season" {
		episodes := make([]api.TVDBEpisodeMetadata, 0, len(tvdb.Episodes))
		for _, candidate := range tvdb.Episodes {
			if candidate.SeasonNumber == season {
				episodes = append(episodes, candidate)
			}
		}
		sort.SliceStable(episodes, func(i, j int) bool {
			return episodes[i].EpisodeNumber < episodes[j].EpisodeNumber
		})
		return formatBTNEpisodeAlbumDesc(episodes)
	}
	for _, candidate := range tvdb.Episodes {
		if candidate.SeasonNumber == season && candidate.EpisodeNumber == episode {
			return formatBTNEpisodeAlbumDesc([]api.TVDBEpisodeMetadata{candidate})
		}
	}
	if tvdb.EpisodeSeason > 0 || tvdb.EpisodeNumber > 0 || strings.TrimSpace(tvdb.EpisodeName) != "" || strings.TrimSpace(tvdb.EpisodeOverview) != "" {
		return formatBTNEpisodeAlbumDesc([]api.TVDBEpisodeMetadata{{
			SeasonNumber:           tvdb.EpisodeSeason,
			EpisodeNumber:          tvdb.EpisodeNumber,
			EpisodeName:            tvdb.EpisodeName,
			EpisodeNameEnglish:     tvdb.EpisodeNameEnglish,
			EpisodeOverview:        tvdb.EpisodeOverview,
			EpisodeOverviewEnglish: tvdb.EpisodeOverviewEnglish,
			EpisodeAired:           tvdb.EpisodeAired,
			EpisodeImage:           tvdb.EpisodeImage,
		}})
	}
	return ""
}

func formatBTNEpisodeAlbumDesc(episodes []api.TVDBEpisodeMetadata) string {
	blocks := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		title := metautil.FirstNonEmptyTrimmed(episode.EpisodeNameEnglish, episode.EpisodeName, "TBA")
		overview := metautil.FirstNonEmptyTrimmed(episode.EpisodeOverviewEnglish, episode.EpisodeOverview)
		aired := metautil.FirstNonEmptyTrimmed(episode.EpisodeAired, "TBA")
		var block strings.Builder
		block.WriteString("[b]Episode Name:[/b] ")
		block.WriteString(title)
		block.WriteString("\n[b]Season:[/b] ")
		block.WriteString(strconv.Itoa(episode.SeasonNumber))
		block.WriteString("\n[b]Episode:[/b] ")
		block.WriteString(strconv.Itoa(episode.EpisodeNumber))
		block.WriteString("\n[b]Aired:[/b] ")
		block.WriteString(aired)
		block.WriteString("\n\n[b]Episode overview:[/b]\n")
		block.WriteString(overview)
		if image := strings.TrimSpace(episode.EpisodeImage); image != "" {
			block.WriteString("\n\n[b]Episode image:[/b]\n[img=")
			block.WriteString(image)
			block.WriteString("]")
		}
		blocks = append(blocks, strings.TrimSpace(block.String()))
	}
	return strings.Join(blocks, "\n\n")
}

func btnTVDBEpisodeAired(tvdb *api.TVDBMetadata) string {
	if tvdb == nil {
		return ""
	}
	return strings.TrimSpace(tvdb.EpisodeAired)
}

func btnIMDBEpisodeAired(meta api.UploadSubject) string {
	episode := preferredBTNIMDBEpisode(meta)
	if episode == nil || episode.ReleaseDate.Year <= 0 {
		return ""
	}
	if episode.ReleaseDate.Month <= 0 {
		return strconv.Itoa(episode.ReleaseDate.Year)
	}
	if episode.ReleaseDate.Day <= 0 {
		return fmt.Sprintf("%04d-%02d", episode.ReleaseDate.Year, episode.ReleaseDate.Month)
	}
	return fmt.Sprintf("%04d-%02d-%02d", episode.ReleaseDate.Year, episode.ReleaseDate.Month, episode.ReleaseDate.Day)
}

func preferredBTNIMDBEpisode(meta api.UploadSubject) *api.IMDBEpisode {
	if meta.ProviderMetadata.IMDB == nil || len(meta.ProviderMetadata.IMDB.Episodes) == 0 {
		return nil
	}
	episodes := meta.ProviderMetadata.IMDB.Episodes
	season, episode := meta.CanonicalSeasonEpisode()
	if season > 0 && episode > 0 {
		for i := range episodes {
			if episodes[i].Season != season {
				continue
			}
			if btnIMDBEpisodeNumber(episodes[i].EpisodeText) == episode {
				return &episodes[i]
			}
		}
	}
	if len(episodes) == 1 {
		return &episodes[0]
	}
	return nil
}

func btnIMDBEpisodeNumber(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	matches := btnIMDBEpisodePattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return 0
	}
	parsed, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return parsed
}

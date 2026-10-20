// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

// logPreparedFacts reports the finalized generation, never provider or parser
// intermediates. Hydration and subject projection do not repeat this summary.
func (m *Module) logPreparedFacts(ctx context.Context, release api.PreparedRelease, cache string, revision uint64) {
	logger := logging.FromContext(ctx, m.logger)
	identity, media, naming, providers := release.Identity, release.Media, release.Naming, release.ProviderMetadata
	logger.Debugf("prepared release: generation=%d cache=%s correction_revision=%d category=%q type=%q source=%q resolution=%q",
		release.Generation, cache, revision, identity.Category, media.Type, media.Source, naming.Resolution)
	logger.Debugf("prepared release: external ids generation=%d tmdb=%d(%s) imdb=%d(%s) tvdb=%d(%s) tvmaze=%d(%s) mal=%d(%s)",
		release.Generation, identity.TMDBID, identity.Provenance.TMDB, identity.IMDBID, identity.Provenance.IMDB,
		identity.TVDBID, identity.Provenance.TVDB, identity.TVmazeID, identity.Provenance.TVmaze, identity.MALID, identity.Provenance.MAL)
	logger.Debugf("prepared release: provider metadata available generation=%d tmdb=%t imdb=%t tvdb=%t tvmaze=%t anilist=%t bluray=%t",
		release.Generation, providers.TMDB != nil, providers.IMDB != nil, providers.TVDB != nil, providers.TVmaze != nil,
		providers.AniList != nil, providers.Bluray != nil)
	logger.Debugf("prepared release: media generation=%d audio_languages=%q audio_language_source=%q subtitle_languages=%q subtitle_language_source=%q "+
		"container=%q audio=%q channels=%q commentary=%t 3d=%q uhd=%q hdr=%q distributor=%q distributor_source=%q region=%q "+
		"video_encode=%q video_encoder=%q video_codec=%q bit_depth=%q edition=%q cut=%q presentation=%q features=%q repack=%q webdv=%t stream_optimized=%d service=%q",
		release.Generation, media.AudioLanguages, media.AudioLanguagesProvenance, media.SubtitleLanguages, media.SubtitleLanguagesProvenance,
		media.Container, media.Audio, media.Channels, media.Commentary, media.ThreeD, media.UHD, media.HDR, media.Distributor, media.DistributorProvenance,
		media.Region, media.VideoEncode, media.VideoEncoder, media.VideoCodec, media.BitDepth, media.Edition, media.Cut, media.Presentation,
		media.ReleaseFeatures, media.Repack, media.WebDV, media.StreamOptimized, media.Service)
}

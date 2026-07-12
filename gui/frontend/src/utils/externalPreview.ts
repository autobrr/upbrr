// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ExternalPreview } from "../types";

/**
 * Reports whether a preview contains its provider-specific fetched metadata payload.
 * Resolved IDs and shallow preview entries without that payload return false.
 */
export const hasFetchedExternalPreviewData = (preview: ExternalPreview) => {
  switch (preview.Provider) {
    case "imdb":
      return preview.IMDB != null;
    case "tmdb":
      return preview.TMDB != null;
    case "tvdb":
      return preview.TVDB != null;
    case "tvmaze":
      return preview.TVmaze != null;
    case "mal":
      return preview.AniList != null;
    default:
      return false;
  }
};

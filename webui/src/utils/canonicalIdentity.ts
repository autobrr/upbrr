// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  ExternalIdentity,
  ExternalIdentityCandidate,
  ExternalIDInfo,
  ExternalIdentityDraft,
  PreparationDiagnostic,
} from "../types";

export const emptyExternalIdentity = (sourcePath = ""): ExternalIdentity => ({
  SourcePath: sourcePath,
  Generation: 0,
  TMDBID: 0,
  IMDBID: 0,
  TVDBID: 0,
  TVmazeID: 0,
  MALID: 0,
  Category: "unknown",
  Provenance: {},
  Overrides: {},
  Conflict: "none",
  Resolution: {
    SourceFingerprint: "",
    IntentFingerprint: "",
    ContractVersion: "",
  },
  ResolvedAt: "",
});

const provenance = (identity: ExternalIdentity, key: string) =>
  String(identity.Provenance?.[key] || "");

export const externalIdentityDraftFromIdentity = (
  identity: ExternalIdentity,
): ExternalIdentityDraft => ({
  TMDBID: Number(identity.TMDBID || 0),
  IMDBID: Number(identity.IMDBID || 0),
  TVDBID: Number(identity.TVDBID || 0),
  TVmazeID: Number(identity.TVmazeID || 0),
  MALID: Number(identity.MALID || 0),
  Category: String(identity.Category || "unknown"),
  SourceTMDB: provenance(identity, "TMDB"),
  SourceIMDB: provenance(identity, "IMDB"),
  SourceTVDB: provenance(identity, "TVDB"),
  SourceTVmaze: provenance(identity, "TVmaze"),
  SourceMAL: provenance(identity, "MAL"),
});

export const externalIDInfoFromIdentity = (identity: ExternalIdentity): ExternalIDInfo[] => {
  const ids = externalIdentityDraftFromIdentity(identity);
  return [
    { Provider: "tmdb", ID: ids.TMDBID, Source: ids.SourceTMDB },
    { Provider: "imdb", ID: ids.IMDBID, Source: ids.SourceIMDB },
    { Provider: "tvdb", ID: ids.TVDBID, Source: ids.SourceTVDB },
    { Provider: "tvmaze", ID: ids.TVmazeID, Source: ids.SourceTVmaze },
    { Provider: "mal", ID: ids.MALID, Source: ids.SourceMAL },
  ].filter((item) => item.ID > 0);
};

export const candidatesFromDiagnostics = (
  diagnostics: PreparationDiagnostic[],
  provider: "tmdb" | "imdb",
): ExternalIdentityCandidate[] =>
  (diagnostics || [])
    .flatMap((diagnostic) => diagnostic.Candidates || [])
    .filter((candidate) => String(candidate.Provider).toLowerCase() === provider);

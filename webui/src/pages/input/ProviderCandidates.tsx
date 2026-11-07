// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useId, useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import type { InputFacet } from "../../releaseSession/types";
import type { ExternalIdentityCandidate } from "../../types";
import { formatIMDbID } from "../../utils/providerId";

const providerNames: Record<string, string> = { tmdb: "TMDB", imdb: "IMDb", tvdb: "TVDB" };
const overrideKeys = { tmdb: "TMDBID", imdb: "IMDBID", tvdb: "TVDBID" } as const;

const candidateURL = (candidate: ExternalIdentityCandidate) => {
  if (!Number.isSafeInteger(candidate.ID) || candidate.ID <= 0) return "";
  if (candidate.Provider === "imdb")
    return `https://www.imdb.com/title/${formatIMDbID(candidate.ID)}/`;
  if (candidate.Provider === "tvdb" && candidate.Category.toUpperCase() === "TV")
    return `https://thetvdb.com/dereferrer/series/${candidate.ID}`;
  if (candidate.Provider === "tmdb" && ["TV", "MOVIE"].includes(candidate.Category.toUpperCase()))
    return `https://www.themoviedb.org/${candidate.Category.toUpperCase() === "TV" ? "tv" : "movie"}/${candidate.ID}`;
  return "";
};

/** Preserves TMDB preview sizes while loading originals only through the lightbox. */
function CandidatePoster({
  url,
  title,
  onOpen,
}: {
  url: string;
  title: string;
  onOpen(url: string, alt: string): void;
}) {
  const [failed, setFailed] = useState(false);
  const tmdbImage = /^https:\/\/image\.tmdb\.org\/t\/p\/(?:original|w342)\//;
  const fullURL = url.replace(tmdbImage, "https://image.tmdb.org/t/p/original/");
  const thumbnailURL = url.replace(tmdbImage, "https://image.tmdb.org/t/p/w342/");
  return url && !failed ? (
    <button
      type="button"
      className="h-28 w-[75px] shrink-0 self-start rounded-lg border-0 p-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
      aria-label={`View ${title} poster full size`}
      onClick={() => onOpen(fullURL, `${title} poster`)}
    >
      <img
        className="h-full w-full rounded-lg object-cover"
        src={thumbnailURL}
        alt={`${title} poster`}
        loading="lazy"
        onError={() => setFailed(true)}
      />
    </button>
  ) : (
    <span className="flex h-28 w-[75px] shrink-0 items-center justify-center rounded-lg border border-border text-center text-xs text-muted-foreground">
      No image
    </span>
  );
}

/** Keeps image inspection separate from staged provider selections and explicit clears. */
export function ProviderCandidates({
  facet,
  initialQuery,
  category,
  needsSelection,
  loading,
  onRefresh,
  onPosterOpen,
}: {
  facet: InputFacet;
  initialQuery: string;
  category: string;
  needsSelection: boolean;
  loading: boolean;
  onRefresh(): void;
  onPosterOpen(url: string, alt: string): void;
}) {
  const id = useId();
  const [draft, setDraft] = useState(initialQuery);
  const [tab, setTab] = useState("tmdb");
  const categoryKnown = ["TV", "MOVIE"].includes(category);
  const tabs = category === "TV" ? ["tmdb", "imdb", "tvdb"] : ["tmdb", "imdb"];
  const { result, status, error } = facet.view.providerSearch;
  const searchRef = useRef(facet.searchProviders);
  searchRef.current = facet.searchProviders;
  const initialSearchAttempted = useRef(false);
  useEffect(() => {
    if (
      loading ||
      !categoryKnown ||
      !needsSelection ||
      initialSearchAttempted.current ||
      !initialQuery.trim()
    )
      return;
    initialSearchAttempted.current = true;
    if (!result && status !== "running") void searchRef.current(initialQuery);
  }, [categoryKnown, initialQuery, loading, needsSelection, result, status]);

  const provider = result?.Providers.find((item) => item.Provider === tab);
  const resolverCandidates =
    categoryKnown && facet.view.preview?.Identity.Category.toUpperCase() === category
      ? (facet.view.preview.Diagnostics || [])
          .filter((diagnostic) => diagnostic.Code === "external_identity_candidates")
          .flatMap((diagnostic) => diagnostic.Candidates || [])
      : [];
  const candidatesForProvider = (name: string) => {
    const seen = new Set<number>();
    return [
      ...(result?.Providers.find((item) => item.Provider === name)?.Candidates || []).map(
        (candidate) => ({ candidate, fromResolution: false }),
      ),
      ...resolverCandidates.map((candidate) => ({ candidate, fromResolution: true })),
    ].filter(({ candidate }) => {
      const candidateCategory = candidate.Category.toUpperCase();
      if (
        candidate.Provider !== name ||
        !Number.isSafeInteger(candidate.ID) ||
        candidate.ID <= 0 ||
        (candidateCategory && candidateCategory !== "UNKNOWN" && candidateCategory !== category) ||
        (name === "tvdb" && candidateCategory !== "TV") ||
        seen.has(candidate.ID)
      )
        return false;
      seen.add(candidate.ID);
      return true;
    });
  };
  const candidates = candidatesForProvider(tab);
  const identity = facet.view.preview?.Identity;
  const key = overrideKeys[tab as keyof typeof overrideKeys];
  const selectedID = Number(facet.view.intent.identity[key] ?? identity?.[key] ?? 0);
  const submitted = result?.Query || "";
  const normalizedDraft = draft.trim().split(/\s+/).join(" ");
  return (
    <details
      className="group rounded-2xl border border-border bg-card/80 p-4"
      open={needsSelection || undefined}
    >
      <summary className="cursor-pointer font-semibold">
        {needsSelection ? "External ID candidates" : "Search provider titles"}
      </summary>
      <div className="mt-3 grid min-w-0 gap-3">
        <p className="text-sm text-muted-foreground">
          Select a candidate to stage its ID override, or select it again to clear that provider.
          Refresh metadata to apply your changes.
        </p>
        <form
          className="grid gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            void facet.searchProviders(draft);
          }}
        >
          <label htmlFor={`${id}-query`} className="font-medium">
            Search query
          </label>
          <div className="flex flex-wrap gap-2">
            <input
              id={`${id}-query`}
              className="min-w-0 flex-1 rounded-lg border border-input bg-background px-3 py-2"
              value={draft}
              maxLength={512}
              onChange={(event) => setDraft(event.target.value)}
              aria-describedby={`${id}-helper`}
            />
            <Button
              type="submit"
              variant="primary"
              disabled={loading || !categoryKnown || !draft.trim()}
            >
              Search providers
            </Button>
            {status === "running" ? (
              <Button type="button" onClick={facet.cancelProviderSearch}>
                Cancel search
              </Button>
            ) : null}
          </div>
          <p id={`${id}-helper`} className="text-sm text-muted-foreground">
            Include the year to narrow results, e.g. My Example 2015
          </p>
          {normalizedDraft !== submitted && submitted ? (
            <p className="text-sm text-muted-foreground">
              Query edited. Choose Search providers to run the new query.
            </p>
          ) : null}
        </form>
        {!categoryKnown ? (
          <p>Choose Movie or TV under Edit Release Details before searching providers.</p>
        ) : null}
        <div role="status" aria-live="polite">
          {status === "running" ? <p>Searching providers…</p> : null}
          {error ? <p>{error}</p> : null}
          {result ? <p className="font-semibold">Results for “{result.Query}”</p> : null}
        </div>
        <div role="tablist" aria-label="Metadata providers" className="flex flex-wrap gap-2">
          {tabs.map((name, index) => (
            <button
              key={name}
              id={`${id}-${name}-tab`}
              type="button"
              role="tab"
              aria-selected={tab === name}
              aria-controls={`${id}-${name}-panel`}
              tabIndex={tab === name ? 0 : -1}
              className={`rounded-lg border px-3 py-2 ${tab === name ? "border-ring bg-accent" : "border-border"}`}
              onClick={() => setTab(name)}
              onKeyDown={(event) => {
                let next = index;
                if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
                else if (event.key === "ArrowLeft") next = (index + tabs.length - 1) % tabs.length;
                else if (event.key === "Home") next = 0;
                else if (event.key === "End") next = tabs.length - 1;
                else return;
                event.preventDefault();
                setTab(tabs[next]);
                document.getElementById(`${id}-${tabs[next]}-tab`)?.focus();
              }}
            >
              {providerNames[name]}
              {result || resolverCandidates.length > 0
                ? ` (${candidatesForProvider(name).length})`
                : ""}
            </button>
          ))}
        </div>
        {tabs.map((name) => (
          <div
            key={name}
            id={`${id}-${name}-panel`}
            role="tabpanel"
            aria-labelledby={`${id}-${name}-tab`}
            hidden={tab !== name}
            tabIndex={0}
          >
            {tab === name ? (
              <>
                {name === "tvdb" ? (
                  <p className="text-sm text-muted-foreground">TV series only</p>
                ) : null}
                {provider ? (
                  <p className="mb-3 text-sm text-muted-foreground">
                    {providerNames[name]} request: “{provider.Query}”
                    {provider.Year ? ` · year ${provider.Year}` : ""} ·{" "}
                    {provider.Category.toUpperCase() === "TV" ? "TV" : "Movie"}
                    {name === "imdb" ? " (category filtered from returned titles)" : ""}
                  </p>
                ) : null}
                {provider?.Filters ? (
                  <p className="mb-3 text-xs text-muted-foreground">
                    {Object.entries(provider.Filters)
                      .map(([filter, value]) => `${filter}: ${value}`)
                      .join(" · ")}
                  </p>
                ) : null}
                {provider?.Status === "unavailable" ? (
                  <p>Provider search unavailable. Retry the search.</p>
                ) : provider && candidates.every((choice) => choice.fromResolution) ? (
                  <p>No {providerNames[name]} matches for this search.</p>
                ) : null}
                {provider?.Status === "partial" ? (
                  <p className="mb-3 text-sm text-muted-foreground">
                    Some details could not be loaded. Available candidates can still be selected.
                  </p>
                ) : null}
                {candidates.some((choice) => choice.fromResolution) ? (
                  <p className="mb-3 text-sm text-muted-foreground">
                    Choices from metadata resolution may come from a different title or year search.
                  </p>
                ) : null}
                <fieldset className="m-0 grid min-w-0 gap-3 border-0 p-0 lg:grid-cols-2">
                  <legend className="sr-only">{providerNames[name]} candidates</legend>
                  {candidates.map(({ candidate, fromResolution }) => {
                    const inputID = `${id}-${name}-${candidate.ID}`;
                    const url = candidateURL(candidate);
                    return (
                      <article
                        key={candidate.ID}
                        className={`flex min-w-0 flex-col rounded-xl border p-3 ${selectedID === candidate.ID ? "border-ring bg-accent/60" : "border-foreground/20"}`}
                      >
                        <div className="flex-1">
                          <div className="flex gap-3">
                            <CandidatePoster
                              key={candidate.PosterURL}
                              url={candidate.PosterURL}
                              title={candidate.Title || "Candidate"}
                              onOpen={onPosterOpen}
                            />
                            <label
                              htmlFor={inputID}
                              className="grid min-w-0 flex-1 cursor-pointer content-start gap-1 break-words"
                            >
                              <span className="text-lg font-semibold">
                                {candidate.Title || "(Untitled)"}
                              </span>
                              {fromResolution ? (
                                <span className="text-xs text-muted-foreground">
                                  From metadata resolution
                                </span>
                              ) : null}
                              <span className="text-sm text-muted-foreground">
                                {candidate.Year || "Year unavailable"} ·{" "}
                                {candidate.Category || "Unknown category"}
                              </span>
                              <span className="text-sm">
                                {(candidate.RuntimeMinutes || 0) > 0
                                  ? `${candidate.RuntimeMinutes} min${candidate.RuntimeKind === "average_episode" ? " avg / episode" : candidate.RuntimeKind === "episode" ? " / episode" : ""}`
                                  : "Runtime unavailable"}
                              </span>
                              <span className="text-sm">
                                Director: {candidate.Directors?.join(", ") || "Unavailable"}
                              </span>
                              <span className="text-sm">
                                {candidate.CastSource === "billing"
                                  ? "Top cast"
                                  : candidate.CastSource === "principal"
                                    ? "Stars"
                                    : "Cast (provider order)"}
                                : {candidate.Cast?.join(" · ") || "Unavailable"}
                              </span>
                            </label>
                          </div>
                          <label
                            htmlFor={inputID}
                            className="mt-2 line-clamp-3 block cursor-pointer break-words text-sm text-muted-foreground"
                          >
                            {candidate.Overview || "No overview available."}
                          </label>
                          {candidate.DetailsStatus === "partial" ? (
                            <span className="mt-1 block text-xs text-muted-foreground">
                              Some details unavailable.
                            </span>
                          ) : null}
                        </div>
                        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-2 text-sm">
                          <span>{name === "imdb" ? formatIMDbID(candidate.ID) : candidate.ID}</span>
                          {url ? (
                            <a
                              className="underline underline-offset-4"
                              href={url}
                              target="_blank"
                              rel="noopener noreferrer"
                              aria-label={`Open ${providerNames[name]} ${candidate.Title || candidate.ID} in a new tab`}
                            >
                              {providerNames[name]} ↗
                            </a>
                          ) : null}
                          <label className="flex cursor-pointer items-center gap-2">
                            <input
                              id={inputID}
                              type="checkbox"
                              name={`${id}-${name}-selection`}
                              checked={selectedID === candidate.ID}
                              disabled={loading}
                              onChange={(event) =>
                                facet.changeIdentity({
                                  ...facet.view.intent.identity,
                                  [key]: event.currentTarget.checked ? candidate.ID : 0,
                                })
                              }
                              aria-label={`Select ${providerNames[name]} ${candidate.Title || "untitled"} ${candidate.Year || ""}, ID ${candidate.ID}`}
                            />
                            {selectedID === candidate.ID ? "Selected" : "Select"}
                          </label>
                        </div>
                      </article>
                    );
                  })}
                </fieldset>
              </>
            ) : null}
          </div>
        ))}
        <div className="flex justify-end">
          <Button type="button" variant="primary" onClick={onRefresh} disabled={loading}>
            {loading ? "Refreshing..." : "Refresh metadata"}
          </Button>
        </div>
      </div>
    </details>
  );
}

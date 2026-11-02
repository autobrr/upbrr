// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../../components/ui/pageStyle";
import type { InputFacet } from "../../releaseSession/types";
import type { BlurayReleaseCandidate } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = {
  facet: InputFacet;
  setLightboxImage: (url: string) => void;
  setLightboxAlt: (alt: string) => void;
};

const scoreLabel = (candidate: BlurayReleaseCandidate) =>
  candidate.DetailsStatus === "complete" ? `${candidate.Score.toFixed(1)}/100` : "Not compared";

/** Presents scored disc editions, calendar release dates, on-demand inspection, and explicit candidate selection. */
export default function BlurayCandidatesPage(props: Props) {
  const { facet, setLightboxImage, setLightboxAlt } = props;
  const selecting = facet.view.status === "running";
  const error = facet.view.error;
  const bluray = facet.view.preview?.Bluray;
  const candidates = bluray?.Candidates || [];
  const selectedID = bluray?.SelectedReleaseID || "";

  return (
    <section className="flex flex-col gap-3">
      <header className="max-w-3xl">
        <p className={pageStyle.eyebrow}>Blu-ray.com</p>
        <h1>Release Candidates</h1>
        <p className={pageStyle.subtitle}>
          A single matching-country release is selected automatically. Multiple releases use
          disc-spec scores; select another eligible release here.
        </p>
      </header>

      {error ? <p className={pageStyle.error}>{error}</p> : null}

      {!bluray ? (
        <section className={pageStyle.panel}>
          <p className="text-muted-foreground">No Blu-ray.com lookup data available.</p>
        </section>
      ) : (
        <>
          <section className={`${pageStyle.panel} grid gap-2 py-3`}>
            <div className="flex flex-wrap gap-2">
              <button
                type="button"
                className="primary"
                disabled={selecting || !candidates.length}
                onClick={() => void facet.fetchCandidateDetails()}
              >
                Fetch all release details
              </button>
              <button
                type="button"
                className="ghost"
                disabled={selecting}
                onClick={() => void facet.setCandidateSelection("skip")}
              >
                Skip selection
              </button>
              <button
                type="button"
                className="ghost"
                disabled={selecting}
                onClick={() => void facet.setCandidateSelection("auto")}
              >
                Auto selection
              </button>
              {selecting ? (
                <button
                  type="button"
                  className="ghost"
                  disabled={!facet.view.candidateDetailsCancelable}
                  onClick={() => void facet.cancelCandidateDetails()}
                >
                  Cancel
                </button>
              ) : null}
            </div>
            {selecting ? <p role="status">Processing requested Blu-ray.com work…</p> : null}
            <div className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-2">
              <div>
                <p className={pageStyle.label}>Release country</p>
                <p className={pageStyle.value}>{bluray.Country || "Not locked"}</p>
              </div>
              <div>
                <p className={pageStyle.label}>Best score</p>
                <p className={pageStyle.value}>
                  {candidates.some((c) => c.DetailsStatus === "complete")
                    ? bluray.BestScore.toFixed(1)
                    : "Not compared"}
                </p>
              </div>
              <div>
                <p className={pageStyle.label}>Required score</p>
                <p className={pageStyle.value}>
                  {bluray.Threshold === 0 ? "No minimum" : bluray.Threshold.toFixed(1)}
                </p>
              </div>
              <div>
                <p className={pageStyle.label}>Auto-selected</p>
                <p className={pageStyle.value}>{bluray.AutoSelected ? "Yes" : "No"}</p>
              </div>
              <div>
                <p className={pageStyle.label}>Candidates</p>
                <p className={pageStyle.value}>{candidates.length}</p>
              </div>
            </div>
            {bluray.SearchURL ? (
              <a
                className="inline-flex items-center gap-[5px] font-semibold text-foreground underline w-fit"
                href={bluray.SearchURL}
                target="_blank"
                rel="noreferrer"
                onAuxClick={handleExternalLinkClick}
                onClick={handleExternalLinkClick}
              >
                Open search
              </a>
            ) : null}
            {!selectedID && bluray.SelectionReason ? (
              <p className="m-0 rounded-md border border-[var(--status-warning)] bg-card px-2 py-1 text-[0.82rem] text-foreground">
                {bluray.SelectionReason}
              </p>
            ) : null}
          </section>

          {candidates.length === 0 ? (
            <section className={pageStyle.panel}>
              <p className="text-muted-foreground">No release candidates found.</p>
            </section>
          ) : (
            <div className="grid gap-3">
              {candidates.map((candidate, index) => {
                const selected = candidate.ReleaseID === selectedID || candidate.Accepted;
                const hasReleaseID = Boolean(candidate.ReleaseID);
                const candidateLabel = `candidate ${index + 1}: ${candidate.Title || "Untitled release"}`;
                return (
                  <section
                    className={`relative z-[1] rounded-[var(--radius)] border border-border bg-card p-[14px] text-card-foreground shadow-[var(--shadow)] grid gap-3 ${selected ? "border-sidebar-ring" : ""}`}
                    key={candidate.ReleaseID || candidate.URL}
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <h2 className="[overflow-wrap:anywhere]">{candidate.Title || "-"}</h2>
                        <p className="text-muted-foreground [overflow-wrap:anywhere]">
                          {[candidate.MovieTitle, candidate.MovieYear].filter(Boolean).join(" ")}
                        </p>
                      </div>
                      <button
                        className={selected ? "primary" : "ghost"}
                        type="button"
                        aria-label={`${selected ? "Selected" : selecting ? "Selecting..." : "Select"} ${candidateLabel}`}
                        disabled={selecting || selected || !hasReleaseID}
                        onClick={() => {
                          if (hasReleaseID) void facet.selectCandidate(candidate.ReleaseID);
                        }}
                      >
                        {selected ? "Selected" : selecting ? "Selecting..." : "Select"}
                      </button>
                    </div>

                    <div className="flex flex-wrap items-center gap-2">
                      <button
                        type="button"
                        className="ghost"
                        disabled={selecting}
                        aria-label={`Fetch details for ${candidateLabel}`}
                        onClick={() => void facet.fetchCandidateDetails(candidate.ReleaseID)}
                      >
                        Fetch release details
                      </button>
                      <p role={candidate.DetailsStatus === "failed" ? "alert" : undefined}>
                        {candidate.DetailsStatus === "complete"
                          ? "Details fetched"
                          : candidate.DetailsStatus === "failed"
                            ? "Release details unavailable"
                            : "Details not fetched"}
                      </p>
                    </div>
                    <div className="grid grid-cols-[repeat(auto-fit,minmax(120px,1fr))] gap-2">
                      <div>
                        <p className={pageStyle.label}>Score</p>
                        <p className={pageStyle.value}>{scoreLabel(candidate)}</p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Country</p>
                        <p className={pageStyle.value}>{candidate.Country || "-"}</p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Country code</p>
                        <p className={pageStyle.value}>{candidate.Region || "-"}</p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Publisher</p>
                        <p className={pageStyle.value}>{candidate.Publisher || "-"}</p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Release date</p>
                        <p className={pageStyle.value}>
                          {candidate.ReleaseDate ? (
                            <time dateTime={candidate.ReleaseDate}>{candidate.ReleaseDate}</time>
                          ) : candidate.DetailsStatus ? (
                            "Unavailable"
                          ) : (
                            "Not fetched"
                          )}
                        </p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Disc</p>
                        <p className={pageStyle.value}>{candidate.Specs?.Discs?.Format || "-"}</p>
                      </div>
                    </div>

                    {candidate.URL ? (
                      <a
                        className="inline-flex items-center gap-[5px] font-semibold text-foreground underline w-fit"
                        href={candidate.URL}
                        target="_blank"
                        rel="noreferrer"
                        onAuxClick={handleExternalLinkClick}
                        onClick={handleExternalLinkClick}
                      >
                        Open release
                      </a>
                    ) : null}

                    <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-2">
                      <div>
                        <p className={pageStyle.label}>Video</p>
                        <p className={pageStyle.value}>
                          {[candidate.Specs?.Video?.Codec, candidate.Specs?.Video?.Resolution]
                            .filter(Boolean)
                            .join(" ") || "-"}
                        </p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Audio</p>
                        <p className={`${pageStyle.value} [overflow-wrap:anywhere]`}>
                          {(candidate.Specs?.Audio || []).slice(0, 3).join("; ") || "-"}
                        </p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Subtitles</p>
                        <p className={`${pageStyle.value} [overflow-wrap:anywhere]`}>
                          {(candidate.Specs?.Subtitles || []).slice(0, 6).join(", ") || "-"}
                        </p>
                      </div>
                      <div>
                        <p className={pageStyle.label}>Playback region</p>
                        <p className={pageStyle.value}>
                          {candidate.Specs?.Playback?.Region
                            ? `${candidate.Specs.Playback.Region}${candidate.Specs.Playback.RegionNotes ? ` (${candidate.Specs.Playback.RegionNotes})` : ""}`
                            : "-"}
                        </p>
                      </div>
                    </div>

                    {candidate.MatchNotes?.length ? (
                      <details className="min-w-0 rounded-md border border-border p-3">
                        <summary
                          className="cursor-pointer font-semibold focus-visible:outline-2 focus-visible:outline-ring"
                          aria-label={`Score details for ${candidateLabel}`}
                        >
                          Score details ({candidate.MatchNotes.length})
                        </summary>
                        <ul className="mt-2 list-disc pl-5 text-sm [overflow-wrap:anywhere]">
                          {candidate.MatchNotes.map((note, noteIndex) => (
                            <li key={noteIndex}>{note}</li>
                          ))}
                        </ul>
                      </details>
                    ) : null}

                    {candidate.CoverImages?.length ? (
                      <div className="grid grid-cols-[repeat(auto-fit,minmax(120px,160px))] gap-2">
                        {candidate.CoverImages.map((image, imageIndex) => (
                          <button
                            className="cursor-pointer border-0 bg-transparent p-0"
                            type="button"
                            aria-label={`Preview ${candidateLabel} ${image.Kind || "cover"} image ${imageIndex + 1}`}
                            key={`${candidate.ReleaseID}-${image.Kind}-${image.URL}`}
                            onClick={() => {
                              setLightboxImage(image.URL);
                              setLightboxAlt(`${candidate.Title} ${image.Kind}`);
                            }}
                          >
                            <img
                              className="w-full rounded-md border border-border"
                              src={image.PreviewURL || image.URL}
                              alt={image.Kind || "Blu-ray cover"}
                              loading="lazy"
                            />
                          </button>
                        ))}
                      </div>
                    ) : null}
                  </section>
                );
              })}
            </div>
          )}
        </>
      )}
    </section>
  );
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { BlurayReleaseCandidate, MetadataPreview } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = {
  preview: MetadataPreview;
  selecting: boolean;
  error: string;
  onSelect: (releaseID: string) => void;
  setLightboxImage: (url: string) => void;
  setLightboxAlt: (alt: string) => void;
};

const scoreLabel = (candidate: BlurayReleaseCandidate) => `${candidate.Score.toFixed(1)}/100`;

export default function BlurayCandidatesPage(props: Props) {
  const { preview, selecting, error, onSelect, setLightboxImage, setLightboxAlt } = props;
  const bluray = preview.Bluray;
  const candidates = bluray?.Candidates || [];
  const selectedID = bluray?.SelectedReleaseID || "";

  return (
    <section className="flex flex-col gap-3">
      <header className="max-w-3xl">
        <p className="eyebrow">Blu-ray.com</p>
        <h1>Release Candidates</h1>
        <p className="subtitle">Best accepted match loads by score; select another release here.</p>
      </header>

      {error ? <p className="error">{error}</p> : null}

      {!bluray ? (
        <section className="panel">
          <p className="muted">No Blu-ray.com lookup data available.</p>
        </section>
      ) : (
        <>
          <section className="panel grid gap-2 py-3">
            <div className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-2">
              <div>
                <p className="label">Best score</p>
                <p className="value">{bluray.BestScore.toFixed(1)}</p>
              </div>
              <div>
                <p className="label">Required score</p>
                <p className="value">{bluray.Threshold.toFixed(1)}</p>
              </div>
              <div>
                <p className="label">Auto-selected</p>
                <p className="value">{bluray.AutoSelected ? "Yes" : "No"}</p>
              </div>
              <div>
                <p className="label">Candidates</p>
                <p className="value">{candidates.length}</p>
              </div>
            </div>
            {bluray.SearchURL ? (
              <a
                className="tracker-link w-fit"
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
              <p className="m-0 rounded-md border border-amber-400/40 bg-amber-400/10 px-2 py-1 text-[0.82rem] text-amber-100">
                {bluray.SelectionReason}
              </p>
            ) : null}
          </section>

          {candidates.length === 0 ? (
            <section className="panel">
              <p className="muted">No release candidates found.</p>
            </section>
          ) : (
            <div className="grid gap-3">
              {candidates.map((candidate) => {
                const selected = candidate.ReleaseID === selectedID || candidate.Accepted;
                const hasReleaseID = Boolean(candidate.ReleaseID);
                return (
                  <section
                    className={`panel grid gap-3 ${selected ? "border-[var(--sidebar-active-border)]" : ""}`}
                    key={candidate.ReleaseID || candidate.URL}
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <h2 className="[overflow-wrap:anywhere]">{candidate.Title || "-"}</h2>
                        <p className="muted [overflow-wrap:anywhere]">
                          {[candidate.MovieTitle, candidate.MovieYear].filter(Boolean).join(" ")}
                        </p>
                      </div>
                      <button
                        className={selected ? "primary" : "ghost"}
                        type="button"
                        disabled={selecting || selected || !hasReleaseID}
                        onClick={() => {
                          if (hasReleaseID) onSelect(candidate.ReleaseID);
                        }}
                      >
                        {selected ? "Selected" : selecting ? "Selecting..." : "Select"}
                      </button>
                    </div>

                    <div className="grid grid-cols-[repeat(auto-fit,minmax(120px,1fr))] gap-2">
                      <div>
                        <p className="label">Score</p>
                        <p className="value">{scoreLabel(candidate)}</p>
                      </div>
                      <div>
                        <p className="label">Country</p>
                        <p className="value">{candidate.Country || "-"}</p>
                      </div>
                      <div>
                        <p className="label">Region</p>
                        <p className="value">{candidate.Region || "-"}</p>
                      </div>
                      <div>
                        <p className="label">Publisher</p>
                        <p className="value">{candidate.Publisher || "-"}</p>
                      </div>
                      <div>
                        <p className="label">Disc</p>
                        <p className="value">{candidate.Specs?.Discs?.Format || "-"}</p>
                      </div>
                    </div>

                    {candidate.URL ? (
                      <a
                        className="tracker-link w-fit"
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
                        <p className="label">Video</p>
                        <p className="value">
                          {[candidate.Specs?.Video?.Codec, candidate.Specs?.Video?.Resolution]
                            .filter(Boolean)
                            .join(" ") || "-"}
                        </p>
                      </div>
                      <div>
                        <p className="label">Audio</p>
                        <p className="value [overflow-wrap:anywhere]">
                          {(candidate.Specs?.Audio || []).slice(0, 3).join("; ") || "-"}
                        </p>
                      </div>
                      <div>
                        <p className="label">Subtitles</p>
                        <p className="value [overflow-wrap:anywhere]">
                          {(candidate.Specs?.Subtitles || []).slice(0, 6).join(", ") || "-"}
                        </p>
                      </div>
                    </div>

                    {candidate.MatchNotes?.length ? (
                      <div>
                        <p className="label">Score notes</p>
                        <p className="value [overflow-wrap:anywhere]">
                          {candidate.MatchNotes.slice(0, 5).join(" | ")}
                        </p>
                      </div>
                    ) : null}

                    {candidate.CoverImages?.length ? (
                      <div className="grid grid-cols-[repeat(auto-fit,minmax(120px,160px))] gap-2">
                        {candidate.CoverImages.map((image) => (
                          <button
                            className="cursor-pointer border-0 bg-transparent p-0"
                            type="button"
                            key={`${candidate.ReleaseID}-${image.Kind}-${image.URL}`}
                            onClick={() => {
                              setLightboxImage(image.URL);
                              setLightboxAlt(`${candidate.Title} ${image.Kind}`);
                            }}
                          >
                            <img
                              className="w-full rounded-md border border-[var(--border)]"
                              src={image.URL}
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

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo } from "react";
import type { Dispatch, SetStateAction } from "react";
import RenderedDescription from "../../components/RenderedDescription";
import { Button } from "../../components/ui/button";
import type { MetadataPreview, TrackerPreview } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = {
  preview: MetadataPreview;
  renderedDescriptions: Record<string, boolean>;
  setRenderedDescriptions: Dispatch<SetStateAction<Record<string, boolean>>>;
  setLightboxImage: Dispatch<SetStateAction<string>>;
  setLightboxAlt: Dispatch<SetStateAction<string>>;
};

export default function TrackerDataPage(props: Props) {
  const {
    preview,
    renderedDescriptions,
    setRenderedDescriptions,
    setLightboxImage,
    setLightboxAlt,
  } = props;

  const trackerDataOrdered = useMemo(() => {
    const items = preview.TrackerData || [];
    if (items.length === 0) {
      return { items: [], primaryIndex: -1 };
    }
    const hasActualData = (item: TrackerPreview) =>
      Boolean(
        item.Description ||
        item.DescriptionHTML ||
        (item.ImageURLs && item.ImageURLs.length > 0) ||
        item.TMDBID ||
        item.IMDBID ||
        item.TVDBID ||
        item.MALID ||
        item.InfoHash ||
        item.Category ||
        item.Filename,
      );
    const primaryIndex = items.findIndex(hasActualData);
    if (primaryIndex <= 0) {
      return { items, primaryIndex };
    }
    const primary = items[primaryIndex];
    const rest = items.filter((_, index) => index !== primaryIndex);
    return { items: [primary, ...rest], primaryIndex: 0 };
  }, [preview.TrackerData]);

  return (
    <section className="flex flex-col gap-3">
      <header className="max-w-3xl">
        <p className="eyebrow">Tracker Data</p>
        <h1>Input Metadata</h1>
        <p className="subtitle">Tracker-provided metadata, descriptions, and images.</p>
      </header>
      {preview.TrackerData.length === 0 ? (
        <p className="muted">No tracker data available.</p>
      ) : (
        <div className="grid gap-3">
          {trackerDataOrdered.items.map((item, index) => {
            const trackerKey = `${item.Tracker}-${index}`;
            const isRendered =
              Boolean(renderedDescriptions[trackerKey]) && Boolean(item.DescriptionHTML);
            const renderedHTML = isRendered ? item.DescriptionHTML : "";
            const isPrimary = index === trackerDataOrdered.primaryIndex;
            return (
              <details
                className="overflow-hidden rounded-lg border border-white/10 bg-[rgba(12,16,26,0.78)]"
                key={trackerKey}
                open={isPrimary}
              >
                <summary className="flex cursor-pointer list-none items-center justify-between gap-3 px-3 py-2 font-semibold marker:content-[''] [&::-webkit-details-marker]:hidden">
                  <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
                    {item.Tracker || "Unknown"}
                  </span>
                  <span className="whitespace-nowrap text-sm font-medium text-[var(--muted)]">
                    Torrent ID: {item.TrackerID || "-"}
                  </span>
                </summary>
                <div className="grid gap-3 border-t border-white/10 px-3 pb-3 pt-2">
                  <div className="grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-2">
                    <div>
                      <p className="label">Tracker</p>
                      {item.TorrentURL ? (
                        <a
                          className="tracker-link"
                          href={item.TorrentURL}
                          target="_blank"
                          rel="noreferrer"
                          onClick={handleExternalLinkClick}
                        >
                          {item.Tracker || "Unknown"}
                        </a>
                      ) : (
                        <p className="value">{item.Tracker || "Unknown"}</p>
                      )}
                    </div>
                    <div>
                      <p className="label">Matched</p>
                      <p className="value">{item.Matched ? "Yes" : "No"}</p>
                    </div>
                    <div>
                      <p className="label">Updated</p>
                      <p className="value">{item.UpdatedAt || "-"}</p>
                    </div>
                  </div>
                  <div className="grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-2">
                    <div className="min-w-0">
                      <p className="label">Torrent ID</p>
                      <p className="value mono [overflow-wrap:anywhere]">{item.TrackerID || "-"}</p>
                    </div>
                    <div className="min-w-0">
                      <p className="label">Info Hash</p>
                      <p className="value mono [overflow-wrap:anywhere]">{item.InfoHash || "-"}</p>
                    </div>
                    <div className="min-w-0">
                      <p className="label">Category</p>
                      <p className="value [overflow-wrap:anywhere]">{item.Category || "-"}</p>
                    </div>
                    <div className="min-w-0">
                      <p className="label">Filename</p>
                      <p className="value [overflow-wrap:anywhere]">{item.Filename || "-"}</p>
                    </div>
                  </div>
                  <div className="grid grid-cols-[repeat(auto-fit,minmax(120px,1fr))] gap-2">
                    <div>
                      <p className="label">TMDB</p>
                      <p className="value mono">{item.TMDBID || 0}</p>
                    </div>
                    <div>
                      <p className="label">IMDB</p>
                      <p className="value mono">{item.IMDBID || 0}</p>
                    </div>
                    <div>
                      <p className="label">TVDB</p>
                      <p className="value mono">{item.TVDBID || 0}</p>
                    </div>
                    <div>
                      <p className="label">MAL</p>
                      <p className="value mono">{item.MALID || 0}</p>
                    </div>
                  </div>
                  <div>
                    <div className="flex items-center justify-between gap-2">
                      <h2>Description</h2>
                      {item.DescriptionHTML ? (
                        <Button
                          className="h-7 rounded-full px-2 text-xs"
                          type="button"
                          onClick={() =>
                            setRenderedDescriptions((prev) => ({
                              ...prev,
                              [trackerKey]: !prev[trackerKey],
                            }))
                          }
                        >
                          {isRendered ? "Show raw" : "Render"}
                        </Button>
                      ) : null}
                    </div>
                    {isRendered ? (
                      <RenderedDescription html={renderedHTML} />
                    ) : (
                      <p className="tracker-description">
                        {item.Description || "No description provided."}
                      </p>
                    )}
                  </div>
                  <div>
                    <h2>Images</h2>
                    {item.ImageURLs.length === 0 ? (
                      <p className="muted">No images provided.</p>
                    ) : (
                      <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-2">
                        {item.ImageURLs.map((url, imageIndex) => (
                          <button
                            className="cursor-pointer border-0 bg-transparent p-0"
                            type="button"
                            key={`${url}-${imageIndex}`}
                            onClick={() => {
                              setLightboxImage(url);
                              setLightboxAlt(`${item.Tracker || "Tracker"} image`);
                            }}
                          >
                            <img
                              className="w-full rounded-lg border border-white/10"
                              src={url}
                              alt="Tracker"
                              loading="lazy"
                            />
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              </details>
            );
          })}
        </div>
      )}
    </section>
  );
}

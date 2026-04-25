// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo } from "react";
import type { Dispatch, SetStateAction } from "react";
import type { MetadataPreview, TrackerPreview } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";
import "./styles.css";

type Props = {
  preview: MetadataPreview;
  renderedDescriptions: Record<string, boolean>;
  setRenderedDescriptions: Dispatch<SetStateAction<Record<string, boolean>>>;
  setLightboxImage: Dispatch<SetStateAction<string>>;
  setLightboxAlt: Dispatch<SetStateAction<string>>;
};

const decodeHtmlEntities = (value: string) => {
  if (!value) return value;
  if (!value.includes("&lt;") && !value.includes("&gt;") && !value.includes("&#")) {
    return value;
  }
  const textarea = document.createElement("textarea");
  textarea.innerHTML = value;
  return textarea.value;
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
    <section className="tracker-panel">
      <header className="tracker-header">
        <p className="eyebrow">Tracker Data</p>
        <h1>Input Metadata</h1>
        <p className="subtitle">Tracker-provided metadata, descriptions, and images.</p>
      </header>
      {preview.TrackerData.length === 0 ? (
        <p className="muted">No tracker data available.</p>
      ) : (
        <div className="tracker-grid">
          {trackerDataOrdered.items.map((item, index) => {
            const trackerKey = `${item.Tracker}-${index}`;
            const isRendered =
              Boolean(renderedDescriptions[trackerKey]) && Boolean(item.DescriptionHTML);
            const renderedHTML = isRendered ? decodeHtmlEntities(item.DescriptionHTML) : "";
            const isPrimary = index === trackerDataOrdered.primaryIndex;
            return (
              <details className="tracker-card" key={trackerKey} open={isPrimary}>
                <summary className="tracker-card__summary">
                  <span className="tracker-card__summary-name">{item.Tracker || "Unknown"}</span>
                  <span className="tracker-card__summary-id">
                    Torrent ID: {item.TrackerID || "-"}
                  </span>
                </summary>
                <div className="tracker-card__body">
                  <div className="tracker-card__header">
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
                  <div className="tracker-card__meta">
                    <div>
                      <p className="label">Torrent ID</p>
                      <p className="value mono tracker-meta-value">{item.TrackerID || "-"}</p>
                    </div>
                    <div>
                      <p className="label">Info Hash</p>
                      <p className="value mono tracker-meta-value">{item.InfoHash || "-"}</p>
                    </div>
                    <div>
                      <p className="label">Category</p>
                      <p className="value tracker-meta-value">{item.Category || "-"}</p>
                    </div>
                    <div>
                      <p className="label">Filename</p>
                      <p className="value tracker-meta-value">{item.Filename || "-"}</p>
                    </div>
                  </div>
                  <div className="tracker-card__ids">
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
                  <div className="tracker-card__desc">
                    <div className="tracker-desc__header">
                      <h2>Description</h2>
                      {item.DescriptionHTML ? (
                        <button
                          className="tracker-desc__toggle"
                          type="button"
                          onClick={() =>
                            setRenderedDescriptions((prev) => ({
                              ...prev,
                              [trackerKey]: !prev[trackerKey],
                            }))
                          }
                        >
                          {isRendered ? "Show raw" : "Render"}
                        </button>
                      ) : null}
                    </div>
                    {isRendered ? (
                      <div
                        className="tracker-description rendered"
                        onClick={handleExternalLinkClick}
                        dangerouslySetInnerHTML={{ __html: renderedHTML }}
                      />
                    ) : (
                      <p className="tracker-description">
                        {item.Description || "No description provided."}
                      </p>
                    )}
                  </div>
                  <div className="tracker-card__images">
                    <h2>Images</h2>
                    {item.ImageURLs.length === 0 ? (
                      <p className="muted">No images provided.</p>
                    ) : (
                      <div className="tracker-images">
                        {item.ImageURLs.map((url, imageIndex) => (
                          <button
                            className="tracker-image-button"
                            type="button"
                            key={`${url}-${imageIndex}`}
                            onClick={() => {
                              setLightboxImage(url);
                              setLightboxAlt(`${item.Tracker || "Tracker"} image`);
                            }}
                          >
                            <img src={url} alt="Tracker" loading="lazy" />
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

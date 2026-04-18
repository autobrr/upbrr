// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { Dispatch, SetStateAction } from "react";
import type { PreparationPreview } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = {
  path: string;
  prepLoading: boolean;
  prepError: string;
  prepPreview: PreparationPreview;
  prepRendered: Record<string, boolean>;
  setPrepRendered: Dispatch<SetStateAction<Record<string, boolean>>>;
  runPreparation: () => void;
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

const renderImageHostMessage = (message: string, reuploaded: boolean) => {
  if (!message || !reuploaded) return null;
  return <p className="muted">{message}</p>;
};

export default function PreparationPage(props: Props) {
  const {
    path,
    prepLoading,
    prepError,
    prepPreview,
    prepRendered,
    setPrepRendered,
    runPreparation,
  } = props;

  const prepDescriptions = prepPreview.Descriptions || [];
  const prepHasDescriptions = prepDescriptions.length > 0;

  return (
    <section className="prepare-panel">
      <header className="prepare-header">
        <p className="eyebrow">Final Preparation</p>
        <h1>Review Description</h1>
        <p className="subtitle">Inspect the description that will be sent to trackers.</p>
      </header>

      <section className="panel prepare-actions">
        <div>
          <p className="label">Source path</p>
          <p className="value dupe-path">{path || "No path selected"}</p>
        </div>
        <button
          className="primary"
          type="button"
          onClick={runPreparation}
          disabled={prepLoading || !path.trim()}
        >
          {prepLoading ? "Preparing..." : "Build preparation"}
        </button>
      </section>

      {prepError ? <p className="error">{prepError}</p> : null}

      <section className="panel prepare-card">
        <div className="prepare-card__header">
          <h2>Description</h2>
        </div>
        {prepHasDescriptions ? (
          <div className="prepare-list">
            {prepDescriptions.map((entry, index) => {
              const entryKey = `prep-${index}`;
              const isRendered = prepRendered[entryKey] ?? true;
              const renderedHTML = entry.DescriptionHTML
                ? decodeHtmlEntities(entry.DescriptionHTML)
                : "";
              return (
                <article className="prepare-entry" key={entryKey}>
                  <div className="prepare-entry__header">
                    <div>
                      <p className="label">Trackers</p>
                      <p className="value">{entry.Trackers?.join(", ") || "Unknown"}</p>
                      {renderImageHostMessage(
                        entry.ImageHost?.Message || "",
                        Boolean(entry.ImageHost?.Reuploaded),
                      )}
                    </div>
                    {entry.DescriptionHTML ? (
                      <button
                        className="tracker-desc__toggle"
                        type="button"
                        onClick={() =>
                          setPrepRendered((prev) => ({
                            ...prev,
                            [entryKey]: !isRendered,
                          }))
                        }
                      >
                        {isRendered ? "Show raw" : "Render"}
                      </button>
                    ) : null}
                  </div>
                  {isRendered && entry.DescriptionHTML ? (
                    <div
                      className="tracker-description rendered"
                      onClick={handleExternalLinkClick}
                      dangerouslySetInnerHTML={{ __html: renderedHTML }}
                    />
                  ) : (
                    <p className="tracker-description">
                      {entry.Description || "No description provided."}
                    </p>
                  )}
                </article>
              );
            })}
          </div>
        ) : (
          <p className="muted">No description generated yet.</p>
        )}
      </section>
    </section>
  );
}

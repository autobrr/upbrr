// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import RenderedDescription from "../../components/RenderedDescription";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import type { TrackerIconCache } from "../../hooks/useTrackerIcons";
import { trackerIconFor } from "../../hooks/useTrackerIcons";
import type { DescriptionsFacet } from "../../releaseSession/types";

type Props = {
  facet: DescriptionsFacet;
  sourcePath: string;
  useFavicons?: boolean;
  faviconOnly?: boolean;
  trackerIconSrcByName?: TrackerIconCache;
};

const groupLabel = (groupKey: string, trackers: string[]) => {
  if (trackers.length > 0) return trackers.join(", ");
  if (groupKey === "unit3d") return "Unit3D";
  return groupKey || "Description";
};

export default function DescriptionBuilderPage(props: Props) {
  const {
    facet,
    sourcePath,
    useFavicons = true,
    faviconOnly = false,
    trackerIconSrcByName = {},
  } = props;
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({});
  const { view } = facet;
  const groups = view.preview?.Groups || [];
  const builderLoading = view.status === "running";
  const builderError = view.error;
  const builderSaved = view.notice;

  return (
    <section className="flex flex-col gap-3">
      <header className="max-w-3xl">
        <p className="eyebrow">Description Builder</p>
        <h1>Customize Description</h1>
        <p className="subtitle">
          Edit tracker-group raw descriptions here. Tracker-specific formatting is applied from this
          builder.
        </p>
      </header>

      <section className="panel flex flex-wrap items-center justify-between gap-3 py-3">
        <div className="min-w-0">
          <p className="label">Source path</p>
          <p className="value [overflow-wrap:anywhere] text-sm">
            {sourcePath || "No path selected"}
          </p>
        </div>
        <button
          className="ghost"
          type="button"
          onClick={() => void facet.load()}
          disabled={builderLoading || !sourcePath.trim()}
        >
          {builderLoading ? "Refreshing..." : "Refresh descriptions"}
        </button>
      </section>

      {builderError ? <p className="error">{builderError}</p> : null}
      {builderSaved ? <p className="success">{builderSaved}</p> : null}

      {builderLoading && groups.length === 0 ? (
        <section className="panel">
          <div className="mb-2 flex flex-col gap-1">
            <h2>Building Descriptions</h2>
          </div>
          <p className="muted">
            Preparing tracker-group descriptions and image-host adjustments...
          </p>
        </section>
      ) : groups.length === 0 ? (
        <section className="panel">
          <p className="muted">No tracker descriptions generated yet.</p>
        </section>
      ) : (
        groups.map((group, i) => {
          const groupKey = group.GroupKey;
          const reactKey = groupKey || `default-${i}`;
          const seededRaw = group.RawDescription || "";
          const raw = view.rawByGroup[groupKey] ?? seededRaw;
          const seededRendered = group.RawDescriptionHTML || "";
          const renderedHTML = view.renderedByGroup[groupKey] ?? seededRendered;
          const expanded = expandedGroups[groupKey] ?? false;
          const trackers = (group.Trackers || []).map((tracker) => tracker.trim()).filter(Boolean);
          const label = groupLabel(groupKey, trackers);
          const hideTrackerNames = faviconOnly && useFavicons && trackers.length > 0;
          const imageHostWarnings = group.ImageHost?.Warnings || [];

          return (
            <section className="panel grid gap-3" key={reactKey}>
              <div className="mb-1 flex flex-wrap items-start justify-between gap-3">
                <div>
                  <h2>
                    <span
                      aria-label={hideTrackerNames ? label : undefined}
                      className="inline-flex flex-wrap items-center gap-1.5"
                    >
                      {trackers.map((tracker) => (
                        <TrackerIconImage
                          tracker={tracker}
                          iconSrc={trackerIconFor(trackerIconSrcByName, tracker)}
                          enabled={useFavicons}
                          key={`${groupKey}-${tracker}`}
                        />
                      ))}
                      {hideTrackerNames ? null : label}
                    </span>
                  </h2>
                  <p className="muted">
                    {group.HasOverride
                      ? "Saved override active for this group."
                      : "Using generated raw description."}
                  </p>
                  {group.ImageHost?.Reuploaded && group.ImageHost?.Message ? (
                    <p className="muted">{group.ImageHost.Message}</p>
                  ) : null}
                  {group.ImageHost?.Status === "warning" && group.ImageHost?.Message ? (
                    <p className="m-0 mt-1 rounded-md border border-amber-400/40 bg-amber-400/10 px-2 py-1 text-[0.82rem] text-amber-100 [overflow-wrap:anywhere]">
                      {group.ImageHost.Message}
                    </p>
                  ) : null}
                  {imageHostWarnings.map((warning, index) => {
                    const host = String(warning.Host || "").trim();
                    const message = String(warning.Message || "").trim();
                    if (!host && !message) return null;
                    return (
                      <p
                        className="m-0 mt-1 rounded-md border border-amber-400/40 bg-amber-400/10 px-2 py-1 text-[0.82rem] text-amber-100 [overflow-wrap:anywhere]"
                        key={`${host || "host"}-${index}`}
                      >
                        {host ? `${host} failed` : "Image host warning"}
                        {message ? `: ${message}` : ""}
                      </p>
                    );
                  })}
                </div>
                <button
                  className="ghost"
                  type="button"
                  onClick={() =>
                    setExpandedGroups((prev) => ({
                      ...prev,
                      [groupKey]: !expanded,
                    }))
                  }
                >
                  {expanded ? "Collapse" : "Expand"}
                </button>
              </div>

              {expanded ? (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      className="ghost"
                      type="button"
                      onClick={() => void facet.reset(groupKey)}
                      disabled={builderLoading || !sourcePath.trim()}
                    >
                      {builderLoading ? "Working..." : "Reset group"}
                    </button>
                    <button
                      className="ghost"
                      type="button"
                      onClick={() => void facet.render(groupKey)}
                      disabled={builderLoading}
                    >
                      {builderLoading ? "Working..." : "Render"}
                    </button>
                    <button
                      className="primary"
                      type="button"
                      onClick={() => void facet.save(groupKey)}
                      disabled={builderLoading || !sourcePath.trim()}
                    >
                      {builderLoading ? "Working..." : "Save group"}
                    </button>
                  </div>

                  <section className="panel">
                    <div className="mb-2 flex flex-col gap-1">
                      <h2>Raw Description</h2>
                      <p className="muted">
                        This final raw description is the upload source of truth for{" "}
                        {hideTrackerNames ? "this group" : label}.
                      </p>
                    </div>
                    <textarea
                      className="min-h-[170px] w-full resize-y rounded-lg border border-white/10 bg-black/25 px-3 py-2 text-[0.95rem] leading-6 text-[var(--text)]"
                      value={raw}
                      onChange={(event) => {
                        const nextValue = event.target.value;
                        facet.edit(groupKey, nextValue);
                      }}
                    />
                  </section>

                  <section className="panel">
                    <div className="mb-2 flex flex-col gap-1">
                      <h2>Rendered Raw Preview</h2>
                    </div>
                    {renderedHTML ? (
                      <RenderedDescription html={renderedHTML} />
                    ) : (
                      <p className="muted">No rendered preview yet.</p>
                    )}
                  </section>
                </>
              ) : null}
            </section>
          );
        })
      )}
    </section>
  );
}

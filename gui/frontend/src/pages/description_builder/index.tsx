// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { Dispatch, SetStateAction } from "react";
import type { DescriptionBuilderPreview } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";
import "./styles.css";

type Props = {
  path: string;
  builderPreview: DescriptionBuilderPreview;
  builderRawByGroup: Record<string, string>;
  builderRenderedByGroup: Record<string, string>;
  builderExpandedGroups: Record<string, boolean>;
  builderLoading: boolean;
  builderSaving: boolean;
  builderRenderLoading: boolean;
  builderRefreshing: boolean;
  builderError: string;
  builderSaved: string;
  refreshDescriptionBuilder: () => void;
  setBuilderRawByGroup: Dispatch<SetStateAction<Record<string, string>>>;
  setBuilderDirtyByGroup: Dispatch<SetStateAction<Record<string, boolean>>>;
  setBuilderExpandedGroups: Dispatch<SetStateAction<Record<string, boolean>>>;
  resetBuilderDescription: (groupKey: string) => void;
  renderBuilderDescription: (groupKey: string) => void;
  saveBuilderDescription: (groupKey: string) => void;
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

const groupLabel = (groupKey: string, trackers: string[]) => {
  if (trackers.length > 0) return trackers.join(", ");
  if (groupKey === "unit3d") return "Unit3D";
  return groupKey || "Description";
};

export default function DescriptionBuilderPage(props: Props) {
  const {
    path,
    builderPreview,
    builderRawByGroup,
    builderRenderedByGroup,
    builderExpandedGroups,
    builderLoading,
    builderSaving,
    builderRenderLoading,
    builderRefreshing,
    builderError,
    builderSaved,
    refreshDescriptionBuilder,
    setBuilderRawByGroup,
    setBuilderDirtyByGroup,
    setBuilderExpandedGroups,
    resetBuilderDescription,
    renderBuilderDescription,
    saveBuilderDescription,
  } = props;

  const groups = builderPreview.Groups || [];

  return (
    <section className="builder-panel">
      <header className="builder-header">
        <p className="eyebrow">Description Builder</p>
        <h1>Customize Description</h1>
        <p className="subtitle">
          Edit tracker-group raw descriptions here. Tracker-specific formatting is applied from this
          builder.
        </p>
      </header>

      <section className="panel builder-actions">
        <div>
          <p className="label">Source path</p>
          <p className="value dupe-path">{path || "No path selected"}</p>
        </div>
        <button
          className="ghost"
          type="button"
          onClick={refreshDescriptionBuilder}
          disabled={builderLoading || builderSaving || builderRenderLoading || !path.trim()}
        >
          {builderRefreshing ? "Refreshing..." : "Refresh descriptions"}
        </button>
      </section>

      {builderError ? <p className="error">{builderError}</p> : null}
      {builderSaved ? <p className="success">{builderSaved}</p> : null}

      {builderLoading && groups.length === 0 ? (
        <section className="panel builder-preview">
          <div className="builder-preview__header">
            <h2>Building Descriptions</h2>
          </div>
          <p className="muted">
            Preparing tracker-group descriptions and image-host adjustments...
          </p>
        </section>
      ) : groups.length === 0 ? (
        <section className="panel builder-preview">
          <p className="muted">No tracker descriptions generated yet.</p>
        </section>
      ) : (
        groups.map((group, i) => {
          const groupKey = group.GroupKey;
          const reactKey = groupKey || `default-${i}`;
          const seededRaw = group.RawDescription || "";
          const raw = builderRawByGroup[groupKey] ?? seededRaw;
          const seededRendered = group.RawDescriptionHTML || "";
          const renderedHTML = builderRenderedByGroup[groupKey] ?? seededRendered;
          const expanded = builderExpandedGroups[groupKey] ?? false;
          const label = groupLabel(groupKey, group.Trackers || []);

          return (
            <section className="panel builder-preview" key={reactKey}>
              <div className="builder-editor__header">
                <div>
                  <h2>{label}</h2>
                  <p className="muted">
                    {group.HasOverride
                      ? "Saved override active for this group."
                      : "Using generated raw description."}
                  </p>
                  {group.ImageHost?.Reuploaded && group.ImageHost?.Message ? (
                    <p className="muted">{group.ImageHost.Message}</p>
                  ) : null}
                </div>
                <button
                  className="ghost"
                  type="button"
                  onClick={() =>
                    setBuilderExpandedGroups((prev) => ({
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
                  <div className="builder-actions__buttons">
                    <button
                      className="ghost"
                      type="button"
                      onClick={() => resetBuilderDescription(groupKey)}
                      disabled={builderLoading || builderSaving || !path.trim()}
                    >
                      {builderLoading ? "Resetting..." : "Reset group"}
                    </button>
                    <button
                      className="ghost"
                      type="button"
                      onClick={() => renderBuilderDescription(groupKey)}
                      disabled={builderRenderLoading}
                    >
                      {builderRenderLoading ? "Rendering..." : "Render"}
                    </button>
                    <button
                      className="primary"
                      type="button"
                      onClick={() => saveBuilderDescription(groupKey)}
                      disabled={builderSaving || !path.trim()}
                    >
                      {builderSaving ? "Saving..." : "Save group"}
                    </button>
                  </div>

                  <section className="panel builder-editor">
                    <div className="builder-editor__header">
                      <h2>Raw Description</h2>
                      <p className="muted">
                        This saved raw description is the upload source of truth for {label}.
                      </p>
                    </div>
                    <textarea
                      className="builder-textarea"
                      value={raw}
                      onChange={(event) => {
                        const nextValue = event.target.value;
                        setBuilderRawByGroup((prev) => ({ ...prev, [groupKey]: nextValue }));
                        setBuilderDirtyByGroup((prev) => ({ ...prev, [groupKey]: true }));
                      }}
                    />
                  </section>

                  <section className="panel builder-preview">
                    <div className="builder-preview__header">
                      <h2>Rendered Raw Preview</h2>
                    </div>
                    {renderedHTML ? (
                      <div
                        className="tracker-description rendered"
                        onClick={handleExternalLinkClick}
                        dangerouslySetInnerHTML={{ __html: decodeHtmlEntities(renderedHTML) }}
                      />
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

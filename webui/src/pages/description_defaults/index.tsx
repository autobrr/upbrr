// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useQuery } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { descriptionDefaultsClient, type DescriptionLayoutItem } from "../../api/app";
import RenderedDescription from "../../components/RenderedDescription";
import { pageStyle } from "../../components/ui/pageStyle";
import type { useSettingsState } from "../../hooks/useSettingsState";
import type { ConfigMap, ConfigValue, FieldMeta } from "../../types";

type Props = Readonly<{
  settings: ReturnType<typeof useSettingsState>;
  renderField: (key: string, value: ConfigValue, path: string[], meta?: FieldMeta) => ReactNode;
}>;

/** Reproduces the backend's slot order while preserving fixed section positions. */
function orderItems(items: readonly DescriptionLayoutItem[], saved: readonly string[]) {
  const byID = new Map(items.filter((item) => item.id).map((item) => [item.id, item]));
  const selected = new Set(saved.filter((id) => byID.has(id)));
  const ordered = [...selected].map((id) => byID.get(id)!);
  let i = 0;
  return items.map((item) => (selected.has(item.id) ? ordered[i++] : item));
}

export default function DescriptionDefaultsPage({ settings, renderField }: Props) {
  const { settingsConfigData, settingsLoading, settingsDirty, settingsSaved, settingsError } =
    settings;
  const value = settingsConfigData?.Description;
  const draft = value && typeof value === "object" && !Array.isArray(value) ? value : null;
  const [selection, setSelection] = useState("");
  const [sample, setSample] = useState("movie");
  const [dragged, setDragged] = useState("");
  const catalog = useQuery({
    queryKey: ["description-defaults-trackers", settings.configData],
    queryFn: ({ signal }) => descriptionDefaultsClient.list(signal),
  });
  const tracker = catalog.data?.find((entry) => entry.name === selection) ?? catalog.data?.[0];
  const tracked = tracker?.name ?? "";
  const preview = useQuery({
    queryKey: ["description-defaults-preview", tracked, sample, draft, settings.configData],
    gcTime: 0,
    enabled: Boolean(tracked && draft),
    queryFn: ({ signal }) =>
      descriptionDefaultsClient.preview(tracked, sample, draft ?? {}, signal),
  });

  const saved = Array.isArray(draft?.SectionOrder)
    ? draft.SectionOrder.filter((entry): entry is string => typeof entry === "string")
    : [];
  const headingValue = draft?.Labels;
  const headings =
    headingValue && typeof headingValue === "object" && !Array.isArray(headingValue)
      ? headingValue
      : {};
  const optionValue = draft?.ItemOptions;
  const itemOptions =
    optionValue && typeof optionValue === "object" && !Array.isArray(optionValue)
      ? optionValue
      : {};
  const displayOptions = (id: string): ConfigMap => {
    const options = itemOptions[id];
    return options && typeof options === "object" && !Array.isArray(options) ? options : {};
  };
  const updateOption = (id: string, key: "Hidden" | "Compact", value: boolean) => {
    const next = { ...displayOptions(id), [key]: value };
    const all = { ...itemOptions };
    if (next.Hidden || next.Compact) all[id] = next;
    else delete all[id];
    settings.updateConfigValue(["Description", "ItemOptions"], all);
  };
  const items = orderItems(tracker?.items ?? [], saved);
  const movable = items.filter((item) => item.id);
  const move = (from: string, to: string) => {
    const order = movable.map((item) => item.id);
    const source = order.indexOf(from);
    const target = order.indexOf(to);
    if (source < 0 || target < 0 || source === target) return;
    order.splice(source, 1);
    order.splice(target, 0, from);
    const retained = saved.filter((id) => !order.includes(id));
    settings.updateConfigValue(["Description", "SectionOrder"], [...retained, ...order]);
  };

  return (
    <div className="flex flex-col gap-4">
      <header className="relative z-[1] max-w-[720px]">
        <p className={pageStyle.eyebrow}>upbrr</p>
        <h1>Descriptions</h1>
        <p className={pageStyle.subtitle}>
          Set defaults for new site descriptions using a sample release.
        </p>
      </header>

      <section className={pageStyle.panel} aria-label="Description settings controls">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <p className="helper">
            Saving applies these defaults to newly generated descriptions, not to saved edits.
          </p>
          <div className="flex items-center gap-2">
            <button
              className="ghost"
              type="button"
              disabled={settingsLoading}
              onClick={() => {
                if (!settingsDirty || window.confirm("Discard unsaved settings and reload?")) {
                  settings.loadSettings();
                  if (catalog.isError) void catalog.refetch();
                  if (preview.isError) void preview.refetch();
                }
              }}
            >
              Reload
            </button>
            <button
              className="primary"
              type="button"
              disabled={settingsLoading || !settingsDirty}
              onClick={settings.handleSaveSettings}
            >
              Save
            </button>
          </div>
        </div>
        {settingsSaved ? <p role="status">{settingsSaved}</p> : null}
        {settingsError ? (
          <p role="alert" className={pageStyle.error}>
            {settingsError}
          </p>
        ) : null}
      </section>

      {catalog.isError ? (
        <p role="alert" className={pageStyle.error}>
          Could not load description preview trackers.
        </p>
      ) : catalog.isPending || !draft ? (
        <p role="status">Loading description defaults…</p>
      ) : !tracker ? (
        <p role="status" className={pageStyle.panel}>
          No configured tracker supports a description preview. Configure a tracker in Settings to
          get started.
        </p>
      ) : (
        <div className="grid min-w-0 gap-4 xl:grid-cols-2">
          <section className={pageStyle.panel} aria-label="Description layout">
            <div className="flex flex-wrap items-center gap-3">
              <label>
                Preview tracker
                <select
                  className="ml-2"
                  value={tracked}
                  onChange={(event) => setSelection(event.target.value)}
                >
                  {catalog.data?.map(({ name }) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
              </label>
              <button
                type="button"
                className="ghost"
                disabled={!saved.some((id) => movable.some((item) => item.id === id))}
                onClick={() => {
                  const ids = new Set(movable.map((item) => item.id));
                  settings.updateConfigValue(
                    ["Description", "SectionOrder"],
                    saved.filter((id) => !ids.has(id)),
                  );
                }}
              >
                Reset order
              </button>
            </div>
            <p className="helper">
              Drag an item by its name, or use the move buttons. Tracker-required groups stay
              together.
            </p>
            <ol className="flex flex-col gap-2">
              {items.map(({ id, label, fields, labels, canHide, canCompact }) => {
                const index = movable.findIndex((item) => item.id === id);
                return (
                  <li
                    key={id || label}
                    className="rounded-md border p-3"
                    onDragOver={(event) => {
                      if (id && dragged) event.preventDefault();
                    }}
                    onDrop={(event) => {
                      if (!id || !dragged) return;
                      event.preventDefault();
                      move(dragged, id);
                      setDragged("");
                    }}
                  >
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span
                        draggable={Boolean(id)}
                        onDragStart={(event) => {
                          event.dataTransfer.setData("text/plain", id);
                          event.dataTransfer.effectAllowed = "move";
                          setDragged(id);
                        }}
                        onDragEnd={() => setDragged("")}
                      >
                        {label}
                      </span>
                      {id ? (
                        <div className="flex gap-2">
                          <button
                            type="button"
                            className="ghost"
                            aria-label={`Move ${label} up`}
                            disabled={index <= 0}
                            onClick={() => move(id, movable[index - 1].id)}
                          >
                            ↑
                          </button>
                          <button
                            type="button"
                            className="ghost"
                            aria-label={`Move ${label} down`}
                            disabled={index >= movable.length - 1}
                            onClick={() => move(id, movable[index + 1].id)}
                          >
                            ↓
                          </button>
                        </div>
                      ) : null}
                    </div>
                    {canHide || canCompact ? (
                      <div className="mt-2 flex flex-wrap gap-3">
                        {canHide ? (
                          <label className="flex items-center gap-1">
                            <input
                              type="checkbox"
                              checked={displayOptions(id).Hidden !== true}
                              onChange={(event) =>
                                updateOption(id, "Hidden", !event.target.checked)
                              }
                            />
                            Show {label.toLowerCase()}
                          </label>
                        ) : null}
                        {canCompact ? (
                          <label className="flex items-center gap-1">
                            <input
                              type="checkbox"
                              checked={displayOptions(id).Compact === true}
                              onChange={(event) =>
                                updateOption(id, "Compact", event.target.checked)
                              }
                            />
                            Compact spacing before {label.toLowerCase()}
                          </label>
                        ) : null}
                      </div>
                    ) : null}
                    {labels?.map((heading) => (
                      <label key={heading.key} className="mt-2 block">
                        {heading.label}
                        <input
                          className="ml-2 min-w-0"
                          value={String(headings[heading.key] ?? "")}
                          placeholder={heading.default}
                          onChange={(event) => {
                            const value = event.target.value;
                            const next = { ...headings };
                            if (value) next[heading.key] = value;
                            else delete next[heading.key];
                            settings.updateConfigValue(["Description", "Labels"], next);
                          }}
                        />
                      </label>
                    ))}
                    {fields?.map((field) => (
                      <div key={field}>
                        {renderField(
                          field,
                          draft[field] ?? "",
                          ["Description", field],
                          settings.sectionFieldMeta.Description?.[field],
                        )}
                      </div>
                    ))}
                  </li>
                );
              })}
            </ol>
          </section>
          <section className={pageStyle.panel} aria-label="Mock description">
            <h2 className="text-lg font-semibold">Sample description</h2>
            <label>
              Sample release
              <select
                className="ml-2"
                value={sample}
                onChange={(event) => setSample(event.target.value)}
              >
                <option value="movie">Movie</option>
                <option value="episode">TV episode</option>
                <option value="dvd">DVD</option>
                <option value="bluray">Blu-ray</option>
              </select>
            </label>
            {preview.isError ? (
              <p role="alert" className={pageStyle.error}>
                Could not render the sample description.
              </p>
            ) : null}
            {preview.isPending ? <p role="status">Rendering sample…</p> : null}
            {preview.data ? (
              <RenderedDescription html={preview.data} />
            ) : preview.isSuccess ? (
              <p>This tracker has no visible sections for the sample.</p>
            ) : null}
          </section>
        </div>
      )}
    </div>
  );
}

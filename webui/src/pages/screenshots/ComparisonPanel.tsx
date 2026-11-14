// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import type {
  ComparisonCell,
  ComparisonPlan,
  ComparisonSource,
  ComparisonPreferences,
} from "../../api/generated/release-workflow";
import { Button } from "../../components/ui/button";
import { Checkbox } from "../../components/ui/checkbox";
import { Select } from "../../components/ui/select";
import { pageStyle } from "../../components/ui/pageStyle";
import { HostBrowserDialog } from "../../features/hostBrowser/HostBrowserDialog";
import type { ComparisonsFacet } from "../../releaseSession/types";

type Props = Readonly<{
  facet: ComparisonsFacet;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

const inputClass = "min-w-0 rounded border border-input bg-card p-2 text-card-foreground";
const emptyPlan = (imported: boolean): ComparisonPlan => ({
  sources: imported
    ? []
    : [
        {
          id: "main",
          label: "Main input",
          imported: false,
          overlay: true,
          allowedFrameTypes: ["B"],
          preferBFrames: true,
          toneMap: false,
        },
      ],
  rows: [],
  mainSourceId: imported ? "" : "main",
  imported,
  reuseMainScreenshots: false,
  preferences: { count: 3, motionPercent: 0, darkPercent: 0, motionCount: 0, darkCount: 0 },
  fingerprint: "",
});
const editableChoices = (plan: ComparisonPlan) => ({
  ...plan,
  fingerprint: "",
  approvedFingerprint: "",
  problems: undefined,
  warnings: undefined,
});

// Old plans retain their percentage meaning until edited; an edit fixes both
// targets as the row counts shown here before changing the total row count.
const scenePreferences = (preferences: ComparisonPreferences) => ({
  ...preferences,
  motionCount:
    preferences.motionCount ?? Math.ceil((preferences.count * preferences.motionPercent) / 100),
  darkCount:
    preferences.darkCount ?? Math.ceil((preferences.count * preferences.darkPercent) / 100),
  motionPercent: 0,
  darkPercent: 0,
});

/** Configures, corrects and reviews workflow-owned comparison images before hosting. */
export default function ComparisonPanel({ facet, setLightboxImage, setLightboxAlt }: Props) {
  const { view } = facet;
  const [enabled, setEnabled] = useState(Boolean(view.plan));
  const [draft, setDraft] = useState<ComparisonPlan>(view.plan || emptyPlan(false));
  const [paths, setPaths] = useState<Readonly<Record<string, string>>>({});
  const [positions, setPositions] = useState<Readonly<Record<string, number>>>({});
  const [browseSource, setBrowseSource] = useState("");
  const [files, setFiles] = useState<readonly File[]>([]);
  const positionFor = (sourceID: string) =>
    positions[sourceID] ?? view.previewPositions[sourceID] ?? 0;
  const blocked = view.busy || Boolean(view.blockedReason);
  const dirty =
    JSON.stringify(editableChoices(draft)) !==
      JSON.stringify(editableChoices(view.plan || emptyPlan(false))) ||
    Object.values(paths).some(Boolean);
  const approved = Boolean(
    view.plan?.fingerprint && view.plan.approvedFingerprint === view.plan.fingerprint,
  );
  const preferences = scenePreferences(draft.preferences);
  const validScenes =
    draft.imported ||
    (Number.isInteger(preferences.count) &&
      preferences.count > 0 &&
      [preferences.motionCount, preferences.darkCount].every(
        (count) => Number.isInteger(count) && count >= 0 && count <= preferences.count,
      ));
  const sourceOrder = new Map(draft.sources.map((source, index) => [source.id, index]));
  const change = (next: ComparisonPlan) =>
    setDraft({ ...next, fingerprint: "", approvedFingerprint: "" });
  const sourceChange = (id: string, values: Partial<ComparisonSource>) =>
    change({
      ...draft,
      sources: draft.sources.map((source) =>
        source.id === id ? { ...source, ...values } : source,
      ),
    });
  const updateCell = (rowID: string, index: number, values: Partial<ComparisonCell>) =>
    change({
      ...draft,
      rows: draft.rows.map((row) =>
        row.id === rowID
          ? {
              ...row,
              cells: row.cells.map((cell, cellIndex) =>
                cellIndex === index ? { ...cell, ...values } : cell,
              ),
            }
          : row,
      ),
    });
  const submit = async (action: "configure" | "match" | "capture", plan = draft) => {
    const applied = await facet.edit({
      action,
      plan: {
        ...plan,
        sources: plan.sources.map((source) =>
          source.imported || plan.imported ? source : { ...source, overlay: true },
        ),
      },
      videoSources: Object.entries(paths)
        .filter(([, path]) => path.trim())
        .map(([id, path]) => ({ id, path })),
    });
    if (applied) setPaths({});
    return applied;
  };
  const moveSource = (index: number, direction: number) => {
    const sources = [...draft.sources];
    [sources[index], sources[index + direction]] = [sources[index + direction]!, sources[index]!];
    change({ ...draft, sources });
  };
  const addSource = () => {
    let next = draft.sources.length + 1;
    while (
      draft.sources.some((source) => source.id === `column-${next}`) ||
      view.plan?.sources.some((source) => source.id === `column-${next}`)
    )
      next++;
    change({
      ...draft,
      sources: [
        ...draft.sources,
        {
          id: `column-${next}`,
          label: `Source ${next}`,
          imported: draft.imported,
          overlay: !draft.imported,
          ...(!draft.imported
            ? { allowedFrameTypes: ["B"], preferBFrames: true, toneMap: false }
            : {}),
        },
      ],
    });
  };
  const removeSource = (id: string) => {
    change({
      ...draft,
      sources: draft.sources.filter((source) => source.id !== id),
      sizeReferenceSourceId: draft.sizeReferenceSourceId === id ? "" : draft.sizeReferenceSourceId,
      rows: draft.rows.map((row) => ({
        ...row,
        cells: row.cells.filter((cell) => cell.sourceId !== id),
      })),
      anchors: draft.anchors?.filter((anchor) => anchor.sourceId !== id),
    });
    setPaths((previous) => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
    setPositions((previous) => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
  };
  const addManualRow = () => {
    let next = draft.rows.length + 1;
    while (draft.rows.some((row) => row.id === `row-${next}`)) next++;
    change({
      ...draft,
      rows: [
        ...draft.rows,
        {
          id: `row-${next}`,
          excluded: false,
          cells: draft.sources.map((source) => ({
            sourceId: source.id,
            timestampSeconds: positionFor(source.id),
          })),
        },
      ],
    });
  };
  const moveCell = (rowID: string, index: number, target: string) => {
    target = target.trim();
    if (!target || target === rowID) return;
    const cell = draft.rows.find((row) => row.id === rowID)?.cells[index];
    if (!cell) return;
    let rows = draft.rows.map((row) =>
      row.id === rowID
        ? { ...row, cells: row.cells.filter((_, cellIndex) => cellIndex !== index) }
        : row,
    );
    if (rows.some((row) => row.id === target))
      rows = rows.map((row) => (row.id === target ? { ...row, cells: [...row.cells, cell] } : row));
    else rows = [...rows, { id: target, excluded: false, cells: [cell] }];
    change({ ...draft, rows });
  };
  const showImage = (url: string, label: string) => {
    setLightboxImage(url);
    setLightboxAlt(label);
  };
  const previewSource = async (
    sourceID: string,
    timestampSeconds: number,
    stepDirection?: -1 | 0 | 1,
  ) => {
    const preview = await facet.preview(sourceID, timestampSeconds, stepDirection);
    if (preview)
      setPositions((previous) => ({ ...previous, [sourceID]: preview.timestampSeconds }));
  };

  return (
    <section
      className={`${pageStyle.panel} grid min-w-0 gap-3 [overflow-wrap:anywhere]`}
      aria-label="Comparisons"
    >
      <div>
        <h2>Comparisons</h2>
        <p className="text-muted-foreground">
          Create matched images from videos or import prepared images. Every retained row needs
          review before any comparison image is uploaded.
        </p>
      </div>
      {view.error && <p role="alert">{view.error}</p>}
      {view.blockedReason && <p role="status">{view.blockedReason}</p>}
      {!enabled ? (
        <div className="flex flex-wrap gap-2">
          <Button
            disabled={blocked}
            onClick={() => {
              setDraft(emptyPlan(false));
              setEnabled(true);
            }}
          >
            Create from videos
          </Button>
          <Button
            disabled={blocked}
            onClick={() => {
              setDraft(emptyPlan(true));
              setEnabled(true);
            }}
          >
            Import existing comparisons
          </Button>
        </div>
      ) : (
        <fieldset disabled={blocked} className="grid min-w-0 gap-3">
          <legend className="sr-only">Comparison setup and review</legend>
          {draft.imported && (
            <div className="grid gap-2">
              <p>
                Imported pixels are retained unchanged. Their video timing, picture type, matching
                confidence and processing provenance are unknown.
              </p>
              <label className="grid gap-1">
                <span>Prepared comparison images</span>
                <input
                  type="file"
                  accept="image/png,image/jpeg,image/webp"
                  multiple
                  onChange={(event) => setFiles(Array.from(event.target.files || []))}
                />
              </label>
              <p className="text-muted-foreground">
                Names such as 01-02-encode.png identify row 1, column 2. Other names can be mapped
                after import; incomplete rows must be corrected or excluded.
              </p>
              <Button
                disabled={!files.length}
                onClick={() => void facet.importImages(files, draft)}
              >
                Import images
              </Button>
            </div>
          )}

          <div className="flex flex-wrap items-end gap-3">
            <label className="grid gap-1">
              <span>Main-input column</span>
              <Select
                value={draft.mainSourceId}
                aria-describedby={
                  view.plan?.problems?.length ? "comparison-corrections" : undefined
                }
                disabled={!draft.imported}
                onChange={(event) => change({ ...draft, mainSourceId: event.target.value })}
              >
                <option value="">Choose the main-input column</option>
                {draft.sources.map((source) => (
                  <option key={source.id} value={source.id}>
                    {source.label || source.id}
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex items-center gap-2">
              <Checkbox
                checked={draft.reuseMainScreenshots}
                onCheckedChange={(checked) => change({ ...draft, reuseMainScreenshots: checked })}
              />
              Use the main column as normal screenshots
            </label>
          </div>

          {!draft.imported && (
            <label className="grid gap-1">
              <span>Comparison frame size</span>
              <Select
                aria-label="Comparison frame size"
                aria-describedby="comparison-size-help"
                value={draft.sizeReferenceSourceId || ""}
                onChange={(event) =>
                  change({ ...draft, sizeReferenceSourceId: event.target.value })
                }
              >
                <option value="">Keep each source's original size</option>
                {draft.sources
                  .filter((source) => !source.imported)
                  .map((source) => (
                    <option key={source.id} value={source.id}>
                      Match {source.label || source.id}
                      {source.width && source.height ? ` · ${source.width} × ${source.height}` : ""}
                    </option>
                  ))}
              </Select>
              <span id="comparison-size-help" className="text-muted-foreground">
                Fit videos to the selected source's display size, adding centered padding when
                aspect ratios differ. No cropping or stretching. Apply setup before previewing.
              </span>
            </label>
          )}

          <div className="grid gap-3 lg:grid-cols-2">
            {draft.sources.map((source, index) => {
              const preview = view.previews[source.id];
              const position = positionFor(source.id);
              const setPosition = (value: number) =>
                setPositions((previous) => ({
                  ...previous,
                  [source.id]: Math.max(
                    0,
                    Math.min(value, source.durationSeconds || Number.MAX_SAFE_INTEGER),
                  ),
                }));
              return (
                <fieldset
                  className="grid min-w-0 gap-2 rounded border border-border p-3"
                  key={source.id}
                  aria-invalid={
                    view.plan?.problems?.some(
                      (problem) => problem.sourceId === source.id && !problem.rowId,
                    ) || undefined
                  }
                  aria-describedby={
                    view.plan?.problems?.some((problem) => problem.sourceId === source.id)
                      ? "comparison-corrections"
                      : undefined
                  }
                >
                  <legend>
                    {source.label || source.id} · column {index + 1}
                  </legend>
                  <label className="grid gap-1">
                    <span>Column label</span>
                    <input
                      className={inputClass}
                      value={source.label}
                      onChange={(event) => sourceChange(source.id, { label: event.target.value })}
                    />
                  </label>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      disabled={index === 0}
                      aria-label={`Move ${source.label || source.id} left`}
                      onClick={() => moveSource(index, -1)}
                    >
                      Move left
                    </Button>
                    <Button
                      disabled={index === draft.sources.length - 1}
                      aria-label={`Move ${source.label || source.id} right`}
                      onClick={() => moveSource(index, 1)}
                    >
                      Move right
                    </Button>
                    <Button
                      disabled={source.id === draft.mainSourceId}
                      aria-label={`Remove ${source.label || source.id} column`}
                      onClick={() => removeSource(source.id)}
                    >
                      Remove column
                    </Button>
                  </div>
                  {!source.imported && (
                    <>
                      {source.id !== "main" && (
                        <div className="flex gap-2">
                          <input
                            className={`${inputClass} flex-1`}
                            aria-label={`Video path for ${source.label || source.id}`}
                            value={paths[source.id] || ""}
                            placeholder={
                              source.fingerprint
                                ? "Registered video; enter a replacement path"
                                : "Server-local video path"
                            }
                            onChange={(event) =>
                              setPaths((previous) => ({
                                ...previous,
                                [source.id]: event.target.value,
                              }))
                            }
                          />
                          <Button onClick={() => setBrowseSource(source.id)}>Browse video</Button>
                        </div>
                      )}
                      <p>
                        {source.width || "?"} × {source.height || "?"} ·{" "}
                        {source.frameRate?.toFixed(3) || "unknown"} FPS ·{" "}
                        {source.durationSeconds?.toFixed(1) || "unknown"} seconds
                      </p>
                      <fieldset className="flex flex-wrap gap-3">
                        <legend>Allowed picture types · unchecked means any</legend>
                        <label className="flex items-center gap-1">
                          <Checkbox
                            checked={Boolean(source.preferBFrames)}
                            onCheckedChange={(preferBFrames) =>
                              sourceChange(source.id, {
                                preferBFrames,
                                ...(preferBFrames ? { allowedFrameTypes: ["B"] } : {}),
                              })
                            }
                          />
                          Default B-frame preference
                        </label>
                        {["I", "P", "B"].map((type) => (
                          <label className="flex items-center gap-1" key={type}>
                            <Checkbox
                              checked={Boolean(source.allowedFrameTypes?.includes(type))}
                              onCheckedChange={(checked) =>
                                sourceChange(source.id, {
                                  preferBFrames: false,
                                  allowedFrameTypes: checked
                                    ? [...(source.allowedFrameTypes || []), type]
                                    : source.allowedFrameTypes?.filter((value) => value !== type),
                                })
                              }
                            />
                            {type}
                          </label>
                        ))}
                      </fieldset>
                      {source.preferBFrames && (
                        <p className="text-muted-foreground">
                          Prefer B frames. The main column can fall back to P, then I. Automatic
                          matches for a non-I main frame can fall back only to P. Explicit picture
                          types and manual correspondence stay as selected.
                        </p>
                      )}
                      <p className="text-muted-foreground">
                        Comparison captures include the column label, observed PTS and picture type.
                      </p>
                      <label className="grid gap-1">
                        <span>Tone mapping · {source.label}</span>
                        <Select
                          aria-label={`Tone mapping · ${source.label}`}
                          aria-describedby={`comparison-tonemap-${index}`}
                          value={source.toneMap == null ? "default" : source.toneMap ? "on" : "off"}
                          onChange={(event) =>
                            sourceChange(source.id, {
                              toneMap:
                                event.target.value === "default"
                                  ? undefined
                                  : event.target.value === "on",
                            })
                          }
                        >
                          <option value="off">Off</option>
                          <option value="on">On for HDR</option>
                          {source.toneMap == null && (
                            <option value="default">Use screenshot setting</option>
                          )}
                        </Select>
                        <span id={`comparison-tonemap-${index}`} className="text-muted-foreground">
                          Uses the configured tone-mapping renderer. Inspect processing outcomes;
                          renderer fallback may leave colours inaccurate.
                        </span>
                      </label>
                      <label className="grid gap-1">
                        <span>Approximate frame seek · {source.label}</span>
                        <input
                          className={inputClass}
                          type="number"
                          min="0"
                          step="1"
                          disabled={!source.frameRate}
                          aria-describedby={`comparison-position-${index}`}
                          value={source.frameRate ? Math.round(position * source.frameRate) : ""}
                          onChange={(event) =>
                            setPosition((Number(event.target.value) || 0) / (source.frameRate || 1))
                          }
                        />
                      </label>
                      <output
                        id={`comparison-position-${index}`}
                        aria-live="off"
                        className="text-xs text-muted-foreground"
                        aria-label={`Preview seconds · ${source.label}`}
                      >
                        Preview seconds: {position.toFixed(3)} · position from this source's start.
                      </output>
                      <p className="text-sm text-muted-foreground">
                        Frame-number seeking estimates from average FPS. Previous and next use
                        actual decoded frames.
                      </p>
                      <input
                        aria-label={`Timeline for ${source.label}`}
                        type="range"
                        min="0"
                        max={source.durationSeconds || 0}
                        step={source.frameRate ? 1 / source.frameRate : 1}
                        value={Math.min(position, source.durationSeconds || 0)}
                        disabled={!source.durationSeconds}
                        onChange={(event) => setPosition(Number(event.target.value))}
                      />
                      <div className="flex flex-wrap gap-2">
                        <Button
                          disabled={dirty || !source.fingerprint}
                          onClick={() => void previewSource(source.id, position, -1)}
                        >
                          Previous frame
                        </Button>
                        <Button
                          disabled={dirty || !source.fingerprint}
                          onClick={() => void previewSource(source.id, position, 1)}
                        >
                          Next frame
                        </Button>
                        <Button
                          disabled={dirty || !source.fingerprint}
                          onClick={() => void previewSource(source.id, position)}
                        >
                          Preview {source.label}
                        </Button>
                      </div>
                      {preview && (
                        <div className="grid gap-1">
                          <button
                            type="button"
                            className="justify-self-start"
                            onClick={() =>
                              showImage(preview.contentUrl, `${source.label} comparison preview`)
                            }
                          >
                            <img
                              src={preview.contentUrl}
                              className="max-h-40 max-w-full"
                              alt={`${source.label} comparison preview`}
                            />
                          </button>
                          <p>
                            Actual preview: {preview.timestampSeconds.toFixed(3)}s ·{" "}
                            {preview.pictureType || "unknown picture type"}
                          </p>
                          {preview.processing && (
                            <p role="status">
                              {preview.processing.warning ||
                                `Renderer: ${preview.processing.renderer}; tonemapping applied: ${preview.processing.tonemapped ? "yes" : "no"}.`}
                            </p>
                          )}
                        </div>
                      )}
                      {source.id !== draft.mainSourceId && (
                        <Button
                          disabled={!preview || !view.previews[draft.mainSourceId]}
                          onClick={() =>
                            change({
                              ...draft,
                              anchors: [
                                ...(draft.anchors || []),
                                {
                                  sourceId: source.id,
                                  mainSeconds: view.previews[draft.mainSourceId]!.timestampSeconds,
                                  sourceSeconds: preview!.timestampSeconds,
                                },
                              ],
                            })
                          }
                        >
                          Use displayed previews as an anchor
                        </Button>
                      )}
                    </>
                  )}
                </fieldset>
              );
            })}
          </div>
          <Button onClick={addSource}>
            {draft.imported ? "Add mapped column" : "Add comparison video"}
          </Button>

          {!draft.imported && (
            <>
              <div className="grid gap-2 sm:grid-cols-3">
                {(
                  [
                    ["count", "Comparison rows", 1],
                    ["motionCount", "Fast-motion scenes (rows)", 0],
                    ["darkCount", "Dark scenes (rows)", 0],
                  ] as const
                ).map(([key, label, min]) => (
                  <label className="grid gap-1" key={key}>
                    <span>{label}</span>
                    <input
                      className={inputClass}
                      type="number"
                      min={min}
                      max={key === "count" ? undefined : preferences.count}
                      step={1}
                      aria-invalid={
                        !Number.isInteger(preferences[key]) ||
                        preferences[key] < min ||
                        (key !== "count" && preferences[key] > preferences.count) ||
                        undefined
                      }
                      aria-describedby="comparison-scene-help"
                      value={preferences[key]}
                      onChange={(event) =>
                        change({
                          ...draft,
                          preferences: {
                            ...preferences,
                            [key]: Number(event.target.value) || 0,
                          },
                        })
                      }
                    />
                  </label>
                ))}
              </div>
              <p
                id="comparison-scene-help"
                className="text-muted-foreground"
                role={validScenes ? undefined : "alert"}
              >
                Scene targets must be whole row counts from 0 to the comparison row count. A row
                qualifying as both dark and fast motion counts toward both targets. Unavailable
                scenes remain reported as unmet.
              </p>
              {(draft.anchors || []).map((anchor, index) => (
                <div
                  className="flex flex-wrap items-center gap-2"
                  key={`${anchor.sourceId}-${index}`}
                >
                  <span>
                    Anchor: main {anchor.mainSeconds.toFixed(3)}s ↔{" "}
                    {draft.sources.find((source) => source.id === anchor.sourceId)?.label ||
                      anchor.sourceId}{" "}
                    {anchor.sourceSeconds.toFixed(3)}s
                  </span>
                  <Button
                    aria-label={`Remove anchor ${index + 1}`}
                    onClick={() =>
                      change({
                        ...draft,
                        anchors: draft.anchors?.filter((_, candidate) => candidate !== index),
                      })
                    }
                  >
                    Remove anchor
                  </Button>
                </div>
              ))}
            </>
          )}

          <div className="flex flex-wrap gap-2">
            <Button disabled={!validScenes} onClick={() => void submit("configure")}>
              Apply comparison setup
            </Button>
            {!draft.imported && (
              <>
                <Button
                  disabled={!validScenes || draft.sources.length < 2}
                  onClick={() => void submit("match")}
                >
                  Find matching scenes
                </Button>
                <Button disabled={draft.sources.length < 2} onClick={addManualRow}>
                  Add manual row from timeline positions
                </Button>
                <Button
                  disabled={!validScenes || !draft.rows.length}
                  onClick={() => void submit("capture")}
                >
                  Capture comparison rows
                </Button>
              </>
            )}
            <Button
              onClick={async () => {
                if (
                  await facet.edit({
                    action: "configure",
                    plan: { ...emptyPlan(false), sources: [] },
                  })
                ) {
                  setEnabled(false);
                  setDraft(emptyPlan(false));
                  setPaths({});
                  setPositions({});
                  setFiles([]);
                }
              }}
            >
              Remove comparisons
            </Button>
          </div>
          {dirty && (
            <p role="status">
              Comparison choices changed. Apply or capture the changes before reviewing and
              approving them.
            </p>
          )}
          {view.plan?.sceneMix && (
            <p>
              Last measured scene mix: dark {view.plan.sceneMix.achievedDark}/
              {view.plan.sceneMix.requestedDark}; fast motion {view.plan.sceneMix.achievedMotion}/
              {view.plan.sceneMix.requestedMotion} requested rows.
            </p>
          )}
          {(view.plan?.warnings || []).map((warning, index) => (
            <p role="status" key={index}>
              {warning}
            </p>
          ))}
          {Boolean(view.plan?.problems?.length) && (
            <ul
              id="comparison-corrections"
              aria-label="Comparison corrections"
              className="list-disc pl-5"
              aria-live="polite"
            >
              {view.plan!.problems!.map((problem, index) => (
                <li key={index}>
                  {problem.rowId ? `Row ${problem.rowId}: ` : ""}
                  {problem.sourceId ? `Column ${problem.sourceId}: ` : ""}
                  {problem.message}
                </li>
              ))}
            </ul>
          )}

          <div className="grid min-w-0 gap-3">
            {draft.rows.map((row) => (
              <section
                key={row.id}
                className="grid min-w-0 gap-2 rounded border border-border p-3"
                aria-label={`Comparison row ${row.id}`}
                aria-describedby={
                  view.plan?.problems?.some((problem) => problem.rowId === row.id)
                    ? "comparison-corrections"
                    : undefined
                }
              >
                <div className="flex flex-wrap items-center gap-3">
                  <h3>
                    Row {row.id}
                    {row.scene ? ` · ${row.scene}` : ""}
                  </h3>
                  <label className="flex items-center gap-2">
                    <Checkbox
                      checked={row.excluded}
                      onCheckedChange={(excluded) =>
                        change({
                          ...draft,
                          rows: draft.rows.map((candidate) =>
                            candidate.id === row.id ? { ...candidate, excluded } : candidate,
                          ),
                        })
                      }
                    />
                    Exclude row {row.id}
                  </label>
                  {!draft.imported && (
                    <Button
                      onClick={() =>
                        void submit("capture", {
                          ...draft,
                          rows: draft.rows.map((candidate) =>
                            candidate.id === row.id
                              ? {
                                  ...candidate,
                                  cells: candidate.cells.map((cell) => ({
                                    ...cell,
                                    artifactId: "",
                                    contentSha256: "",
                                  })),
                                }
                              : candidate,
                          ),
                        })
                      }
                    >
                      Recapture row {row.id}
                    </Button>
                  )}
                </div>
                <div className="grid min-w-0 gap-3 lg:grid-cols-3">
                  {row.cells
                    .map((cell, index) => ({ cell, index }))
                    .sort(
                      (left, right) =>
                        (sourceOrder.get(left.cell.sourceId) ?? draft.sources.length) -
                        (sourceOrder.get(right.cell.sourceId) ?? draft.sources.length),
                    )
                    .map(({ cell, index }) => {
                      const source = draft.sources.find(
                        (candidate) => candidate.id === cell.sourceId,
                      );
                      const image = view.artifacts?.artifacts.find(
                        (artifact) => artifact.id === cell.artifactId,
                      );
                      const label = `${source?.label || cell.sourceId || "Unmapped source"}, row ${row.id}, image ${index + 1}`;
                      return (
                        <div
                          className="grid min-w-0 content-start gap-2"
                          key={draft.imported && cell.artifactId ? cell.artifactId : cell.sourceId}
                        >
                          <h4>{source?.label || "Unmapped column"}</h4>
                          {image?.url ? (
                            <button type="button" onClick={() => showImage(image.url!, label)}>
                              <img className="max-h-48 max-w-full" src={image.url} alt={label} />
                            </button>
                          ) : (
                            <p>Image missing or awaiting capture.</p>
                          )}
                          {draft.imported ? (
                            <p>Externally supplied image · timing, type and processing unknown.</p>
                          ) : (
                            <>
                              <p>
                                Frame {cell.frameNumber ?? "unknown"} ·{" "}
                                {cell.pictureType || "unknown type"} ·{" "}
                                {cell.timestampSeconds?.toFixed(3) ?? "unknown"}s
                              </p>
                              <p>
                                {cell.confidence == null
                                  ? "Manual or unresolved correspondence"
                                  : `Visual similarity score: ${cell.confidence.toFixed(3)}`}
                              </p>
                              <label className="grid gap-1">
                                <span>Frame time for {label}</span>
                                <input
                                  className={inputClass}
                                  type="number"
                                  min="0"
                                  step="any"
                                  value={cell.timestampSeconds ?? ""}
                                  onChange={(event) =>
                                    updateCell(row.id, index, {
                                      timestampSeconds: Number(event.target.value) || 0,
                                      frameNumber: undefined,
                                      pictureType: "",
                                      confidence: undefined,
                                      artifactId: "",
                                      contentSha256: "",
                                      observedPts: undefined,
                                      timeBaseNumerator: 0,
                                      timeBaseDenominator: 0,
                                    })
                                  }
                                />
                              </label>
                              <Button
                                disabled={dirty || cell.timestampSeconds == null}
                                onClick={() =>
                                  void previewSource(cell.sourceId, cell.timestampSeconds!)
                                }
                              >
                                Preview {label}
                              </Button>
                            </>
                          )}
                          {cell.warning && <p role="status">{cell.warning}</p>}
                          {image?.processing && (
                            <p role="status">
                              {image.processing.warning ||
                                `Renderer: ${image.processing.renderer}; tonemapping applied: ${image.processing.tonemapped ? "yes" : "no"}.`}
                            </p>
                          )}
                          {draft.imported && (
                            <>
                              <Button
                                onClick={() =>
                                  change({
                                    ...draft,
                                    rows: draft.rows.map((candidate) =>
                                      candidate.id === row.id
                                        ? {
                                            ...candidate,
                                            cells: candidate.cells.filter(
                                              (_, cellIndex) => cellIndex !== index,
                                            ),
                                          }
                                        : candidate,
                                    ),
                                  })
                                }
                              >
                                Remove {label} image
                              </Button>
                              <label className="grid gap-1">
                                <span>Source column for {label}</span>
                                <Select
                                  value={cell.sourceId}
                                  onChange={(event) =>
                                    updateCell(row.id, index, { sourceId: event.target.value })
                                  }
                                >
                                  <option value="">Choose a column</option>
                                  {draft.sources.map((column) => (
                                    <option key={column.id} value={column.id}>
                                      {column.label || column.id}
                                    </option>
                                  ))}
                                </Select>
                              </label>
                              <label className="grid gap-1">
                                <span>Row for {label}</span>
                                <input
                                  className={inputClass}
                                  defaultValue={row.id}
                                  onBlur={(event) => moveCell(row.id, index, event.target.value)}
                                />
                              </label>
                              <label className="grid gap-1">
                                <span>Replace {label}</span>
                                <input
                                  type="file"
                                  accept="image/png,image/jpeg,image/webp"
                                  onChange={(event) => {
                                    const file = event.target.files?.[0];
                                    if (file)
                                      void facet.importImages([file], draft, {
                                        rowID: row.id,
                                        sourceID: cell.sourceId,
                                        artifactID: cell.artifactId,
                                      });
                                  }}
                                />
                              </label>
                            </>
                          )}
                        </div>
                      );
                    })}
                  {draft.sources
                    .filter((source) => !row.cells.some((cell) => cell.sourceId === source.id))
                    .map((source) => (
                      <div className="grid gap-2" key={source.id}>
                        <p>Missing {source.label} image</p>
                        {draft.imported && (
                          <label className="grid gap-1">
                            <span>
                              Supply {source.label}, row {row.id}
                            </span>
                            <input
                              type="file"
                              accept="image/png,image/jpeg,image/webp"
                              onChange={(event) => {
                                const file = event.target.files?.[0];
                                if (file)
                                  void facet.importImages([file], draft, {
                                    rowID: row.id,
                                    sourceID: source.id,
                                  });
                              }}
                            />
                          </label>
                        )}
                      </div>
                    ))}
                </div>
              </section>
            ))}
          </div>
          <div className="grid gap-2 border-t border-border pt-3">
            <p role="status">
              {approved && !dirty
                ? "These captured comparison images are approved."
                : "Comparison images have not been approved for upload."}
            </p>
            <p className="text-muted-foreground">
              Inspect all retained images at full size before approval. Replacements, corrections
              and changed settings require another review. Normal tracker screenshot requirements
              still apply.
            </p>
            <Button
              variant="primary"
              disabled={
                dirty || !view.plan?.fingerprint || Boolean(view.plan.problems?.length) || approved
              }
              onClick={() =>
                void facet.edit({
                  action: "approve",
                  plan: view.plan!,
                  expectedFingerprint: view.plan!.fingerprint,
                })
              }
            >
              Approve reviewed comparison images
            </Button>
          </div>
        </fieldset>
      )}
      <HostBrowserDialog
        mode={browseSource ? "file" : null}
        initialPath={paths[browseSource] || ""}
        onClose={() => setBrowseSource("")}
        onSelect={(path) => setPaths((previous) => ({ ...previous, [browseSource]: path }))}
      />
    </section>
  );
}

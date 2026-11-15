// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import type {
  ComparisonCell,
  ComparisonPlan,
  ComparisonSource,
  ComparisonPreferences,
  ComparisonSourceDiscovery,
  ComparisonProgrammeSelection,
  ComparisonFrameSelection,
} from "../../api/generated/release-workflow";
import { Button } from "../../components/ui/button";
import { Checkbox } from "../../components/ui/checkbox";
import { Select } from "../../components/ui/select";
import { FieldHelp } from "../../components/ui/field-help";
import { pageStyle } from "../../components/ui/pageStyle";
import { HostBrowserDialog } from "../../features/hostBrowser/HostBrowserDialog";
import { ProgrammeCandidates } from "../../features/programmes/ProgrammeCandidates";
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
        },
      ],
  rows: [],
  mainSourceId: imported ? "" : "main",
  imported,
  reuseMainScreenshots: true,
  ...(!imported && {
    sizeReferenceSourceId: "main",
    resizeLargerOnly: true,
    resizeSmaller: true,
  }),
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
  const [browseMode, setBrowseMode] = useState<"file" | "folder">("file");
  const [sourceModes, setSourceModes] = useState<Record<string, "file" | "folder">>({});
  const [discoveries, setDiscoveries] = useState<Record<string, ComparisonSourceDiscovery>>({});
  const [programmes, setProgrammes] = useState<Record<string, ComparisonProgrammeSelection>>({});
  const [frameSelections, setFrameSelections] = useState<Record<string, ComparisonFrameSelection>>(
    {},
  );
  const [files, setFiles] = useState<readonly File[]>([]);
  const positionFor = (sourceID: string) =>
    positions[sourceID] ?? view.previewPositions[sourceID] ?? 0;
  const blocked = view.busy || Boolean(view.blockedReason);
  const pendingFrames = Object.keys(frameSelections).length > 0;
  const dirty =
    JSON.stringify(editableChoices(draft)) !==
      JSON.stringify(editableChoices(view.plan || emptyPlan(false))) ||
    Object.values(paths).some(Boolean) ||
    Object.keys(programmes).length > 0 ||
    pendingFrames;
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
  const clearFrameSelections = (sourceID: string) =>
    setFrameSelections((previous) =>
      Object.fromEntries(
        Object.entries(previous).filter(([, selection]) => selection.sourceId !== sourceID),
      ),
    );
  const sourceChange = (id: string, values: Partial<ComparisonSource>) =>
    change({
      ...draft,
      sources: draft.sources.map((source) =>
        source.id === id ? { ...source, ...values } : source,
      ),
    });
  const updateCell = (rowID: string, index: number, values: Partial<ComparisonCell>) => {
    const cell = draft.rows.find((row) => row.id === rowID)?.cells[index];
    if (cell && ("timestampSeconds" in values || "sourceId" in values))
      setFrameSelections((previous) => {
        const next = { ...previous };
        delete next[`${rowID}/${cell.sourceId}`];
        return next;
      });
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
  };
  const submit = async (action: "configure" | "match" | "capture", plan = draft) => {
    if (action !== "configure" && pendingFrames) return false;
    const applied = await facet.edit({
      action,
      plan: {
        ...plan,
        sources: plan.sources.map((source) =>
          source.imported || plan.imported ? source : { ...source, overlay: true },
        ),
      },
      videoSources: [...new Set([...Object.keys(paths), ...Object.keys(programmes)])]
        .filter((id) => paths[id]?.trim() || programmes[id])
        .map((id) => ({
          id,
          path: paths[id] || "",
          ...(programmes[id] ? { programme: programmes[id] } : {}),
        })),
      ...(action === "configure" ? { frameSelections: Object.values(frameSelections) } : {}),
    });
    if (applied) {
      setPaths({});
      setSourceModes({});
      setProgrammes({});
      setFrameSelections({});
    }
    return applied;
  };
  const changePath = (id: string, path: string) => {
    clearFrameSelections(id);
    setPaths((previous) => ({ ...previous, [id]: path }));
    setDiscoveries((previous) => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
    setProgrammes((previous) => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
  };
  const discover = async (id: string) => {
    const discovery = await facet.discover(id, paths[id] || "");
    if (discovery)
      setDiscoveries((previous) => ({
        ...previous,
        [id]: { ...discovery, candidates: discovery.candidates ?? [] },
      }));
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
          ...(!draft.imported ? { allowedFrameTypes: ["B"], preferBFrames: true } : {}),
        },
      ],
    });
  };
  const removeSource = (id: string) => {
    setSourceModes((previous) => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
    setDiscoveries((previous) => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
    setProgrammes((previous) => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
    clearFrameSelections(id);
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
      <div className="flex items-center gap-1">
        <h2>Comparisons</h2>
        <FieldHelp label="Comparisons">
          Create matched images from videos or import prepared images. Every retained row needs
          review before any comparison image is uploaded. Captions include the observed picture
          type, position, column label and processing. Unverified frame numbers remain unknown.
        </FieldHelp>
      </div>
      {view.error && <p role="alert">{view.error}</p>}
      {view.blockedReason && <p role="status">{view.blockedReason}</p>}
      {!enabled ? (
        <div className="flex flex-wrap gap-2">
          {view.restorePending && (
            <Button
              disabled={blocked}
              onClick={() => void facet.edit({ action: "restore", plan: emptyPlan(true) })}
            >
              Restore retained comparisons
            </Button>
          )}
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
        <fieldset disabled={blocked} className="grid min-w-0 grid-cols-1 gap-3">
          <legend className="sr-only">Comparison setup and review</legend>
          {draft.imported && (
            <div className="flex flex-wrap items-center gap-3">
              <FieldHelp label="Imported comparisons">
                Imported pixels are retained unchanged. Their video timing, picture type, matching
                confidence and processing provenance are unknown. Names such as 01-02-encode.png
                identify row 1, column 2. Other names can be mapped after import; incomplete rows
                must be corrected or excluded.
              </FieldHelp>
              <label className="grid gap-1">
                <span>Prepared comparison images</span>
                <input
                  type="file"
                  accept="image/png,image/jpeg,image/webp"
                  multiple
                  onChange={(event) => setFiles(Array.from(event.target.files || []))}
                />
              </label>
              <Button
                disabled={!files.length}
                onClick={() => void facet.importImages(files, draft)}
              >
                Import images
              </Button>
            </div>
          )}

          <div className="flex flex-wrap items-end gap-x-5 gap-y-3 rounded-lg border border-border p-3 text-sm">
            <label className="grid gap-1">
              <span>Main-input column</span>
              <Select
                value={draft.mainSourceId}
                disabled={!draft.imported}
                aria-describedby={
                  view.plan?.problems?.length ? "comparison-corrections" : undefined
                }
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
            <label className="flex min-h-9 items-center gap-2">
              <Checkbox
                checked={draft.reuseMainScreenshots}
                onCheckedChange={(checked) => change({ ...draft, reuseMainScreenshots: checked })}
              />
              Use the main column as normal screenshots
            </label>
            {!draft.imported && (
              <>
                <div className="grid w-64 max-w-full gap-1">
                  <div className="flex items-center gap-1">
                    <label htmlFor="comparison-frame-size">Frame size</label>
                    <FieldHelp label="Comparison frame size" id="comparison-size-help">
                      {draft.resizeLargerOnly
                        ? `Match width for wide references or height for references narrower than 16:9, preserving aspect ratio. ${draft.resizeSmaller ? "Lower resolutions are resized too." : "Lower resolutions keep their size."} No padding or stretching.`
                        : "Fit sources to the selected display size, centering with padding when aspect ratios differ. No cropping or stretching."}{" "}
                      Apply setup before previewing.
                    </FieldHelp>
                  </div>
                  <Select
                    id="comparison-frame-size"
                    aria-label="Comparison frame size"
                    aria-describedby="comparison-size-help"
                    value={draft.sizeReferenceSourceId || ""}
                    onChange={(event) =>
                      change({ ...draft, sizeReferenceSourceId: event.target.value })
                    }
                  >
                    <option value="">Original source sizes</option>
                    {draft.sources
                      .filter((source) => !source.imported)
                      .map((source) => (
                        <option key={source.id} value={source.id}>
                          Match {source.label || source.id}
                          {source.width && source.height
                            ? ` · ${source.width} × ${source.height}`
                            : ""}
                        </option>
                      ))}
                  </Select>
                </div>
                {draft.sizeReferenceSourceId && (
                  <div className="grid min-h-9 content-center gap-1">
                    <label className="flex items-center gap-2">
                      <Checkbox
                        checked={draft.resizeLargerOnly || false}
                        onCheckedChange={(checked) =>
                          change({ ...draft, resizeLargerOnly: checked })
                        }
                      />
                      Resize resolutions proportionally, without padding
                    </label>
                    {draft.resizeLargerOnly && (
                      <label className="flex items-center gap-2">
                        <Checkbox
                          checked={draft.resizeSmaller || false}
                          onCheckedChange={(checked) =>
                            change({ ...draft, resizeSmaller: checked })
                          }
                        />
                        Also resize lower resolutions
                      </label>
                    )}
                  </div>
                )}
                <div className="flex flex-wrap items-end gap-2">
                  {(
                    [
                      ["count", "Rows", "Comparison rows", 1],
                      ["motionCount", "Motion", "Fast-motion scenes (rows)", 0],
                      ["darkCount", "Dark", "Dark scenes (rows)", 0],
                    ] as const
                  ).map(([key, text, label, min]) => (
                    <label className="grid w-16 gap-1" key={key}>
                      <span>{text}</span>
                      <input
                        className={inputClass}
                        type="number"
                        min={min}
                        step="1"
                        aria-label={label}
                        max={key === "count" ? undefined : preferences.count}
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
                  <div className="mb-1">
                    <FieldHelp label="Scene targets" id="comparison-scene-help">
                      Scene targets are whole row counts from zero to the total rows. A row can
                      count toward both dark and motion targets. Unavailable scenes are reported as
                      unmet. Leave both targets at zero to allow bounded automatic alignment.
                    </FieldHelp>
                  </div>
                </div>
              </>
            )}
          </div>
          {!validScenes && (
            <p role="alert">
              Scene targets must be whole counts between zero and the number of rows.
            </p>
          )}
          <div className="grid items-start gap-3 [grid-template-columns:repeat(auto-fill,minmax(min(100%,22rem),1fr))]">
            {draft.sources.map((source, index) => {
              const preview = view.previews[source.id];
              const capabilities =
                source.capabilities ??
                (source.id === draft.mainSourceId ? view.mainSourceCapabilities : undefined);
              const mode =
                sourceModes[source.id] ??
                (capabilities?.programmeSelectionRequired || source.programme ? "folder" : "file");
              const toneMappingAvailable =
                capabilities?.toneMappingAvailable &&
                !paths[source.id]?.trim() &&
                !sourceModes[source.id];
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
                  className="grid min-w-0 content-start gap-2 rounded-lg border border-border p-3"
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
                  <legend className="px-1 text-sm font-semibold">
                    {source.label || source.id} · column {index + 1}
                  </legend>
                  <div className="flex min-w-0 items-center gap-1">
                    <label className="min-w-0 flex-1">
                      <span className="sr-only">Column label</span>
                      <input
                        className={`${inputClass} w-full`}
                        value={source.label}
                        onChange={(event) => sourceChange(source.id, { label: event.target.value })}
                      />
                    </label>
                    <Button
                      className="px-2"
                      disabled={index === 0}
                      aria-label={`Move ${source.label || source.id} left`}
                      onClick={() => moveSource(index, -1)}
                    >
                      ←
                    </Button>
                    <Button
                      className="px-2"
                      disabled={index === draft.sources.length - 1}
                      aria-label={`Move ${source.label || source.id} right`}
                      onClick={() => moveSource(index, 1)}
                    >
                      →
                    </Button>
                    <Button
                      className="px-2"
                      disabled={source.id === draft.mainSourceId}
                      aria-label={`Remove ${source.label || source.id} column`}
                      onClick={() => removeSource(source.id)}
                    >
                      ×
                    </Button>
                  </div>
                  {!source.imported && (
                    <>
                      {source.id !== draft.mainSourceId && (
                        <div className="grid min-w-0 grid-cols-[7rem_minmax(0,1fr)_auto] gap-1">
                          <Select
                            aria-label={`Source type for ${source.label}`}
                            value={mode}
                            onChange={(event) => {
                              const nextMode = event.target.value === "folder" ? "folder" : "file";
                              setSourceModes((previous) => ({
                                ...previous,
                                [source.id]: nextMode,
                              }));
                              changePath(source.id, "");
                            }}
                          >
                            <option value="file">Video file</option>
                            <option value="folder">Disc folder</option>
                          </Select>
                          <input
                            className={inputClass}
                            aria-label={`Video path for ${source.label || source.id}`}
                            value={paths[source.id] || ""}
                            placeholder={source.fingerprint ? "Replace source…" : "Server path…"}
                            onChange={(event) => changePath(source.id, event.target.value)}
                          />
                          <Button
                            className="px-2"
                            aria-label={mode === "folder" ? "Browse disc folder" : "Browse video"}
                            onClick={() => {
                              setBrowseMode(mode);
                              setBrowseSource(source.id);
                            }}
                          >
                            Browse
                          </Button>
                        </div>
                      )}
                      {mode === "folder" && (
                        <>
                          <Button
                            className="justify-self-start"
                            disabled={
                              blocked || (source.id !== draft.mainSourceId && !paths[source.id])
                            }
                            onClick={() => void discover(source.id)}
                          >
                            Discover titles and playlists
                          </Button>
                          {discoveries[source.id] && (
                            <fieldset className="grid max-h-48 gap-1 overflow-y-auto text-sm">
                              <legend>Programme · {source.label}</legend>
                              {discoveries[source.id]!.candidates.length ? (
                                <ProgrammeCandidates
                                  name={`comparison-programme-${source.id}`}
                                  disabled={blocked}
                                  selected={
                                    programmes[source.id]
                                      ? [programmes[source.id]!.candidateId]
                                      : []
                                  }
                                  candidates={discoveries[source.id]!.candidates.map(
                                    (candidate) => ({
                                      ...candidate,
                                      description: `${candidate.durationSeconds.toFixed(1)} seconds`,
                                      limitation: candidate.supported
                                        ? undefined
                                        : candidate.limitation || "Unavailable for comparisons",
                                    }),
                                  )}
                                  onChoose={(id) => {
                                    clearFrameSelections(source.id);
                                    const discovery = discoveries[source.id]!;
                                    const candidate = discovery.candidates.find(
                                      (entry) => entry.id === id,
                                    )!;
                                    setProgrammes((previous) => ({
                                      ...previous,
                                      [source.id]: {
                                        kind: candidate.kind,
                                        discId: candidate.discId,
                                        candidateId: candidate.id,
                                        discoveryFingerprint: discovery.fingerprint,
                                        angle: 1,
                                      },
                                    }));
                                  }}
                                />
                              ) : (
                                <p role="status">
                                  No titles or playlists found. Choose an extracted disc folder.
                                </p>
                              )}
                            </fieldset>
                          )}
                          {source.programme && (
                            <p className="text-xs text-muted-foreground">
                              Selected programme: {source.programme.discName} ·{" "}
                              {source.programme.label}
                            </p>
                          )}
                        </>
                      )}
                      <p className="text-xs text-muted-foreground">
                        {source.fingerprint ? (
                          <>
                            {source.width || "?"} × {source.height || "?"} ·{" "}
                            {source.frameRate?.toFixed(3) || "?"} FPS ·{" "}
                            {source.durationSeconds?.toFixed(1) || "?"}s
                          </>
                        ) : (
                          "Apply setup to inspect this source"
                        )}
                      </p>
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                        <fieldset className="flex flex-wrap items-center gap-2 text-sm">
                          <legend className="sr-only">Allowed picture types</legend>
                          <label className="flex items-center gap-1">
                            <Checkbox
                              aria-label="Default B-frame preference"
                              checked={Boolean(source.preferBFrames)}
                              onCheckedChange={(preferBFrames) =>
                                sourceChange(source.id, {
                                  preferBFrames,
                                  ...(preferBFrames ? { allowedFrameTypes: ["B"] } : {}),
                                })
                              }
                            />
                            Auto B
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
                          <FieldHelp label={`Picture types · ${source.label}`}>
                            Unchecked means any picture type. Auto B prefers B frames; the main
                            column can fall back to P, then I. Other automatic matches can fall back
                            only to P for a non-I main frame. Explicit choices and manual
                            correspondence are retained.
                          </FieldHelp>
                        </fieldset>
                        {toneMappingAvailable && (
                          <div className="flex items-center gap-1">
                            <label className="flex min-w-0 items-center gap-1 text-sm">
                              <span>Tone map</span>
                              <Select
                                className="max-w-40"
                                aria-label={`Tone mapping · ${source.label}`}
                                aria-describedby={`comparison-tonemap-${index}`}
                                value={
                                  source.toneMap == null ? "default" : source.toneMap ? "on" : "off"
                                }
                                onChange={(event) =>
                                  sourceChange(source.id, {
                                    toneMap:
                                      event.target.value === "default"
                                        ? undefined
                                        : event.target.value === "on",
                                  })
                                }
                              >
                                <option value="default">Screenshot setting</option>
                                <option value="on">Tone mapping on</option>
                                <option value="off">Tone mapping off</option>
                              </Select>
                            </label>
                            <FieldHelp
                              label={`Tone mapping · ${source.label}`}
                              id={`comparison-tonemap-${index}`}
                            >
                              Inherit the normal screenshot tone-mapping setting, or override it for
                              this HDR source. Uses the configured renderer. Inspect any renderer
                              fallback warning before approval.
                            </FieldHelp>
                          </div>
                        )}
                      </div>
                      <details className="min-w-0 border-t border-border pt-2">
                        <summary className="cursor-pointer rounded text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
                          Preview &amp; alignment
                          <span
                            role="status"
                            className="ml-2 text-xs font-normal text-muted-foreground"
                            id={`comparison-position-${index}`}
                            aria-live="off"
                            aria-label={`Preview seconds · ${source.label}`}
                          >
                            {position.toFixed(3)}s
                          </span>
                        </summary>
                        <div className="mt-2 grid min-w-0 gap-2">
                          <div className="flex items-center gap-1">
                            <label className="flex min-w-0 flex-1 items-center gap-2 text-sm">
                              <span>Frame</span>
                              <input
                                className={`${inputClass} w-full`}
                                type="number"
                                min="0"
                                step="1"
                                aria-label={`Approximate frame seek · ${source.label}`}
                                aria-describedby={`comparison-seek-help-${index}`}
                                disabled={!source.frameRate}
                                value={
                                  source.frameRate ? Math.round(position * source.frameRate) : ""
                                }
                                onChange={(event) =>
                                  setPosition(
                                    (Number(event.target.value) || 0) / (source.frameRate || 1),
                                  )
                                }
                              />
                            </label>
                            <FieldHelp
                              label={`Frame seek · ${source.label}`}
                              id={`comparison-seek-help-${index}`}
                            >
                              Frame seeking estimates from average FPS; the position is measured
                              from this source's start. Previous and next use actual decoded frames.
                              Apply changes before previewing.
                            </FieldHelp>
                          </div>
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
                          <div className="flex flex-wrap gap-1">
                            <Button
                              className="px-2"
                              disabled={dirty || !source.fingerprint}
                              onClick={() => void previewSource(source.id, position, -1)}
                            >
                              Previous frame
                            </Button>
                            <Button
                              className="px-2"
                              disabled={dirty || !source.fingerprint}
                              onClick={() => void previewSource(source.id, position, 1)}
                            >
                              Next frame
                            </Button>
                            <Button
                              className="px-2"
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
                                  showImage(
                                    preview.contentUrl,
                                    `${source.label} comparison preview`,
                                  )
                                }
                              >
                                <img
                                  src={preview.contentUrl}
                                  className="max-h-40 max-w-full"
                                  alt={`${source.label} comparison preview`}
                                />
                              </button>
                              <p className="text-xs">
                                Actual preview: {preview.timestampSeconds.toFixed(3)}s ·{" "}
                                {preview.pictureType || "unknown picture type"}
                              </p>
                              {preview.processing?.warning ? (
                                <p role="status">{preview.processing.warning}</p>
                              ) : (
                                preview.processing && (
                                  <FieldHelp label={`Preview processing · ${source.label}`}>
                                    Renderer: {preview.processing.renderer}; tonemapping applied:{" "}
                                    {preview.processing.tonemapped ? "yes" : "no"}.
                                  </FieldHelp>
                                )
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
                                      mainSeconds:
                                        view.previews[draft.mainSourceId]!.timestampSeconds,
                                      sourceSeconds: preview!.timestampSeconds,
                                    },
                                  ],
                                })
                              }
                            >
                              Use displayed previews as an anchor
                            </Button>
                          )}
                        </div>
                      </details>
                    </>
                  )}
                </fieldset>
              );
            })}
          </div>
          <Button className="justify-self-start" onClick={addSource}>
            {draft.imported ? "Add mapped column" : "Add comparison video"}
          </Button>

          {!draft.imported && (
            <>
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
                  disabled={!validScenes || draft.sources.length < 2 || pendingFrames}
                  onClick={() => void submit("match")}
                >
                  Find matching scenes
                </Button>
                <Button
                  className="max-w-full"
                  disabled={draft.sources.length < 2}
                  onClick={addManualRow}
                >
                  Add manual row from timeline positions
                </Button>
                <Button
                  disabled={!validScenes || !draft.rows.length || pendingFrames}
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
                  setSourceModes({});
                  setProgrammes({});
                  setDiscoveries({});
                  setFrameSelections({});
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
              {pendingFrames
                ? "Apply comparison setup to retain displayed frame choices before matching or capturing."
                : "Comparison choices changed. Apply or capture the changes before reviewing and approving them."}
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
                      disabled={pendingFrames}
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
                <div className="grid min-w-0 gap-3 [grid-template-columns:repeat(auto-fit,minmax(min(100%,18rem),1fr))]">
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
                            <FieldHelp label={`Imported image · ${label}`}>
                              Externally supplied image · timing, type and processing unknown.
                            </FieldHelp>
                          ) : (
                            <>
                              <p className="text-xs">
                                Frame {cell.frameNumber ?? "unknown"} ·{" "}
                                {cell.pictureType || "unknown type"} ·{" "}
                                {cell.timestampSeconds?.toFixed(3) ?? "unknown"}s
                              </p>
                              <FieldHelp label={`Correspondence · ${label}`}>
                                {cell.confidence == null
                                  ? "Manual or unresolved correspondence"
                                  : `Visual similarity score: ${cell.confidence.toFixed(3)}`}
                              </FieldHelp>
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
                                      observedIdentity: undefined,
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
                              <Button
                                disabled={
                                  !view.previews[cell.sourceId]?.observedIdentity ||
                                  Boolean(paths[cell.sourceId]?.trim()) ||
                                  Boolean(programmes[cell.sourceId])
                                }
                                onClick={() => {
                                  const preview = view.previews[cell.sourceId]!;
                                  updateCell(row.id, index, {
                                    timestampSeconds: preview.timestampSeconds,
                                    artifactId: "",
                                    contentSha256: "",
                                    confidence: undefined,
                                    observedIdentity: undefined,
                                  });
                                  setFrameSelections((previous) => ({
                                    ...previous,
                                    [`${row.id}/${cell.sourceId}`]: {
                                      rowId: row.id,
                                      sourceId: cell.sourceId,
                                      previewId: preview.id,
                                    },
                                  }));
                                }}
                              >
                                Use displayed frame for {label}
                              </Button>
                            </>
                          )}
                          {cell.warning && <p role="status">{cell.warning}</p>}
                          {image?.processing?.warning ? (
                            <p role="status">{image.processing.warning}</p>
                          ) : (
                            image?.processing && (
                              <FieldHelp label={`Image processing · ${label}`}>
                                Renderer: {image.processing.renderer}; tonemapping applied:{" "}
                                {image.processing.tonemapped ? "yes" : "no"}.
                              </FieldHelp>
                            )
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
          <div className="flex flex-wrap items-center gap-3 border-t border-border pt-3 [&>button]:max-w-full">
            <p role="status">
              {approved && !dirty
                ? "These captured comparison images are approved."
                : "Comparison images have not been approved for upload."}
            </p>
            <FieldHelp label="Comparison image review">
              Inspect all retained images at full size before approval. Replacements, corrections
              and changed settings require another review. Normal tracker screenshot requirements
              still apply.
            </FieldHelp>
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
        mode={browseSource ? browseMode : null}
        initialPath={paths[browseSource] || ""}
        onClose={() => setBrowseSource("")}
        onSelect={(path, isDir) => {
          setSourceModes((previous) => ({
            ...previous,
            [browseSource]: isDir ? "folder" : "file",
          }));
          changePath(browseSource, path);
        }}
      />
    </section>
  );
}

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useId, useState } from "react";
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
import { HostBrowserDialog } from "../../features/hostBrowser/HostBrowserDialog";
import { ProgrammeCandidates } from "../../features/programmes/ProgrammeCandidates";
import type { ComparisonsFacet } from "../../releaseSession/types";

type Props = Readonly<{
  facet: ComparisonsFacet;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

const inputClass = "min-w-0 rounded border border-input bg-card p-2 text-card-foreground";
const imageButtonClass =
  "rounded-none border-0 bg-transparent p-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";
const settingsGroupClass = "min-w-0 border-t border-border px-3";
const summaryClass =
  "cursor-pointer rounded font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";
const settingsSummaryClass = `${summaryClass} list-none py-2.5 [&::-webkit-details-marker]:hidden`;

function SettingsSummary({ label, value }: Readonly<{ label: string; value?: string }>) {
  return (
    <summary className={settingsSummaryClass}>
      <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 text-sm">
        <span
          aria-hidden="true"
          className="inline-block shrink-0 text-muted-foreground group-open/settings:rotate-90"
        >
          ›
        </span>
        <span className="font-semibold">{label}</span>
        {value && (
          <span className="ml-auto text-xs font-normal text-muted-foreground">{value}</span>
        )}
      </span>
    </summary>
  );
}

const sizingOptions = [
  { id: "fit", label: "Fit main size with black padding", summary: "Fit with padding" },
  { id: "resize", label: "Resize to fit without padding", summary: "Resize to fit" },
  { id: "crop", label: "Resize and center-crop to fill main size", summary: "Center crop" },
  { id: "source", label: "Keep each source's display size", summary: "Source size" },
] as const;
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
    resizeLargerOnly: false,
    resizeSmaller: false,
  }),
  preferences: {
    scanMode: "fast",
    flatCount: 0,
    count: 3,
    motionPercent: 0,
    darkPercent: 0,
    motionCount: 0,
    darkCount: 0,
  },
  fingerprint: "",
});
const editableChoices = (plan: ComparisonPlan) => ({
  sizeReferenceSourceId: undefined,
  resizeLargerOnly: false,
  resizeSmaller: false,
  ...plan,
  fingerprint: "",
  approvedFingerprint: "",
  problems: undefined,
  warnings: undefined,
});

// Old plans retain their percentage meaning until edited; an edit fixes both
// targets as the row counts shown here before changing the total row count.
const scenePreferences = (preferences: ComparisonPreferences) => ({
  flatCount: 0,
  scanMode: "fast",
  ...preferences,
  motionCount:
    preferences.motionCount ?? Math.ceil((preferences.count * preferences.motionPercent) / 100),
  darkCount:
    preferences.darkCount ?? Math.ceil((preferences.count * preferences.darkPercent) / 100),
  motionPercent: 0,
  darkPercent: 0,
});

/** Keeps review navigation while a new comparison revision replaces the editable draft. */
export default function ComparisonPanel(props: Props) {
  const [settingsOpen, setSettingsOpen] = useState(true);
  const [selectedRowID, setSelectedRowID] = useState("");

  return (
    <ComparisonWorkspace
      key={props.facet.view.plan?.fingerprint || "none"}
      {...props}
      settingsOpen={settingsOpen}
      onToggleSettings={() => setSettingsOpen((open) => !open)}
      selectedRowID={selectedRowID}
      onSelectRow={setSelectedRowID}
    />
  );
}

function ComparisonWorkspace({
  facet,
  setLightboxImage,
  setLightboxAlt,
  settingsOpen,
  onToggleSettings,
  selectedRowID,
  onSelectRow,
}: Props &
  Readonly<{
    settingsOpen: boolean;
    onToggleSettings: () => void;
    selectedRowID: string;
    onSelectRow: (id: string) => void;
  }>) {
  const { view } = facet;
  const settingsID = useId();
  const reviewID = useId();
  const [enabled, setEnabled] = useState(Boolean(view.plan));
  const [draft, setDraft] = useState<ComparisonPlan>(
    view.plan && !view.plan.imported
      ? {
          ...view.plan,
          sizeReferenceSourceId: view.plan.mainSourceId,
          resizeLargerOnly: false,
          resizeSmaller: false,
        }
      : view.plan || emptyPlan(false),
  );
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
      preferences.count <= 16 &&
      [preferences.motionCount, preferences.darkCount, preferences.flatCount].every(
        (count) => Number.isInteger(count) && count >= 0 && count <= preferences.count,
      ));
  const sourceOrder = new Map(draft.sources.map((source, index) => [source.id, index]));
  const labelProblems = new Map<string, string>();
  for (const source of draft.imported ? [] : draft.sources) {
    if (!source.label.trim())
      labelProblems.set(source.id, "Choose a label for this source column.");
    else if (source.label.length > 80 || /[^A-Za-z0-9 ._-]/u.test(source.label))
      labelProblems.set(
        source.id,
        "Use up to 80 ASCII letters, digits, spaces, periods, underscores or hyphens for the column label.",
      );
  }
  const validSetup = validScenes && labelProblems.size === 0;
  const invalidColumns = Array.from(labelProblems.keys(), (id) => sourceOrder.get(id)! + 1);
  const selectedRowIndex = Math.max(
    0,
    draft.rows.findIndex((row) => row.id === selectedRowID),
  );
  const selectedRow = draft.rows[selectedRowIndex];
  const scanLabel = preferences.scanMode === "deep" ? "Deep Scan" : "Quick Scan";
  const sizing =
    sizingOptions.find((option) => option.id === draft.screenshotSizing) ?? sizingOptions[0];
  const rowCount = draft.rows.length;
  const sourceCount = draft.sources.length;
  const anchorCount = draft.anchors?.length ?? 0;
  const approvedCurrent = approved && !dirty;
  const activeRows = draft.rows.filter((row) => !row.excluded);
  const allRowsExcluded = rowCount > 0 && activeRows.length === 0;
  const hasImages = activeRows.some((row) => row.cells.some((cell) => cell.artifactId));
  const problems = (view.plan?.problems ?? []).filter(
    (problem) =>
      (allRowsExcluded ||
        (problem.code !== "no_active_rows" &&
          problem.message !== "Retain at least one complete comparison row.")) &&
      (problem.code !== "source_label" || draft.imported),
  );
  const change = (next: ComparisonPlan) =>
    setDraft({
      ...next,
      ...(!next.imported && {
        sizeReferenceSourceId: next.mainSourceId,
        resizeLargerOnly: false,
        resizeSmaller: false,
      }),
      fingerprint: "",
      approvedFingerprint: "",
    });
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
    if (!validSetup || (action !== "configure" && pendingFrames)) return false;
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
  const newRowID = () => {
    let next = draft.rows.length + 1;
    while (draft.rows.some((row) => row.id === `row-${next}`)) next++;
    return `row-${next}`;
  };
  const addManualRow = () => {
    const rowID = newRowID();
    change({
      ...draft,
      rows: [
        ...draft.rows,
        {
          id: rowID,
          excluded: false,
          cells: draft.sources.map((source) => ({
            sourceId: source.id,
            timestampSeconds: positionFor(source.id),
          })),
        },
      ],
    });
    onSelectRow(rowID);
  };
  const applyPreviewAnchor = (sourceID: string) => {
    const mainID = draft.mainSourceId;
    const main = view.previews[mainID];
    const target = view.previews[sourceID];
    if (!main?.observedIdentity || !target?.observedIdentity) return;
    const existing = draft.rows.find(
      (row) =>
        !row.excluded &&
        row.cells.some(
          (cell) => cell.sourceId === mainID && cell.timestampSeconds === main.timestampSeconds,
        ),
    );
    const rowID = existing?.id ?? newRowID();
    const selected = [
      { sourceId: mainID, preview: main },
      { sourceId: sourceID, preview: target },
    ];
    const cells = draft.sources.map((source) => {
      const chosen = selected.find((item) => item.sourceId === source.id);
      const prior = existing?.cells.find((cell) => cell.sourceId === source.id);
      if (chosen && prior?.timestampSeconds !== chosen.preview.timestampSeconds) {
        return { sourceId: source.id, timestampSeconds: chosen.preview.timestampSeconds };
      }
      return prior ?? { sourceId: source.id };
    });
    const row = { ...existing, id: rowID, excluded: false, cells };
    change({
      ...draft,
      rows: existing
        ? draft.rows.map((item) => (item.id === rowID ? row : item))
        : [...draft.rows, row],
      anchors: [
        ...(draft.anchors ?? []).filter(
          (anchor) => anchor.sourceId !== sourceID || anchor.mainSeconds !== main.timestampSeconds,
        ),
        {
          sourceId: sourceID,
          mainSeconds: main.timestampSeconds,
          sourceSeconds: target.timestampSeconds,
        },
      ],
    });
    setFrameSelections((previous) => {
      const next = { ...previous };
      for (const { sourceId, preview } of selected) {
        next[`${rowID}/${sourceId}`] = { rowId: rowID, sourceId, previewId: preview.id };
      }
      return next;
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
    const displayed = view.previews[sourceID];
    const preview =
      stepDirection && displayed?.timestampSeconds === timestampSeconds
        ? await facet.preview(sourceID, timestampSeconds, stepDirection, displayed.id)
        : await facet.preview(sourceID, timestampSeconds, stepDirection);
    if (preview)
      setPositions((previous) => ({ ...previous, [sourceID]: preview.timestampSeconds }));
  };

  return (
    <section
      className="@container min-w-0 rounded-xl border border-border bg-background text-foreground [overflow-wrap:anywhere]"
      aria-label="Comparisons"
    >
      <header className="flex flex-wrap items-end justify-between gap-2 rounded-t-xl border-b border-border bg-card px-4 py-3">
        <div>
          <div className="flex items-center gap-1 text-xs font-semibold text-muted-foreground">
            Screenshots / Comparisons
            <FieldHelp label="Comparisons">
              Create matched images from videos or import prepared images. Every retained row needs
              review before any comparison image is uploaded. Captions include the observed picture
              type, position, column label and processing. Frame counters in captured images are
              estimates.
            </FieldHelp>
          </div>
          <h1 className="m-0 text-2xl leading-tight">Plan &amp; Capture</h1>
        </div>
        {enabled && (
          <Button
            type="button"
            aria-controls={settingsID}
            aria-expanded={settingsOpen}
            onClick={onToggleSettings}
            className="gap-2 bg-background text-foreground"
          >
            <span
              aria-hidden="true"
              className="relative size-4 rounded-xs border border-current before:absolute before:inset-y-0 before:left-1 before:border-l before:border-current"
            />
            {settingsOpen ? "Hide comparison settings" : "Show comparison settings"}
          </Button>
        )}
      </header>
      {view.error && (
        <p className="px-4 py-2 font-semibold text-destructive-text" role="alert">
          {view.error}
        </p>
      )}
      {view.blockedReason && (
        <p className="px-4 py-2 text-sm" role="status">
          {view.blockedReason}
        </p>
      )}
      {!enabled ? (
        <div className="flex flex-wrap gap-2 px-4 py-3">
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
        <fieldset className="min-w-0">
          <legend className="sr-only">Comparison setup and review</legend>
          <div
            className={`grid min-w-0 ${settingsOpen ? "@min-[46rem]:grid-cols-[minmax(16rem,19rem)_minmax(0,1fr)]" : ""}`}
          >
            <aside
              id={settingsID}
              aria-label="Comparison settings"
              hidden={!settingsOpen}
              className="min-w-0 rounded-bl-xl border-b border-border bg-card text-sm @min-[46rem]:border-r @min-[46rem]:border-b-0"
            >
              <fieldset disabled={blocked} className="min-w-0 pb-2" aria-label="Comparison setup">
                <div className="grid min-w-0 gap-2 px-3 py-3">
                  <h2 className="m-0 text-base font-semibold">Comparison settings</h2>
                  <div className="rounded-md border border-border bg-background px-2.5 py-1.5">
                    <p className="text-xs text-muted-foreground">Image source</p>
                    <p className="font-medium">
                      {draft.imported ? "Import prepared images" : "Capture from videos"}
                    </p>
                  </div>
                  {draft.imported && (
                    <div className="grid min-w-0 gap-2">
                      <FieldHelp label="Imported comparisons">
                        Imported pixels are retained unchanged. Their video timing, picture type,
                        matching confidence and processing provenance are unknown. Names such as
                        01-02-encode.png identify row 1, column 2. Other names can be mapped after
                        import; incomplete rows must be corrected or excluded.
                      </FieldHelp>
                      <label className="grid min-w-0 gap-1">
                        <span>Prepared comparison images</span>
                        <input
                          className="min-w-0"
                          type="file"
                          accept="image/png,image/jpeg,image/webp"
                          multiple
                          onChange={(event) => setFiles(Array.from(event.target.files || []))}
                        />
                      </label>
                      <Button
                        className="justify-self-start"
                        disabled={!files.length}
                        onClick={() => void facet.importImages(files, draft)}
                      >
                        Import images
                      </Button>
                    </div>
                  )}
                </div>

                <details open name={settingsID} className={`group/settings ${settingsGroupClass}`}>
                  <SettingsSummary
                    label="Sources"
                    value={`${sourceCount} ${sourceCount === 1 ? "column" : "columns"}`}
                  />
                  <div className="grid min-w-0 gap-1.5 pb-3">
                    {draft.sources.map((source, index) => {
                      const labelProblem = labelProblems.get(source.id);
                      const labelProblemID = `comparison-column-label-${index}`;
                      const capabilities =
                        source.capabilities ??
                        (source.id === draft.mainSourceId
                          ? view.mainSourceCapabilities
                          : undefined);
                      const mode =
                        sourceModes[source.id] ??
                        (capabilities?.programmeSelectionRequired || source.programme
                          ? "folder"
                          : "file");
                      const toneMappingAvailable =
                        capabilities?.toneMappingAvailable &&
                        !paths[source.id]?.trim() &&
                        !sourceModes[source.id];
                      return (
                        <details
                          open={
                            source.id !== draft.mainSourceId &&
                            !source.fingerprint &&
                            !draft.imported
                          }
                          className="min-w-0 rounded-md border border-border bg-background p-2"
                          key={source.id}
                        >
                          <summary className={summaryClass}>
                            {source.label || source.id} · column {index + 1}
                          </summary>
                          <fieldset
                            className="mt-2 grid min-w-0 content-start gap-2"
                            aria-label={`${source.label || source.id} · column ${index + 1}`}
                            aria-invalid={
                              problems.some(
                                (problem) => problem.sourceId === source.id && !problem.rowId,
                              ) || undefined
                            }
                            aria-describedby={
                              problems.some((problem) => problem.sourceId === source.id)
                                ? "comparison-corrections"
                                : undefined
                            }
                          >
                            <div className="flex min-w-0 items-center gap-1">
                              <label className="min-w-0 flex-1">
                                <span className="sr-only">Column label</span>
                                <input
                                  className={`${inputClass} w-full`}
                                  aria-invalid={Boolean(labelProblem) || undefined}
                                  aria-describedby={labelProblem ? labelProblemID : undefined}
                                  value={source.label}
                                  onChange={(event) =>
                                    sourceChange(source.id, { label: event.target.value })
                                  }
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
                            {labelProblem && (
                              <p
                                className="text-sm font-bold text-destructive-text"
                                id={labelProblemID}
                                role="alert"
                              >
                                {labelProblem}
                              </p>
                            )}
                            {!source.imported && (
                              <>
                                {source.id !== draft.mainSourceId && (
                                  <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-1">
                                    <Select
                                      className="col-span-full"
                                      aria-label={`Source type for ${source.label}`}
                                      value={mode}
                                      onChange={(event) => {
                                        const nextMode =
                                          event.target.value === "folder" ? "folder" : "file";
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
                                      placeholder={
                                        source.fingerprint ? "Replace source…" : "Server path…"
                                      }
                                      onChange={(event) =>
                                        changePath(source.id, event.target.value)
                                      }
                                    />
                                    <Button
                                      className="px-2"
                                      aria-label={
                                        mode === "folder" ? "Browse disc folder" : "Browse video"
                                      }
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
                                        blocked ||
                                        (source.id !== draft.mainSourceId && !paths[source.id])
                                      }
                                      onClick={() => void discover(source.id)}
                                    >
                                      Discover titles and playlists
                                    </Button>
                                    {discoveries[source.id] && (
                                      <fieldset className="min-w-0 text-sm">
                                        <legend>Programme · {source.label}</legend>
                                        {discoveries[source.id]!.candidates.length ? (
                                          <div className="max-h-60 overflow-y-auto">
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
                                                    : candidate.limitation ||
                                                      "Unavailable for comparisons",
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
                                          </div>
                                        ) : (
                                          <p role="status">
                                            No titles or playlists found. Choose an extracted disc
                                            folder.
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
                                          checked={Boolean(
                                            source.allowedFrameTypes?.includes(type),
                                          )}
                                          onCheckedChange={(checked) =>
                                            sourceChange(source.id, {
                                              preferBFrames: false,
                                              allowedFrameTypes: checked
                                                ? [...(source.allowedFrameTypes || []), type]
                                                : source.allowedFrameTypes?.filter(
                                                    (value) => value !== type,
                                                  ),
                                            })
                                          }
                                        />
                                        {type}
                                      </label>
                                    ))}
                                    <FieldHelp label={`Picture types · ${source.label}`}>
                                      Unchecked means any picture type. Auto B prefers B frames; the
                                      main column can fall back to P, then I. Other automatic
                                      matches can fall back only to P for a non-I main frame.
                                      Explicit choices and manual correspondence are retained.
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
                                            source.toneMap == null
                                              ? "default"
                                              : source.toneMap
                                                ? "on"
                                                : "off"
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
                                        Inherit the normal screenshot tone-mapping setting, or
                                        override it for this HDR source. Uses the configured
                                        renderer. Inspect any renderer fallback warning before
                                        approval.
                                      </FieldHelp>
                                    </div>
                                  )}
                                </div>
                              </>
                            )}
                          </fieldset>
                        </details>
                      );
                    })}
                    <Button className="justify-self-start" onClick={addSource}>
                      {draft.imported ? "Add mapped column" : "Add comparison video"}
                    </Button>
                    <div className="grid min-w-0 gap-2">
                      <label className="grid gap-1">
                        <span>Main-input column</span>
                        <Select
                          value={draft.mainSourceId}
                          disabled={!draft.imported}
                          aria-describedby={problems.length ? "comparison-corrections" : undefined}
                          onChange={(event) =>
                            change({ ...draft, mainSourceId: event.target.value })
                          }
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
                          onCheckedChange={(checked) =>
                            change({ ...draft, reuseMainScreenshots: checked })
                          }
                        />
                        Use the main column as normal screenshots
                      </label>
                    </div>
                  </div>
                </details>
                {!draft.imported && (
                  <details name={settingsID} className={`group/settings ${settingsGroupClass}`}>
                    <SettingsSummary
                      label="Scene selection"
                      value={`${scanLabel} · ${preferences.count} rows`}
                    />
                    <div className="grid min-w-0 gap-2 pb-3">
                      <div className="grid min-w-0 gap-1">
                        <div className="flex items-center gap-1">
                          <label htmlFor="comparison-scan-mode">Scene discovery</label>
                          <FieldHelp label="Comparison scan mode" id="comparison-scan-help">
                            Quick Scan checks a limited set of scenes and makes a best-effort
                            selection. Deep Scan takes longer because it scans the entire main input
                            and selects the best-scoring eligible candidates for each requested
                            scene type. Other sources are still quick-matched. If suitable scenes
                            are unavailable, both modes use best effort.
                          </FieldHelp>
                        </div>
                        <Select
                          id="comparison-scan-mode"
                          aria-label="Comparison scan mode"
                          aria-describedby="comparison-scan-help"
                          value={preferences.scanMode || "fast"}
                          onChange={(event) =>
                            change({
                              ...draft,
                              preferences: { ...preferences, scanMode: event.target.value },
                            })
                          }
                        >
                          <option value="fast">Quick Scan</option>
                          <option value="deep">Deep Scan</option>
                        </Select>
                      </div>
                      <div className="grid grid-cols-2 items-end gap-2">
                        {(
                          [
                            ["count", "Rows", "Comparison rows", 1],
                            ["motionCount", "Motion", "Fast-motion scenes (rows)", 0],
                            ["darkCount", "Dark", "Dark scenes (rows)", 0],
                            ["flatCount", "Flat scenes", "Flat scenes (rows)", 0],
                          ] as const
                        ).map(([key, text, label, min]) => (
                          <label className="grid min-w-0 gap-1" key={key}>
                            <span>{text}</span>
                            <input
                              className={`${inputClass} w-full`}
                              type="number"
                              min={min}
                              step="1"
                              aria-label={label}
                              max={key === "count" ? 16 : preferences.count}
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
                            Scene targets are whole row counts from zero to the total rows. A row
                            can count toward all requested categories. Unavailable scenes are
                            reported as unmet, with eligible alternatives selected using best
                            effort.
                          </FieldHelp>
                        </div>
                        <FieldHelp label="Flat scenes" id="comparison-flat-help">
                          Prefer scenes containing smooth or low-texture areas, such as walls or
                          sky. Only part of the frame needs to be smooth. Uses best effort when
                          suitable scenes are unavailable.
                        </FieldHelp>
                      </div>
                      <div className="grid grid-cols-2 items-end gap-2">
                        <label className="grid gap-1">
                          Start (seconds)
                          <input
                            className={`${inputClass} w-full`}
                            type="number"
                            min="0"
                            step="any"
                            aria-label="Comparison selection start"
                            value={preferences.selectionWindow?.start ?? ""}
                            onChange={(event) =>
                              change({
                                ...draft,
                                preferences: {
                                  ...preferences,
                                  selectionWindow: {
                                    ...preferences.selectionWindow,
                                    start:
                                      event.target.value === ""
                                        ? undefined
                                        : Number(event.target.value),
                                  },
                                },
                              })
                            }
                          />
                        </label>
                        <label className="grid gap-1">
                          End (seconds)
                          <input
                            className={`${inputClass} w-full`}
                            type="number"
                            min="0"
                            step="any"
                            aria-label="Comparison selection end"
                            value={preferences.selectionWindow?.end ?? ""}
                            onChange={(event) =>
                              change({
                                ...draft,
                                preferences: {
                                  ...preferences,
                                  selectionWindow: {
                                    ...preferences.selectionWindow,
                                    end:
                                      event.target.value === ""
                                        ? undefined
                                        : Number(event.target.value),
                                  },
                                },
                              })
                            }
                          />
                        </label>
                      </div>
                      {view.plan?.analysis && (
                        <p>
                          Effective selection: {view.plan.analysis.selection.start.toFixed(2)}–
                          {view.plan.analysis.selection.end.toFixed(2)} seconds
                          {view.plan.analysis.excluded
                            ?.map(
                              (interval) =>
                                `; exclude ${interval.start.toFixed(2)}–${interval.end.toFixed(2)}`,
                            )
                            .join("")}
                          . Apply setup after changing the range.
                        </p>
                      )}
                      {view.plan?.sceneMix && (
                        <p className="text-xs text-muted-foreground">
                          Last measured scene mix: dark {view.plan.sceneMix.achievedDark}/
                          {view.plan.sceneMix.requestedDark}; fast motion{" "}
                          {view.plan.sceneMix.achievedMotion}/{view.plan.sceneMix.requestedMotion};
                          flat {view.plan.sceneMix.achievedFlat ?? 0}/
                          {view.plan.sceneMix.requestedFlat ?? 0} requested rows. Counts include
                          verified categories only.
                        </p>
                      )}
                      {view.plan?.analysis && (
                        <p role="status" className="text-xs text-muted-foreground">
                          {view.plan.analysis.scanMode === "deep" ? "Deep Scan" : "Quick Scan"}:{" "}
                          {view.plan.analysis.complete
                            ? "complete main coverage"
                            : "partial main coverage"}
                          ; {view.plan.analysis.coveredSeconds.toFixed(1)} seconds visited. Other
                          sources use bounded matching.
                        </p>
                      )}
                    </div>
                  </details>
                )}
                {!draft.imported && (
                  <details name={settingsID} className={`group/settings ${settingsGroupClass}`}>
                    <SettingsSummary label="Screenshot sizing" value={sizing.summary} />
                    <label className="grid min-w-0 gap-1 pb-3">
                      Output size
                      <Select
                        value={draft.screenshotSizing || "fit"}
                        onChange={(event) =>
                          change({
                            ...draft,
                            screenshotSizing:
                              event.target.value === "fit" ? undefined : event.target.value,
                          })
                        }
                      >
                        {sizingOptions.map((option) => (
                          <option key={option.id} value={option.id}>
                            {option.label}
                          </option>
                        ))}
                      </Select>
                    </label>
                  </details>
                )}

                {!draft.imported && (
                  <details name={settingsID} className={`group/settings ${settingsGroupClass}`}>
                    <SettingsSummary
                      label="Preview & alignment"
                      value={
                        anchorCount
                          ? `${anchorCount} ${anchorCount === 1 ? "anchor" : "anchors"}`
                          : "No anchors"
                      }
                    />
                    <div className="grid min-w-0 gap-2 pb-3">
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
                          <details
                            key={source.id}
                            className="min-w-0 rounded-md border border-border bg-background p-2"
                          >
                            <summary className="cursor-pointer rounded text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
                              <span>{source.label || source.id}</span>
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
                            <div
                              className="mt-2 grid min-w-0 gap-2"
                              role="group"
                              aria-label={`Preview for ${source.label || source.id}`}
                            >
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
                                      source.frameRate
                                        ? Math.round(position * source.frameRate)
                                        : ""
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
                                  from this source's start. Previous and next use actual decoded
                                  frames. Apply changes before previewing.
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
                                    className={`${imageButtonClass} min-w-0 justify-self-start`}
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
                                        Renderer: {preview.processing.renderer}; tonemapping
                                        applied: {preview.processing.tonemapped ? "yes" : "no"}.
                                      </FieldHelp>
                                    )
                                  )}
                                </div>
                              )}
                              {source.id !== draft.mainSourceId && (
                                <Button
                                  disabled={
                                    !preview?.observedIdentity ||
                                    !view.previews[draft.mainSourceId]?.observedIdentity ||
                                    Boolean(
                                      paths[source.id]?.trim() ||
                                      paths[draft.mainSourceId]?.trim() ||
                                      programmes[source.id] ||
                                      programmes[draft.mainSourceId],
                                    )
                                  }
                                  onClick={() => applyPreviewAnchor(source.id)}
                                >
                                  Use displayed previews as an anchor
                                </Button>
                              )}
                            </div>
                          </details>
                        );
                      })}
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
                                anchors: draft.anchors?.filter(
                                  (_, candidate) => candidate !== index,
                                ),
                              })
                            }
                          >
                            Remove anchor
                          </Button>
                        </div>
                      ))}
                    </div>
                  </details>
                )}

                <details name={settingsID} className={`group/settings ${settingsGroupClass}`}>
                  <SettingsSummary label="Comparison actions" />
                  <div className="flex flex-wrap gap-2 pb-3 [&>button]:max-w-full [&>button]:whitespace-normal">
                    {!draft.imported && (
                      <>
                        <Button
                          disabled={!validSetup || draft.sources.length < 2 || pendingFrames}
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
                          disabled={!validSetup || !draft.rows.length || pendingFrames}
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
                          onSelectRow("");
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
                </details>
              </fieldset>
            </aside>
            <div className="grid min-w-0 content-start gap-3 p-3">
              <div
                className={`flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2 text-sm ${dirty || (!approvedCurrent && rowCount > 0) ? "border-status-warning/40 bg-status-warning/10" : "border-border bg-card"}`}
              >
                <div role="status" className="min-w-0 flex-1 basis-56">
                  <p
                    className={`font-semibold ${approvedCurrent ? "text-status-success" : dirty ? "text-status-warning" : ""}`}
                  >
                    {approvedCurrent
                      ? "Comparison images approved"
                      : dirty || !rowCount
                        ? "Comparison setup"
                        : allRowsExcluded
                          ? "All comparison rows are excluded"
                          : "Review comparison rows before upload"}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {!validSetup
                      ? "Correct the highlighted settings before applying setup."
                      : pendingFrames
                        ? "Apply comparison setup to retain displayed frame choices before matching or capturing."
                        : dirty
                          ? "Comparison choices changed. Apply or capture the changes before reviewing and approving them."
                          : approvedCurrent
                            ? "The current images are approved for upload."
                            : !rowCount
                              ? draft.imported
                                ? "Import prepared comparison images to start review."
                                : "Add comparison videos, then find matching scenes or add a manual row."
                              : allRowsExcluded
                                ? "Use Row and image corrections to include a row again."
                                : !hasImages
                                  ? "Capture or supply images for the rows before reviewing them."
                                  : "Open each image at full size and check the row before approving."}
                  </p>
                </div>
                <Button
                  variant={dirty || !view.plan ? "primary" : "secondary"}
                  disabled={blocked || !validSetup}
                  onClick={() => void submit("configure")}
                >
                  Apply comparison setup
                </Button>
              </div>
              <section
                aria-labelledby={reviewID}
                className="grid min-w-0 gap-3 rounded-lg border border-border bg-card p-3"
              >
                <header className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <h2 id={reviewID} className="m-0 text-base font-semibold">
                      Review comparison images
                    </h2>
                    <p className="text-xs text-muted-foreground">
                      {rowCount} {rowCount === 1 ? "row" : "rows"} · {sourceCount}{" "}
                      {sourceCount === 1 ? "source" : "sources"}
                    </p>
                  </div>
                  {rowCount > 1 && (
                    <div className="flex gap-1">
                      <Button
                        type="button"
                        aria-label="Previous comparison row"
                        className="px-3 text-lg leading-none"
                        disabled={selectedRowIndex === 0}
                        onClick={() => onSelectRow(draft.rows[selectedRowIndex - 1].id)}
                      >
                        ‹
                      </Button>
                      <Button
                        type="button"
                        aria-label="Next comparison row"
                        className="px-3 text-lg leading-none"
                        disabled={selectedRowIndex === rowCount - 1}
                        onClick={() => onSelectRow(draft.rows[selectedRowIndex + 1].id)}
                      >
                        ›
                      </Button>
                    </div>
                  )}
                </header>
                {rowCount > 1 && (
                  <div role="group" aria-label="Comparison rows" className="flex flex-wrap gap-1">
                    {draft.rows.map((row, index) => (
                      <Button
                        key={row.id}
                        type="button"
                        aria-label={`Select comparison row ${index + 1}${row.excluded ? " (excluded)" : ""}`}
                        aria-description={
                          row.id !== String(index + 1) ? `Row ID: ${row.id}` : undefined
                        }
                        aria-current={row.id === selectedRow?.id ? "true" : undefined}
                        className="max-w-full shrink whitespace-normal bg-background px-2.5 text-foreground aria-[current=true]:border-primary aria-[current=true]:bg-primary/10 aria-[current=true]:text-primary-text"
                        onClick={() => onSelectRow(row.id)}
                      >
                        <span className={row.excluded ? "line-through" : undefined}>
                          {index + 1}
                        </span>
                      </Button>
                    ))}
                  </div>
                )}
                {!validScenes && (
                  <p className="font-bold text-destructive-text" role="alert">
                    Scene targets must be whole counts between zero and the number of rows.
                  </p>
                )}
                {invalidColumns.length > 0 && (
                  <p className="font-bold text-destructive-text" role="status">
                    Invalid {invalidColumns.length === 1 ? "label in column" : "labels in columns"}{" "}
                    {invalidColumns.join(", ")}.{" "}
                    {settingsOpen ? "Open Sources" : "Show comparison settings and open Sources"} to
                    correct {invalidColumns.length === 1 ? "it" : "them"}.
                  </p>
                )}
                {(view.plan?.warnings || []).map((warning, index) => (
                  <p role="status" key={index}>
                    {warning}
                  </p>
                ))}
                {problems.length > 0 && (
                  <ul
                    id="comparison-corrections"
                    aria-label="Comparison corrections"
                    className="list-disc pl-5 font-bold text-destructive-text"
                    aria-live="polite"
                  >
                    {problems.map((problem, index) => (
                      <li key={index}>
                        {problem.rowId ? `Row ${problem.rowId}. ` : ""}
                        {problem.sourceId && sourceOrder.has(problem.sourceId)
                          ? `Column ${sourceOrder.get(problem.sourceId)! + 1}. `
                          : ""}
                        {problem.message}
                      </li>
                    ))}
                  </ul>
                )}

                {!selectedRow && (
                  <div className="grid min-h-28 place-content-center gap-1 rounded-md border border-dashed border-border bg-background px-3 py-5 text-center text-sm">
                    <p className="font-semibold">No comparison rows yet</p>
                    <p className="text-muted-foreground">
                      {draft.imported
                        ? "Imported rows will appear here."
                        : "Matching scenes and manually added rows will appear here."}
                    </p>
                  </div>
                )}
                <div className="grid min-w-0 gap-3">
                  {draft.rows.map((row) => {
                    const mainTime = row.cells.find(
                      (cell) => cell.sourceId === draft.mainSourceId,
                    )?.timestampSeconds;
                    const cells = row.cells
                      .map((cell, index) => ({ cell, index }))
                      .sort(
                        (left, right) =>
                          (sourceOrder.get(left.cell.sourceId) ?? sourceCount) -
                          (sourceOrder.get(right.cell.sourceId) ?? sourceCount),
                      )
                      .map(({ cell, index }) => {
                        const source = draft.sources.find(
                          (candidate) => candidate.id === cell.sourceId,
                        );
                        const key =
                          draft.imported && cell.artifactId ? cell.artifactId : cell.sourceId;
                        const label = `${source?.label || cell.sourceId || "Unmapped source"}, row ${row.id}, image ${index + 1}`;
                        return { cell, index, source, key, label };
                      });
                    return (
                      <section
                        key={row.id}
                        hidden={row.id !== selectedRow?.id}
                        className="grid min-w-0 gap-2"
                        aria-label={`Comparison row ${row.id}`}
                        aria-describedby={
                          problems.some((problem) => problem.rowId === row.id)
                            ? "comparison-corrections"
                            : undefined
                        }
                      >
                        <div className="flex flex-wrap items-baseline gap-2">
                          <h3 className="text-sm font-semibold">
                            Row {row.id}
                            {row.scene ? ` · ${row.scene}` : ""}
                          </h3>
                          <span className="text-xs text-muted-foreground">
                            {draft.imported
                              ? "Imported images"
                              : mainTime != null
                                ? `${mainTime.toFixed(3)}s`
                                : ""}
                          </span>
                        </div>
                        <div className="grid min-w-0 gap-3 [grid-template-columns:repeat(auto-fit,minmax(min(100%,10rem),1fr))]">
                          {cells.map(({ cell, source, label, key }) => {
                            const image = view.artifacts?.artifacts.find(
                              (artifact) => artifact.id === cell.artifactId,
                            );
                            const timing = [
                              cell.pictureType,
                              cell.timestampSeconds != null
                                ? `${cell.timestampSeconds.toFixed(3)}s`
                                : undefined,
                            ]
                              .filter(Boolean)
                              .join(" · ");
                            return (
                              <figure key={key} className="grid min-w-0 content-start gap-1">
                                {image?.url ? (
                                  <button
                                    type="button"
                                    className={`${imageButtonClass} block w-full`}
                                    onClick={() => showImage(image.url!, label)}
                                  >
                                    <img
                                      className="block h-auto w-full"
                                      loading="lazy"
                                      src={image.url}
                                      alt={label}
                                      width={image.width || undefined}
                                      height={image.height || undefined}
                                    />
                                  </button>
                                ) : (
                                  <p className="grid aspect-video place-items-center rounded-md border border-border bg-muted p-2 text-center text-sm">
                                    Image missing or awaiting capture.
                                  </p>
                                )}
                                <figcaption className="min-w-0">
                                  <h4 className="text-sm font-semibold">
                                    {source?.label || "Unmapped column"}
                                  </h4>
                                  <div className="flex flex-wrap items-center gap-x-1 text-xs text-muted-foreground">
                                    {draft.imported ? (
                                      <>
                                        Imported image
                                        <FieldHelp label={`Imported image · ${label}`}>
                                          Externally supplied image · timing, type and processing
                                          unknown.
                                        </FieldHelp>
                                      </>
                                    ) : (
                                      <>
                                        {timing && <p>{timing}</p>}
                                        <p>
                                          {cell.confidence == null
                                            ? "Manual or unresolved correspondence"
                                            : `Visual similarity score: ${cell.confidence.toFixed(3)}`}
                                        </p>
                                      </>
                                    )}
                                  </div>
                                  {cell.warning && (
                                    <p role="status" className="text-xs text-status-warning">
                                      {cell.warning}
                                    </p>
                                  )}
                                  {image?.processing?.warning ? (
                                    <p role="status" className="text-xs text-status-warning">
                                      {image.processing.warning}
                                    </p>
                                  ) : (
                                    image?.processing && (
                                      <FieldHelp label={`Image processing · ${label}`}>
                                        Renderer: {image.processing.renderer}; tonemapping applied:{" "}
                                        {image.processing.tonemapped ? "yes" : "no"}.
                                      </FieldHelp>
                                    )
                                  )}
                                </figcaption>
                              </figure>
                            );
                          })}
                          {draft.sources
                            .filter(
                              (source) => !row.cells.some((cell) => cell.sourceId === source.id),
                            )
                            .map((source) => (
                              <div className="grid gap-2" key={source.id}>
                                <p>Missing {source.label} image</p>
                                {draft.imported && (
                                  <label className="grid gap-1">
                                    <span>
                                      Supply {source.label}, row {row.id}
                                    </span>
                                    <input
                                      disabled={blocked}
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
                        <details className="min-w-0 border-t border-border pt-2 text-sm">
                          <summary className={summaryClass}>Row and image corrections</summary>
                          <div className="mt-2 grid min-w-0 gap-2">
                            <div className="flex flex-wrap items-center gap-2">
                              <label className="flex items-center gap-2">
                                <Checkbox
                                  disabled={blocked}
                                  checked={row.excluded}
                                  onCheckedChange={(excluded) =>
                                    change({
                                      ...draft,
                                      rows: draft.rows.map((candidate) =>
                                        candidate.id === row.id
                                          ? { ...candidate, excluded }
                                          : candidate,
                                      ),
                                    })
                                  }
                                />
                                Exclude row {row.id}
                              </label>
                              {!draft.imported && (
                                <Button
                                  disabled={blocked || !validSetup || pendingFrames}
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
                            <div className="grid min-w-0 gap-2 [grid-template-columns:repeat(auto-fit,minmax(min(100%,12rem),1fr))]">
                              {cells.map(({ cell, index, source, label, key }) => {
                                const framePreview = view.previews[cell.sourceId];
                                const framePreviewLabel = framePreview
                                  ? `${label} preview at ${framePreview.timestampSeconds.toFixed(3)}s`
                                  : "";
                                return (
                                  <div
                                    key={key}
                                    role="group"
                                    aria-label={`Corrections for ${label}`}
                                    className="min-w-0 rounded-md border border-border bg-background p-2"
                                  >
                                    <p className="mb-1 text-sm font-semibold">
                                      {source?.label || "Unmapped column"}
                                    </p>
                                    {!draft.imported ? (
                                      <details>
                                        <summary className={summaryClass}>Correct frame</summary>
                                        <fieldset
                                          disabled={blocked}
                                          className="mt-2 grid min-w-0 gap-2 [&>button]:whitespace-normal"
                                          aria-label={`Correct frame for ${label}`}
                                        >
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
                                              void previewSource(
                                                cell.sourceId,
                                                cell.timestampSeconds!,
                                              )
                                            }
                                          >
                                            Preview {label}
                                          </Button>
                                          {framePreview && (
                                            <button
                                              type="button"
                                              className={`${imageButtonClass} grid min-w-0 gap-1 text-left text-xs`}
                                              onClick={() =>
                                                showImage(
                                                  framePreview.contentUrl,
                                                  framePreviewLabel,
                                                )
                                              }
                                            >
                                              <img
                                                className="max-h-48 max-w-full"
                                                src={framePreview.contentUrl}
                                                alt={framePreviewLabel}
                                              />
                                              <span>
                                                Source preview at{" "}
                                                {framePreview.timestampSeconds.toFixed(3)}s
                                              </span>
                                            </button>
                                          )}
                                          <Button
                                            disabled={
                                              !framePreview?.observedIdentity ||
                                              Boolean(paths[cell.sourceId]?.trim()) ||
                                              Boolean(programmes[cell.sourceId])
                                            }
                                            onClick={() => {
                                              const preview = framePreview!;
                                              if (
                                                cell.timestampSeconds !== preview.timestampSeconds
                                              ) {
                                                updateCell(row.id, index, {
                                                  timestampSeconds: preview.timestampSeconds,
                                                  artifactId: "",
                                                  contentSha256: "",
                                                  confidence: undefined,
                                                  observedIdentity: undefined,
                                                });
                                              }
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
                                        </fieldset>
                                      </details>
                                    ) : (
                                      <details>
                                        <summary className={summaryClass}>
                                          Map or replace image
                                        </summary>
                                        <fieldset
                                          disabled={blocked}
                                          className="mt-2 grid min-w-0 gap-2 [&>button]:whitespace-normal"
                                          aria-label={`Map or replace ${label}`}
                                        >
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
                                                updateCell(row.id, index, {
                                                  sourceId: event.target.value,
                                                })
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
                                              onBlur={(event) =>
                                                moveCell(row.id, index, event.target.value)
                                              }
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
                                        </fieldset>
                                      </details>
                                    )}
                                  </div>
                                );
                              })}
                            </div>
                          </div>
                        </details>
                      </section>
                    );
                  })}
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-sm [&>button]:max-w-full">
                  <div className="flex min-w-0 flex-1 basis-56 items-center gap-1">
                    <p role="status" className="text-muted-foreground">
                      {approvedCurrent
                        ? "These captured comparison images are approved."
                        : "Inspect each source image at full size before approving."}
                    </p>
                    <FieldHelp label="Comparison image review">
                      Replacements, corrections and changed settings require another review. Normal
                      tracker screenshot requirements still apply.
                    </FieldHelp>
                  </div>
                  <Button
                    variant="primary"
                    disabled={
                      blocked ||
                      dirty ||
                      !view.plan?.fingerprint ||
                      Boolean(view.plan.problems?.length) ||
                      approved
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
              </section>
            </div>
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

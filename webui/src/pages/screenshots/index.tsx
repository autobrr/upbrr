// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../../components/ui/pageStyle";
import { useEffect, useMemo, useRef, useState } from "react";
import { Select } from "../../components/ui/select";
import type { ScreenshotsFacet } from "../../releaseSession/types";
import type { ScreenshotSelection } from "../../types";

type Props = Readonly<{
  facet: ScreenshotsFacet;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

const frameInputClass =
  "h-8 w-full rounded-md border border-input bg-card px-2.5 text-sm text-card-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring";

/** Presents screenshot planning, live previews, retained capture, ordering, and final selection. */
export default function ScreenshotsPage({ facet, setLightboxImage, setLightboxAlt }: Props) {
  const { view } = facet;
  const loadRef = useRef(facet.load);
  const [livePreviewSeconds, setLivePreviewSeconds] = useState(0);
  const [livePreviewDiscID, setLivePreviewDiscID] = useState("");
  const [finalDragIndex, setFinalDragIndex] = useState<number | null>(null);
  loadRef.current = facet.load;

  useEffect(() => {
    if (
      view.status !== "running" &&
      view.status !== "error" &&
      !view.mutationBlockedReason &&
      !view.plan &&
      (view.workflowMode || Boolean(view.staleReason))
    ) {
      void loadRef.current();
    }
  }, [view.mutationBlockedReason, view.plan, view.staleReason, view.status, view.workflowMode]);

  const plan = view.plan;
  const busy = view.status === "running";
  const mutationsBlocked = busy || Boolean(view.mutationBlockedReason);
  const selections = view.selections;
  const workflowImages = useMemo(
    () =>
      (view.artifacts?.artifacts || [])
        .filter((artifact) => artifact.kind === "screenshot")
        .sort((left, right) => (left.order || 0) - (right.order || 0)),
    [view.artifacts],
  );
  const selectedWorkflowImages = useMemo(
    () => workflowImages.filter((artifact) => artifact.selected),
    [workflowImages],
  );
  const retainedImageURLs = new Set(
    (view.artifacts?.artifacts || [])
      .filter((artifact) => artifact.kind === "hosted_image")
      .map((artifact) => artifact.url),
  );
  const savedTrackerImages = (plan?.SavedTrackerImages || []).filter(
    (image) => !retainedImageURLs.has(image.URL),
  );

  const discPlans = useMemo(() => {
    if (plan?.Discs?.length) return plan.Discs;
    if (!plan) return [];
    return [
      {
        DiscID: "",
        DiscName: plan.DiscType ? `${plan.DiscType} disc` : "Source",
        DurationSeconds: plan.DurationSeconds,
        FrameRate: plan.FrameRate,
        SuggestedSelections: plan.SuggestedSelections,
      },
    ];
  }, [plan]);
  useEffect(() => {
    if (!discPlans.length) return;
    if (!discPlans.some((disc) => disc.DiscID === livePreviewDiscID)) {
      setLivePreviewDiscID(discPlans[0].DiscID);
      setLivePreviewSeconds(0);
    }
  }, [discPlans, livePreviewDiscID]);

  const livePreviewDisc =
    discPlans.find((disc) => disc.DiscID === livePreviewDiscID) || discPlans[0];
  const selectionGroups = discPlans.map((disc) => ({
    disc,
    selections: selections
      .map((selection, index) => ({ selection, index }))
      .filter(({ selection }) => (selection.DiscID || "") === disc.DiscID),
  }));
  const workflowImageGroups = discPlans.map((disc) => ({
    disc,
    images: workflowImages.filter((artifact) => (artifact.discId || "") === disc.DiscID),
  }));
  const unassignedImages = workflowImages.filter(
    (artifact) => !discPlans.some((disc) => disc.DiscID === (artifact.discId || "")),
  );
  if (unassignedImages.length) {
    workflowImageGroups.push({
      disc: {
        DiscID: "",
        DiscName: "Saved images",
        DurationSeconds: 0,
        FrameRate: 0,
        SuggestedSelections: [],
      },
      images: unassignedImages,
    });
  }

  const previewDuration = Math.max(livePreviewDisc?.DurationSeconds || 0, 0);
  const previewFrameRate = Math.max(livePreviewDisc?.FrameRate || 0, 0);
  const previewTimingDisabled = previewDuration <= 0 || previewFrameRate <= 0;
  const clampPreviewSeconds = (value: number) => {
    if (!Number.isFinite(value)) return 0;
    return Math.min(Math.max(value, 0), previewDuration);
  };
  const livePreviewFrame =
    previewFrameRate > 0 ? Math.max(0, Math.round(livePreviewSeconds * previewFrameRate)) : 0;

  const runLivePreviewAt = async (value: number) => {
    const next = clampPreviewSeconds(value);
    setLivePreviewSeconds(next);
    await facet.previewFrame(livePreviewDisc?.DiscID || "", next);
  };

  const stepLivePreview = (direction: number) => {
    if (previewTimingDisabled) return;
    void runLivePreviewAt(livePreviewSeconds + direction / previewFrameRate);
  };

  const captureLivePreview = () => {
    const nextIndex =
      Math.max(
        -1,
        ...selections.map((selection) => selection.Index),
        ...workflowImages.map((artifact) => artifact.index ?? -1),
      ) + 1;
    const selection: ScreenshotSelection = {
      ...(livePreviewDisc?.DiscID ? { DiscID: livePreviewDisc.DiscID } : {}),
      Index: nextIndex,
      TimestampSeconds: clampPreviewSeconds(livePreviewSeconds),
      Frame: livePreviewFrame,
      Source: "manual",
    };
    void facet.generate("final", [selection]);
  };

  return (
    <section className="flex flex-col gap-2.5">
      <header className="screens-header">
        <p className={pageStyle.eyebrow}>Screenshots</p>
        <h1>Plan &amp; Capture</h1>
        <p className={pageStyle.subtitle}>
          Review tracker images, adjust frame times, and generate screenshots.
        </p>
      </header>

      {view.artifacts ? (
        <section className={`${pageStyle.panel} grid gap-1`} role="status">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2>Authoritative media set</h2>
            <span className="text-muted-foreground">{view.artifacts.status}</span>
          </div>
          <p className="text-muted-foreground">
            {view.artifacts.artifacts.filter((artifact) => artifact.kind === "screenshot").length}{" "}
            captured screenshot(s)
          </p>
        </section>
      ) : null}

      <section
        className={`${pageStyle.panel} flex flex-wrap items-start justify-between gap-3`}
        aria-busy={busy}
      >
        <div>
          <p className={pageStyle.label}>Source path</p>
          <p className={`${pageStyle.value} [overflow-wrap:anywhere]`}>
            {plan?.SourcePath || "No prepared source"}
          </p>
          {plan ? (
            <div className="flex flex-wrap gap-x-2.5 gap-y-1.5">
              <p className="text-muted-foreground">Duration: {plan.DurationSeconds.toFixed(1)}s</p>
              <p className="text-muted-foreground">Frame rate: {plan.FrameRate.toFixed(3)}</p>
              {plan.DiscType ? (
                <p className="text-muted-foreground">Disc type: {plan.DiscType}</p>
              ) : null}
              {discPlans.length ? (
                <p className="text-muted-foreground">Discs: {discPlans.length}</p>
              ) : null}
            </div>
          ) : null}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            className="ghost"
            type="button"
            onClick={() => void facet.load()}
            disabled={mutationsBlocked}
          >
            {busy ? "Loading..." : "Load suggestions"}
          </button>
          <button
            className="primary"
            type="button"
            onClick={() => void facet.generate("final")}
            disabled={mutationsBlocked || selections.length === 0}
          >
            {busy ? "Capturing..." : "Generate screenshots"}
          </button>
        </div>
      </section>

      {savedTrackerImages.length ? (
        <section className={`${pageStyle.panel} grid gap-3`} aria-busy={busy}>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2>Saved tracker images</h2>
            <p className="text-muted-foreground">
              {savedTrackerImages.length} saved image(s) can be used as screenshots with their
              existing upload URLs.
            </p>
            <button
              className="primary"
              type="button"
              disabled={mutationsBlocked}
              onClick={() => void facet.generate("final", [])}
            >
              Use saved images
            </button>
          </div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-[9px]">
            {savedTrackerImages.map((image, index) => {
              const label = `Saved screenshot ${index + 1} from ${image.TrackerID}`;
              return (
                <div className="grid gap-1.5" key={`${image.TrackerID}-${image.URL}`}>
                  <button
                    className="overflow-hidden rounded-[14px] border border-foreground/10 bg-card/60 p-0 text-left cursor-pointer [&>img]:block [&>img]:w-full"
                    type="button"
                    onClick={() => {
                      setLightboxImage(image.URL);
                      setLightboxAlt(label);
                    }}
                  >
                    <img
                      src={image.PreviewURL || image.URL}
                      alt={label}
                      loading="lazy"
                      referrerPolicy="no-referrer"
                    />
                  </button>
                  <p className="text-muted-foreground">
                    {image.TrackerID} · {image.Host}
                  </p>
                </div>
              );
            })}
          </div>
        </section>
      ) : null}

      <section className={`${pageStyle.panel} grid gap-[9px]`}>
        <details>
          <summary>
            Frame Selection · {selections.length} frame{selections.length === 1 ? "" : "s"}
          </summary>
          <div className="flex flex-wrap items-baseline justify-between gap-[9px] mt-3">
            <p className="text-muted-foreground">
              Adjust timestamps or frame numbers, then preview.
            </p>
          </div>
          {!plan ? (
            <p className="text-muted-foreground">Load suggestions to edit frame selections.</p>
          ) : selections.length === 0 ? (
            <p className="text-muted-foreground">No selections available yet.</p>
          ) : (
            <div className="grid gap-4">
              {selectionGroups.map(({ disc, selections: discSelections }) => (
                <section className="grid gap-2" key={disc.DiscID || "single-disc"}>
                  <h3>{disc.DiscName}</h3>
                  {discSelections.length ? (
                    <div className="grid gap-[9px]">
                      {discSelections.map(({ selection, index }) => (
                        <div
                          className="grid min-w-0 grid-cols-2 items-center gap-2 rounded-xl border border-foreground/10 bg-card/60 px-2.5 py-[9px] sm:grid-cols-[minmax(140px,1fr)_minmax(120px,140px)_minmax(120px,140px)_auto]"
                          key={`sel-${selection.DiscID || "single"}-${selection.Index}`}
                        >
                          <div className="col-span-full sm:col-span-1">
                            <p className={pageStyle.label}>Shot {selection.Index + 1}</p>
                            <p className="text-muted-foreground">
                              Source: {selection.Source || "auto"}
                            </p>
                          </div>
                          <label className="grid min-w-0 gap-1.5 text-sm text-foreground">
                            <span>Seconds</span>
                            <input
                              className={frameInputClass}
                              type="number"
                              aria-label={`${disc.DiscName || "Source"} shot ${selection.Index + 1} seconds`}
                              step="0.1"
                              value={selection.TimestampSeconds}
                              onChange={(event) =>
                                facet.changeSelection(index, {
                                  TimestampSeconds: Number(event.target.value) || 0,
                                })
                              }
                            />
                          </label>
                          <label className="grid min-w-0 gap-1.5 text-sm text-foreground">
                            <span>Frame</span>
                            <input
                              className={frameInputClass}
                              type="number"
                              aria-label={`${disc.DiscName || "Source"} shot ${selection.Index + 1} frame`}
                              step="1"
                              value={selection.Frame}
                              onChange={(event) =>
                                facet.changeSelection(index, {
                                  Frame: Number(event.target.value) || 0,
                                })
                              }
                            />
                          </label>
                          <button
                            className="ghost col-span-full sm:col-span-1"
                            type="button"
                            aria-label={`Preview ${disc.DiscName || "Source"} shot ${selection.Index + 1}`}
                            disabled={mutationsBlocked}
                            onClick={() => void facet.generate("preview", [selection])}
                          >
                            {busy ? "Previewing..." : "Preview"}
                          </button>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="text-muted-foreground">No suggested frames for this disc.</p>
                  )}
                </section>
              ))}
            </div>
          )}
        </details>
      </section>

      {workflowImages.length ? (
        <section className={`${pageStyle.panel} grid gap-3`} aria-busy={busy}>
          <div className="flex flex-wrap items-baseline justify-between gap-[9px]">
            <h2>Generated Screenshots</h2>
            <p className="text-muted-foreground">
              Workflow-owned screenshots ready for description building.
            </p>
            <button
              className="ghost"
              type="button"
              disabled={mutationsBlocked}
              onClick={() => {
                if (globalThis.confirm("Delete all generated screenshots?"))
                  void facet.deleteArtifacts(workflowImages.map((artifact) => artifact.id));
              }}
            >
              Delete all
            </button>
          </div>
          <div className="grid gap-4">
            {workflowImageGroups
              .filter((group) => group.images.length > 0)
              .map(({ disc, images }) => (
                <section className="grid gap-2" key={disc.DiscID || "single-disc"}>
                  <h3>{disc.DiscName}</h3>
                  <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-[9px]">
                    {images.map((artifact, index) => {
                      const selectedIndex = selectedWorkflowImages.findIndex(
                        (selected) => selected.id === artifact.id,
                      );
                      const imageLabel = disc.DiscID
                        ? `${disc.DiscName} screenshot ${index + 1}`
                        : `Screenshot ${index + 1}`;
                      return (
                        <div
                          className="relative grid gap-1.5"
                          key={artifact.id}
                          draggable={artifact.selected && !mutationsBlocked}
                          onDragStart={() => setFinalDragIndex(selectedIndex)}
                          onDragOver={(event) => {
                            if (artifact.selected) event.preventDefault();
                          }}
                          onDrop={(event) => {
                            event.preventDefault();
                            if (!mutationsBlocked && artifact.selected && finalDragIndex !== null)
                              void facet.reorderFinal(finalDragIndex, selectedIndex);
                            setFinalDragIndex(null);
                          }}
                          onDragEnd={() => setFinalDragIndex(null)}
                        >
                          <button
                            className="overflow-hidden rounded-[14px] border border-foreground/10 bg-card/60 p-0 text-left cursor-pointer [&>img]:block [&>img]:w-full"
                            type="button"
                            disabled={!artifact.url}
                            onClick={() => {
                              if (!artifact.url) return;
                              setLightboxImage(artifact.url);
                              setLightboxAlt(imageLabel);
                            }}
                          >
                            {artifact.url ? (
                              <img src={artifact.url} alt={imageLabel} loading="lazy" />
                            ) : (
                              <span className="text-muted-foreground block p-4 text-center text-sm">
                                Image unavailable
                              </span>
                            )}
                          </button>
                          <button
                            className="ghost"
                            type="button"
                            aria-label={`${artifact.selected ? "Unselect" : "Select"} ${imageLabel}`}
                            disabled={mutationsBlocked}
                            onClick={() =>
                              void facet.selectArtifact(artifact.id, !artifact.selected)
                            }
                          >
                            {artifact.selected ? "Unselect" : "Select"}
                          </button>
                          <button
                            className="cursor-pointer rounded-[10px] border border-foreground/10 bg-[var(--destructive-action)] px-2 py-[5px] text-[0.85rem] text-white"
                            type="button"
                            aria-label={`Delete ${imageLabel}`}
                            disabled={mutationsBlocked}
                            onClick={() => void facet.deleteArtifacts([artifact.id])}
                          >
                            Delete
                          </button>
                        </div>
                      );
                    })}
                  </div>
                </section>
              ))}
          </div>
        </section>
      ) : view.workflowMode && view.artifacts ? (
        <section className={pageStyle.panel}>
          <p className="text-muted-foreground">
            No generated screenshots retained for this workflow.
          </p>
        </section>
      ) : null}

      {view.error ? (
        <p className={pageStyle.error} role="alert">
          {view.error}
        </p>
      ) : null}
      {view.mutationBlockedReason ? (
        <p className="text-muted-foreground" role="status">
          {view.mutationBlockedReason}
        </p>
      ) : null}
      {plan?.RequiresManualFrames ? (
        <p className="text-muted-foreground">
          Duration or frame rate is missing. Use saved images or enter manual frame times to capture
          new screenshots.
        </p>
      ) : null}

      <section className={`${pageStyle.panel} screens-preview`}>
        <div className="flex flex-wrap items-baseline justify-between gap-[9px]">
          <h2>Live Preview</h2>
          <p className="text-muted-foreground">
            Scrub the {livePreviewDisc?.DiscName || "source"} timeline and capture the current
            frame.
          </p>
        </div>
        {plan ? (
          <div className="grid gap-[9px]">
            <div className="grid gap-[9px]">
              {discPlans.length > 1 ? (
                <label className="grid min-w-0 gap-1.5 text-sm text-foreground">
                  <span>Disc</span>
                  <Select
                    aria-label="Preview disc"
                    value={livePreviewDisc?.DiscID || ""}
                    onChange={(event) => {
                      setLivePreviewDiscID(event.target.value);
                      setLivePreviewSeconds(0);
                    }}
                  >
                    {discPlans.map((disc) => (
                      <option key={disc.DiscID} value={disc.DiscID}>
                        {disc.DiscName}
                      </option>
                    ))}
                  </Select>
                </label>
              ) : null}
              <label className="grid min-w-0 gap-1.5 text-sm text-foreground">
                <span>Seconds</span>
                <input
                  className={frameInputClass}
                  type="number"
                  step="0.1"
                  value={livePreviewSeconds}
                  onChange={(event) =>
                    setLivePreviewSeconds(clampPreviewSeconds(Number(event.target.value)))
                  }
                />
              </label>
              <label className="grid min-w-0 gap-1.5 text-sm text-foreground">
                <span>Frame</span>
                <input
                  className={frameInputClass}
                  type="number"
                  step="1"
                  value={livePreviewFrame}
                  onChange={(event) => {
                    const frame = Number(event.target.value);
                    setLivePreviewSeconds(
                      previewFrameRate > 0 ? clampPreviewSeconds(frame / previewFrameRate) : 0,
                    );
                  }}
                />
              </label>
              <div className="grid gap-1.5">
                <input
                  aria-label="Preview timeline"
                  type="range"
                  min={0}
                  max={previewDuration}
                  step={previewFrameRate > 0 ? 1 / previewFrameRate : 1}
                  value={clampPreviewSeconds(livePreviewSeconds)}
                  onChange={(event) =>
                    setLivePreviewSeconds(clampPreviewSeconds(Number(event.target.value)))
                  }
                  disabled={previewTimingDisabled}
                />
                <div className="flex flex-wrap gap-x-2.5 gap-y-1.5">
                  <span className="text-muted-foreground">
                    Duration: {previewDuration.toFixed(1)}s
                  </span>
                  <span className="text-muted-foreground">FPS: {previewFrameRate.toFixed(3)}</span>
                </div>
              </div>
              <div className="flex flex-wrap gap-x-2.5 gap-y-1.5">
                <button
                  className="ghost"
                  type="button"
                  onClick={() => stepLivePreview(-1)}
                  disabled={previewTimingDisabled || mutationsBlocked}
                >
                  Prev frame
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => stepLivePreview(1)}
                  disabled={previewTimingDisabled || mutationsBlocked}
                >
                  Next frame
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => void runLivePreviewAt(livePreviewSeconds)}
                  disabled={previewTimingDisabled || mutationsBlocked}
                >
                  {busy ? "Loading..." : "Run preview"}
                </button>
                <button
                  className="primary"
                  type="button"
                  onClick={captureLivePreview}
                  disabled={previewTimingDisabled || mutationsBlocked}
                >
                  {busy ? "Capturing..." : "Capture preview"}
                </button>
              </div>
            </div>
            <div className="grid gap-[9px]">
              {view.previewImage ? (
                <button
                  className="overflow-hidden rounded-[14px] border border-foreground/10 bg-card/60 p-0 text-left cursor-pointer [&>img]:block [&>img]:w-full max-w-md"
                  type="button"
                  onClick={() => {
                    setLightboxImage(view.previewImage);
                    setLightboxAlt("Live preview");
                  }}
                >
                  <img src={view.previewImage} alt="Live preview" />
                </button>
              ) : busy ? (
                <p className="text-muted-foreground">Loading preview...</p>
              ) : (
                <p className="text-muted-foreground">No preview yet.</p>
              )}
            </div>
          </div>
        ) : (
          <p className="text-muted-foreground">Load suggestions to enable live preview.</p>
        )}
      </section>
    </section>
  );
}

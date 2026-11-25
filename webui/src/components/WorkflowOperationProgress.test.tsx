// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { Operation } from "../api/generated/release-workflow";
import { WorkflowOperationProgress } from "./WorkflowOperationProgress";

afterEach(cleanup);

it("shows the active comparison phase and percentages instead of internal work-unit counts", () => {
  const operation = {
    id: "comparison-1",
    workflowId: "workflow-1",
    operation: "media",
    status: "running",
    phase: "comparisons_sampling",
    completed: 500,
    total: 5000,
    progress: 10,
    message: "Scanning comparison videos",
    items: [
      {
        id: "source-1",
        kind: "comparison_window",
        label: "Column 1",
        phase: "comparisons_sampling",
        status: "queued",
        completed: 0,
        total: 1000,
      },
      {
        id: "source-2",
        kind: "comparison_window",
        label: "Column 2",
        phase: "comparisons_sampling",
        status: "running",
        completed: 500,
        total: 1000,
        message: "running — 50%",
      },
    ],
    events: [
      {
        sequence: 1,
        scope: "workflow",
        scopeId: "workflow-1",
        lifecycle: "running",
        message: "Comparisons",
      },
    ],
  } as unknown as Operation;
  const { rerender } = render(<WorkflowOperationProgress operation={operation} />);
  expect(screen.getByText("Scanning comparison videos")).toBeVisible();
  expect(screen.getByText("10%")).toBeVisible();
  expect(screen.getByText("running — 50%")).toBeVisible();
  expect(screen.queryByText("500/5000 complete")).not.toBeInTheDocument();
  const taskTrack = screen.getByRole("progressbar", { name: "Comparison task progress" });
  expect(taskTrack).toHaveAttribute("aria-valuenow", "50");
  const completedItem = {
    ...operation.items![1],
    status: "completed" as const,
    completed: 1000,
    message: "completed — 100%",
  };
  rerender(<WorkflowOperationProgress operation={{ ...operation, items: [completedItem] }} />);
  expect(screen.getByText("completed — 100%")).toBeVisible();
  expect(taskTrack).toHaveAttribute("aria-valuenow", "100");
  rerender(
    <WorkflowOperationProgress
      operation={{
        ...operation,
        items: [{ ...completedItem, status: "skipped", completed: 500, message: "skipped — 0%" }],
      }}
    />,
  );
  expect(screen.getByText("skipped — 0%")).toBeVisible();
  expect(taskTrack).toHaveAttribute("aria-valuenow", "0");
  rerender(
    <WorkflowOperationProgress
      operation={{
        ...operation,
        items: [
          { ...completedItem, id: "cell-1", kind: "comparison_cell", label: "Column 2, row 1" },
          { ...completedItem, status: "skipped", message: "skipped — 0%" },
        ],
      }}
    />,
  );
  expect(screen.getByText("Overall progress only")).toBeVisible();
  expect(screen.queryByRole("progressbar", { name: "Comparison task progress" })).toBeNull();
  rerender(
    <WorkflowOperationProgress
      operation={{
        ...operation,
        phase: "comparisons_refinement",
        message: "Refining comparison frames",
        progress: 55,
        items: [completedItem],
      }}
    />,
  );
  expect(screen.getByText("Refining comparison frames")).toBeVisible();
  expect(screen.getByText("55%")).toBeVisible();
  expect(screen.getByText("Overall progress only")).toBeVisible();
  expect(screen.queryByRole("progressbar", { name: "Comparison task progress" })).toBeNull();
});

it("updates metadata detail in place, keeps stage-order percentages out of fetch counts, and retains partial and canceled outcomes", () => {
  let operation = {
    id: "metadata-1",
    workflowId: "workflow-1",
    operation: "preparation",
    status: "running",
    phase: "media_facts",
    completed: 800,
    total: 1200,
    progress: 67,
    message: "Reading release details",
    items: [
      {
        id: "media_facts",
        kind: "preparation_substep",
        label: "Derive media facts",
        status: "running",
        message: "Read Blu-ray.com release details: 7/100 processed (10s elapsed)",
      },
    ],
    events: Array.from({ length: 40 }, (_, i) => ({
      sequence: i,
      scopeId: "media_facts",
      scope: "item",
      lifecycle: "running",
      message: `Old edition ${i}`,
    })),
  } as unknown as Operation;
  const { rerender } = render(<WorkflowOperationProgress operation={operation} />);
  expect(screen.getAllByRole("status")).toHaveLength(1);
  expect(screen.getByText("Derive media facts")).toBeInTheDocument();
  expect(screen.getByText(/7\/100 processed \(10s elapsed\)/)).toBeInTheDocument();
  expect(screen.queryByText(/Old edition/)).not.toBeInTheDocument();
  expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  operation = {
    ...operation,
    items: [
      { ...operation.items![0], message: "Read Blu-ray.com release details: 8/100 processed" },
    ],
  };
  rerender(<WorkflowOperationProgress operation={{ ...operation }} />);
  expect(screen.queryByText(/7\/100 processed/)).not.toBeInTheDocument();
  expect(screen.getByText(/8\/100 processed/)).toBeInTheDocument();
  operation = {
    ...operation,
    status: "completed",
    items: [
      {
        ...operation.items![0],
        kind: "preparation_phase",
        status: "partial",
        message: "Stage complete with unavailable optional metadata.",
      },
    ],
  };
  rerender(<WorkflowOperationProgress operation={{ ...operation }} />);
  expect(
    screen.getByText("Stage complete with unavailable optional metadata."),
  ).toBeInTheDocument();
  operation = {
    ...operation,
    status: "canceled",
    items: [{ ...operation.items![0], status: "canceled", message: "" }],
    events: [
      ...operation.events!,
      {
        ...operation.events![0],
        sequence: 41,
        scope: "item",
        scopeId: "media_facts",
        lifecycle: "canceled",
        severity: "warn",
        message: "Stage canceled.",
      },
    ],
  };
  rerender(<WorkflowOperationProgress operation={operation} />);
  expect(screen.getByText("Derive media facts")).toBeInTheDocument();
  expect(screen.getAllByText("Stage canceled.")).toHaveLength(1);
  expect(screen.queryByText(/Old edition/)).not.toBeInTheDocument();
  operation = {
    ...operation,
    items: [{ ...operation.items![0], message: "Stage canceled." }],
    events: [],
  };
  rerender(<WorkflowOperationProgress operation={operation} />);
  expect(screen.getByText("Stage canceled.")).toBeInTheDocument();
});

it("suppresses queued tracker placeholders only while a per-tracker subtask is queued or running", () => {
  type OperationItem = NonNullable<Operation["items"]>[number];
  type OperationEvent = NonNullable<Operation["events"]>[number];
  const trackerIDs = ["TL", "BLU"] as const;
  const queuedTrackerItem = (id: string) =>
    ({
      id,
      kind: "tracker",
      label: id,
      phase: "upload_plan",
      status: "queued",
      completed: 0,
      total: trackerIDs.length,
      message: "Tracker upload-plan preparation queued.",
    }) as unknown as OperationItem;
  const queuedTrackerEvent = (sequence: number, id: string) =>
    ({
      sequence,
      scope: "tracker",
      scopeId: id,
      lifecycle: "queued",
      message: "Tracker upload-plan preparation queued.",
    }) as unknown as OperationEvent;
  const rootEvent = {
    sequence: 1,
    scope: "workflow",
    scopeId: "workflow-1",
    lifecycle: "running",
    message: "Preparing torrent",
  } as unknown as OperationEvent;
  const torrentItem = {
    id: "TL:torrent",
    kind: "upload",
    label: "TL",
    phase: "torrent",
    status: "running",
    completed: 0,
    total: 0,
    message: "Preparing torrent",
  } as unknown as OperationItem;
  const torrentEvent = {
    sequence: 4,
    scope: "tracker",
    scopeId: "TL:torrent",
    lifecycle: "running",
    message: "Preparing torrent",
  } as unknown as OperationEvent;
  const queuedItems = trackerIDs.map(queuedTrackerItem);
  const queuedEvents = trackerIDs.map((id, index) => queuedTrackerEvent(index + 2, id));
  const baseOperation = {
    id: "upload-plan-1",
    workflowId: "workflow-1",
    operation: "upload_plan",
    status: "running",
    phase: "torrent",
    completed: 0,
    total: trackerIDs.length,
    progress: 0,
    message: "Preparing torrent",
    items: [...queuedItems, torrentItem],
    events: [rootEvent, ...queuedEvents, torrentEvent],
  } as unknown as Operation;

  const { rerender } = render(<WorkflowOperationProgress operation={baseOperation} />);
  expect(screen.getAllByText("TL")).toHaveLength(1);
  expect(screen.getAllByText("BLU")).toHaveLength(1);
  expect(screen.getAllByText("Tracker upload-plan preparation queued.")).toHaveLength(1);
  expect(screen.getAllByText("Preparing torrent")).toHaveLength(2); // header + TL row

  // Item-only fallback (no events) also avoids duplicate TL rows.
  rerender(<WorkflowOperationProgress operation={{ ...baseOperation, events: [] }} />);
  expect(screen.getAllByText("TL")).toHaveLength(1);
  expect(screen.getAllByText("BLU")).toHaveLength(1);
  expect(screen.getAllByText("Tracker upload-plan preparation queued.")).toHaveLength(1);

  // Once the torrent subtask completes, TL's queued row reappears until upload-plan preparation completes.
  rerender(
    <WorkflowOperationProgress
      operation={{
        ...baseOperation,
        items: [
          ...queuedItems,
          { ...torrentItem, status: "completed", message: "Torrent ready" } as OperationItem,
        ],
        events: [
          rootEvent,
          ...queuedEvents,
          {
            ...torrentEvent,
            lifecycle: "terminal",
            state: "completed",
            disposition: "succeeded",
            message: "Torrent ready",
          } as OperationEvent,
        ],
      }}
    />,
  );
  expect(screen.getAllByText("TL")).toHaveLength(1);
  expect(screen.getAllByText("Tracker upload-plan preparation queued.")).toHaveLength(2);

  // A failed torrent leaves the queued tracker visible in both progress views.
  const failureMessage = "Torrent creation failed";
  const failedItems = [
    ...queuedItems,
    { ...torrentItem, status: "failed", message: failureMessage } as OperationItem,
  ];
  const failedEvents = [
    rootEvent,
    ...queuedEvents,
    {
      ...torrentEvent,
      lifecycle: "terminal",
      state: "failed",
      disposition: "failed",
      severity: "error",
      message: failureMessage,
    } as OperationEvent,
  ];
  for (const events of [failedEvents, []]) {
    rerender(
      <WorkflowOperationProgress operation={{ ...baseOperation, items: failedItems, events }} />,
    );
    expect(screen.getAllByText("Tracker upload-plan preparation queued.")).toHaveLength(2);
    expect(screen.getByText(failureMessage)).toBeVisible();
  }
});

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
        id: "source-2",
        kind: "comparison_source",
        label: "Column 2",
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
  rerender(
    <WorkflowOperationProgress
      operation={{
        ...operation,
        phase: "comparisons_refinement",
        message: "Refining comparison frames",
        progress: 55,
      }}
    />,
  );
  expect(screen.getByText("Refining comparison frames")).toBeVisible();
  expect(screen.getByText("55%")).toBeVisible();
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

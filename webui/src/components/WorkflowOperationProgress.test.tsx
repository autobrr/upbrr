// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { Operation } from "../api/generated/release-workflow";
import { WorkflowOperationProgress } from "./WorkflowOperationProgress";

afterEach(cleanup);

it("updates metadata detail in place, keeps stage-order percentages out of fetch counts, and retains partial outcomes", () => {
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
});

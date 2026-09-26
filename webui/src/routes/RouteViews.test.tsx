// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { RouteViewsContext, routeComponents, type RouteViewContextValue } from "./RouteViews";

vi.mock("../pages/dupe_check", () => ({
  default: () => <div>Live duplicate results</div>,
}));

const DuplicatesRoute = routeComponents.dupes;

function contextFor(
  workflowID: string,
  available: boolean,
  operationStatus: string | null,
  viewStatus: "ready" | "running" = "ready",
  reasonCode = "operation_active",
) {
  return {
    session: {
      navigation: {
        view: {
          access: {
            duplicates: {
              available,
              reason: available ? "" : "Wait for the active operation to finish.",
              reasonCode: available ? undefined : reasonCode,
            },
          },
        },
      },
      workflow: {
        view: {
          status: viewStatus,
          current: {
            workflow: { id: workflowID, status: "active" },
            operation: operationStatus ? { status: operationStatus } : null,
          },
        },
      },
      upload: {
        view: {
          dryRunStatus: "idle",
          uploadStatus: "idle",
          dryRunResult: null,
          result: null,
          submissionExclusions: [],
        },
      },
      identity: { view: { sourcePath: "C:\\media\\Example.Release.2026-GRP.mkv" } },
      duplicates: {},
    },
    trackerUploadItems: [],
    trackerIconSrcByName: {},
    navigateTo: vi.fn(),
  } as unknown as RouteViewContextValue;
}

it("keeps the active workflow view mounted during progress but honors later or different-workflow denial", async () => {
  const show = (
    workflowID: string,
    available: boolean,
    operationStatus: string | null,
    viewStatus: "ready" | "running" = "ready",
    reasonCode?: string,
  ) => (
    <RouteViewsContext.Provider
      value={contextFor(workflowID, available, operationStatus, viewStatus, reasonCode)}
    >
      <DuplicatesRoute />
    </RouteViewsContext.Provider>
  );
  const { rerender } = render(show("workflow-a", true, null));
  expect(await screen.findByText("Live duplicate results")).toBeInTheDocument();

  rerender(show("workflow-a", false, "running", "running"));
  expect(screen.getByText("Live duplicate results")).toBeInTheDocument();
  expect(screen.queryByText("View unavailable")).not.toBeInTheDocument();

  rerender(show("workflow-a", false, "running", "ready"));
  expect(screen.getByText("Live duplicate results")).toBeInTheDocument();

  rerender(show("workflow-a", false, "completed", "running"));
  expect(screen.getByText("Live duplicate results")).toBeInTheDocument();
  expect(screen.queryByText("View unavailable")).not.toBeInTheDocument();

  rerender(show("workflow-a", false, "completed", "ready", "missing_prerequisite"));
  expect(screen.getByText("View unavailable")).toBeInTheDocument();

  rerender(show("workflow-a", false, "running", "running"));
  expect(screen.getByText("View unavailable")).toBeInTheDocument();

  rerender(show("workflow-a", true, null));
  expect(screen.getByText("Live duplicate results")).toBeInTheDocument();

  rerender(show("workflow-a", false, "running", "running", "recovery_actions"));
  expect(screen.getByText("View unavailable")).toBeInTheDocument();

  rerender(show("workflow-b", false, "running", "running"));
  expect(screen.getByText("View unavailable")).toBeInTheDocument();
});

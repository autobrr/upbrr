// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ReleaseRoute } from "../releaseSession/types";
import { RouteViewsContext, routeComponents, type RouteViewContextValue } from "./RouteViews";

vi.mock("../pages/tracker_data", () => ({ default: () => <div>Retained tracker data</div> }));
vi.mock("../pages/dupe_check", () => ({
  default: () => <div>Live duplicate results</div>,
}));
vi.mock("../pages/screenshots", () => ({ default: () => <div>Retained screenshots</div> }));
vi.mock("../pages/menu_images", () => ({ default: () => <div>Retained menu images</div> }));
vi.mock("../pages/upload_images", () => ({ default: () => <div>Retained uploaded images</div> }));
vi.mock("../pages/description_builder", () => ({
  default: () => <div>Retained descriptions</div>,
}));

const DuplicatesRoute = routeComponents.dupes;

afterEach(cleanup);

function contextFor(
  workflowID: string,
  available: boolean,
  operationStatus: string | null,
  viewStatus: "ready" | "running" = "ready",
  reasonCode = "operation_active",
  workflowStatus: "active" | "completed" = "active",
  retainedRoute: ReleaseRoute | "imageHostFailure" | null = null,
) {
  const submissionExclusions =
    retainedRoute === "duplicates" ? [{ trackerId: "HDS", reason: "already_uploaded" }] : [];
  const routeAccess = {
    available,
    reason: available ? "" : "Wait for the active operation to finish.",
    reasonCode: available ? undefined : reasonCode,
  };
  return {
    session: {
      navigation: {
        view: {
          access: {
            trackerData: routeAccess,
            duplicates: routeAccess,
            screenshots: routeAccess,
            menuImages: routeAccess,
            uploadedImages: routeAccess,
            descriptions: routeAccess,
          },
        },
      },
      workflow: {
        view: {
          status: viewStatus,
          current: {
            workflow: { id: workflowID, status: workflowStatus, submissionExclusions },
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
          submissionExclusions,
        },
      },
      input: { view: { trackerData: retainedRoute === "trackerData" ? [{}] : [] } },
      identity: { view: { sourcePath: "C:\\media\\Example.Release.2026-GRP.mkv" } },
      duplicates: { view: { assessment: null } },
      screenshots: {
        view: {
          artifacts:
            retainedRoute === "screenshots" ? { artifacts: [{ kind: "screenshot" }] } : null,
        },
      },
      menuImages: { view: { images: retainedRoute === "menuImages" ? [{}] : [] } },
      uploadedImages: {
        view: {
          uploaded: retainedRoute === "uploadedImages" ? [{}] : [],
          failures: retainedRoute === "imageHostFailure" ? [{}] : [],
          candidates: [],
        },
      },
      descriptions: {
        view: { artifact: retainedRoute === "descriptions" ? { descriptions: [{}] } : null },
      },
    },
    settings: { resolveImageHostLabel: vi.fn() },
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

it.each([
  ["tracker", "trackerData", "Retained tracker data"],
  ["dupes", "duplicates", "Live duplicate results"],
  ["screenshots", "screenshots", "Retained screenshots"],
  ["menu_images", "menuImages", "Retained menu images"],
  ["upload_images", "uploadedImages", "Retained uploaded images"],
  ["upload_images", "imageHostFailure", "Retained uploaded images"],
  ["description_builder", "descriptions", "Retained descriptions"],
] as const)(
  "shows completed %s (%s) only when its data exists",
  async (screenID, route, resultText) => {
    const Route = routeComponents[screenID];
    const show = (retainedRoute: ReleaseRoute | "imageHostFailure" | null) => (
      <RouteViewsContext.Provider
        value={contextFor(
          "workflow-a",
          false,
          null,
          "ready",
          "workflow_complete",
          "completed",
          retainedRoute,
        )}
      >
        <Route />
      </RouteViewsContext.Provider>
    );
    const { rerender } = render(show(null));
    expect(screen.getByText("View unavailable")).toBeInTheDocument();
    rerender(show(route));
    expect(await screen.findByText(resultText)).toBeInTheDocument();
  },
);

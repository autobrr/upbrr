// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createElement } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./app";
import { setAppRequestHandlerForTests } from "./api/client";
import type { MetadataPreview, TrackerCatalog } from "./types";
import { emptyExternalIdentity } from "./utils/canonicalIdentity";
import { sourcePathHistoryStorageKey } from "./utils/inputHistory";
import type { ReleaseWorkflowCurrent } from "./api/generated/release-workflow";

const storedValues = new Map<string, string>();
const localStorageStub: Storage = {
  get length() {
    return storedValues.size;
  },
  clear: () => storedValues.clear(),
  getItem: (key) => storedValues.get(key) ?? null,
  key: (index) => Array.from(storedValues.keys())[index] ?? null,
  removeItem: (key) => storedValues.delete(key),
  setItem: (key, value) => storedValues.set(key, value),
};

beforeEach(() => {
  Object.defineProperty(document.defaultView, "localStorage", {
    configurable: true,
    value: localStorageStub,
  });
});

afterEach(() => {
  cleanup();
  setAppRequestHandlerForTests(null);
  localStorageStub.clear();
});

const trackerCatalog = (): TrackerCatalog => ({
  entries: ["AITHER", "BLU"].map((name) => ({
    name,
    family: "unit3d",
    baseURL: `https://${name.toLowerCase()}.example.invalid`,
    uploadContentMode: "description",
    configured: true,
    default: true,
    fields: [{ key: "APIKey", yamlKey: "api_key", default: "", activation: true }],
  })),
  unsupported: [],
});

const metadataPreview = (sourcePath: string): MetadataPreview => ({
  SourcePath: sourcePath,
  TrackerName: "",
  ReleaseName: "Example.Release.2026.1080p-GRP",
  ReleaseNameOverrides: {},
  Release: { SourcePath: sourcePath, Generation: 1 },
  Identity: { ...emptyExternalIdentity(sourcePath), Generation: 1 },
  Display: { ReleaseName: "Example.Release.2026.1080p-GRP", Providers: [] },
  Bluray: null,
  Diagnostics: [],
  TrackerData: [],
  TrackerRuleFailures: {},
});

describe("App shell", () => {
  it("composes the release session and renders input routing", async () => {
    setAppRequestHandlerForTests(async (method) => {
      if (method === "GetConfig" || method === "GetDefaultConfig") return "{}";
      throw new Error(`unexpected app request: ${method}`);
    });

    render(createElement(App));

    expect(screen.getByRole("heading", { name: "Build Release Name" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Dupe Check" })).toBeDisabled());
  });

  it("opens folders separately from selecting them", async () => {
    const browse = vi.fn(async (path: string) => ({
      currentPath: path || "C:\\media",
      parentPath: path ? "C:\\media" : "C:\\",
      mode: "folder" as const,
      entries:
        path === "C:\\media\\Example"
          ? []
          : [
              {
                name: "Example",
                path: "C:\\media\\Example",
                isDir: true,
                size: 0,
                modifiedAt: "2026-01-01T00:00:00Z",
              },
            ],
    }));
    setAppRequestHandlerForTests(async (method, body) => {
      if (method === "GetConfig" || method === "GetDefaultConfig") return "{}";
      if (method === "BrowseDirectory") return browse((body as { path: string }).path);
      throw new Error(`unexpected app request: ${method}`);
    });

    render(createElement(App));
    screen.getByRole("button", { name: "Browse folder" }).click();

    expect(await screen.findByRole("dialog", { name: "Host browser" })).toBeInTheDocument();
    await waitFor(() => expect(browse).toHaveBeenCalledWith(""));
    expect(await screen.findByText("C:\\media")).toBeInTheDocument();
    expect(await screen.findByText("[DIR] Example")).toBeInTheDocument();
    expect(screen.queryByText("C:\\media\\Example")).not.toBeInTheDocument();
    expect(browse).toHaveBeenCalledOnce();

    screen.getByRole("button", { name: "Open Example" }).click();
    await waitFor(() => expect(browse).toHaveBeenLastCalledWith("C:\\media\\Example"));
    expect(screen.getByLabelText("Source path")).toHaveValue("");
    expect(await screen.findByText("C:\\media\\Example")).toBeInTheDocument();

    screen.getByRole("button", { name: "Select folder" }).click();
    await waitFor(() =>
      expect(screen.getByLabelText("Source path")).toHaveValue("C:\\media\\Example"),
    );
  });

  it("starts with a blank source draft while retaining source history", async () => {
    localStorageStub.setItem(
      sourcePathHistoryStorageKey,
      JSON.stringify([{ path: "C:\\media\\Previously.Used.mkv", mode: "file" }]),
    );
    setAppRequestHandlerForTests(async (method) => {
      if (method === "GetConfig" || method === "GetDefaultConfig") return "{}";
      throw new Error(`unexpected app request: ${method}`);
    });

    render(createElement(App));

    const sourceInput = screen.getByLabelText("Source path");
    fireEvent.focus(sourceInput);
    expect(await screen.findByText("C:\\media\\Previously.Used.mkv")).toBeInTheDocument();
    expect(sourceInput).toHaveValue("");
  });

  it("selects configured default trackers for each fresh release session", async () => {
    let workflowSequence = 0;
    let sourcePath = "";
    const workflows = new Map<string, ReleaseWorkflowCurrent>();
    const workflowCurrent = (
      workflowID: string,
      revision: number,
      selected = false,
    ): ReleaseWorkflowCurrent => ({
      continuation: {
        lifecycle: selected ? "ready" : "waiting",
        disposition: "none",
        refs: {},
        availableGoals: [
          "prepared",
          "trackers_assessed",
          "duplicates_decided",
          "media_ready",
          "descriptions_ready",
          "upload_reviewed",
          "dry_run",
          "uploaded",
        ].map((goal) => ({ goal, available: selected })),
      },
      workflow: {
        id: workflowID,
        revision,
        factInstructions: { id: `${workflowID}-facts`, revision: 1 },
        ...(selected ? { selection: { id: `${workflowID}-selection`, revision } } : {}),
        status: selected ? "active" : "draft",
        createdAt: "2026-07-20T00:00:00Z",
        updatedAt: "2026-07-20T00:00:00Z",
      },
      ...(selected
        ? {
            selection: {
              id: `${workflowID}-selection`,
              workflowId: workflowID,
              revision,
              catalog: { id: `${workflowID}-catalog`, revision },
              runtime: { id: `${workflowID}-runtime`, revision },
              trackerIds: ["AITHER", "BLU"],
              fingerprint: "0".repeat(64),
              createdAt: "2026-07-20T00:00:00Z",
            },
          }
        : {}),
    });
    setAppRequestHandlerForTests(async (method, body) => {
      if (method === "ListTrackerCatalog") return trackerCatalog();
      if (method === "GetDefaultConfig") return "{}";
      if (method === "GetConfig") {
        return JSON.stringify({
          Trackers: {
            DefaultTrackers: ["AITHER", "BLU"],
            Trackers: {
              AITHER: { APIKey: "configured" },
              BLU: { APIKey: "configured" },
            },
          },
        });
      }
      if (method === "ContinueReleaseWorkflow") {
        const command = body as {
          authority?: { workflowId: string };
          intent: { preparation?: { SourcePath: string } };
        };
        if (!command.authority) {
          workflowSequence += 1;
          const created = workflowCurrent(`workflow-${workflowSequence}`, 1);
          workflows.set(created.workflow.id, created);
          return created;
        }
        const retained = workflows.get(command.authority.workflowId);
        if (retained?.release) return retained;
        sourcePath = command.intent.preparation?.SourcePath || "";
        const current = workflowCurrent(command.authority.workflowId, 2);
        const value = metadataPreview(sourcePath);
        const prepared = {
          ...current,
          workflow: {
            ...current.workflow,
            release: { id: `${command.authority.workflowId}-release`, revision: 2 },
            status: "active",
          },
          release: {
            id: `${command.authority.workflowId}-release`,
            workflowId: command.authority.workflowId,
            revision: 2,
            factInstructions: current.workflow.factInstructions,
            release: {
              Generation: value.Release.Generation,
              Source: { SourcePath: sourcePath, Classification: { DiscType: "" }, Entries: [] },
              Naming: { ReleaseName: value.ReleaseName },
              Identity: value.Identity,
              ProviderMetadata: { Bluray: value.Bluray },
            },
            display: { ...value.Display, TrackerData: value.TrackerData },
            diagnostics: value.Diagnostics,
            fingerprint: "1".repeat(64),
            createdAt: "2026-07-20T00:00:00Z",
          },
        } as unknown as ReleaseWorkflowCurrent;
        workflows.set(prepared.workflow.id, prepared);
        return prepared;
      }
      throw new Error(`unexpected app request: ${method}`);
    });

    render(createElement(App));
    const sourceInput = screen.getByLabelText("Source path");
    fireEvent.change(sourceInput, { target: { value: "C:\\media\\Example.Release.2026.mkv" } });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    expect(await screen.findByText("2/2")).toBeInTheDocument();
    const aither = screen.getByRole("checkbox", { name: "AITHER" });
    expect(aither).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "BLU" })).toBeChecked();

    fireEvent.click(aither);
    expect(aither).not.toBeChecked();

    fireEvent.change(sourceInput, { target: { value: "C:\\media\\Next.Release.2026.mkv" } });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(screen.getByRole("checkbox", { name: "AITHER" })).toBeChecked());
  });
});

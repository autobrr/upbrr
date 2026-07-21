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
      if (method === "ListJobs") return [];
      if (method === "GetConfig" || method === "GetDefaultConfig") return "{}";
      throw new Error(`unexpected app request: ${method}`);
    });

    render(createElement(App, { jobOwnerKey: "test-owner" }));

    expect(screen.getByRole("heading", { name: "Build Release Name" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Dupe Check" })).toBeDisabled());
  });

  it("opens the global host-browser dialog from input presentation", async () => {
    const browse = vi.fn(async (_path: string) => ({
      currentPath: "C:\\media",
      parentPath: "C:\\",
      mode: "folder",
      entries: [],
    }));
    setAppRequestHandlerForTests(async (method, body) => {
      if (method === "ListJobs") return [];
      if (method === "GetConfig" || method === "GetDefaultConfig") return "{}";
      if (method === "BrowseDirectory") return browse((body as { path: string }).path);
      throw new Error(`unexpected app request: ${method}`);
    });

    render(createElement(App));
    screen.getByRole("button", { name: "Browse folder" }).click();

    expect(await screen.findByRole("dialog", { name: "Select folder" })).toBeInTheDocument();
    expect(browse).toHaveBeenCalledOnce();
  });

  it("starts with a blank source draft while retaining source history", async () => {
    localStorageStub.setItem(
      sourcePathHistoryStorageKey,
      JSON.stringify([{ path: "C:\\media\\Previously.Used.mkv", mode: "file" }]),
    );
    setAppRequestHandlerForTests(async (method) => {
      if (method === "ListJobs") return [];
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
    setAppRequestHandlerForTests(async (method, body) => {
      if (method === "ListJobs") return [];
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
      if (method === "DetectDiscType") return "";
      if (method === "FetchMetadata") {
        return metadataPreview(String((body as { Path?: string }).Path || ""));
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

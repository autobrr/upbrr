// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createElement } from "react";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./app";
import { setAppRequestHandlerForTests } from "./api/client";

afterEach(() => {
  cleanup();
  setAppRequestHandlerForTests(null);
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
});

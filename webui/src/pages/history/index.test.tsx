// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { HistoryEntry, HistoryOverview } from "../../types";
import { installAppOperationMocks } from "../../test/appRequestMock";
import { emptyExternalIdentity } from "../../utils/canonicalIdentity";
import HistoryPage from ".";

afterEach(cleanup);

const entry = (sourcePath: string, title: string): HistoryEntry => ({
  SourcePath: sourcePath,
  ReleaseTitle: title,
  ReleaseSource: "WEB",
  ReleaseResolution: "1080p",
  MetadataUpdatedAt: "2026-08-05T00:00:00Z",
  LatestUploadStatus: "",
  LatestUploadAt: "",
  RuleFailureCount: 0,
});

const overview = (sourcePath: string, title: string): HistoryOverview => ({
  SourcePath: sourcePath,
  ReleaseTitle: title,
  ReleaseSource: "WEB",
  ReleaseResolution: "1080p",
  MetadataUpdatedAt: "2026-08-05T00:00:00Z",
  LatestUploadStatus: "",
  LatestUploadAt: "",
  StatusLabel: "Stored",
  Metadata: {},
  Release: { SourcePath: sourcePath, Generation: 1 },
  Identity: emptyExternalIdentity(sourcePath),
  Display: { ReleaseName: title, Providers: [] },
  ReleaseNameOverrides: {},
  DescriptionOverride: { SourcePath: sourcePath, GroupKey: "", Description: "", UpdatedAt: "" },
  DescriptionOverrides: [],
  PlaylistSelection: {
    SourcePath: sourcePath,
    SelectedPlaylists: [],
    UseAll: false,
    UpdatedAt: "",
  },
  TrackerMetadata: [],
  TrackerRuleFailures: [],
  Screenshots: [],
  FinalSelections: [],
  UploadedImages: [],
  UploadHistory: [],
});

describe("HistoryPage", () => {
  it("opens a stored source through the shared active-input callback", async () => {
    const sourcePath = "C:\\media\\Stored.Release.2026.1080p-GRP.mkv";
    const onOpenInput = vi.fn(async () => true);
    installAppOperationMocks({
      ListHistory: async () => [entry(sourcePath, "Stored Release 2026")],
      GetHistoryOverview: async () => overview(sourcePath, "Stored Release 2026"),
    });

    render(<HistoryPage onOpenInput={onOpenInput} />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Open input" }));

    expect(onOpenInput).toHaveBeenCalledWith(sourcePath);
  });

  it("reports a rejected open and clears the error on a successful retry", async () => {
    const sourcePath = "C:\\media\\Stored.Release.2026.1080p-GRP.mkv";
    let allowed = false;
    const onOpenInput = vi.fn(async () => allowed);
    installAppOperationMocks({
      ListHistory: async () => [entry(sourcePath, "Stored Release 2026")],
      GetHistoryOverview: async () => overview(sourcePath, "Stored Release 2026"),
    });

    render(<HistoryPage onOpenInput={onOpenInput} />);
    const user = userEvent.setup();
    const button = await screen.findByRole("button", { name: "Open input" });
    await user.click(button);

    const message =
      "Input could not be opened. Check the Input page for errors or recovery actions.";
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(button).toBeEnabled();

    allowed = true;
    await user.click(button);
    expect(screen.queryByText(message)).not.toBeInTheDocument();
    expect(onOpenInput).toHaveBeenCalledTimes(2);
  });

  it.each(["false", "throw"])(
    "ignores an old open's %s result after selection changes",
    async (outcome) => {
      const firstPath = "C:\\media\\First.Release.2026.1080p-GRP.mkv";
      const secondPath = "C:\\media\\Second.Release.2026.1080p-GRP.mkv";
      let finish: () => void = () => undefined;
      const pending = new Promise<boolean>((resolve, reject) => {
        finish = () => {
          if (outcome === "throw") reject(new Error("Old open failed"));
          else resolve(false);
        };
      });
      const onOpenInput = vi.fn(() => pending);
      installAppOperationMocks({
        ListHistory: async () => [
          entry(firstPath, "First Release"),
          entry(secondPath, "Second Release"),
        ],
        GetHistoryOverview: async (path) =>
          overview(path, path === firstPath ? "First Release" : "Second Release"),
      });

      render(<HistoryPage onOpenInput={onOpenInput} />);
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Open input" }));
      await user.click(screen.getByRole("button", { name: /Second Release/ }));
      expect(await screen.findByText(secondPath)).toBeInTheDocument();
      await act(async () => finish());

      expect(
        screen.queryByText(/Input could not be opened|Old open failed/),
      ).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Open input" })).toBeEnabled();
      expect(onOpenInput).toHaveBeenCalledWith(firstPath);
    },
  );

  it("ignores superseded overview responses", async () => {
    const firstPath = "C:\\media\\Example.Release.2026.1080p-GRP.mkv";
    const secondPath = "C:\\media\\Second.Example.2026.1080p-GRP.mkv";
    let resolveFirst: (value: HistoryOverview) => void = () => undefined;
    const firstResponse = new Promise<HistoryOverview>((resolve) => {
      resolveFirst = resolve;
    });
    installAppOperationMocks({
      ListHistory: async () => [
        entry(firstPath, "Example Release 2026"),
        entry(secondPath, "Second Example 2026"),
      ],
      GetHistoryOverview: async (sourcePath: string) => {
        if (sourcePath === firstPath) {
          return firstResponse;
        }
        return overview(secondPath, "Second Example 2026");
      },
    });

    render(<HistoryPage />);
    expect(await screen.findByText("Loading overview...")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Second Example 2026/ }));
    expect(await screen.findByText(secondPath)).toBeInTheDocument();

    await act(async () => {
      resolveFirst(overview(firstPath, "Example Release 2026"));
    });
    expect(screen.getByText(secondPath)).toBeInTheDocument();
    expect(screen.queryByText(firstPath)).not.toBeInTheDocument();
  });

  it("does not retain another release's details after a failed selection", async () => {
    const unavailablePath = "C:\\media\\Example.Release.2026.1080p-GRP.mkv";
    const storedPath = "C:\\media\\Second.Example.2026.1080p-GRP.mkv";
    installAppOperationMocks({
      ListHistory: async () => [
        entry(unavailablePath, "Example Release 2026"),
        entry(storedPath, "Second Example 2026"),
      ],
      GetHistoryOverview: async (sourcePath: string) => {
        if (sourcePath === unavailablePath) {
          throw new Error("stored preparation unavailable");
        }
        return overview(storedPath, "Second Example 2026");
      },
    });

    render(<HistoryPage />);
    expect(await screen.findByText("Error: stored preparation unavailable")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Second Example 2026/ }));
    expect(await screen.findByText(storedPath)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByText("Error: stored preparation unavailable")).not.toBeInTheDocument(),
    );

    await user.click(screen.getByRole("button", { name: /Example Release 2026/ }));
    expect(await screen.findByText("Error: stored preparation unavailable")).toBeInTheDocument();
    expect(screen.queryByText(storedPath)).not.toBeInTheDocument();
  });
});

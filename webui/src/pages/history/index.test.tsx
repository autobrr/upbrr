// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
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

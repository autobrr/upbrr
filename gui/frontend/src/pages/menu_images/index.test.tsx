// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

import type { DVDMenuCaptureResult, DVDMenuCaptureSnapshot, ScreenshotImage } from "../../types";
import MenuImagesPage from "./index";

const menuImage = (path: string, index: number): ScreenshotImage => ({
  Index: index,
  TimestampSeconds: 0,
  Path: path,
  Purpose: "menu",
  Width: 720,
  Height: 480,
  SizeBytes: 1024,
});

const captureResult = (overrides: Partial<DVDMenuCaptureResult> = {}): DVDMenuCaptureResult => ({
  SourcePath: "C:/media/Example/VIDEO_TS",
  Images: [],
  SelectedLanguage: "en",
  Region: 0,
  DiscoveredMenus: 2,
  VisitedStates: 4,
  VisitedButtons: 3,
  MaxItems: 6,
  Complete: true,
  Partial: false,
  Truncated: false,
  Warnings: [],
  Engine: {
    EngineVersion: "phase0a-1",
    SchemaVersion: 1,
    SupportedFeatures: [],
    FFmpegVersion: "8.0",
    FFmpegDVDVideo: true,
    MissingFFmpegOptions: [],
  },
  ...overrides,
});

const captureSnapshot = (status: string, result = captureResult()): DVDMenuCaptureSnapshot => ({
  jobID: "dvd-job-1",
  sourcePath: result.SourcePath,
  status,
  phase: status === "completed" ? "complete" : "capturing",
  message: status === "completed" ? "DVD menu capture finished." : "Rendering menus.",
  discoveredMenus: result.DiscoveredMenus,
  visitedStates: result.VisitedStates,
  visitedButtons: result.VisitedButtons,
  capturedCount: result.Images.length || 2,
  warningCount: result.Warnings.length,
  result,
  error: "",
  startedAt: "2026-07-10T00:00:00Z",
  finishedAt: status === "completed" ? "2026-07-10T00:00:01Z" : "",
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
};

const installBridge = (overrides: Record<string, unknown> = {}) => {
  const app = {
    ListDVDMenuScreenshots: vi.fn(async () => [] as ScreenshotImage[]),
    ReadScreenshotImage: vi.fn(async () => "data:image/png;base64,example"),
    StartDVDMenuCapture: vi.fn(async () => "dvd-job-1"),
    GetDVDMenuCaptureSnapshot: vi.fn(async () => captureSnapshot("completed")),
    CancelDVDMenuCapture: vi.fn(async () => undefined),
    DeleteDVDMenuScreenshot: vi.fn(async () => undefined),
    ImportMenuImages: vi.fn(async () => undefined),
    BrowseImageFiles: vi.fn(async () => [] as string[]),
    ...overrides,
  };
  (globalThis as typeof globalThis & { go?: any }).go = { guiapp: { App: app } };
  return app;
};

const pageProps = (
  overrides: Partial<ComponentProps<typeof MenuImagesPage>> = {},
): ComponentProps<typeof MenuImagesPage> => ({
  path: "C:/media/Example/VIDEO_TS",
  overrides: {},
  nameOverrides: {},
  currentDiscType: "DVD",
  maxMenuItems: 6,
  browseAvailable: true,
  onImagesChanged: vi.fn(),
  onContinue: vi.fn(),
  setLightboxImage: vi.fn(),
  setLightboxAlt: vi.fn(),
  ...overrides,
});

const renderPage = (overrides: Partial<ComponentProps<typeof MenuImagesPage>> = {}) => {
  const props = pageProps(overrides);
  render(<MenuImagesPage {...props} />);
  return props;
};

afterEach(() => {
  cleanup();
  delete (globalThis as typeof globalThis & { go?: any }).go;
});

describe("MenuImagesPage", () => {
  it("loads persisted images and shows DVD-only capture controls with configured maximum", async () => {
    const app = installBridge({
      ListDVDMenuScreenshots: vi.fn(async () => [menuImage("menu-1.png", 0)]),
    });
    const props = renderPage({ maxMenuItems: 8 });

    expect(screen.getByText("Capture up to 8 distinct DVD menu screens.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Capture DVD menus" })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Preview DVD menu 1" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Preview DVD menu 1" }));
    expect(props.setLightboxAlt).toHaveBeenCalledWith("DVD menu 1");
    expect(app.ListDVDMenuScreenshots).toHaveBeenCalledOnce();
  });

  it.each(["BDMV", "HDDVD"])("keeps %s in manual-only mode", (discType) => {
    installBridge();
    renderPage({ currentDiscType: discType });

    expect(screen.queryByRole("button", { name: "Capture DVD menus" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add images" })).toBeInTheDocument();
    expect(
      screen.getByText(new RegExp(`Manual disc menu import remains available for ${discType}`)),
    ).toBeInTheDocument();
  });

  it("shows progress and cancels an active capture", async () => {
    const app = installBridge({
      GetDVDMenuCaptureSnapshot: vi.fn(async () => captureSnapshot("running")),
    });
    renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Capture DVD menus" }));
    expect(await screen.findByText("Rendering menus.")).toBeInTheDocument();
    expect(screen.getByText(/States 4/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(app.CancelDVDMenuCapture).toHaveBeenCalledWith("dvd-job-1"));
    expect(
      screen.getByRole("button", { name: "Capture DVD menus" }).closest("section"),
    ).toHaveAttribute("aria-busy", "true");
  });

  it("cancels an active capture and ignores its late completion after a source change", async () => {
    const oldPath = "C:/media/Example.Release.2026/VIDEO_TS";
    const newPath = "C:/media/Example.Release.2027/VIDEO_TS";
    const pendingSnapshot = deferred<DVDMenuCaptureSnapshot>();
    const app = installBridge({
      StartDVDMenuCapture: vi.fn(async () => "old-dvd-job"),
      GetDVDMenuCaptureSnapshot: vi.fn(() => pendingSnapshot.promise),
    });
    const onImagesChanged = vi.fn();
    const props = pageProps({ path: oldPath, onImagesChanged });
    const view = render(<MenuImagesPage {...props} />);

    fireEvent.click(screen.getByRole("button", { name: "Capture DVD menus" }));
    await waitFor(() => expect(app.GetDVDMenuCaptureSnapshot).toHaveBeenCalledWith("old-dvd-job"));

    view.rerender(<MenuImagesPage {...props} path={newPath} />);
    await waitFor(() => expect(app.CancelDVDMenuCapture).toHaveBeenCalledWith("old-dvd-job"));

    await act(async () => {
      pendingSnapshot.resolve(captureSnapshot("completed", captureResult({ SourcePath: oldPath })));
      await pendingSnapshot.promise;
    });

    expect(screen.queryByText("DVD menu capture finished.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Capture DVD menus" })).toBeEnabled();
    expect(onImagesChanged).not.toHaveBeenCalled();
  });

  it("gates duplicate starts synchronously and clears the pending state on rejection", async () => {
    const pendingStart = deferred<string>();
    const app = installBridge({
      StartDVDMenuCapture: vi.fn(() => pendingStart.promise),
    });
    renderPage();

    const captureButton = screen.getByRole("button", { name: "Capture DVD menus" });
    fireEvent.click(captureButton);
    fireEvent.click(captureButton);

    expect(app.StartDVDMenuCapture).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Starting..." })).toBeDisabled();

    await act(async () => {
      pendingStart.reject(new Error("DVD capture start failed"));
      await pendingStart.promise.catch(() => undefined);
    });

    expect(await screen.findByRole("alert")).toHaveTextContent("DVD capture start failed");
    expect(screen.getByRole("button", { name: "Capture DVD menus" })).toBeEnabled();
  });

  it.each([
    {
      name: "partial",
      result: captureResult({
        Complete: false,
        Partial: true,
        Warnings: [{ Code: "frame_decode", Message: "One menu could not be rendered." }],
      }),
      text: "Captured 2 DVD menu images.",
    },
    {
      name: "truncated",
      result: captureResult({ Complete: false, Truncated: true, MaxItems: 2 }),
      text: "Maximum reached",
    },
  ])("shows $name completion state without navigating", async ({ result, text }) => {
    installBridge({
      GetDVDMenuCaptureSnapshot: vi.fn(async () => captureSnapshot("completed", result)),
    });
    const props = renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Capture DVD menus" }));
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(props.onImagesChanged).toHaveBeenCalledOnce();
    expect(props.onContinue).not.toHaveBeenCalled();
  });

  it("hides internal navigation warnings while keeping basic capture feedback", async () => {
    const hiddenWarnings = [
      {
        Code: "unsupported_post_link",
        Message: "menu post-command target was not resolved",
      },
      {
        Code: "structural_state",
        Message: "structural menu candidate could not be classified",
      },
      {
        Code: "structural_only",
        Message: "visible menu was not reached through navigation",
      },
      {
        Code: "nav_scan_limit",
        Message: "menu NAV/SPU sector scan limit reached",
      },
      {
        Code: "structural_discovery",
        Message:
          "Some menu screens were found through structural inventory rather than reachable navigation.",
      },
    ];
    installBridge({
      GetDVDMenuCaptureSnapshot: vi.fn(async () =>
        captureSnapshot(
          "completed",
          captureResult({ Complete: false, Partial: true, Warnings: hiddenWarnings }),
        ),
      ),
    });
    renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Capture DVD menus" }));
    expect(await screen.findByText("Captured 2 DVD menu images.")).toBeInTheDocument();
    for (const warning of hiddenWarnings) {
      expect(screen.queryByText(warning.Message)).not.toBeInTheDocument();
    }
    expect(screen.queryByText(/Warnings \d+/)).not.toBeInTheDocument();
  });

  it("shows hard capture failures in an alert region", async () => {
    installBridge({
      StartDVDMenuCapture: vi.fn(async () => {
        throw new Error("Compatible FFmpeg DVD menu support is required");
      }),
    });
    renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Capture DVD menus" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Compatible FFmpeg DVD menu support is required",
    );
  });

  it("imports without automatic navigation and exposes explicit continue", async () => {
    const app = installBridge({
      BrowseImageFiles: vi.fn(async () => ["C:/images/menu.png"]),
    });
    const props = renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Add images" }));
    expect(await screen.findByText("C:/images/menu.png")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Import images" }));
    await waitFor(() => expect(app.ImportMenuImages).toHaveBeenCalledOnce());
    expect(props.onImagesChanged).toHaveBeenCalledOnce();
    expect(props.onContinue).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Continue to Upload Images" }));
    expect(props.onContinue).toHaveBeenCalledOnce();
  });

  it("removes one persisted image, preserves order, and focuses the next removal", async () => {
    let persisted = [menuImage("menu-1.png", 0), menuImage("menu-2.png", 1)];
    const app = installBridge({
      ListDVDMenuScreenshots: vi.fn(async () => persisted),
      DeleteDVDMenuScreenshot: vi.fn(async (_path, _overrides, _nameOverrides, imagePath) => {
        persisted = persisted.filter((image) => image.Path !== imagePath);
      }),
    });
    const props = renderPage();

    expect(await screen.findByRole("button", { name: "Remove DVD menu 2" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove DVD menu 1" }));
    await waitFor(() => expect(app.DeleteDVDMenuScreenshot).toHaveBeenCalledOnce());
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Remove DVD menu 1" })).toHaveFocus(),
    );
    expect(screen.queryByRole("button", { name: "Remove DVD menu 2" })).not.toBeInTheDocument();
    expect(props.onImagesChanged).toHaveBeenCalledOnce();
  });
});
